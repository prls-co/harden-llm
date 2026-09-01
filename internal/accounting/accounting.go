// Package accounting is the single owner of normalized token usage, pricing,
// cost certainty, checked arithmetic, and result/provider ledger semantics.
package accounting

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

const costPrecision = 1_000_000_000_000

type UsageStatus string

const (
	UsageComplete     UsageStatus = "complete"
	UsagePartial      UsageStatus = "partial"
	UsageUnavailable  UsageStatus = "unavailable"
	UsageInconsistent UsageStatus = "inconsistent"
)

// Usage stores the five exclusive provider-neutral token components. Prompt,
// completion, and total values are always derived through the methods below.
type Usage struct {
	InputTokens         int64       `json:"inputTokens"`
	CacheReadTokens     int64       `json:"cacheReadTokens"`
	CacheCreationTokens int64       `json:"cacheCreationTokens"`
	OutputTokens        int64       `json:"outputTokens"`
	ReasoningTokens     int64       `json:"reasoningTokens"`
	Status              UsageStatus `json:"status"`
}

func CompleteUsage(input, cacheRead, cacheCreation, output, reasoning int64) (Usage, error) {
	usage := Usage{
		InputTokens: input, CacheReadTokens: cacheRead, CacheCreationTokens: cacheCreation,
		OutputTokens: output, ReasoningTokens: reasoning, Status: UsageComplete,
	}
	if err := usage.Validate(); err != nil {
		return Usage{}, err
	}
	return usage, nil
}

func UnavailableUsage() Usage { return Usage{Status: UsageUnavailable} }

func (usage Usage) Validate() error {
	switch usage.Status {
	case UsageComplete, UsagePartial, UsageUnavailable, UsageInconsistent:
	default:
		return fmt.Errorf("accounting: unsupported usage status %q", usage.Status)
	}
	for _, value := range []int64{usage.InputTokens, usage.CacheReadTokens, usage.CacheCreationTokens, usage.OutputTokens, usage.ReasoningTokens} {
		if value < 0 {
			return errors.New("accounting: usage contains a negative token count")
		}
	}
	if usage.Status == UsageUnavailable && usage.componentTotalUnsafe() != 0 {
		return errors.New("accounting: unavailable usage contains token counts")
	}
	if usage.Status == UsageInconsistent {
		return errors.New("accounting: usage is inconsistent")
	}
	_, err := checkedSum(
		usage.InputTokens, usage.CacheReadTokens, usage.CacheCreationTokens,
		usage.OutputTokens, usage.ReasoningTokens,
	)
	return err
}

func (usage Usage) PromptTokens() int64 {
	value, _ := checkedSum(usage.InputTokens, usage.CacheReadTokens, usage.CacheCreationTokens)
	return value
}

func (usage Usage) CompletionTokens() int64 {
	value, _ := checkedSum(usage.OutputTokens, usage.ReasoningTokens)
	return value
}

func (usage Usage) TotalTokens() int64 {
	value, _ := checkedSum(
		usage.InputTokens, usage.CacheReadTokens, usage.CacheCreationTokens,
		usage.OutputTokens, usage.ReasoningTokens,
	)
	return value
}

func (usage Usage) componentTotalUnsafe() int64 {
	return usage.InputTokens + usage.CacheReadTokens + usage.CacheCreationTokens + usage.OutputTokens + usage.ReasoningTokens
}

