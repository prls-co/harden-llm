//go:build compose

package smoke

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
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

	"github.com/prls-co/harden-llm/internal/integrationtest"
)

const (
	readinessBudget = 300 * time.Second
	correlationWait = 150 * time.Second
)

var requiredSmokeStackServices = []string{
	"caddy", "harden-llm-gateway", "garage", "otel-collector",
	"laminar", "prometheus", "loki", "tempo", "grafana",
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
	if strings.TrimSpace(os.Getenv("PRIVATE_MODULE_TOKEN")) == "" {
		t.Fatal("PRIVATE_MODULE_TOKEN is required to build the private Control Plane Go module")
	}
	secrets["PRIVATE_MODULE_TOKEN"] = os.Getenv("PRIVATE_MODULE_TOKEN")
	composeFiles := []string{
		filepath.Join(root, "docker-compose.yml"),
		filepath.Join(root, "deploy", "test", "compose.smoke.yml"),
	}
	runner := composeRunner{
		root: root, project: project, environment: secrets, files: composeFiles,
	}
	receiptPath, err := integrationtest.RegisterResourceReceipt(project, composeFiles)
	if err != nil {
		t.Fatalf("register Compose smoke ownership before Docker mutation: %v", err)
	}
	if err := integrationtest.AdvanceResourceReceipt(receiptPath, "creating"); err != nil {
		t.Fatalf("record Compose smoke creation state before Docker mutation: %v", err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Compose diagnostics before cleanup:\n%s", runner.diagnostics())
		}
		stateErr := integrationtest.AdvanceResourceReceipt(receiptPath, "cleaning")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cleanupErr := runner.run(ctx, nil, "down", "--volumes", "--remove-orphans", "--timeout", "20")
		if stateErr != nil {
			_ = integrationtest.AdvanceResourceReceipt(receiptPath, "cleanup-pending")
			t.Errorf("record Compose smoke cleanup state: %v", stateErr)
			if cleanupErr != nil {
				t.Errorf("Compose cleanup: %v", cleanupErr)
			}
			return
		}
		if cleanupErr != nil {
			_ = integrationtest.AdvanceResourceReceipt(receiptPath, "cleanup-pending")
			t.Errorf("Compose cleanup: %v", cleanupErr)
			return
		}
		if err := integrationtest.AdvanceResourceReceipt(receiptPath, "cleaned"); err != nil {
			t.Errorf("record Compose smoke cleanup completion: %v", err)
		}
	})

	pullContext, cancelPull := context.WithTimeout(context.Background(), 20*time.Minute)
	if err := runner.run(pullContext, nil, "pull", "--ignore-buildable"); err != nil {
		cancelPull()
		t.Fatalf("pre-pull pinned images: %v", err)
	}
	cancelPull()
	validateFrontendSmokeCaddyfile(t, runner)

	started := time.Now()
	startContext, cancelStart := context.WithTimeout(context.Background(), 6*time.Minute)
	err = runner.run(startContext, nil, "up", "-d", "--build", "--wait", "--wait-timeout", "300")
	cancelStart()
	if err != nil {
		t.Fatalf("start Compose stack: %v\n%s", err, runner.diagnostics())
	}
	if err := integrationtest.AdvanceResourceReceipt(receiptPath, "running"); err != nil {
		t.Fatalf("record Compose smoke startup: %v", err)
	}
	readiness := time.Since(started)
	if readiness > readinessBudget {
		t.Fatalf("Compose readiness = %s, budget %s", readiness, readinessBudget)
	}

	report := ComposeReport{TotalServices: len(requiredSmokeStackServices), Readiness: readiness, CorrelationBackends: 4}
	report.ReadyServices = assertContainerTopology(t, runner)
	client := caddyClient(httpsPort, false)
	waitHTTPStatus(t, client, "https://api.smoke.localhost/readyz", http.StatusOK, 45*time.Second, nil)
	waitHTTPStatus(t, client, "https://grafana.smoke.localhost/api/health", http.StatusOK, 45*time.Second, nil)

	token := secrets["HARDEN_LLM_TOKEN"]
	assertProxyComposeBoundary(t, runner)
	requestJSON(t, client, http.MethodGet, "https://api.smoke.localhost/v1/models", nil, "", http.StatusUnauthorized)
	models := requestJSON(t, client, http.MethodGet, "https://api.smoke.localhost/v1/models", nil, token, http.StatusOK)
	modelData := array(t, models["data"], "model list data")
	if len(modelData) != 1 || object(t, modelData[0], "model")["id"] != "smoke-model" {
		t.Fatalf("model discovery = %#v", models)
	}
	response := requestJSON(t, client, http.MethodPost, "https://api.smoke.localhost/v1/responses", map[string]any{
		"model": "smoke-model", "input": "return the smoke response", "store": false,
		"harden": map[string]any{
			"upstream": "smoke", "cache": "off", "diagnostics": true,
			"recovery": map[string]any{
				"maxAttempts": 1, "retryOn": []string{},
				"backoff":    map[string]any{"baseDelayMs": 0, "maxDelayMs": 0},
				"jsonRepair": nil, "rerun": nil,
			},
		},
	}, token, http.StatusOK)
	if response["output_text"] != "smoke-ok" {
		t.Fatalf("standard Responses output = %#v", response["output_text"])
	}
	harden := object(t, response["harden"], "harden diagnostics")
	report.RunID = text(t, harden["execution_id"], "execution ID")
	report.TraceID = text(t, harden["trace_id"], "trace ID")
	if report.RunID == "" || report.TraceID == "" {
		t.Fatal("Responses result returned empty diagnostic IDs")
	}
	requestJSON(t, client, http.MethodGet, "https://api.smoke.localhost/api/v1/history", nil, token, http.StatusNotFound)
	requestJSON(t, client, http.MethodPost, "https://api.smoke.localhost/api/v1/run", map[string]any{}, token, http.StatusNotFound)
	correlation := correlateBackends(t, runner, report)
	report.CorrelatedBackends = correlation
	if correlation != report.CorrelationBackends {
		t.Fatalf("backend correlation = %d/%d", correlation, report.CorrelationBackends)
	}
	assertGrafanaDatasources(t, client, secrets["GRAFANA_ADMIN_USER"], secrets["GRAFANA_ADMIN_PASSWORD"])
	assertProxyComposeBoundary(t, runner)

	t.Logf("Compose evidence: ready=%d/%d readiness=%s correlation=%d/%d run=%s trace=%s",
		report.ReadyServices, report.TotalServices, report.Readiness.Round(time.Millisecond),
		report.CorrelatedBackends, report.CorrelationBackends, report.RunID, report.TraceID)
	return report
}

