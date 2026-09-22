package capacity

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	maxScenarioRequests = 2_000
	maxScenarioInflight = 256
)

type Scenario struct {
	OfferedRPS     int
	Warmup         time.Duration
	Measurement    time.Duration
	Drain          time.Duration
	MaxRequests    int
	MaxInflight    int
	RequireJSON    bool
	CacheMode      string
	RecoveryPolicy string
	Origin         RequestOrigin
}

// RequestOrigin is the bounded client-owned provenance attached to an
// individual synthetic REST call. JobID is populated by the open-loop driver.
type RequestOrigin struct {
	Client         string `json:"client,omitempty"`
	Component      string `json:"component,omitempty"`
	OperationID    string `json:"operationId,omitempty"`
	ParentRunID    string `json:"parentRunId,omitempty"`
	JobID          string `json:"jobId,omitempty"`
	TestRunID      string `json:"testRunId,omitempty"`
	TestID         string `json:"testId,omitempty"`
	SourceRevision string `json:"sourceRevision,omitempty"`
}

type Request struct {
	ID             int
	Population     string
	ScheduledAt    time.Time
	CacheMode      string
	RecoveryPolicy string
	Origin         RequestOrigin
}

type ProviderCall struct {
	Stage             string `json:"stage"`
	Model             string `json:"model"`
	InputTokens       int64  `json:"inputTokens"`
	CachedInputTokens int64  `json:"cachedInputTokens"`
	OutputTokens      int64  `json:"outputTokens"`
	TokenUsageKnown   bool   `json:"tokenUsageKnown"`
}

type Response struct {
	StatusCode      int
	StreamTerminal  string
	ExecutionStatus string
	CallID          string
	RunID           string
	TraceID         string
	Origin          RequestOrigin
	CacheHit        bool
	JSONValid       bool
	FirstEvent      time.Duration
	FirstEventKnown bool
	EventCount      int64
	ReceivedBytes   int64
	ProviderCalls   []ProviderCall
}

type Transport interface {
	Send(context.Context, Request) (Response, error)
}

type Clock interface {
	Now() time.Time
	Sleep(context.Context, time.Duration) error
}

type WallClock struct{}

func (WallClock) Now() time.Time { return time.Now() }

