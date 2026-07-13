package pricing

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-015

func TestUsageCostParity(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../fixtures/parity/generated/usage-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name       string  `json:"name"`
			Normalized Usage   `json:"normalized"`
			Summary    Summary `json:"summary"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, test := range fixture.Cases {
		test := test
		t.Run(test.Name, func(t *testing.T) {
			t.Parallel()
			summary, summarizeErr := Summarize(test.Normalized)
			if summarizeErr != nil {
				t.Fatalf("Summarize: %v", summarizeErr)
			}
			if !reflect.DeepEqual(summary, test.Summary) {
				t.Fatalf("summary mismatch:\n got %#v\nwant %#v", summary, test.Summary)
			}
		})
	}

	unknown := Usage{Items: map[string]Item{
		ItemInput: {Tokens: 1}, ItemOutput: {Tokens: 1},
	}}
	summary, err := Summarize(unknown)
	if err != nil {
		t.Fatalf("Summarize unknown: %v", err)
	}
	if summary.CostKnown || summary.TotalCost != nil {
		t.Fatalf("unknown cost was coerced to zero: %#v", summary)
	}
	reported := 0.25
	cost, err := ResolveCost(unknown, &reported)
	if err != nil || !cost.Known || cost.TotalUSD != reported || cost.Source != "reported" {
		t.Fatalf("reported cost did not win: %#v %v", cost, err)
	}
}

func TestUsageCostParityRejectsInvalidAndConflictingUsage(t *testing.T) {
	t.Parallel()
	_, err := Summarize(Usage{Items: map[string]Item{"unknown": {Tokens: 1}}})
	if err == nil {
		t.Fatal("unknown usage type was accepted")
	}
	negative := Usage{Items: map[string]Item{ItemInput: {Tokens: -1}}}
	if _, err = Summarize(negative); err == nil {
		t.Fatal("negative usage was accepted")
	}
	leftRate, rightRate := 0.1, 0.2
	_, err = Add(
		Usage{Items: map[string]Item{ItemInput: {Tokens: 1, RatePerToken: &leftRate}}},
		Usage{Items: map[string]Item{ItemInput: {Tokens: 1, RatePerToken: &rightRate}}},
	)
	if err == nil {
		t.Fatal("conflicting pricing snapshots were merged")
	}
}
