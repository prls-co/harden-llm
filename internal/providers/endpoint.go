package providers

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

const (
	defaultConnectTimeout        = 10 * time.Second
	defaultTLSHandshakeTimeout   = 10 * time.Second
	defaultResponseHeaderTimeout = 30 * time.Second
	maxOriginTransports          = 256
)

// EndpointPolicy configures the sole provider egress boundary.
type EndpointPolicy struct {
	AllowedHosts        []string
	PrivateAllowedHosts []string
	PrivateAllowlist    []netip.Prefix
	Resolver            interface {
		LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
	}
	DialContext           func(context.Context, string, string) (net.Conn, error)
	TLSConfig             *tls.Config
	ConnectTimeout        time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
}

type endpointGuard struct {
	resolver interface {
		LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
	}
	allowedHosts        map[string]struct{}
	privateAllowedHosts map[string]struct{}
	privateAllowlist    []netip.Prefix
}

type resolvedEndpoint struct {
	origin    string
	host      string
	port      string
	addresses []netip.Addr
}

func newEndpointGuard(policy EndpointPolicy) (*endpointGuard, error) {
	allowed, err := normalizedHostSet(policy.AllowedHosts)
	if err != nil {
		return nil, fmt.Errorf("providers: invalid allowed host: %w", err)
	}
	privateAllowed, err := normalizedHostSet(policy.PrivateAllowedHosts)
	if err != nil {
		return nil, fmt.Errorf("providers: invalid private allowed host: %w", err)
	}
	allowlist := make([]netip.Prefix, 0, len(policy.PrivateAllowlist))
	for _, prefix := range policy.PrivateAllowlist {
		if !prefix.IsValid() {
			return nil, errors.New("providers: private allowlist contains an invalid prefix")
		}
		allowlist = append(allowlist, prefix.Masked())
	}
	resolver := policy.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return &endpointGuard{
		resolver: resolver, allowedHosts: allowed,
		privateAllowedHosts: privateAllowed, privateAllowlist: allowlist,
	}, nil
}

func normalizedHostSet(values []string) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		host, err := normalizeHost(value)
		if err != nil {
			return nil, err
		}
		result[host] = struct{}{}
	}
	return result, nil
}

func normalizeHost(value string) (string, error) {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if host == "" {
		return "", errors.New("host is empty")
	}
	if strings.ContainsAny(host, "*/\\/@:#[]%") {
		if address, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
			return address.Unmap().String(), nil
		}
		return "", fmt.Errorf("host %q is not an exact hostname or address", value)
	}
	for _, character := range host {
		if character > unicode.MaxASCII || !(unicode.IsLetter(character) || unicode.IsDigit(character) || character == '.' || character == '-') {
			return "", fmt.Errorf("host %q contains unsupported characters", value)
		}
	}
	if address, err := netip.ParseAddr(host); err == nil {
		return address.Unmap().String(), nil
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", fmt.Errorf("host %q is malformed", value)
		}
	}
	return host, nil
}