func (WallClock) Sleep(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type RequestResult struct {
	ID              int            `json:"id"`
	Population      string         `json:"population"`
	ScheduledAt     time.Time      `json:"scheduledAt"`
	LaunchedAt      time.Time      `json:"launchedAt"`
	CompletedAt     time.Time      `json:"completedAt,omitempty"`
	LaunchLag       time.Duration  `json:"launchLagNs"`
	Latency         time.Duration  `json:"latencyNs,omitempty"`
	Outcome         string         `json:"outcome"`
	StatusCode      int            `json:"statusCode,omitempty"`
	ExecutionStatus string         `json:"executionStatus,omitempty"`
	CallID          string         `json:"callId,omitempty"`
	RunID           string         `json:"runId,omitempty"`
	TraceID         string         `json:"traceId,omitempty"`
	Origin          RequestOrigin  `json:"origin"`
	ResultOrigin    RequestOrigin  `json:"resultOrigin,omitempty"`
	CacheHit        bool           `json:"cacheHit"`
	StreamTerminal  string         `json:"streamTerminal,omitempty"`
	FirstEvent      time.Duration  `json:"firstEventNs,omitempty"`
	FirstEventKnown bool           `json:"firstEventKnown"`
	EventCount      int64          `json:"eventCount"`
	ReceivedBytes   int64          `json:"receivedBytes"`
	ProviderCalls   []ProviderCall `json:"providerCalls,omitempty"`
}

type PopulationResult struct {
	Offered          int             `json:"offered"`
	Launched         int             `json:"launched"`
	Unsent           int             `json:"unsent"`
	Succeeded        int             `json:"succeeded"`
	Failed           int             `json:"failed"`
	Rejected         int             `json:"rejected"`
	Canceled         int             `json:"canceled"`
	Unfinished       int             `json:"unfinished"`
	ProviderAttempts int             `json:"providerAttempts"`
	StageDispatches  map[string]int  `json:"stageDispatches"`
	ModelDispatches  map[string]int  `json:"modelDispatches"`
	TokenUsage       []ProviderCall  `json:"tokenUsage"`
	Requests         []RequestResult `json:"requests"`
	Admitted         *int            `json:"admitted"`
}

type Result struct {
	PopulationResult
	Warmup PopulationResult `json:"warmup"`
}

type transportResult struct {
	requestID int
	response  Response
	err       error
	completed time.Time
}

func BuildArrivalSchedule(scenario Scenario) ([]time.Duration, error) {
	if err := validateScenario(scenario); err != nil {
		return nil, err
	}
	return buildArrivalSchedule(scenario.OfferedRPS, scenario.Measurement, scenario.MaxRequests)
}

func buildArrivalSchedule(offeredRPS int, duration time.Duration, maxRequests int) ([]time.Duration, error) {
	if offeredRPS <= 0 || duration <= 0 || maxRequests <= 0 {
		return nil, errors.New("arrival schedule rate, duration, and request bound must be positive")
	}
	product := new(big.Int).Mul(big.NewInt(int64(duration)), big.NewInt(int64(offeredRPS)))
	product.Add(product, big.NewInt(int64(time.Second)-1))
	count := product.Quo(product, big.NewInt(int64(time.Second)))
	if !count.IsInt64() || count.Int64() > int64(maxRequests) {
		return nil, fmt.Errorf("offered schedule exceeds maxRequests=%d", maxRequests)
	}
	schedule := make([]time.Duration, int(count.Int64()))
	for index := range schedule {
		// index is bounded by maxScenarioRequests, so this multiplication is safe.
		schedule[index] = time.Duration((int64(index) * int64(time.Second)) / int64(offeredRPS))
	}
	return schedule, nil
}

func validateScenario(scenario Scenario) error {
	switch {
	case scenario.OfferedRPS <= 0:
		return errors.New("offeredRPS must be positive")
	case scenario.Warmup < 0:
		return errors.New("warmup duration cannot be negative")
	case scenario.Warmup > 600*time.Second:
		return errors.New("warmup duration must be at most 600 seconds")
	case scenario.Measurement <= 0:
		return errors.New("measurement duration must be positive")
	case scenario.Measurement > 900*time.Second:
		return errors.New("measurement duration must be at most 900 seconds")
	case scenario.Drain < 0 || scenario.Drain > 120*time.Second:
		return errors.New("drain duration must be between zero and 120 seconds")
	case scenario.MaxRequests <= 0 || scenario.MaxRequests > maxScenarioRequests:
		return fmt.Errorf("maxRequests must be between 1 and %d", maxScenarioRequests)
	case scenario.MaxInflight <= 0 || scenario.MaxInflight > maxScenarioInflight:
		return fmt.Errorf("maxInflight must be between 1 and %d", maxScenarioInflight)
	}
	measurementCount, err := scheduleCount(scenario.OfferedRPS, scenario.Measurement)
	if err != nil {
		return err
	}
	warmupCount, err := scheduleCount(scenario.OfferedRPS, scenario.Warmup)
	if err != nil {
		return err
	}
	if measurementCount+warmupCount > int64(scenario.MaxRequests) {
		return fmt.Errorf("warmup and measurement arrivals exceed maxRequests=%d", scenario.MaxRequests)
	}
	return nil
}

func scheduleCount(offeredRPS int, duration time.Duration) (int64, error) {
	if duration <= 0 {
		return 0, nil
	}
	product := new(big.Int).Mul(big.NewInt(int64(duration)), big.NewInt(int64(offeredRPS)))
	product.Add(product, big.NewInt(int64(time.Second)-1))
	count := product.Quo(product, big.NewInt(int64(time.Second)))
	if !count.IsInt64() {
		return 0, errors.New("arrival count exceeds the supported integer range")
	}
	return count.Int64(), nil
}

// ValidateEndpoint prevents a capacity scenario from reaching a production or
// public provider. Explicit names are exact allow-list entries, not suffixes.
func ValidateEndpoint(endpoint string, explicitLocalHosts []string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed == nil || parsed.Hostname() == "" {
		return errors.New("provider endpoint must be an absolute local HTTP(S) URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("provider endpoint scheme must be http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("provider endpoint must not contain credentials, query, or fragment")
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	if host == "localhost" {
		return nil
	}
	for _, allowed := range explicitLocalHosts {
		if strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(allowed), "."), host) && allowed != "" {
			return nil
		}
	}
	return fmt.Errorf("provider endpoint host %q is not loopback or explicitly allow-listed local test infrastructure", host)
}

func Run(ctx context.Context, scenario Scenario, transport Transport, clock Clock) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("capacity run context is required")
	}
	if transport == nil {
		return Result{}, errors.New("capacity transport is required")
	}
	if clock == nil {
		return Result{}, errors.New("capacity clock is required")
	}
	if err := validateScenario(scenario); err != nil {
		return Result{}, err
	}
	measurementSchedule, err := buildArrivalSchedule(scenario.OfferedRPS, scenario.Measurement, scenario.MaxRequests)
	if err != nil {
		return Result{}, err
	}
	warmupBudget := scenario.MaxRequests - len(measurementSchedule)
	var warmupSchedule []time.Duration
	if scenario.Warmup > 0 {
		warmupSchedule, err = buildArrivalSchedule(scenario.OfferedRPS, scenario.Warmup, warmupBudget)
		if err != nil {
			return Result{}, err
		}
	}
	result := Result{
		PopulationResult: newPopulationResult(len(measurementSchedule)),
		Warmup:           newPopulationResult(len(warmupSchedule)),
	}
	nextID := 1
	if len(warmupSchedule) > 0 {
		nextID, err = runPopulation(ctx, scenario, "warmup", warmupSchedule, nextID, transport, clock, &result.Warmup)
		if err != nil {
			result.Unsent = result.Offered
			return result, err
		}
		if result.Warmup.Succeeded != result.Warmup.Launched || result.Warmup.Unsent > 0 || result.Warmup.Unfinished > 0 {
			result.Unsent = result.Offered
			return result, errors.New("capacity warmup did not complete successfully; measurement traffic was not started")
		}
	}
	_, err = runPopulation(ctx, scenario, "measurement", measurementSchedule, nextID, transport, clock, &result.PopulationResult)
	if err != nil {
		return result, err
	}
	if err := validatePopulationAccounting(result.PopulationResult); err != nil {
		return result, fmt.Errorf("measurement population: %w", err)
	}
	if err := validatePopulationAccounting(result.Warmup); err != nil {
		return result, fmt.Errorf("warmup population: %w", err)
	}
	return result, nil
}

