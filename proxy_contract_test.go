package hardenllm_test

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	hardenllm "github.com/prls-co/harden-llm"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-401 PLAN-HLLM-PROXY-REFERENCE-001
func TestProxyRequestContract(t *testing.T) {
	fixtureBytes, err := os.ReadFile("test/fixtures/proxy-reference-contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		RequestCases []struct {
			Path string          `json:"path"`
			Body json.RawMessage `json:"body"`
		} `json:"requestCases"`
	}
	if err := json.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatal(err)
	}

	var chatBody json.RawMessage
	for _, requestCase := range fixture.RequestCases {
		if requestCase.Path == "/v1/chat/completions" {
			chatBody = requestCase.Body
			break
		}
	}
	if len(chatBody) == 0 {
		t.Fatal("contract fixture has no chat request")
	}

	requestType := reflect.TypeOf(hardenllm.Request{})
	for _, removedField := range []string{"ProfileID", "Profiles"} {
		if _, exists := requestType.FieldByName(removedField); exists {
			t.Errorf("public request still exposes %s", removedField)
		}
	}
	requestValue := reflect.New(requestType)
	if err := json.Unmarshal(chatBody, requestValue.Interface()); err != nil {
		t.Fatalf("decode canonical request: %v", err)
	}

	modelField := requestValue.Elem().FieldByName("ModelID")
	if !modelField.IsValid() || modelField.Kind() != reflect.String || modelField.String() != "gpt-6-astra" {
		t.Fatalf("model ID was not preserved: %v", modelField)
	}
	messagesField := requestValue.Elem().FieldByName("Messages")
	if !messagesField.IsValid() || messagesField.Kind() != reflect.Slice || messagesField.Len() != 4 {
		t.Fatalf("ordered conversation was not preserved: %v", messagesField)
	}
	encodedMessages, err := json.Marshal(messagesField.Interface())
	if err != nil {
		t.Fatal(err)
	}
	var expectedBody map[string]json.RawMessage
	if err := json.Unmarshal(chatBody, &expectedBody); err != nil {
		t.Fatal(err)
	}
	var expectedMessages, decodedMessages any
	if err := json.Unmarshal(expectedBody["messages"], &expectedMessages); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encodedMessages, &decodedMessages); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expectedMessages, decodedMessages) {
		t.Fatalf("ordered messages or tool-call linkage changed:\n got: %s\nwant: %s", encodedMessages, expectedBody["messages"])
	}

	reasoningField := requestValue.Elem().FieldByName("ReasoningEffort")
	if !reasoningField.IsValid() || reasoningField.Kind() != reflect.String || reasoningField.String() != "high" {
		t.Fatalf("native reasoning value was not preserved: %v", reasoningField)
	}

	policy := hardenllm.DefaultStructuredRecoveryPolicy()
	if policy.MaxAttempts != 6 || policy.JSONRepair == nil || policy.Rerun != nil {
		t.Fatalf("profile-free repair default changed its bounded policy: %+v", policy)
	}
	initialTarget := reflect.ValueOf(policy.JSONRepair.Initial)
	if initialTarget.Kind() == reflect.Pointer {
		initialTarget = initialTarget.Elem()
	}
	if _, exists := initialTarget.Type().FieldByName("ProfileID"); exists {
		t.Fatal("recovery targets still expose profile identity")
	}
	sourceField := initialTarget.FieldByName("Source")
	modelIDField := initialTarget.FieldByName("ModelID")
	if !sourceField.IsValid() || sourceField.String() != "generation" || !modelIDField.IsValid() || modelIDField.String() != "" {
		t.Fatalf("default repair must reuse the selected generation target: %+v", policy.JSONRepair.Initial)
	}
}
