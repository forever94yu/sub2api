package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// Missing exact Image 2.5 cards must not silently use the older image-family
// fallback: that would hide future price changes and advertise a text output fee.
func TestImage25PricingCatalogUsesExactImageRates(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)
	pricingSvc := &PricingService{}
	pricingSvc.pricingData, err = pricingSvc.parsePricingData(body)
	require.NoError(t, err)
	billingSvc := NewBillingService(&config.Config{}, pricingSvc)
	for _, model := range []string{
		"gpt-image-2.5-sunburst", "gpt-image-2.5-sunburst-2026-09-08",
		"gpt-image-2.5-flare", "gpt-image-2.5-flare-2026-09-08",
	} {
		t.Run(model, func(t *testing.T) {
			require.True(t, billingSvc.HasIdentifiedTokenPricing(model), "must resolve a verified model card, not the image-family heuristic")
			cost, err := billingSvc.CalculateCost(model, UsageTokens{
				InputTokens: 1500, ImageInputTokens: 1000,
				OutputTokens: 800, ImageOutputTokens: 800, CacheReadTokens: 200,
			}, 2)
			require.NoError(t, err)
			require.InDelta(t, 0.0025, cost.InputCost, 1e-12)
			require.InDelta(t, 0.008, cost.ImageInputCost, 1e-12)
			require.InDelta(t, 0.024, cost.ImageOutputCost, 1e-12)
			require.InDelta(t, 0.00025, cost.CacheReadCost, 1e-12)
			require.Zero(t, cost.OutputCost)
			require.InDelta(t, 0.03475, cost.TotalCost, 1e-12)
			require.InDelta(t, 0.0695, cost.ActualCost, 1e-12)
			pricing, err := billingSvc.GetModelPricing(model)
			require.NoError(t, err)
			require.Zero(t, pricing.OutputPricePerToken, "image models do not produce billed text output")
		})
	}
}

func TestImage25PricingFallbackMergePreservesExistingRates(t *testing.T) {
	pricingSvc := &PricingService{cfg: &config.Config{Pricing: config.PricingConfig{
		FallbackFile: filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"),
	}}}
	customOld := &LiteLLMModelPricing{InputCostPerToken: 0.123, OutputCostPerImageToken: 0.456}
	customNew := &LiteLLMModelPricing{InputCostPerToken: 0.234, OutputCostPerImageToken: 0.567}
	pricingSvc.pricingData = pricingSvc.mergeFallbackPricingData(map[string]*LiteLLMModelPricing{
		"gpt-image-2": customOld, "gpt-image-2.5-flare": customNew,
	})
	require.Same(t, customOld, pricingSvc.GetIdentifiedModelPricing("gpt-image-2"))
	require.Same(t, customNew, pricingSvc.GetIdentifiedModelPricing("gpt-image-2.5-flare"))
	pricing := pricingSvc.GetIdentifiedModelPricing("gpt-image-2.5-sunburst")
	require.NotNil(t, pricing, "new bundled models must survive a remote catalog without Image 2.5")
	require.InDelta(t, 5e-6, pricing.InputCostPerToken, 1e-12)
	require.InDelta(t, 3e-5, pricing.OutputCostPerImageToken, 1e-12)
}
