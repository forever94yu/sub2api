package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestClaudeSonnet55PricingAliasesAndVersionIsolation(t *testing.T) {
	for _, dynamic := range []*PricingService{nil, newClaudeCatalogPricingService(t)} {
		svc := NewBillingService(&config.Config{}, dynamic)
		for _, model := range []string{
			"claude-sonnet-5-5", "anthropic.claude-sonnet-5-5",
			"projects/p/locations/global/publishers/anthropic/models/claude-sonnet-5-5",
			"claude-sonnet-5.5", "anthropic/claude-sonnet-5-5", "models/claude-sonnet-5-5",
		} {
			pricing, err := svc.GetModelPricing(model)
			require.NoError(t, err, model)
			require.Equal(t, 2e-6, pricing.InputPricePerToken, model)
			require.Equal(t, 10e-6, pricing.OutputPricePerToken, model)
			require.Equal(t, 2.5e-6, pricing.CacheCreation5mPrice, model)
			require.Equal(t, 4e-6, pricing.CacheCreation1hPrice, model)
			require.InDelta(t, 0.2e-6, pricing.CacheReadPricePerToken, 1e-18, model)
			require.True(t, svc.HasIdentifiedTokenPricing(model), model)
		}
		for _, model := range []string{"claude-sonnet-5-6", "claude-sonnet-55", "claude-sonnet-5-50", "claude-sonnet-5-5-preview"} {
			pricing, err := svc.GetModelPricing(model)
			require.ErrorIs(t, err, ErrModelPricingUnavailable, model)
			require.Nil(t, pricing, model)
			require.False(t, svc.HasIdentifiedTokenPricing(model), model)
		}
	}
	for _, configured := range []string{"claude-sonnet-5", "claude-sonnet-5-5"} {
		dynamic := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
			configured: {InputCostPerToken: 9e-6, OutputCostPerToken: 18e-6},
		}}
		svc := NewBillingService(&config.Config{}, dynamic)
		for _, model := range []string{"claude-sonnet-5", "claude-sonnet-5-5"} {
			pricing, err := svc.GetModelPricing(model)
			require.NoError(t, err)
			want := 2e-6
			if model == configured {
				want = 9e-6
			}
			require.Equal(t, want, pricing.InputPricePerToken, "configured=%s requested=%s", configured, model)
		}
	}
}

func TestClaudeSonnet55PricingLoadsMissingPersistedEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old-pricing.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"claude-sonnet-5":{"input_cost_per_token":0.000009,"output_cost_per_token":0.000018}}`), 0600))
	cfg := &config.Config{}
	cfg.Pricing.FallbackFile = filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json")
	dynamic := &PricingService{cfg: cfg}
	require.NoError(t, dynamic.loadPricingData(path))
	pricing := dynamic.GetIdentifiedModelPricing("claude-sonnet-5-5")
	require.NotNil(t, pricing, "old persisted snapshots must merge the new bundled model")
	require.Equal(t, 2e-6, pricing.InputCostPerToken)
	require.Equal(t, 9e-6, dynamic.GetIdentifiedModelPricing("claude-sonnet-5").InputCostPerToken)

	explicit := &LiteLLMModelPricing{InputCostPerToken: 7e-6, OutputCostPerToken: 14e-6}
	merged := dynamic.mergeFallbackPricingData(map[string]*LiteLLMModelPricing{"claude-sonnet-5-5": explicit})
	require.Same(t, explicit, merged["claude-sonnet-5-5"], "an explicit remote rate must retain precedence")
}

func TestClaudeSonnet55PricingLongContextAndModifiers(t *testing.T) {
	for _, dynamic := range []*PricingService{nil, newClaudeCatalogPricingService(t)} {
		svc := NewBillingService(&config.Config{}, dynamic)
		for _, resolver := range []*ModelPricingResolver{nil, NewModelPricingResolver(nil, svc)} {
			for _, promptTokens := range []int{200000, 200001, 1000000} {
				for _, tt := range []struct {
					tier, geo  string
					multiplier float64
				}{
					{"standard", "global", 1}, {"fast", "global", 1}, {"priority", "global", 1},
					{"standard", "us", 1.1}, {"fast", "us", 1.1},
				} {
					tokens := UsageTokens{InputTokens: promptTokens - 100000, OutputTokens: 200, CacheReadTokens: 60000, CacheCreationTokens: 40000, CacheCreation5mTokens: 20000, CacheCreation1hTokens: 20000}
					cost, err := svc.CalculateCostUnified(CostInput{
						Ctx: context.Background(), Model: "claude-sonnet-5-5", Tokens: tokens,
						RateMultiplier: 1.25, ServiceTier: tt.tier, InferenceGeo: tt.geo, Resolver: resolver,
					})
					require.NoError(t, err)
					require.InDelta(t, float64(tokens.InputTokens)*2e-6*tt.multiplier, cost.InputCost, 1e-12)
					require.InDelta(t, 200*10e-6*tt.multiplier, cost.OutputCost, 1e-12)
					require.InDelta(t, (20000*2.5e-6+20000*4e-6)*tt.multiplier, cost.CacheCreationCost, 1e-12)
					require.InDelta(t, 60000*0.2e-6*tt.multiplier, cost.CacheReadCost, 1e-12)
					standard := float64(tokens.InputTokens)*2e-6 + 200*10e-6 + 20000*2.5e-6 + 20000*4e-6 + 60000*0.2e-6
					require.InDelta(t, standard*tt.multiplier, cost.TotalCost, 1e-12)
					require.InDelta(t, standard*tt.multiplier*1.25, cost.ActualCost, 1e-12)
				}
			}
		}
	}
}

func TestClaudeSonnet55PricingPreservesPartnerRates(t *testing.T) {
	for _, model := range []string{
		"anthropic.claude-sonnet-5-5",
		"projects/p/locations/us-east5/publishers/anthropic/models/claude-sonnet-5-5",
	} {
		dynamic := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
			"claude-sonnet-5-5": {InputCostPerToken: 2e-6, OutputCostPerToken: 10e-6},
			model:               {InputCostPerToken: 2.2e-6, OutputCostPerToken: 11e-6, CacheCreationInputTokenCost: 2.75e-6, CacheCreationInputTokenCostAbove1hr: 4.4e-6, CacheReadInputTokenCost: 0.22e-6},
		}}
		svc := NewBillingService(&config.Config{}, dynamic)
		cost, err := svc.CalculateCostUnified(CostInput{
			Model: model, Tokens: UsageTokens{InputTokens: 1000, OutputTokens: 200, CacheCreationTokens: 500, CacheCreation5mTokens: 200, CacheCreation1hTokens: 300, CacheReadTokens: 1000},
			RateMultiplier: 1, ServiceTier: "fast", InferenceGeo: "us",
		})
		require.NoError(t, err)
		require.InDelta(t, (1000*2.2+200*11+200*2.75+300*4.4+1000*0.22)/1e6, cost.TotalCost, 1e-12, model)
	}
}
