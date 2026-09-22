//go:build integration && capacity

package main

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/internal/capacity"
	"github.com/prls-co/harden-llm/internal/gateway"
	"github.com/prls-co/harden-llm/internal/integrationtest"
	"github.com/prls-co/harden-llm/internal/postgres"
	"github.com/prls-co/harden-llm/internal/profiles"
	"github.com/prls-co/harden-llm/internal/retry"
	logcollector "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metriccollector "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecollector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
)

const (
	capacityProfileID = "Capacity-Fixture"
	capacityModelID   = "synthetic-capacity-model"
	capacityOwnerID   = "capacity-test-owner"
	capacityAPIKey    = "synthetic-capacity-provider-key-not-a-secret"
)

type capacityTelemetrySink struct {
	mu      sync.RWMutex
	blocked chan struct{}
	entered chan struct{}
}

type capacityTraceSink struct {
	tracecollector.UnimplementedTraceServiceServer
	gate *capacityTelemetrySink
}

type capacityMetricSink struct {
	metriccollector.UnimplementedMetricsServiceServer
	gate *capacityTelemetrySink
}

type capacityLogSink struct {
	logcollector.UnimplementedLogsServiceServer
	gate *capacityTelemetrySink
}

func (sink *capacityTelemetrySink) setBlocked(blocked bool) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.blocked != nil {
		close(sink.blocked)
	}
	sink.blocked = nil
	sink.entered = nil
	if blocked {
		sink.blocked = make(chan struct{})
		sink.entered = make(chan struct{}, 1)
	}
}

