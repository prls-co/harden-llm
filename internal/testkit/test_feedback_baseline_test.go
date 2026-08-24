package testkit_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-048

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const testFeedbackBaselineSHA = "009629211632beed029374549938d1e322fcba04"

type testFeedbackManifest struct {
	SchemaVersion            int                        `json:"schemaVersion"`
	ResourceClasses          map[string]json.RawMessage `json:"resourceClasses"`
	FrontendSerialExceptions []json.RawMessage          `json:"frontendSerialExceptions"`
	Tasks                    []testFeedbackManifestTask `json:"tasks"`
}

type testFeedbackManifestTask struct {
	ID               string   `json:"id"`
	TestIDs          []string `json:"testIds"`
	Tier             string   `json:"tier"`
	ResourceClass    string   `json:"resourceClass"`
	Command          []string `json:"command"`
	WorkingDirectory string   `json:"workingDirectory"`
	DependsOn        []string `json:"dependsOn"`
	TimeoutMS        int      `json:"timeoutMs"`
	CleanupOwner     string   `json:"cleanupOwner"`
	Network          string   `json:"network"`
	CredentialKeys   []string `json:"credentialKeys"`
	RequiredFor      []string `json:"requiredFor"`
	PathSelectors    []string `json:"pathSelectors"`
}

