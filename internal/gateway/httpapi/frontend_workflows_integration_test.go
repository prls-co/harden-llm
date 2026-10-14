//go:build integration && frontendintegration

package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-454 WEB-TEST-126

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prls-co/harden-llm/stateful"
)

// This explicit T3 task connects real LiveView events and the real UI client to
// the Go gateway, native provider fixtures and an isolated PostgreSQL database.
// It is separate from Go-only integration and the browser-free fast loop.
func TestFrontendPresetWorkflows(t *testing.T) {
	catalog, err := stateful.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	var mutex sync.Mutex
	var recoveryModels []string
	var expected []nativeRequestExpectation
	counts := map[string]int{}
	native := func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(data))
		var input map[string]any
		_ = json.Unmarshal(data, &input)
		model, _ := input["model"].(string)
		if strings.HasPrefix(model, "fixture-") || bytes.Contains(data, []byte("fixture-force-recovery")) {
			mutex.Lock()
			recoveryModels = append(recoveryModels, model)
			mutex.Unlock()
		}
		if r.Method == http.MethodPost {
			mutex.Lock()
			if len(expected) == 0 {
				mutex.Unlock()
				t.Error("unexpected native dispatch")
				http.Error(w, "unexpected dispatch", 400)
				return
			}
			next := expected[0]
			expected = expected[1:]
			mutex.Unlock()
			if err := verifyNativeRequest(r, next); err != nil {
				t.Errorf("native composition oracle: %v", err)
				http.Error(w, "native oracle rejected request", 400)
				return
			}
		}
		presetNativeResponse(w, r)
	}
	f := referenceHTTPConfigured(t, false, false, catalog, native)
	f.server.Close()
	handler := f.api.Handler()
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/run" {
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				http.Error(w, "invalid fixture body", 400)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(data))
			var input stateful.RunInput
			if err = json.Unmarshal(data, &input); err != nil {
				t.Error(err)
				http.Error(w, "invalid fixture input", 400)
				return
			}
			queue, err := workflowNativeExpectations(catalog, input)
			if err != nil {
				t.Error(err)
				http.Error(w, "invalid native expectations", 400)
				return
			}
			mutex.Lock()
			if len(expected) != 0 {
				t.Error("previous run did not consume its expected dispatches")
			}
			expected = queue
			if input.UserPrompt != "fixture-force-recovery" {
				counts[input.ProfileID+"/"+string(input.CallType)]++
			}
			mutex.Unlock()
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(f.server.Close)

	root, err := filepath.Abs("../../../frontend")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "mix", "test", "test/harden_llm_web/live/gateway_workflows_test.exs", "--only", "preset_workflow", "--seed", "104729")
	command.Dir = root
	command.Env = append(os.Environ(), "PORT=0", "HARDEN_LLM_TEST_GATEWAY_URL="+f.server.URL, "HARDEN_LLM_TEST_GATEWAY_TOKEN="+f.keyA, "MIX_BUILD_PATH="+filepath.Join(t.TempDir(), "mix-build"))
	output, err := command.CombinedOutput()
	logFrontendTestOutput(t, output)
	if err != nil {
		t.Fatalf("real LiveView/gateway workflow: %v", err)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(expected) != 0 {
		t.Error("unconsumed native expectations")
	}
	for id := range catalog {
		for _, mode := range []string{"text", "structured"} {
			if counts[id+"/"+mode] != 1 {
				t.Errorf("preset/mode %s/%s executes %d times, want 1", id, mode, counts[id+"/"+mode])
			}
		}
	}
	expectedModels := []string{"gpt-5.6-luna", "fixture-original-repair-initial", "fixture-original-repair-escalation", "fixture-rerun-generation", "fixture-rerun-repair-initial", "fixture-rerun-repair-escalation"}
	if !reflect.DeepEqual(recoveryModels, expectedModels) {
		t.Fatalf("actual recovery provider routing = %v, expected %v", recoveryModels, expectedModels)
	}
}

func logFrontendTestOutput(t *testing.T, output []byte) {
	t.Helper()
	// Private build paths compile dependencies once. Preserve test failures in
	// the runner's bounded preview instead of filling it with compiler chatter.
	if start := bytes.Index(output, []byte("Running ExUnit")); start >= 0 {
		output = output[start:]
	}
	t.Log(string(output))
}
