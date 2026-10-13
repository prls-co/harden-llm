package stateful

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-451

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func TestHistoryRecordingOutcome(t *testing.T) {
	t.Parallel()
	var fixture struct {
		CommitCases []struct{ Commit, Readback, Status string }
	}
	data, err := os.ReadFile("../test/fixtures/reference/history-outcomes.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, c := range fixture.CommitCases {
		t.Run(c.Commit+"_"+c.Readback, func(t *testing.T) {
			var readErr error
			if c.Readback == "failed" {
				readErr = errors.New("readback fault sentinel")
			}
			got := recordingStatus(c.Commit, c.Readback == "found", readErr)
			if got != c.Status {
				t.Fatalf("recordingStatus(%s,%s)=%s want %s", c.Commit, c.Readback, got, c.Status)
			}
		})
	}
	t.Run("empty readback can precede a delayed commit", func(t *testing.T) {
		if status := recordingStatus("unknown", false, nil); status != "unconfirmed" {
			t.Fatalf("empty read falsely proves rollback: %s", status)
		}
		if status := recordingStatus("unknown", true, nil); status != "saved" {
			t.Fatalf("positive read must confirm: %s", status)
		}
	})
}

func historyFixtureRecord(t *testing.T) HistoryRecord {
	t.Helper()
	data, err := os.ReadFile("../test/fixtures/reference/history-pages.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Rows []struct{ Record HistoryRecord }
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture.Rows[0].Record
}
