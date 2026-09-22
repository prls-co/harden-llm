// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-278

package capacity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCapacityReportCalculatesStageCostsAndSeparateDenominators(t *testing.T) {
	actualProviderCost := 6.0
	storageBytes := int64(1 << 30)
	storagePrice := 1.0
	infrastructureCost := 2.0
	got := CalculateCosts(CostInput{
		ClientRequests:        10,
		SuccessfulOutputs:     5,
		TokenUsageComplete:    true,
		Tokens:                []TokenUsage{{Stage: "generation", Model: "model-a", InputTokens: 1_000_000, OutputTokens: 2_000_000}},
		Prices:                map[string]ModelPrice{"model-a": {InputUSDPerMillion: 1, OutputUSDPerMillion: 2, Source: "official", AsOf: "2026-09-21"}},
		ActualProviderCostUSD: &actualProviderCost,
		StorageBytes:          &storageBytes,
		StorageRetentionDays:  30,
		StorageUSDPerGiBMonth: &storagePrice,
		InfrastructureCostUSD: &infrastructureCost,
	})
	if got.OfficialEquivalentUSD.Value == nil || *got.OfficialEquivalentUSD.Value != 5 {
		t.Fatalf("official-equivalent token cost = %#v, want $5", got.OfficialEquivalentUSD)
	}
	if got.OfficialEquivalentPerRequestUSD.Value == nil || *got.OfficialEquivalentPerRequestUSD.Value != 0.5 {
		t.Fatalf("official-equivalent cost/request = %#v, want $0.50", got.OfficialEquivalentPerRequestUSD)
	}
	if got.OfficialEquivalentPerSuccessUSD.Value == nil || *got.OfficialEquivalentPerSuccessUSD.Value != 1 {
		t.Fatalf("official-equivalent cost/success = %#v, want $1", got.OfficialEquivalentPerSuccessUSD)
	}
	if got.ActualTotalUSD.Value == nil || *got.ActualTotalUSD.Value != 9 {
		t.Fatalf("actual provider + storage + infrastructure = %#v, want $9", got.ActualTotalUSD)
	}
	if got.OfficialEquivalentByStageModel["generation/model-a"].Value == nil || *got.OfficialEquivalentByStageModel["generation/model-a"].Value != 5 {
		t.Fatalf("stage/model breakdown = %#v, want $5", got.OfficialEquivalentByStageModel)
	}
}

func TestCapacityReportUnknownPricesAndMissingMetricsStayUnknown(t *testing.T) {
	actualProviderCost := 0.2
	got := CalculateCosts(CostInput{
		ClientRequests:        1,
		SuccessfulOutputs:     1,
		TokenUsageComplete:    true,
		Tokens:                []TokenUsage{{Stage: "generation", Model: "unpriced-model", InputTokens: 100, OutputTokens: 50}},
		Prices:                map[string]ModelPrice{},
		ActualProviderCostUSD: &actualProviderCost,
	})
	if got.OfficialEquivalentUSD.Value != nil || got.OfficialEquivalentUSD.NullReason == "" {
		t.Fatalf("unknown official-equivalent price was treated as known: %#v", got.OfficialEquivalentUSD)
	}
	if got.ActualProviderUSD.Value == nil || *got.ActualProviderUSD.Value != actualProviderCost {
		t.Fatalf("actual provider bill was conflated with token-equivalent price: %#v", got.ActualProviderUSD)
	}
	if got.ActualTotalUSD.Value != nil || got.ActualTotalUSD.NullReason == "" {
		t.Fatalf("missing storage/infrastructure prices were treated as zero: %#v", got.ActualTotalUSD)
	}
}

func TestCapacityReportFingerprintComparisonRejectsMismatchedInputs(t *testing.T) {
	base := Fingerprint{SourceSHA: "source", WorkingTreeSHA256: "working-tree", ScenarioSHA: "scenario", ConfigSHA: "config", ImageSetSHA: "images", TopologySHA: "topology"}
	if !base.Comparable(base) {
		t.Fatal("identical workload fingerprints must be comparable")
	}
	different := base
	different.TopologySHA = "other-topology"
	if base.Comparable(different) {
		t.Fatal("different topology fingerprints must not be compared as before/after")
	}
	if (Fingerprint{}).Comparable(Fingerprint{}) {
		t.Fatal("missing fingerprints must not be considered comparable")
	}
}

