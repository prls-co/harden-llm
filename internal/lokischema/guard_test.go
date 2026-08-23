package lokischema

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-030

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommittedSchemaStateMatchesCandidate(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	result, err := ValidateFiles(
		filepath.Join(root, "deploy", "loki", "schema-periods.lock.yaml"),
		filepath.Join(root, "deploy", "loki", "loki.yaml"),
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.AcceptedPeriods != 2 || len(result.NewPeriods) != 0 {
		t.Fatalf("unexpected committed schema result: %#v", result)
	}
}

const currentPeriod = `
from: "2024-01-01"
store: tsdb
object_store: filesystem
schema: v13
index:
  prefix: index_
  period: 24h`

func stateFor(t *testing.T, periodYAML string) []byte {
	t.Helper()
	var wrapper struct {
		Period Period `yaml:"period"`
	}
	if err := yamlUnmarshal([]byte("period:"+strings.ReplaceAll(periodYAML, "\n", "\n  ")), &wrapper); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := Fingerprint(wrapper.Period)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(fmt.Sprintf(`schema_version: %s
recorded_at: "2026-08-23T13:15:00Z"
periods:
  - from: %q
    fingerprint_sha256: %s
`, StateSchemaVersion, wrapper.Period.From, fingerprint))
}

func candidate(periods ...string) []byte {
	return []byte("schema_config:\n  configs:" + strings.Join(periods, "") + "\n")
}

func indentedPeriod(period string) string {
	return strings.ReplaceAll(period, "\n", "\n    - ")
}

func TestValidateAcceptsUnchangedStateAndFutureAppend(t *testing.T) {
	state := stateFor(t, currentPeriod)
	base := "\n    -" + strings.ReplaceAll(currentPeriod, "\n", "\n     ")
	future := `
from: "2026-08-24"
store: tsdb
object_store: s3
schema: v13
index:
  prefix: prls_index_
  period: 24h`
	futureItem := "\n    -" + strings.ReplaceAll(future, "\n", "\n     ")

	result, err := Validate(state, candidate(base), time.Date(2026, 8, 23, 20, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("unchanged candidate rejected: %v", err)
	}
	if result.AcceptedPeriods != 1 || len(result.NewPeriods) != 0 {
		t.Fatalf("unexpected unchanged result: %#v", result)
	}
	result, err = Validate(state, candidate(base, futureItem), time.Date(2026, 8, 23, 20, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("future append rejected: %v", err)
	}
	if len(result.NewPeriods) != 1 || result.NewPeriods[0] != "2026-08-24" {
		t.Fatalf("unexpected append result: %#v", result)
	}
}

func TestValidateRejectsSameDayAppend(t *testing.T) {
	state := stateFor(t, currentPeriod)
	base := "\n    -" + strings.ReplaceAll(currentPeriod, "\n", "\n     ")
	sameDay := strings.ReplaceAll(base, "2024-01-01", "2026-08-23")
	_, err := Validate(state, candidate(base, sameDay), time.Date(2026, 8, 23, 1, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "must activate after") {
		t.Fatalf("same-day append error = %v", err)
	}
}

func TestValidateRejectsRemovalOrMutation(t *testing.T) {
	state := stateFor(t, currentPeriod)
	_, err := Validate(state, candidate(), time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("removed deployed period was accepted")
	}
	mutated := strings.ReplaceAll(currentPeriod, "prefix: index_", "prefix: changed_")
	item := "\n    -" + strings.ReplaceAll(mutated, "\n", "\n     ")
	_, err = Validate(state, candidate(item), time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "mutates deployed") {
		t.Fatalf("mutated deployed period error = %v", err)
	}
}
