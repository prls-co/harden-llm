package capacity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	maxGatewayStreamBytes = 2 << 20
	maxGatewaySSELine     = 64 << 10
)

type GatewayHTTPTransportConfig struct {
	Endpoint       string
	BearerToken    string
	ProfileID      string
	ScenarioID     string
	TestRunID      string
	SourceRevision string
	LocalHosts     []string
	Client         *http.Client
}

type GatewayHTTPTransport struct {
	endpoint       string
	bearerToken    string
	profileID      string
	scenarioID     string
	testRunID      string
	sourceRevision string
	client         *http.Client
}

func NewGatewayHTTPTransport(config GatewayHTTPTransportConfig) (*GatewayHTTPTransport, error) {
	if err := ValidateEndpoint(config.Endpoint, config.LocalHosts); err != nil {
		return nil, err
	}
	parsed, err := url.Parse(config.Endpoint)
	if err != nil || parsed.Path != "/api/v1/run" || parsed.RawPath != "" {
		return nil, errors.New("gateway endpoint must be the exact /api/v1/run route")
	}
	if strings.TrimSpace(config.BearerToken) != config.BearerToken || len(config.BearerToken) < 32 || strings.ContainsAny(config.BearerToken, "\r\n\t ") {
		return nil, errors.New("capacity gateway token must be a synthetic non-whitespace token of at least 32 bytes")
	}
	if strings.TrimSpace(config.ProfileID) == "" || len(config.ProfileID) > 128 {
		return nil, errors.New("capacity profile ID is required and bounded")
	}
	if !validSyntheticLabel(config.ScenarioID, 80) || !validSyntheticLabel(config.TestRunID, 128) {
		return nil, errors.New("capacity scenario and test run IDs must be bounded safe labels")
	}
	if len(config.SourceRevision) > 128 || strings.ContainsAny(config.SourceRevision, "\r\n\t ") {
		return nil, errors.New("capacity source revision is not a safe bounded label")
	}
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	clientCopy := *client
	if clientCopy.Timeout <= 0 || clientCopy.Timeout > 60*time.Second {
		clientCopy.Timeout = 60 * time.Second
	}
	clientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &GatewayHTTPTransport{
		endpoint: config.Endpoint, bearerToken: config.BearerToken, profileID: config.ProfileID,
		scenarioID: config.ScenarioID, testRunID: config.TestRunID, sourceRevision: config.SourceRevision,
		client: &clientCopy,
	}, nil
}

func validSyntheticLabel(value string, maximum int) bool {
	if value == "" || len(value) > maximum {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') && !(char >= '0' && char <= '9') && char != '-' && char != '_' && char != '.' {
			return false
		}
	}
	return true
}