func TestTestFeedbackBaselineContract(t *testing.T) {
	root := repositoryRoot(t)

	manifestPath := filepath.Join(root, "test", "test-tiers.json")
	manifestBytes := readFile(t, manifestPath)
	var manifest testFeedbackManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("parse %s: %v", manifestPath, err)
	}
	if manifest.SchemaVersion != 1 {
		t.Fatalf("manifest schemaVersion = %d, want 1", manifest.SchemaVersion)
	}
	if len(manifest.ResourceClasses) == 0 {
		t.Fatal("manifest has no resourceClasses")
	}
	if len(manifest.Tasks) == 0 {
		t.Fatal("manifest has no tasks")
	}

	seenTaskIDs := make(map[string]struct{}, len(manifest.Tasks))
	seenTestIDs := make(map[string]string)
	for index, task := range manifest.Tasks {
		if task.ID == "" {
			t.Errorf("task %d has no id", index)
		}
		if _, exists := seenTaskIDs[task.ID]; exists {
			t.Errorf("task %q is duplicated", task.ID)
		}
		seenTaskIDs[task.ID] = struct{}{}
		if len(task.TestIDs) == 0 {
			t.Errorf("task %q has no canonical test IDs", task.ID)
		}
		if task.Tier != "T0" && task.Tier != "T1" && task.Tier != "T2" && task.Tier != "T3" && task.Tier != "T4" && task.Tier != "T5" {
			t.Errorf("task %q has invalid tier %q", task.ID, task.Tier)
		}
		if _, exists := manifest.ResourceClasses[task.ResourceClass]; !exists {
			t.Errorf("task %q references unknown resource class %q", task.ID, task.ResourceClass)
		}
		if len(task.Command) == 0 || task.Command[0] == "" {
			t.Errorf("task %q has no executable command", task.ID)
		}
		if task.TimeoutMS <= 0 {
			t.Errorf("task %q has non-positive timeoutMs", task.ID)
		}
		if task.CleanupOwner == "" {
			t.Errorf("task %q has no cleanupOwner", task.ID)
		}
		if task.Network == "" {
			t.Errorf("task %q has no network policy", task.ID)
		}
		for _, testID := range task.TestIDs {
			if previous, exists := seenTestIDs[testID]; exists {
				t.Errorf("test ID %q is assigned to both %q and %q", testID, previous, task.ID)
			}
			seenTestIDs[testID] = task.ID
		}
		if task.Tier == "T0" || task.Tier == "T1" || task.Tier == "T2" {
			if task.Network != "forbidden" {
				t.Errorf("cheap task %q has network policy %q, want forbidden", task.ID, task.Network)
			}
			if len(task.CredentialKeys) != 0 {
				t.Errorf("cheap task %q declares credential keys", task.ID)
			}
		}
	}

	expectedCommands := map[string][]string{
		"make format":                  {"make", "format"},
		"make lint":                    {"make", "lint"},
		"make build":                   {"make", "build"},
		"make test-static":             {"make", "test-static"},
		"make test-unit":               {"make", "test-unit"},
		"make test-parity":             {"make", "test-parity"},
		"make test-integration":        {"make", "test-integration"},
		"make test-integration-race":   {"make", "test-integration-race"},
		"make test-api":                {"make", "test-api"},
		"make test-observability":      {"make", "test-observability"},
		"make test-compose":            {"make", "test-compose"},
		"make test-race":               {"make", "test-race"},
		"make test-vulnerability":      {"make", "test-vulnerability"},
		"make live-structured-call":    {"make", "live-structured-call"},
		"mix format --check-formatted": {"mix", "format", "--check-formatted"},
		"mix compile":                  {"mix", "compile", "--warnings-as-errors"},
		"mix test":                     {"mix", "test"},
		"mix browser":                  {"mix", "test", "--only", "browser", "--max-cases", "1"},
		"mix compose":                  {"mix", "test", "--only", "compose", "--max-cases", "1"},
		"mix deps.audit":               {"mix", "deps.audit"},
		"mix hex.audit":                {"mix", "hex.audit"},
		"mix assets.deploy":            {"mix", "assets.deploy"},
		"mix release":                  {"mix", "release"},
	}
	commandKeys := make(map[string]struct{}, len(manifest.Tasks))
	for _, task := range manifest.Tasks {
		commandKeys[strings.Join(task.Command, " ")] = struct{}{}
	}
	for name, command := range expectedCommands {
		if _, exists := commandKeys[strings.Join(command, " ")]; !exists {
			t.Errorf("manifest is missing current command %q", name)
		}
	}

	benchmarkPath := filepath.Join(root, "scripts", "benchmark-test-feedback.mjs")
	benchmark := string(readFile(t, benchmarkPath))
	for _, required := range []string{"performance.now", "schemaVersion", "time -v", "/proc", "sha256"} {
		if !strings.Contains(benchmark, required) {
			t.Errorf("benchmark harness does not contain required primitive %q", required)
		}
	}

	kerPath := filepath.Join(root, "ker", "test-feedback", "baseline.json")
	var ker struct {
		SchemaVersion   int                        `json:"schemaVersion"`
		DocumentID      string                     `json:"documentId"`
		KERID           string                     `json:"kerId"`
		HostFingerprint map[string]json.RawMessage `json:"hostFingerprint"`
		ExecutionStatus string                     `json:"executionStatus"`
		Evaluations     map[string]json.RawMessage `json:"evaluations"`
		AcceptedFields  []string                   `json:"acceptedEvaluationFields"`
		Reference       struct {
			EvidencePath      string  `json:"evidencePath"`
			RawEvidenceSHA256 string  `json:"rawEvidenceSha256"`
			ManifestSHA256    string  `json:"manifestSha256"`
			WarmSamples       int     `json:"warmSamples"`
			ColdSamples       int     `json:"coldSamples"`
			MaxVariation      float64 `json:"maxCoefficientOfVariation"`
		} `json:"reference"`
	}
	if err := json.Unmarshal(readFile(t, kerPath), &ker); err != nil {
		t.Fatalf("parse %s: %v", kerPath, err)
	}
	if ker.SchemaVersion != 1 || ker.DocumentID != "PLAN-HARDEN-LLM-TEST-FEEDBACK-002" || ker.KERID != "KER-HLLM-TEST-FEEDBACK-001" {
		t.Errorf("KER identity is incomplete: schema=%d document=%q ker=%q", ker.SchemaVersion, ker.DocumentID, ker.KERID)
	}
	if ker.ExecutionStatus != "not_run" && ker.ExecutionStatus != "measured" {
		t.Errorf("KER executionStatus = %q, want not_run or measured", ker.ExecutionStatus)
	}
	if ker.Evaluations == nil {
		t.Error("initial KER evaluations must be an object")
	}
	if ker.ExecutionStatus == "measured" {
		if len(ker.Evaluations) == 0 {
			t.Error("measured KER evaluations must not be empty")
		}
		if ker.Reference.EvidencePath == "" || !strings.HasPrefix(ker.Reference.EvidencePath, "plans/evidence/") {
			t.Errorf("measured KER evidencePath = %q, want ignored plans/evidence path", ker.Reference.EvidencePath)
		}
		if len(ker.Reference.RawEvidenceSHA256) != 64 || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(ker.Reference.RawEvidenceSHA256) {
			t.Errorf("measured KER rawEvidenceSha256 = %q, want SHA-256", ker.Reference.RawEvidenceSHA256)
		}
		if ker.Reference.WarmSamples != 5 || ker.Reference.ColdSamples != 3 {
			t.Errorf("measured KER samples = warm %d cold %d, want warm 5 cold 3", ker.Reference.WarmSamples, ker.Reference.ColdSamples)
		}
		if ker.Reference.MaxVariation != 0.2 {
			t.Errorf("measured KER maxCoefficientOfVariation = %v, want 0.2", ker.Reference.MaxVariation)
		}
		for key, rawEvaluation := range ker.Evaluations {
			var evaluation struct {
				Accepted    bool `json:"accepted"`
				SampleCount int  `json:"sampleCount"`
				WallTimeMS  struct {
					P50, P95, Max          float64
					CoefficientOfVariation float64 `json:"coefficientOfVariation"`
				} `json:"wallTimeMs"`
				PeakRSSMiB struct {
					Max                    float64
					CoefficientOfVariation float64 `json:"coefficientOfVariation"`
				} `json:"peakRssMiB"`
				FailureCount        int    `json:"failureCount"`
				LeakedResourceCount int    `json:"leakedResourceCount"`
				RawEvidenceSHA256   string `json:"rawEvidenceSha256"`
			}
			if err := json.Unmarshal(rawEvaluation, &evaluation); err != nil {
				t.Errorf("evaluation %q is invalid: %v", key, err)
				continue
			}
			if !evaluation.Accepted || (evaluation.SampleCount != 3 && evaluation.SampleCount != 5) || evaluation.WallTimeMS.P50 <= 0 || evaluation.WallTimeMS.P95 <= 0 || evaluation.WallTimeMS.Max <= 0 || evaluation.PeakRSSMiB.Max <= 0 || evaluation.FailureCount != 0 || evaluation.LeakedResourceCount != 0 {
				t.Errorf("evaluation %q is not an accepted nonzero clean aggregate: %+v", key, evaluation)
			}
			if evaluation.WallTimeMS.CoefficientOfVariation > ker.Reference.MaxVariation || evaluation.PeakRSSMiB.CoefficientOfVariation > ker.Reference.MaxVariation {
				t.Errorf("evaluation %q exceeds coefficient-of-variation limit", key)
			}
			if evaluation.RawEvidenceSHA256 != ker.Reference.RawEvidenceSHA256 {
				t.Errorf("evaluation %q raw evidence hash does not match reference", key)
			}
		}
	}
	for _, field := range []string{"accepted", "sampleCount", "wallTimeMs.p50", "wallTimeMs.p95", "wallTimeMs.max", "peakRssMiB.max", "failureCount", "leakedResourceCount", "rawEvidenceSha256"} {
		if !containsString(ker.AcceptedFields, field) {
			t.Errorf("KER acceptedEvaluationFields missing %q", field)
		}
	}
	for _, field := range []string{"os", "physicalCpuCount", "logicalCpuCount", "memoryMiB", "goVersion", "nodeVersion", "dockerVersion", "composeVersion"} {
		if _, exists := ker.HostFingerprint[field]; !exists {
			t.Errorf("KER hostFingerprint missing %q", field)
		}
	}

	adrPath := filepath.Join(root, "docs", "adr", "ADR-HLLM-015-parallel-test-feedback-hierarchy.md")
	adr := string(readFile(t, adrPath))
	if !strings.Contains(adr, "ADR-HLLM-015") || !strings.Contains(strings.ToLower(adr), "no initial synthetic dom") {
		t.Error("ADR-HLLM-015 is missing the accepted hierarchy/no-DOM decision")
	}
	adrIndex := string(readFile(t, filepath.Join(root, "docs", "adr", "README.md")))
	if strings.Count(adrIndex, "| [ADR-HLLM-015]") != 1 {
		t.Errorf("ADR index contains ADR-HLLM-015 row %d times, want once", strings.Count(adrIndex, "| [ADR-HLLM-015]"))
	}

	var status map[string]json.RawMessage
	statusPath := filepath.Join(root, "plans", "implementation-status.json")
	if err := json.Unmarshal(readFile(t, statusPath), &status); err != nil {
		t.Fatalf("parse %s: %v", statusPath, err)
	}
	var hierarchy map[string]json.RawMessage
	if raw, ok := status["testFeedbackHierarchy"]; !ok {
		t.Fatal("implementation status has no top-level testFeedbackHierarchy object")
	} else if err := json.Unmarshal(raw, &hierarchy); err != nil || len(hierarchy) == 0 {
		t.Fatal("implementation status testFeedbackHierarchy is not a non-empty object")
	}

	for _, path := range []string{manifestPath, benchmarkPath, kerPath, adrPath} {
		if err := rejectCredentialMaterial(t, path, string(readFile(t, path))); err != nil {
			t.Error(err)
		}
	}
	if !strings.Contains(string(readFile(t, statusPath)), testFeedbackBaselineSHA) {
		t.Errorf("implementation status does not record baseline SHA %s", testFeedbackBaselineSHA)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func rejectCredentialMaterial(t *testing.T, path, contents string) error {
	t.Helper()
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)sk-[A-Za-z0-9]{16,}`),
		regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._-]{16,}`),
		regexp.MustCompile(`(?i)(password|secret|api[_-]?key|access[_-]?key)\s*[:=]\s*[^\s"']+`),
	}
	for _, pattern := range patterns {
		if match := pattern.FindString(contents); match != "" {
			return fmt.Errorf("%s contains credential-shaped material %q", path, match)
		}
	}
	return nil
}
