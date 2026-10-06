package hardenllm

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

func validateTools(tools []FunctionTool, choice ToolChoice) error {
	if len(tools) > 128 {
		return errors.New("hardenllm: at most 128 function tools are supported")
	}
	known := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if !validFunctionName(tool.Name) || len(tool.Description) > 1024 || !utf8.ValidString(tool.Description) {
			return errors.New("hardenllm: function tool name or description is invalid")
		}
		if _, duplicate := known[tool.Name]; duplicate {
			return errors.New("hardenllm: function tool names must be unique")
		}
		known[tool.Name] = struct{}{}
		var parameters map[string]json.RawMessage
		if len(tool.Parameters) == 0 || json.Unmarshal(tool.Parameters, &parameters) != nil || parameters == nil {
			return errors.New("hardenllm: function tool parameters must be a JSON object")
		}
	}
	choice.Mode = strings.TrimSpace(choice.Mode)
	choice.Name = strings.TrimSpace(choice.Name)
	if len(tools) == 0 {
		if choice.Mode == "" || choice.Mode == "auto" || choice.Mode == "none" {
			if choice.Name != "" {
				return errors.New("hardenllm: tool choice name requires function mode")
			}
			return nil
		}
		if choice.Mode == "required" || choice.Mode == "function" {
			return errors.New("hardenllm: tool choice requires function tools")
		}
		return errors.New("hardenllm: tool choice mode is unsupported")
	}
	switch choice.Mode {
	case "", "auto", "none", "required":
		if choice.Name != "" {
			return errors.New("hardenllm: tool choice name requires function mode")
		}
	case "function":
		if !validFunctionName(choice.Name) {
			return errors.New("hardenllm: function tool choice requires a valid name")
		}
		if _, exists := known[choice.Name]; !exists {
			return errors.New("hardenllm: function tool choice must name a configured tool")
		}
	default:
		return errors.New("hardenllm: tool choice mode is unsupported")
	}
	return nil
}

func validFunctionName(value string) bool {
	if value == "" || len(value) > 64 || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' && character != '-' {
			return false
		}
	}
	return true
}
