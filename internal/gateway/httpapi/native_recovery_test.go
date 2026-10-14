package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-462

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/stateful"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestNativeRecoveryInteractions(t *testing.T) {
	t.Parallel()
	catalog, err := stateful.DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	// Five native endpoint families, not five inference types: Agent uses Responses.
	ids := []string{"CPA gpt-6.1-sol", "OpenRouter DeepSeek V4 Pro", "Gemini 3.1 Flash Lite", "Claude Sonnet 4.6", "Perplexity GPT-6.1 Sol"}
	paths := [][]string{{"jsonRepair", "initial"}, {"jsonRepair", "escalation"}, {"rerun", "target"}, {"rerun", "jsonRepair", "initial"}, {"rerun", "jsonRepair", "escalation"}}
	var lock sync.Mutex
	var queue []nativeRequestExpectation
	var received []string
	var invalid string
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lock.Lock()
		defer lock.Unlock()
		if len(queue) == 0 {
			t.Error("unexpected native recovery dispatch")
			http.Error(w, "unexpected dispatch", 400)
			return
		}
		e := queue[0]
		queue = queue[1:]
		if err := verifyNativeRequest(r, e); err != nil {
			t.Error(err)
			http.Error(w, "native oracle rejected request", 400)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		model, _ := body["model"].(string)
		if strings.Contains(r.URL.Path, ":generateContent") {
			model = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1beta/models/"), ":generateContent")
		}
		received = append(received, model)
		text := invalid
		if model == "fixture-rerun-repair-escalation" {
			text = presetFixtureText
		}
		writePresetNativeResponse(w, r, body, text)
	}))
	defer provider.Close()
	roots := x509.NewCertPool()
	roots.AddCert(provider.Certificate())
	selected := stateful.Catalog{}
	connections := []hardenllm.Connection{}
	for _, id := range ids {
		preset := catalog[id]
		var d map[string]any
		if err = json.Unmarshal(preset.Definition, &d); err != nil {
			t.Fatal(err)
		}
		base := provider.URL + "/v1"
		if d["provider"] == "google" {
			base = provider.URL
		}
		d["baseUrl"] = base
		preset.Definition, _ = json.Marshal(d)
		selected[id] = preset
		connections = append(connections, hardenllm.Connection{ID: preset.Binding.DeploymentUpstreamID, Provider: d["provider"].(string), Protocol: d["apiInferenceType"].(string), BaseURL: base, APIKey: "fixture-provider-secret"})
	}
	service, err := stateful.NewService(stateful.Config{Presets: selected, Client: stateful.ClientOptions{Connections: connections, DefaultConnection: connections[0].ID, EndpointPolicy: hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}})
	if err != nil {
		t.Fatal(err)
	}
	runs := 0
	for _, source := range ids {
		for _, target := range ids {
			for slot, path := range paths {
				for _, failure := range []string{"syntax", "schema"} {
					for _, remaining := range []int{0, 1} {
						runs++
						name := fmt.Sprintf("%s/%s/%d/%s/remaining-%d", source, target, slot, failure, remaining)
						t.Run(name, func(t *testing.T) {
							targetAttempt := slot + 2
							budget := targetAttempt - 1 + remaining
							leaf := func(id, model string) map[string]any {
								return map[string]any{"source": "profile", "profileId": id, "modelId": model}
							}
							policy := map[string]any{"maxAttempts": budget, "retryOn": []string{}, "backoff": map[string]any{"baseDelayMs": 0, "maxDelayMs": 0}, "jsonRepair": map[string]any{"initial": leaf(source, "fixture-fail-1"), "escalation": leaf(source, "fixture-fail-2")}, "rerun": map[string]any{"target": leaf(source, "fixture-fail-3"), "jsonRepair": map[string]any{"initial": leaf(source, "fixture-fail-4"), "escalation": leaf(source, "fixture-fail-5")}}}
							node := policy
							for _, key := range path[:len(path)-1] {
								node = node[key].(map[string]any)
							}
							node[path[len(path)-1]] = leaf(target, "fixture-rerun-repair-escalation")
							raw, _ := json.Marshal(policy)
							var inputValues struct {
								Schema json.RawMessage `json:"schema"`
							}
							if err := json.Unmarshal(selected[source].Inputs, &inputValues); err != nil {
								t.Fatal(err)
							}
							input := stateful.RunInput{ProfileID: source, ModelID: "fixture-fail-0", UserPrompt: "return the original object", CallType: hardenllm.CallTypeStructured, Schema: inputValues.Schema, RecoveryPolicy: raw, CacheMode: hardenllm.CacheModeOff}
							expectedModels := []string{}
							expectations := []nativeRequestExpectation{}
							for attempt := 0; attempt < budget; attempt++ {
								profile, model := source, fmt.Sprintf("fixture-fail-%d", attempt)
								if attempt == slot+1 {
									profile = target
									model = "fixture-rerun-repair-escalation"
								}
								e, err := presetNativeExpectation(selected, profile, model, "", "structured")
								if err != nil {
									t.Fatal(err)
								}
								e.prompt = input.UserPrompt
								expectations = append(expectations, e)
								expectedModels = append(expectedModels, model)
							}
							lock.Lock()
							queue = expectations
							received = nil
							invalid = "not-json"
							if failure == "schema" {
								invalid = `{"joke":17,"explanation":"wrong type"}`
							}
							lock.Unlock()
							output, _, err := service.Run(context.Background(), strings.Repeat("a", 64), input)
							lock.Lock()
							actual := append([]string{}, received...)
							left := len(queue)
							lock.Unlock()
							if fmt.Sprint(actual) != fmt.Sprint(expectedModels) || left != 0 || len(output.Attempts) != budget {
								t.Fatalf("native ordering/budget differs: calls=%v want=%v attempts=%d left=%d err=%v", actual, expectedModels, len(output.Attempts), left, err)
							}
							if remaining == 1 {
								producer := output.ResultSource.Producer
								if err != nil || output.StopReason != "succeeded" || producer == nil || producer.ModelID != "fixture-rerun-repair-escalation" || producer.ProfileID != target || output.SelectedTarget == nil || output.SelectedTarget.ModelID != "fixture-fail-0" || output.ResultSource.AttemptNumber != targetAttempt {
									t.Fatalf("target did not produce result: status=%s stop=%s producer=%+v attempt=%d err=%v", output.Status, output.StopReason, producer, output.ResultSource.AttemptNumber, err)
								}
							} else {
								if err == nil || output.StopReason != "attempts_exhausted" || output.Output != nil {
									t.Fatalf("exhausted budget dispatched target or exposed invalid output: status=%s stop=%s output=%v err=%v", output.Status, output.StopReason, output.Output, err)
								}
							}
						})
					}
				}
			}
		}
	}
	if runs != 500 {
		t.Fatalf("incomplete finite domain: %d want 500", runs)
	}
}
