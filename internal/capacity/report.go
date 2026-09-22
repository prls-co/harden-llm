package capacity

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxExecutionReportBytes = 1 << 20

const (
	maxRequestDiagnostics           = 96
	maxNonSuccessRequestDiagnostics = 32
	maxPerMetricRequestDiagnostics  = 12
	maxStoredArtifactSamples        = 96
)

type Fingerprint struct {
	SourceSHA         string `json:"sourceSha"`
	WorkingTreeSHA256 string `json:"workingTreeSha256"`
	ScenarioSHA       string `json:"scenarioSha"`
	ConfigSHA         string `json:"configSha"`
	ImageSetSHA       string `json:"imageSetSha"`
	TopologySHA       string `json:"topologySha"`
}

func (fingerprint Fingerprint) Comparable(other Fingerprint) bool {
	if fingerprint.SourceSHA == "" || fingerprint.WorkingTreeSHA256 == "" || fingerprint.ScenarioSHA == "" || fingerprint.ConfigSHA == "" || fingerprint.ImageSetSHA == "" || fingerprint.TopologySHA == "" {
		return false
	}
	if other.SourceSHA == "" || other.WorkingTreeSHA256 == "" || other.ScenarioSHA == "" || other.ConfigSHA == "" || other.ImageSetSHA == "" || other.TopologySHA == "" {
		return false
	}
	return fingerprint == other
}

type LatencyDistribution struct {
	SampleCount int           `json:"sampleCount"`
	MinNS       time.Duration `json:"minNs,omitempty"`
	P50NS       time.Duration `json:"p50Ns,omitempty"`
	P95NS       time.Duration `json:"p95Ns,omitempty"`
	P99NS       time.Duration `json:"p99Ns,omitempty"`
	MaxNS       time.Duration `json:"maxNs,omitempty"`
}

