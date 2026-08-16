package providers

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-014

type staticResolver map[string][]netip.Addr

func (resolver staticResolver) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	addresses, ok := resolver[host]
	if !ok {
		return nil, errors.New("host not found")
	}
	return append([]netip.Addr(nil), addresses...), nil
}

type sequenceResolver struct {
	mu      sync.Mutex
	results [][]netip.Addr
}

func (resolver *sequenceResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	if len(resolver.results) == 0 {
		return nil, errors.New("no resolver result")
	}
	result := append([]netip.Addr(nil), resolver.results[0]...)
	resolver.results = resolver.results[1:]
	return result, nil
}

func TestEndpointPolicyRejectsUnsafeTargetsBeforeDial(t *testing.T) {
	t.Parallel()
	resolver := staticResolver{
		"public.example":   {netip.MustParseAddr("93.184.216.34")},
		"mixed.example":    {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("127.0.0.1")},
		"loopback.example": {netip.MustParseAddr("::1")},
	}
	guard, err := newEndpointGuard(EndpointPolicy{Resolver: resolver})
	if err != nil {
		t.Fatalf("newEndpointGuard: %v", err)
	}

	accepted, err := guard.resolve(context.Background(), "https://public.example/v1")
	if err != nil {
		t.Fatalf("public endpoint rejected: %v", err)
	}
	if accepted.origin != "https://public.example" || accepted.port != "443" {
		t.Fatalf("unexpected normalized endpoint: %#v", accepted)
	}

	for _, rawURL := range []string{
		"http://public.example/v1",
		"https://user:pass@public.example/v1",
		"https://127.0.0.1/v1",
		"https://[::1]/v1",
		"https://169.254.169.254/latest/meta-data",
		"https://mixed.example/v1",
		"https://loopback.example/v1",
	} {
		rawURL := rawURL
		t.Run(rawURL, func(t *testing.T) {
			t.Parallel()
			if _, resolveErr := guard.resolve(context.Background(), rawURL); resolveErr == nil {
				t.Fatalf("unsafe endpoint %q was accepted", rawURL)
			}
		})
	}
}

func TestEndpointPolicyUsesExactPrivateExceptions(t *testing.T) {
	t.Parallel()
	resolver := staticResolver{
		"model.internal": {netip.MustParseAddr("10.20.30.40")},
		"other.internal": {netip.MustParseAddr("10.20.30.41")},
	}
	guard, err := newEndpointGuard(EndpointPolicy{
		Resolver:            resolver,
		PrivateAllowedHosts: []string{"model.internal"},
	})
	if err != nil {
		t.Fatalf("newEndpointGuard: %v", err)
	}
	if _, err = guard.resolve(context.Background(), "https://model.internal/v1"); err != nil {
		t.Fatalf("exact private host rejected: %v", err)
	}
	if _, err = guard.resolve(context.Background(), "https://other.internal/v1"); err == nil {
		t.Fatal("private host exception matched a different host")
	}

	guard, err = newEndpointGuard(EndpointPolicy{
		Resolver:         resolver,
		PrivateAllowlist: []netip.Prefix{netip.MustParsePrefix("10.20.30.40/32")},
	})
	if err != nil {
		t.Fatalf("newEndpointGuard CIDR: %v", err)
	}
	if _, err = guard.resolve(context.Background(), "https://model.internal/v1"); err != nil {
		t.Fatalf("allowlisted private address rejected: %v", err)
	}
	if _, err = guard.resolve(context.Background(), "https://other.internal/v1"); err == nil {
		t.Fatal("private CIDR exception admitted an address outside the prefix")
	}
}