func (sink *capacityTelemetrySink) wait(ctx context.Context) error {
	sink.mu.RLock()
	blocked, entered := sink.blocked, sink.entered
	sink.mu.RUnlock()
	if blocked == nil {
		return nil
	}
	select {
	case entered <- struct{}{}:
	default:
	}
	select {
	case <-blocked:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (sink *capacityTraceSink) Export(ctx context.Context, _ *tracecollector.ExportTraceServiceRequest) (*tracecollector.ExportTraceServiceResponse, error) {
	return &tracecollector.ExportTraceServiceResponse{}, sink.gate.wait(ctx)
}

func (sink *capacityMetricSink) Export(ctx context.Context, _ *metriccollector.ExportMetricsServiceRequest) (*metriccollector.ExportMetricsServiceResponse, error) {
	return &metriccollector.ExportMetricsServiceResponse{}, sink.gate.wait(ctx)
}

func (sink *capacityLogSink) Export(ctx context.Context, _ *logcollector.ExportLogsServiceRequest) (*logcollector.ExportLogsServiceResponse, error) {
	return &logcollector.ExportLogsServiceResponse{}, sink.gate.wait(ctx)
}

func TestGatewayCapacityBaseline(t *testing.T) {
	// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-277
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if err := requireCapacityFingerprints(os.Getenv("HARDEN_LLM_TEST_IMAGE_SET_SHA256"), os.Getenv("HARDEN_LLM_TEST_TOPOLOGY_SHA256")); err != nil {
		t.Fatal(err)
	}
	reportPath := os.Getenv("HARDEN_LLM_CAPACITY_REPORT_PATH")
	if reportPath == "" {
		t.Fatal("runner-owned HARDEN_LLM_CAPACITY_REPORT_PATH is required")
	}
	caseSet := os.Getenv("HARDEN_LLM_CAPACITY_CASE_SET")
	if caseSet == "" {
		caseSet = "correctness"
	}
	if caseSet == "full-stack" {
		t.Fatal("full-stack is certified by the separately owned go-compose smoke; it is not a capacity workload")
	}
	if caseSet != "correctness" && caseSet != "exploration" && caseSet != "holdout" {
		t.Fatalf("unsupported capacity case set %q", caseSet)
	}
	catalogBytes, err := os.ReadFile(filepath.Join(root, "test", "capacity-scenarios.json"))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := capacity.ParseScenarioCatalog(catalogBytes)
	if err != nil {
		t.Fatal(err)
	}
	scenarios := catalog.CaseSets[caseSet]
	if len(scenarios) == 0 {
		t.Fatalf("capacity scenario set %q is empty", caseSet)
	}

	_, databaseURL := integrationtest.PostgresLease(t)
	_, garageFixture := integrationtest.GarageLease(t)
	if err := bootstrapCapacityOwner(databaseURL); err != nil {
		t.Fatalf("bootstrap static-token owner through the existing operator command: %v", err)
	}
	if err := integrationtest.DeleteGarageOwnerTraceArtifacts(context.Background(), garageFixture, capacityOwnerID); err != nil {
		t.Fatalf("clear exact synthetic owner artifacts before the run: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if cleanupErr := integrationtest.DeleteGarageOwnerTraceArtifacts(cleanupContext, garageFixture, capacityOwnerID); cleanupErr != nil {
			t.Errorf("delete exact synthetic owner artifacts: %v", cleanupErr)
		}
	})

	provider, err := capacity.NewScriptedProvider("success", 0)
	if err != nil {
		t.Fatal(err)
	}
	providerServer := httptest.NewTLSServer(provider)
	t.Cleanup(providerServer.Close)
	trustProviderCertificate(t, providerServer.Certificate())

	telemetrySink, telemetryServer, telemetryEndpoint := startCapacityTelemetrySink(t)
	t.Cleanup(telemetryServer.Stop)

	listenAddress := reserveLoopbackAddress(t)
	serverContext, cancelServer := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	serverEnvironment := capacityGatewayEnvironment(databaseURL, garageFixture, listenAddress, telemetryEndpoint)
	go func() {
		serverDone <- runGatewayServer(serverContext, io.Discard, io.Discard, func(key string) string { return serverEnvironment[key] })
	}()
	gatewayEndpoint := "http://" + listenAddress + "/api/v1/run"
	waitForGatewayReady(t, "http://"+listenAddress+"/readyz", serverDone)
	t.Cleanup(func() {
		cancelServer()
		select {
		case err := <-serverDone:
			if err != nil {
				t.Errorf("stop real gateway capacity fixture: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("real gateway capacity fixture did not stop within its bounded shutdown allowance")
		}
	})

	client := &http.Client{Timeout: 60 * time.Second}
	if err := saveCapacityProfile(context.Background(), client, "http://"+listenAddress, providerServer.URL+"/v1"); err != nil {
		t.Fatalf("save and provider-probe local capacity profile: %v", err)
	}
	store, err := postgres.Open(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open isolated database for canonical execution checks: %v", err)
	}
	defer store.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 22*time.Minute)
	defer cancel()
	startedAt := time.Now().UTC()
	fingerprint, err := capacityFingerprint(root, catalogBytes, serverEnvironment, caseSet)
	if err != nil {
		t.Fatalf("fingerprint exact source, worktree, scenario, and configuration: %v", err)
	}
	report := capacity.ExecutionReport{
		SchemaVersion: 2, ReportKind: "harden-llm-capacity.v2", TestRunID: os.Getenv("HARDEN_LLM_TEST_RUN_ID"),
		CaseSet: caseSet, StartedAt: startedAt, TestIDs: []string{"TEST-277"}, Fingerprint: fingerprint,
		Limitations: []string{
			"The provider is synthetic; results measure gateway, Postgres, Garage, and test-driver overhead, not public-provider latency or model quality.",
			"Provider invoice, infrastructure price, retained-storage price, and a production SLO were not supplied; their costs and capacity disposition remain unknown.",
		},
	}
	reportAttempted := false
	defer func() {
		if reportAttempted || len(report.Cases) == 0 {
			return
		}
		finalizeCapacityReport(&report, false)
		reportAttempted = true
		if err := capacity.WriteExecutionReport(reportPath, report); err != nil {
			t.Errorf("write bounded partial capacity report after early stop: %v", err)
		}
	}()
	for _, scenarioSpec := range scenarios {
		if err := provider.Configure(scenarioSpec.ProviderScript, time.Duration(scenarioSpec.ProviderDelayMS)*time.Millisecond); err != nil {
			t.Fatalf("configure local provider for %s: %v", scenarioSpec.ID, err)
		}
		telemetrySink.setBlocked(scenarioSpec.TelemetryMode == "blocked-exporter")
		scenario := scenarioSpec.DriverScenario()
		scenario.Origin.TestRunID = report.TestRunID
		scenario.Origin.SourceRevision = report.Fingerprint.SourceSHA
		transport, err := capacity.NewGatewayHTTPTransport(capacity.GatewayHTTPTransportConfig{
			Endpoint: gatewayEndpoint, BearerToken: capacityToken, ProfileID: capacityProfileID,
			ScenarioID: scenarioSpec.ID, TestRunID: report.TestRunID,
			SourceRevision: report.Fingerprint.SourceSHA, Client: client,
		})
		if err != nil {
			t.Fatalf("configure REST transport for %s: %v", scenarioSpec.ID, err)
		}
		caseStartedAt := time.Now()
		result, runErr := capacity.Run(ctx, scenario, transport, capacity.WallClock{})
		caseReport := capacity.SummarizeScenario(scenarioSpec, result, provider.RequestCount(), time.Since(caseStartedAt))
		if runErr != nil {
			report.Cases = append(report.Cases, caseReport)
			report.Limitations = append(report.Limitations, fmt.Sprintf("scenario %s stopped before completion: %v", scenarioSpec.ID, runErr))
			t.Fatalf("capacity scenario %s: %v", scenarioSpec.ID, runErr)
		}
		if err := assertExpectedOutcomes(scenarioSpec.ExpectedOutcome, result); err != nil {
			report.Cases = append(report.Cases, caseReport)
			report.Limitations = append(report.Limitations, fmt.Sprintf("scenario %s did not match its declared outcome: %v", scenarioSpec.ID, err))
			t.Fatalf("scenario %s outcome oracle: %v", scenarioSpec.ID, err)
		}
		if err := assertProviderDispatchAccounting(result, provider.RequestCount()); err != nil {
			report.Cases = append(report.Cases, caseReport)
			report.Limitations = append(report.Limitations, fmt.Sprintf("scenario %s provider accounting did not reconcile: %v", scenarioSpec.ID, err))
			t.Fatalf("scenario %s provider dispatch accounting: %v", scenarioSpec.ID, err)
		}
		if err := verifyPersistedCapacityExecutions(ctx, t, client, "http://"+listenAddress, store, garageFixture, scenarioSpec, result, &caseReport); err != nil {
			report.Cases = append(report.Cases, caseReport)
			report.Limitations = append(report.Limitations, fmt.Sprintf("scenario %s persistence verification failed: %v", scenarioSpec.ID, err))
			t.Fatalf("scenario %s canonical history/trace/artifact checks: %v", scenarioSpec.ID, err)
		}
		report.Cases = append(report.Cases, caseReport)
		if caseSet == "exploration" {
			if reason := capacity.ExplorationStopReason(scenarioSpec.ID, result.PopulationResult, caseReport.MeasuredTraffic.LaunchLag); reason != "" {
				report.Limitations = append(report.Limitations, reason)
				t.Fatalf("%s", reason)
			}
		}
		if scenarioSpec.TelemetryMode == "blocked-exporter" {
			if err := waitForBlockedTelemetryExport(telemetrySink); err != nil {
				t.Fatalf("blocked telemetry exporter did not receive a bounded export: %v", err)
			}
			telemetrySink.setBlocked(false)
		}
	}
	finalizeCapacityReport(&report, true)
	if report.Disposition != capacity.DispositionInsufficientEvidence {
		t.Fatalf("missing SLO/pricing unexpectedly produced a capacity disposition %q", report.Disposition)
	}
	reportAttempted = true
	if err := capacity.WriteExecutionReport(reportPath, report); err != nil {
		t.Fatalf("write bounded private capacity report: %v", err)
	}
}

func finalizeCapacityReport(report *capacity.ExecutionReport, evidenceComplete bool) {
	report.EndedAt = time.Now().UTC()
	report.DurationMS = report.EndedAt.Sub(report.StartedAt).Milliseconds()
	report.Costs = capacity.CostsForExecution(report.Cases)
	report.Disposition = capacity.ClassifyDisposition(capacity.DispositionInput{EvidenceValid: evidenceComplete})
}

func requireCapacityFingerprints(imageSHA, topologySHA string) error {
	for name, value := range map[string]string{"image-set": imageSHA, "topology": topologySHA} {
		if len(value) != 64 {
			return fmt.Errorf("runner did not provide a complete %s SHA-256 fingerprint", name)
		}
		if _, err := hex.DecodeString(value); err != nil {
			return fmt.Errorf("runner provided an invalid %s fingerprint", name)
		}
	}
	return nil
}

func capacityGatewayEnvironment(databaseURL string, garage integrationtest.Garage, listenAddress, telemetryEndpoint string) map[string]string {
	key := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	return map[string]string{
		"HARDEN_LLM_LISTEN_ADDRESS":              listenAddress,
		"HARDEN_LLM_DATABASE_URL":                databaseURL,
		"HARDEN_LLM_ENCRYPTION_KEYS":             fmt.Sprintf(`{"capacity-test":"%s"}`, key),
		"HARDEN_LLM_ACTIVE_ENCRYPTION_KEY_ID":    "capacity-test",
		"HARDEN_LLM_ARTIFACT_ENDPOINT":           garage.Endpoint,
		"HARDEN_LLM_ARTIFACT_EXTERNAL_ENDPOINT":  garage.Endpoint,
		"HARDEN_LLM_ARTIFACT_BUCKET":             garage.Bucket,
		"HARDEN_LLM_ARTIFACT_ACCESS_KEY_ID":      garage.AccessKeyID,
		"HARDEN_LLM_ARTIFACT_SECRET_ACCESS_KEY":  garage.SecretAccessKey,
		"HARDEN_LLM_ENVIRONMENT":                 "test",
		"HARDEN_LLM_RELEASE":                     "capacity-test",
		"HARDEN_LLM_SERVICE_NAME":                "harden-llm-capacity-test",
		"HARDEN_LLM_STATIC_TOKEN":                capacityToken,
		"HARDEN_LLM_STATIC_TOKEN_OWNER_ID":       capacityOwnerID,
		"HARDEN_LLM_MAX_RUN_DURATION_MS":         "60000",
		"HARDEN_LLM_PROVIDER_PRIVATE_ALLOWLIST":  "127.0.0.1/32",
		"HARDEN_LLM_TEST_GARAGE_ENDPOINT":        strings.TrimPrefix(garage.Endpoint, "http://"),
		"HARDEN_LLM_OTEL_EXPORTER_OTLP_ENDPOINT": telemetryEndpoint,
	}
}

func bootstrapCapacityOwner(databaseURL string) error {
	environment := map[string]string{databaseURLEnvironment: databaseURL}
	return run(context.Background(), []string{
		"bootstrap-user", "--owner-id", capacityOwnerID, "--email", "capacity-test@example.test",
	}, strings.NewReader("capacity-fixture-password-not-used\n"), io.Discard, io.Discard, func(key string) string {
		return environment[key]
	})
}

func saveCapacityProfile(ctx context.Context, client *http.Client, gatewayURL, providerBaseURL string) error {
	profile := profiles.Profile{
		SchemaVersion: profiles.SchemaVersion, LLMProfile: capacityProfileID, Provider: "openai",
		APIInferenceType: "responses", EndpointCredentialScope: "user", BaseURL: providerBaseURL,
		ModelID: capacityModelID, SupportsContractedStructuredOutput: true, DefaultOptions: map[string]any{},
		RecoveryPolicy: retry.Policy{MaxAttempts: 1, RetryOn: []retry.Category{}, Backoff: retry.Backoff{}},
	}
	body, err := json.Marshal(map[string]any{
		"profile": profile, "credentialId": "capacity-test-provider",
		"credential": profiles.CredentialPayload{APIKey: capacityAPIKey},
	})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, gatewayURL+"/api/v1/profiles/"+capacityProfileID, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+capacityToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("save profile request failed: %w", err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("save profile returned HTTP %d", response.StatusCode)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(content, &envelope); err != nil || len(envelope.Result) == 0 {
		return errors.New("save profile returned an invalid success envelope")
	}
	return nil
}

func trustProviderCertificate(t *testing.T, certificate *x509.Certificate) {
	t.Helper()
	if certificate == nil || len(certificate.Raw) == 0 {
		t.Fatal("synthetic provider TLS certificate is missing")
	}
	path := filepath.Join(t.TempDir(), "provider-ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0o600); err != nil {
		t.Fatalf("write isolated local provider CA: %v", err)
	}
	t.Setenv("SSL_CERT_FILE", path)
}

func reserveLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve gateway listen port: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release gateway listen reservation: %v", err)
	}
	return address
}

func waitForGatewayReady(t *testing.T, endpoint string, serverDone <-chan error) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for time.Now().Before(deadline) {
		select {
		case err := <-serverDone:
			t.Fatalf("real gateway exited before readiness: %v", err)
		default:
		}
		request, _ := http.NewRequest(http.MethodGet, endpoint, nil)
		response, err := client.Do(request)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("real gateway did not become ready within the existing startup allowance")
}

func startCapacityTelemetrySink(t *testing.T) (*capacityTelemetrySink, *grpc.Server, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for isolated OTLP sink: %v", err)
	}
	server := grpc.NewServer()
	sink := &capacityTelemetrySink{}
	tracecollector.RegisterTraceServiceServer(server, &capacityTraceSink{gate: sink})
	metriccollector.RegisterMetricsServiceServer(server, &capacityMetricSink{gate: sink})
	logcollector.RegisterLogsServiceServer(server, &capacityLogSink{gate: sink})
	go func() { _ = server.Serve(listener) }()
	return sink, server, "http://" + listener.Addr().String()
}