func (guard *endpointGuard) resolve(ctx context.Context, rawURL string) (resolvedEndpoint, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return resolvedEndpoint{}, fmt.Errorf("providers: invalid endpoint URL: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Opaque != "" {
		return resolvedEndpoint{}, errors.New("providers: endpoint must use HTTPS")
	}
	if parsed.User != nil {
		return resolvedEndpoint{}, errors.New("providers: endpoint userinfo is forbidden")
	}
	if parsed.Fragment != "" {
		return resolvedEndpoint{}, errors.New("providers: endpoint fragments are forbidden")
	}
	host, err := normalizeHost(parsed.Hostname())
	if err != nil {
		return resolvedEndpoint{}, fmt.Errorf("providers: invalid endpoint host: %w", err)
	}
	if len(guard.allowedHosts) > 0 {
		if _, ok := guard.allowedHosts[host]; !ok {
			return resolvedEndpoint{}, fmt.Errorf("providers: endpoint host %q is not allowed", host)
		}
	}
	port := parsed.Port()
	if port == "" {
		port = "443"
	} else if number, portErr := strconv.Atoi(port); portErr != nil || number < 1 || number > 65535 {
		return resolvedEndpoint{}, errors.New("providers: endpoint port is invalid")
	}

	var addresses []netip.Addr
	if literal, parseErr := netip.ParseAddr(host); parseErr == nil {
		addresses = []netip.Addr{literal.Unmap()}
	} else {
		addresses, err = guard.resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return resolvedEndpoint{}, fmt.Errorf("providers: resolve endpoint host: %w", err)
		}
	}
	if len(addresses) == 0 {
		return resolvedEndpoint{}, errors.New("providers: endpoint host resolved to no addresses")
	}
	addresses = normalizeAddresses(addresses)
	if len(addresses) == 0 {
		return resolvedEndpoint{}, errors.New("providers: endpoint host resolved only to invalid addresses")
	}
	_, privateHostAllowed := guard.privateAllowedHosts[host]
	for _, address := range addresses {
		if err := guard.validateAddress(address, privateHostAllowed); err != nil {
			return resolvedEndpoint{}, fmt.Errorf("providers: endpoint address %s rejected: %w", address, err)
		}
	}
	originHost := host
	if address, parseErr := netip.ParseAddr(host); parseErr == nil && address.Is6() {
		originHost = "[" + host + "]"
	}
	origin := "https://" + originHost
	if port != "443" {
		origin += ":" + port
	}
	return resolvedEndpoint{origin: origin, host: host, port: port, addresses: addresses}, nil
}

func normalizeAddresses(input []netip.Addr) []netip.Addr {
	seen := make(map[netip.Addr]struct{}, len(input))
	result := make([]netip.Addr, 0, len(input))
	for _, address := range input {
		address = address.Unmap()
		if !address.IsValid() {
			continue
		}
		if _, ok := seen[address]; ok {
			continue
		}
		seen[address] = struct{}{}
		result = append(result, address)
	}
	slices.SortFunc(result, func(left, right netip.Addr) int { return left.Compare(right) })
	return result
}

func (guard *endpointGuard) validateAddress(address netip.Addr, privateHostAllowed bool) error {
	if !address.IsValid() || address.IsUnspecified() || address.IsMulticast() {
		return errors.New("unspecified or multicast address")
	}
	if !isRestrictedAddress(address) {
		return nil
	}
	if privateHostAllowed {
		return nil
	}
	for _, prefix := range guard.privateAllowlist {
		if prefix.Contains(address) {
			return nil
		}
	}
	return errors.New("private, loopback, link-local, or reserved address")
}

var restrictedPrefixes = mustPrefixes(
	"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
	"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
	"::/128", "100::/64", "2001:db8::/32", "2001:10::/28", "ff00::/8",
)

func mustPrefixes(values ...string) []netip.Prefix {
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		result = append(result, netip.MustParsePrefix(value))
	}
	return result
}

func isRestrictedAddress(address netip.Addr) bool {
	if address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || !address.IsGlobalUnicast() {
		return true
	}
	for _, prefix := range restrictedPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

type safeRoundTripper struct {
	guard          *endpointGuard
	dial           func(context.Context, string, string) (net.Conn, error)
	connectTimeout time.Duration
	tlsConfig      *tls.Config
	tlsTimeout     time.Duration
	headerTimeout  time.Duration

	mu         sync.Mutex
	transports map[string]*http.Transport
	order      []string
}

func newSafeHTTPClient(policy EndpointPolicy) (*http.Client, error) {
	guard, err := newEndpointGuard(policy)
	if err != nil {
		return nil, err
	}
	if policy.TLSConfig != nil && policy.TLSConfig.InsecureSkipVerify {
		return nil, errors.New("providers: TLS verification cannot be disabled")
	}
	if policy.TLSConfig != nil && policy.TLSConfig.MaxVersion != 0 && policy.TLSConfig.MaxVersion < tls.VersionTLS12 {
		return nil, errors.New("providers: TLS maximum version is below TLS 1.2")
	}
	connectTimeout := policy.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = defaultConnectTimeout
	}
	dial := policy.DialContext
	if dial == nil {
		dialer := &net.Dialer{Timeout: connectTimeout, KeepAlive: 30 * time.Second}
		dial = dialer.DialContext
	}
	tlsTimeout := policy.TLSHandshakeTimeout
	if tlsTimeout <= 0 {
		tlsTimeout = defaultTLSHandshakeTimeout
	}
	headerTimeout := policy.ResponseHeaderTimeout
	if headerTimeout <= 0 {
		headerTimeout = defaultResponseHeaderTimeout
	}
	transport := &safeRoundTripper{
		guard: guard, dial: dial, tlsConfig: policy.TLSConfig,
		connectTimeout: connectTimeout, tlsTimeout: tlsTimeout, headerTimeout: headerTimeout,
		transports: make(map[string]*http.Transport),
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("providers: redirects are disabled")
		},
	}, nil
}

