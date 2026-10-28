package llm

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"strings"
)

// ModelPrice is the USD price per million tokens.
type ModelPrice struct {
	Input       float64 `json:"input"`
	CachedInput float64 `json:"cached_input"`
	// CacheWriteInput is the price of input tokens written to the prompt cache
	// (Anthropic: 1.25× Input for the 5-minute TTL). 0 means Input.
	CacheWriteInput float64 `json:"cache_write_input,omitempty"`
	Output          float64 `json:"output"`
}

// BatchDiscount is the factor applied to every token of a Message Batches request
// (Anthropic bills batch usage at 50% of the list price, including cache reads/writes).
const BatchDiscount = 0.5

// PriceTable maps model name → price. Used to estimate the cost shown in run logs.
type PriceTable struct {
	prices map[string]ModelPrice
}

// defaultPrices are the values from https://developers.openai.com/api/docs/pricing
// (checked as of 2026-08-13) and the Anthropic pricing page (claude-* entries). Since prices rise
// and new models appear, they can be overridden with PRICING_FILE before an experiment.
var defaultPrices = map[string]ModelPrice{
	"gpt-5.6-sol":            {Input: 5.00, CachedInput: 0.50, Output: 30.00},
	"gpt-5.6-terra":          {Input: 2.00, CachedInput: 0.20, Output: 12.00},
	"gpt-5.6-luna":           {Input: 0.20, CachedInput: 0.02, Output: 1.20},
	"gpt-5.5":                {Input: 5.00, CachedInput: 0.50, Output: 30.00},
	"gpt-5.4":                {Input: 2.50, CachedInput: 0.25, Output: 15.00},
	"gpt-5.2":                {Input: 1.75, CachedInput: 0.175, Output: 14.00},
	"gpt-5.1":                {Input: 1.25, CachedInput: 0.125, Output: 10.00},
	"gpt-5":                  {Input: 1.25, CachedInput: 0.125, Output: 10.00},
	"gpt-5-mini":             {Input: 0.25, CachedInput: 0.025, Output: 2.00},
	"gpt-5-nano":             {Input: 0.05, CachedInput: 0.005, Output: 0.40},
	"gpt-4.1":                {Input: 2.00, CachedInput: 0.50, Output: 8.00},
	"gpt-4.1-mini":           {Input: 0.40, CachedInput: 0.10, Output: 1.60},
	"gpt-4o":                 {Input: 2.50, CachedInput: 1.25, Output: 10.00},
	"gpt-4o-mini":            {Input: 0.15, CachedInput: 0.075, Output: 0.60},
	"text-embedding-3-small": {Input: 0.02},
	"text-embedding-3-large": {Input: 0.13},
	// Anthropic list prices (Sonnet 5.5 / Haiku 5.5 verified 2026-10-28; Opus 5.5 from the
	// claude-api reference cached 2026-10-06). Cache reads are 10% of input (Opus 5.5: $0.20 = 5%),
	// 5-minute cache writes 1.25×. Message Batches halve everything (ChatCostBatch).
	// Haiku 5.5 is priced for prompts ≤ 100K tokens ($0.50 / $2.50 beyond); the judge prompts
	// are far below that, so the long-context tier is not modeled.
	"claude-opus-5-5":   {Input: 4.00, CachedInput: 0.20, CacheWriteInput: 5.00, Output: 20.00},
	"claude-sonnet-5-5": {Input: 2.00, CachedInput: 0.20, CacheWriteInput: 2.50, Output: 10.00},
	"claude-haiku-5-5":  {Input: 0.10, CachedInput: 0.01, CacheWriteInput: 0.125, Output: 0.50},
}

// defaultEmbeddingDims are the default embedding dimensions (used when EMBEDDING_DIM=0).
var defaultEmbeddingDims = map[string]int{
	"text-embedding-3-small": 1536,
	"text-embedding-3-large": 3072,
}

func DefaultPricing() *PriceTable {
	return &PriceTable{prices: maps.Clone(defaultPrices)}
}

// LoadPricing overrides the default table with a JSON file ({"model": {"input":..,"cached_input":..,"output":..}}).
// If path is empty, the default table is returned as-is.
func LoadPricing(path string) (*PriceTable, error) {
	t := DefaultPricing()
	if path == "" {
		return t, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("llm: read pricing file %s: %w", path, err)
	}
	var overrides map[string]ModelPrice
	if err := json.Unmarshal(b, &overrides); err != nil {
		return nil, fmt.Errorf("llm: parse pricing file %s: %w", path, err)
	}
	maps.Copy(t.prices, overrides)
	return t, nil
}

// Price returns the model's price. Model names with a date suffix returned by the API,
// such as "gpt-5-mini-2026-01-01", are mapped to the base model by longest prefix match.
func (t *PriceTable) Price(model string) (ModelPrice, bool) {
	if p, ok := t.prices[model]; ok {
		return p, true
	}
	var (
		best    ModelPrice
		bestLen int
	)
	for name, p := range t.prices {
		if strings.HasPrefix(model, name) && len(name) > bestLen {
			best, bestLen = p, len(name)
		}
	}
	return best, bestLen > 0
}

// ChatCost computes the estimated cost (USD) from usage. Returns 0 if the price is unknown.
//
// u.InputTokens is the total prompt size, including cache reads and cache writes
// (OpenAI semantics; the Anthropic client sums its three input counters to match).
func (t *PriceTable) ChatCost(model string, u Usage) float64 {
	p, ok := t.Price(model)
	if !ok {
		return 0
	}
	const perToken = 1_000_000.0
	fresh := max(u.InputTokens-u.CachedInputTokens-u.CacheWriteInputTokens, 0)
	cachedRate := p.CachedInput
	if cachedRate == 0 {
		cachedRate = p.Input
	}
	writeRate := p.CacheWriteInput
	if writeRate == 0 {
		writeRate = p.Input
	}
	return (float64(fresh)*p.Input +
		float64(u.CachedInputTokens)*cachedRate +
		float64(u.CacheWriteInputTokens)*writeRate +
		float64(u.OutputTokens)*p.Output) / perToken
}

// ChatCostBatch is ChatCost for a Message Batches request (BatchDiscount applied).
func (t *PriceTable) ChatCostBatch(model string, u Usage) float64 {
	return t.ChatCost(model, u) * BatchDiscount
}
