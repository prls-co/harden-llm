// Package stats owns the strict numeric projection from one canonical trace
// into aggregate LLM totals.
package stats

import (
	"errors"
	"fmt"
	"math"

	"github.com/prls-co/harden-llm/internal/pricing"
)

const statsCostPrecision = 1_000_000_000_000

type Totals struct {
	CachedCost          float64 `json:"cachedCost"`
	CachedCount         int64   `json:"cachedCount"`
	FailureCount        int64   `json:"failureCount"`
	MaxCallDurationMs   int64   `json:"maxCallDurationMs"`
	MaxOverBudgetMs     int64   `json:"maxOverBudgetMs"`
	OverBudgetCount     int64   `json:"overBudgetCount"`
	SuccessCount        int64   `json:"successCount"`
	TimeoutCount        int64   `json:"timeoutCount"`
	TotalCallDurationMs int64   `json:"totalCallDurationMs"`
	TotalCost           float64 `json:"totalCost"`
	TotalCount          int64   `json:"totalCount"`
	TotalOutputTokens   int64   `json:"totalOutputTokens"`
	TotalPromptTokens   int64   `json:"totalPromptTokens"`
}

type TraceCost struct {
	Cached      float64 `json:"cached"`
	CachedKnown bool    `json:"cachedKnown"`
	Known       bool    `json:"known"`
	Source      string  `json:"source"`
	Total       float64 `json:"total"`
}

type Trace struct {
	LastErrorCategory   *string       `json:"lastErrorCategory"`
	OverBudgetMs        int64         `json:"overBudgetMs"`
	TotalCallDurationMs int64         `json:"totalCallDurationMs"`
	TotalWaitMs         int64         `json:"totalWaitMs"`
	Usage               pricing.Usage `json:"usage"`
	FreshUsage          pricing.Usage `json:"freshUsage"`
	SavedUsage          pricing.Usage `json:"savedUsage"`
	ServedFromCache     bool          `json:"servedFromCache"`
	ProviderInvoked     *bool         `json:"providerInvoked"`
	Cost                TraceCost     `json:"cost"`
}

type ContractError struct {
	Code    string
	Message string
	Context map[string]string
}

func (contractError *ContractError) Error() string {
	if contractError == nil {
		return "stats projection contract error"
	}
	return contractError.Message
}

func Merge(current Totals, trace Trace, context map[string]string) (Totals, error) {
	if err := validateTotals(current); err != nil {
		return Totals{}, err
	}
	usage := trace.Usage
	if trace.ServedFromCache {
		usage = trace.SavedUsage
	}
	if usage.Items == nil {
		return Totals{}, contractFailure(
			"missing_trace_usage_items", "LLM trace must include canonical usage.items.", context,
		)
	}
	summary, err := pricing.Summarize(usage)
	if err != nil {
		return Totals{}, contractFailure("invalid_trace_usage", "LLM trace usage is invalid.", context)
	}
	cost, known := summary.TotalCost, summary.CostKnown
	if trace.Cost.Known {
		cost, known = floatPointer(trace.Cost.Total), true
	}
	if !known || cost == nil {
		return Totals{}, contractFailure(
			"unknown_trace_usage_cost",
			"LLM trace usage must include ratePerToken values for numeric llmStats.totalCost projection.",
			context,
		)
	}
	input := usage.Items[pricing.ItemInput].Tokens + usage.Items[pricing.ItemCacheRead].Tokens + usage.Items[pricing.ItemCacheCreation].Tokens
	output := usage.Items[pricing.ItemOutput].Tokens + usage.Items[pricing.ItemReasoning].Tokens
	duration := trace.TotalCallDurationMs
	if duration == 0 {
		duration = trace.TotalWaitMs
	}
	if duration < 0 || trace.OverBudgetMs < 0 {
		return Totals{}, contractFailure("invalid_trace_duration", "LLM trace duration fields must be non-negative.", context)
	}

	result := current
	result.TotalCount++
	result.TotalCallDurationMs += duration
	result.MaxCallDurationMs = max(result.MaxCallDurationMs, duration)
	result.TotalPromptTokens += input
	result.TotalOutputTokens += output
	result.TotalCost = roundStatsCost(result.TotalCost + *cost)
	if trace.OverBudgetMs > 0 {
		result.OverBudgetCount++
		result.MaxOverBudgetMs = max(result.MaxOverBudgetMs, trace.OverBudgetMs)
	}
	category := ""
	if trace.LastErrorCategory != nil {
		category = *trace.LastErrorCategory
	}
	switch category {
	case "":
		result.SuccessCount++
	case "timeout":
		result.TimeoutCount++
	default:
		result.FailureCount++
	}
	if trace.ServedFromCache {
		result.CachedCount++
		cachedCost := *cost
		if trace.Cost.CachedKnown {
			cachedCost = trace.Cost.Cached
		}
		if math.IsNaN(cachedCost) || math.IsInf(cachedCost, 0) || cachedCost < 0 {
			return Totals{}, contractFailure("invalid_cached_cost", "LLM trace cached cost is invalid.", context)
		}
		result.CachedCost = roundStatsCost(result.CachedCost + cachedCost)
	}
	return result, nil
}

func validateTotals(totals Totals) error {
	counts := []int64{
		totals.CachedCount, totals.FailureCount, totals.MaxCallDurationMs, totals.MaxOverBudgetMs,
		totals.OverBudgetCount, totals.SuccessCount, totals.TimeoutCount, totals.TotalCallDurationMs,
		totals.TotalCount, totals.TotalOutputTokens, totals.TotalPromptTokens,
	}
	for _, value := range counts {
		if value < 0 {
			return errors.New("stats: current totals contain a negative value")
		}
	}
	for _, value := range []float64{totals.CachedCost, totals.TotalCost} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return errors.New("stats: current totals contain an invalid cost")
		}
	}
	if totals.SuccessCount+totals.FailureCount+totals.TimeoutCount > totals.TotalCount {
		return fmt.Errorf("stats: outcome counts exceed totalCount")
	}
	return nil
}

func contractFailure(code, message string, context map[string]string) *ContractError {
	cloned := make(map[string]string, len(context))
	for key, value := range context {
		cloned[key] = value
	}
	return &ContractError{Code: code, Message: message, Context: cloned}
}

func roundStatsCost(value float64) float64 {
	return math.Round(value*statsCostPrecision) / statsCostPrecision
}

func floatPointer(value float64) *float64 {
	return &value
}
