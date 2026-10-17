package httpclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"unicode/utf8"

	hardenllm "github.com/prls-co/harden-llm"
	"github.com/prls-co/harden-llm/api"
	"github.com/prls-co/harden-llm/internal/schema"
)

func encodeRun(input RunInput) ([]byte, error) {
	invalid := errors.New("harden HTTP run input violates the published contract")
	if !utf8.ValidString(input.UserPrompt) || !utf8.ValidString(input.SystemPrompt) || !utf8.ValidString(input.ModelID) || len(input.Schema) > 64<<10 {
		return nil, invalid
	}
	options, err := json.Marshal(input.ProviderOptions)
	if err != nil || len(options) > 32<<10 {
		return nil, invalid
	}
	data, err := json.Marshal(input)
	if err != nil || len(data) > maximumRequestBytes || api.Validate("RunInput", data) != nil {
		return nil, invalid
	}
	if input.CallType == hardenllm.CallTypeStructured {
		if len(input.Schema) == 0 {
			return nil, invalid
		}
		if _, err := schema.Normalize(input.Schema); err != nil {
			return nil, invalid
		}
	} else if len(input.Schema) > 0 && string(input.Schema) != "null" {
		return nil, invalid
	}
	return data, nil
}

// Run sends exactly one inference POST. A known terminal survives a lost
// history tail; missing terminal evidence never becomes a retryable result.
func (client *Client) Run(ctx context.Context, input RunInput) (RunOutcome, error) {
	var outcome RunOutcome
	data, err := encodeRun(input)
	if err != nil {
		return outcome, err
	}
	if ctx == nil {
		return outcome, errors.New("harden HTTP context is required")
	}
	if err := ctx.Err(); err != nil {
		return outcome, err
	}
	execution, cancel := context.WithTimeout(ctx, maximumClientDuration)
	defer cancel()
	response, err := client.request(execution, http.MethodPost, "/api/v1/run", "text/event-stream", data)
	if err != nil {
		return outcome, &UnknownOutcomeError{Cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		err := rejection(response)
		var rejected *RejectedError
		if errors.As(err, &rejected) {
			return outcome, rejected
		}
		return outcome, &UnknownOutcomeError{Cause: err}
	}
	if mediaType(response) != "text/event-stream" {
		return outcome, &UnknownOutcomeError{Cause: &ProtocolError{}}
	}
	return readRunStream(response.Body, input)
}

func validateStructured(input RunInput, output json.RawMessage) error {
	if input.CallType == hardenllm.CallTypeText {
		var text string
		if json.Unmarshal(output, &text) != nil {
			return &ProtocolError{}
		}
		return nil
	}
	normalized, err := schema.Normalize(input.Schema)
	if err != nil {
		return &ProtocolError{}
	}
	if _, _, err := schema.ParseAndValidate(string(output), normalized); err != nil {
		return &ProtocolError{}
	}
	return nil
}
