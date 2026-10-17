package stateful

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/google/uuid"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/diagnostics"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const artifactOperationTimeout = 2 * time.Second
const maximumArtifactBytes = 16 << 20

var artifactIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type ArtifactOptions struct {
	Endpoint, Bucket, Region, AccessKeyID, SecretAccessKey string
	HTTPClient                                             aws.HTTPClient
}
type ArtifactStore struct {
	client     *s3.Client
	bucket     string
	testPrefix string
}
type ArtifactMetadata struct {
	ArtifactID  string `json:"artifactId"`
	Kind        string `json:"kind"`
	State       string `json:"state"`
	ContentType string `json:"contentType"`
	SizeBytes   int64  `json:"sizeBytes"`
	SHA256      string `json:"sha256"`
}
type ArtifactReader struct {
	Body io.ReadCloser
	ArtifactMetadata
}
type artifactDeleteOptions struct{ beforeDelete func(string) error }

// Batch artifacts use the same key-scoped Garage prefix and credential client.
func (store *ArtifactStore) PutBatchResult(ctx context.Context, hash, jobID, id string, data []byte) error {
	_, err := store.put(ctx, hash, jobID, id, "batch-result", data)
	return err
}
func (store *ArtifactStore) OpenBatchResult(ctx context.Context, hash, jobID, id string) (io.ReadCloser, error) {
	if err := artifactAddress(hash, jobID, id); err != nil {
		return nil, err
	}
	response, err := store.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(store.bucket), Key: aws.String(store.runPrefix(hash, jobID) + id)})
	if err != nil {
		return nil, &OperationError{Code: "artifact_read_failed", Message: "Garage result retrieval failed.", cause: err}
	}
	return response.Body, nil
}