func AddUsage(left, right Usage) (Usage, error) {
	if left.Status == UsageUnavailable {
		if err := right.Validate(); err != nil {
			return Usage{}, err
		}
		return right, nil
	}
	if right.Status == UsageUnavailable {
		if err := left.Validate(); err != nil {
			return Usage{}, err
		}
		return left, nil
	}
	if err := left.Validate(); err != nil {
		return Usage{}, err
	}
	if err := right.Validate(); err != nil {
		return Usage{}, err
	}
	values := make([]int64, 5)
	pairs := [][2]int64{
		{left.InputTokens, right.InputTokens},
		{left.CacheReadTokens, right.CacheReadTokens},
		{left.CacheCreationTokens, right.CacheCreationTokens},
		{left.OutputTokens, right.OutputTokens},
		{left.ReasoningTokens, right.ReasoningTokens},
	}
	for index, pair := range pairs {
		value, err := checkedSum(pair[0], pair[1])
		if err != nil {
			return Usage{}, err
		}
		values[index] = value
	}
	status := UsageComplete
	if left.Status == UsagePartial || right.Status == UsagePartial {
		status = UsagePartial
	}
	result := Usage{
		InputTokens: values[0], CacheReadTokens: values[1], CacheCreationTokens: values[2],
		OutputTokens: values[3], ReasoningTokens: values[4], Status: status,
	}
	if err := result.Validate(); err != nil {
		return Usage{}, err
	}
	return result, nil
}

type CostStatus string

const (
	CostExact       CostStatus = "exact"
	CostPartial     CostStatus = "partial"
	CostUnknown     CostStatus = "unknown"
	CostUnavailable CostStatus = "unavailable"
)

// Cost preserves the measured subtotal even when one or more observations are
// unknown. It is diagnostic trace-attributed cost, not a billing ledger.
type Cost struct {
	KnownSubtotalUSD    float64    `json:"knownSubtotalUsd"`
	Status              CostStatus `json:"status"`
	Source              string     `json:"source"`
	KnownObservations   int64      `json:"knownObservations"`
	UnknownObservations int64      `json:"unknownObservations"`
}

func ExactCost(value float64, source string) Cost {
	return Cost{
		KnownSubtotalUSD: roundCost(value), Status: CostExact, Source: strings.TrimSpace(source),
		KnownObservations: 1,
	}
}

func UnknownCost(source string) Cost {
	return Cost{Status: CostUnknown, Source: strings.TrimSpace(source), UnknownObservations: 1}
}

func UnavailableCost() Cost { return Cost{Status: CostUnavailable} }

func (cost Cost) Validate() error {
	if math.IsNaN(cost.KnownSubtotalUSD) || math.IsInf(cost.KnownSubtotalUSD, 0) || cost.KnownSubtotalUSD < 0 {
		return errors.New("accounting: cost subtotal is invalid")
	}
	if cost.KnownObservations < 0 || cost.UnknownObservations < 0 {
		return errors.New("accounting: cost observation count is negative")
	}
	switch cost.Status {
	case CostExact:
		if cost.KnownObservations == 0 || cost.UnknownObservations != 0 {
			return errors.New("accounting: exact cost has invalid coverage")
		}
	case CostPartial:
		if cost.KnownObservations == 0 || cost.UnknownObservations == 0 {
			return errors.New("accounting: partial cost has invalid coverage")
		}
	case CostUnknown:
		if cost.KnownObservations != 0 || cost.UnknownObservations == 0 || cost.KnownSubtotalUSD != 0 {
			return errors.New("accounting: unknown cost has invalid coverage")
		}
	case CostUnavailable:
		if cost.KnownObservations != 0 || cost.UnknownObservations != 0 || cost.KnownSubtotalUSD != 0 {
			return errors.New("accounting: unavailable cost contains observations")
		}
	default:
		return fmt.Errorf("accounting: unsupported cost status %q", cost.Status)
	}
	return nil
}

func AddCost(left, right Cost) (Cost, error) {
	if left.Status == CostUnavailable {
		if err := right.Validate(); err != nil {
			return Cost{}, err
		}
		return right, nil
	}
	if right.Status == CostUnavailable {
		if err := left.Validate(); err != nil {
			return Cost{}, err
		}
		return left, nil
	}
	if err := left.Validate(); err != nil {
		return Cost{}, err
	}
	if err := right.Validate(); err != nil {
		return Cost{}, err
	}
	result := Cost{
		KnownSubtotalUSD:    roundCost(left.KnownSubtotalUSD + right.KnownSubtotalUSD),
		KnownObservations:   left.KnownObservations + right.KnownObservations,
		UnknownObservations: left.UnknownObservations + right.UnknownObservations,
		Source:              mergeSource(left.Source, right.Source),
	}
	switch {
	case result.KnownObservations > 0 && result.UnknownObservations > 0:
		result.Status = CostPartial
	case result.KnownObservations > 0:
		result.Status = CostExact
	default:
		result.Status = CostUnknown
	}
	if err := result.Validate(); err != nil {
		return Cost{}, err
	}
	return result, nil
}