func validateFrontendSmokeCaddyfile(t *testing.T, runner composeRunner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	config, err := runner.output(ctx, "config", "--format", "json")
	if err != nil {
		t.Fatalf("render smoke Compose before validating frontend Caddy config: %v", err)
	}
	var model struct {
		Services map[string]struct {
			Image string `json:"image"`
		} `json:"services"`
	}
	if err := json.Unmarshal(config, &model); err != nil {
		t.Fatalf("decode smoke Compose image for Caddy config validation: %v", err)
	}
	caddy, exists := model.Services["caddy"]
	if !exists || caddy.Image == "" {
		t.Fatalf("smoke Compose has no pinned Caddy image for frontend config validation")
	}

	environment := make(map[string]string, len(runner.environment)+1)
	for name, value := range runner.environment {
		environment[name] = value
	}
	environment["HARDEN_LLM_WEB_HOST"] = "app.smoke.localhost"
	environment["PRLS_PORTAL_HOST"] = "portal.smoke.localhost"
	arguments := []string{
		"run", "--rm", "--network", "none",
		"--label", "com.docker.compose.project=" + runner.project,
		"--mount", fmt.Sprintf("type=bind,source=%s,target=/etc/caddy/Caddyfile,readonly", filepath.Join(runner.root, "deploy", "test", "Caddyfile.frontend-smoke")),
		"--mount", fmt.Sprintf("type=bind,source=%s,target=/etc/caddy/test,readonly", filepath.Join(runner.root, "deploy", "test")),
	}
	for _, name := range []string{
		"HARDEN_LLM_API_HOST", "HARDEN_LLM_GRAFANA_HOST",
		"HARDEN_LLM_TLS_MODE", "HARDEN_LLM_WEB_HOST", "PRLS_PORTAL_HOST",
	} {
		value, exists := environment[name]
		if !exists || value == "" {
			t.Fatalf("synthetic smoke Caddy configuration omits %s", name)
		}
		arguments = append(arguments, "--env", name+"="+value)
	}
	arguments = append(arguments, caddy.Image, "caddy", "validate", "--config", "/etc/caddy/Caddyfile")
	command := exec.CommandContext(ctx, "docker", arguments...)
	command.Dir = runner.root
	command.Env = append(os.Environ(), sortedEnvironment(environment)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("validate frontend smoke Caddyfile with the pinned image: %v: %s", err, runner.redact(string(output)))
	}
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
	observabilityLogs, _ := runner.output(ctx, "logs", "--no-color", "--tail", "40", "otel-collector", "loki")
	return runner.redact("COMPOSE PS\n" + string(ps) + "\nCONTAINER STATES\n" + string(states) + "\nRECENT LOGS\n" + string(logs) + "\nGATEWAY AND PROVIDER LOGS\n" + string(serviceLogs) + "\nOBSERVABILITY LOGS\n" + string(observabilityLogs))
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
	configPath := filepath.Join(filepath.Dir(material.ca), "upstreams.json")
	connectionConfig := map[string]any{
		"default_upstream": "smoke",
		"upstreams": []map[string]any{{
			"id": "smoke", "provider": "openai", "protocol": "responses",
			"base_url": "https://fake-provider:8443/v1", "api_key_env": "CPA_API_KEY",
			"cache_domain": "smoke", "supports_web_search": false,
		}},
	}
	configContents, err := json.Marshal(connectionConfig)
	if err != nil {
		t.Fatal(err)
	}
	// The file contains only non-secret connection metadata; the gateway runs
	// unprivileged and receives provider credentials through its environment.
	if err := os.WriteFile(configPath, configContents, 0o644); err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"HARDEN_LLM_API_HOST": "api.smoke.localhost", "HARDEN_LLM_GRAFANA_HOST": "grafana.smoke.localhost",
		"PRLS_SMOKE_OBSERVABILITY_NETWORK":   "prls-observability-smoke-" + hexSecret(8),
		"PRLS_LAMINAR_PROJECT_API_KEY":       textSecret("lmnr"),
		"HARDEN_LLM_LAMINAR_PROJECT_API_KEY": textSecret("lmnr-harden"),
		"HARDEN_LLM_LAMINAR_ENDPOINT":        "laminar:8001",
		"HARDEN_LLM_BIND_ADDRESS":            "127.0.0.1", "HARDEN_LLM_HTTP_PORT": strconv.Itoa(httpPort),
		"HARDEN_LLM_HTTPS_PORT": strconv.Itoa(httpsPort), "HARDEN_LLM_TLS_MODE": "internal",
		"HARDEN_LLM_RELEASE": "compose-smoke-0.1.0", "HARDEN_LLM_ENVIRONMENT": "test",
		"PRLS_PORTAL_URL":                         "https://a.prls.co",
		"HARDEN_LLM_CONTROL_PLANE_URL":            "http://control-plane:4310",
		"HARDEN_LLM_CONTROL_PLANE_INTERNAL_TOKEN": textSecret("control-plane-smoke-"),
		"HARDEN_LLM_CONFIG_FILE":                  configPath,
		"HARDEN_LLM_TOKEN":                        textSecret("harden-llm-smoke-"),
		"CPA_API_KEY":                             textSecret("smoke-provider-"),
		"HARDEN_LLM_POSTGRES_PASSWORD":            textSecret("db"),
		"GARAGE_RPC_SECRET":                       hexSecret(32), "GARAGE_SMOKE_BUCKET": "harden-llm-observability-smoke",
		"GARAGE_SMOKE_ACCESS_KEY": "GK" + strings.ToUpper(hexSecret(16)),
		"GARAGE_SMOKE_SECRET_KEY": hexSecret(32),
		"PRLS_LOKI_S3_ACCESS_KEY": "GK" + strings.ToUpper(hexSecret(16)), "PRLS_LOKI_S3_SECRET_KEY": hexSecret(32),
		"GRAFANA_ADMIN_USER": "smoke-admin", "GRAFANA_ADMIN_PASSWORD": textSecret("grafana"),
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
	return strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "KEY") || strings.Contains(upper, "SALT") || strings.Contains(upper, "TOKEN")
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
	for _, service := range requiredSmokeStackServices {
		process, ok := byService[service]
		if !ok {
			t.Errorf("Compose smoke stack service %s has no running container", service)
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
	if ready != len(requiredSmokeStackServices) {
		t.Fatalf("ready smoke stack services = %d/%d", ready, len(requiredSmokeStackServices))
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

func correlateBackends(t *testing.T, runner composeRunner, report ComposeReport) int {
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
			query := `{service_name="harden-llm-gateway"} |= "http request completed"`
			body, err := internalFetch(runner, "http://loki:3100/loki/api/v1/query_range?limit=100&direction=backward&query="+url.QueryEscape(query))
			return err == nil && hasCorrelatedResponseLog(body, otelTraceID), boundedProbeDetail(body, err)
		}},
		{name: "Laminar", try: func() (bool, string) {
			if otelTraceID == "" {
				return false, "waiting for Tempo OTel trace identity"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			body, err := runner.output(ctx, "logs", "--no-log-prefix", "--tail", "3000", "laminar")
			if err != nil {
				return false, "Laminar test sink log query failed"
			}
			lowerBody := bytes.ToLower(body)
			otelTracePresent := bytes.Contains(lowerBody, bytes.ToLower([]byte(otelTraceID)))
			domainTracePresent := bytes.Contains(lowerBody, bytes.ToLower([]byte(report.TraceID)))
			return otelTracePresent && domainTracePresent, fmt.Sprintf("otel_trace_id_present=%t application_trace_id_present=%t", otelTracePresent, domainTracePresent)
		}},
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

func assertProxyComposeBoundary(t *testing.T, runner composeRunner) {
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
			Volumes     []struct {
				Source string `json:"source"`
				Target string `json:"target"`
			} `json:"volumes"`
		} `json:"services"`
	}
	if err := json.Unmarshal(config, &effective); err != nil {
		t.Fatal(err)
	}
	gateway := effective.Services["harden-llm-gateway"]
	if _, exists := effective.Services["harden-postgres"]; exists {
		t.Fatal("proxy-only Compose unexpectedly starts the reference database")
	}
	if gateway.Environment["HARDEN_LLM_TOKEN"] != runner.environment["HARDEN_LLM_TOKEN"] ||
		gateway.Environment["CPA_API_KEY"] != runner.environment["CPA_API_KEY"] ||
		gateway.Environment["HARDEN_LLM_TOKEN"] == gateway.Environment["CPA_API_KEY"] {
		t.Fatal("gateway does not keep its incoming bearer and upstream key distinct")
	}
	if gateway.Environment["HARDEN_LLM_CONFIG_FILE"] != "/etc/harden-llm/upstreams.json" {
		t.Fatal("gateway does not load the single mounted connection catalog")
	}
	for _, key := range []string{"HARDEN_LLM_DATABASE_URL", "HARDEN_LLM_ARTIFACT_ENDPOINT", "HARDEN_LLM_CONTROL_PLANE_URL"} {
		if _, exists := gateway.Environment[key]; exists {
			t.Fatalf("proxy gateway still receives retired application setting %s", key)
		}
	}
	configSource := false
	for _, volume := range gateway.Volumes {
		if volume.Target == "/etc/harden-llm/upstreams.json" && volume.Source == runner.environment["HARDEN_LLM_CONFIG_FILE"] {
			configSource = true
		}
	}
	if !configSource {
		t.Fatal("gateway Compose does not mount the configured connection JSON read-only")
	}
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
