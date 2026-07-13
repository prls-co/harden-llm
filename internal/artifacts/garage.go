// Package artifacts contains the private Garage-backed ArtifactStore adapter.
package artifacts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	hardenllm "github.com/prls-co/harden-llm"
)

const (
	defaultOperationTimeout = 2 * time.Second
	maximumOperationTimeout = 30 * time.Second
	defaultPresignTTL       = 5 * time.Minute
	maximumPresignTTL       = 5 * time.Minute
	maximumArtifactBytes    = 16 << 20
)

// Kind classifies artifact-store failures without exposing endpoint details.
type Kind string

const (
	KindInvalid      Kind = "invalid"
	KindNotFound     Kind = "not_found"
	KindConflict     Kind = "conflict"
	KindUnauthorized Kind = "unauthorized"
	KindTimeout      Kind = "timeout"
	KindUnavailable  Kind = "unavailable"
	KindIntegrity    Kind = "integrity"
)

// Error is a bounded, credential-safe artifact failure.
type Error struct {
	Operation string
	Kind      Kind
	err       error
}

func (err *Error) Error() string {
	if err == nil {
		return "artifacts: operation failed"
	}
	return fmt.Sprintf("artifacts: %s %s", err.Operation, err.Kind)
}

func (err *Error) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.err
}

// IsKind reports whether err has the requested safe classification.
func IsKind(err error, kind Kind) bool {
	var artifactError *Error
	return errors.As(err, &artifactError) && artifactError.Kind == kind
}

// Config defines one private Garage bucket and its client-reachable presign origin.
type Config struct {
	Endpoint         string
	ExternalEndpoint string
	Bucket           string
	Region           string
	AccessKeyID      string
	SecretAccessKey  string
	OperationTimeout time.Duration
	MaxPresignTTL    time.Duration
	HTTPClient       aws.HTTPClient
}

// GarageStore is the sole S3-compatible artifact adapter.
type GarageStore struct {
	bucket           string
	operationTimeout time.Duration
	maxPresignTTL    time.Duration
	client           *s3.Client
	presigner        *s3.PresignClient
	keyLocks         [64]sync.Mutex
}

// ScopedStore confines every operation to one owner-derived object prefix.
type ScopedStore struct {
	store  *GarageStore
	prefix string
}

// NewGarage validates configuration and constructs bounded path-style clients.
func NewGarage(config Config) (*GarageStore, error) {
	endpoint, err := validateEndpoint(config.Endpoint)
	if err != nil {
		return nil, &Error{Operation: "configure", Kind: KindInvalid, err: err}
	}
	external := config.ExternalEndpoint
	if strings.TrimSpace(external) == "" {
		external = config.Endpoint
	}
	externalEndpoint, err := validateEndpoint(external)
	if err != nil {
		return nil, &Error{Operation: "configure", Kind: KindInvalid, err: err}
	}
	if !validBucket(config.Bucket) || strings.TrimSpace(config.Region) == "" || len(config.Region) > 64 {
		return nil, &Error{Operation: "configure", Kind: KindInvalid, err: errors.New("invalid bucket or region")}
	}
	if strings.TrimSpace(config.AccessKeyID) == "" || strings.TrimSpace(config.SecretAccessKey) == "" {
		return nil, &Error{Operation: "configure", Kind: KindInvalid, err: errors.New("bucket credentials are required")}
	}
	operationTimeout := config.OperationTimeout
	if operationTimeout == 0 {
		operationTimeout = defaultOperationTimeout
	}
	if operationTimeout < time.Millisecond || operationTimeout > maximumOperationTimeout {
		return nil, &Error{Operation: "configure", Kind: KindInvalid, err: errors.New("invalid operation timeout")}
	}
	maxPresignTTL := config.MaxPresignTTL
	if maxPresignTTL == 0 {
		maxPresignTTL = defaultPresignTTL
	}
	if maxPresignTTL < time.Second || maxPresignTTL > maximumPresignTTL {
		return nil, &Error{Operation: "configure", Kind: KindInvalid, err: errors.New("invalid presign TTL")}
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = safeHTTPClient(operationTimeout)
	}
	credentials := aws.NewCredentialsCache(aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: config.AccessKeyID, SecretAccessKey: config.SecretAccessKey, Source: "harden-llm-garage"}, nil
	}))
	baseConfig := aws.Config{
		Region: config.Region, Credentials: credentials, HTTPClient: httpClient,
		RetryMaxAttempts: 2, RetryMode: aws.RetryModeStandard,
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}
	client := newClient(baseConfig, endpoint.String())
	externalClient := newClient(baseConfig, externalEndpoint.String())
	return &GarageStore{
		bucket: config.Bucket, operationTimeout: operationTimeout, maxPresignTTL: maxPresignTTL,
		client: client, presigner: s3.NewPresignClient(externalClient),
	}, nil
}

