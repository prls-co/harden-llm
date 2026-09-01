package accounting

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-058

import (
	"math"
	"testing"
)

func TestAccountingUsageDerivationsAndCheckedAddition(t *testing.T) {
	t.Parallel()

	usage, err := CompleteUsage(11, 3, 2, 7, 5)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Status != UsageComplete || usage.PromptTokens() != 16 || usage.CompletionTokens() != 12 || usage.TotalTokens() != 28 {
		t.Fatalf("canonical usage = %#v", usage)
	}

	combined, err := AddUsage(usage, Usage{InputTokens: 1, OutputTokens: 2, Status: UsagePartial})
	if err != nil {
		t.Fatal(err)
	}
	if combined.Status != UsagePartial || combined.TotalTokens() != 31 {
		t.Fatalf("combined usage = %#v", combined)
	}

	if _, err := AddUsage(
		Usage{InputTokens: math.MaxInt64, Status: UsageComplete},
		Usage{InputTokens: 1, Status: UsageComplete},
	); err == nil {
		t.Fatal("token overflow was accepted")
	}
	if _, err := CompleteUsage(-1, 0, 0, 0, 0); err == nil {
		t.Fatal("negative token count was accepted")
	}
}

func TestAccountingCostCertaintyPreservesKnownSubtotal(t *testing.T) {
	t.Parallel()

	exact := ExactCost(0.000000000001, "reported")
	unknown := UnknownCost("missing_rate")
	partial, err := AddCost(exact, unknown)
	if err != nil {
		t.Fatal(err)
	}
	if partial.Status != CostPartial || partial.KnownSubtotalUSD != exact.KnownSubtotalUSD || partial.KnownObservations != 1 || partial.UnknownObservations != 1 {
		t.Fatalf("partial cost = %#v", partial)
	}
	if partial.KnownSubtotalUSD == 0 {
		t.Fatal("tiny known subtotal was rounded to zero")
	}

	unknownOnly, err := AddCost(unknown, UnknownCost("missing_rate"))
	if err != nil {
		t.Fatal(err)
	}
	if unknownOnly.Status != CostUnknown || unknownOnly.KnownSubtotalUSD != 0 || unknownOnly.UnknownObservations != 2 {
		t.Fatalf("unknown cost = %#v", unknownOnly)
	}
}

func TestAccountingSeparatesResultAndProviderLedgers(t *testing.T) {
	t.Parallel()

	resultUsage, err := CompleteUsage(10, 2, 0, 4, 1)
	if err != nil {
		t.Fatal(err)
	}
	accounting := Accounting{
		Result:   Ledger{Usage: resultUsage, Cost: ExactCost(0.0025, "cache_producer")},
		Provider: EmptyLedger(),
	}
	if accounting.Result.Usage.TotalTokens() != 17 || accounting.Provider.Usage.Status != UsageUnavailable || accounting.Provider.Cost.Status != CostUnavailable {
		t.Fatalf("cache accounting = %#v", accounting)
	}
}
