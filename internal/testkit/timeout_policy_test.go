package testkit_test

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-039

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

type timeoutManifest struct {
	SchemaVersion       int             `json:"schemaVersion"`
	IncreaseEvidenceDir string          `json:"increaseEvidenceDirectory"`
	ClientTimeoutPolicy string          `json:"clientTimeoutPolicy"`
	RequiredRCAFields   []string        `json:"requiredRCAFields"`
	Policies            []timeoutPolicy `json:"policies"`
}

type timeoutPolicy struct {
	ID                         string `json:"id"`
	Path                       string `json:"path"`
	Pattern                    string `json:"pattern"`
	UnitMultiplierMilliseconds int64  `json:"unitMultiplierMilliseconds"`
	BaselineMilliseconds       int64  `json:"baselineMilliseconds"`
	InitialBaseline            bool   `json:"initialBaseline"`
	Basis                      string `json:"basis"`
}

type timeoutRCA struct {
	PolicyID                      string    `json:"policyId"`
	Phase                         string    `json:"phase"`
	StartProof                    string    `json:"startProof"`
	FailedTimingsMilliseconds     []float64 `json:"failedTimingsMilliseconds"`
	ComparableSuccessMilliseconds []float64 `json:"comparableSuccessesMilliseconds"`
	P95Milliseconds               float64   `json:"p95Milliseconds"`
	MaximumMilliseconds           float64   `json:"maximumMilliseconds"`
	PreviousTimeoutMilliseconds   int64     `json:"previousTimeoutMilliseconds"`
	ConfiguredTimeoutMilliseconds int64     `json:"configuredTimeoutMilliseconds"`
	HeadroomMilliseconds          int64     `json:"headroomMilliseconds"`
	RootCause                     string    `json:"rootCause"`
	Rationale                     string    `json:"rationale"`
}

func TestTimeoutPolicy(t *testing.T) {
	root := repositoryRoot(t)
	manifestPath := filepath.Join(root, "ker", "timeouts", "baseline.json")
	var manifest timeoutManifest
	if err := json.Unmarshal(readFile(t, manifestPath), &manifest); err != nil {
		t.Fatalf("parse timeout baseline: %v", err)
	}
	if manifest.SchemaVersion != 1 || len(manifest.Policies) == 0 {
		t.Fatal("timeout baseline must use schema version 1 and contain policies")
	}
	if filepath.ToSlash(manifest.IncreaseEvidenceDir) != "ker/timeouts/rca" {
		t.Fatalf("unexpected timeout RCA directory %q", manifest.IncreaseEvidenceDir)
	}
	if !strings.Contains(strings.ToLower(manifest.ClientTimeoutPolicy), "does not infer or pad") {
		t.Fatal("backend timeout policy must not infer or pad a frontend client timeout")
	}
	wantFields := []string{
		"policyId", "phase", "startProof", "failedTimingsMilliseconds",
		"comparableSuccessesMilliseconds", "p95Milliseconds", "maximumMilliseconds",
		"previousTimeoutMilliseconds", "configuredTimeoutMilliseconds",
		"headroomMilliseconds", "rootCause", "rationale",
	}
	if strings.Join(manifest.RequiredRCAFields, "\x00") != strings.Join(wantFields, "\x00") {
		t.Fatalf("timeout RCA fields = %v, want %v", manifest.RequiredRCAFields, wantFields)
	}

	rcas := readTimeoutRCAs(t, filepath.Join(root, filepath.FromSlash(manifest.IncreaseEvidenceDir)))
	seen := make(map[string]bool)
	for _, policy := range manifest.Policies {
		if policy.ID == "" || seen[policy.ID] {
			t.Fatalf("timeout policy ID %q is empty or duplicated", policy.ID)
		}
		seen[policy.ID] = true
		if strings.HasPrefix(filepath.ToSlash(policy.Path), "frontend/") || strings.HasPrefix(filepath.ToSlash(policy.Path), "deploy/frontend/") {
			t.Fatalf("backend timeout manifest includes frontend path %q", policy.Path)
		}
		if policy.BaselineMilliseconds <= 0 || policy.UnitMultiplierMilliseconds <= 0 || !policy.InitialBaseline || strings.TrimSpace(policy.Basis) == "" {
			t.Fatalf("timeout policy %s has an incomplete certified baseline", policy.ID)
		}
		actual := timeoutValue(t, root, policy)
		if actual <= policy.BaselineMilliseconds {
			continue
		}
		if !hasSupportingRCA(rcas, policy, actual) {
			t.Errorf("timeout %s increased from certified baseline %dms to %dms without a complete RCA", policy.ID, policy.BaselineMilliseconds, actual)
		}
	}

	gateway, ok := policyByID(manifest.Policies, "gateway.maximum-run-duration")
	if !ok || gateway.BaselineMilliseconds != 60_000 {
		t.Fatal("baseline must record the 60-second gateway maximum run duration")
	}
	compose, ok := policyByID(manifest.Policies, "compose.readiness-budget")
	if !ok || compose.BaselineMilliseconds != 300_000 || !strings.Contains(strings.ToLower(compose.Basis), "langfuse") || !strings.Contains(strings.ToLower(compose.Basis), "two-to-three-minute") {
		t.Fatal("initial 300-second Compose baseline must record its Langfuse startup basis")
	}
}

