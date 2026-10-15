package livecapacity

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-468

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"github.com/prls-co/harden-llm/internal/redaction"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/internal/gateway/httpapi"
	"github.com/prls-co/harden-llm/stateful"
)

func TestTransportThroughRealGateway(t *testing.T) {
	for _, route := range []string{"cpa", "openai"} {
		for _, status := range []int{200, 429, 503} {
			t.Run(route+"/"+http.StatusText(status), func(t *testing.T) {
				upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body["model"] != "gpt-6-luna" || body["max_output_tokens"] != float64(512) || body["reasoning"].(map[string]any)["effort"] != "low" || body["tools"] != nil {
						t.Errorf("wrong workload: %#v", body)
					}
					if route == "openai" && body["service_tier"] != "default" {
						t.Error("standard tier did not reach official provider")
					}
					if route == "cpa" && body["service_tier"] != nil {
						t.Error("CPA baseline changed")
					}
					w.Header().Set("Content-Type", "application/json")
					if status != 200 {
						w.Header().Set("Retry-After", "45")
						w.WriteHeader(status)
						io.WriteString(w, `{"error":{"code":"auth_unavailable","message":"fixture rejected"}}`)
						return
					}
					io.WriteString(w, `{"id":"fixture","status":"completed","model":"gpt-6-luna","output":[{"type":"message","content":[{"type":"output_text","text":"{\"nonce\":\"fixture-0\"}"}]}],"usage":{"input_tokens":111,"output_tokens":22,"total_tokens":133,"output_tokens_details":{"reasoning_tokens":5}}}`)
				}))
				defer upstream.Close()
				roots := x509.NewCertPool()
				roots.AddCert(upstream.Certificate())
				service, err := stateful.NewService(stateful.Config{Client: stateful.ClientOptions{Connections: []hardenllm.Connection{{ID: route, Provider: route, Protocol: "responses", BaseURL: upstream.URL + "/v1", APIKey: "synthetic"}}, DefaultConnection: route, EndpointPolicy: hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, PrivateAllowlist: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}})
				if err != nil {
					t.Fatal(err)
				}
				api, err := httpapi.New(httpapi.Config{APIKeys: []string{"synthetic-capacity-token-1234567890"}, Service: service})
				if err != nil {
					t.Fatal(err)
				}
				gateway := httptest.NewServer(api.Handler())
				defer gateway.Close()
				now := time.Now()
				s := NewScheduler(1, now, RouteConfig{Concurrency: 1}, 5)
				item, _ := s.Admit(now, false)
				item.Route = route
				transport := Transport{URL: gateway.URL, Token: "synthetic-capacity-token-1234567890", NoncePrefix: "fixture", Client: gateway.Client()}
				m := transport.Call(context.Background(), item)
				if status == 200 {
					if m.Outcome.Kind != "completed" || len(m.Attempts) != 1 || m.InputTokens == nil || *m.InputTokens != 111 || m.ReasoningTokens == nil || *m.ReasoningTokens != 5 {
						t.Fatalf("real gateway qualification failed: %+v", m)
					}
				} else if m.Outcome.Kind != "rejected" || m.Outcome.RetryAfter != 45*time.Second {
					t.Fatalf("retry hint lost: %+v", m)
				}
			})
		}
	}
}
func TestRetryAfterAndRequestBound(t *testing.T) {
	now := time.Unix(100, 0)
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{{"2", 2 * time.Second}, {"-2", 0}, {"bad", 0}, {now.Add(time.Minute).UTC().Format(http.TimeFormat), time.Minute}} {
		if got := RetryAfter(tc.value, now); got != tc.want {
			t.Fatal(tc.value, got)
		}
	}
	if n := len(RequestBody("openai", strings.Repeat("x", 128))); n >= 2048 {
		t.Fatal("reservation input byte bound exceeded", n)
	}
}
func TestCampaignCancelCheckpointsWithoutReplay(t *testing.T) {
	started := make(chan struct{})
	exited := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(exited)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-started; cancel() }()
	campaign := Campaign{ID: "test", Directory: t.TempDir(), Started: time.Now(), Budget: 5, Events: io.Discard, Transport: Transport{URL: server.URL, Token: "fixture", Client: server.Client()}}
	s, err := campaign.RunPhase(ctx, "cancel", 3, RouteConfig{Concurrency: 1, Interval: time.Hour}, true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if p := s.Snapshot(time.Now()); p.Unconfirmed != 1 || p.QueuedCPA != 2 || p.ActiveCPA != 0 || p.StopReason != "deadline_or_canceled" {
		t.Fatal(p)
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("HTTP worker leaked")
	}
}

func TestPaidRequestPinsStandardTier(t *testing.T) {
	var body map[string]any
	if err := json.Unmarshal(RequestBody("openai", "fixture"), &body); err != nil {
		t.Fatal(err)
	}
	if body["service_tier"] != "default" {
		t.Fatal("paid budget must not depend on project auto tier", body["service_tier"])
	}
}

func TestCampaignRedactsProviderDiagnostics(t *testing.T) {
	var output bytes.Buffer
	c := Campaign{ID: "fixture", Started: time.Now(), Events: &output, Redactor: redaction.New("sensitive-fixture-value")}
	if err := c.event("call_finished", Measurement{ErrorCode: "sensitive-fixture-value"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "sensitive-fixture-value") || !strings.Contains(output.String(), "REDACTED") {
		t.Fatal("diagnostic redaction failed")
	}
}

// The live CPA baseline exposed the provider response-header deadline before
// the larger call deadline. Exercise that exact transport boundary cheaply:
// a written request with no response headers must never become a replay.
func TestResponseHeaderTimeoutRemainsUnconfirmed(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer upstream.Close()
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	service, err := stateful.NewService(stateful.Config{Client: stateful.ClientOptions{
		Connections: []hardenllm.Connection{{ID: "cpa", Provider: "cpa", Protocol: "responses", BaseURL: upstream.URL + "/v1", APIKey: "synthetic"}}, DefaultConnection: "cpa",
		EndpointPolicy: hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, PrivateAllowlist: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, ResponseHeaderTimeout: 10 * time.Millisecond},
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}})
	if err != nil {
		t.Fatal(err)
	}
	api, err := httpapi.New(httpapi.Config{APIKeys: []string{"synthetic-capacity-token-1234567890"}, Service: service})
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(api.Handler())
	defer gateway.Close()
	now := time.Now()
	s := NewScheduler(2, now, RouteConfig{Concurrency: 8}, 5)
	item, _ := s.Admit(now, false)
	transport := Transport{URL: gateway.URL, Token: "synthetic-capacity-token-1234567890", NoncePrefix: "timeout", Client: gateway.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	m := transport.Call(ctx, item)
	if m.HTTPStatus != 502 || m.ErrorCode != "ETIMEDOUT" || m.Outcome.Kind != "unconfirmed" || len(m.Attempts) != 1 || !m.Attempts[0].DispatchObserved {
		t.Fatalf("lost dispatched result misclassified: %+v", m)
	}
	s.Finish(item.ID, m.Outcome, m.Finished)
	next, ok := s.Admit(m.Finished, true)
	if !ok || next.Route != "openai" || next.ID == item.ID || s.Items[item.ID].Status != "unconfirmed" || s.CPA.Concurrency != 4 {
		t.Fatal("unknown outcome replayed or shared admission not reduced", next, s.Items)
	}
}
