package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	hardenllm "github.com/prls-co/harden-llm"
)

type hardenRequest struct {
	Upstream    string                    `json:"upstream,omitempty"`
	Cache       hardenllm.CacheMode       `json:"cache,omitempty"`
	TimeoutMS   int64                     `json:"timeout_ms,omitempty"`
	Recovery    *hardenllm.RecoveryPolicy `json:"recovery,omitempty"`
	Diagnostics bool                      `json:"diagnostics,omitempty"`
}

// Tagged fields are validated locally and also forwarded. Untagged fields
// belong to the gateway's execution or response contract.
type requestOptions struct {
	options  map[string]any
	ordinary bool
}

func (o *requestOptions) nativeOptions() *requestOptions { return o }

type generationInput interface{ nativeOptions() *requestOptions }

type chatRequest struct {
	requestOptions
	Logprobs            *bool                 `json:"logprobs,omitempty"`
	TopLogprobs         json.RawMessage       `json:"top_logprobs,omitempty"`
	Model               string                `json:"model"`
	Messages            []json.RawMessage     `json:"messages"`
	Tools               []json.RawMessage     `json:"tools,omitempty"`
	ToolChoice          json.RawMessage       `json:"tool_choice,omitempty"`
	ResponseFormat      json.RawMessage       `json:"response_format,omitempty"`
	Stream              bool                  `json:"stream,omitempty"`
	StreamOptions       *streamOptions        `json:"stream_options,omitempty"`
	N                   *int                  `json:"n,omitempty"`
	Store               *bool                 `json:"store,omitempty" forward:"ordinary"`
	MaxTokens           *int                  `json:"max_tokens,omitempty" forward:"true"`
	MaxCompletionTokens *int                  `json:"max_completion_tokens,omitempty" forward:"true"`
	Temperature         *float64              `json:"temperature,omitempty" forward:"true"`
	TopP                *float64              `json:"top_p,omitempty" forward:"true"`
	FrequencyPenalty    *float64              `json:"frequency_penalty,omitempty" forward:"true"`
	PresencePenalty     *float64              `json:"presence_penalty,omitempty" forward:"true"`
	Seed                *int64                `json:"seed,omitempty" forward:"true"`
	Stop                json.RawMessage       `json:"stop,omitempty" forward:"true"`
	ReasoningEffort     string                `json:"reasoning_effort,omitempty"`
	WebSearchOptions    *chatWebSearchOptions `json:"web_search_options,omitempty"`
	Harden              hardenRequest         `json:"harden,omitempty"`
}

