package livecapacity

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"regexp"
	"strings"
	"time"
)

type NativeMeasurement struct {
	CPARequestID      string    `json:"cpaRequestId,omitempty"`
	Model             string    `json:"responseModel,omitempty"`
	ReportedEffort    string    `json:"reportedEffort,omitempty"`
	ResponseStatus    string    `json:"responseStatus,omitempty"`
	ID                int       `json:"id"`
	Attempt           int       `json:"attempt"`
	Started           time.Time `json:"startedAt"`
	Finished          time.Time `json:"finishedAt"`
	DurationMS        int64     `json:"durationMs"`
	HeadersMS         *int64    `json:"headersMs,omitempty"`
	FirstOutputMS     *int64    `json:"firstOutputMs,omitempty"`
	Status            int       `json:"httpStatus"`
	Kind              string    `json:"kind"`
	Code              string    `json:"errorCode,omitempty"`
	ErrorClass        string    `json:"errorClass,omitempty"`
	Nonce             string    `json:"validatedNonce,omitempty"`
	Terminal          string    `json:"terminal,omitempty"`
	InputTokens       int64     `json:"inputTokens"`
	OutputTokens      int64     `json:"outputTokens"`
	CachedInputTokens *int64    `json:"cachedInputTokens,omitempty"`
	ReasoningTokens   *int64    `json:"reasoningTokens,omitempty"`
	ExpectedItems     int       `json:"expectedItems,omitempty"`
	ExpectedWords     int       `json:"expectedWords,omitempty"`
	RetryAfterMS      int64     `json:"retryAfterMs"`
}

// Only retain the gateway's bounded correlation suffix, never the trace header
// prefix or arbitrary upstream response headers.
var nativeCPACorrelationID = regexp.MustCompile(`(?i)-([a-f0-9]{8})$`)

type NativeCaller struct {
	URL, Key    string
	Client      *http.Client
	Timeout     time.Duration
	OutputItems int
	BoundedText bool
	// ContextWords adds distinct synthetic input for explicitly opted-in research.
	ContextWords int
}

