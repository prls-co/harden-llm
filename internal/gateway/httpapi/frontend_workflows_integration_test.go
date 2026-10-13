//go:build integration && frontendintegration

package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-454

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
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
		presetNativeResponse(w, r)
	}
	f := referenceHTTPConfigured(t, false, false, catalog, native)
	root, err := filepath.Abs("../../../frontend")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "mix", "test", "test/harden_llm_web/live/gateway_workflows_test.exs", "--include", "database", "--seed", "104729")
	command.Dir = root
	command.Env = append(os.Environ(), "PORT=0", "HARDEN_LLM_TEST_GATEWAY_URL="+f.server.URL, "HARDEN_LLM_TEST_GATEWAY_TOKEN="+f.keyA, "MIX_BUILD_PATH="+filepath.Join(t.TempDir(), "mix-build"))
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatalf("real LiveView/gateway workflow: %v", err)
	}
	expected := []string{"gpt-5.6-luna", "fixture-original-repair-initial", "fixture-original-repair-escalation", "fixture-rerun-generation", "fixture-rerun-repair-initial", "fixture-rerun-repair-escalation"}
	if !reflect.DeepEqual(recoveryModels, expected) {
		t.Fatalf("actual recovery provider routing = %v, expected %v", recoveryModels, expected)
	}
}