func newPopulationResult(offered int) PopulationResult {
	return PopulationResult{
		Offered: offered, StageDispatches: make(map[string]int), ModelDispatches: make(map[string]int),
		TokenUsage: make([]ProviderCall, 0), Requests: make([]RequestResult, 0, offered),
	}
}

func runPopulation(ctx context.Context, scenario Scenario, population string, schedule []time.Duration, firstID int, transport Transport, clock Clock, result *PopulationResult) (int, error) {
	if len(schedule) == 0 {
		return firstID, nil
	}
	startedAt := clock.Now()
	completions := make(chan transportResult, len(schedule))
	cancels := make(map[int]context.CancelFunc, scenario.MaxInflight)
	terminalByID := make(map[int]bool, len(schedule))
	launchedByID := make(map[int]RequestResult, len(schedule))
	inflight := 0
	nextID := firstID

	applyCompletion := func(completed transportResult) {
		if terminalByID[completed.requestID] {
			return
		}
		terminalByID[completed.requestID] = true
		inflight--
		if cancel := cancels[completed.requestID]; cancel != nil {
			cancel()
			delete(cancels, completed.requestID)
		}
		requestResult := launchedByID[completed.requestID]
		requestResult.CompletedAt = completed.completed
		requestResult.Latency = maxDuration(0, completed.completed.Sub(requestResult.LaunchedAt))
		requestResult.StatusCode = completed.response.StatusCode
		requestResult.ExecutionStatus = completed.response.ExecutionStatus
		requestResult.CallID = completed.response.CallID
		requestResult.RunID = completed.response.RunID
		requestResult.TraceID = completed.response.TraceID
		requestResult.ResultOrigin = completed.response.Origin
		requestResult.CacheHit = completed.response.CacheHit
		requestResult.StreamTerminal = completed.response.StreamTerminal
		requestResult.FirstEvent = completed.response.FirstEvent
		requestResult.FirstEventKnown = completed.response.FirstEventKnown
		requestResult.EventCount = completed.response.EventCount
		requestResult.ReceivedBytes = completed.response.ReceivedBytes
		requestResult.ProviderCalls = append([]ProviderCall(nil), completed.response.ProviderCalls...)
		requestResult.Outcome = classifyResponse(completed.response, completed.err, scenario.RequireJSON)
		countOutcome(result, requestResult.Outcome)
		if !completed.response.CacheHit {
			for _, call := range completed.response.ProviderCalls {
				result.ProviderAttempts++
				stage := call.Stage
				if stage == "" {
					stage = "unknown"
				}
				model := call.Model
				if model == "" {
					model = "unknown"
				}
				result.StageDispatches[stage]++
				result.ModelDispatches[model]++
				if call.TokenUsageKnown {
					result.TokenUsage = append(result.TokenUsage, call)
				}
			}
		}
		result.Requests = append(result.Requests, requestResult)
	}

	drainCompletions := func() {
		for {
			select {
			case completion := <-completions:
				applyCompletion(completion)
			default:
				return
			}
		}
	}

	for index, offset := range schedule {
		requestID := nextID
		nextID++
		dueAt := startedAt.Add(offset)
		if wait := dueAt.Sub(clock.Now()); wait > 0 {
			if err := clock.Sleep(ctx, wait); err != nil {
				result.Unsent += len(schedule) - index
				break
			}
		}
		drainCompletions()
		if ctx.Err() != nil {
			result.Unsent += len(schedule) - index
			break
		}
		if inflight >= scenario.MaxInflight {
			result.Unsent++
			continue
		}

		now := clock.Now()
		origin := scenario.Origin
		origin.JobID = fmt.Sprintf("%s-request-%d", population, requestID)
		request := Request{
			ID: requestID, Population: population, ScheduledAt: dueAt,
			CacheMode: scenario.CacheMode, RecoveryPolicy: scenario.RecoveryPolicy, Origin: origin,
		}
		requestContext, cancel := context.WithCancel(ctx)
		cancels[request.ID] = cancel
		inflight++
		result.Launched++
		launched := RequestResult{
			ID: request.ID, Population: population, ScheduledAt: dueAt, LaunchedAt: now,
			LaunchLag: maxDuration(0, now.Sub(dueAt)), Origin: origin,
		}
		launchedByID[request.ID] = launched
		go func(request Request, requestContext context.Context) {
			response, sendErr := transport.Send(requestContext, request)
			completions <- transportResult{requestID: request.ID, response: response, err: sendErr, completed: clock.Now()}
		}(request, requestContext)
	}

	if inflight > 0 {
		drainTimer := time.NewTimer(scenario.Drain)
		defer drainTimer.Stop()
		for inflight > 0 {
			select {
			case completion := <-completions:
				applyCompletion(completion)
			case <-ctx.Done():
				for requestID, cancel := range cancels {
					cancel()
					if !terminalByID[requestID] {
						terminalByID[requestID] = true
						result.Canceled++
						requestResult := launchedByID[requestID]
						requestResult.Outcome = "canceled"
						result.Requests = append(result.Requests, requestResult)
						inflight--
					}
				}
			case <-drainTimer.C:
				for requestID, cancel := range cancels {
					cancel()
					if !terminalByID[requestID] {
						terminalByID[requestID] = true
						result.Unfinished++
						requestResult := launchedByID[requestID]
						requestResult.Outcome = "unfinished"
						result.Requests = append(result.Requests, requestResult)
						inflight--
					}
				}
			}
		}
	}

	sort.Slice(result.Requests, func(i, j int) bool { return result.Requests[i].ID < result.Requests[j].ID })
	if err := validatePopulationAccounting(*result); err != nil {
		return nextID, err
	}
	return nextID, ctx.Err()
}

