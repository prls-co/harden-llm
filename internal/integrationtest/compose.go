//go:build integration

// Package integrationtest owns isolated local Compose test infrastructure.
package integrationtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Service is one isolated Compose service and its dynamically published port.
type Service struct {
	Endpoint      string
	composeFile   string
	containerPort int
	project       string
	service       string
}

const composeOperationTimeout = 2 * time.Minute

// StartPostgres starts the dedicated application database used by integration tests.
func StartPostgres(t testing.TB) (*Service, string) {
	t.Helper()
	service := start(t, "harden-postgres", 5432)
	dsn := fmt.Sprintf("postgres://harden_test:harden_test_password@%s/harden_llm_test?sslmode=disable", service.Endpoint)
	return service, dsn
}

// Garage describes the isolated bucket-scoped S3 test configuration.
type Garage struct {
	Endpoint        string
	Bucket          string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
}

// StartGarage starts the pinned Garage service with one default private bucket.
func StartGarage(t testing.TB) (*Service, Garage) {
	t.Helper()
	service := start(t, "garage", 3900)
	return service, Garage{
		Endpoint:        "http://" + service.Endpoint,
		Bucket:          "harden-llm-artifacts-test",
		Region:          "garage",
		AccessKeyID:     "GK00000000000000000000000000000000",
		SecretAccessKey: "0000000000000000000000000000000000000000000000000000000000000000",
	}
}

// Restart recreates the same service while retaining its project volumes.
func (service *Service) Restart(t testing.TB) {
	t.Helper()
	service.run(t, "restart", service.service)
	service.refreshEndpoint(t)
	waitTCP(t, service.Endpoint, 45*time.Second)
}

func start(t testing.TB, serviceName string, containerPort int) *Service {
	t.Helper()
	root := repositoryRoot(t)
	composeFile := filepath.Join(root, "deploy", "test", "compose.integration.yml")
	project := "harden-llm-" + strings.ReplaceAll(serviceName, "_", "-") + "-" + randomSuffix(t)
	service := &Service{composeFile: composeFile, containerPort: containerPort, project: project, service: serviceName}
	// Prefer the pinned local image and only contact the registry when it is
	// missing. An unconditional pull makes otherwise-hermetic integration tests
	// depend on registry availability and can hang before the test starts.
	service.run(t, "up", "-d", "--wait", "--pull", "missing", serviceName)
	service.refreshEndpoint(t)
	waitTCP(t, service.Endpoint, 45*time.Second)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "docker", "compose", "-f", composeFile, "-p", project, "down", "-v", "--remove-orphans")
		_ = command.Run()
	})
	return service
}

func (service *Service) refreshEndpoint(t testing.TB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), composeOperationTimeout)
	defer cancel()
	command := service.commandContext(ctx, "port", service.service, fmt.Sprintf("%d", service.containerPort))
	output, err := command.CombinedOutput()
	if err != nil {
		service.fail(t, "resolve published port", err, output)
	}
	endpoint := strings.TrimSpace(string(output))
	if host, port, splitErr := net.SplitHostPort(endpoint); splitErr == nil {
		if host == "0.0.0.0" || host == "::" {
			host = "127.0.0.1"
		}
		endpoint = net.JoinHostPort(host, port)
	}
	service.Endpoint = endpoint
}

func (service *Service) run(t testing.TB, arguments ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), composeOperationTimeout)
	defer cancel()
	command := service.commandContext(ctx, arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		service.fail(t, strings.Join(arguments, " "), err, output)
	}
}

func (service *Service) command(arguments ...string) *exec.Cmd {
	base := []string{"compose", "-f", service.composeFile, "-p", service.project}
	return exec.Command("docker", append(base, arguments...)...)
}

func (service *Service) commandContext(ctx context.Context, arguments ...string) *exec.Cmd {
	base := []string{"compose", "-f", service.composeFile, "-p", service.project}
	return exec.CommandContext(ctx, "docker", append(base, arguments...)...)
}

func (service *Service) fail(t testing.TB, operation string, err error, output []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	logs, _ := service.commandContext(ctx, "logs", "--no-color", service.service).CombinedOutput()
	t.Fatalf("compose %s: %v\n%s\n%s", operation, err, output, logs)
}

func waitTCP(t testing.TB, endpoint string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", endpoint, 500*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("service %s did not accept TCP connections: %v", endpoint, lastErr)
}

func repositoryRoot(t testing.TB) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(directory, "go.mod")); statErr == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root not found")
		}
		directory = parent
	}
}

func randomSuffix(t testing.TB) string {
	t.Helper()
	value := make([]byte, 6)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(value)
}
