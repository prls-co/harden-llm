// Package pricing preserves the source-parity API while delegating all token,
// rate, and cost semantics to accounting.
package pricing

import (
	"github.com/prls-co/harden-llm/internal/accounting"
	"github.com/prls-co/harden-llm/internal/runtime"
)

const (
	ItemInput         = accounting.ItemInput
	ItemCacheRead     = accounting.ItemCacheRead
	ItemCacheCreation = accounting.ItemCacheCreation
	ItemOutput        = accounting.ItemOutput
	ItemReasoning     = accounting.ItemReasoning
)

type Item = accounting.Item
type Usage = accounting.PricedUsage
type SummaryItem = accounting.SummaryItem
type GroupSummary = accounting.GroupSummary
type Summary = accounting.Summary
type Cost = accounting.Cost

func Normalize(usage Usage) (Usage, error) { return accounting.NormalizePricedUsage(usage) }
func Zero() Usage                          { return accounting.ZeroPricedUsage() }
func Summarize(usage Usage) (Summary, error) {
	return accounting.SummarizePricedUsage(usage)
}
func ResolveCost(usage Usage, reported *float64) (Cost, error) {
	return accounting.ResolvePricedCost(usage, reported)
}
func Add(left, right Usage) (Usage, error) { return accounting.AddPricedUsage(left, right) }
func FromRuntime(usage runtime.Usage, pricing runtime.Pricing) Usage {
	return accounting.PricedUsageFrom(usage, pricing)
}