func validatePopulationAccounting(result PopulationResult) error {
	if result.Offered != result.Launched+result.Unsent {
		return fmt.Errorf("offered accounting mismatch: %d != %d + %d", result.Offered, result.Launched, result.Unsent)
	}
	terminal := result.Succeeded + result.Failed + result.Rejected + result.Canceled + result.Unfinished
	if result.Launched != terminal {
		return fmt.Errorf("launched terminal accounting mismatch: %d != %d", result.Launched, terminal)
	}
	return nil
}

func classifyResponse(response Response, sendErr error, requireJSON bool) string {
	if errors.Is(sendErr, context.Canceled) {
		return "canceled"
	}
	if sendErr != nil {
		return "failed"
	}
	if response.StatusCode == 429 || response.StatusCode == 503 {
		return "rejected"
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "failed"
	}
	if response.StreamTerminal != "run.completed" {
		return "failed"
	}
	if response.ExecutionStatus != "" && response.ExecutionStatus != "succeeded" {
		return "failed"
	}
	if requireJSON && !response.JSONValid {
		return "failed"
	}
	return "succeeded"
}

func countOutcome(result *PopulationResult, outcome string) {
	switch outcome {
	case "succeeded":
		result.Succeeded++
	case "rejected":
		result.Rejected++
	case "canceled":
		result.Canceled++
	case "unfinished":
		result.Unfinished++
	default:
		result.Failed++
	}
}

func maxDuration(left, right time.Duration) time.Duration {
	if left > right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
