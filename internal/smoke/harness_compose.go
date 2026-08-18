//go:build compose

package smoke

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	readinessBudget = 300 * time.Second
	correlationWait = 150 * time.Second
)

var requiredProductionServices = []string{
	"caddy", "harden-llm-gateway", "harden-postgres", "garage", "otel-collector",
	"prometheus", "loki", "tempo", "grafana", "langfuse-web", "langfuse-worker",
	"postgres", "clickhouse", "redis", "minio",
}

// ComposeReport is the threshold evidence shared by TEST-034 and EVAL-004.
type ComposeReport struct {
	ReadyServices       int
	TotalServices       int
	Readiness           time.Duration
	CorrelatedBackends  int
	CorrelationBackends int
	RunID               string
	TraceID             string
}

// RunComposeSmoke creates an isolated project, exercises one complete run, and
// always removes its containers and named volumes before returning.
func RunComposeSmoke(t *testing.T) ComposeReport {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("Docker is required for the Compose smoke: %v", err)
	}
	root := repositoryRoot(t)
	material := generateTLSMaterial(t, t.TempDir())
	httpPort := freeTCPPort(t)
	httpsPort := freeTCPPort(t)
	for httpsPort == httpPort {
		httpsPort = freeTCPPort(t)
	}
	project := fmt.Sprintf("harden-llm-smoke-%d-%d", os.Getpid(), time.Now().UnixNano())
	secrets := smokeEnvironment(t, material, httpPort, httpsPort)
	runner := composeRunner{
		root: root, project: project, environment: secrets,
		files: []string{
			filepath.Join(root, "docker-compose.yml"),
			filepath.Join(root, "deploy", "langfuse", "docker-compose.upstream.yml"),
			filepath.Join(root, "deploy", "langfuse", "compose.private.yml"),
			filepath.Join(root, "deploy", "test", "compose.smoke.yml"),
		},
	}
	_ = runner.run(context.Background(), nil, "down", "--volumes", "--remove-orphans", "--timeout", "10")
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Compose diagnostics before cleanup:\n%s", runner.diagnostics())
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := runner.run(ctx, nil, "down", "--volumes", "--remove-orphans", "--timeout", "20"); err != nil {
			t.Logf("Compose cleanup: %v", err)
		}
	})

	pullContext, cancelPull := context.WithTimeout(context.Background(), 20*time.Minute)
	if err := runner.run(pullContext, nil, "pull", "--ignore-buildable"); err != nil {
		cancelPull()
		t.Fatalf("pre-pull pinned images: %v", err)
	}
	cancelPull()

	started := time.Now()
	startContext, cancelStart := context.WithTimeout(context.Background(), 6*time.Minute)
	err := runner.run(startContext, nil, "up", "-d", "--build", "--wait", "--wait-timeout", "300")
	cancelStart()
	if err != nil {
		t.Fatalf("start Compose stack: %v\n%s", err, runner.diagnostics())
	}
	readiness := time.Since(started)
	if readiness > readinessBudget {
		t.Fatalf("Compose readiness = %s, budget %s", readiness, readinessBudget)
	}

	report := ComposeReport{TotalServices: len(requiredProductionServices), Readiness: readiness, CorrelationBackends: 5}
	report.ReadyServices = assertContainerTopology(t, runner)
	client := caddyClient(httpsPort, false)
	waitHTTPStatus(t, client, "https://api.smoke.localhost/readyz", http.StatusOK, 45*time.Second, nil)
	waitHTTPStatus(t, client, "https://grafana.smoke.localhost/api/health", http.StatusOK, 45*time.Second, nil)
	waitHTTPStatus(t, client, "https://langfuse.smoke.localhost/api/public/health", http.StatusOK, 90*time.Second, nil)

	bootstrapPassword := "Smoke-user-password-7xQ2mV9p"
	bootstrapContext, cancelBootstrap := context.WithTimeout(context.Background(), 45*time.Second)
	if err := runner.run(bootstrapContext, strings.NewReader(bootstrapPassword+"\n"), "run", "--rm", "-T", "harden-llm-gateway",
		"bootstrap-user", "--owner-id", "smoke-owner", "--email", "smoke@example.test", "--password-file", "-"); err != nil {
		cancelBootstrap()
		t.Fatalf("bootstrap smoke user: %v", err)
	}
	cancelBootstrap()

	login := requestJSON(t, client, http.MethodPost, "https://api.smoke.localhost/api/v1/auth/login", map[string]any{
		"email": "smoke@example.test", "password": bootstrapPassword,
	}, "", http.StatusOK)
	loginResult := object(t, login["result"], "login result")
	token := text(t, loginResult["accessToken"], "access token")
	if token == "" || strings.Contains(token, "smoke") {
		t.Fatal("login did not return an opaque bearer token")
	}

	providerSecret := "smoke-provider-key-must-remain-redacted"
	profileDocument := map[string]any{
		"profile": map[string]any{
			"schemaVersion": 1, "llmProfile": "Smoke", "provider": "openai", "apiInferenceType": "responses",
			"endpointCredentialScope": "user", "baseUrl": "https://fake-provider:8443/v1", "modelId": "smoke-model",
			"pricing": nil, "supportsTemperature": false, "supportsContractedStructuredOutput": true,
			"tokensParam": nil, "responsesTokensParam": "max_output_tokens", "defaultOptions": map[string]any{},
			"backupProfiles": []any{},
		},
		"credentialId": "smoke-provider", "credential": map[string]any{"apiKey": providerSecret},
	}
	requestJSON(t, client, http.MethodPut, "https://api.smoke.localhost/api/v1/profiles/Smoke", profileDocument, token, http.StatusOK)

	runEnvelope := requestJSON(t, client, http.MethodPost, "https://api.smoke.localhost/api/v1/run", map[string]any{
		"profileId": "Smoke", "userPrompt": "return the smoke response", "callType": "text", "cacheMode": "off", "maxAttempts": 1,
	}, token, http.StatusOK)
	runResult := object(t, runEnvelope["result"], "run result")
	if runResult["output"] != "smoke-ok" {
		t.Fatalf("fake-provider output = %#v", runResult["output"])
	}
	report.RunID = text(t, runResult["runId"], "run ID")
	report.TraceID = text(t, runResult["traceId"], "trace ID")
	if report.RunID == "" || report.TraceID == "" {
		t.Fatal("run returned empty correlation IDs")
	}

	traceEnvelope := requestJSON(t, client, http.MethodGet,
		"https://api.smoke.localhost/api/v1/traces/"+url.PathEscape(report.TraceID), nil, token, http.StatusOK)
	traceView := object(t, traceEnvelope["result"], "trace result")
	artifacts := array(t, traceView["artifacts"], "trace artifacts")
	if len(artifacts) != 1 {
		t.Fatalf("trace artifact count = %d, want 1", len(artifacts))
	}
	artifact := object(t, artifacts[0], "trace artifact")
	artifactID := text(t, artifact["artifactId"], "artifact ID")
	wantDigest := text(t, artifact["sha256"], "artifact SHA-256")
	wantSize := integer(t, artifact["sizeBytes"], "artifact size")
	if artifact["kind"] != "trace" || len(wantDigest) != 64 || wantSize < 1 {
		t.Fatalf("artifact metadata = %#v", artifact)
	}

	redirectClient := caddyClient(httpsPort, true)
	location := artifactLocation(t, redirectClient,
		"https://api.smoke.localhost/api/v1/traces/"+url.PathEscape(report.TraceID)+"/artifacts/"+url.PathEscape(artifactID), token)
	artifactBytes := fetchArtifact(t, caddyClient(httpsPort, false), location)
	digest := sha256.Sum256(artifactBytes)
	if hex.EncodeToString(digest[:]) != wantDigest || int64(len(artifactBytes)) != wantSize || !json.Valid(artifactBytes) {
		t.Fatalf("artifact integrity mismatch: bytes=%d sha256=%x metadata=%d/%s", len(artifactBytes), digest, wantSize, wantDigest)
	}
	if bytes.Contains(artifactBytes, []byte(providerSecret)) || bytes.Contains(artifactBytes, []byte("return the smoke response")) {
		t.Fatal("redacted trace artifact contains provider credentials or prompt text")
	}

	assertPostgresState(t, runner)
	correlation := correlateBackends(t, runner, client, report, secrets)
	report.CorrelatedBackends = correlation
	if correlation != report.CorrelationBackends {
		t.Fatalf("backend correlation = %d/%d", correlation, report.CorrelationBackends)
	}
	assertGrafanaDatasources(t, client, secrets["GRAFANA_ADMIN_USER"], secrets["GRAFANA_ADMIN_PASSWORD"])
	assertLiveStorageOwnership(t, runner)

	t.Logf("Compose evidence: ready=%d/%d readiness=%s correlation=%d/%d run=%s trace=%s",
		report.ReadyServices, report.TotalServices, report.Readiness.Round(time.Millisecond),
		report.CorrelatedBackends, report.CorrelationBackends, report.RunID, report.TraceID)
	return report
}

