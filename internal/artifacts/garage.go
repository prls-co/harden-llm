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
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	hardenllm "github.com/prls-co/harden-llm"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
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
	TracerProvider   trace.TracerProvider
	MeterProvider    metric.MeterProvider
}

// GarageStore is the sole S3-compatible artifact adapter.
type GarageStore struct {
	bucket           string
	operationTimeout time.Duration
	maxPresignTTL    time.Duration
	client           *s3.Client
	presigner        *s3.PresignClient
	telemetry        *storeTelemetry
	keyLocks         [64]sync.Mutex
}

// ScopedStore confines every operation to one owner-derived object prefix.
type ScopedStore struct {
	store  *GarageStore
	prefix string
}

// InventoryObject is the minimum redacted S3 fact needed to compare Garage
// ownership with PostgreSQL references. Object bodies and user identifiers are
// never loaded by inventory.
type InventoryObject struct {
	Key          string
	SizeBytes    int64
	LastModified time.Time
}

type InventoryPage struct {
	Objects           []InventoryObject
	ContinuationToken string
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
	telemetry, err := newStoreTelemetry(config.TracerProvider, config.MeterProvider)
	if err != nil {
		return nil, &Error{Operation: "configure", Kind: KindInvalid, err: err}
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
		client: client, presigner: s3.NewPresignClient(externalClient), telemetry: telemetry,
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

func (store *ScopedStore) Inspect(ctx context.Context, key string) (hardenllm.ArtifactRef, bool, error) {
	if store == nil || store.store == nil {
		return hardenllm.ArtifactRef{}, false, &Error{Operation: "head", Kind: KindInvalid, err: errors.New("store is not initialized")}
	}
	return store.store.inspect(ctx, key, store.prefix)
}

func (store *GarageStore) Inspect(ctx context.Context, key string) (hardenllm.ArtifactRef, bool, error) {
	return store.inspect(ctx, key, "")
}

// Inventory lists one bounded page below an exact artifact prefix. It is an
// administrative read path for conservative cross-store audits, not a product
// lookup or deletion API.
func (store *GarageStore) Inventory(ctx context.Context, prefix, continuationToken string, limit int32) (page InventoryPage, err error) {
	if store == nil || store.client == nil {
		return InventoryPage{}, &Error{Operation: "list", Kind: KindInvalid, err: errors.New("store is not initialized")}
	}
	if limit < 1 || limit > 1000 || len(continuationToken) > 4096 || !validInventoryPrefix(prefix) {
		return InventoryPage{}, &Error{Operation: "list", Kind: KindInvalid, err: errors.New("invalid inventory page")}
	}
	ctx, endOperation := store.telemetry.Start(ctx, "list")
	defer func() { endOperation(err) }()
	requestContext, cancel := store.operationContext(ctx)
	defer cancel()
	input := &s3.ListObjectsV2Input{Bucket: aws.String(store.bucket), Prefix: aws.String(prefix), MaxKeys: aws.Int32(limit)}
	if continuationToken != "" {
		input.ContinuationToken = aws.String(continuationToken)
	}
	output, err := store.client.ListObjectsV2(requestContext, input)
	if err != nil {
		return InventoryPage{}, classify("list", requestContext, err)
	}
	page.Objects = make([]InventoryObject, 0, len(output.Contents))
	for _, object := range output.Contents {
		key := aws.ToString(object.Key)
		if object.Size == nil || *object.Size <= 0 || object.LastModified == nil || validateKey(key, prefix) != nil {
			return InventoryPage{}, &Error{Operation: "list", Kind: KindIntegrity, err: errors.New("inventory object metadata is invalid")}
		}
		page.Objects = append(page.Objects, InventoryObject{Key: key, SizeBytes: *object.Size, LastModified: object.LastModified.UTC()})
	}
	if aws.ToBool(output.IsTruncated) {
		page.ContinuationToken = aws.ToString(output.NextContinuationToken)
		if page.ContinuationToken == "" {
			return InventoryPage{}, &Error{Operation: "list", Kind: KindIntegrity, err: errors.New("inventory continuation token is missing")}
		}
	}
	return page, nil
}

func (store *GarageStore) put(ctx context.Context, key string, content []byte, contentType, prefix string) (reference hardenllm.ArtifactRef, err error) {
	if store == nil || store.client == nil {
		return hardenllm.ArtifactRef{}, &Error{Operation: "put", Kind: KindInvalid, err: errors.New("store is not initialized")}
	}
	if err := validateKey(key, prefix); err != nil {
		return hardenllm.ArtifactRef{}, &Error{Operation: "put", Kind: KindInvalid, err: err}
	}
	if contentType != "application/json" || len(content) == 0 || len(content) > maximumArtifactBytes || !json.Valid(content) {
		return hardenllm.ArtifactRef{}, &Error{Operation: "put", Kind: KindInvalid, err: errors.New("artifact must be bounded valid JSON")}
	}
	ctx, endOperation := store.telemetry.Start(ctx, "put")
	defer func() { endOperation(err) }()
	digest := sha256.Sum256(content)
	digestText := hex.EncodeToString(digest[:])
	keyDigest := sha256.Sum256([]byte(key))
	keyLock := &store.keyLocks[int(keyDigest[0])%len(store.keyLocks)]
	keyLock.Lock()
	defer keyLock.Unlock()
	requestContext, cancel := store.operationContext(ctx)
	defer cancel()
	existing, found, inspectErr := store.inspect(requestContext, key, prefix)
	if inspectErr != nil {
		return hardenllm.ArtifactRef{}, inspectErr
	}
	if found {
		if existing.SHA256 == digestText && existing.SizeBytes == int64(len(content)) && existing.ContentType == contentType {
			return existing, nil
		}
		return hardenllm.ArtifactRef{}, &Error{Operation: "put", Kind: KindConflict, err: errors.New("artifact key contains different content")}
	}
	_, err = store.client.PutObject(requestContext, &s3.PutObjectInput{
		Bucket: aws.String(store.bucket), Key: aws.String(key), Body: bytes.NewReader(content),
		ContentLength: aws.Int64(int64(len(content))), ContentType: aws.String(contentType),
		IfNoneMatch: aws.String("*"), Metadata: map[string]string{"sha256": digestText},
	})
	if err != nil {
		putErr := classify("put", requestContext, err)
		if existing, found, inspectErr := store.inspect(context.WithoutCancel(ctx), key, prefix); inspectErr == nil && found &&
			existing.SHA256 == digestText && existing.SizeBytes == int64(len(content)) && existing.ContentType == contentType {
			return existing, nil
		}
		return hardenllm.ArtifactRef{}, putErr
	}
	return hardenllm.ArtifactRef{Key: key, SHA256: digestText, SizeBytes: int64(len(content)), ContentType: contentType}, nil
}

func (store *GarageStore) inspect(ctx context.Context, key, prefix string) (reference hardenllm.ArtifactRef, found bool, err error) {
	if store == nil || store.client == nil {
		return hardenllm.ArtifactRef{}, false, &Error{Operation: "head", Kind: KindInvalid, err: errors.New("store is not initialized")}
	}
	if err := validateKey(key, prefix); err != nil {
		return hardenllm.ArtifactRef{}, false, &Error{Operation: "head", Kind: KindInvalid, err: err}
	}
	requestContext, cancel := store.operationContext(ctx)
	defer cancel()
	output, err := store.client.HeadObject(requestContext, &s3.HeadObjectInput{Bucket: aws.String(store.bucket), Key: aws.String(key)})
	if err != nil {
		classified := classify("head", requestContext, err)
		if IsKind(classified, KindNotFound) {
			return hardenllm.ArtifactRef{}, false, nil
		}
		return hardenllm.ArtifactRef{}, false, classified
	}
	digest := ""
	for name, value := range output.Metadata {
		if strings.EqualFold(name, "sha256") {
			digest = strings.ToLower(value)
		}
	}
	if output.ContentLength == nil || *output.ContentLength <= 0 || aws.ToString(output.ContentType) != "application/json" || len(digest) != 64 {
		return hardenllm.ArtifactRef{}, true, &Error{Operation: "head", Kind: KindIntegrity, err: errors.New("stored artifact metadata is invalid")}
	}
	return hardenllm.ArtifactRef{Key: key, SHA256: digest, SizeBytes: *output.ContentLength, ContentType: aws.ToString(output.ContentType)}, true, nil
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

func (store *GarageStore) get(ctx context.Context, key, prefix string) (content []byte, reference hardenllm.ArtifactRef, err error) {
	if store == nil || store.client == nil {
		return nil, hardenllm.ArtifactRef{}, &Error{Operation: "get", Kind: KindInvalid, err: errors.New("store is not initialized")}
	}
	if err := validateKey(key, prefix); err != nil {
		return nil, hardenllm.ArtifactRef{}, &Error{Operation: "get", Kind: KindInvalid, err: err}
	}
	ctx, endOperation := store.telemetry.Start(ctx, "get")
	defer func() { endOperation(err) }()
	requestContext, cancel := store.operationContext(ctx)
	defer cancel()
	output, err := store.client.GetObject(requestContext, &s3.GetObjectInput{Bucket: aws.String(store.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, hardenllm.ArtifactRef{}, classify("get", requestContext, err)
	}
	defer output.Body.Close()
	content, err = io.ReadAll(io.LimitReader(output.Body, maximumArtifactBytes+1))
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

// DeleteMany removes an owner-scoped set of objects. S3 deletion is
// idempotent, so retries are safe when metadata deletion fails after the
// object operation completes.
func (store *ScopedStore) DeleteMany(ctx context.Context, keys []string) error {
	if store == nil || store.store == nil {
		return &Error{Operation: "delete", Kind: KindInvalid, err: errors.New("store is not initialized")}
	}
	return store.store.deleteMany(ctx, keys, store.prefix)
}

func (store *GarageStore) DeleteMany(ctx context.Context, keys []string) error {
	return store.deleteMany(ctx, keys, "")
}

func (store *GarageStore) deleteMany(ctx context.Context, keys []string, prefix string) (err error) {
	if store == nil || store.client == nil {
		return &Error{Operation: "delete", Kind: KindInvalid, err: errors.New("store is not initialized")}
	}
	if len(keys) == 0 {
		return nil
	}
	objects := make([]types.ObjectIdentifier, len(keys))
	for index, key := range keys {
		if err := validateKey(key, prefix); err != nil {
			return &Error{Operation: "delete", Kind: KindInvalid, err: err}
		}
		objects[index] = types.ObjectIdentifier{Key: aws.String(key)}
	}
	ctx, endOperation := store.telemetry.Start(ctx, "delete")
	defer func() { endOperation(err) }()
	for start := 0; start < len(objects); start += 1000 {
		end := min(start+1000, len(objects))
		requestContext, cancel := store.operationContext(ctx)
		output, deleteErr := store.client.DeleteObjects(requestContext, &s3.DeleteObjectsInput{
			Bucket: aws.String(store.bucket),
			Delete: &types.Delete{Objects: objects[start:end], Quiet: aws.Bool(true)},
		})
		if deleteErr != nil {
			classified := classify("delete", requestContext, deleteErr)
			cancel()
			return classified
		}
		cancel()
		if output == nil || deleteOutputFailed(output.Errors) {
			return &Error{Operation: "delete", Kind: KindUnavailable, err: errors.New("object deletion was incomplete")}
		}
	}
	return nil
}

func deleteOutputFailed(deleteErrors []types.Error) bool {
	for _, deleteError := range deleteErrors {
		switch strings.ToLower(strings.TrimSpace(aws.ToString(deleteError.Code))) {
		case "nosuchkey", "notfound", "no_such_key":
			continue
		default:
			return true
		}
	}
	return false
}

func (store *GarageStore) presignGet(ctx context.Context, key string, ttl time.Duration, prefix string) (location string, err error) {
	if store == nil || store.presigner == nil {
		return "", &Error{Operation: "presign", Kind: KindInvalid, err: errors.New("store is not initialized")}
	}
	if err := validateKey(key, prefix); err != nil {
		return "", &Error{Operation: "presign", Kind: KindInvalid, err: err}
	}
	if ttl < time.Second || ttl > store.maxPresignTTL || ttl > maximumPresignTTL {
		return "", &Error{Operation: "presign", Kind: KindInvalid, err: errors.New("presign TTL is outside the supported range")}
	}
	ctx, endOperation := store.telemetry.Start(ctx, "presign")
	defer func() { endOperation(err) }()
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
func (store *GarageStore) Ready(ctx context.Context) (err error) {
	if store == nil || store.client == nil {
		return &Error{Operation: "ready", Kind: KindInvalid, err: errors.New("store is not initialized")}
	}
	ctx, endOperation := store.telemetry.Start(ctx, "ready")
	defer func() { endOperation(err) }()
	requestContext, cancel := store.operationContext(ctx)
	defer cancel()
	_, err = store.client.HeadBucket(requestContext, &s3.HeadBucketInput{Bucket: aws.String(store.bucket)})
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

func validInventoryPrefix(prefix string) bool {
	if prefix == "" || len(prefix) > 740 || !strings.HasSuffix(prefix, "/") || !utf8.ValidString(prefix) ||
		path.IsAbs(prefix) || path.Clean(strings.TrimSuffix(prefix, "/"))+"/" != prefix ||
		strings.ContainsAny(prefix, "\\?#\x00\r\n") || strings.Contains(prefix, "//") {
		return false
	}
	return true
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
