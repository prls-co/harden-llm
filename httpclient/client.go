package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/prls-co/harden-llm/api"
)

const (
	maximumRequestBytes   = 256 << 10
	maximumResponseBytes  = 16 << 20
	maximumErrorBytes     = 64 << 10
	maximumStreamBytes    = maximumResponseBytes + maximumErrorBytes
	maximumClientDuration = 75 * time.Second
)

type Config struct {
	BaseURL    string
	APIKey     string       `json:"-"`
	HTTPClient *http.Client `json:"-"`
}

func (Config) String() string          { return "httpclient.Config{APIKey:[redacted]}" }
func (config Config) GoString() string { return config.String() }

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func (*Client) String() string          { return "harden HTTP client (server-held bearer redacted)" }
func (client *Client) GoString() string { return client.String() }

func New(config Config) (*Client, error) {
	origin, err := url.Parse(config.BaseURL)
	if err != nil || origin == nil || origin.Host == "" || origin.Opaque != "" || origin.User != nil || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") || origin.RawPath != "" {
		return nil, errors.New("harden HTTP client requires an absolute origin without credentials, path, query or fragment")
	}
	if origin.Scheme != "https" {
		ip := net.ParseIP(origin.Hostname())
		if origin.Scheme != "http" || (origin.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return nil, errors.New("harden HTTP client requires HTTPS except explicit loopback test origins")
		}
	}
	if len(config.APIKey) < 32 || len(config.APIKey) > 512 {
		return nil, errors.New("harden HTTP client requires a valid server-held bearer")
	}
	for _, character := range []byte(config.APIKey) {
		if character < 0x21 || character > 0x7e {
			return nil, errors.New("harden HTTP client requires a valid server-held bearer")
		}
	}
	transport := http.Client{}
	if config.HTTPClient != nil {
		transport = *config.HTTPClient
	}
	transport.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if transport.Timeout <= 0 || transport.Timeout > maximumClientDuration {
		transport.Timeout = maximumClientDuration
	}
	origin.Path = ""
	return &Client{baseURL: strings.TrimSuffix(origin.String(), "/"), apiKey: config.APIKey, http: &transport}, nil
}

func (client *Client) request(ctx context.Context, method, path, accept string, body []byte) (*http.Response, error) {
	return client.requestHeaders(ctx, method, path, accept, body, nil)
}
func (client *Client) requestHeaders(ctx context.Context, method, path, accept string, body []byte, headers map[string]string) (*http.Response, error) {
	if client == nil || client.http == nil || ctx == nil {
		return nil, errors.New("harden HTTP client and context are required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+client.apiKey)
	request.Header.Set("Accept", accept)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	// The inference POST has no automatic transport replay or redirect path.
	if method == http.MethodPost {
		request.GetBody = nil
	}
	return client.http.Do(request)
}

func readBounded(reader io.Reader, limit int) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, &ProtocolError{}
	}
	return data, nil
}

func mediaType(response *http.Response) string {
	kind, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil {
		return ""
	}
	return strings.ToLower(kind)
}

func decodeWire(name string, data []byte, destination any) error {
	if err := api.Validate(name, data); err != nil {
		return &ProtocolError{}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return &ProtocolError{}
	}
	return nil
}

func rejection(response *http.Response) error {
	if mediaType(response) != "application/json" {
		return &ProtocolError{}
	}
	data, err := readBounded(response.Body, maximumErrorBytes)
	if err != nil {
		return err
	}
	var envelope struct {
		Error RunError `json:"error"`
	}
	if err := decodeWire("ReferenceErrorEnvelope", data, &envelope); err != nil {
		return err
	}
	return &RejectedError{StatusCode: response.StatusCode, Problem: envelope.Error}
}
