package eval

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/prls-co/harden-llm/internal/providers"
	"github.com/prls-co/harden-llm/internal/runtime"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 EVAL-001 EVAL-002

func TestParityCoverageEval(t *testing.T) {
	t.Parallel()
	report, err := EvaluateParity("../..")
	if err != nil {
		t.Fatalf("EvaluateParity: %v", err)
	}
	if report.Percent != 100 || report.Covered != report.Required || len(report.Missing) != 0 || report.ProviderCases != 5 {
		t.Fatalf("parity threshold failed: %#v", report)
	}
}

func TestEndpointSafetyEval(t *testing.T) {
	t.Parallel()
	resolver := evalResolver{addresses: map[string][]netip.Addr{
		"public.example":   {netip.MustParseAddr("93.184.216.34")},
		"private.example":  {netip.MustParseAddr("10.0.0.7")},
		"mixed.example":    {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("127.0.0.1")},
		"metadata.example": {netip.MustParseAddr("169.254.169.254")},
	}}
	var dialCount atomic.Int64
	router, err := providers.NewRouter(providers.Config{EndpointPolicy: providers.EndpointPolicy{
		Resolver: resolver,
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			dialCount.Add(1)
			return nil, errors.New("evaluation dial must not run")
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	staticUnsafe := []string{
		"http://public.example/v1", "https://user:password@public.example/v1",
		"https://127.0.0.1/v1", "https://[::1]/v1", "https://169.254.169.254/latest",
	}
	for _, endpoint := range staticUnsafe {
		profile := runtime.Profile{ID: "eval", Provider: "vendor", APIInferenceType: "chat-completions", BaseURL: endpoint, ModelID: "model", SupportsTemperature: true}
		if _, prepareErr := router.Prepare(context.Background(), profile, runtime.Credential{APIKey: "fixture-secret"}, runtime.Call{CallType: "text", UserPrompt: "fixture"}); prepareErr == nil {
			t.Fatalf("statically unsafe endpoint was accepted: %s", endpoint)
		}
	}
	for _, endpoint := range []string{"https://private.example/v1", "https://mixed.example/v1", "https://metadata.example/v1"} {
		profile := runtime.Profile{ID: "eval", Provider: "vendor", APIInferenceType: "chat-completions", BaseURL: endpoint, ModelID: "model", SupportsTemperature: true}
		prepared, prepareErr := router.Prepare(context.Background(), profile, runtime.Credential{APIKey: "fixture-secret"}, runtime.Call{CallType: "text", UserPrompt: "fixture"})
		if prepareErr != nil {
			t.Fatalf("Prepare performed network-dependent safety validation for %s: %v", endpoint, prepareErr)
		}
		if _, executeErr := router.Execute(context.Background(), prepared); executeErr == nil {
			t.Fatalf("runtime endpoint safety accepted resolved unsafe endpoint: %s", endpoint)
		}
	}
	publicProfile := runtime.Profile{ID: "eval", Provider: "vendor", APIInferenceType: "chat-completions", BaseURL: "https://public.example/v1", ModelID: "model", SupportsTemperature: true}
	if _, err = router.Prepare(context.Background(), publicProfile, runtime.Credential{APIKey: "fixture-secret"}, runtime.Call{CallType: "text", UserPrompt: "fixture"}); err != nil {
		t.Fatalf("public endpoint rejected: %v", err)
	}
	privateRouter, err := providers.NewRouter(providers.Config{EndpointPolicy: providers.EndpointPolicy{
		Resolver: resolver, PrivateAllowedHosts: []string{"private.example"},
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			dialCount.Add(1)
			return nil, errors.New("evaluation dial must not run")
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	privateProfile := publicProfile
	privateProfile.BaseURL = "https://private.example/v1"
	if _, err = privateRouter.Prepare(context.Background(), privateProfile, runtime.Credential{APIKey: "fixture-secret"}, runtime.Call{CallType: "text", UserPrompt: "fixture"}); err != nil {
		t.Fatalf("explicit private endpoint rejected: %v", err)
	}
	if got := dialCount.Load(); got != 0 {
		t.Fatalf("endpoint safety evaluation made %d unintended dials", got)
	}
}

type evalResolver struct {
	addresses map[string][]netip.Addr
}

func (resolver evalResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	addresses, ok := resolver.addresses[host]
	if !ok {
		return nil, errors.New("fixture host missing")
	}
	return append([]netip.Addr(nil), addresses...), nil
}
