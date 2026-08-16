// Package pricing owns canonical token groups, versioned rates, and exact cost
// summaries shared by providers, traces, and statistics.
package pricing

import (
	"errors"
	"fmt"
	"math"

	"github.com/prls-co/harden-llm/internal/runtime"
)

const costPrecision = 1_000_000_000_000

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

type Item struct {
	Tokens       int64    `json:"tokens"`
	RatePerToken *float64 `json:"ratePerToken"`
}

type Usage struct {
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

type Cost struct {
	TotalUSD float64 `json:"totalUsd"`
	Known    bool    `json:"known"`
	Source   string  `json:"source"`
}

func Normalize(usage Usage) (Usage, error) {
	if usage.Items == nil {
		return Usage{}, errors.New("pricing: usage.items is required")
	}
	for itemType := range usage.Items {
		if _, ok := displayGroup[itemType]; !ok {
			return Usage{}, fmt.Errorf("pricing: unsupported usage item type %q", itemType)
		}
	}
	result := Usage{Items: make(map[string]Item, len(itemOrder))}
	for _, itemType := range itemOrder {
		item := usage.Items[itemType]
		if item.Tokens < 0 {
			return Usage{}, fmt.Errorf("pricing: usage item %q has negative tokens", itemType)
		}
		if item.RatePerToken != nil {
			rate := *item.RatePerToken
			if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
				return Usage{}, fmt.Errorf("pricing: usage item %q has an invalid rate", itemType)
			}
			item.RatePerToken = floatPointer(rate)
		}
		result.Items[itemType] = item
	}
	return result, nil
}

func Zero() Usage {
	items := make(map[string]Item, len(itemOrder))
	for _, itemType := range itemOrder {
		items[itemType] = Item{}
	}
	return Usage{Items: items}
}

func Summarize(usage Usage) (Summary, error) {
	normalized, err := Normalize(usage)
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
			value := *group.Cost + *cost
			group.Cost = floatPointer(roundCost(value))
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

func ResolveCost(usage Usage, reported *float64) (Cost, error) {
	if reported != nil {
		if math.IsNaN(*reported) || math.IsInf(*reported, 0) || *reported < 0 {
			return Cost{}, errors.New("pricing: reported cost is invalid")
		}
		return Cost{TotalUSD: roundCost(*reported), Known: true, Source: "reported"}, nil
	}
	summary, err := Summarize(usage)
	if err != nil {
		return Cost{}, err
	}
	if !summary.CostKnown || summary.TotalCost == nil {
		return Cost{Known: false, Source: "unknown"}, nil
	}
	return Cost{TotalUSD: *summary.TotalCost, Known: true, Source: "calculated"}, nil
}

func Add(left, right Usage) (Usage, error) {
	left, err := Normalize(left)
	if err != nil {
		return Usage{}, err
	}
	right, err = Normalize(right)
	if err != nil {
		return Usage{}, err
	}
	result := Zero()
	for _, itemType := range itemOrder {
		leftItem, rightItem := left.Items[itemType], right.Items[itemType]
		rate, err := mergeRate(itemType, leftItem.RatePerToken, rightItem.RatePerToken)
		if err != nil {
			return Usage{}, err
		}
		result.Items[itemType] = Item{Tokens: leftItem.Tokens + rightItem.Tokens, RatePerToken: rate}
	}
	return result, nil
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
		return nil, fmt.Errorf("pricing: conflicting rate for usage item %q", itemType)
	}
	return floatPointer(*left), nil
}

func FromRuntime(usage runtime.Usage, pricing runtime.Pricing) Usage {
	return Usage{Items: map[string]Item{
		ItemInput:         {Tokens: usage.InputTokens, RatePerToken: cloneRate(pricing.Input)},
		ItemCacheRead:     {Tokens: usage.CacheReadTokens, RatePerToken: cloneRate(pricing.CacheRead)},
		ItemCacheCreation: {Tokens: usage.CacheCreationTokens, RatePerToken: cloneRate(pricing.CacheCreation)},
		ItemOutput:        {Tokens: usage.OutputTokens, RatePerToken: cloneRate(pricing.Output)},
		ItemReasoning:     {Tokens: usage.ReasoningTokens, RatePerToken: cloneRate(pricing.Reasoning)},
	}}
}

func cloneRate(value *float64) *float64 {
	if value == nil {
		return nil
	}
	return floatPointer(*value)
}

func roundCost(value float64) float64 {
	return math.Round(value*costPrecision) / costPrecision
}

func floatPointer(value float64) *float64 {
	return &value
}