type Ledger struct {
	Usage Usage `json:"usage"`
	Cost  Cost  `json:"cost"`
}

func EmptyLedger() Ledger {
	return Ledger{Usage: UnavailableUsage(), Cost: UnavailableCost()}
}

func AddLedger(left, right Ledger) (Ledger, error) {
	usage, err := AddUsage(left.Usage, right.Usage)
	if err != nil {
		return Ledger{}, err
	}
	cost, err := AddCost(left.Cost, right.Cost)
	if err != nil {
		return Ledger{}, err
	}
	return Ledger{Usage: usage, Cost: cost}, nil
}

type Accounting struct {
	Result   Ledger `json:"result"`
	Provider Ledger `json:"provider"`
}

type Pricing struct {
	Input         *float64 `json:"input,omitempty"`
	CacheRead     *float64 `json:"cacheRead,omitempty"`
	CacheCreation *float64 `json:"cacheCreation,omitempty"`
	Output        *float64 `json:"output,omitempty"`
	Reasoning     *float64 `json:"reasoning,omitempty"`
}

func ResolveCost(usage Usage, pricing Pricing, reported *float64) (Cost, error) {
	if reported != nil {
		cost := ExactCost(*reported, "reported")
		return cost, cost.Validate()
	}
	if err := usage.Validate(); err != nil {
		return Cost{}, err
	}
	if usage.Status == UsageUnavailable {
		return UnavailableCost(), nil
	}
	counts := []int64{usage.InputTokens, usage.CacheReadTokens, usage.CacheCreationTokens, usage.OutputTokens, usage.ReasoningTokens}
	rates := []*float64{pricing.Input, pricing.CacheRead, pricing.CacheCreation, pricing.Output, pricing.Reasoning}
	knownSubtotal := float64(0)
	known, unknown := int64(0), int64(0)
	for index, count := range counts {
		if count == 0 {
			continue
		}
		rate := rates[index]
		if rate == nil || math.IsNaN(*rate) || math.IsInf(*rate, 0) || *rate < 0 {
			unknown++
			continue
		}
		known++
		knownSubtotal = roundCost(knownSubtotal + float64(count)**rate)
	}
	if known == 0 && unknown == 0 {
		if allRatesMissing(rates) {
			return UnknownCost("missing_rate"), nil
		}
		return ExactCost(0, "profile"), nil
	}
	cost := Cost{KnownSubtotalUSD: knownSubtotal, KnownObservations: known, UnknownObservations: unknown, Source: "profile"}
	switch {
	case known > 0 && unknown > 0:
		cost.Status = CostPartial
		cost.KnownObservations = 1
		cost.UnknownObservations = 1
	case known > 0:
		cost.Status = CostExact
		cost.KnownObservations = 1
		cost.UnknownObservations = 0
	default:
		cost.Status = CostUnknown
		cost.Source = "missing_rate"
		cost.KnownObservations = 0
		cost.UnknownObservations = 1
	}
	return cost, cost.Validate()
}

func checkedSum(values ...int64) (int64, error) {
	result := int64(0)
	for _, value := range values {
		if value < 0 {
			return 0, errors.New("accounting: negative value cannot be summed")
		}
		if value > math.MaxInt64-result {
			return 0, errors.New("accounting: integer overflow")
		}
		result += value
	}
	return result, nil
}

func allRatesMissing(rates []*float64) bool {
	for _, rate := range rates {
		if rate != nil {
			return false
		}
	}
	return true
}

func mergeSource(left, right string) string {
	left, right = strings.TrimSpace(left), strings.TrimSpace(right)
	if left == "" {
		return right
	}
	if right == "" || left == right {
		return left
	}
	return "mixed"
}

func roundCost(value float64) float64 {
	return math.Round(value*costPrecision) / costPrecision
}