func newClient(config aws.Config, endpoint string) *s3.Client {
	return s3.NewFromConfig(config, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(endpoint)
		options.UsePathStyle = true
		options.DisableMultiRegionAccessPoints = true
		options.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		options.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
}

// Scoped returns an adapter that rejects every key outside prefix.
func (store *GarageStore) Scoped(prefix string) (*ScopedStore, error) {
	if store == nil {
		return nil, &Error{Operation: "scope", Kind: KindInvalid, err: errors.New("store is not initialized")}
	}
	trimmed := strings.TrimSuffix(strings.TrimSpace(prefix), "/")
	if err := validateKey(trimmed+"/scope.json", ""); err != nil {
		return nil, &Error{Operation: "scope", Kind: KindInvalid, err: err}
	}
	return &ScopedStore{store: store, prefix: trimmed + "/"}, nil
}

func (store *ScopedStore) Put(ctx context.Context, key string, content []byte, contentType string) (hardenllm.ArtifactRef, error) {
	if store == nil || store.store == nil {
		return hardenllm.ArtifactRef{}, &Error{Operation: "put", Kind: KindInvalid, err: errors.New("store is not initialized")}
	}
	return store.store.put(ctx, key, content, contentType, store.prefix)
}

func (store *GarageStore) Put(ctx context.Context, key string, content []byte, contentType string) (hardenllm.ArtifactRef, error) {
	return store.put(ctx, key, content, contentType, "")
}

func (store *GarageStore) put(ctx context.Context, key string, content []byte, contentType, prefix string) (hardenllm.ArtifactRef, error) {
	if store == nil || store.client == nil {
		return hardenllm.ArtifactRef{}, &Error{Operation: "put", Kind: KindInvalid, err: errors.New("store is not initialized")}
	}
	if err := validateKey(key, prefix); err != nil {
		return hardenllm.ArtifactRef{}, &Error{Operation: "put", Kind: KindInvalid, err: err}
	}
	if contentType != "application/json" || len(content) == 0 || len(content) > maximumArtifactBytes || !json.Valid(content) {
		return hardenllm.ArtifactRef{}, &Error{Operation: "put", Kind: KindInvalid, err: errors.New("artifact must be bounded valid JSON")}
	}
	digest := sha256.Sum256(content)
	digestText := hex.EncodeToString(digest[:])
	keyDigest := sha256.Sum256([]byte(key))
	keyLock := &store.keyLocks[int(keyDigest[0])%len(store.keyLocks)]
	keyLock.Lock()
	defer keyLock.Unlock()
	requestContext, cancel := store.operationContext(ctx)
	defer cancel()
	_, headErr := store.client.HeadObject(requestContext, &s3.HeadObjectInput{Bucket: aws.String(store.bucket), Key: aws.String(key)})
	if headErr == nil {
		return hardenllm.ArtifactRef{}, &Error{Operation: "put", Kind: KindConflict, err: errors.New("artifact key already exists")}
	}
	if classified := classify("put", requestContext, headErr); !IsKind(classified, KindNotFound) {
		return hardenllm.ArtifactRef{}, classified
	}
	_, err := store.client.PutObject(requestContext, &s3.PutObjectInput{
		Bucket: aws.String(store.bucket), Key: aws.String(key), Body: bytes.NewReader(content),
		ContentLength: aws.Int64(int64(len(content))), ContentType: aws.String(contentType),
		IfNoneMatch: aws.String("*"), Metadata: map[string]string{"sha256": digestText},
	})
	if err != nil {
		return hardenllm.ArtifactRef{}, classify("put", requestContext, err)
	}
	return hardenllm.ArtifactRef{Key: key, SHA256: digestText, SizeBytes: int64(len(content)), ContentType: contentType}, nil
}

func (store *ScopedStore) Get(ctx context.Context, key string) ([]byte, hardenllm.ArtifactRef, error) {
	if store == nil || store.store == nil {
		return nil, hardenllm.ArtifactRef{}, &Error{Operation: "get", Kind: KindInvalid, err: errors.New("store is not initialized")}
	}
	return store.store.get(ctx, key, store.prefix)
}

// Get loads and integrity-checks exact artifact bytes. Gateway callers authorize
// through Postgres before using this internal method.
func (store *GarageStore) Get(ctx context.Context, key string) ([]byte, hardenllm.ArtifactRef, error) {
	return store.get(ctx, key, "")
}

func (store *GarageStore) get(ctx context.Context, key, prefix string) ([]byte, hardenllm.ArtifactRef, error) {
	if store == nil || store.client == nil {
		return nil, hardenllm.ArtifactRef{}, &Error{Operation: "get", Kind: KindInvalid, err: errors.New("store is not initialized")}
	}
	if err := validateKey(key, prefix); err != nil {
		return nil, hardenllm.ArtifactRef{}, &Error{Operation: "get", Kind: KindInvalid, err: err}
	}
	requestContext, cancel := store.operationContext(ctx)
	defer cancel()
	output, err := store.client.GetObject(requestContext, &s3.GetObjectInput{Bucket: aws.String(store.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, hardenllm.ArtifactRef{}, classify("get", requestContext, err)
	}
	defer output.Body.Close()
	content, err := io.ReadAll(io.LimitReader(output.Body, maximumArtifactBytes+1))
	if err != nil {
		return nil, hardenllm.ArtifactRef{}, classify("get", requestContext, err)
	}
	if len(content) == 0 || len(content) > maximumArtifactBytes || !json.Valid(content) {
		return nil, hardenllm.ArtifactRef{}, &Error{Operation: "get", Kind: KindIntegrity, err: errors.New("stored artifact is invalid")}
	}
	contentType := aws.ToString(output.ContentType)
	if contentType != "application/json" {
		return nil, hardenllm.ArtifactRef{}, &Error{Operation: "get", Kind: KindIntegrity, err: errors.New("stored content type is invalid")}
	}
	digest := sha256.Sum256(content)
	digestText := hex.EncodeToString(digest[:])
	metadataDigest := ""
	for name, value := range output.Metadata {
		if strings.EqualFold(name, "sha256") {
			metadataDigest = strings.ToLower(value)
		}
	}
	if metadataDigest == "" || metadataDigest != digestText || (output.ContentLength != nil && *output.ContentLength != int64(len(content))) {
		return nil, hardenllm.ArtifactRef{}, &Error{Operation: "get", Kind: KindIntegrity, err: errors.New("stored artifact metadata does not match bytes")}
	}
	return content, hardenllm.ArtifactRef{Key: key, SHA256: digestText, SizeBytes: int64(len(content)), ContentType: contentType}, nil
}

func (store *ScopedStore) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if store == nil || store.store == nil {
		return "", &Error{Operation: "presign", Kind: KindInvalid, err: errors.New("store is not initialized")}
	}
	return store.store.presignGet(ctx, key, ttl, store.prefix)
}

func (store *GarageStore) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return store.presignGet(ctx, key, ttl, "")
}

func (store *GarageStore) presignGet(ctx context.Context, key string, ttl time.Duration, prefix string) (string, error) {
	if store == nil || store.presigner == nil {
		return "", &Error{Operation: "presign", Kind: KindInvalid, err: errors.New("store is not initialized")}
	}
	if err := validateKey(key, prefix); err != nil {
		return "", &Error{Operation: "presign", Kind: KindInvalid, err: err}
	}
	if ttl < time.Second || ttl > store.maxPresignTTL || ttl > maximumPresignTTL {
		return "", &Error{Operation: "presign", Kind: KindInvalid, err: errors.New("presign TTL is outside the supported range")}
	}
	requestContext, cancel := store.operationContext(ctx)
	defer cancel()
	request, err := store.presigner.PresignGetObject(requestContext, &s3.GetObjectInput{
		Bucket: aws.String(store.bucket), Key: aws.String(key),
	}, func(options *s3.PresignOptions) { options.Expires = ttl })
	if err != nil {
		return "", classify("presign", requestContext, err)
	}
	return request.URL, nil
}

// Ready verifies access to the configured private bucket.
func (store *GarageStore) Ready(ctx context.Context) error {
	if store == nil || store.client == nil {
		return &Error{Operation: "ready", Kind: KindInvalid, err: errors.New("store is not initialized")}
	}
	requestContext, cancel := store.operationContext(ctx)
	defer cancel()
	_, err := store.client.HeadBucket(requestContext, &s3.HeadBucketInput{Bucket: aws.String(store.bucket)})
	if err != nil {
		return classify("ready", requestContext, err)
	}
	return nil
}

func (store *GarageStore) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, store.operationTimeout)
}

