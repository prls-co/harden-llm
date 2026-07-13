package stats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-016

func TestParityStatsTotals(t *testing.T) {
	t.Parallel()
	paths, err := filepath.Glob("../../fixtures/parity/source/llm-stats-totals/parity/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("stats parity fixtures are missing")
	}
	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			var fixture struct {
				Input struct {
					Context      map[string]string `json:"context"`
					CurrentStats Totals            `json:"currentStats"`
					TraceData    Trace             `json:"traceData"`
				} `json:"input"`
				Output json.RawMessage `json:"output"`
			}
			if unmarshalErr := json.Unmarshal(data, &fixture); unmarshalErr != nil {
				t.Fatal(unmarshalErr)
			}
			got, mergeErr := Merge(fixture.Input.CurrentStats, fixture.Input.TraceData, fixture.Input.Context)
			var errorOutput struct {
				ErrorCode string `json:"errorCode"`
			}
			_ = json.Unmarshal(fixture.Output, &errorOutput)
			if errorOutput.ErrorCode != "" {
				if mergeErr == nil {
					t.Fatalf("expected contract error %q", errorOutput.ErrorCode)
				}
				contractErr, ok := mergeErr.(*ContractError)
				if !ok || contractErr.Code != errorOutput.ErrorCode || !reflect.DeepEqual(contractErr.Context, fixture.Input.Context) {
					t.Fatalf("unexpected contract error: %#v", mergeErr)
				}
				return
			}
			if mergeErr != nil {
				t.Fatalf("Merge: %v", mergeErr)
			}
			var want Totals
			if unmarshalErr := json.Unmarshal(fixture.Output, &want); unmarshalErr != nil {
				t.Fatal(unmarshalErr)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("totals mismatch:\n got %#v\nwant %#v", got, want)
			}
		})
	}
}
