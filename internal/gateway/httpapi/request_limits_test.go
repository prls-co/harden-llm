package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-422

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	hardenllm "github.com/prls-co/harden-llm"
)

// TEST-422 / NER migration case 502: acquire a full source-sized input by encoded byte
// length, including JSON escaping, without treating unknown Content-Length as safe.
func TestRequestBodyBudget(t *testing.T) {
	input := strings.Repeat("\x00", 256<<10)
	body, err := json.Marshal(map[string]any{"model": "test", "input": []map[string]string{
		{"role": "system", "content": "source prompt"}, {"role": "user", "content": input},
		{"role": "assistant", "content": "{}"}, {"role": "user", "content": "correct the result"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("source input bytes=%d encoded correction request bytes=%d", len(input), len(body))
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "known length", true: "unknown length"}[unknown], func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			if unknown {
				req.ContentLength = -1
			}
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			var got responsesRequest
			if !(&API{}).decodeRequest(rec, req, &got, maximumResponsesRequestBytes) {
				t.Fatalf("admitted source-sized request rejected: status=%d body=%s", rec.Code, rec.Body.String())
			}
			var items []map[string]string
			if err := json.Unmarshal(got.Input, &items); err != nil {
				t.Fatal(err)
			}
			if len(items) != 4 || items[1]["content"] != input || items[2]["role"] != "assistant" {
				t.Fatal("request content or ordering changed")
			}
		})
	}
}

// A declared length is only an optimization; streaming must enforce the same bound.
func TestRequestBodyBudgetRejectsOversize(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "known length", true: "unknown length"}[unknown], func(t *testing.T) {
			body := bytes.Repeat([]byte("x"), maximumResponsesRequestBytes+1)
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			if unknown {
				req.ContentLength = -1
			}
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			var got responsesRequest
			if (&API{}).decodeRequest(rec, req, &got, maximumResponsesRequestBytes) || rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("oversize status = %d", rec.Code)
			}
		})
	}
}

func TestRequestBodyBudgetAuthorization(t *testing.T) {
	payload, err := json.Marshal(map[string]any{"model": "test", "input": strings.Repeat("\x00", 256<<10)})
	if err != nil {
		t.Fatal(err)
	}
	const key = "synthetic-http-client-key-0000000000000001"
	api := &API{keys: []string{key}, clientForKey: func(string) (*hardenllm.Client, error) { return nil, nil }}
	called := false
	handler := api.authorize(func(w http.ResponseWriter, r *http.Request) {
		called = true
		var got responsesRequest
		if !api.decodeRequest(w, r, &got, maximumResponsesRequestBytes) {
			t.Error("decoder rejected authorized body")
		}
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler(rec, req)
	if !called {
		t.Fatalf("authorization middleware rejected admitted request: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRequestBodyBudgetKeepsOtherBoundaries(t *testing.T) {
	key := "synthetic-http-client-key-0000000000000001"
	api := &API{keys: []string{key}, clientForKey: func(string) (*hardenllm.Client, error) { return nil, nil }}
	called := false
	handler := api.authorize(func(w http.ResponseWriter, r *http.Request) { called = true })
	for _, tc := range []struct {
		name, path, body string
		status           int
	}{
		{"chat stays bounded", "/v1/chat/completions", `{"model":"test","messages":"` + strings.Repeat("x", 256<<10) + `"}`, http.StatusRequestEntityTooLarge},
		{"scope selector stays rejected", "/v1/responses", `{"model":"test","input":"` + strings.Repeat("x", 256<<10) + `","ownerId":"other"}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler(rec, req)
			if called || rec.Code != tc.status {
				t.Fatalf("called=%t status=%d, want %d", called, rec.Code, tc.status)
			}
		})
	}
}
