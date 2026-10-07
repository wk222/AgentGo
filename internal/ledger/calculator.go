package ledger

import (
	"strings"
)

// ModelPricing defines per-million token costs in USD.
type ModelPricing struct {
	PromptPerMUSD      float64
	CompletionPerMUSD  float64
	CacheReadPerMUSD   float64
	CacheWritePerMUSD  float64
	ReasoningMultiplier float64
}

// USDToCNYRate is the default conversion rate.
const USDToCNYRate = 7.25

var defaultPricings = map[string]ModelPricing{
	// DeepSeek V3 / R1 (Very cost-effective)
	"deepseek-chat": {
		PromptPerMUSD:     0.14,
		CompletionPerMUSD: 0.28,
		CacheReadPerMUSD:  0.014,
		CacheWritePerMUSD: 0.14,
	},
	"deepseek-reasoner": {
		PromptPerMUSD:       0.55,
		CompletionPerMUSD:   2.19,
		CacheReadPerMUSD:    0.14,
		CacheWritePerMUSD:   0.55,
		ReasoningMultiplier: 1.0,
	},
	// OpenAI GPT-4o
	"gpt-4o": {
		PromptPerMUSD:     2.50,
		CompletionPerMUSD: 10.00,
		CacheReadPerMUSD:  1.25,
		CacheWritePerMUSD: 2.50,
	},
	"gpt-4o-mini": {
		PromptPerMUSD:     0.15,
		CompletionPerMUSD: 0.60,
		CacheReadPerMUSD:  0.075,
		CacheWritePerMUSD: 0.15,
	},
	// Claude 3.5 Sonnet / 3.7 Sonnet
	"claude-3-5-sonnet": {
		PromptPerMUSD:     3.00,
		CompletionPerMUSD: 15.00,
		CacheReadPerMUSD:  0.30,
		CacheWritePerMUSD: 3.75,
	},
	"claude-3-7-sonnet": {
		PromptPerMUSD:     3.00,
		CompletionPerMUSD: 15.00,
		CacheReadPerMUSD:  0.30,
		CacheWritePerMUSD: 3.75,
	},
}

// CalculateCost computes total USD and CNY costs based on model and token counts.
func CalculateCost(model string, promptTokens, completionTokens, reasoningTokens, cacheReadTokens, cacheWriteTokens int) (costUSD float64, costCNY float64) {
	normModel := strings.ToLower(strings.TrimSpace(model))
	pricing, ok := defaultPricings[normModel]
	if !ok {
		// Fuzzy match
		for k, p := range defaultPricings {
			if strings.Contains(normModel, k) {
				pricing = p
				ok = true
				break
			}
		}
	}
	if !ok {
		// Default fallback to competitive pricing
		pricing = defaultPricings["deepseek-chat"]
	}

	effectivePromptTokens := promptTokens - cacheReadTokens
	if effectivePromptTokens < 0 {
		effectivePromptTokens = 0
	}

	costUSD += (float64(effectivePromptTokens) / 1_000_000.0) * pricing.PromptPerMUSD
	costUSD += (float64(cacheReadTokens) / 1_000_000.0) * pricing.CacheReadPerMUSD
	costUSD += (float64(cacheWriteTokens) / 1_000_000.0) * pricing.CacheWritePerMUSD
	costUSD += (float64(completionTokens) / 1_000_000.0) * pricing.CompletionPerMUSD

	costCNY = costUSD * USDToCNYRate
	return costUSD, costCNY
}