// NewArtifactStore constructs one bounded path-style Garage client without I/O.
func NewArtifactStore(options ArtifactOptions) (*ArtifactStore, error) {
	parsed, err := url.Parse(options.Endpoint)
	if err != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" || options.Bucket == "" || options.Region == "" || options.AccessKeyID == "" || options.SecretAccessKey == "" {
		return nil, &OperationError{Code: "reference_configuration_error", Message: "Configure one Garage endpoint, bucket, region and credential pair."}
	}
	client := options.HTTPClient
	if client == nil {
		dialer := &net.Dialer{Timeout: artifactOperationTimeout, KeepAlive: 30 * time.Second}
		client = &http.Client{Transport: &http.Transport{Proxy: nil, DialContext: dialer.DialContext, MaxIdleConns: 32, MaxIdleConnsPerHost: 16, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: artifactOperationTimeout, ResponseHeaderTimeout: artifactOperationTimeout, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}}
	}
	config := aws.Config{Region: options.Region, Credentials: aws.NewCredentialsCache(aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: options.AccessKeyID, SecretAccessKey: options.SecretAccessKey, Source: "harden-llm-garage"}, nil
	})), HTTPClient: client, RetryMaxAttempts: 1, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired}
	api := s3.NewFromConfig(config, func(opts *s3.Options) {
		opts.BaseEndpoint = aws.String(strings.TrimSuffix(options.Endpoint, "/"))
		opts.UsePathStyle = true
		opts.DisableMultiRegionAccessPoints = true
	})
	return &ArtifactStore{client: api, bucket: options.Bucket}, nil
}
func artifactAddress(hash, runID, id string) error {
	if !validKeyHash(hash) {
		return &OperationError{Code: "invalid_api_key", Message: "An authenticated access-key scope is required."}
	}
	if _, err := uuid.Parse(runID); err != nil || !artifactIDPattern.MatchString(id) {
		return &OperationError{Code: "artifact_invalid", Message: "Use a valid run UUID and artifact identifier."}
	}
	return nil
}
func (store *ArtifactStore) runPrefix(hash, runID string) string {
	return store.testPrefix + "artifacts/" + hash + "/" + runID + "/"
}
func (store *ArtifactStore) put(ctx context.Context, hash, runID, id, kind string, data []byte) (ArtifactMetadata, error) {
	if err := artifactAddress(hash, runID, id); err != nil {
		return ArtifactMetadata{}, err
	}
	if len(data) == 0 || len(data) > maximumArtifactBytes || !json.Valid(data) {
		return ArtifactMetadata{}, &OperationError{Code: "artifact_invalid", Message: "Artifacts must be bounded valid JSON."}
	}
	digest := sha256.Sum256(data)
	text := hex.EncodeToString(digest[:])
	_, err := store.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(store.bucket), Key: aws.String(store.runPrefix(hash, runID) + id), Body: bytes.NewReader(data), ContentLength: aws.Int64(int64(len(data))), ContentType: aws.String("application/json"), Metadata: map[string]string{"sha256": text, "kind": kind}, IfNoneMatch: aws.String("*")})
	if err != nil {
		return ArtifactMetadata{}, &OperationError{Code: "artifact_write_failed", Message: "Garage artifact publication failed; inspect correlated diagnostics.", cause: err}
	}
	return ArtifactMetadata{ArtifactID: id, Kind: kind, State: "available", ContentType: "application/json", SizeBytes: int64(len(data)), SHA256: text}, nil
}
func (s *Service) publishArtifacts(ctx context.Context, hash, runID string, capture hardenllm.DiagnosticCapture) ([]ArtifactMetadata, *Diagnostic) {
	if s.config.Artifacts == nil {
		return []ArtifactMetadata{}, nil
	}
	started := time.Now()
	refs := []ArtifactMetadata{}
	failure := func(err error) ([]ArtifactMetadata, *Diagnostic) {
		return refs, &Diagnostic{Code: "artifact_write_failed", Component: "artifacts", Operation: "publish", RequestID: runID, RunID: runID, TraceID: capture.Trace.TraceID, InferenceOutcome: "preserved", PersistenceOutcome: "not_saved", Cause: safeCause(err), DurationMS: time.Since(started).Milliseconds(), Message: "Artifact publication failed; the inference result is preserved.", Action: "Check Garage endpoint, credentials and bucket permissions using the correlated run ID."}
	}
	if err := artifactAddress(hash, runID, "trace.json"); err != nil {
		return failure(err)
	}
	if capture.ErrorCode != "" {
		return failure(&OperationError{Code: capture.ErrorCode, Message: "Core diagnostic projection failed."})
	}
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), artifactOperationTimeout)
	defer cancel()
	for _, projection := range capture.Artifacts {
		id := projection.ArtifactID + ".json"
		if projection.Kind == "trace" {
			id = "trace.json"
		}
		ref, err := s.config.Artifacts.put(bounded, hash, runID, id, projection.Kind, projection.Content)
		if err != nil {
			return failure(err)
		}
		refs = append(refs, ref)
	}
	bundle, err := diagnostics.Build(diagnostics.Input{Trace: capture.Trace, EndpointURL: capture.Trace.SelectedTarget.Endpoint, RuntimeIdentity: diagnostics.RuntimeIdentity{Version: capture.Trace.Context.Release, GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, CapturedAt: s.now().UTC()}})
	if err != nil {
		return failure(err)
	}
	encoded, err := json.Marshal(bundle)
	if err != nil {
		return failure(err)
	}
	ref, err := s.config.Artifacts.put(bounded, hash, runID, "support-bundle.json", "support-bundle", encoded)
	if err != nil {
		return failure(err)
	}
	refs = append(refs, ref)
	return refs, nil
}
func (s *Service) OpenArtifact(ctx context.Context, hash, runID, id string) (ArtifactReader, error) {
	if err := artifactAddress(hash, runID, id); err != nil {
		return ArtifactReader{}, err
	}
	if s.config.Artifacts == nil {
		return ArtifactReader{}, &OperationError{Code: "artifact_not_found", Message: "Artifact is not available for this access key."}
	}
	store := s.config.Artifacts
	bounded, cancel := context.WithTimeout(ctx, artifactOperationTimeout)
	defer cancel()
	output, err := store.client.GetObject(bounded, &s3.GetObjectInput{Bucket: aws.String(store.bucket), Key: aws.String(store.runPrefix(hash, runID) + id)})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NoSuchKey" || apiErr.ErrorCode() == "NotFound") {
			return ArtifactReader{}, &OperationError{Code: "artifact_not_found", Message: "Artifact is not available for this access key."}
		}
		return ArtifactReader{}, &OperationError{Code: "artifact_read_failed", Message: "Garage artifact could not be read; inspect storage diagnostics.", cause: err}
	}
	defer output.Body.Close()
	data, err := io.ReadAll(io.LimitReader(output.Body, maximumArtifactBytes+1))
	if err != nil {
		return ArtifactReader{}, &OperationError{Code: "artifact_read_failed", Message: "Garage artifact read did not complete.", cause: err}
	}
	digest := sha256.Sum256(data)
	text := hex.EncodeToString(digest[:])
	kind := output.Metadata["kind"]
	if len(data) == 0 || len(data) > maximumArtifactBytes || !json.Valid(data) || aws.ToString(output.ContentType) != "application/json" || output.ContentLength == nil || *output.ContentLength != int64(len(data)) || output.Metadata["sha256"] != text || (kind != "trace" && kind != "parse-failure-response" && kind != "diagnostic-event" && kind != "support-bundle") {
		return ArtifactReader{}, &OperationError{Code: "artifact_integrity_failed", Message: "Garage artifact metadata does not match its bytes."}
	}
	return ArtifactReader{Body: io.NopCloser(bytes.NewReader(data)), ArtifactMetadata: ArtifactMetadata{ArtifactID: id, Kind: kind, State: "available", ContentType: "application/json", SizeBytes: int64(len(data)), SHA256: text}}, nil
}
func (store *ArtifactStore) deleteRunPrefix(ctx context.Context, hash, runID string, options artifactDeleteOptions) error {
	if err := artifactAddress(hash, runID, "trace.json"); err != nil {
		return err
	}
	if store == nil {
		return nil
	}
	bounded, cancel := context.WithTimeout(ctx, artifactOperationTimeout)
	defer cancel()
	prefix := store.runPrefix(hash, runID)
	continuation := ""
	failure := func(err error) error {
		return &OperationError{Code: "artifact_cleanup_failed", Message: "Artifact cleanup failed for run " + runID + "; retry deletion to finish.", cause: err}
	}
	for {
		input := &s3.ListObjectsV2Input{Bucket: aws.String(store.bucket), Prefix: aws.String(prefix), MaxKeys: aws.Int32(1000)}
		if continuation != "" {
			input.ContinuationToken = aws.String(continuation)
		}
		page, err := store.client.ListObjectsV2(bounded, input)
		if err != nil {
			return failure(err)
		}
		for _, object := range page.Contents {
			key := aws.ToString(object.Key)
			if !strings.HasPrefix(key, prefix) {
				return failure(errors.New("Garage prefix inventory mismatch"))
			}
			if options.beforeDelete != nil {
				if err := options.beforeDelete(key); err != nil {
					return failure(err)
				}
			}
			if _, err := store.client.DeleteObject(bounded, &s3.DeleteObjectInput{Bucket: aws.String(store.bucket), Key: aws.String(key)}); err != nil {
				return failure(err)
			}
		}
		if !aws.ToBool(page.IsTruncated) {
			return nil
		}
		continuation = aws.ToString(page.NextContinuationToken)
		if continuation == "" {
			return failure(errors.New("Garage continuation token missing"))
		}
	}
}
func (s *Service) artifactFeature() FeatureStatus {
	if s.config.Artifacts == nil {
		return FeatureStatus{Mode: "disabled", Status: "disabled"}
	}
	return FeatureStatus{Mode: "enabled", Status: "configured"}
}
