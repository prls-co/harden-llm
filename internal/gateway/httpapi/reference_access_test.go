package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-425

import (
	"encoding/json"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/stateful"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestReferenceAccess(t *testing.T) {
	t.Parallel()
	var fixture struct {
		Keys []struct{ Label, APIKey, APIKeyHash string }
	}
	bytes, err := os.ReadFile("../../../test/fixtures/reference/access-keys.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(bytes, &fixture); err != nil {
		t.Fatal(err)
	}
	keys := []string{fixture.Keys[0].APIKey, fixture.Keys[1].APIKey}
	service, err := stateful.NewService(stateful.Config{Client: stateful.ClientOptions{Connections: []hardenllm.Connection{{ID: "fixture", Provider: "cpa", Protocol: "responses", BaseURL: "https://provider.example/v1", APIKey: "provider-leak-sentinel"}}, DefaultConnection: "fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	api, err := New(Config{APIKeys: keys, Service: service})
	if err != nil {
		t.Fatal(err)
	}
	var clientConstructions, handlerCalls, controlPlaneCalls atomic.Int32
	authority := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { controlPlaneCalls.Add(1) }))
	authority.Close()
	api.clientForKey = func(hash string) (*hardenllm.Client, error) {
		clientConstructions.Add(1)
		return service.ClientForKey(hash)
	}
	var observed accessScope
	handler := api.authorize(func(w http.ResponseWriter, r *http.Request) {
		handlerCalls.Add(1)
		observed = scopeFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	t.Run("invalid authorization reaches no handler", func(t *testing.T) {
		for _, path := range []string{"/v1/models", "/api/v1/status"} {
			for _, headers := range [][]string{nil, {"Bearer unknown-key"}, {"Bearer " + keys[0], "Bearer " + keys[1]}, {"Bearer " + fixture.Keys[0].APIKeyHash}, {"Basic c2VjcmV0"}} {
				request := httptest.NewRequest(http.MethodGet, path, nil)
				for _, header := range headers {
					request.Header.Add("Authorization", header)
				}
				response := httptest.NewRecorder()
				handler(response, request)
				if response.Code != 401 || !strings.Contains(response.Body.String(), `"code":"invalid_api_key"`) {
					t.Fatalf("invalid bearer %s: %d %s", path, response.Code, response.Body.String())
				}
			}
		}
		if handlerCalls.Load() != 0 || clientConstructions.Load() != 0 {
			t.Fatal("invalid authorization invoked composition")
		}
	})
	t.Run("keys derive fixed independent hashes and one client per request", func(t *testing.T) {
		for i, key := range keys {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
			request.Header.Set("Authorization", "Bearer "+key)
			response := httptest.NewRecorder()
			before := clientConstructions.Load()
			handler(response, request)
			if response.Code != 204 || observed.hash != fixture.Keys[i].APIKeyHash || observed.client == nil || clientConstructions.Load() != before+1 {
				t.Fatalf("derived scope/composition for key %s: code=%d hash=%q client=%v constructions=%d", fixture.Keys[i].Label, response.Code, observed.hash, observed.client != nil, clientConstructions.Load()-before)
			}
		}
		if controlPlaneCalls.Load() != 0 {
			t.Fatal("REST depends on human identity")
		}
	})
	t.Run("caller cannot select scope through query or JSON", func(t *testing.T) {
		for _, field := range []string{"apiKeyHash", "owner", "ownerId", "userId", "sessionReference"} {
			for _, kind := range []string{"query", "body"} {
				url := "/api/v1/state"
				body := ""
				method := http.MethodGet
				if kind == "query" {
					url += "?" + field + "=caller-selector"
				} else {
					method = http.MethodPost
					encoded, _ := json.Marshal(map[string]string{field: "caller-selector"})
					body = string(encoded)
				}
				request := httptest.NewRequest(method, url, strings.NewReader(body))
				request.Header.Set("Authorization", "Bearer "+keys[0])
				response := httptest.NewRecorder()
				before := handlerCalls.Load()
				composedBefore := clientConstructions.Load()
				handler(response, request)
				if response.Code != 400 || !strings.Contains(response.Body.String(), `"code":"scope_selector_rejected"`) || handlerCalls.Load() != before || clientConstructions.Load() != composedBefore {
					t.Fatalf("scope selector %s in %s reached handler: %d %s", field, kind, response.Code, response.Body.String())
				}
			}
		}
	})
	if controlPlaneCalls.Load() != 0 {
		t.Fatal("REST contacted shared identity")
	}
}
