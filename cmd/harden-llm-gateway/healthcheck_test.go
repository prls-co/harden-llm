package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-033

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthcheckCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/readyz" {
			t.Fatalf("health path = %q", request.URL.Path)
		}
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	var output bytes.Buffer
	if err := runHealthcheck(context.Background(), []string{"--url", server.URL + "/readyz"}, &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "ready\n" {
		t.Fatalf("health output = %q", output.String())
	}

	for _, endpoint := range []string{"https://127.0.0.1/", "http://example.com/", "http://127.0.0.1/?query=1"} {
		if err := runHealthcheck(context.Background(), []string{"--url", endpoint}, &output); err == nil {
			t.Errorf("unsafe healthcheck URL %q was accepted", endpoint)
		}
	}
	failing := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "not ready", http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	if err := runHealthcheck(context.Background(), []string{"--url", failing.URL}, &output); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("unready response error = %v", err)
	}
}