func (transport *safeRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil {
		return nil, errors.New("providers: HTTP request URL is required")
	}
	resolved, err := transport.guard.resolve(request.Context(), request.URL.String())
	if err != nil {
		return nil, err
	}
	clone := request.Clone(request.Context())
	clone.Header = sanitizeHeaders(request.Header)
	clone.Host = ""
	return transport.transportFor(resolved).RoundTrip(clone)
}

func (transport *safeRoundTripper) transportFor(endpoint resolvedEndpoint) *http.Transport {
	parts := make([]string, 0, len(endpoint.addresses))
	for _, address := range endpoint.addresses {
		parts = append(parts, address.String())
	}
	key := endpoint.origin + "|" + strings.Join(parts, ",")
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if existing := transport.transports[key]; existing != nil {
		return existing
	}
	var cursor atomic.Uint64
	baseTLS := &tls.Config{MinVersion: tls.VersionTLS12}
	if transport.tlsConfig != nil {
		baseTLS = transport.tlsConfig.Clone()
		if baseTLS.MinVersion < tls.VersionTLS12 {
			baseTLS.MinVersion = tls.VersionTLS12
		}
	}
	baseTLS.InsecureSkipVerify = false
	baseTLS.ServerName = endpoint.host
	created := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, splitErr := net.SplitHostPort(address)
			if splitErr != nil || normalizeDialHost(host) != endpoint.host || port != endpoint.port {
				return nil, errors.New("providers: transport attempted an unvalidated dial target")
			}
			connectContext, cancel := context.WithTimeout(ctx, transport.connectTimeout)
			defer cancel()
			start := cursor.Add(1) - 1
			failures := make([]error, 0, len(endpoint.addresses))
			for offset := range endpoint.addresses {
				selected := endpoint.addresses[(int(start)+offset)%len(endpoint.addresses)]
				connection, dialErr := transport.dial(connectContext, network, net.JoinHostPort(selected.String(), endpoint.port))
				if dialErr == nil {
					return connection, nil
				}
				failures = append(failures, dialErr)
				if connectContext.Err() != nil {
					break
				}
			}
			return nil, fmt.Errorf("providers: all validated endpoint addresses failed: %w", errors.Join(failures...))
		},
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       baseTLS,
		TLSHandshakeTimeout:   transport.tlsTimeout,
		ResponseHeaderTimeout: transport.headerTimeout,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   32,
		MaxConnsPerHost:       128,
	}
	transport.transports[key] = created
	transport.order = append(transport.order, key)
	if len(transport.order) > maxOriginTransports {
		evictedKey := transport.order[0]
		transport.order = transport.order[1:]
		if evicted := transport.transports[evictedKey]; evicted != nil {
			evicted.CloseIdleConnections()
			delete(transport.transports, evictedKey)
		}
	}
	return created
}

func normalizeDialHost(value string) string {
	host, err := normalizeHost(value)
	if err != nil {
		return ""
	}
	return host
}

func sanitizeHeaders(input http.Header) http.Header {
	result := input.Clone()
	connectionTokens := strings.Split(result.Get("Connection"), ",")
	for _, name := range connectionTokens {
		result.Del(strings.TrimSpace(name))
	}
	for _, name := range []string{
		"Host", "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Proxy-Connection", "TE", "Trailer", "Transfer-Encoding", "Upgrade", "Content-Length",
		"Forwarded", "Via", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Port", "X-Forwarded-Proto",
		"X-Real-IP", "CF-Connecting-IP", "True-Client-IP",
	} {
		result.Del(name)
	}
	for name := range result {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "proxy-") || strings.HasPrefix(lower, "x-forwarded-") {
			result.Del(name)
		}
	}
	return result
}
