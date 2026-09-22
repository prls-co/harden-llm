// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-278

package capacity

import (
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

func TestCapacityReportPublicationIsPrivateAtomicAndDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capacity-report.json")
	report := ExecutionReport{
		SchemaVersion: 1,
		ReportKind:    "harden-llm-capacity.v1",
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
		SchemaVersion: 1,
		ReportKind:    "harden-llm-capacity.v1",
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
		SchemaVersion: 1,
		ReportKind:    "harden-llm-capacity.v1",
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