func waitForBlockedTelemetryExport(sink *capacityTelemetrySink) error {
	sink.mu.RLock()
	entered := sink.entered
	sink.mu.RUnlock()
	if entered == nil {
		return errors.New("blocked exporter gate was not enabled")
	}
	select {
	case <-entered:
		return nil
	case <-time.After(7 * time.Second):
		return errors.New("OTLP exporter did not enter the local blocked sink")
	}
}

func assertExpectedOutcomes(expected string, result capacity.Result) error {
	for _, population := range []capacity.PopulationResult{result.Warmup, result.PopulationResult} {
		for _, request := range population.Requests {
			if request.Outcome != expected {
				return fmt.Errorf("request %d outcome = %q, want %q", request.ID, request.Outcome, expected)
			}
		}
	}
	return nil
}

func assertProviderDispatchAccounting(result capacity.Result, providerRequests int64) error {
	var observed int64
	for _, population := range []capacity.PopulationResult{result.Warmup, result.PopulationResult} {
		for _, request := range population.Requests {
			observed += int64(len(request.ProviderCalls))
		}
	}
	if observed != providerRequests {
		return fmt.Errorf("driver observed %d provider dispatches, local provider received %d", observed, providerRequests)
	}
	return nil
}

func verifyPersistedCapacityExecutions(ctx context.Context, t *testing.T, client *http.Client, gatewayURL string, store *postgres.Store, garage integrationtest.Garage, scenario capacity.ScenarioSpec, result capacity.Result, report *capacity.ScenarioReport) error {
	t.Helper()
	requests := append(append([]capacity.RequestResult(nil), result.Warmup.Requests...), result.PopulationResult.Requests...)
	for _, request := range requests {
		if request.RunID == "" || request.TraceID == "" || request.CallID == "" {
			return fmt.Errorf("request %d lacks run/call/trace correlation IDs", request.ID)
		}
		if request.ResultOrigin != request.Origin {
			return fmt.Errorf("request %d terminal origin differs from sent origin", request.ID)
		}
		run, err := store.RunByTrace(ctx, capacityOwnerID, request.TraceID)
		if err != nil {
			return fmt.Errorf("load persisted run for trace %s: %w", request.TraceID, err)
		}
		if run.ID != request.RunID || run.Status != request.ExecutionStatus {
			return fmt.Errorf("persisted execution %s/%s status=%s disagrees with terminal response %s/%s", run.ID, run.TraceID, run.Status, request.RunID, request.ExecutionStatus)
		}
		var stored gateway.RunOutput
		if err := json.Unmarshal(run.Result, &stored); err != nil {
			return fmt.Errorf("decode persisted run result: %w", err)
		}
		if stored.TraceID != request.TraceID || stored.CallID != request.CallID || gatewayOriginRecord(stored.Origin.Client, stored.Origin.Component, stored.Origin.OperationID, stored.Origin.ParentRunID, stored.Origin.JobID, stored.Origin.TestRunID, stored.Origin.TestID, stored.Origin.SourceRevision) != requestOriginRecord(request.Origin) {
			return fmt.Errorf("persisted run result lost synthetic correlation for request %d", request.ID)
		}
		traceRecord, observations, err := store.Trace(ctx, capacityOwnerID, request.TraceID)
		if err != nil {
			return fmt.Errorf("load persisted trace %s: %w", request.TraceID, err)
		}
		var traceDocument struct {
			RunID   string             `json:"runId"`
			TraceID string             `json:"traceId"`
			Origin  profilesOriginJSON `json:"origin"`
		}
		if err := json.Unmarshal(traceRecord.Record, &traceDocument); err != nil {
			return fmt.Errorf("decode persisted trace provenance: %w", err)
		}
		if traceDocument.RunID != request.RunID || traceDocument.TraceID != request.TraceID || traceDocument.Origin != requestOriginRecord(request.Origin) || len(observations) != len(stored.Attempts) {
			return fmt.Errorf("persisted trace lost run/trace/origin correlation for request %d", request.ID)
		}
		traceView, err := getCapacityTrace(ctx, client, gatewayURL, request.TraceID)
		if err != nil {
			return err
		}
		if traceView.TraceID != request.TraceID || len(traceView.Artifacts) != len(stored.Artifacts) {
			return fmt.Errorf("REST trace view artifact/correlation count disagrees for request %d", request.ID)
		}
		for _, artifact := range stored.Artifacts {
			record, err := store.Artifact(ctx, capacityOwnerID, request.TraceID, artifact.ArtifactID)
			if err != nil {
				return fmt.Errorf("load persisted artifact metadata: %w", err)
			}
			content, err := integrationtest.GetGarageOwnerTraceArtifact(ctx, garage, capacityOwnerID, record.ObjectKey)
			if err != nil {
				objectKeyDigest := sha256.Sum256([]byte(record.ObjectKey))
				return fmt.Errorf("request %d run %s trace %s artifact %s object_key_sha256 %s: read owner-scoped artifact object: %w",
					request.ID, run.ID, request.TraceID, record.ID, hex.EncodeToString(objectKeyDigest[:]), err)
			}
			digest := sha256.Sum256(content)
			if hex.EncodeToString(digest[:]) != record.SHA256 || int64(len(content)) != record.SizeBytes || record.SHA256 != artifact.SHA256 {
				return fmt.Errorf("persisted artifact metadata/digest mismatch for request %d", request.ID)
			}
			report.ArtifactBytesProduced += int64(len(content))
			report.ArtifactCount++
			report.StoredArtifacts = append(report.StoredArtifacts, capacity.StoredArtifact{
				RequestID: request.ID, ArtifactID: artifact.ArtifactID, Kind: artifact.Kind,
				SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes,
			})
		}
		report.PersistedExecutions++
	}
	if err := assertCapacityHistory(ctx, client, gatewayURL, requests); err != nil {
		return err
	}
	if scenario.CacheMode == "hit" {
		if len(result.Warmup.Requests) != 1 || len(result.PopulationResult.Requests) != 1 || result.Warmup.Requests[0].CacheHit || !result.PopulationResult.Requests[0].CacheHit {
			return errors.New("cache scenario did not show a provider-backed warmup followed by a measured cache hit")
		}
	}
	return nil
}

