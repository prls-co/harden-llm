//go:build integration

package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-443

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/integrationtest"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"strings"
	"testing"
	"time"
)

func artifactFixture(t *testing.T) (*Service, *ArtifactStore, integrationtest.Garage, hardenllm.DiagnosticCapture) {
	t.Helper()
	_, garage := integrationtest.GarageLease(t)
	store, err := NewArtifactStore(ArtifactOptions{Endpoint: garage.Endpoint, Bucket: garage.Bucket, Region: garage.Region, AccessKeyID: garage.AccessKeyID, SecretAccessKey: garage.SecretAccessKey})
	if err != nil {
		t.Fatal(err)
	}
	store.testPrefix = garage.Namespace
	service, _ := serviceFixture(t, nil, nil)
	service.config.Artifacts = store
	capture := hardenllm.DiagnosticCapture{Trace: hardenllm.Trace{}, Artifacts: []hardenllm.ArtifactProjection{{ArtifactID: "trace-fixture", Kind: "trace", Content: json.RawMessage(`{"schemaVersion":"trace-fixture"}`), ContentType: "application/json"}, {ArtifactID: "raw-attempt-1", Kind: "parse-failure-response", Content: json.RawMessage(`{"rawResponse":"broken"}`), ContentType: "application/json"}}}
	capture.Trace.SchemaVersion = "harden-llm.trace.v3"
	capture.Trace.CallID = "00000000-0000-4000-8000-000000000001"
	capture.Trace.TraceID = capture.Trace.CallID
	capture.Trace.SelectedTarget.Endpoint = "https://provider.example/v1"
	return service, store, garage, capture
}
func TestReferenceArtifacts(t *testing.T) {
	t.Parallel()
	runID := "00000000-0000-4000-8000-000000000001"
	t.Run("publish metadata and bytes live under the exact key and run prefix", func(t *testing.T) {
		s, store, garage, capture := artifactFixture(t)
		refs, diagnostic := s.publishArtifacts(context.Background(), keyHashA, runID, capture)
		if diagnostic != nil || len(refs) != 3 {
			t.Fatal("trace parse evidence and support bundle not published", diagnostic, len(refs))
		}
		for _, ref := range refs {
			key := store.testPrefix + "artifacts/" + keyHashA + "/" + runID + "/" + ref.ArtifactID
			object, err := store.client.GetObject(context.Background(), &s3.GetObjectInput{Bucket: aws.String(garage.Bucket), Key: aws.String(key)})
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(object.Body)
			_ = object.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(data)
			if ref.ContentType != "application/json" || ref.SizeBytes != int64(len(data)) || ref.SHA256 != hex.EncodeToString(digest[:]) || object.Metadata["sha256"] != ref.SHA256 || aws.ToString(object.ContentType) != ref.ContentType {
				t.Fatal("published metadata differs from object bytes")
			}
		}
	})
	t.Run("same-key clients stream bytes without needing a history row", func(t *testing.T) {
		s, _, _, capture := artifactFixture(t)
		refs, diagnostic := s.publishArtifacts(context.Background(), keyHashA, runID, capture)
		if diagnostic != nil || len(refs) == 0 {
			t.Fatal(diagnostic)
		}
		other := *s
		for _, client := range []*Service{s, &other} {
			reader, err := client.OpenArtifact(context.Background(), keyHashA, runID, "trace.json")
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(reader.Body)
			_ = reader.Body.Close()
			if err != nil || !bytes.Equal(data, capture.Artifacts[0].Content) || reader.ContentType != "application/json" {
				t.Fatal("artifact stream bytes differ", err)
			}
		}
		_, err := s.OpenArtifact(context.Background(), keyHashB, runID, "trace.json")
		requireCode(t, err, "artifact_not_found")
	})
	t.Run("invalid UUID and escaping artifact IDs fail before Garage", func(t *testing.T) {
		s, _, _, _ := artifactFixture(t)
		for _, pair := range [][2]string{{"invalid", "trace.json"}, {runID, "../x"}, {runID, "/absolute.json"}, {runID, "x/y.json"}} {
			if _, err := s.OpenArtifact(context.Background(), keyHashA, pair[0], pair[1]); err == nil {
				t.Fatal("unsafe artifact address accepted")
			}
		}
	})
	t.Run("digest mismatch returns no bytes", func(t *testing.T) {
		s, store, garage, capture := artifactFixture(t)
		if refs, d := s.publishArtifacts(context.Background(), keyHashA, runID, capture); d != nil || len(refs) != 3 {
			t.Fatal("publication fixture is incomplete", d)
		}
		key := store.testPrefix + "artifacts/" + keyHashA + "/" + runID + "/trace.json"
		_, err := store.client.PutObject(context.Background(), &s3.PutObjectInput{Bucket: aws.String(garage.Bucket), Key: aws.String(key), Body: strings.NewReader(`{"tampered":true}`), ContentType: aws.String("application/json"), Metadata: map[string]string{"sha256": strings.Repeat("0", 64)}})
		if err != nil {
			t.Fatal(err)
		}
		reader, err := s.OpenArtifact(context.Background(), keyHashA, runID, "trace.json")
		if err == nil || reader.Body != nil {
			t.Fatal("corrupt artifact bytes exposed")
		}
	})
	t.Run("unavailable publication is bounded and preserves the capture", func(t *testing.T) {
		s, _, _, capture := artifactFixture(t)
		store, err := NewArtifactStore(ArtifactOptions{Endpoint: "http://127.0.0.1:1", Bucket: "fixture-bucket", Region: "garage", AccessKeyID: "fixture", SecretAccessKey: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
		s.config.Artifacts = store
		before, _ := json.Marshal(capture)
		started := time.Now()
		_, diagnostic := s.publishArtifacts(context.Background(), keyHashA, runID, capture)
		after, _ := json.Marshal(capture)
		if diagnostic == nil || time.Since(started) > 2*time.Second || !bytes.Equal(before, after) {
			t.Fatal("publication changed capture or exceeded budget", diagnostic, time.Since(started))
		}
	})
	t.Run("prefix delete is exact idempotent and resumable after an object failure", func(t *testing.T) {
		s, store, _, capture := artifactFixture(t)
		otherRun := "00000000-0000-4000-8000-000000000002"
		for _, pair := range [][2]string{{keyHashA, runID}, {keyHashA, otherRun}, {keyHashB, runID}} {
			if _, d := s.publishArtifacts(context.Background(), pair[0], pair[1], capture); d != nil {
				t.Fatal(d)
			}
		}
		attempts := 0
		err := store.deleteRunPrefix(context.Background(), keyHashA, runID, artifactDeleteOptions{beforeDelete: func(string) error {
			attempts++
			if attempts == 2 {
				return errors.New("fixture delete failure")
			}
			return nil
		}})
		requireCode(t, err, "artifact_cleanup_failed")
		if err = store.deleteRunPrefix(context.Background(), keyHashA, runID, artifactDeleteOptions{}); err != nil {
			t.Fatal(err)
		}
		if err = store.deleteRunPrefix(context.Background(), keyHashA, runID, artifactDeleteOptions{}); err != nil {
			t.Fatal("missing prefix must be idempotent", err)
		}
		_, err = s.OpenArtifact(context.Background(), keyHashA, runID, "trace.json")
		requireCode(t, err, "artifact_not_found")
		for _, pair := range [][2]string{{keyHashA, otherRun}, {keyHashB, runID}} {
			reader, err := s.OpenArtifact(context.Background(), pair[0], pair[1], "trace.json")
			if err != nil {
				t.Fatal("prefix delete crossed scope", err)
			}
			_ = reader.Body.Close()
		}
	})
	t.Run("disabled artifacts perform no publication and report disabled", func(t *testing.T) {
		s, _, _, capture := artifactFixture(t)
		s.config.Artifacts = nil
		refs, diagnostic := s.publishArtifacts(context.Background(), keyHashA, runID, capture)
		if len(refs) != 0 || diagnostic != nil || s.artifactFeature().Status != "disabled" {
			t.Fatal("disabled artifacts created diagnostic or work")
		}
		_, err := s.OpenArtifact(context.Background(), keyHashA, runID, "trace.json")
		requireCode(t, err, "artifact_not_found")
	})
	t.Run("exported product functions take IDs rather than Garage keys or URLs", func(t *testing.T) {
		file, err := parser.ParseFile(token.NewFileSet(), "artifact.go", nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() || fn.Name.Name == "NewArtifactStore" {
				continue
			}
			for _, field := range fn.Type.Params.List {
				for _, name := range field.Names {
					if name.Name == "key" || name.Name == "url" || name.Name == "prefix" {
						t.Fatal("exported caller can supply Garage address", fn.Name.Name)
					}
				}
			}
		}
	})
}
