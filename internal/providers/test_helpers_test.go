package providers

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-012

import (
	"encoding/json"

	"github.com/prls-co/harden-llm/internal/runtime"
)

func providerMessage(role, text string) runtime.Message {
	content, _ := json.Marshal(text)
	return runtime.Message{Role: role, Content: content}
}

func providerMessages(system, user string) []runtime.Message {
	messages := make([]runtime.Message, 0, 2)
	if system != "" {
		messages = append(messages, providerMessage("system", system))
	}
	return append(messages, providerMessage("user", user))
}

func providerUserCall(text string) runtime.Call {
	return runtime.Call{ModelID: "fixture", CallType: "text", Messages: providerMessages("", text)}
}