type StoredArtifact struct {
	RequestID  int    `json:"requestId"`
	ArtifactID string `json:"artifactId"`
	Kind       string `json:"kind"`
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"sizeBytes"`
}

// TrafficSummary exposes measured-population rates and stream volume without
// retaining an unbounded copy of every client request in the report.
type TrafficSummary struct {
	ScheduledDurationNS                time.Duration       `json:"scheduledDurationNs"`
	OfferedPerScheduledSecond          float64             `json:"offeredPerScheduledSecond"`
	LaunchedPerScheduledSecond         float64             `json:"launchedPerScheduledSecond"`
	SucceededPerScheduledSecond        float64             `json:"succeededPerScheduledSecond"`
	LaunchLag                          LatencyDistribution `json:"launchLag"`
	FirstEventLatency                  LatencyDistribution `json:"firstEventLatency"`
	StreamEvents                       int64               `json:"streamEvents"`
	MeanStreamEventsPerLaunchedRequest float64             `json:"meanStreamEventsPerLaunchedRequest"`
	MaxStreamEventsPerRequest          int64               `json:"maxStreamEventsPerRequest"`
	ReceivedBytes                      int64               `json:"receivedBytes"`
	MeanBytesPerLaunchedRequest        float64             `json:"meanBytesPerLaunchedRequest"`
	MaxBytesPerRequest                 int64               `json:"maxBytesPerRequest"`
}

type PopulationSummary struct {
	Offered            int            `json:"offered"`
	Launched           int            `json:"launched"`
	Unsent             int            `json:"unsent"`
	Succeeded          int            `json:"succeeded"`
	Failed             int            `json:"failed"`
	Rejected           int            `json:"rejected"`
	Canceled           int            `json:"canceled"`
	Unfinished         int            `json:"unfinished"`
	ProviderAttempts   int            `json:"providerAttempts"`
	StageDispatches    map[string]int `json:"stageDispatches"`
	ModelDispatches    map[string]int `json:"modelDispatches"`
	TokenUsage         []ProviderCall `json:"tokenUsage"`
	TokenUsageComplete bool           `json:"tokenUsageComplete"`
	Admitted           *int           `json:"admitted"`
}

// RequestDiagnostic is a bounded, body-free sample that keeps traceability for
// non-success outcomes and the most costly latency/stream outliers.
type RequestDiagnostic struct {
	RequestID       int            `json:"requestId"`
	Population      string         `json:"population"`
	Outcome         string         `json:"outcome"`
	StatusCode      int            `json:"statusCode,omitempty"`
	ExecutionStatus string         `json:"executionStatus,omitempty"`
	CallID          string         `json:"callId,omitempty"`
	RunID           string         `json:"runId,omitempty"`
	TraceID         string         `json:"traceId,omitempty"`
	Origin          RequestOrigin  `json:"origin"`
	ResultOrigin    *RequestOrigin `json:"resultOrigin,omitempty"`
	CacheHit        bool           `json:"cacheHit"`
	StreamTerminal  string         `json:"streamTerminal,omitempty"`
	LaunchLagNS     time.Duration  `json:"launchLagNs"`
	LatencyNS       time.Duration  `json:"latencyNs,omitempty"`
	FirstEventNS    time.Duration  `json:"firstEventNs,omitempty"`
	EventCount      int64          `json:"eventCount"`
	ReceivedBytes   int64          `json:"receivedBytes"`
	ProviderCalls   []ProviderCall `json:"providerCalls,omitempty"`
	Reasons         []string       `json:"reasons"`
}

type ScenarioReport struct {
	ScenarioID                string              `json:"scenarioId"`
	ExpectedOutcome           string              `json:"expectedOutcome"`
	DurationNS                time.Duration       `json:"durationNs"`
	Warmup                    PopulationSummary   `json:"warmup"`
	Measurement               PopulationSummary   `json:"measurement"`
	MeasuredTraffic           TrafficSummary      `json:"measuredTraffic"`
	WarmupLatency             LatencyDistribution `json:"warmupLatency"`
	MeasurementLatency        LatencyDistribution `json:"measurementLatency"`
	RequestDiagnostics        []RequestDiagnostic `json:"requestDiagnostics,omitempty"`
	RequestDiagnosticsOmitted int                 `json:"requestDiagnosticsOmitted"`
	ProviderRequestsReceived  int64               `json:"providerRequestsReceived"`
	DriverProviderDispatches  int64               `json:"driverProviderDispatches"`
	ProviderAccountingMatches bool                `json:"providerAccountingMatches"`
	StoredArtifacts           []StoredArtifact    `json:"storedArtifacts,omitempty"`
	StoredArtifactsOmitted    int                 `json:"storedArtifactsOmitted"`
	ArtifactCount             int                 `json:"artifactCount"`
	ArtifactBytesProduced     int64               `json:"artifactBytesProduced"`
	PersistedExecutions       int                 `json:"persistedExecutions"`
}

// RecordVerifiedArtifact preserves exact artifact totals while keeping only a
// bounded body-free sample of per-object metadata in the report.
func RecordVerifiedArtifact(report *ScenarioReport, artifact StoredArtifact) {
	if report == nil {
		return
	}
	report.ArtifactCount++
	report.ArtifactBytesProduced += artifact.SizeBytes
	if len(report.StoredArtifacts) >= maxStoredArtifactSamples {
		report.StoredArtifactsOmitted++
		return
	}
	report.StoredArtifacts = append(report.StoredArtifacts, artifact)
}

type ExecutionReport struct {
	SchemaVersion int              `json:"schemaVersion"`
	ReportKind    string           `json:"reportKind"`
	TestRunID     string           `json:"testRunId"`
	TestIDs       []string         `json:"testIds"`
	CaseSet       string           `json:"caseSet"`
	StartedAt     time.Time        `json:"startedAt"`
	EndedAt       time.Time        `json:"endedAt"`
	DurationMS    int64            `json:"durationMs"`
	Fingerprint   Fingerprint      `json:"fingerprint"`
	Cases         []ScenarioReport `json:"cases"`
	Costs         CostReport       `json:"costs"`
	Disposition   Disposition      `json:"disposition"`
	Limitations   []string         `json:"limitations"`
}

func SummarizeScenario(spec ScenarioSpec, result Result, providerRequests int64, duration time.Duration) ScenarioReport {
	var dispatches int64
	for _, population := range []PopulationResult{result.Warmup, result.PopulationResult} {
		for _, request := range population.Requests {
			dispatches += int64(len(request.ProviderCalls))
		}
	}
	requests := make([]RequestResult, 0, len(result.Warmup.Requests)+len(result.PopulationResult.Requests))
	requests = append(requests, result.Warmup.Requests...)
	requests = append(requests, result.PopulationResult.Requests...)
	diagnostics := summarizeRequestDiagnostics(requests)
	return ScenarioReport{
		ScenarioID: spec.ID, ExpectedOutcome: spec.ExpectedOutcome, DurationNS: duration,
		Warmup: summarizePopulation(result.Warmup), Measurement: summarizePopulation(result.PopulationResult),
		MeasuredTraffic: summarizeTraffic(spec, result.PopulationResult),
		WarmupLatency:   summarizeLatency(result.Warmup.Requests), MeasurementLatency: summarizeLatency(result.PopulationResult.Requests),
		RequestDiagnostics: diagnostics, RequestDiagnosticsOmitted: len(requests) - len(diagnostics),
		ProviderRequestsReceived: providerRequests, DriverProviderDispatches: dispatches,
		ProviderAccountingMatches: providerRequests == dispatches,
	}
}

func summarizePopulation(population PopulationResult) PopulationSummary {
	complete := true
	for _, request := range population.Requests {
		for _, call := range request.ProviderCalls {
			if !call.TokenUsageKnown {
				complete = false
			}
		}
	}
	usage := make(map[struct{ stage, model string }]ProviderCall, len(population.TokenUsage))
	for _, call := range population.TokenUsage {
		if !call.TokenUsageKnown {
			complete = false
			continue
		}
		key := struct{ stage, model string }{stage: call.Stage, model: call.Model}
		total, exists := usage[key]
		if !exists {
			total = ProviderCall{Stage: call.Stage, Model: call.Model, TokenUsageKnown: true}
		}
		var valid bool
		if total.InputTokens, valid = addTokenCount(total.InputTokens, call.InputTokens); !valid {
			complete = false
			continue
		}
		if total.CachedInputTokens, valid = addTokenCount(total.CachedInputTokens, call.CachedInputTokens); !valid {
			complete = false
			continue
		}
		if total.OutputTokens, valid = addTokenCount(total.OutputTokens, call.OutputTokens); !valid {
			complete = false
			continue
		}
		usage[key] = total
	}
	keys := make([]struct{ stage, model string }, 0, len(usage))
	for key := range usage {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].stage != keys[j].stage {
			return keys[i].stage < keys[j].stage
		}
		return keys[i].model < keys[j].model
	})
	tokenUsage := make([]ProviderCall, 0, len(keys))
	for _, key := range keys {
		tokenUsage = append(tokenUsage, usage[key])
	}
	var admitted *int
	if population.Admitted != nil {
		value := *population.Admitted
		admitted = &value
	}
	return PopulationSummary{
		Offered: population.Offered, Launched: population.Launched, Unsent: population.Unsent,
		Succeeded: population.Succeeded, Failed: population.Failed, Rejected: population.Rejected,
		Canceled: population.Canceled, Unfinished: population.Unfinished, ProviderAttempts: population.ProviderAttempts,
		StageDispatches: cloneCounts(population.StageDispatches), ModelDispatches: cloneCounts(population.ModelDispatches),
		TokenUsage: tokenUsage, TokenUsageComplete: complete, Admitted: admitted,
	}
}

func addTokenCount(total, value int64) (int64, bool) {
	if total < 0 || value < 0 || total > math.MaxInt64-value {
		return total, false
	}
	return total + value, true
}

func cloneCounts(counts map[string]int) map[string]int {
	if counts == nil {
		return map[string]int{}
	}
	copy := make(map[string]int, len(counts))
	for key, value := range counts {
		copy[key] = value
	}
	return copy
}

func summarizeRequestDiagnostics(requests []RequestResult) []RequestDiagnostic {
	selected := make(map[string]*RequestDiagnostic, maxRequestDiagnostics)
	ordered := make([]*RequestDiagnostic, 0, maxRequestDiagnostics)
	add := func(request RequestResult, reason string) {
		key := fmt.Sprintf("%s:%d", request.Population, request.ID)
		if existing, ok := selected[key]; ok {
			for _, existingReason := range existing.Reasons {
				if existingReason == reason {
					return
				}
			}
			existing.Reasons = append(existing.Reasons, reason)
			return
		}
		if len(ordered) >= maxRequestDiagnostics {
			return
		}
		diagnostic := requestDiagnostic(request, reason)
		selected[key] = &diagnostic
		ordered = append(ordered, &diagnostic)
	}
	orderedRequests := append([]RequestResult(nil), requests...)
	sort.Slice(orderedRequests, func(i, j int) bool {
		if orderedRequests[i].Population != orderedRequests[j].Population {
			return orderedRequests[i].Population < orderedRequests[j].Population
		}
		return orderedRequests[i].ID < orderedRequests[j].ID
	})
	nonSuccessCount := 0
	for _, request := range orderedRequests {
		if request.Outcome == "succeeded" {
			continue
		}
		if nonSuccessCount == maxNonSuccessRequestDiagnostics {
			break
		}
		add(request, "non_success_terminal")
		nonSuccessCount++
	}
	addHighest := func(reason string, value func(RequestResult) (int64, bool)) {
		candidates := make([]RequestResult, 0, len(orderedRequests))
		for _, request := range orderedRequests {
			if _, valid := value(request); valid {
				candidates = append(candidates, request)
			}
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			left, _ := value(candidates[i])
			right, _ := value(candidates[j])
			if left != right {
				return left > right
			}
			if candidates[i].Population != candidates[j].Population {
				return candidates[i].Population < candidates[j].Population
			}
			return candidates[i].ID < candidates[j].ID
		})
		limit := min(maxPerMetricRequestDiagnostics, len(candidates))
		for _, request := range candidates[:limit] {
			add(request, reason)
		}
	}
	addHighest("highest_latency", func(request RequestResult) (int64, bool) {
		return int64(request.Latency), !request.CompletedAt.IsZero() && request.Latency >= 0
	})
	addHighest("highest_first_event_latency", func(request RequestResult) (int64, bool) {
		return int64(request.FirstEvent), request.FirstEventKnown && request.FirstEvent >= 0
	})
	addHighest("highest_launch_lag", func(request RequestResult) (int64, bool) {
		return int64(request.LaunchLag), !request.LaunchedAt.IsZero() && request.LaunchLag >= 0
	})
	addHighest("largest_stream", func(request RequestResult) (int64, bool) {
		return request.ReceivedBytes, request.ReceivedBytes > 0
	})
	addHighest("most_stream_events", func(request RequestResult) (int64, bool) {
		return request.EventCount, request.EventCount > 0
	})
	result := make([]RequestDiagnostic, 0, len(ordered))
	for _, diagnostic := range ordered {
		result = append(result, *diagnostic)
	}
	return result
}

func requestDiagnostic(request RequestResult, reason string) RequestDiagnostic {
	diagnostic := RequestDiagnostic{
		RequestID: request.ID, Population: request.Population, Outcome: request.Outcome,
		StatusCode: request.StatusCode, ExecutionStatus: request.ExecutionStatus,
		CallID: request.CallID, RunID: request.RunID, TraceID: request.TraceID, Origin: request.Origin,
		CacheHit: request.CacheHit, StreamTerminal: request.StreamTerminal,
		LaunchLagNS: request.LaunchLag, LatencyNS: request.Latency, FirstEventNS: request.FirstEvent,
		EventCount: request.EventCount, ReceivedBytes: request.ReceivedBytes,
		ProviderCalls: append([]ProviderCall(nil), request.ProviderCalls...), Reasons: []string{reason},
	}
	if request.ResultOrigin != (RequestOrigin{}) && request.ResultOrigin != request.Origin {
		resultOrigin := request.ResultOrigin
		diagnostic.ResultOrigin = &resultOrigin
	}
	return diagnostic
}

func summarizeLatency(requests []RequestResult) LatencyDistribution {
	values := make([]time.Duration, 0, len(requests))
	for _, request := range requests {
		if request.CompletedAt.IsZero() || request.Latency < 0 {
			continue
		}
		values = append(values, request.Latency)
	}
	return summarizeDurations(values)
}

func summarizeDurations(values []time.Duration) LatencyDistribution {
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	if len(values) == 0 {
		return LatencyDistribution{}
	}
	p99 := time.Duration(0)
	if len(values) >= 1_000 {
		p99 = nearestRank(values, .99)
	}
	return LatencyDistribution{
		SampleCount: len(values), MinNS: values[0], P50NS: nearestRank(values, .50),
		P95NS: nearestRank(values, .95), P99NS: p99, MaxNS: values[len(values)-1],
	}
}

func summarizeTraffic(spec ScenarioSpec, result PopulationResult) TrafficSummary {
	duration := time.Duration(spec.MeasurementSeconds) * time.Second
	traffic := TrafficSummary{ScheduledDurationNS: duration}
	if duration > 0 {
		seconds := duration.Seconds()
		traffic.OfferedPerScheduledSecond = float64(result.Offered) / seconds
		traffic.LaunchedPerScheduledSecond = float64(result.Launched) / seconds
		traffic.SucceededPerScheduledSecond = float64(result.Succeeded) / seconds
	}
	launchLags := make([]time.Duration, 0, len(result.Requests))
	firstEvents := make([]time.Duration, 0, len(result.Requests))
	for _, request := range result.Requests {
		if request.LaunchedAt.IsZero() || request.LaunchLag < 0 {
			continue
		}
		launchLags = append(launchLags, request.LaunchLag)
		if request.FirstEventKnown && request.FirstEvent >= 0 {
			firstEvents = append(firstEvents, request.FirstEvent)
		}
		traffic.StreamEvents += request.EventCount
		traffic.ReceivedBytes += request.ReceivedBytes
		if request.EventCount > traffic.MaxStreamEventsPerRequest {
			traffic.MaxStreamEventsPerRequest = request.EventCount
		}
		if request.ReceivedBytes > traffic.MaxBytesPerRequest {
			traffic.MaxBytesPerRequest = request.ReceivedBytes
		}
	}
	traffic.LaunchLag = summarizeDurations(launchLags)
	traffic.FirstEventLatency = summarizeDurations(firstEvents)
	if result.Launched > 0 {
		traffic.MeanStreamEventsPerLaunchedRequest = float64(traffic.StreamEvents) / float64(result.Launched)
		traffic.MeanBytesPerLaunchedRequest = float64(traffic.ReceivedBytes) / float64(result.Launched)
	}
	return traffic
}

// ExplorationStopReason enforces the pre-approved stop-after-this-scenario
// limits. It reports a diagnostic reason rather than assigning a production
// availability target.
func ExplorationStopReason(scenarioID string, measured PopulationResult, launchLag LatencyDistribution) string {
	if measured.Launched >= 100 {
		unexpected := measured.Failed + measured.Rejected + measured.Canceled + measured.Unfinished
		if float64(unexpected)/float64(measured.Launched) > 0.05 {
			return fmt.Sprintf("exploration stopped after %s exceeded 5%% unexpected outcomes (%d/%d)", scenarioID, unexpected, measured.Launched)
		}
	}
	if launchLag.SampleCount > 0 && launchLag.P95NS > 100*time.Millisecond {
		return fmt.Sprintf("exploration stopped after %s exceeded 100 ms p95 scheduler launch lag", scenarioID)
	}
	return ""
}

func nearestRank(values []time.Duration, quantile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	index := int(math.Ceil(quantile*float64(len(values)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}

func CostsForExecution(cases []ScenarioReport) CostReport {
	var requests, successes int64
	var tokens []TokenUsage
	complete := true
	for _, scenario := range cases {
		for _, population := range []PopulationSummary{scenario.Warmup, scenario.Measurement} {
			requests += int64(population.Launched)
			successes += int64(population.Succeeded)
			if !population.TokenUsageComplete {
				complete = false
			}
			for _, usage := range population.TokenUsage {
				tokens = append(tokens, TokenUsage{
					Stage: usage.Stage, Model: usage.Model, InputTokens: usage.InputTokens,
					CachedInputTokens: usage.CachedInputTokens, OutputTokens: usage.OutputTokens,
				})
			}
		}
	}
	return CalculateCosts(CostInput{
		ClientRequests: requests, SuccessfulOutputs: successes, TokenUsageComplete: complete,
		Tokens: tokens, Prices: map[string]ModelPrice{},
	})
}

func WriteExecutionReport(filename string, report ExecutionReport) error {
	if strings.TrimSpace(filename) == "" {
		return errors.New("capacity report path is required")
	}
	if report.SchemaVersion != 2 || report.ReportKind != "harden-llm-capacity.v2" || report.TestRunID == "" || len(report.Cases) == 0 {
		return errors.New("capacity report identity or case set is invalid")
	}
	if err := boundStoredArtifactSamples(&report); err != nil {
		return err
	}
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode capacity report: %w", err)
	}
	content = append(content, '\n')
	if len(content) > maxExecutionReportBytes {
		return fmt.Errorf("capacity report exceeds the %d-byte limit", maxExecutionReportBytes)
	}
	directory := filepath.Dir(filename)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create capacity report directory: %w", err)
	}
	nonce := make([]byte, 8)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("generate private capacity report name: %w", err)
	}
	temporary := filepath.Join(directory, "."+filepath.Base(filename)+"."+hex.EncodeToString(nonce)+".tmp")
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create private capacity report: %w", err)
	}
	writeErr := error(nil)
	if _, writeErr = file.Write(content); writeErr == nil {
		writeErr = file.Sync()
	}
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("write capacity report: %w", writeErr)
	}
	if err := os.Link(temporary, filename); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("publish capacity report: %w", err)
	}
	if err := os.Remove(temporary); err != nil {
		return fmt.Errorf("remove temporary capacity report link: %w", err)
	}
	directoryFile, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open capacity report directory for sync: %w", err)
	}
	defer directoryFile.Close()
	if err := directoryFile.Sync(); err != nil {
		return fmt.Errorf("sync capacity report directory: %w", err)
	}
	return nil
}

func boundStoredArtifactSamples(report *ExecutionReport) error {
	if report == nil {
		return errors.New("capacity report is required")
	}
	cases := make([]ScenarioReport, len(report.Cases))
	copy(cases, report.Cases)
	report.Cases = cases
	maxInt := int(^uint(0) >> 1)
	for index := range report.Cases {
		scenario := &report.Cases[index]
		if len(scenario.StoredArtifacts) <= maxStoredArtifactSamples {
			continue
		}
		omitted := len(scenario.StoredArtifacts) - maxStoredArtifactSamples
		if scenario.StoredArtifactsOmitted > maxInt-omitted {
			return errors.New("capacity report omitted artifact count overflows")
		}
		scenario.StoredArtifactsOmitted += omitted
		scenario.StoredArtifacts = append([]StoredArtifact(nil), scenario.StoredArtifacts[:maxStoredArtifactSamples]...)
	}
	return nil
}

type TokenUsage struct {
	Stage             string
	Model             string
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
}

type ModelPrice struct {
	InputUSDPerMillion       float64
	CachedInputUSDPerMillion float64
	CachedInputPriceKnown    bool
	OutputUSDPerMillion      float64
	Source                   string
	AsOf                     string
}

type CostInput struct {
	ClientRequests        int64
	SuccessfulOutputs     int64
	TokenUsageComplete    bool
	Tokens                []TokenUsage
	Prices                map[string]ModelPrice
	ActualProviderCostUSD *float64
	StorageBytes          *int64
	StorageRetentionDays  int
	StorageUSDPerGiBMonth *float64
	InfrastructureCostUSD *float64
}

type MeasuredUSD struct {
	Value      *float64 `json:"value"`
	NullReason string   `json:"nullReason,omitempty"`
}

func knownUSD(value float64) MeasuredUSD {
	copy := value
	return MeasuredUSD{Value: &copy}
}

func unknownUSD(reason string) MeasuredUSD {
	return MeasuredUSD{NullReason: reason}
}

type CostReport struct {
	OfficialEquivalentUSD           MeasuredUSD            `json:"officialEquivalentUsd"`
	OfficialEquivalentPerRequestUSD MeasuredUSD            `json:"officialEquivalentPerRequestUsd"`
	OfficialEquivalentPerSuccessUSD MeasuredUSD            `json:"officialEquivalentPerSuccessUsd"`
	OfficialEquivalentByStageModel  map[string]MeasuredUSD `json:"officialEquivalentByStageModelUsd"`
	ActualProviderUSD               MeasuredUSD            `json:"actualProviderUsd"`
	ActualStorageUSD                MeasuredUSD            `json:"actualStorageUsd"`
	ActualInfrastructureUSD         MeasuredUSD            `json:"actualInfrastructureUsd"`
	ActualTotalUSD                  MeasuredUSD            `json:"actualTotalUsd"`
}

func CalculateCosts(input CostInput) CostReport {
	report := CostReport{OfficialEquivalentByStageModel: make(map[string]MeasuredUSD)}
	if input.ClientRequests < 0 || input.SuccessfulOutputs < 0 {
		report.OfficialEquivalentUSD = unknownUSD("request and success denominators must be non-negative")
	} else {
		total, stageCosts, reason := calculateOfficialEquivalent(input)
		if reason != "" {
			report.OfficialEquivalentUSD = unknownUSD(reason)
			for key := range stageCosts {
				report.OfficialEquivalentByStageModel[key] = unknownUSD(reason)
			}
		} else {
			report.OfficialEquivalentUSD = knownUSD(total)
			for key, cost := range stageCosts {
				report.OfficialEquivalentByStageModel[key] = knownUSD(cost)
			}
		}
	}

	if report.OfficialEquivalentUSD.Value == nil {
		report.OfficialEquivalentPerRequestUSD = unknownUSD(report.OfficialEquivalentUSD.NullReason)
		report.OfficialEquivalentPerSuccessUSD = unknownUSD(report.OfficialEquivalentUSD.NullReason)
	} else if input.ClientRequests == 0 {
		report.OfficialEquivalentPerRequestUSD = unknownUSD("client request denominator is zero")
	} else {
		report.OfficialEquivalentPerRequestUSD = knownUSD(*report.OfficialEquivalentUSD.Value / float64(input.ClientRequests))
	}
	if report.OfficialEquivalentUSD.Value != nil {
		if input.SuccessfulOutputs == 0 {
			report.OfficialEquivalentPerSuccessUSD = unknownUSD("successful output denominator is zero")
		} else {
			report.OfficialEquivalentPerSuccessUSD = knownUSD(*report.OfficialEquivalentUSD.Value / float64(input.SuccessfulOutputs))
		}
	}

	report.ActualProviderUSD = actualCost(input.ActualProviderCostUSD, "actual provider bill was not supplied")
	report.ActualStorageUSD = calculateStorageCost(input)
	report.ActualInfrastructureUSD = actualCost(input.InfrastructureCostUSD, "infrastructure cost was not supplied")
	actualParts := []MeasuredUSD{report.ActualProviderUSD, report.ActualStorageUSD, report.ActualInfrastructureUSD}
	missing := make([]string, 0, len(actualParts))
	actualTotal := 0.0
	for _, part := range actualParts {
		if part.Value == nil {
			missing = append(missing, part.NullReason)
			continue
		}
		actualTotal += *part.Value
	}
	if len(missing) > 0 {
		report.ActualTotalUSD = unknownUSD("actual total is incomplete: " + strings.Join(missing, "; "))
	} else if !finiteNonNegative(actualTotal) {
		report.ActualTotalUSD = unknownUSD("actual total is not a finite non-negative amount")
	} else {
		report.ActualTotalUSD = knownUSD(actualTotal)
	}
	return report
}

func calculateOfficialEquivalent(input CostInput) (float64, map[string]float64, string) {
	stageCosts := make(map[string]float64)
	if !input.TokenUsageComplete {
		return 0, stageCosts, "provider token usage completeness was not established"
	}
	if len(input.Tokens) == 0 {
		return 0, stageCosts, ""
	}

	models := make([]string, 0, len(input.Tokens))
	seen := make(map[string]bool, len(input.Tokens))
	for _, token := range input.Tokens {
		if token.Stage != "" && token.Model != "" {
			key := token.Stage + "/" + token.Model
			if !seen[key] {
				models = append(models, key)
				seen[key] = true
			}
		}
		if token.Model == "" || token.Stage == "" {
			return 0, stageCosts, "token usage record is missing model or stage"
		}
		if token.InputTokens < 0 || token.CachedInputTokens < 0 || token.OutputTokens < 0 || token.CachedInputTokens > token.InputTokens {
			return 0, stageCosts, fmt.Sprintf("token usage for stage %q and model %q is invalid", token.Stage, token.Model)
		}
		key := token.Stage + "/" + token.Model
		price, found := input.Prices[token.Model]
		if !found || price.Source == "" || price.AsOf == "" {
			return 0, stageCosts, fmt.Sprintf("official-equivalent price or provenance is unavailable for model %q", token.Model)
		}
		if !finiteNonNegative(price.InputUSDPerMillion) || !finiteNonNegative(price.OutputUSDPerMillion) || !finiteNonNegative(price.CachedInputUSDPerMillion) {
			return 0, stageCosts, fmt.Sprintf("official-equivalent price is invalid for model %q", token.Model)
		}
		if token.CachedInputTokens > 0 && !price.CachedInputPriceKnown {
			return 0, stageCosts, fmt.Sprintf("cached-input price is unavailable for model %q", token.Model)
		}
		uncachedInput := token.InputTokens - token.CachedInputTokens
		stageCost := (float64(uncachedInput)*price.InputUSDPerMillion + float64(token.CachedInputTokens)*price.CachedInputUSDPerMillion + float64(token.OutputTokens)*price.OutputUSDPerMillion) / 1_000_000
		if !finiteNonNegative(stageCost) {
			return 0, stageCosts, fmt.Sprintf("calculated token cost is invalid for stage %q and model %q", token.Stage, token.Model)
		}
		stageCosts[key] += stageCost
	}
	var total float64
	for _, key := range models {
		total += stageCosts[key]
	}
	if !finiteNonNegative(total) {
		return 0, stageCosts, "calculated official-equivalent total is invalid"
	}
	return total, stageCosts, ""
}

func actualCost(value *float64, missingReason string) MeasuredUSD {
	if value == nil {
		return unknownUSD(missingReason)
	}
	if !finiteNonNegative(*value) {
		return unknownUSD("cost must be a finite non-negative amount")
	}
	return knownUSD(*value)
}

func calculateStorageCost(input CostInput) MeasuredUSD {
	if input.StorageBytes == nil {
		return unknownUSD("storage bytes were not measured")
	}
	if *input.StorageBytes < 0 {
		return unknownUSD("storage bytes must be non-negative")
	}
	if input.StorageRetentionDays <= 0 {
		return unknownUSD("storage retention days were not supplied")
	}
	if input.StorageUSDPerGiBMonth == nil {
		return unknownUSD("storage price per GiB-month was not supplied")
	}
	if !finiteNonNegative(*input.StorageUSDPerGiBMonth) {
		return unknownUSD("storage price must be a finite non-negative amount")
	}
	const gib = float64(uint64(1) << 30)
	value := (float64(*input.StorageBytes) / gib) * (float64(input.StorageRetentionDays) / 30) * *input.StorageUSDPerGiBMonth
	if !finiteNonNegative(value) {
		return unknownUSD("calculated storage cost is invalid")
	}
	return knownUSD(value)
}

func finiteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

type Disposition string

const (
	DispositionSufficientCurrentTopology Disposition = "sufficient_current_topology"
	DispositionMeasuredBottleneck        Disposition = "measured_bottleneck"
	DispositionAvailabilityRequirement   Disposition = "availability_requirement"
	DispositionInsufficientEvidence      Disposition = "insufficient_evidence"
)

type DispositionInput struct {
	EvidenceValid             bool
	CurrentTopologySufficient bool
	MeasuredBottleneck        string
	AvailabilityRequirement   string
}

func ClassifyDisposition(input DispositionInput) Disposition {
	if !input.EvidenceValid {
		return DispositionInsufficientEvidence
	}
	if input.CurrentTopologySufficient {
		return DispositionSufficientCurrentTopology
	}
	if strings.TrimSpace(input.MeasuredBottleneck) != "" {
		return DispositionMeasuredBottleneck
	}
	if strings.TrimSpace(input.AvailabilityRequirement) != "" {
		return DispositionAvailabilityRequirement
	}
	return DispositionInsufficientEvidence
}

func SortedStageModelKeys(values map[string]MeasuredUSD) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