func TestCapacityReportRequiresCachedPriceAndCompleteTokenUsage(t *testing.T) {
	base := CostInput{
		ClientRequests:     1,
		SuccessfulOutputs:  1,
		TokenUsageComplete: true,
		Tokens:             []TokenUsage{{Stage: "generation", Model: "model-a", InputTokens: 100, CachedInputTokens: 50, OutputTokens: 20}},
		Prices:             map[string]ModelPrice{"model-a": {InputUSDPerMillion: 1, OutputUSDPerMillion: 2, Source: "official", AsOf: "2026-09-21"}},
	}
	if got := CalculateCosts(base); got.OfficialEquivalentUSD.Value != nil || got.OfficialEquivalentUSD.NullReason == "" {
		t.Fatalf("missing cached-input price was treated as zero: %#v", got.OfficialEquivalentUSD)
	}
	base.Prices["model-a"] = ModelPrice{InputUSDPerMillion: 1, CachedInputUSDPerMillion: 0.25, CachedInputPriceKnown: true, OutputUSDPerMillion: 2, Source: "official", AsOf: "2026-09-21"}
	got := CalculateCosts(base)
	if got.OfficialEquivalentUSD.Value == nil || *got.OfficialEquivalentUSD.Value != 0.0001025 {
		t.Fatalf("cached and uncached input token cost = %#v, want $0.0001025", got.OfficialEquivalentUSD)
	}
	base.TokenUsageComplete = false
	if got := CalculateCosts(base); got.OfficialEquivalentUSD.Value != nil || got.OfficialEquivalentUSD.NullReason == "" {
		t.Fatalf("incomplete token usage was treated as complete: %#v", got.OfficialEquivalentUSD)
	}
}

func TestCapacityReportCompleteZeroProviderUsageIsKnownZero(t *testing.T) {
	got := CalculateCosts(CostInput{ClientRequests: 12, SuccessfulOutputs: 12, TokenUsageComplete: true})
	if got.OfficialEquivalentUSD.Value == nil || *got.OfficialEquivalentUSD.Value != 0 {
		t.Fatalf("complete zero-provider token usage = %#v, want known zero", got.OfficialEquivalentUSD)
	}
	if got.OfficialEquivalentPerRequestUSD.Value == nil || *got.OfficialEquivalentPerRequestUSD.Value != 0 {
		t.Fatalf("known zero request cost = %#v, want known zero", got.OfficialEquivalentPerRequestUSD)
	}
	if got.OfficialEquivalentPerSuccessUSD.Value == nil || *got.OfficialEquivalentPerSuccessUSD.Value != 0 {
		t.Fatalf("known zero success cost = %#v, want known zero", got.OfficialEquivalentPerSuccessUSD)
	}
}

