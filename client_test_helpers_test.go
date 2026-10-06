package hardenllm

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-401

import (
	"encoding/json"
)

func testConnection() Connection {
	return Connection{
		ID: "primary", Provider: "openai", Protocol: "responses",
		BaseURL: "https://api.openai.com/v1", APIKey: "fixture-only-key",
	}
}

func testOptions() Options {
	return Options{Connections: []Connection{testConnection()}, DefaultConnection: "primary"}
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