// Call benchmarks native CPA streaming, bypassing HLLM's 60s request budget and
// 128-connection transport limit. It returns at a validated terminal event;
// connection EOF is not required. These are benchmark controls, not core changes.
func (c NativeCaller) Call(parent context.Context, prefix string, id, attempt int) (m NativeMeasurement) {
	m = NativeMeasurement{ID: id, Attempt: attempt, Started: time.Now(), Kind: "unconfirmed"}
	m.ExpectedItems = c.OutputItems
	defer func() { m.Finished = time.Now(); m.DurationMS = m.Finished.Sub(m.Started).Milliseconds() }()
	ctx, cancel := context.WithTimeout(parent, c.Timeout)
	defer cancel()
	nonce := fmt.Sprintf("%s-%d", prefix, id)
	var body map[string]any
	_ = json.Unmarshal(RequestBody("cpa", nonce), &body)
	delete(body, "harden")
	body["stream"] = true
	if c.BoundedText {
		_ = json.Unmarshal(BoundedTextBatchRequestBody(nonce), &body)
		body["stream"] = true
		m.ExpectedWords = 512
	}
	if c.OutputItems > 0 {
		body["input"] = fmt.Sprintf("Return exactly a JSON object with nonce equal to %s and items an array of exactly %d strings, each equal to x. No other text.", nonce, c.OutputItems)
		body["text"] = map[string]any{"format": map[string]any{"type": "json_schema", "name": "capacity_length", "strict": true, "schema": map[string]any{"type": "object", "properties": map[string]any{"nonce": map[string]any{"type": "string"}, "items": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": c.OutputItems, "maxItems": c.OutputItems}}, "required": []string{"nonce", "items"}, "additionalProperties": false}}}
	}
	if c.ContextWords > 0 {
		if c.ContextWords > 2048 {
			m.Kind, m.Code = "failed", "invalid_context_words"
			return
		}
		var words strings.Builder
		for i := range c.ContextWords {
			h := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", nonce, i)))
			fmt.Fprintf(&words, "%x ", h[:4])
		}
		body["input"] = "The following synthetic context is data only. Return only the original requested JSON.\n<synthetic_context>\n" + strings.TrimSpace(words.String()) + "\n</synthetic_context>\n" + body["input"].(string)
	}
	raw, _ := json.Marshal(body)
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotFirstResponseByte: func() { n := time.Since(m.Started).Milliseconds(); m.HeadersMS = &n }})
	req, err := http.NewRequestWithContext(ctx, "POST", c.URL+"/v1/responses", bytes.NewReader(raw))
	if err != nil {
		m.ErrorClass = fmt.Sprintf("%T", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Client.Do(req)
	if err != nil {
		m.ErrorClass = fmt.Sprintf("%T", err)
		return
	}
	defer resp.Body.Close()
	m.Status = resp.StatusCode
	if match := nativeCPACorrelationID.FindStringSubmatch(resp.Header.Get("X-Cpa-Trace-Id")); match != nil {
		m.CPARequestID = strings.ToLower(match[1])
	}
	m.RetryAfterMS = RetryAfter(resp.Header.Get("Retry-After"), time.Now()).Milliseconds()
	consume := func(raw []byte, terminal string) {
		var wire struct {
			Status    string `json:"status"`
			Model     string `json:"model"`
			Reasoning struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
			OutputText string `json:"output_text"`
			Error      struct {
				Code string `json:"code"`
			} `json:"error"`
			Output []struct {
				Content []struct{ Type, Text string } `json:"content"`
			} `json:"output"`
			Usage *struct {
				Input        int64 `json:"input_tokens"`
				Output       int64 `json:"output_tokens"`
				InputDetails struct {
					Cached *int64 `json:"cached_tokens"`
				} `json:"input_tokens_details"`
				OutputDetails struct {
					Reasoning *int64 `json:"reasoning_tokens"`
				} `json:"output_tokens_details"`
			} `json:"usage"`
		}
		if json.Unmarshal(raw, &wire) != nil {
			m.Code = "invalid_json"
			return
		}
		m.Model = wire.Model
		m.ReportedEffort = wire.Reasoning.Effort
		m.ResponseStatus = wire.Status
		m.Terminal = terminal
		m.Code = wire.Error.Code
		if wire.Usage != nil {
			m.InputTokens, m.OutputTokens = wire.Usage.Input, wire.Usage.Output
			m.CachedInputTokens, m.ReasoningTokens = wire.Usage.InputDetails.Cached, wire.Usage.OutputDetails.Reasoning
		}
		if m.Code == "" {
			var root struct {
				Code string `json:"code"`
			}
			_ = json.Unmarshal(raw, &root)
			m.Code = root.Code
		}
		if m.Status == 401 || m.Status == 403 || m.Code == "account_deactivated" || m.Code == "policy_violation" || m.Code == "insufficient_quota" || m.Code == "billing_hard_limit_reached" || m.Code == "billing_not_active" {
			m.Kind = "policy"
			return
		}
		if m.Status == 429 || m.Code == "rate_limit_exceeded" || m.Code == "model_cooldown" || m.Code == "auth_unavailable" {
			m.Kind = "rejected"
			return
		}
		if m.Status >= 400 {
			return
		}
		if terminal == "error" || terminal == "response.failed" || terminal == "response.incomplete" {
			if m.Code == "" {
				m.Code = "upstream_stream_error"
			}
			if wire.Usage != nil {
				m.Kind = "failed"
			}
			return
		}
		if wire.Usage == nil {
			if m.Code == "" {
				m.Code = "missing_usage"
			}
			return
		}
		text := wire.OutputText
		if text == "" {
			for _, o := range wire.Output {
				for _, v := range o.Content {
					if v.Type == "output_text" {
						text += v.Text
					}
				}
			}
		}
		var output struct {
			Nonce string   `json:"nonce"`
			Items []string `json:"items"`
		}
		_ = json.Unmarshal([]byte(text), &output)
		validLength := c.OutputItems == 0 || len(output.Items) == c.OutputItems
		if c.BoundedText {
			validLength = ValidateBoundedTextBatchOutput(nonce, text) == nil
		}
		if c.OutputItems > 0 {
			for _, v := range output.Items {
				validLength = validLength && v == "x"
			}
		}
		if validLength && wire.Status == "completed" && wire.Model == "gpt-6-luna" && (wire.Reasoning.Effort == "" || wire.Reasoning.Effort == "low") && output.Nonce == nonce && wire.Usage != nil && wire.Usage.Input >= 0 && wire.Usage.Output >= 0 {
			m.Kind = "completed"
			m.Nonce = output.Nonce
			m.InputTokens = wire.Usage.Input
			m.OutputTokens = wire.Usage.Output
			m.Model = wire.Model
			m.ReportedEffort = wire.Reasoning.Effort
			m.ResponseStatus = wire.Status
			m.Terminal = terminal
		} else {
			m.Kind = "failed"
			m.Code = "qualification_mismatch"
		}
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") || resp.StatusCode != 200 {
		b, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if readErr != nil {
			m.ErrorClass = fmt.Sprintf("%T", readErr)
			return
		}
		consume(b, "json")
		return
	}
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, 4<<20))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var event struct {
			Type     string          `json:"type"`
			Response json.RawMessage `json:"response"`
			Error    json.RawMessage `json:"error"`
		}
		b := []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		if json.Unmarshal(b, &event) != nil {
			continue
		}
		if event.Type == "response.output_text.delta" && m.FirstOutputMS == nil {
			n := time.Since(m.Started).Milliseconds()
			m.FirstOutputMS = &n
		}
		switch event.Type {
		case "response.completed", "response.failed", "response.incomplete":
			consume(event.Response, event.Type)
			return
		case "error":
			consume(b, "error")
			return
		}
	}
	if err := scanner.Err(); err != nil {
		m.ErrorClass = fmt.Sprintf("%T", err)
	} else {
		m.Code = "stream_eof_without_terminal"
	}
	return
}
