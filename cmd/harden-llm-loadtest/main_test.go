package main

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-468

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestLiveOptInAndBudgetValidation(t *testing.T) {
	for _, args := range [][]string{nil, {"--live", "--output", "unused", "--api-budget", "NaN"}, {"--live", "--output", "unused", "--api-budget", "+Inf"}, {"--live", "--output", "unused", "--api-budget", "6"}, {"--live", "--output", "unused", "--duration", "3h"}} {
		if err := run(args); err == nil {
			t.Fatal("invalid live input accepted", args)
		}
	}
}

func TestCPAConcurrencyBounds(t *testing.T) {
	for _, args := range [][]string{
		{"--live", "--cpa-only", "--output", "unused", "--max-concurrency", "0"},
		{"--live", "--cpa-only", "--output", "unused", "--max-concurrency", "129"},
		{"--live", "--cpa-only", "--output", "unused", "--waves", "0"},
		{"--live", "--cpa-only", "--output", "unused", "--waves", "6"},
		{"--live", "--cpa-only", "--output", "unused", "--min-concurrency", "65"},
		{"--live", "--cpa-only", "--output", "unused", "--steady-calls", "1001"},
		{"--live", "--cpa-only", "--output", "unused", "--start-interval", "-1s"},
	} {
		if err := run(args); err == nil {
			t.Fatal("unbounded concurrency accepted", args)
		}
	}
}

// TEST-470: the loopback control forwards the exact request and cooldown hint.
func TestLoopbackControlPreservesProviderBoundary(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/v1/responses" || string(body) != "synthetic-control" || r.Header.Get("Authorization") != "Bearer synthetic-key" {
			t.Error("control changed upstream request")
		}
		w.Header().Set("Retry-After", "17")
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"code":"rate_limit_exceeded"}}`)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	server, policy := newCPAControl(target)
	defer server.Close()
	if policy.TLSConfig == nil || len(policy.PrivateAllowedHosts) != 1 {
		t.Fatal("control lacks explicit TLS/private boundary")
	}
	req, _ := http.NewRequest("POST", server.URL+"/v1/responses", strings.NewReader("synthetic-control"))
	req.Header.Set("Authorization", "Bearer synthetic-key")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") != "17" {
		t.Fatal("control lost native failure")
	}
}
