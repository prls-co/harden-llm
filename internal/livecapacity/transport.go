package livecapacity

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"time"

	hardenllm "github.com/prls-co/harden-llm"
)

type Measurement struct {
	ValidatedNonce      string              `json:"validatedNonce,omitempty"`
	ID                  int                 `json:"id"`
	Route               string              `json:"route"`
	Attempt             int                 `json:"attempt"`
	Started             time.Time           `json:"startedAt"`
	Finished            time.Time           `json:"finishedAt"`
	QueueMS             int64               `json:"queueWaitMs"`
	RetryMS             int64               `json:"retryWaitMs"`
	DurationMS          int64               `json:"localGatewayHTTPMs"`
	HeadersMS           *int64              `json:"localGatewayHeadersMs"`
	HTTPStatus          int                 `json:"httpStatus"`
	ErrorCode           string              `json:"errorCode,omitempty"`
	ExecutionID         string              `json:"executionId,omitempty"`
	TraceID             string              `json:"traceId,omitempty"`
	Model               string              `json:"model"`
	NativeEffort        string              `json:"nativeEffort"`
	Attempts            []hardenllm.Attempt `json:"hllmAttempts"`
	CPAInternalAttempts *int                `json:"cpaInternalAttempts"` // not exposed by CPA
	InputTokens         *int64              `json:"inputTokens"`
	OutputTokens        *int64              `json:"outputTokens"`
	ReasoningTokens     *int64              `json:"reasoningTokens"`
	Outcome             Outcome             `json:"outcome"`
}
type Transport struct {
	URL, Token, NoncePrefix string
	Client                  *http.Client
}

func RequestBody(route, nonce string) []byte {
	request := map[string]any{
		"model": "gpt-6-luna", "input": "Return exactly the JSON object with nonce equal to " + nonce + ". No other text.",
		"max_output_tokens": 512, "reasoning": map[string]any{"effort": "low"}, "store": false,
		"text":   map[string]any{"format": map[string]any{"type": "json_schema", "name": "capacity_nonce", "strict": true, "schema": map[string]any{"type": "object", "properties": map[string]any{"nonce": map[string]any{"type": "string"}}, "required": []string{"nonce"}, "additionalProperties": false}}},
		"harden": map[string]any{"upstream": route, "cache": "off", "timeout_ms": 60000, "diagnostics": true, "recovery": map[string]any{"maxAttempts": 1, "retryOn": []string{}, "backoff": map[string]any{"baseDelayMs": 0, "maxDelayMs": 0}, "jsonRepair": nil, "rerun": nil}},
	}
	if route == "openai" {
		request["service_tier"] = "default"
	}
	body, _ := json.Marshal(request)
	return body
}
func (t Transport) Call(parent context.Context, item Item) Measurement {
	now := time.Now()
	m := Measurement{ID: item.ID, Route: item.Route, Attempt: item.Attempts, Started: now, QueueMS: item.QueueWait.Milliseconds(), RetryMS: item.RetryWait.Milliseconds(), NativeEffort: "low", Model: "gpt-6-luna", Outcome: Outcome{Kind: "unconfirmed"}}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	var headers time.Time
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotFirstResponseByte: func() { headers = time.Now() }})
	nonce := t.NoncePrefix + "-" + strconv.Itoa(item.ID)
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, t.URL+"/v1/responses", bytes.NewReader(RequestBody(item.Route, nonce)))
	request.Header.Set("Authorization", "Bearer "+t.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := t.Client.Do(request)
	if err == nil {
		m.HTTPStatus = response.StatusCode
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		var wire struct {
			Model      string `json:"model"`
			Status     string `json:"status"`
			OutputText string `json:"output_text"`
			Error      struct {
				Code string `json:"code"`
			} `json:"error"`
			Usage *struct {
				Input   int64 `json:"input_tokens"`
				Output  int64 `json:"output_tokens"`
				Details struct {
					Reasoning int64 `json:"reasoning_tokens"`
				} `json:"output_tokens_details"`
			} `json:"usage"`
			Harden struct {
				ExecutionID string                    `json:"execution_id"`
				TraceID     string                    `json:"trace_id"`
				Attempts    []hardenllm.Attempt       `json:"attempts"`
				Target      hardenllm.ExecutionTarget `json:"selected_target"`
			} `json:"harden"`
		}
		if readErr == nil && json.Unmarshal(raw, &wire) == nil {
			m.ErrorCode = wire.Error.Code
			m.ExecutionID = wire.Harden.ExecutionID
			m.TraceID = wire.Harden.TraceID
			m.Attempts = wire.Harden.Attempts
			if wire.Usage != nil && wire.Usage.Input >= 0 && wire.Usage.Output >= 0 {
				m.InputTokens = &wire.Usage.Input
				m.OutputTokens = &wire.Usage.Output
				m.ReasoningTokens = &wire.Usage.Details.Reasoning
				m.Outcome.UsageKnown = true
				// Deliberately ignore cache discounts for conservative settled accounting.
				m.Outcome.Cost = float64(wire.Usage.Input)*0.125/1e6 + float64(wire.Usage.Output)*0.5/1e6
			}
			if response.StatusCode == 200 {
				var output struct {
					Nonce string `json:"nonce"`
				}
				valid := json.Unmarshal([]byte(wire.OutputText), &output) == nil && output.Nonce == nonce && wire.Status == "completed" && wire.Model == "gpt-6-luna" && wire.Harden.Target.ConnectionID == item.Route && wire.Harden.Target.ModelID == "gpt-6-luna" && len(m.Attempts) == 1 && m.Attempts[0].ReasoningEffort == "low" && m.Attempts[0].DispatchObserved && m.Outcome.UsageKnown
				if valid {
					m.Outcome.Kind = "completed"
					m.ValidatedNonce = output.Nonce
				} else {
					m.Outcome.Kind = "failed"
					m.ErrorCode = "qualification_mismatch"
				}
			} else {
				status := response.StatusCode
				if len(m.Attempts) > 0 {
					status = m.Attempts[len(m.Attempts)-1].HTTPStatus
				}
				switch {
				case status == 401 || status == 403 || m.ErrorCode == "account_deactivated" || m.ErrorCode == "policy_violation" || m.ErrorCode == "insufficient_quota" || m.ErrorCode == "billing_hard_limit_reached" || m.ErrorCode == "billing_not_active":
					m.Outcome.Kind = "policy"
				case status == 429 || m.ErrorCode == "auth_unavailable":
					m.Outcome.Kind = "rejected"
				case status >= 400 && status < 500:
					m.Outcome.Kind = "failed"
				}
			}
		}
		m.Outcome.RetryAfter = RetryAfter(response.Header.Get("Retry-After"), time.Now())
	}
	m.Finished = time.Now()
	m.DurationMS = m.Finished.Sub(m.Started).Milliseconds()
	if !headers.IsZero() {
		n := headers.Sub(now).Milliseconds()
		m.HeadersMS = &n
	}
	return m
}
func RetryAfter(value string, now time.Time) time.Duration {
	if n, err := strconv.ParseInt(value, 10, 32); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date.Sub(now)
	}
	return 0
}
