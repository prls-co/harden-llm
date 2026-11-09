package httpapi

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-538

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"testing"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/stateful"
)

func TestOpenAIWebSearchContract(t *testing.T) {
	fixture, err := os.ReadFile("../../providers/testdata/web_search/openai_responses.json")
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer upstream.Close()
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	service, err := stateful.NewService(stateful.Config{Client: stateful.ClientOptions{Connections: []hardenllm.Connection{{ID: "native", Provider: "openai", Protocol: "responses", BaseURL: upstream.URL + "/v1", CacheDomain: "sdk", APIKey: "synthetic", SupportsWebSearch: true}}, DefaultConnection: "native", EndpointPolicy: hardenllm.EndpointPolicy{PrivateAllowedHosts: []string{"127.0.0.1"}, PrivateAllowlist: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}})
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := New(Config{APIKeys: []string{proxyTestToken}, Service: service})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(gateway.Handler())
	defer server.Close()
	client := openai.NewClient(option.WithBaseURL(server.URL+"/v1"), option.WithAPIKey(proxyTestToken), option.WithMaxRetries(0))
	params := responses.ResponseNewParams{Model: "fixture", Input: responses.ResponseNewParamsInputUnion{OfString: openai.String("hello")}}
	opts := []option.RequestOption{option.WithJSONSet("tools", []any{map[string]any{"type": "web_search"}}), option.WithJSONSet("include", []string{"web_search_call.action.sources"})}
	result, err := client.Responses.New(context.Background(), params, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Output) < 2 {
		t.Fatalf("missing output: %#v", result.Output)
	}
	call := result.Output[0].AsWebSearchCall()
	search := call.Action.AsSearch()
	if search.Query != "q one" || search.JSON.ExtraFields["sources"].Raw() != `[{"type":"url","url":"https://example.org/a"},{"type":"url","url":"https://example.org/b"}]` {
		t.Fatalf("search lost evidence: %#v", search)
	}
	var found bool
	for _, item := range result.Output {
		if item.Type == "message" {
			msg := item.AsMessage()
			if len(msg.Content) > 0 && len(msg.Content[0].Annotations) > 0 {
				c := msg.Content[0].Annotations[0].AsURLCitation()
				found = c.StartIndex == 6 && c.EndIndex == 10 && c.URL == "https://example.org/a"
			}
		}
	}
	if !found {
		t.Fatal("SDK did not decode citation")
	}
	stream := client.Responses.NewStreaming(context.Background(), params, opts...)
	defer stream.Close()
	events := map[string]bool{}
	for stream.Next() {
		events[stream.Current().Type] = true
	}
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	if !events["response.web_search_call.completed"] || !events["response.output_text.annotation.added"] {
		t.Fatalf("missing SDK events: %#v", events)
	}
	chat, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{Model: "fixture", Messages: []openai.ChatCompletionMessageParamUnion{{OfUser: &openai.ChatCompletionUserMessageParam{Content: openai.ChatCompletionUserMessageParamContentUnion{OfString: openai.String("hello")}}}}}, option.WithJSONSet("web_search_options", map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(chat.Choices) != 1 || len(chat.Choices[0].Message.Annotations) != 2 || chat.Choices[0].Message.Annotations[0].URLCitation.StartIndex != 6 {
		t.Fatalf("SDK chat lost citations: %#v", chat.Choices)
	}
}