func requestOriginRecord(origin capacity.RequestOrigin) profilesOriginJSON {
	return profilesOriginJSON{
		Client: origin.Client, Component: origin.Component, OperationID: origin.OperationID,
		ParentRunID: origin.ParentRunID, JobID: origin.JobID, TestRunID: origin.TestRunID,
		TestID: origin.TestID, SourceRevision: origin.SourceRevision,
	}
}

func gatewayOriginRecord(client, component, operationID, parentRunID, jobID, testRunID, testID, sourceRevision string) profilesOriginJSON {
	return profilesOriginJSON{
		Client: client, Component: component, OperationID: operationID, ParentRunID: parentRunID,
		JobID: jobID, TestRunID: testRunID, TestID: testID, SourceRevision: sourceRevision,
	}
}

type profilesOriginJSON struct {
	Client         string `json:"client"`
	Component      string `json:"component"`
	OperationID    string `json:"operationId"`
	ParentRunID    string `json:"parentRunId"`
	JobID          string `json:"jobId"`
	TestRunID      string `json:"testRunId"`
	TestID         string `json:"testId"`
	SourceRevision string `json:"sourceRevision"`
}

func getCapacityTrace(ctx context.Context, client *http.Client, gatewayURL, traceID string) (gateway.TraceView, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, gatewayURL+"/api/v1/traces/"+traceID, nil)
	if err != nil {
		return gateway.TraceView{}, err
	}
	request.Header.Set("Authorization", "Bearer "+capacityToken)
	response, err := client.Do(request)
	if err != nil {
		return gateway.TraceView{}, err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return gateway.TraceView{}, err
	}
	if response.StatusCode != http.StatusOK {
		return gateway.TraceView{}, fmt.Errorf("REST trace endpoint returned HTTP %d", response.StatusCode)
	}
	var envelope struct {
		Result gateway.TraceView `json:"result"`
	}
	if err := json.Unmarshal(content, &envelope); err != nil {
		return gateway.TraceView{}, fmt.Errorf("decode REST trace envelope: %w", err)
	}
	return envelope.Result, nil
}

