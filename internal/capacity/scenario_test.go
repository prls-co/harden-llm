// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-276

package capacity

import (
	"os"
	"strings"
	"testing"
)

func TestCapacityDriverScenarioCatalogIsBoundedAndStrict(t *testing.T) {
	data, err := os.ReadFile("../../test/capacity-scenarios.json")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := ParseScenarioCatalog(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, caseSet := range []string{"correctness", "exploration", "holdout"} {
		if len(catalog.CaseSets[caseSet]) == 0 {
			t.Errorf("scenario catalog has no %q cases", caseSet)
		}
		for _, scenario := range catalog.CaseSets[caseSet] {
			if _, err := BuildArrivalSchedule(scenario.DriverScenario()); err != nil {
				t.Errorf("scenario %s has invalid driver schedule: %v", scenario.ID, err)
			}
		}
	}
	var cacheHit ScenarioSpec
	for _, scenario := range catalog.CaseSets["correctness"] {
		if scenario.CacheMode == "hit" {
			cacheHit = scenario
		}
	}
	if cacheHit.ID == "" || cacheHit.WarmupSeconds == 0 || cacheHit.MaxRequests < cacheHit.OfferedRPS*(cacheHit.WarmupSeconds+cacheHit.MeasurementSeconds) {
		t.Fatalf("cache-hit case cannot seed and measure a stable cache key: %#v", cacheHit)
	}
	converted := cacheHit.DriverScenario()
	if converted.CacheMode != "hit" || converted.RecoveryPolicy != "default" || converted.Origin.OperationID != cacheHit.ID || converted.Origin.TestID != "TEST-277" {
		t.Fatalf("scenario settings or origin were lost during driver conversion: %#v", converted)
	}

	valid := `{"schemaVersion":1,"caseSets":{"correctness":[{"id":"valid","seed":104729,"offeredRps":1,"warmupSeconds":0,"measurementSeconds":1,"drainSeconds":1,"maxRequests":1,"maxInflight":1,"providerScript":"success","expectedOutcome":"succeeded","cacheMode":"miss","recoveryPolicy":"off","telemetryMode":"disabled"}]}}`
	if _, err := ParseScenarioCatalog([]byte(valid)); err != nil {
		t.Fatalf("valid minimal catalog rejected: %v", err)
	}
	unknownField := strings.Replace(valid, `"id":"valid"`, `"id":"valid","providerEndpoint":"https://api.openai.com"`, 1)
	if _, err := ParseScenarioCatalog([]byte(unknownField)); err == nil {
		t.Fatal("arbitrary provider endpoint must be rejected by strict scenario decoding")
	}
	tooLarge := strings.Replace(valid, `"maxRequests":1`, `"maxRequests":2001`, 1)
	if _, err := ParseScenarioCatalog([]byte(tooLarge)); err == nil {
		t.Fatal("scenario request bound above 2000 must fail validation")
	}
	fullStack := strings.Replace(valid, `"correctness"`, `"full-stack"`, 1)
	if _, err := ParseScenarioCatalog([]byte(fullStack)); err == nil {
		t.Fatal("full-stack smoke must remain separately owned by the existing Compose task")
	}
}
