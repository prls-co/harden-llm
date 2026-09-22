// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-282

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prls-co/harden-llm/internal/capacity"
)

type capacityHistoryTestPage struct {
	Items      []capacityHistoryTestItem `json:"items"`
	NextCursor string                    `json:"nextCursor,omitempty"`
}

type capacityHistoryTestItem struct {
	RunID   string `json:"runId"`
	TraceID string `json:"traceId"`
}

func writeCapacityHistoryTestPage(t *testing.T, writer http.ResponseWriter, page capacityHistoryTestPage) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(map[string]any{"result": page}); err != nil {
		t.Errorf("encode history test page: %v", err)
	}
}

func TestCapacityHistoryPaginationFindsAllExpectedPairsAcrossCursorPages(t *testing.T) {
	cursor := "opaque cursor/+="
	requestsSeen := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestsSeen++
		if request.Header.Get("Authorization") != "Bearer "+capacityToken {
			t.Errorf("authorization header = %q", request.Header.Get("Authorization"))
		}
		if request.URL.Query().Get("limit") != "100" {
			t.Errorf("history page limit = %q, want 100", request.URL.Query().Get("limit"))
		}
		switch requestsSeen {
		case 1:
			if got := request.URL.Query().Get("cursor"); got != "" {
				t.Errorf("first page cursor = %q, want empty", got)
			}
			writeCapacityHistoryTestPage(t, writer, capacityHistoryTestPage{
				Items:      []capacityHistoryTestItem{{RunID: "new-run", TraceID: "new-trace"}},
				NextCursor: cursor,
			})
		case 2:
			if got := request.URL.Query().Get("cursor"); got != cursor {
				t.Errorf("second page cursor = %q, want decoded opaque cursor %q", got, cursor)
			}
			writeCapacityHistoryTestPage(t, writer, capacityHistoryTestPage{
				Items:      []capacityHistoryTestItem{{RunID: "old-run", TraceID: "old-trace"}},
				NextCursor: "another-page",
			})
		default:
			t.Errorf("unexpected extra history page request %d", requestsSeen)
			writeCapacityHistoryTestPage(t, writer, capacityHistoryTestPage{})
		}
	}))
	defer server.Close()

	err := assertCapacityHistory(context.Background(), server.Client(), server.URL, []capacity.RequestResult{
		{RunID: "new-run", TraceID: "new-trace"},
		{RunID: "old-run", TraceID: "old-trace"},
	})
	if err != nil {
		t.Fatalf("verify paginated history: %v", err)
	}
	if requestsSeen != 2 {
		t.Fatalf("history page requests = %d, want 2", requestsSeen)
	}
}

func TestCapacityHistoryPaginationRejectsRepeatedCursor(t *testing.T) {
	requestsSeen := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestsSeen++
		writeCapacityHistoryTestPage(t, writer, capacityHistoryTestPage{NextCursor: "same-cursor"})
	}))
	defer server.Close()

	err := assertCapacityHistory(context.Background(), server.Client(), server.URL, []capacity.RequestResult{{RunID: "missing", TraceID: "trace"}})
	if err == nil || err.Error() != "REST history pagination repeated a cursor" {
		t.Fatalf("repeated cursor error = %v, want bounded repeated-cursor diagnostic", err)
	}
	if requestsSeen != 2 {
		t.Fatalf("history page requests = %d, want 2 before detecting the repeated cursor", requestsSeen)
	}
}
