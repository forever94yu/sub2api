package service

import "time"

// These cards are independent of earlier GPT and image model prices.
var openAIGPT62FallbackPricing = map[string]*LiteLLMModelPricing{
	"gpt-6.2-astra": {
		InputCostPerToken: 8e-6, InputCostPerTokenPriority: 16e-6,
		OutputCostPerToken: 40e-6, OutputCostPerTokenPriority: 80e-6,
		CacheReadInputTokenCost: .4e-6, CacheReadInputTokenCostPriority: .8e-6,
		CacheCreationInputTokenCost: 10e-6, CacheCreationInputTokenCostPriority: 20e-6,
		CacheCreationInputTokenCost12h: 20e-6,
		LongContextInputTokenThreshold: 272000, LongContextInputCostMultiplier: 2, LongContextOutputCostMultiplier: 1.5,
		SupportsServiceTier: true, SupportsPromptCaching: true, LiteLLMProvider: "openai", Mode: "chat",
	},
	"gpt-6.2-sol": {
		InputCostPerToken: 2e-6, InputCostPerTokenPriority: 4e-6,
		OutputCostPerToken: 10e-6, OutputCostPerTokenPriority: 20e-6,
		CacheReadInputTokenCost: .1e-6, CacheReadInputTokenCostPriority: .2e-6,
		CacheCreationInputTokenCost: 2.5e-6, CacheCreationInputTokenCostPriority: 5e-6,
		CacheCreationInputTokenCost12h: 5e-6,
		LongContextInputTokenThreshold: 272000, LongContextInputCostMultiplier: 2, LongContextOutputCostMultiplier: 1.5,
		SupportsServiceTier: true, SupportsPromptCaching: true, LiteLLMProvider: "openai", Mode: "chat",
	},
}

var openAIImage26FallbackPricing = &LiteLLMModelPricing{
	InputCostPerToken: 5e-6, InputCostPerImageToken: 8e-6,
	CacheReadInputTokenCost: 1.25e-6, OutputCostPerImageToken: 30e-6,
	LiteLLMProvider: "openai", Mode: "image_generation",
}

func isOpenAIImage26Model(model string) bool {
	switch normalizeModelNameForPricing(lastOpenAIModelSegment(model)) {
	case "gpt-image-2.6-sunburst", "gpt-image-2.6-flare":
		return true
	default:
		return false
	}
}

func openAIGPT62TierPricing(model, tier string, at time.Time) (string, float64) {
	if !isOpenAIGPT62Model(model) {
		return tier, 1
	}
	switch normalizeBillingServiceTier(tier) {
	case "fast":
		return "priority", 1
	case "flex":
		// No Flex rate is published for GPT-6.2. Never infer the older discount.
		return "default", 1
	case "ultrafast":
		if at.IsZero() {
			at = time.Now()
		}
		// March 1, 2027 midnight in Pacific time is 08:00 UTC (before DST).
		if at.Before(time.Date(2027, 3, 1, 8, 0, 0, 0, time.UTC)) {
			return "default", 3
		}
		return "default", 6
	default:
		return tier, 1
	}
}