type composeRunner struct {
	root        string
	project     string
	files       []string
	environment map[string]string
}

func (runner composeRunner) run(ctx context.Context, stdin io.Reader, arguments ...string) error {
	args := []string{"compose", "--project-name", runner.project}
	for _, file := range runner.files {
		args = append(args, "-f", file)
	}
	args = append(args, arguments...)
	command := exec.CommandContext(ctx, "docker", args...)
	command.Dir = runner.root
	command.Env = append(os.Environ(), sortedEnvironment(runner.environment)...)
	command.Stdin = stdin
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return fmt.Errorf("docker %s: %w: %s", strings.Join(arguments, " "), err, runner.redact(output.String()))
	}
	return nil
}

func (runner composeRunner) output(ctx context.Context, arguments ...string) ([]byte, error) {
	args := []string{"compose", "--project-name", runner.project}
	for _, file := range runner.files {
		args = append(args, "-f", file)
	}
	args = append(args, arguments...)
	command := exec.CommandContext(ctx, "docker", args...)
	command.Dir = runner.root
	command.Env = append(os.Environ(), sortedEnvironment(runner.environment)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker %s: %w: %s", strings.Join(arguments, " "), err, runner.redact(string(output)))
	}
	return output, nil
}

func (runner composeRunner) redact(value string) string {
	for name, secret := range runner.environment {
		if isSensitiveEnvironment(name) && secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	if len(value) > 6<<10 {
		value = value[len(value)-(6<<10):]
	}
	return strings.TrimSpace(value)
}

func (runner composeRunner) diagnostics() string {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ps, _ := runner.output(ctx, "ps", "--all", "--format", "json")
	ids, _ := runner.output(ctx, "ps", "--all", "--quiet")
	var states []byte
	containerIDs := strings.Fields(string(ids))
	if len(containerIDs) > 0 {
		arguments := append([]string{"inspect", "--format", `{{.Name}} {{json .State}}`}, containerIDs...)
		command := exec.CommandContext(ctx, "docker", arguments...)
		states, _ = command.CombinedOutput()
	}
	logs, _ := runner.output(ctx, "logs", "--no-color", "--tail", "20")
	serviceLogs, _ := runner.output(ctx, "logs", "--no-color", "--tail", "200", "harden-llm-gateway", "fake-provider")
	return runner.redact("COMPOSE PS\n" + string(ps) + "\nCONTAINER STATES\n" + string(states) + "\nRECENT LOGS\n" + string(logs) + "\nGATEWAY AND PROVIDER LOGS\n" + string(serviceLogs))
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if data, readErr := os.ReadFile(filepath.Join(directory, "go.mod")); readErr == nil && bytes.Contains(data, []byte("module github.com/prls-co/harden-llm")) {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root was not found")
		}
		directory = parent
	}
}

