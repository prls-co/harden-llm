package httpclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/prls-co/harden-llm/api"
	"github.com/prls-co/harden-llm/internal/schema"

	hardenllm "github.com/prls-co/harden-llm"
)

// ResponseMessage is an ordered Responses API message.
type ResponseMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type ResponseFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict bool            `json:"strict"`
}
type ResponseText struct {
	Format ResponseFormat `json:"format"`
}
type ResponseReasoning struct {
	Effort string `json:"effort"`
}
type ResponseControls struct {
	Upstream    string                    `json:"upstream,omitempty"`
	Cache       hardenllm.CacheMode       `json:"cache,omitempty"`
	TimeoutMS   int                       `json:"timeout_ms,omitempty"`
	Recovery    *hardenllm.RecoveryPolicy `json:"recovery,omitempty"`
	Diagnostics bool                      `json:"diagnostics,omitempty"`
}
type ResponsesInput struct {
	Model      string             `json:"model"`
	Input      []ResponseMessage  `json:"input"`
	Text       ResponseText       `json:"text"`
	Reasoning  *ResponseReasoning `json:"reasoning,omitempty"`
	Harden     ResponseControls   `json:"harden"`
	Tools      []json.RawMessage  `json:"tools,omitempty"`
	ToolChoice json.RawMessage    `json:"tool_choice,omitempty"`
	Include    []string           `json:"include,omitempty"`
}
type ResponsesOutput struct {
	Model      string               `json:"model"`
	OutputText string               `json:"output_text"`
	Harden     json.RawMessage      `json:"harden"`
	Output     []ResponseOutputItem `json:"output"`
}

type ResponseOutputItem struct {
	ID      string                  `json:"id"`
	Type    string                  `json:"type"`
	Status  string                  `json:"status"`
	Action  *ResponseSearchAction   `json:"action,omitempty"`
	Content []ResponseOutputContent `json:"content,omitempty"`
}
type ResponseSearchAction struct {
	Type    string                 `json:"type"`
	Query   string                 `json:"query,omitempty"`
	URL     string                 `json:"url,omitempty"`
	Pattern string                 `json:"pattern,omitempty"`
	Sources []ResponseSearchSource `json:"sources,omitempty"`
}
type ResponseSearchSource struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}
type ResponseOutputContent struct {
	Type        string               `json:"type"`
	Text        string               `json:"text"`
	Annotations []ResponseAnnotation `json:"annotations"`
}
type ResponseAnnotation struct {
	Type       string `json:"type"`
	StartIndex int    `json:"start_index"`
	EndIndex   int    `json:"end_index"`
	URL        string `json:"url"`
	Title      string `json:"title"`
}

// ResponsesFailure preserves the authoritative terminal error and optional
// execution metadata. Its Error string never includes remote output or input.
type ResponsesFailure struct {
	StatusCode int
	Code       string
	OutputText string
	Harden     json.RawMessage
}

func (*ResponsesFailure) Error() string { return "harden Responses request failed" }

// ResponsesUnconfirmed preserves received error facts without classifying the
// inference as terminal. It is the typed cause of an UnknownOutcomeError.
// OutputText and Harden may contain sensitive remote content; Error omits them.
type ResponsesUnconfirmed struct {
	StatusCode int
	Code       string
	OutputText string
	Harden     json.RawMessage
}

func (*ResponsesUnconfirmed) Error() string {
	return "harden Responses execution outcome is unconfirmed"
}

// ResponsesInvalidOutput is a received terminal response that does not satisfy
// the requested schema. It retains only its model and optional diagnostics.
type ResponsesInvalidOutput struct {
	Model  string
	Harden json.RawMessage
}

func (*ResponsesInvalidOutput) Error() string {
	return "harden Responses output violates the requested schema"
}

// Responses performs one POST and never replays an unknown outcome.
func (client *Client) Responses(ctx context.Context, input ResponsesInput) (ResponsesOutput, error) {
	var out ResponsesOutput
	if client == nil || client.http == nil || ctx == nil {
		return out, errors.New("harden client and context are required")
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if !utf8.ValidString(input.Model) || !utf8.ValidString(input.Text.Format.Type) || !utf8.ValidString(input.Text.Format.Name) || !utf8.ValidString(input.Harden.Upstream) || (input.Reasoning != nil && !utf8.ValidString(input.Reasoning.Effort)) {
		return out, &ProtocolError{}
	}
	for _, message := range input.Input {
		if !utf8.ValidString(message.Role) || !utf8.ValidString(message.Content) {
			return out, &ProtocolError{}
		}
	}
	for _, item := range input.Include {
		if !utf8.ValidString(item) {
			return out, &ProtocolError{}
		}
	}
	body, err := json.Marshal(input)
	if err != nil || !utf8.Valid(body) || len(body) > maximumRequestBytes || api.Validate("ResponseRequest", body) != nil {
		return out, &ProtocolError{}
	}
	normalizedSchema, err := schema.Normalize(input.Text.Format.Schema)
	if err != nil {
		return out, &ProtocolError{}
	}
	execution, cancel := context.WithTimeout(ctx, maximumClientDuration)
	defer cancel()
	response, err := client.request(execution, http.MethodPost, "/v1/responses", "application/json", body)
	if err != nil {
		return out, &UnknownOutcomeError{Cause: err}
	}
	defer response.Body.Close()
	if mediaType(response) != "application/json" {
		return out, &UnknownOutcomeError{Cause: &ProtocolError{}}
	}
	raw, err := readBounded(response.Body, maximumResponseBytes)
	if err != nil {
		return out, &UnknownOutcomeError{Cause: err}
	}
	if response.StatusCode != http.StatusOK {
		if api.Validate("Error", raw) != nil {
			return out, &UnknownOutcomeError{Cause: &ProtocolError{}}
		}
		var envelope struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
			OutputText string          `json:"output_text"`
			Harden     json.RawMessage `json:"harden"`
		}
		if json.Unmarshal(raw, &envelope) != nil {
			return out, &UnknownOutcomeError{Cause: &ProtocolError{}}
		}
		unconfirmed := func() error {
			return &UnknownOutcomeError{Cause: &ResponsesUnconfirmed{StatusCode: response.StatusCode, Code: envelope.Error.Code, OutputText: envelope.OutputText, Harden: envelope.Harden}}
		}
		// The tracking conflict is a documented pre-execution rejection. Other
		// no-retry responses can describe entered, uncertain provider work.
		trackingConflict := response.StatusCode == http.StatusConflict && envelope.Error.Code == "tracking_id_conflict"
		if envelope.Error.Code == "execution_unconfirmed" || (!trackingConflict && strings.EqualFold(strings.TrimSpace(response.Header.Get("x-should-retry")), "false")) {
			return out, unconfirmed()
		}
		if response.StatusCode >= 500 {
			var evidence struct {
				ExecutionID string `json:"execution_id"`
			}
			if json.Unmarshal(envelope.Harden, &evidence) != nil || evidence.ExecutionID == "" {
				return out, unconfirmed()
			}
		}
		return out, &ResponsesFailure{StatusCode: response.StatusCode, Code: envelope.Error.Code, OutputText: envelope.OutputText, Harden: envelope.Harden}
	}
	if err := decodeWire("Response", raw, &out); err != nil {
		return ResponsesOutput{}, &UnknownOutcomeError{Cause: err}
	}
	if _, _, err := schema.ParseAndValidate(out.OutputText, normalizedSchema); err != nil {
		return ResponsesOutput{}, &ResponsesInvalidOutput{Model: out.Model, Harden: out.Harden}
	}
	return out, nil
}