func TestEndpointPolicyPinsValidatedAddressAndDisablesRedirects(t *testing.T) {
	t.Parallel()
	var dialed string
	client, err := newSafeHTTPClient(EndpointPolicy{
		Resolver: staticResolver{"provider.example": {netip.MustParseAddr("93.184.216.34")}},
		DialContext: func(_ context.Context, _, address string) (net.Conn, error) {
			dialed = address
			return nil, errors.New("stop after dial capture")
		},
		ConnectTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("newSafeHTTPClient: %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://provider.example/v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = client.Do(req)
	if dialed != "93.184.216.34:443" {
		t.Fatalf("dial was not pinned to validated address: %q", dialed)
	}
	if redirectErr := client.CheckRedirect(req, nil); redirectErr == nil {
		t.Fatal("redirects must be disabled")
	}

	_, err = newSafeHTTPClient(EndpointPolicy{TLSConfig: &tls.Config{InsecureSkipVerify: true}}) //nolint:gosec // verifies rejection
	if err == nil {
		t.Fatal("TLS verification bypass was accepted")
	}
}

func TestEndpointPolicySanitizesHopByHopAndForwardingHeaders(t *testing.T) {
	t.Parallel()
	headers := http.Header{
		"Authorization":       {"Bearer secret"},
		"Host":                {"metadata.internal"},
		"Connection":          {"keep-alive, x-remove-me"},
		"X-Remove-Me":         {"bad"},
		"Proxy-Authorization": {"bad"},
		"X-Forwarded-For":     {"127.0.0.1"},
		"Transfer-Encoding":   {"chunked"},
		"Content-Length":      {"999"},
		"X-Real-IP":           {"127.0.0.1"},
		"CF-Connecting-IP":    {"127.0.0.1"},
		"True-Client-IP":      {"127.0.0.1"},
		"X-Provider-Feature":  {"safe"},
	}
	clean := sanitizeHeaders(headers)
	if clean.Get("Authorization") == "" || clean.Get("X-Provider-Feature") != "safe" {
		t.Fatalf("safe headers were removed: %#v", clean)
	}
	for _, name := range []string{
		"Host", "Connection", "X-Remove-Me", "Proxy-Authorization", "X-Forwarded-For", "Transfer-Encoding",
		"Content-Length", "X-Real-IP", "CF-Connecting-IP", "True-Client-IP",
	} {
		if clean.Get(name) != "" {
			t.Fatalf("forbidden header %q survived sanitization", name)
		}
	}
}

func TestEndpointPolicyFailsOverAcrossValidatedAddresses(t *testing.T) {
	t.Parallel()
	addresses := []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("93.184.216.35")}
	dialed := make([]string, 0, len(addresses))
	var remote net.Conn
	client, err := newSafeHTTPClient(EndpointPolicy{
		Resolver: staticResolver{"provider.example": addresses},
		DialContext: func(_ context.Context, _, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			if len(dialed) == 1 {
				return nil, errors.New("first address unavailable")
			}
			local, peer := net.Pipe()
			remote = peer
			return local, nil
		},
		ConnectTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := client.Transport.(*safeRoundTripper).guard.resolve(context.Background(), "https://provider.example/v1")
	if err != nil {
		t.Fatal(err)
	}
	connection, err := client.Transport.(*safeRoundTripper).transportFor(endpoint).DialContext(
		context.Background(), "tcp", "provider.example:443",
	)
	if err != nil {
		t.Fatalf("validated address failover failed: %v", err)
	}
	connection.Close()
	remote.Close()
	if len(dialed) != 2 || dialed[0] == dialed[1] {
		t.Fatalf("validated addresses were not attempted once each: %#v", dialed)
	}
}

func TestEndpointPolicyRejectsDNSRebindingWithoutSecondDial(t *testing.T) {
	t.Parallel()
	resolver := &sequenceResolver{results: [][]netip.Addr{
		{netip.MustParseAddr("93.184.216.34")},
		{netip.MustParseAddr("127.0.0.1")},
	}}
	dials := 0
	client, err := newSafeHTTPClient(EndpointPolicy{
		AllowedHosts: []string{"provider.example"}, Resolver: resolver,
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			dials++
			return nil, errors.New("dial fixture")
		},
	})
	if err != nil {
		t.Fatalf("newSafeHTTPClient: %v", err)
	}
	request, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://provider.example/v1", nil)
	_, _ = client.Do(request)
	_, secondErr := client.Do(request)
	if secondErr == nil || !strings.Contains(secondErr.Error(), "rejected") {
		t.Fatalf("rebound private address was not rejected: %v", secondErr)
	}
	if dials != 1 {
		t.Fatalf("rebound request caused an unintended dial: %d", dials)
	}

	guard, err := newEndpointGuard(EndpointPolicy{AllowedHosts: []string{"provider.example"}, Resolver: staticResolver{
		"provider.example": {netip.MustParseAddr("93.184.216.34")},
		"other.example":    {netip.MustParseAddr("93.184.216.34")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = guard.resolve(context.Background(), "https://other.example/v1"); err == nil {
		t.Fatal("host outside AllowedHosts was accepted")
	}
}