func sourceRevision(root string) (string, error) {
	command := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("read checkout revision: %w", err)
	}
	revision := strings.TrimSpace(string(output))
	if len(revision) != 40 {
		return "", errors.New("checkout revision is not a full Git SHA-1")
	}
	if _, err := hex.DecodeString(revision); err != nil {
		return "", errors.New("checkout revision contains non-hexadecimal data")
	}
	return revision, nil
}

func capacityFingerprint(root string, catalog []byte, environment map[string]string, caseSet string) (capacity.Fingerprint, error) {
	source, err := sourceRevision(root)
	if err != nil {
		return capacity.Fingerprint{}, err
	}
	workingTree, err := capacityWorkingTreeBytes(root)
	if err != nil {
		return capacity.Fingerprint{}, err
	}
	dirtyHash := sha256.Sum256(workingTree)
	configBytes, _ := json.Marshal(map[string]any{
		"profileId": capacityProfileID, "provider": "openai", "protocol": "responses", "model": capacityModelID,
		"maxRunDurationMs": environment["HARDEN_LLM_MAX_RUN_DURATION_MS"], "privateAllowlist": "127.0.0.1/32", "caseSet": caseSet,
	})
	configHash := sha256.Sum256(configBytes)
	scenarioHash := sha256.Sum256(catalog)
	return capacity.Fingerprint{
		SourceSHA: source, WorkingTreeSHA256: hex.EncodeToString(dirtyHash[:]),
		ScenarioSHA: hex.EncodeToString(scenarioHash[:]), ConfigSHA: hex.EncodeToString(configHash[:]),
		ImageSetSHA: os.Getenv("HARDEN_LLM_TEST_IMAGE_SET_SHA256"),
		TopologySHA: os.Getenv("HARDEN_LLM_TEST_TOPOLOGY_SHA256"),
	}, nil
}

