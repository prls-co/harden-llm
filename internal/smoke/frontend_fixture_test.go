//go:build compose

package smoke

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFrontendComposeFixture owns the real 16-service topology while
// WEB-TEST-012 drives Chromium and probes telemetry from the Elixir test.
// It is inert outside that coordinated gate.
func TestFrontendComposeFixture(t *testing.T) {
	statePath := os.Getenv("HARDEN_LLM_FRONTEND_SMOKE_STATE")
	donePath := os.Getenv("HARDEN_LLM_FRONTEND_SMOKE_DONE")
	workDir := os.Getenv("HARDEN_LLM_FRONTEND_SMOKE_DIR")
	if statePath == "" || donePath == "" || workDir == "" {
		t.Skip("frontend Compose fixture is only started by WEB-TEST-012")
	}

	if err := os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	root := repositoryRoot(t)
	material := generateTLSMaterial(t, workDir)
	httpPort := freeTCPPort(t)
	httpsPort := freeTCPPort(t)
	for httpsPort == httpPort {
		httpsPort = freeTCPPort(t)
	}

	project := fmt.Sprintf("harden-llm-web-smoke-%d-%d", os.Getpid(), time.Now().UnixNano())
	environment := smokeEnvironment(t, material, httpPort, httpsPort)
	environment["HARDEN_LLM_WEB_HOST"] = "app.smoke.localhost"
	environment["HARDEN_LLM_WEB_SECRET_KEY_BASE"] = fixtureSecret(t, "web-secret-", 64)
	environment["HARDEN_LLM_WEB_SESSION_SIGNING_SALT"] = fixtureSecret(t, "sign-", 24)
	environment["HARDEN_LLM_WEB_SESSION_ENCRYPTION_SALT"] = fixtureSecret(t, "encrypt-", 24)
	environment["HARDEN_LLM_WEB_INSTANCE_ID"] = "harden-llm-web-smoke-1"
	environment["HARDEN_LLM_WEB_API_TIMEOUT_MS"] = "3000"

	files := []string{
		filepath.Join(root, "docker-compose.yml"),
		filepath.Join(root, "deploy", "langfuse", "docker-compose.upstream.yml"),
		filepath.Join(root, "deploy", "langfuse", "compose.private.yml"),
		filepath.Join(root, "deploy", "test", "compose.smoke.yml"),
		filepath.Join(root, "deploy", "frontend", "compose.frontend.yml"),
	}
	runner := composeRunner{root: root, project: project, environment: environment, files: files}
	_ = runner.run(context.Background(), nil, "down", "--volumes", "--remove-orphans", "--timeout", "10")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := runner.run(ctx, nil, "down", "--volumes", "--remove-orphans", "--timeout", "20"); err != nil {
			t.Logf("frontend Compose cleanup: %v", err)
		}
	})

	pullContext, cancelPull := context.WithTimeout(context.Background(), 20*time.Minute)
	if err := runner.run(pullContext, nil, "pull", "--ignore-buildable"); err != nil {
		cancelPull()
		t.Fatalf("pre-pull frontend Compose images: %v", err)
	}
	cancelPull()

	started := time.Now()
	startContext, cancelStart := context.WithTimeout(context.Background(), 7*time.Minute)
	err := runner.run(startContext, nil, "up", "-d", "--build", "--wait", "--wait-timeout", "360")
	cancelStart()
	if err != nil {
		t.Fatalf("start frontend Compose stack: %v\n%s", err, runner.diagnostics())
	}
	readiness := time.Since(started)
	if readiness > 6*time.Minute {
		t.Fatalf("frontend Compose readiness = %s, budget 6m", readiness)
	}

	if ready := assertContainerTopology(t, runner); ready != len(requiredProductionServices) {
		t.Fatalf("base topology ready = %d/%d", ready, len(requiredProductionServices))
	}
	assertFrontendService(t, runner)

	client := caddyClient(httpsPort, false)
	waitHTTPStatus(t, client, "https://app.smoke.localhost/healthz", 200, 45*time.Second, nil)
	waitHTTPStatus(t, client, "https://api.smoke.localhost/readyz", 200, 45*time.Second, nil)
	waitHTTPStatus(t, client, "https://grafana.smoke.localhost/api/health", 200, 45*time.Second, nil)
	waitHTTPStatus(t, client, "https://langfuse.smoke.localhost/api/public/health", 200, 90*time.Second, nil)

	bootstrapPassword := fixtureSecret(t, "Web-smoke-password-", 24)
	bootstrapContext, cancelBootstrap := context.WithTimeout(context.Background(), 45*time.Second)
	if err := runner.run(
		bootstrapContext,
		strings.NewReader(bootstrapPassword+"\n"),
		"run",
		"--rm",
		"-T",
		"harden-llm-gateway",
		"bootstrap-user",
		"--owner-id",
		"web-smoke-owner",
		"--email",
		"web-smoke@example.test",
		"--password-file",
		"-",
	); err != nil {
		cancelBootstrap()
		t.Fatalf("bootstrap frontend smoke user: %v", err)
	}
	cancelBootstrap()

	envFile := filepath.Join(workDir, "compose.env")
	if err := writeEnvironmentFile(envFile, environment); err != nil {
		t.Fatal(err)
	}

	state := frontendFixtureState{
		Project:         project,
		HTTPSPort:       httpsPort,
		WebURL:          fmt.Sprintf("https://app.smoke.localhost:%d", httpsPort),
		GrafanaURL:      fmt.Sprintf("https://grafana.smoke.localhost:%d", httpsPort),
		GrafanaUser:     environment["GRAFANA_ADMIN_USER"],
		GrafanaPassword: environment["GRAFANA_ADMIN_PASSWORD"],
		LoginEmail:      "web-smoke@example.test",
		LoginPassword:   bootstrapPassword,
		EnvironmentFile: envFile,
		ComposeFiles:    files,
		ReadinessMS:     readiness.Milliseconds(),
	}
	writeFrontendFixtureState(t, statePath, state)

	deadline := time.Now().Add(12 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(donePath); err == nil {
			t.Logf("Frontend Compose fixture released: ready=16/16 readiness=%s", readiness.Round(time.Millisecond))
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("WEB-TEST-012 did not release the frontend Compose fixture within 12 minutes")
}

type frontendFixtureState struct {
	Project         string   `json:"project"`
	HTTPSPort       int      `json:"https_port"`
	WebURL          string   `json:"web_url"`
	GrafanaURL      string   `json:"grafana_url"`
	GrafanaUser     string   `json:"grafana_user"`
	GrafanaPassword string   `json:"grafana_password"`
	LoginEmail      string   `json:"login_email"`
	LoginPassword   string   `json:"login_password"`
	EnvironmentFile string   `json:"environment_file"`
	ComposeFiles    []string `json:"compose_files"`
	ReadinessMS     int64    `json:"readiness_ms"`
}

func assertFrontendService(t *testing.T, runner composeRunner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := runner.output(ctx, "ps", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var processes []composeProcess
	if err := json.Unmarshal(output, &processes); err != nil {
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			var process composeProcess
			if decodeErr := json.Unmarshal([]byte(line), &process); decodeErr != nil {
				t.Fatalf("decode frontend Compose ps: %v; output=%s", decodeErr, output)
			}
			processes = append(processes, process)
		}
	}
	for _, process := range processes {
		if process.Service == "harden-llm-web" {
			if process.State != "running" || process.Health != "healthy" {
				t.Fatalf("frontend service state/health = %s/%s", process.State, process.Health)
			}
			for _, publisher := range process.Publishers {
				if publisher.PublishedPort > 0 {
					t.Fatalf("frontend service publishes host port %d", publisher.PublishedPort)
				}
			}
			return
		}
	}
	t.Fatal("frontend service has no running container")
}

func fixtureSecret(t *testing.T, prefix string, count int) string {
	t.Helper()
	value := make([]byte, count)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(value)
}

func writeEnvironmentFile(path string, environment map[string]string) error {
	var contents strings.Builder
	for _, entry := range sortedEnvironment(environment) {
		contents.WriteString(entry)
		contents.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(contents.String()), 0o600)
}

func writeFrontendFixtureState(t *testing.T, path string, state frontendFixtureState) {
	t.Helper()
	contents, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, path); err != nil {
		t.Fatal(err)
	}
}