type tlsMaterial struct{ ca, certificate, key string }

func generateTLSMaterial(t *testing.T, directory string) tlsMaterial {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	caTemplate := &x509.Certificate{
		SerialNumber: randomSerial(t), Subject: pkix.Name{CommonName: "Harden LLM smoke CA"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(2 * time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	providerKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	providerTemplate := &x509.Certificate{
		SerialNumber: randomSerial(t), Subject: pkix.Name{CommonName: "fake-provider"},
		DNSNames: []string{"fake-provider"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(2 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	providerDER, err := x509.CreateCertificate(rand.Reader, providerTemplate, caTemplate, &providerKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(providerKey)
	if err != nil {
		t.Fatal(err)
	}
	material := tlsMaterial{
		ca: filepath.Join(directory, "provider-ca.crt"), certificate: filepath.Join(directory, "provider.crt"), key: filepath.Join(directory, "provider.key"),
	}
	writePEM(t, material.ca, "CERTIFICATE", caDER)
	writePEM(t, material.certificate, "CERTIFICATE", providerDER)
	writePEM(t, material.key, "PRIVATE KEY", keyDER)
	return material
}

func randomSerial(t *testing.T) *big.Int {
	t.Helper()
	maximum := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, maximum)
	if err != nil {
		t.Fatal(err)
	}
	return serial
}

func writePEM(t *testing.T, path, kind string, data []byte) {
	t.Helper()
	contents := pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: data})
	if err := os.WriteFile(path, contents, 0o444); err != nil {
		t.Fatal(err)
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func smokeEnvironment(t *testing.T, material tlsMaterial, httpPort, httpsPort int) map[string]string {
	t.Helper()
	randomBytes := func(count int) []byte {
		value := make([]byte, count)
		if _, err := rand.Read(value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	hexSecret := func(count int) string { return hex.EncodeToString(randomBytes(count)) }
	textSecret := func(prefix string) string { return prefix + base64.RawURLEncoding.EncodeToString(randomBytes(24)) }
	return map[string]string{
		"HARDEN_LLM_API_HOST": "api.smoke.localhost", "HARDEN_LLM_GRAFANA_HOST": "grafana.smoke.localhost",
		"HARDEN_LLM_LANGFUSE_HOST": "langfuse.smoke.localhost", "HARDEN_LLM_ARTIFACT_HOST": "artifacts.smoke.localhost",
		"HARDEN_LLM_ARTIFACT_EXTERNAL_ENDPOINT": fmt.Sprintf("https://artifacts.smoke.localhost:%d", httpsPort),
		"HARDEN_LLM_BIND_ADDRESS":               "127.0.0.1", "HARDEN_LLM_HTTP_PORT": strconv.Itoa(httpPort),
		"HARDEN_LLM_HTTPS_PORT": strconv.Itoa(httpsPort), "HARDEN_LLM_TLS_MODE": "internal",
		"HARDEN_LLM_RELEASE": "compose-smoke-0.1.0", "HARDEN_LLM_ENVIRONMENT": "test",
		"HARDEN_LLM_POSTGRES_PASSWORD":        textSecret("db"),
		"HARDEN_LLM_ENCRYPTION_KEYS":          fmt.Sprintf(`{"primary":"%s"}`, base64.RawURLEncoding.EncodeToString(randomBytes(32))),
		"HARDEN_LLM_ACTIVE_ENCRYPTION_KEY_ID": "primary",
		"HARDEN_LLM_GARAGE_RPC_SECRET":        hexSecret(32), "HARDEN_LLM_ARTIFACT_BUCKET": "harden-llm-artifacts-smoke",
		"HARDEN_LLM_ARTIFACT_ACCESS_KEY_ID":     "GK" + strings.ToUpper(hexSecret(16)),
		"HARDEN_LLM_ARTIFACT_SECRET_ACCESS_KEY": hexSecret(32), "HARDEN_LLM_ARTIFACT_PRESIGN_TTL": "2m",
		"GRAFANA_ADMIN_USER": "smoke-admin", "GRAFANA_ADMIN_PASSWORD": textSecret("grafana"),
		"LANGFUSE_POSTGRES_PASSWORD": textSecret("lfdb"), "LANGFUSE_SALT": textSecret("salt"),
		"LANGFUSE_ENCRYPTION_KEY": hexSecret(32), "LANGFUSE_NEXTAUTH_SECRET": textSecret("auth"),
		"LANGFUSE_INIT_ORG_ID": "harden-llm-smoke", "LANGFUSE_INIT_ORG_NAME": "Harden LLM Smoke",
		"LANGFUSE_INIT_PROJECT_ID": "harden-llm-smoke", "LANGFUSE_INIT_PROJECT_NAME": "Harden LLM Smoke",
		"LANGFUSE_INIT_PROJECT_PUBLIC_KEY": textSecret("pk-lf-"), "LANGFUSE_INIT_PROJECT_SECRET_KEY": textSecret("sk-lf-"),
		"LANGFUSE_INIT_USER_EMAIL": "smoke@localhost.invalid", "LANGFUSE_INIT_USER_NAME": "Smoke Administrator",
		"LANGFUSE_INIT_USER_PASSWORD": textSecret("user"), "CLICKHOUSE_PASSWORD": textSecret("clickhouse"),
		"REDIS_AUTH": textSecret("redis"), "MINIO_ROOT_USER": "smokeminio", "MINIO_ROOT_PASSWORD": textSecret("minio"),
		"SMOKE_CA_CERT": material.ca, "SMOKE_PROVIDER_CERT": material.certificate, "SMOKE_PROVIDER_KEY": material.key,
	}
}

func sortedEnvironment(environment map[string]string) []string {
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+environment[key])
	}
	return result
}

func isSensitiveEnvironment(name string) bool {
	upper := strings.ToUpper(name)
	return strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "KEY") || strings.Contains(upper, "SALT")
}

type composeProcess struct {
	Name       string `json:"Name"`
	Service    string `json:"Service"`
	State      string `json:"State"`
	Health     string `json:"Health"`
	Publishers []struct {
		URL           string `json:"URL"`
		TargetPort    int    `json:"TargetPort"`
		PublishedPort int    `json:"PublishedPort"`
		Protocol      string `json:"Protocol"`
	} `json:"Publishers"`
}

func assertContainerTopology(t *testing.T, runner composeRunner) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := runner.output(ctx, "ps", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var processes []composeProcess
	if err := json.Unmarshal(output, &processes); err != nil {
		for _, line := range bytes.Split(bytes.TrimSpace(output), []byte("\n")) {
			var process composeProcess
			if decodeErr := json.Unmarshal(line, &process); decodeErr != nil {
				t.Fatalf("decode Compose ps: %v; output=%s", err, output)
			}
			processes = append(processes, process)
		}
	}
	byService := make(map[string]composeProcess, len(processes))
	for _, process := range processes {
		byService[process.Service] = process
	}
	ready := 0
	for _, service := range requiredProductionServices {
		process, ok := byService[service]
		if !ok {
			t.Errorf("Compose service %s has no running container", service)
			continue
		}
		if process.State != "running" || (process.Health != "" && process.Health != "healthy") {
			t.Errorf("Compose service %s state/health = %s/%s", service, process.State, process.Health)
			continue
		}
		ready++
	}
	if process, ok := byService["fake-provider"]; !ok || process.State != "running" || process.Health != "healthy" {
		t.Errorf("test-only fake-provider state = %#v", process)
	}
	for service, process := range byService {
		for _, publisher := range process.Publishers {
			if publisher.PublishedPort > 0 && service != "caddy" {
				t.Errorf("non-Caddy service %s publishes %s:%d", service, publisher.URL, publisher.PublishedPort)
			}
		}
	}
	if ready != len(requiredProductionServices) {
		t.Fatalf("ready production services = %d/%d", ready, len(requiredProductionServices))
	}
	return ready
}

func caddyClient(port int, stopRedirect bool) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		},
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}, // Caddy's ephemeral test CA is not host-trusted.
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 20 * time.Second,
		MaxIdleConns: 16, IdleConnTimeout: 30 * time.Second,
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	if stopRedirect {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return client
}

func waitHTTPStatus(t *testing.T, client *http.Client, target string, want int, budget time.Duration, configure func(*http.Request)) {
	t.Helper()
	deadline := time.Now().Add(budget)
	var last string
	for time.Now().Before(deadline) {
		request, _ := http.NewRequest(http.MethodGet, target, nil)
		if configure != nil {
			configure(request)
		}
		response, err := client.Do(request)
		if err == nil {
			body, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
			response.Body.Close()
			if response.StatusCode == want {
				return
			}
			last = fmt.Sprintf("%s: %s", response.Status, strings.TrimSpace(string(body)))
		} else {
			last = err.Error()
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("%s did not return %d within %s: %s", target, want, budget, last)
}

func requestJSON(t *testing.T, client *http.Client, method, target string, document any, token string, wantStatus int) map[string]any {
	t.Helper()
	var body io.Reader
	if document != nil {
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	if document != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("%s %s = %s, want %d: %s", method, target, response.Status, wantStatus, contents)
	}
	var envelope map[string]any
	if err := json.Unmarshal(contents, &envelope); err != nil {
		t.Fatalf("decode %s: %v: %s", target, err, contents)
	}
	return envelope
}

func artifactLocation(t *testing.T, client *http.Client, target, token string) string {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
		t.Fatalf("artifact redirect = %s: %s", response.Status, body)
	}
	location := response.Header.Get("Location")
	parsed, err := url.Parse(location)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != "artifacts.smoke.localhost" || parsed.Query().Get("X-Amz-Signature") == "" {
		t.Fatalf("artifact location is not a short-lived Caddy/Garage presign: %q", location)
	}
	return location
}

func fetchArtifact(t *testing.T, client *http.Client, location string) []byte {
	t.Helper()
	response, err := client.Get(location)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, 20<<20))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("presigned artifact GET = %s: %s", response.Status, contents)
	}
	return contents
}

func assertPostgresState(t *testing.T, runner composeRunner) {
	t.Helper()
	query := `SELECT (SELECT count(*) FROM llm_runs WHERE owner_id='smoke-owner'), (SELECT count(*) FROM llm_traces WHERE owner_id='smoke-owner'), (SELECT count(*) FROM llm_artifacts WHERE owner_id='smoke-owner' AND available);`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := runner.output(ctx, "exec", "-T", "-e", "PGPASSWORD="+runner.environment["HARDEN_LLM_POSTGRES_PASSWORD"],
		"harden-postgres", "psql", "-U", "harden_llm", "-d", "harden_llm", "-At", "-F", "|", "-c", query)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(output)) != "1|1|1" {
		t.Fatalf("application Postgres state = %q, want 1|1|1", strings.TrimSpace(string(output)))
	}
}

