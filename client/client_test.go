package client

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-024

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientSubmitsAndPollsOneDurableOperation(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer service-secret" || request.Header.Get("X-PRLS-Service") != "ner" || request.Header.Get("X-PRLS-Account-ID") != "account-1" {
			t.Fatalf("service headers = %q %q %q", request.Header.Get("Authorization"), request.Header.Get("X-PRLS-Service"), request.Header.Get("X-PRLS-Account-ID"))
		}
		requests++
		operation := Operation{SchemaVersion: SchemaVersion, OperationID: "op-1", AccountID: "account-1", InputDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Status: "accepted", CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC()}
		if request.Method == http.MethodGet {
			operation.Status = "succeeded"
			operation.Output = json.RawMessage(`{"mentions":[]}`)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(operation)
	}))
	defer server.Close()
	service, err := New(Config{BaseURL: server.URL, ServiceName: "ner", ServiceKey: "service-secret", PollEvery: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := service.SubmitStructured(context.Background(), StructuredRequest{OperationID: "op-1", AccountID: "account-1", InputDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ProfileID: "profile", UserPrompt: "text", Schema: json.RawMessage(`{"type":"object"}`)})
	if err != nil || operation.Status != "accepted" {
		t.Fatalf("submit = %#v, %v", operation, err)
	}
	completed, err := service.Wait(context.Background(), "account-1", "op-1")
	if err != nil || completed.Status != "succeeded" || string(completed.Output) != `{"mentions":[]}` {
		t.Fatalf("completed = %#v, %v", completed, err)
	}
	if requests != 2 {
		t.Fatalf("HTTP requests = %d", requests)
	}
}

func TestClientRejectsChangedOperationIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusConflict)
		_, _ = writer.Write([]byte(`{"schemaVersion":"harden-llm-operation.v1","operationId":"op-1"}`))
	}))
	defer server.Close()
	service, err := New(Config{BaseURL: server.URL, ServiceName: "ner", ServiceKey: "service-secret"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.SubmitStructured(context.Background(), StructuredRequest{OperationID: "op-1", AccountID: "account-1", InputDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ProfileID: "profile", UserPrompt: "text", Schema: json.RawMessage(`{"type":"object"}`)})
	if err != ErrConflict {
		t.Fatalf("error = %v", err)
	}
}