func (transport *GatewayHTTPTransport) Send(ctx context.Context, request Request) (Response, error) {
	if ctx == nil || transport == nil || transport.client == nil {
		return Response{}, errors.New("capacity gateway transport is not initialized")
	}
	if request.ID < 1 {
		return Response{}, errors.New("capacity request ID must be positive")
	}
	origin := request.Origin
	if origin.Client == "" {
		origin = RequestOrigin{
			Client: "harden-llm-capacity-test", Component: "capacity-baseline",
			OperationID: transport.scenarioID, JobID: fmt.Sprintf("%s-request-%d", request.Population, request.ID),
			TestRunID: transport.testRunID, TestID: "TEST-277", SourceRevision: transport.sourceRevision,
		}
	}
	if origin.OperationID != transport.scenarioID || !validSyntheticLabel(origin.Client, 128) ||
		!validSyntheticLabel(origin.Component, 128) || !validSyntheticLabel(origin.OperationID, 80) ||
		!validSyntheticLabel(origin.JobID, 128) || !validSyntheticLabel(origin.TestRunID, 128) ||
		!validSyntheticLabel(origin.TestID, 64) || (origin.SourceRevision != "" && !validSyntheticLabel(origin.SourceRevision, 128)) {
		return Response{}, errors.New("capacity request origin is invalid or does not match its scenario")
	}
	cacheMode, userPrompt, err := gatewayCacheInputs(request, transport.scenarioID)
	if err != nil {
		return Response{}, err
	}
	recoveryPolicy, err := gatewayRecoveryPolicy(request.RecoveryPolicy)
	if err != nil {
		return Response{}, err
	}
	payload := map[string]any{
		"profileId":  transport.profileID,
		"userPrompt": userPrompt,
		"callType":   "structured",
		"schema": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"ok"},
			"properties":           map[string]any{"ok": map[string]any{"type": "boolean"}},
		},
		"cacheMode":      cacheMode,
		"recoveryPolicy": recoveryPolicy,
		"origin":         origin,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Response{}, errors.New("encode synthetic capacity request")
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, transport.endpoint, bytes.NewReader(body))
	if err != nil {
		return Response{}, errors.New("build capacity gateway request")
	}
	httpRequest.Header.Set("Authorization", "Bearer "+transport.bearerToken)
	httpRequest.Header.Set("Accept", "text/event-stream")
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := transport.client.Do(httpRequest)
	if err != nil {
		return Response{}, err
	}
	defer response.Body.Close()
	result := Response{StatusCode: response.StatusCode}
	if response.StatusCode != http.StatusOK {
		return result, nil
	}
	if !strings.EqualFold(strings.TrimSpace(strings.SplitN(response.Header.Get("Content-Type"), ";", 2)[0]), "text/event-stream") {
		return result, errors.New("gateway capacity response did not use the requested SSE transport")
	}
	result, err = readGatewaySSE(response.Body, result)
	if err != nil {
		return result, err
	}
	if result.StreamTerminal == "run.completed" || result.StreamTerminal == "run.failed" {
		if result.Origin != origin {
			return result, errors.New("gateway terminal response origin did not match the synthetic request")
		}
	}
	return result, nil
}

func gatewayCacheInputs(request Request, scenarioID string) (string, string, error) {
	cacheMode := "off"
	switch request.CacheMode {
	case "", "off":
	case "hit", "miss", "mixed":
		cacheMode = "cache"
	default:
		return "", "", errors.New("capacity scenario has an unsupported cache mode")
	}
	if request.CacheMode == "hit" {
		return cacheMode, fmt.Sprintf("Synthetic capacity cache fixture %s", scenarioID), nil
	}
	return cacheMode, fmt.Sprintf("Synthetic capacity fixture %s %s request %d", scenarioID, request.Population, request.ID), nil
}

func gatewayRecoveryPolicy(policy string) (map[string]any, error) {
	base := map[string]any{
		"maxAttempts": 1, "retryOn": []string{}, "backoff": map[string]int{"baseDelayMs": 0, "maxDelayMs": 0},
		"jsonRepair": nil, "rerun": nil,
	}
	switch policy {
	case "", "off":
		return base, nil
	case "retry":
		base["maxAttempts"] = 2
		base["retryOn"] = []string{"rate_limit"}
		return base, nil
	case "json-repair":
		base["maxAttempts"] = 2
		base["jsonRepair"] = map[string]any{
			"initial": map[string]string{"source": "generation"}, "escalation": nil,
		}
		return base, nil
	case "default":
		return map[string]any{
			"maxAttempts": 4,
			"retryOn":     []string{"network", "rate_limit", "server_error", "empty_response", "provider_retry"},
			"backoff":     map[string]int{"baseDelayMs": 500, "maxDelayMs": 8000},
			"jsonRepair": map[string]any{
				"initial":    map[string]string{"source": "generation"},
				"escalation": map[string]string{"source": "generation"},
			},
			"rerun": nil,
		}, nil
	default:
		return nil, errors.New("capacity scenario has an unsupported recovery policy")
	}
}

type gatewayEventEnvelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type gatewayEventData struct {
	Result struct {
		Status   string          `json:"status"`
		Output   json.RawMessage `json:"output"`
		CallID   string          `json:"callId"`
		RunID    string          `json:"runId"`
		TraceID  string          `json:"traceId"`
		Origin   RequestOrigin   `json:"origin"`
		Attempts []struct {
			Stage            string `json:"stage"`
			ProviderUsed     bool   `json:"providerUsed"`
			DispatchObserved bool   `json:"dispatchObserved"`
			Target           struct {
				ModelID string `json:"modelId"`
			} `json:"target"`
		} `json:"attempts"`
		Cache struct {
			Served bool `json:"served"`
		} `json:"cache"`
		Accounting struct {
			Provider struct {
				Usage struct {
					InputTokens         int64  `json:"inputTokens"`
					CacheReadTokens     int64  `json:"cacheReadTokens"`
					CacheCreationTokens int64  `json:"cacheCreationTokens"`
					OutputTokens        int64  `json:"outputTokens"`
					Status              string `json:"status"`
				} `json:"usage"`
			} `json:"provider"`
		} `json:"accounting"`
	} `json:"result"`
}