type chatWebSearchOptions struct {
	SearchContextSize string              `json:"search_context_size,omitempty"`
	UserLocation      *chatSearchLocation `json:"user_location,omitempty"`
}
type chatSearchLocation struct {
	Type        string                   `json:"type"`
	Approximate *chatApproximateLocation `json:"approximate,omitempty"`
}
type chatApproximateLocation struct {
	Country  string `json:"country,omitempty"`
	Region   string `json:"region,omitempty"`
	City     string `json:"city,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type responsesRequest struct {
	requestOptions
	ServiceTier        string            `json:"service_tier,omitempty" forward:"true"`
	Model              string            `json:"model"`
	Input              json.RawMessage   `json:"input"`
	Instructions       string            `json:"instructions,omitempty"`
	Tools              []json.RawMessage `json:"tools,omitempty"`
	ToolChoice         json.RawMessage   `json:"tool_choice,omitempty"`
	Text               json.RawMessage   `json:"text,omitempty"`
	Stream             bool              `json:"stream,omitempty"`
	Store              *bool             `json:"store,omitempty" forward:"ordinary"`
	PreviousResponseID string            `json:"previous_response_id,omitempty"`
	Background         *bool             `json:"background,omitempty"`
	MaxOutputTokens    *int              `json:"max_output_tokens,omitempty" forward:"true"`
	Temperature        *float64          `json:"temperature,omitempty" forward:"true"`
	TopP               *float64          `json:"top_p,omitempty" forward:"true"`
	Reasoning          json.RawMessage   `json:"reasoning,omitempty"`
	Include            []string          `json:"include,omitempty"`
	Truncation         string            `json:"truncation,omitempty" forward:"true"`
	ParallelToolCalls  *bool             `json:"parallel_tool_calls,omitempty" forward:"true"`
	N                  *int              `json:"n,omitempty"`
	Harden             hardenRequest     `json:"harden,omitempty"`
}

type chatResponseFormat struct {
	Type       string          `json:"type"`
	JSONSchema json.RawMessage `json:"json_schema,omitempty"`
}

type chatJSONSchemaFormat struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict *bool           `json:"strict,omitempty"`
}

type responsesTextFormat struct {
	Format json.RawMessage `json:"format"`
}

type responsesSchemaFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict *bool           `json:"strict,omitempty"`
}

type functionToolWire struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      *bool           `json:"strict,omitempty"`
	Function    *chatFunction   `json:"function,omitempty"`
}

type chatFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      *bool           `json:"strict,omitempty"`
}

type toolChoiceFunction struct {
	Type     string         `json:"type"`
	Name     string         `json:"name,omitempty"`
	Function *namedFunction `json:"function,omitempty"`
}

type namedFunction struct {
	Name string `json:"name"`
}

func (api *API) decodeRequest(writer http.ResponseWriter, request *http.Request, target generationInput) bool {
	mediaType := strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0])
	if mediaType != "application/json" {
		writeError(writer, http.StatusUnsupportedMediaType, "invalid_request_error", "unsupported_media_type", "Content-Type must be application/json.", "content_type")
		return false
	}
	if request.ContentLength > maximumRequestBytes {
		writeError(writer, http.StatusRequestEntityTooLarge, "invalid_request_error", "request_too_large", "The request body exceeds the configured size limit.", nil)
		return false
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maximumRequestBytes+1))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request_error", "invalid_json", "The request body could not be read.", nil)
		return false
	}
	return decodeGeneration(writer, body, target, true)
}

// Decode declared fields strictly. The remaining ordinary members are native
// options. Batch items use the same declarations with a closed boundary.
func decodeGeneration(w http.ResponseWriter, body []byte, target generationInput, ordinary bool) bool {
	if len(body) > maximumRequestBytes {
		writeError(w, 413, "invalid_request_error", "request_too_large", "The request body exceeds the configured size limit.", nil)
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		writeError(w, 400, "invalid_request_error", "invalid_json", "The request body must contain one JSON object.", nil)
		return false
	}
	if rejectStoredOrStateful(w, fields) {
		return false
	}
	opts := target.nativeOptions()
	opts.ordinary, opts.options = ordinary, make(map[string]any)
	object := reflect.ValueOf(target).Elem()
	for i := 0; i < object.NumField(); i++ {
		field := object.Type().Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		raw, found := fields[name]
		if name == "" || !found {
			continue
		}
		value := object.Field(i)
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(value.Addr().Interface()); err != nil {
			writeRequestError(w, errors.New("field has an invalid value or unsupported member"), name)
			return false
		}
		forward := field.Tag.Get("forward")
		if ordinary && forward != "" {
			continue
		}
		delete(fields, name)
		if !ordinary && forward == "true" && !value.IsZero() {
			// Durable snapshots use the original typed numeric representation.
			opts.options[name] = reflect.Indirect(value).Interface()
		}
	}
	for key, raw := range fields {
		if !ordinary || hardenllm.ReservedOrdinaryOption(key) {
			writeRequestError(w, errors.New("field is not available on this endpoint"), key)
			return false
		}
		var value any
		if err := decodeJSONValue(raw, &value); err != nil {
			writeRequestError(w, errors.New("invalid JSON option"), key)
			return false
		}
		opts.options[key] = value
	}
	return true
}

// Keep native siblings while the existing public validators consume the one
// member that affects execution or projection.
func (o *requestOptions) consumeMember(w http.ResponseWriter, name, member string, raw *json.RawMessage) bool {
	if len(*raw) == 0 {
		return true
	}
	var object map[string]any
	if err := decodeJSONValue(*raw, &object); err != nil {
		writeRequestError(w, errors.New("field must be an object"), name)
		return false
	}
	if _, summary := object["summary"]; name == "reasoning" && summary {
		writeRequestError(w, errors.New("reasoning summaries cannot be returned"), "reasoning.summary")
		return false
	}
	*raw = nil
	if value, found := object[member]; found {
		*raw = marshalRaw(map[string]any{member: value})
		delete(object, member)
	}
	if len(object) > 0 {
		o.options[name] = object
	}
	return true
}

func rejectStoredOrStateful(writer http.ResponseWriter, fields map[string]json.RawMessage) bool {
	for _, field := range []string{"previous_response_id", "conversation"} {
		if _, present := fields[field]; present {
			writeError(writer, http.StatusBadRequest, "invalid_request_error", "unsupported_field", fmt.Sprintf("The %s field is not supported by this stateless API.", field), field)
			return true
		}
	}
	if value, present := fields["background"]; present {
		var enabled bool
		if json.Unmarshal(value, &enabled) == nil && enabled {
			writeError(writer, http.StatusBadRequest, "invalid_request_error", "unsupported_field", "Background execution is not supported.", "background")
			return true
		}
	}
	if value, present := fields["store"]; present {
		var stored bool
		if json.Unmarshal(value, &stored) != nil || stored || string(value) == "null" {
			writeError(writer, http.StatusBadRequest, "invalid_request_error", "unsupported_field", "Server-side response storage is not supported.", "store")
			return true
		}
	}
	return false
}

func parseHarden(value hardenRequest) (connection string, cache hardenllm.CacheMode, timeout int64, recovery hardenllm.RecoveryPolicy, diagnostics bool, err error) {
	if value.Cache == "" {
		value.Cache = hardenllm.CacheModeOff
	}
	switch value.Cache {
	case hardenllm.CacheModeOff, hardenllm.CacheModeCache, hardenllm.CacheModeRefresh:
	default:
		return "", "", 0, hardenllm.RecoveryPolicy{}, false, errors.New("harden.cache must be off, cache or refresh")
	}
	if value.TimeoutMS < 0 || value.TimeoutMS > MaximumRunDuration.Milliseconds() {
		return "", "", 0, hardenllm.RecoveryPolicy{}, false, fmt.Errorf("harden.timeout_ms must be from 0 through %d", MaximumRunDuration.Milliseconds())
	}
	if value.TimeoutMS == 0 {
		timeout = MaximumRunDuration.Milliseconds()
	} else {
		timeout = value.TimeoutMS
	}
	recovery = hardenllm.DefaultRecoveryPolicy()
	if value.Recovery != nil {
		recovery = *value.Recovery
	}
	if err := recovery.Validate(); err != nil {
		return "", "", 0, hardenllm.RecoveryPolicy{}, false, err
	}
	return strings.TrimSpace(value.Upstream), value.Cache, timeout, recovery, value.Diagnostics, nil
}

func decodeTools(values []json.RawMessage, responses bool) ([]hardenllm.FunctionTool, json.RawMessage, error) {
	tools := make([]hardenllm.FunctionTool, 0, len(values))
	var webSearch json.RawMessage
	for _, encoded := range values {
		var head struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(encoded, &head) != nil {
			return nil, nil, errors.New("invalid tool")
		}
		if responses && hardenllm.IsWebSearchToolType(head.Type) {
			if webSearch != nil {
				return nil, nil, errors.New("only one web search tool is supported")
			}
			webSearch = append(json.RawMessage(nil), encoded...)
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.DisallowUnknownFields()
		var value functionToolWire
		if err := decoder.Decode(&value); err != nil {
			return nil, nil, errors.New("tools must contain supported function tool definitions")
		}
		if value.Type != "function" {
			return nil, nil, errors.New("only function tools and the Responses web_search tool are supported")
		}
		if !responses {
			if value.Function == nil || value.Name != "" || len(value.Parameters) != 0 {
				return nil, nil, errors.New("Chat Completions function tools must use the function object")
			}
			tools = append(tools, hardenllm.FunctionTool{Name: value.Function.Name, Description: value.Function.Description, Parameters: value.Function.Parameters, Strict: value.Function.Strict})
		} else {
			if value.Function != nil {
				return nil, nil, errors.New("Responses function tools must define name and parameters directly")
			}
			tools = append(tools, hardenllm.FunctionTool{Name: value.Name, Description: value.Description, Parameters: value.Parameters, Strict: value.Strict})
		}
	}
	return tools, webSearch, nil
}

func decodeToolChoice(encoded json.RawMessage, responses, searchDeclared bool) (hardenllm.ToolChoice, error) {
	if len(encoded) == 0 || bytes.Equal(bytes.TrimSpace(encoded), []byte("null")) {
		return hardenllm.ToolChoice{}, nil
	}
	var mode string
	if err := json.Unmarshal(encoded, &mode); err == nil {
		if mode == "auto" || mode == "none" || mode == "required" {
			return hardenllm.ToolChoice{Mode: mode}, nil
		}
		return hardenllm.ToolChoice{}, errors.New("tool_choice must be auto, none, required or a named function")
	}
	if responses {
		var searchChoice struct {
			Type string `json:"type"`
		}
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&searchChoice) == nil && hardenllm.IsWebSearchToolType(searchChoice.Type) {
			if !searchDeclared {
				return hardenllm.ToolChoice{}, errors.New("web search must be declared in tools before selecting it")
			}
			return hardenllm.ToolChoice{Mode: "required"}, nil
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var choice toolChoiceFunction
	if err := decoder.Decode(&choice); err != nil || choice.Type != "function" {
		return hardenllm.ToolChoice{}, errors.New("tool_choice has an unsupported shape")
	}
	if responses {
		if choice.Function != nil || choice.Name == "" {
			return hardenllm.ToolChoice{}, errors.New("Responses tool_choice requires a function name")
		}
		return hardenllm.ToolChoice{Mode: "function", Name: choice.Name}, nil
	}
	if choice.Function == nil || choice.Name != "" || choice.Function.Name == "" {
		return hardenllm.ToolChoice{}, errors.New("Chat Completions tool_choice requires a function name")
	}
	return hardenllm.ToolChoice{Mode: "function", Name: choice.Function.Name}, nil
}

func validateTextMessages(messages []json.RawMessage) ([]hardenllm.Message, error) {
	if len(messages) == 0 || len(messages) > 4096 {
		return nil, errors.New("messages must contain from 1 through 4096 entries")
	}
	result := make([]hardenllm.Message, 0, len(messages))
	pendingCalls := make(map[string]struct{})
	for _, encoded := range messages {
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.DisallowUnknownFields()
		var message hardenllm.Message
		if err := decoder.Decode(&message); err != nil {
			return nil, errors.New("messages contain an unsupported field or value")
		}
		switch message.Role {
		case "system", "developer", "user", "assistant", "tool":
		default:
			return nil, errors.New("message role is unsupported")
		}
		if len(message.Content) > 0 && !bytes.Equal(bytes.TrimSpace(message.Content), []byte("null")) {
			var content string
			if json.Unmarshal(message.Content, &content) != nil {
				return nil, errors.New("message content must be text")
			}
		}
		if message.Role != "assistant" && len(message.ToolCalls) > 0 {
			return nil, errors.New("only assistant messages may contain tool_calls")
		}
		if message.Role == "tool" && strings.TrimSpace(message.ToolCallID) == "" {
			return nil, errors.New("tool messages require tool_call_id")
		}
		if message.Role == "assistant" {
			for _, call := range message.ToolCalls {
				if call.Type != "function" || strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Function.Name) == "" || !json.Valid([]byte(call.Function.Arguments)) {
					return nil, errors.New("assistant tool_calls require valid function IDs, names and JSON arguments")
				}
				if _, duplicate := pendingCalls[call.ID]; duplicate {
					return nil, errors.New("tool call IDs must be unique within the request")
				}
				pendingCalls[call.ID] = struct{}{}
			}
		}
		if message.Role == "tool" {
			if _, exists := pendingCalls[message.ToolCallID]; !exists {
				return nil, errors.New("tool_call_id must refer to an earlier assistant tool call")
			}
			delete(pendingCalls, message.ToolCallID)
		}
		result = append(result, message)
	}
	return result, nil
}

func decodeResponsesInput(encoded json.RawMessage, instructions string) ([]hardenllm.Message, error) {
	if len(encoded) == 0 {
		return nil, errors.New("input is required")
	}
	var inputText string
	if json.Unmarshal(encoded, &inputText) == nil {
		messages := []hardenllm.Message{{Role: "user", Content: marshalRaw(inputText)}}
		if instructions != "" {
			messages = append([]hardenllm.Message{{Role: "system", Content: marshalRaw(instructions)}}, messages...)
		}
		return messages, nil
	}
	var items []json.RawMessage
	if json.Unmarshal(encoded, &items) != nil || len(items) == 0 || len(items) > 4096 {
		return nil, errors.New("input must be text or a non-empty array of text and function items")
	}
	messages := make([]hardenllm.Message, 0, len(items)+1)
	if instructions != "" {
		messages = append(messages, hardenllm.Message{Role: "system", Content: marshalRaw(instructions)})
	}
	for _, item := range items {
		var fields map[string]json.RawMessage
		if json.Unmarshal(item, &fields) != nil || fields == nil {
			return nil, errors.New("input items must be objects")
		}
		itemType := rawString(fields["type"])
		switch itemType {
		case "", "message":
			if err := allowFields(fields, "type", "role", "content", "id", "status"); err != nil {
				return nil, err
			}
			role := rawString(fields["role"])
			if role != "user" && role != "assistant" && role != "system" && role != "developer" {
				return nil, errors.New("message input role is unsupported")
			}
			content, err := decodeResponseText(fields["content"])
			if err != nil {
				return nil, err
			}
			messages = append(messages, hardenllm.Message{Role: role, Content: marshalRaw(content)})
		case "function_call":
			if err := allowFields(fields, "type", "id", "status", "call_id", "name", "arguments"); err != nil {
				return nil, err
			}
			arguments := rawString(fields["arguments"])
			if rawString(fields["call_id"]) == "" || rawString(fields["name"]) == "" || !json.Valid([]byte(arguments)) {
				return nil, errors.New("function_call requires call_id, name and JSON arguments")
			}
			call := hardenllm.ToolCall{
				ID: rawString(fields["call_id"]), Type: "function", ItemID: rawString(fields["id"]),
				Function: hardenllm.FunctionCall{Name: rawString(fields["name"]), Arguments: arguments},
			}
			if len(messages) > 0 && messages[len(messages)-1].Role == "assistant" && bytes.Equal(messages[len(messages)-1].Content, []byte(`""`)) {
				messages[len(messages)-1].ToolCalls = append(messages[len(messages)-1].ToolCalls, call)
			} else {
				messages = append(messages, hardenllm.Message{Role: "assistant", Content: marshalRaw(""), ToolCalls: []hardenllm.ToolCall{call}})
			}
		case "web_search_call":
			// A prior tool item is provenance, not conversation text.
			continue
		case "function_call_output":
			if err := allowFields(fields, "type", "call_id", "output"); err != nil {
				return nil, err
			}
			var output string
			if json.Unmarshal(fields["output"], &output) != nil || rawString(fields["call_id"]) == "" {
				return nil, errors.New("function_call_output requires call_id and text output")
			}
			messages = append(messages, hardenllm.Message{Role: "tool", ToolCallID: rawString(fields["call_id"]), Content: marshalRaw(output)})
		default:
			return nil, errors.New("input item type is unsupported")
		}
	}
	encodedMessages := make([]json.RawMessage, 0, len(messages))
	for _, message := range messages {
		encodedMessages = append(encodedMessages, marshalRaw(message))
	}
	return validateTextMessages(encodedMessages)
}

func decodeResponseText(encoded json.RawMessage) (string, error) {
	var text string
	if json.Unmarshal(encoded, &text) == nil {
		return text, nil
	}
	var parts []json.RawMessage
	if json.Unmarshal(encoded, &parts) != nil || len(parts) == 0 {
		return "", errors.New("message content must be text")
	}
	var builder strings.Builder
	for _, part := range parts {
		var fields map[string]json.RawMessage
		if json.Unmarshal(part, &fields) != nil || fields == nil {
			return "", errors.New("message content supports only text parts")
		}
		kind := rawString(fields["type"])
		if kind == "output_text" {
			if allowFields(fields, "type", "text", "annotations") != nil {
				return "", errors.New("message content supports only text parts")
			}
		} else if allowFields(fields, "type", "text") != nil {
			return "", errors.New("message content supports only text parts")
		}
		if kind != "input_text" && kind != "output_text" {
			return "", errors.New("message content supports only input_text and output_text parts")
		}
		builder.WriteString(rawString(fields["text"]))
	}
	return builder.String(), nil
}

func allowFields(fields map[string]json.RawMessage, allowed ...string) error {
	known := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		known[name] = struct{}{}
	}
	for name := range fields {
		if _, exists := known[name]; !exists {
			return errors.New("input item contains an unsupported field")
		}
	}
	return nil
}

func rawString(value json.RawMessage) string {
	var result string
	_ = json.Unmarshal(value, &result)
	return result
}

func marshalRaw(value any) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}
