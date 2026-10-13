package hardenllm

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-401

import (
	"encoding/json"
	"github.com/prls-co/harden-llm/internal/providers"
	"testing"
)

func testConnection() Connection {
	return Connection{
		ID: "primary", Provider: "openai", Protocol: "responses",
		BaseURL: "https://api.openai.com/v1", APIKey: "fixture-only-key",
	}
}

func testOptions() Options {
	return Options{Connections: ConnectionCatalog{"primary": testConnection()}, DefaultConnection: "primary", Executor: &fixedExecutor{result: fixtureProviderResult()}}
}

func testOptionsWith(cache CacheStore) Options {
	options := testOptions()
	options.Cache = cache
	return options
}

func testMessages(user string) []Message {
	return []Message{testMessage("user", user)}
}

func testConversation(system, user string) []Message {
	return []Message{testMessage("system", system), testMessage("user", user)}
}

func testMessage(role, content string) Message {
	encoded, _ := json.Marshal(content)
	return Message{Role: role, Content: encoded}
}

func testProviderEffects(t *testing.T, options Options, policy EndpointPolicy) Options {
	t.Helper()
	executor, err := providers.NewRouter(providers.Config{EndpointPolicy: providers.EndpointPolicy{AllowedHosts: policy.AllowedHosts, PrivateAllowedHosts: policy.PrivateAllowedHosts, TLSConfig: policy.TLSConfig}})
	if err != nil {
		t.Fatal(err)
	}
	options.Executor = executor
	return options
}