func readGatewaySSE(body io.Reader, response Response) (Response, error) {
	limited := &io.LimitedReader{R: body, N: maxGatewayStreamBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), maxGatewaySSELine)
	eventName := ""
	dataLines := make([]string, 0, 1)
	firstEvent := time.Time{}
	startedAt := time.Now()
	terminalSeen := false
	var parseErr error

	finishEvent := func() {
		if parseErr != nil || (eventName == "" && len(dataLines) == 0) {
			eventName, dataLines = "", dataLines[:0]
			return
		}
		response.EventCount++
		if firstEvent.IsZero() && eventName != "" {
			firstEvent = time.Now()
			response.FirstEvent = firstEvent.Sub(startedAt)
			response.FirstEventKnown = true
		}
		if len(dataLines) > 0 {
			var envelope gatewayEventEnvelope
			if err := json.Unmarshal([]byte(strings.Join(dataLines, "\n")), &envelope); err != nil {
				parseErr = errors.New("gateway SSE event data is invalid JSON")
			} else if envelope.Type != eventName {
				parseErr = errors.New("gateway SSE event name and payload type do not match")
			} else if eventName == "run.completed" || eventName == "run.failed" {
				if terminalSeen {
					parseErr = errors.New("gateway SSE returned more than one terminal event")
				} else {
					terminalSeen = true
					response.StreamTerminal = eventName
					parseErr = applyGatewayTerminal(&response, envelope.Data)
				}
			}
		}
		eventName, dataLines = "", dataLines[:0]
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			finishEvent()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			field, value = line, ""
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			eventName = value
		case "data":
			dataLines = append(dataLines, value)
		}
	}
	if scanner.Err() != nil {
		return response, errors.New("gateway SSE stream could not be read within its line bound")
	}
	if limited.N == 0 {
		return response, errors.New("gateway SSE stream exceeded the 2 MiB response bound")
	}
	response.ReceivedBytes = int64(maxGatewayStreamBytes + 1 - limited.N)
	finishEvent()
	if parseErr != nil {
		return response, parseErr
	}
	if !terminalSeen {
		return response, nil
	}
	return response, nil
}

func applyGatewayTerminal(response *Response, data json.RawMessage) error {
	var event gatewayEventData
	if err := json.Unmarshal(data, &event); err != nil {
		return errors.New("gateway terminal event data is invalid")
	}
	response.ExecutionStatus = event.Result.Status
	response.CallID = event.Result.CallID
	response.RunID = event.Result.RunID
	response.TraceID = event.Result.TraceID
	response.Origin = event.Result.Origin
	response.CacheHit = event.Result.Cache.Served
	response.JSONValid = len(event.Result.Output) > 0 && json.Valid(event.Result.Output)
	if response.CacheHit {
		return nil
	}
	for _, attempt := range event.Result.Attempts {
		if attempt.ProviderUsed || attempt.DispatchObserved {
			response.ProviderCalls = append(response.ProviderCalls, ProviderCall{
				Stage: attempt.Stage, Model: attempt.Target.ModelID,
			})
		}
	}
	usage := event.Result.Accounting.Provider.Usage
	if len(response.ProviderCalls) == 1 && usage.Status == "complete" && usage.CacheCreationTokens == 0 {
		response.ProviderCalls[0].InputTokens = usage.InputTokens + usage.CacheReadTokens
		response.ProviderCalls[0].CachedInputTokens = usage.CacheReadTokens
		response.ProviderCalls[0].OutputTokens = usage.OutputTokens
		response.ProviderCalls[0].TokenUsageKnown = true
	}
	return nil
}
