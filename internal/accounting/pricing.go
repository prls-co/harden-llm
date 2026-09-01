package accounting

import (
	"errors"
	"fmt"
	"math"
)

const (
	ItemInput         = "input"
	ItemCacheRead     = "cacheReadInput"
	ItemCacheCreation = "cacheCreationInput"
	ItemOutput        = "output"
	ItemReasoning     = "reasoningOutput"
)

var itemOrder = []string{ItemInput, ItemCacheRead, ItemCacheCreation, ItemOutput, ItemReasoning}

var displayGroup = map[string]string{
	ItemInput: "input", ItemCacheRead: "cache", ItemCacheCreation: "cache",
	ItemOutput: "output", ItemReasoning: "output",
}

// Item and PricedUsage retain the source parity fixture shape. Runtime code
// uses the flat canonical Usage and converts here only at the fixture boundary.
type Item struct {
	Tokens       int64    `json:"tokens"`
	RatePerToken *float64 `json:"ratePerToken"`
}

type PricedUsage struct {
	Items map[string]Item `json:"items"`
}

type SummaryItem struct {
	Tokens       int64    `json:"tokens"`
	RatePerToken *float64 `json:"ratePerToken"`
	Cost         *float64 `json:"cost"`
	CostKnown    bool     `json:"costKnown"`
	DisplayGroup string   `json:"displayGroup"`
}

type GroupSummary struct {
	Tokens    int64    `json:"tokens"`
	Cost      *float64 `json:"cost"`
	CostKnown bool     `json:"costKnown"`
}

type Summary struct {
	Items       map[string]SummaryItem  `json:"items"`
	Groups      map[string]GroupSummary `json:"groups"`
	TotalTokens int64                   `json:"totalTokens"`
	TotalCost   *float64                `json:"totalCost"`
	CostKnown   bool                    `json:"costKnown"`
}

func NormalizePricedUsage(usage PricedUsage) (PricedUsage, error) {
	if usage.Items == nil {
		return PricedUsage{}, errors.New("accounting: priced usage.items is required")
	}
	for itemType := range usage.Items {
		if _, ok := displayGroup[itemType]; !ok {
			return PricedUsage{}, fmt.Errorf("accounting: unsupported usage item type %q", itemType)
		}
	}
	result := PricedUsage{Items: make(map[string]Item, len(itemOrder))}
	for _, itemType := range itemOrder {
		item := usage.Items[itemType]
		if item.Tokens < 0 {
			return PricedUsage{}, fmt.Errorf("accounting: usage item %q has negative tokens", itemType)
		}
		if item.RatePerToken != nil {
			rate := *item.RatePerToken
			if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
				return PricedUsage{}, fmt.Errorf("accounting: usage item %q has an invalid rate", itemType)
			}
			item.RatePerToken = floatPointer(rate)
		}
		result.Items[itemType] = item
	}
	return result, nil
}

func ZeroPricedUsage() PricedUsage {
	items := make(map[string]Item, len(itemOrder))
	for _, itemType := range itemOrder {
		items[itemType] = Item{}
	}
	return PricedUsage{Items: items}
}

func SummarizePricedUsage(usage PricedUsage) (Summary, error) {
	normalized, err := NormalizePricedUsage(usage)
	if err != nil {
		return Summary{}, err
	}
	groups := map[string]GroupSummary{
		"input":  {Cost: floatPointer(0), CostKnown: true},
		"cache":  {Cost: floatPointer(0), CostKnown: true},
		"output": {Cost: floatPointer(0), CostKnown: true},
	}
	result := Summary{Items: make(map[string]SummaryItem, len(itemOrder)), Groups: groups, CostKnown: true}
	totalCost := float64(0)
	for _, itemType := range itemOrder {
		item := normalized.Items[itemType]
		known := item.Tokens == 0 || item.RatePerToken != nil
		var cost *float64
		if known {
			value := float64(0)
			if item.RatePerToken != nil {
				value = roundCost(float64(item.Tokens) * *item.RatePerToken)
			}
			cost = floatPointer(value)
			totalCost = roundCost(totalCost + value)
		}
		result.Items[itemType] = SummaryItem{
			Tokens: item.Tokens, RatePerToken: item.RatePerToken, Cost: cost,
			CostKnown: known, DisplayGroup: displayGroup[itemType],
		}
		result.TotalTokens += item.Tokens
		group := result.Groups[displayGroup[itemType]]
		group.Tokens += item.Tokens
		if known && group.CostKnown {
			group.Cost = floatPointer(roundCost(*group.Cost + *cost))
		} else if !known {
			group.Cost = nil
			group.CostKnown = false
			result.CostKnown = false
		}
		result.Groups[displayGroup[itemType]] = group
	}
	if result.CostKnown {
		result.TotalCost = floatPointer(totalCost)
	}
	return result, nil
}

func ResolvePricedCost(usage PricedUsage, reported *float64) (Cost, error) {
	if reported != nil {
		cost := ExactCost(*reported, "reported")
		return cost, cost.Validate()
	}
	summary, err := SummarizePricedUsage(usage)
	if err != nil {
		return Cost{}, err
	}
	if !summary.CostKnown || summary.TotalCost == nil {
		return UnknownCost("missing_rate"), nil
	}
	return ExactCost(*summary.TotalCost, "calculated"), nil
}

func AddPricedUsage(left, right PricedUsage) (PricedUsage, error) {
	left, err := NormalizePricedUsage(left)
	if err != nil {
		return PricedUsage{}, err
	}
	right, err = NormalizePricedUsage(right)
	if err != nil {
		return PricedUsage{}, err
	}
	result := ZeroPricedUsage()
	for _, itemType := range itemOrder {
		leftItem, rightItem := left.Items[itemType], right.Items[itemType]
		rate, err := mergeRate(itemType, leftItem.RatePerToken, rightItem.RatePerToken)
		if err != nil {
			return PricedUsage{}, err
		}
		tokens, err := checkedSum(leftItem.Tokens, rightItem.Tokens)
		if err != nil {
			return PricedUsage{}, err
		}
		result.Items[itemType] = Item{Tokens: tokens, RatePerToken: rate}
	}
	return result, nil
}

func PricedUsageFrom(usage Usage, pricing Pricing) PricedUsage {
	return PricedUsage{Items: map[string]Item{
		ItemInput:         {Tokens: usage.InputTokens, RatePerToken: cloneRate(pricing.Input)},
		ItemCacheRead:     {Tokens: usage.CacheReadTokens, RatePerToken: cloneRate(pricing.CacheRead)},
		ItemCacheCreation: {Tokens: usage.CacheCreationTokens, RatePerToken: cloneRate(pricing.CacheCreation)},
		ItemOutput:        {Tokens: usage.OutputTokens, RatePerToken: cloneRate(pricing.Output)},
		ItemReasoning:     {Tokens: usage.ReasoningTokens, RatePerToken: cloneRate(pricing.Reasoning)},
	}}
}

func mergeRate(itemType string, left, right *float64) (*float64, error) {
	if left == nil && right == nil {
		return nil, nil
	}
	if left == nil {
		return floatPointer(*right), nil
	}
	if right == nil {
		return floatPointer(*left), nil
	}
	if *left != *right {
		return nil, fmt.Errorf("accounting: conflicting rate for usage item %q", itemType)
	}
	return floatPointer(*left), nil
}

func cloneRate(value *float64) *float64 {
	if value == nil {
		return nil
	}
	return floatPointer(*value)
}

func floatPointer(value float64) *float64 { return &value }