func TestCapacityReportClassifiesAllFourEvidenceDispositions(t *testing.T) {
	cases := []struct {
		name string
		in   DispositionInput
		want Disposition
	}{
		{"sufficient current topology", DispositionInput{EvidenceValid: true, CurrentTopologySufficient: true}, DispositionSufficientCurrentTopology},
		{"measured bottleneck", DispositionInput{EvidenceValid: true, MeasuredBottleneck: "storage I/O"}, DispositionMeasuredBottleneck},
		{"availability requirement", DispositionInput{EvidenceValid: true, AvailabilityRequirement: "multi-zone"}, DispositionAvailabilityRequirement},
		{"insufficient evidence", DispositionInput{}, DispositionInsufficientEvidence},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := ClassifyDisposition(testCase.in); got != testCase.want {
				t.Fatalf("disposition = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestCapacityReportLatencyUsesNearestRankAndIgnoresIncompleteRequests(t *testing.T) {
	completed := time.Unix(1, 0)
	requests := []RequestResult{
		{Latency: time.Millisecond, CompletedAt: completed},
		{Latency: 3 * time.Millisecond, CompletedAt: completed},
		{Latency: 5 * time.Millisecond, CompletedAt: completed},
		{Latency: 9 * time.Millisecond, CompletedAt: completed},
		{Latency: -time.Millisecond, CompletedAt: completed},
		{Latency: 99 * time.Millisecond},
	}
	got := summarizeLatency(requests)
	want := LatencyDistribution{
		SampleCount: 4,
		MinNS:       time.Millisecond,
		P50NS:       3 * time.Millisecond,
		P95NS:       9 * time.Millisecond,
		MaxNS:       9 * time.Millisecond,
	}
	if got != want {
		t.Fatalf("latency distribution = %#v, want %#v", got, want)
	}
	if empty := summarizeLatency(nil); empty.SampleCount != 0 || empty.P50NS != 0 || empty.P99NS != 0 {
		t.Fatalf("empty latency population must remain empty, got %#v", empty)
	}
	if got.P99NS != 0 {
		t.Fatal("p99 must remain absent for fewer than 1000 samples")
	}
	large := make([]time.Duration, 1_000)
	for index := range large {
		large[index] = time.Duration(index+1) * time.Millisecond
	}
	if got := summarizeDurations(large); got.P99NS != 990*time.Millisecond || got.SampleCount != 1_000 {
		t.Fatalf("1000-sample p99 = %#v, want nearest-rank 990 ms", got)
	}
}

func TestCapacityReportSummarizesScheduledRateAndStreamingVolume(t *testing.T) {
	completed := time.Unix(1, 0)
	spec := ScenarioSpec{ID: "stream-growth", OfferedRPS: 2, MeasurementSeconds: 10}
	result := PopulationResult{
		Offered: 20, Launched: 3, Succeeded: 2,
		Requests: []RequestResult{
			{LaunchedAt: completed, LaunchLag: time.Millisecond, FirstEventKnown: true, FirstEvent: 5 * time.Millisecond, EventCount: 4, ReceivedBytes: 100},
			{LaunchedAt: completed, LaunchLag: 2 * time.Millisecond, FirstEventKnown: true, FirstEvent: 6 * time.Millisecond, EventCount: 8, ReceivedBytes: 200},
			{LaunchedAt: completed, LaunchLag: 3 * time.Millisecond, EventCount: 0, ReceivedBytes: 30},
		},
	}
	got := summarizeTraffic(spec, result)
	if got.ScheduledDurationNS != 10*time.Second || got.OfferedPerScheduledSecond != 2 || got.LaunchedPerScheduledSecond != 0.3 || got.SucceededPerScheduledSecond != 0.2 {
		t.Fatalf("scheduled traffic rates = %#v", got)
	}
	if got.LaunchLag.SampleCount != 3 || got.LaunchLag.P95NS != 3*time.Millisecond {
		t.Fatalf("launch lag summary = %#v", got.LaunchLag)
	}
	if got.FirstEventLatency.SampleCount != 2 || got.FirstEventLatency.P50NS != 5*time.Millisecond {
		t.Fatalf("first-event summary = %#v", got.FirstEventLatency)
	}
	if got.StreamEvents != 12 || got.MeanStreamEventsPerLaunchedRequest != 4 || got.MaxStreamEventsPerRequest != 8 {
		t.Fatalf("SSE event volume summary = %#v", got)
	}
	if got.ReceivedBytes != 330 || got.MeanBytesPerLaunchedRequest != 110 || got.MaxBytesPerRequest != 200 {
		t.Fatalf("response byte volume summary = %#v", got)
	}
}

func TestCapacityReportBoundsRequestDiagnosticsAtMaximumScenarioPopulation(t *testing.T) {
	const requestCount = 2_000
	base := time.Unix(1_800_000_000, 0).UTC()
	requests := make([]RequestResult, requestCount)
	providerCalls := make([]ProviderCall, requestCount)
	for index := range requests {
		requestID := index + 1
		requests[index] = RequestResult{
			ID: requestID, Population: "measurement", ScheduledAt: base.Add(time.Duration(index) * time.Millisecond),
			LaunchedAt:  base.Add(time.Duration(index)*time.Millisecond + time.Millisecond),
			CompletedAt: base.Add(time.Duration(index)*time.Millisecond + 10*time.Millisecond),
			LaunchLag:   time.Duration(index) * time.Microsecond, Latency: time.Duration(index+1) * time.Millisecond,
			Outcome: "succeeded", StatusCode: 200, ExecutionStatus: "succeeded",
			CallID: fmt.Sprintf("call-%04d", requestID), RunID: fmt.Sprintf("run-%04d", requestID),
			TraceID: fmt.Sprintf("trace-%04d", requestID),
			Origin: RequestOrigin{
				Client: "capacity-test", Component: "gateway", OperationID: fmt.Sprintf("operation-%04d", requestID),
				JobID: fmt.Sprintf("measurement-request-%d", requestID), TestRunID: "capacity-run",
				TestID: "TEST-277", SourceRevision: strings.Repeat("a", 40),
			},
			FirstEvent: time.Duration(index+1) * time.Millisecond, FirstEventKnown: true,
			EventCount: int64(requestID), ReceivedBytes: int64(requestID * 100),
			ProviderCalls: []ProviderCall{{Stage: "original.generate", Model: "synthetic-model", InputTokens: 1, OutputTokens: 1, TokenUsageKnown: true}},
		}
		providerCalls[index] = ProviderCall{Stage: "original.generate", Model: "synthetic-model", InputTokens: 1, OutputTokens: 1, TokenUsageKnown: true}
	}
	requests[0].Outcome = "failed"
	requests[0].ExecutionStatus = "failed"
	population := PopulationResult{
		Offered: requestCount, Launched: requestCount, Succeeded: requestCount - 1, Failed: 1,
		ProviderAttempts: requestCount, StageDispatches: map[string]int{"original.generate": requestCount},
		ModelDispatches: map[string]int{"synthetic-model": requestCount}, TokenUsage: providerCalls, Requests: requests,
	}
	scenario := SummarizeScenario(ScenarioSpec{ID: "maximum-scenario", MeasurementSeconds: 60}, Result{PopulationResult: population}, requestCount, time.Minute)
	report := ExecutionReport{
		SchemaVersion: 2, ReportKind: "harden-llm-capacity.v2", TestRunID: "capacity-run",
		TestIDs: []string{"TEST-277"}, CaseSet: "exploration", Cases: []ScenarioReport{scenario},
	}
	path := filepath.Join(t.TempDir(), "capacity-report.json")
	if err := WriteExecutionReport(path, report); err != nil {
		t.Fatalf("maximum bounded workload report should publish: %v", err)
	}
	metadata, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Size() > maxExecutionReportBytes {
		t.Fatalf("maximum workload report size = %d, limit = %d", metadata.Size(), maxExecutionReportBytes)
	}
	if len(scenario.RequestDiagnostics) > maxRequestDiagnostics || scenario.RequestDiagnosticsOmitted != requestCount-len(scenario.RequestDiagnostics) {
		t.Fatalf("request diagnostic bound/count = %d/%d, omitted=%d", len(scenario.RequestDiagnostics), maxRequestDiagnostics, scenario.RequestDiagnosticsOmitted)
	}
	if len(scenario.Measurement.TokenUsage) != 1 || scenario.Measurement.TokenUsage[0].InputTokens != requestCount || scenario.Measurement.TokenUsage[0].OutputTokens != requestCount {
		t.Fatalf("stage/model token usage was not exactly aggregated: %#v", scenario.Measurement.TokenUsage)
	}
	if !diagnosticHasReason(scenario.RequestDiagnostics, 1, "non_success_terminal") ||
		!diagnosticHasReason(scenario.RequestDiagnostics, requestCount, "highest_latency") ||
		!diagnosticHasReason(scenario.RequestDiagnostics, requestCount, "highest_first_event_latency") {
		t.Fatalf("failure, latency, and stream-start trace diagnostics were not retained: %#v", scenario.RequestDiagnostics)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var serialized struct {
		Cases []struct {
			Measurement json.RawMessage `json:"measurement"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(content, &serialized); err != nil {
		t.Fatalf("decode compact capacity report: %v", err)
	}
	if len(serialized.Cases) != 1 {
		t.Fatalf("serialized cases = %d, want 1", len(serialized.Cases))
	}
	var measured map[string]json.RawMessage
	if err := json.Unmarshal(serialized.Cases[0].Measurement, &measured); err != nil {
		t.Fatalf("decode summarized measurement: %v", err)
	}
	if _, exists := measured["requests"]; exists {
		t.Fatal("report serialized the unbounded raw measurement request slice")
	}
}

func diagnosticHasReason(diagnostics []RequestDiagnostic, requestID int, reason string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.RequestID != requestID {
			continue
		}
		for _, candidate := range diagnostic.Reasons {
			if candidate == reason {
				return true
			}
		}
	}
	return false
}

func TestCapacityReportPublicationIsPrivateAtomicAndDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capacity-report.json")
	report := ExecutionReport{
		SchemaVersion: 2,
		ReportKind:    "harden-llm-capacity.v2",
		TestRunID:     "run-one",
		Cases:         []ScenarioReport{{ScenarioID: "valid-text"}},
	}
	if err := WriteExecutionReport(path, report); err != nil {
		t.Fatalf("write valid report: %v", err)
	}
	metadata, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Mode().Perm() != 0o600 {
		t.Fatalf("report permissions = %04o, want 0600", metadata.Mode().Perm())
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteExecutionReport(path, ExecutionReport{
		SchemaVersion: 2,
		ReportKind:    "harden-llm-capacity.v2",
		TestRunID:     "run-two",
		Cases:         []ScenarioReport{{ScenarioID: "other"}},
	}); err == nil {
		t.Fatal("report publication must not replace an existing run report")
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(first) {
		t.Fatal("failed duplicate publication changed the accepted report")
	}
}

func TestCapacityReportRejectsOversizedPayloadBeforePublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capacity-report.json")
	report := ExecutionReport{
		SchemaVersion: 2,
		ReportKind:    "harden-llm-capacity.v2",
		TestRunID:     "run-one",
		Cases:         []ScenarioReport{{ScenarioID: "valid-text"}},
		Limitations:   []string{strings.Repeat("x", maxExecutionReportBytes)},
	}
	if err := WriteExecutionReport(path, report); err == nil {
		t.Fatal("oversized report unexpectedly succeeded")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("oversized report left a published target: %v", err)
	}
}
