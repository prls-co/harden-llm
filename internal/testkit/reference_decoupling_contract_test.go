package testkit_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-420

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type referenceWorkflowManifest struct {
	SchemaVersion   int      `json:"schemaVersion"`
	BaselineSHA     string   `json:"baselineSHA"`
	AcceptedChanges []string `json:"acceptedChanges"`
	Sources         []struct {
		Path   string   `json:"path"`
		SHA256 string   `json:"sha256"`
		IDs    []string `json:"ids"`
		Events []string `json:"events"`
	} `json:"sources"`
	Capabilities []struct {
		ID    string `json:"id"`
		Owner string `json:"owner"`
		Phase string `json:"phase"`
	} `json:"capabilities"`
}

func TestReferenceDecouplingContract(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	var manifest referenceWorkflowManifest
	if err := json.Unmarshal(readFile(t, filepath.Join(root, "test/fixtures/reference-workflows.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != 1 || manifest.BaselineSHA != "754690c8d0890c664d69fbdf526b16b3e5601060" {
		t.Fatal("restoration must use the pinned pre-PR-97 HLLM baseline")
	}
	wantChanges := []string{"api-key-scoped-state-with-per-portal-user-drafts", "complete-create-on-save-profile-snapshots"}
	if strings.Join(manifest.AcceptedChanges, "|") != strings.Join(wantChanges, "|") {
		t.Error("only the two accepted product deltas may change baseline capabilities")
	}
	if len(manifest.Sources) != 12 || len(manifest.Capabilities) != 32 {
		t.Error("the frozen source/capability manifest is incomplete")
	}
	seen := map[string]bool{}
	for _, capability := range manifest.Capabilities {
		if capability.ID == "" || seen[capability.ID] || capability.Owner == "" || (capability.Phase != "P03" && capability.Phase != "P04") {
			t.Errorf("capability has no unique restoration owner: %+v", capability)
		}
		seen[capability.ID] = true
	}
	for _, source := range manifest.Sources {
		cmd := exec.Command("git", "show", manifest.BaselineSHA+":"+source.Path)
		cmd.Dir = root
		contents, err := cmd.Output()
		if err != nil {
			t.Fatalf("read pinned baseline %s: %v", source.Path, err)
		}
		digest := sha256.Sum256(contents)
		if hex.EncodeToString(digest[:]) != source.SHA256 {
			t.Errorf("baseline source provenance changed: %s", source.Path)
		}
		for _, id := range source.IDs {
			if !strings.Contains(string(contents), `id="`+id+`"`) {
				t.Errorf("baseline selector %q is absent from %s", id, source.Path)
			}
		}
		for _, event := range source.Events {
			if !strings.Contains(string(contents), `="`+event+`"`) {
				t.Errorf("baseline event %q is absent from %s", event, source.Path)
			}
		}
	}

	policies := map[string][]string{
		"AGENTS.md":            {"api_key_hash", "stateful/", "HardenLlmUI", "Agent Platform", "create-on-save"},
		"docs/architecture.md": {"api_key_hash", "stateful/", "HardenLlmUI", "create-on-save", "Model Recall"},
		"plans/from_utility-llm/self-hosted-go-stack-spec.md":        {"ADR-HLLM-033/034", "api_key_hash", "stateful/", "HardenLlmUI"},
		"plans/from_utility-llm/harden-llm-self-hosted-test-spec.md": {"ADR-HLLM-033", "api_key_hash", "create-on-save", "TEST-420", "TEST-436"},
		"plans/from_utility-llm/phoenix-liveview-frontend-spec.md":   {"HardenLlmUI", "api_key_hash", "draftKey", "create-on-save", "Model Recall"},
	}
	for path, required := range policies {
		contents := string(readFile(t, filepath.Join(root, path)))
		for _, value := range required {
			if !strings.Contains(contents, value) {
				t.Errorf("canonical policy %s does not declare accepted reference ownership %q", path, value)
			}
		}
	}
	var contract struct {
		Paths     map[string]any        `yaml:"paths"`
		Security  []map[string][]string `yaml:"security"`
		Ownership map[string]any        `yaml:"x-harden-llm-ownership"`
	}
	if err := yaml.Unmarshal(readFile(t, filepath.Join(root, "api/openapi.yaml")), &contract); err != nil {
		t.Fatal(err)
	}
	if len(contract.Security) != 1 || contract.Security[0]["bearerAuth"] == nil {
		// An empty YAML sequence has a non-nil decoded slice.
		t.Error("both REST surfaces must share exactly one bearer boundary")
	}
	for _, path := range []string{"/v1/models", "/v1/chat/completions", "/v1/responses", "/api/v1/profiles", "/api/v1/run", "/api/v1/state", "/api/v1/history"} {
		if _, exists := contract.Paths[path]; !exists {
			t.Errorf("canonical REST contract lacks preserved operation %s", path)
		}
	}
	wantOwnership := map[string]string{
		"scope": "api_key_hash", "scopeDerivation": "sha256(validated bearer)",
		"profileSave": "create-on-save", "history": "reference-only PostgreSQL",
		"credentials": "deployment-env or encrypted-entered", "admission": "frontend-only Portal",
		"draftKey": "host-supplied inside key scope", "statelessCore": "injected effects without auth or storage",
	}
	for key, want := range wantOwnership {
		if contract.Ownership[key] != want {
			t.Errorf("OpenAPI ownership %s = %v, want %q", key, contract.Ownership[key], want)
		}
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Uses string         `yaml:"uses"`
				With map[string]any `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(readFile(t, filepath.Join(root, ".github/workflows/test-hierarchy.yml")), &workflow); err != nil {
		t.Fatal(err)
	}
	for job, config := range workflow.Jobs {
		for _, step := range config.Steps {
			if step.Uses == "actions/checkout@v4" && step.With["fetch-depth"] != 0 {
				t.Errorf("hosted job %s cannot verify offline baseline provenance with a shallow checkout", job)
			}
		}
	}
}