func correlateBackends(t *testing.T, runner composeRunner, client *http.Client, report ComposeReport, environment map[string]string) int {
	t.Helper()
	var otelTraceID string
	type probe struct {
		name string
		try  func() (bool, string)
	}
	probes := []probe{
		{name: "Tempo", try: func() (bool, string) {
			query := `{ span.harden_llm.trace.id = "` + report.TraceID + `" }`
			body, err := internalFetch(runner, "http://tempo:3200/api/search?q="+url.QueryEscape(query))
			otelTraceID = tempoTraceID(body)
			return err == nil && bytes.Contains(body, []byte(report.TraceID)) && otelTraceID != "", "otel_trace_id=" + otelTraceID + " " + boundedProbeDetail(body, err)
		}},
		{name: "Prometheus", try: func() (bool, string) {
			body, err := internalFetch(runner, "http://prometheus:9090/api/v1/query?query="+url.QueryEscape("harden_llm_calls"))
			return err == nil && prometheusHasSample(body), boundedProbeDetail(body, err)
		}},
		{name: "Loki", try: func() (bool, string) {
			// OTel log attributes are native Loki structured metadata. Select the
			// stable body, then assert the run_id metadata in the returned stream.
			query := `{service_name="harden-llm-gateway"} |= "run completed"`
			body, err := internalFetch(runner, "http://loki:3100/loki/api/v1/query_range?limit=100&direction=backward&query="+url.QueryEscape(query))
			return err == nil && bytes.Contains(body, []byte(report.RunID)), boundedProbeDetail(body, err)
		}},
		{name: "Langfuse", try: func() (bool, string) {
			if otelTraceID == "" {
				return false, "waiting for Tempo OTel trace identity"
			}
			list, listErr := publicGET(client, "https://langfuse.smoke.localhost/api/public/traces?limit=100", environment["LANGFUSE_INIT_PROJECT_PUBLIC_KEY"], environment["LANGFUSE_INIT_PROJECT_SECRET_KEY"])
			matches := langfuseTraceIDMatches(list, otelTraceID)
			trace, traceErr := publicGET(client, "https://langfuse.smoke.localhost/api/public/traces/"+url.PathEscape(otelTraceID), environment["LANGFUSE_INIT_PROJECT_PUBLIC_KEY"], environment["LANGFUSE_INIT_PROJECT_SECRET_KEY"])
			ok := listErr == nil && traceErr == nil && matches == 1 && bytes.Contains(trace, []byte(report.TraceID))
			return ok, fmt.Sprintf("otel_trace_id=%s matches=%d list=%s trace=%s", otelTraceID, matches, boundedProbeDetail(list, listErr), boundedProbeDetail(trace, traceErr))
		}},
		{name: "Garage", try: func() (bool, string) { return true, "artifact bytes and metadata matched" }},
	}
	deadline := time.Now().Add(correlationWait)
	completed := make(map[string]bool, len(probes))
	details := make(map[string]string, len(probes))
	for time.Now().Before(deadline) {
		for _, probe := range probes {
			if completed[probe.name] {
				continue
			}
			ok, detail := probe.try()
			details[probe.name] = detail
			if ok {
				completed[probe.name] = true
			}
		}
		if len(completed) == len(probes) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	for _, probe := range probes {
		if !completed[probe.name] {
			t.Errorf("%s correlation missing after %s: %s", probe.name, correlationWait, details[probe.name])
		}
	}
	return len(completed)
}

func internalFetch(runner composeRunner, target string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return runner.output(ctx, "exec", "-T", "fake-provider", "/fake-provider", "fetch", "--url", target)
}

func publicGET(client *http.Client, target, username, password string) ([]byte, error) {
	request, _ := http.NewRequest(http.MethodGet, target, nil)
	request.SetBasicAuth(username, password)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return body, fmt.Errorf("%s", response.Status)
	}
	return body, nil
}