func validateEndpoint(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("invalid S3 endpoint")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("S3 endpoint must be an origin")
	}
	parsed.Path = ""
	return parsed, nil
}

func validBucket(value string) bool {
	if len(value) < 3 || len(value) > 63 || strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' && character != '.' {
			return false
		}
	}
	return !strings.Contains(value, "..")
}

func validateKey(key, prefix string) error {
	if key == "" || len(key) > 768 || !utf8.ValidString(key) || path.IsAbs(key) || path.Clean(key) != key ||
		strings.ContainsAny(key, "\\?#\x00\r\n") || strings.Contains(key, "//") || !strings.HasSuffix(key, ".json") {
		return errors.New("unsafe artifact key")
	}
	if prefix != "" && !strings.HasPrefix(key, prefix) {
		return errors.New("artifact key is outside owner prefix")
	}
	return nil
}

func safeHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	return &http.Client{Transport: &http.Transport{
		Proxy: nil, DialContext: dialer.DialContext, ForceAttemptHTTP2: true,
		MaxIdleConns: 32, MaxIdleConnsPerHost: 16, IdleConnTimeout: 90 * time.Second,
		TLSHandshakeTimeout: timeout, ResponseHeaderTimeout: timeout,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}}
}

func classify(operation string, ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		kind := KindTimeout
		if errors.Is(ctxErr, context.Canceled) {
			kind = KindUnavailable
		}
		return &Error{Operation: operation, Kind: kind, err: ctxErr}
	}
	kind := KindUnavailable
	var responseError *smithyhttp.ResponseError
	if errors.As(err, &responseError) {
		switch responseError.HTTPStatusCode() {
		case http.StatusNotFound:
			kind = KindNotFound
		case http.StatusPreconditionFailed, http.StatusConflict:
			kind = KindConflict
		case http.StatusUnauthorized, http.StatusForbidden:
			kind = KindUnauthorized
		}
	}
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		switch strings.ToLower(apiError.ErrorCode()) {
		case "nosuchkey", "notfound", "no_such_key":
			kind = KindNotFound
		case "preconditionfailed", "conditionalrequestconflict":
			kind = KindConflict
		case "accessdenied", "invalidaccesskeyid", "signaturedoesnotmatch":
			kind = KindUnauthorized
		}
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		kind = KindTimeout
	}
	return &Error{Operation: operation, Kind: kind, err: err}
}
