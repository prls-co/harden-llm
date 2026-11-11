package httpclient

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-540.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestRunPreservesReportedProviderUncertainty(t *testing.T) {
	t.Parallel()
	for _, recording := range []bool{true, false} {
		t.Run(fmt.Sprint(recording), func(t *testing.T) {
			events := fixtureEvents(t)
			events[2]["type"] = "run.failed"
			result := terminalResult(events)
			result["status"], result["output"] = "failed", nil
			result["resultSource"] = map[string]any{"kind": "none"}
			events[2]["data"].(map[string]any)["error"] = map[string]any{"code": "execution_unconfirmed", "message": "private remote message"}
			if !recording {
				events = events[:3]
			}
			stream := eventBytes(t, events)
			client, calls := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write(stream)
			})
			out, err := client.Run(context.Background(), fixtureRequest(t))
			var unknown *UnknownOutcomeError
			var definitive *RunFailure
			if !errors.As(err, &unknown) || errors.As(err, &definitive) || out.Evidence == nil || out.Evidence.ProviderAttemptCount != 2 || calls.Load() != 1 {
				t.Fatalf("reported unknown became definitive or lost evidence: status=%s evidence=%+v error=%v calls=%d", out.Result.Status, out.Evidence, err, calls.Load())
			}
			if strings.Contains(err.Error(), "private remote") {
				t.Fatal("remote message leaked")
			}
			if unknown.Problem == nil || unknown.Problem.Code != "execution_unconfirmed" || unknown.Evidence != out.Evidence {
				t.Fatal("validated server facts were discarded", unknown)
			}
			want := "saved"
			if !recording {
				want = "unconfirmed"
			}
			if out.Recording.Status != want {
				t.Fatal("provider uncertainty replaced independent history outcome", out.Recording)
			}
		})
	}
}

func TestRunJSONPreservesReportedProviderUncertainty(t *testing.T) {
	t.Parallel()
	for _, status := range []int{408, 502, 504} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			client, calls := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":{"code":"execution_unconfirmed","message":"private remote message"}}`)
			})
			_, err := client.Run(context.Background(), fixtureRequest(t))
			var unknown *UnknownOutcomeError
			var rejected *RejectedError
			if !errors.As(err, &unknown) || errors.As(err, &rejected) || calls.Load() != 1 || strings.Contains(err.Error(), "private remote") {
				t.Fatalf("reported uncertainty became a known rejection: error=%v calls=%d", err, calls.Load())
			}
			if unknown.StatusCode != status || unknown.Problem == nil || unknown.Problem.Code != "execution_unconfirmed" {
				t.Fatal("received JSON error facts were discarded", unknown)
			}
		})
	}
}
