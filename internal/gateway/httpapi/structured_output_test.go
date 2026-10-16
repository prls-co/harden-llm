package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-472

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestModelRecallStructuredResponsesPreserveOriginalOutput(t *testing.T) {
	for _, tc := range []struct {
		file, output string
		status       int
	}{
		{"preparation-v2", "{\n  \"schema_version\": \"2.0\", \"entity_proposals\": [], \"suggestions\": []\n}", 200},
		{"entity-association-v5", "{\n  \"schema_version\": \"5.0\", \"prediction\": {\"status\": \"unknown\", \"entity\": null}, \"explanation\": \"No clue.\"\n}", 200},
		{"preparation-v2", `{"schema_version":"2.0","entity_proposals":[],"suggestions":[],"extra":true}`, 502},
		{"entity-association-v5", `{"schema_version":"5.0","prediction":{"status":"unknown","entity":42},"explanation":"No clue."}`, 502},
	} {
		t.Run(tc.file, func(t *testing.T) {
			schema, err := os.ReadFile("testdata/model-recall/" + tc.file + ".schema.json")
			if err != nil {
				t.Fatal(err)
			}
			var expected any
			if err := json.Unmarshal(schema, &expected); err != nil {
				t.Fatal(err)
			}
			var dispatched atomic.Int32
			provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/responses" {
					http.NotFound(w, r)
					return
				}
				dispatched.Add(1)
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				format := request["text"].(map[string]any)["format"].(map[string]any)
				if format["type"] != "json_schema" || format["strict"] != true || !reflect.DeepEqual(format["schema"], expected) {
					t.Errorf("provider contract changed: %#v", format)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "upstream-response", "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": tc.output}}}}, "output_text": tc.output, "usage": map[string]any{"input_tokens": 4, "output_tokens": 3, "total_tokens": 7}})
			}))
			defer provider.Close()
			proxy := newContractProxy(t, provider)
			defer proxy.Close()
			payload := map[string]any{"model": "fixture-model", "input": "synthetic input", "store": false, "tool_choice": "none", "text": map[string]any{"format": map[string]any{"type": "json_schema", "name": "model_recall_fixture", "strict": true, "schema": json.RawMessage(schema)}}, "harden": map[string]any{"cache": "off", "diagnostics": true, "recovery": map[string]any{"maxAttempts": 1, "retryOn": []any{}, "jsonRepair": nil, "rerun": nil, "backoff": map[string]any{"baseDelayMs": 0, "maxDelayMs": 0}}}}
			body, _ := json.Marshal(payload)
			request, _ := http.NewRequest(http.MethodPost, proxy.URL+"/v1/responses", bytes.NewReader(body))
			request.Header.Set("Authorization", "Bearer "+proxyTestToken)
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			raw, _ := io.ReadAll(response.Body)
			if response.StatusCode != tc.status {
				t.Fatalf("status=%d body=%s", response.StatusCode, raw)
			}
			var output map[string]any
			if err := json.Unmarshal(raw, &output); err != nil {
				t.Fatal(err)
			}
			if output["output_text"] != tc.output {
				t.Fatalf("original assistant text changed: %q", output["output_text"])
			}
			if tc.status == 502 && output["error"].(map[string]any)["code"] != "STRUCTURED_SCHEMA" {
				t.Fatalf("known invalid output became indeterminate: %s", raw)
			}
			attempts := output["harden"].(map[string]any)["attempts"].([]any)
			if dispatched.Load() != 1 || len(attempts) != 1 || attempts[0].(map[string]any)["providerUsed"] != true {
				t.Fatalf("dispatches=%d attempts=%#v", dispatched.Load(), attempts)
			}
		})
	}
}

func TestStructuredSchemaErrorOwnership(t *testing.T) {
	var dispatched atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dispatched.Add(1)
		http.Error(w, "unexpected dispatch", 500)
	}))
	defer provider.Close()
	proxy := newContractProxy(t, provider)
	defer proxy.Close()
	for _, tc := range []struct {
		name, format string
		status       int
	}{
		{"caller schema", `{"type":"json_schema","name":"invalid","strict":true,"schema":{"type":"object"}}`, 400},
		{"generated schema", `{"type":"json_object"}`, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"fixture-model","input":"synthetic","text":{"format":` + tc.format + `},"harden":{"diagnostics":true}}`)
			request, _ := http.NewRequest(http.MethodPost, proxy.URL+"/v1/responses", bytes.NewReader(body))
			request.Header.Set("Authorization", "Bearer "+proxyTestToken)
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			raw, _ := io.ReadAll(response.Body)
			if response.StatusCode != tc.status || dispatched.Load() != 0 {
				t.Fatalf("status=%d dispatches=%d body=%s", response.StatusCode, dispatched.Load(), raw)
			}
		})
	}
}