func capacityWorkingTreeBytes(root string) ([]byte, error) {
	diff := exec.Command("git", "-C", root, "diff", "--binary", "HEAD", "--")
	diffBytes, err := diff.Output()
	if err != nil {
		return nil, fmt.Errorf("read tracked worktree diff: %w", err)
	}
	listing := exec.Command("git", "-C", root, "ls-files", "--others", "--exclude-standard", "-z")
	listingBytes, err := listing.Output()
	if err != nil {
		return nil, fmt.Errorf("list untracked worktree files: %w", err)
	}
	type untrackedFile struct {
		PathSHA256 string `json:"pathSha256"`
		Kind       string `json:"kind"`
		SizeBytes  int64  `json:"sizeBytes"`
		SHA256     string `json:"sha256"`
	}
	paths := strings.Split(string(listingBytes), "\x00")
	sort.Strings(paths)
	files := make([]untrackedFile, 0, len(paths))
	for _, name := range paths {
		if name == "" {
			continue
		}
		if len(files) >= 2_000 {
			return nil, errors.New("untracked worktree contains more than 2000 files")
		}
		filename := filepath.Join(root, name)
		info, err := os.Lstat(filename)
		if err != nil {
			return nil, fmt.Errorf("inspect untracked worktree entry: %w", err)
		}
		kind := "file"
		var content []byte
		switch {
		case info.Mode().IsRegular():
			content, err = os.ReadFile(filename)
		case info.Mode()&os.ModeSymlink != 0:
			kind = "symlink"
			var target string
			target, err = os.Readlink(filename)
			content = []byte(target)
		default:
			return nil, fmt.Errorf("untracked worktree entry %q is not a regular file or symlink", name)
		}
		if err != nil {
			return nil, fmt.Errorf("read untracked worktree entry: %w", err)
		}
		if len(content) > 64<<20 {
			return nil, fmt.Errorf("untracked worktree entry %q exceeds the 64 MiB fingerprint bound", name)
		}
		contentHash := sha256.Sum256(content)
		pathHash := sha256.Sum256([]byte(name))
		files = append(files, untrackedFile{
			PathSHA256: hex.EncodeToString(pathHash[:]), Kind: kind, SizeBytes: int64(len(content)),
			SHA256: hex.EncodeToString(contentHash[:]),
		})
	}
	diffHash := sha256.Sum256(diffBytes)
	return json.Marshal(struct {
		DiffSHA256 string          `json:"diffSha256"`
		Untracked  []untrackedFile `json:"untrackedFiles"`
	}{DiffSHA256: hex.EncodeToString(diffHash[:]), Untracked: files})
}