func prometheusHasSample(body []byte) bool {
	var response struct {
		Status string `json:"status"`
		Data   struct {
			Result []json.RawMessage `json:"result"`
		} `json:"data"`
	}
	return json.Unmarshal(body, &response) == nil && response.Status == "success" && len(response.Data.Result) > 0
}

func langfuseTraceIDMatches(body []byte, otelTraceID string) int {
	var response struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &response) != nil {
		return 0
	}
	matches := 0
	for _, trace := range response.Data {
		if trace.ID == otelTraceID {
			matches++
		}
	}
	return matches
}

func tempoTraceID(body []byte) string {
	var response struct {
		Traces []struct {
			TraceID string `json:"traceID"`
		} `json:"traces"`
	}
	if json.Unmarshal(body, &response) != nil || len(response.Traces) != 1 {
		return ""
	}
	return normalizeTempoTraceID(response.Traces[0].TraceID)
}

func boundedProbeDetail(body []byte, err error) string {
	if err != nil {
		return err.Error()
	}
	if len(body) > 512 {
		body = body[:512]
	}
	return strings.TrimSpace(string(body))
}

func assertGrafanaDatasources(t *testing.T, client *http.Client, username, password string) {
	t.Helper()
	for _, uid := range []string{"harden-prometheus", "harden-loki", "harden-tempo"} {
		target := "https://grafana.smoke.localhost/api/datasources/uid/" + uid + "/health"
		waitHTTPStatus(t, client, target, http.StatusOK, 45*time.Second, func(request *http.Request) {
			request.SetBasicAuth(username, password)
		})
	}
}