func TestTimeoutPolicyRejectsUnsupportedIncrease(t *testing.T) {
	policy := timeoutPolicy{ID: "gateway.maximum-run-duration", BaselineMilliseconds: 60_000}
	if hasSupportingRCA(nil, policy, 65_000) {
		t.Fatal("an unsupported timeout increase was accepted")
	}
	complete := timeoutRCA{
		PolicyID: policy.ID, Phase: "P07 / TEST-039", StartProof: "evidence/run/start.json",
		FailedTimingsMilliseconds: []float64{60_001}, ComparableSuccessMilliseconds: []float64{58_000, 59_000},
		P95Milliseconds: 59_000, MaximumMilliseconds: 60_001,
		PreviousTimeoutMilliseconds: 60_000, ConfiguredTimeoutMilliseconds: 65_000,
		HeadroomMilliseconds: 4_999, RootCause: "measured upstream delay", Rationale: "bounded measured headroom",
	}
	if !hasSupportingRCA([]timeoutRCA{complete}, policy, 65_000) {
		t.Fatal("a complete matching timeout RCA was rejected")
	}
	complete.RootCause = ""
	if hasSupportingRCA([]timeoutRCA{complete}, policy, 65_000) {
		t.Fatal("an incomplete timeout RCA was accepted")
	}
}

func timeoutValue(t *testing.T, root string, policy timeoutPolicy) int64 {
	t.Helper()
	contents := string(readFile(t, filepath.Join(root, filepath.FromSlash(policy.Path))))
	pattern, err := regexp.Compile(policy.Pattern)
	if err != nil {
		t.Fatalf("compile timeout pattern for %s: %v", policy.ID, err)
	}
	matches := pattern.FindAllStringSubmatch(contents, -1)
	if len(matches) != 1 || len(matches[0]) != 2 {
		t.Fatalf("timeout pattern for %s matched %d values in %s", policy.ID, len(matches), policy.Path)
	}
	value, err := strconv.ParseInt(matches[0][1], 10, 64)
	if err != nil || value <= 0 {
		t.Fatalf("parse timeout for %s: %q: %v", policy.ID, matches[0][1], err)
	}
	return value * policy.UnitMultiplierMilliseconds
}

func readTimeoutRCAs(t *testing.T, directory string) []timeoutRCA {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read timeout RCA directory: %v", err)
	}
	var result []timeoutRCA
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var record timeoutRCA
		if err := json.Unmarshal(readFile(t, filepath.Join(directory, entry.Name())), &record); err != nil {
			t.Fatalf("parse timeout RCA %s: %v", entry.Name(), err)
		}
		result = append(result, record)
	}
	return result
}

func hasSupportingRCA(records []timeoutRCA, policy timeoutPolicy, actual int64) bool {
	for _, record := range records {
		if record.PolicyID != policy.ID || record.ConfiguredTimeoutMilliseconds != actual || record.PreviousTimeoutMilliseconds <= 0 ||
			record.PreviousTimeoutMilliseconds > policy.BaselineMilliseconds || actual <= record.PreviousTimeoutMilliseconds {
			continue
		}
		if strings.TrimSpace(record.Phase) == "" || strings.TrimSpace(record.StartProof) == "" || len(record.FailedTimingsMilliseconds) == 0 ||
			len(record.ComparableSuccessMilliseconds) == 0 || record.P95Milliseconds <= 0 || record.MaximumMilliseconds <= 0 ||
			record.HeadroomMilliseconds <= 0 || strings.TrimSpace(record.RootCause) == "" || strings.TrimSpace(record.Rationale) == "" {
			continue
		}
		if !positiveTimings(record.FailedTimingsMilliseconds) || !positiveTimings(record.ComparableSuccessMilliseconds) || record.P95Milliseconds > record.MaximumMilliseconds {
			continue
		}
		if actual < int64(record.MaximumMilliseconds)+record.HeadroomMilliseconds {
			continue
		}
		return true
	}
	return false
}

func positiveTimings(values []float64) bool {
	for _, value := range values {
		if value <= 0 {
			return false
		}
	}
	return true
}

func policyByID(policies []timeoutPolicy, id string) (timeoutPolicy, bool) {
	for _, policy := range policies {
		if policy.ID == id {
			return policy, true
		}
	}
	return timeoutPolicy{}, false
}