func assertLiveStorageOwnership(t *testing.T, runner composeRunner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	config, err := runner.output(ctx, "config", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var effective struct {
		Services map[string]struct {
			Environment map[string]string `json:"environment"`
		} `json:"services"`
	}
	if err := json.Unmarshal(config, &effective); err != nil {
		t.Fatal(err)
	}
	gateway := strings.ToLower(mustJSON(effective.Services["harden-llm-gateway"].Environment))
	if !strings.Contains(gateway, "garage:3900") || strings.Contains(gateway, "minio") {
		t.Fatal("live gateway storage environment is not Garage-only")
	}
	for _, service := range []string{"langfuse-web", "langfuse-worker"} {
		encoded := strings.ToLower(mustJSON(effective.Services[service].Environment))
		if !strings.Contains(encoded, "minio:9000") || strings.Contains(encoded, "garage") {
			t.Fatalf("live %s storage environment is not MinIO-only", service)
		}
	}
}

func mustJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func object(t *testing.T, value any, label string) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want object", label, value)
	}
	return result
}

func array(t *testing.T, value any, label string) []any {
	t.Helper()
	result, ok := value.([]any)
	if !ok {
		t.Fatalf("%s = %#v, want array", label, value)
	}
	return result
}

func text(t *testing.T, value any, label string) string {
	t.Helper()
	result, ok := value.(string)
	if !ok {
		t.Fatalf("%s = %#v, want string", label, value)
	}
	return result
}

func integer(t *testing.T, value any, label string) int64 {
	t.Helper()
	number, ok := value.(float64)
	if !ok || number < 0 || number != float64(int64(number)) {
		t.Fatalf("%s = %#v, want non-negative integer", label, value)
	}
	return int64(number)
}
