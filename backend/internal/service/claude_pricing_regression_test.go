package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// Rates are USD per million tokens, verified against Anthropic's pricing page:
// https://platform.claude.com/docs/en/about-claude/pricing (2026-09-24).
var claudeOfficialRates = []struct {
	model               string
	input, output, read float64
}{
	{"claude-fable-5", 10, 50, 1},
	{"claude-fable-5-1", 10, 50, 0.25},
	{"claude-opus-5-5", 4, 20, 0.2},
	{"claude-opus-5", 5, 25, 0.5},
	{"claude-opus-4-8", 5, 25, 0.5},
	{"claude-opus-4-7", 5, 25, 0.5},
	{"claude-opus-4-6", 5, 25, 0.5},
	{"claude-opus-4-5", 5, 25, 0.5},
	{"claude-opus-4-1", 15, 75, 1.5},
	{"claude-opus-4", 15, 75, 1.5},
	{"claude-3-opus", 15, 75, 1.5},
	{"claude-sonnet-5", 2, 10, 0.2},
	{"claude-sonnet-4-6", 3, 15, 0.3},
	{"claude-sonnet-4-5", 3, 15, 0.3},
	{"claude-sonnet-4", 3, 15, 0.3},
	{"claude-3-7-sonnet", 3, 15, 0.3},
	{"claude-3-5-sonnet", 3, 15, 0.3},
	{"claude-haiku-4-5", 1, 5, 0.1},
	{"claude-3-5-haiku", 0.8, 4, 0.08},
	{"claude-3-haiku", 0.25, 1.25, 0.025},
}

func newClaudeCatalogPricingService(t *testing.T) *PricingService {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)
	svc := &PricingService{}
	svc.pricingData, err = svc.parsePricingData(body)
	require.NoError(t, err)
	return svc
}

func TestClaudePricingRegressionOfficialRatesAndCacheTTL(t *testing.T) {
	for _, source := range []string{"fallback", "catalog"} {
		t.Run(source, func(t *testing.T) {
			var dynamic *PricingService
			if source == "catalog" {
				dynamic = newClaudeCatalogPricingService(t)
			}
			svc := NewBillingService(&config.Config{}, dynamic)
			for _, rate := range claudeOfficialRates {
				t.Run(rate.model, func(t *testing.T) {
					pricing, err := svc.GetModelPricing(rate.model)
					require.NoError(t, err)
					require.InDelta(t, rate.input/1e6, pricing.InputPricePerToken, 1e-12)
					require.InDelta(t, rate.output/1e6, pricing.OutputPricePerToken, 1e-12)
					require.InDelta(t, rate.read/1e6, pricing.CacheReadPricePerToken, 1e-12)
					require.InDelta(t, rate.input*1.25/1e6, pricing.CacheCreation5mPrice, 1e-12)
					require.InDelta(t, rate.input*2/1e6, pricing.CacheCreation1hPrice, 1e-12)
					require.True(t, pricing.SupportsCacheBreakdown)
					tokens := UsageTokens{InputTokens: 1000, OutputTokens: 200, CacheReadTokens: 3000, CacheCreationTokens: 1000, CacheCreation5mTokens: 600, CacheCreation1hTokens: 400}
					cost, err := svc.CalculateCost(rate.model, tokens, 1.5)
					require.NoError(t, err)
					expected := (1000*rate.input + 200*rate.output + 3000*rate.read + 600*rate.input*1.25 + 400*rate.input*2) / 1e6
					require.InDelta(t, expected, cost.TotalCost, 1e-12)
					require.InDelta(t, expected*1.5, cost.ActualCost, 1e-12)
				})
			}
		})
	}
}

func TestClaudePricingRegressionCanonicalAliases(t *testing.T) {
	for _, model := range []string{
		"claude-opus-5.5", "claude-opus-5-5-20260915", "anthropic/claude-opus-5-5",
		"anthropic.claude-opus-5-5-v1:0", "us.anthropic.claude-opus-5-5-v1",
		"claude-opus-5-5@20260915", "projects/test/locations/global/publishers/anthropic/models/claude-opus-5-5",
	} {
		t.Run(model, func(t *testing.T) {
			for _, dynamic := range []*PricingService{nil, newClaudeCatalogPricingService(t)} {
				svc := NewBillingService(&config.Config{}, dynamic)
				pricing, err := svc.GetModelPricing(model)
				require.NoError(t, err)
				require.InDelta(t, 4e-6, pricing.InputPricePerToken, 1e-12)
				require.InDelta(t, 0.2e-6, pricing.CacheReadPricePerToken, 1e-12)
				require.True(t, svc.HasIdentifiedTokenPricing(model))
			}
		})
	}
}

func TestClaudePricingRegressionDoesNotBorrowAnotherVersion(t *testing.T) {
	dynamic := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"claude-opus-5-5":   {InputCostPerToken: 4e-6, OutputCostPerToken: 20e-6},
		"claude-opus-4-5":   {InputCostPerToken: 5e-6, OutputCostPerToken: 25e-6},
		"claude-sonnet-4-5": {InputCostPerToken: 3e-6, OutputCostPerToken: 15e-6},
	}}
	svc := NewBillingService(&config.Config{}, dynamic)
	for _, tt := range []struct {
		model string
		input float64
	}{
		{"claude-opus-5", 5e-6}, {"claude-opus-4", 15e-6}, {"claude-sonnet-5", 2e-6}, {"claude-haiku-4-5", 1e-6},
	} {
		t.Run(tt.model, func(t *testing.T) {
			for range 25 {
				pricing, err := svc.GetModelPricing(tt.model)
				require.NoError(t, err)
				require.InDelta(t, tt.input, pricing.InputPricePerToken, 1e-12)
			}
		})
	}
	for _, model := range []string{"claude-unknown-model", "claude-opus-50", "claude-opus-5-6", "claude-sonnet-4-99", "not-a-claude-opus-5"} {
		pricing, err := svc.GetModelPricing(model)
		require.ErrorIs(t, err, ErrModelPricingUnavailable, model)
		require.Nil(t, pricing, model)
	}
}

func TestClaudePricingRegressionExplicitProviderAndCustomPricesWin(t *testing.T) {
	dynamic := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"claude-opus-5-5":                 {InputCostPerToken: 4e-6},
		"us.anthropic.claude-opus-5-5-v1": {InputCostPerToken: 4.4e-6},
		"claude-custom-model":             {InputCostPerToken: 7e-6},
	}}
	for model, input := range map[string]float64{"us.anthropic.claude-opus-5-5-v1": 4.4e-6, "claude-custom-model": 7e-6} {
		pricing, err := NewBillingService(&config.Config{}, dynamic).GetModelPricing(model)
		require.NoError(t, err)
		require.InDelta(t, input, pricing.InputPricePerToken, 1e-12)
	}
}

func TestClaudePricingRegressionLongContextBoundary(t *testing.T) {
	for _, dynamic := range []*PricingService{nil, newClaudeCatalogPricingService(t)} {
		svc := NewBillingService(&config.Config{}, dynamic)
		for _, model := range []string{"claude-sonnet-4", "claude-sonnet-4-5", "claude-sonnet-4-6", "claude-sonnet-5", "claude-opus-4-6", "claude-opus-5-5", "claude-fable-5-1"} {
			for _, inputTokens := range []int{100000, 100001} {
				pricing, err := svc.GetModelPricing(model)
				require.NoError(t, err)
				tokens := UsageTokens{InputTokens: inputTokens, CacheReadTokens: 50000, CacheCreationTokens: 50000, CacheCreation5mTokens: 30000, CacheCreation1hTokens: 20000, OutputTokens: 100}
				cost, err := svc.CalculateCost(model, tokens, 1)
				require.NoError(t, err)
				inputMultiplier, outputMultiplier := 1.0, 1.0
				if inputTokens > 100000 && (model == "claude-sonnet-4" || model == "claude-sonnet-4-5") {
					inputMultiplier, outputMultiplier = 2, 1.5
				}
				require.InDelta(t, float64(inputTokens)*pricing.InputPricePerToken*inputMultiplier, cost.InputCost, 1e-12, model)
				require.InDelta(t, 50000*pricing.CacheReadPricePerToken*inputMultiplier, cost.CacheReadCost, 1e-12, model)
				require.InDelta(t, (30000*pricing.CacheCreation5mPrice+20000*pricing.CacheCreation1hPrice)*inputMultiplier, cost.CacheCreationCost, 1e-12, model)
				require.InDelta(t, 100*pricing.OutputPricePerToken*outputMultiplier, cost.OutputCost, 1e-12, model)
			}
		}
	}
}

func TestClaudePricingRegressionCacheBreakdownConsistency(t *testing.T) {
	svc := NewBillingService(&config.Config{}, newClaudeCatalogPricingService(t))
	for _, tt := range []struct {
		name               string
		total, five, hour  int
		wantFive, wantHour int
	}{
		{"aggregate only", 100, 0, 0, 100, 0},
		{"partial details", 100, 20, 30, 70, 30},
		{"overlapping details", 100, 100, 100, 50, 50},
		{"one hour exceeds total", 100, 0, 150, 0, 100},
		{"details only", 0, 20, 30, 20, 30},
		{"negative details", 100, -20, -30, 100, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cost, err := svc.CalculateCost("claude-opus-5", UsageTokens{CacheCreationTokens: tt.total, CacheCreation5mTokens: tt.five, CacheCreation1hTokens: tt.hour}, 1)
			require.NoError(t, err)
			require.InDelta(t, float64(tt.wantFive)*6.25e-6+float64(tt.wantHour)*10e-6, cost.CacheCreationCost, 1e-12)
		})
	}
}

func TestClaudePricingRegressionFastAndPriorityAreDifferent(t *testing.T) {
	svc := NewBillingService(&config.Config{}, newClaudeCatalogPricingService(t))
	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 100, CacheCreationTokens: 500, CacheCreation5mTokens: 200, CacheCreation1hTokens: 300, CacheReadTokens: 5000}
	for _, model := range []string{"claude-opus-4-8", "claude-opus-5", "claude-opus-5-5", "claude-opus-4-7", "claude-opus-4-6", "claude-fable-5-1", "claude-sonnet-5"} {
		t.Run(model, func(t *testing.T) {
			standard, err := svc.CalculateCost(model, tokens, 1.5)
			require.NoError(t, err)
			priority, err := svc.CalculateCostWithServiceTier(model, tokens, 1.5, "priority")
			require.NoError(t, err)
			require.InDelta(t, standard.ActualCost, priority.ActualCost, 1e-12, "Anthropic priority is not speed=fast")
			fast, err := svc.CalculateCostWithServiceTier(model, tokens, 1.5, "fast")
			require.NoError(t, err)
			multiplier := 1.0
			if model == "claude-opus-4-8" || model == "claude-opus-5" || model == "claude-opus-5-5" {
				multiplier = 2
			}
			require.InDelta(t, standard.InputCost*multiplier, fast.InputCost, 1e-12)
			require.InDelta(t, standard.OutputCost*multiplier, fast.OutputCost, 1e-12)
			require.InDelta(t, standard.CacheCreationCost*multiplier, fast.CacheCreationCost, 1e-12)
			require.InDelta(t, standard.CacheReadCost*multiplier, fast.CacheReadCost, 1e-12)
			require.InDelta(t, standard.ActualCost*multiplier, fast.ActualCost, 1e-12)
		})
	}
}

func TestClaudePricingRegressionUnifiedFastGeoAndCustomPrice(t *testing.T) {
	svc := NewBillingService(&config.Config{}, newClaudeCatalogPricingService(t))
	resolver := NewModelPricingResolver(nil, svc)
	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 200, CacheCreationTokens: 500, CacheCreation5mTokens: 200, CacheCreation1hTokens: 300, CacheReadTokens: 1000}
	for _, withResolver := range []bool{false, true} {
		input := CostInput{Model: "claude-opus-5-5", Tokens: tokens, RateMultiplier: 1.5, ServiceTier: "fast", InferenceGeo: "us"}
		if withResolver {
			input.Resolver = resolver
			input.Ctx = context.Background()
		}
		cost, err := svc.CalculateCostUnified(input)
		require.NoError(t, err)
		standard := (1000*4.0 + 200*20.0 + 200*5.0 + 300*8.0 + 1000*0.2) / 1e6
		require.InDelta(t, standard*2*1.1, cost.TotalCost, 1e-12)
		require.InDelta(t, standard*2*1.1*1.5, cost.ActualCost, 1e-12)
	}
	customPrice := 7e-6
	group := &Group{ID: 1, ModelPricing: []ChannelModelPricing{{Models: []string{"private-opus"}, InputPrice: &customPrice, OutputPrice: &customPrice}}}
	cost, err := svc.CalculateCostUnified(CostInput{
		Ctx: context.Background(), Model: "private-opus", UpstreamModel: "claude-opus-5-5", Group: group, GroupID: &group.ID,
		Tokens: UsageTokens{InputTokens: 1000, OutputTokens: 200}, RateMultiplier: 1.5, ServiceTier: "fast", InferenceGeo: "us", Resolver: resolver,
	})
	require.NoError(t, err)
	require.InDelta(t, 1200*customPrice*2*1.1*1.5, cost.ActualCost, 1e-12)
	for _, model := range []string{"claude-opus-4-5", "us.anthropic.claude-opus-5-5-v1", "projects/p/locations/us/publishers/anthropic/models/claude-opus-5-5"} {
		base, err := svc.CalculateCost(model, tokens, 1)
		require.NoError(t, err)
		cost, err := svc.CalculateCostUnified(CostInput{Model: model, Tokens: tokens, RateMultiplier: 1, ServiceTier: "fast", InferenceGeo: "us"})
		require.NoError(t, err)
		require.InDelta(t, base.TotalCost, cost.TotalCost, 1e-12, model)
	}
}

func TestClaudePricingRegressionRepairsKnownLegacyMirrorErrors(t *testing.T) {
	svc := &PricingService{}
	data, err := svc.parsePricingData([]byte(`{
		"claude-3-haiku-20240307": {"litellm_provider":"anthropic", "input_cost_per_token":0.00000025, "output_cost_per_token":0.00000125, "cache_creation_input_token_cost":0.0000003, "cache_creation_input_token_cost_above_1hr":0.000006, "cache_read_input_token_cost":0.00000003},
		"claude-3-opus-20240229": {"litellm_provider":"anthropic", "input_cost_per_token":0.000015, "output_cost_per_token":0.000075, "cache_creation_input_token_cost_above_1hr":0.000006},
		"claude-3-haiku": {"litellm_provider":"anthropic", "input_cost_per_token":0.00000025, "output_cost_per_token":0.00000125, "cache_creation_input_token_cost_above_1hr":0.0000007},
		"us.anthropic.claude-3-haiku-20240307-v1:0": {"litellm_provider":"bedrock", "input_cost_per_token":0.00000025, "output_cost_per_token":0.00000125, "cache_creation_input_token_cost_above_1hr":0.000006}
	}`))
	require.NoError(t, err)
	require.InDelta(t, 0.3125e-6, data["claude-3-haiku-20240307"].CacheCreationInputTokenCost, 1e-12)
	require.InDelta(t, 0.5e-6, data["claude-3-haiku-20240307"].CacheCreationInputTokenCostAbove1hr, 1e-12)
	require.InDelta(t, 0.025e-6, data["claude-3-haiku-20240307"].CacheReadInputTokenCost, 1e-12)
	require.InDelta(t, 30e-6, data["claude-3-opus-20240229"].CacheCreationInputTokenCostAbove1hr, 1e-12)
	require.InDelta(t, 0.7e-6, data["claude-3-haiku"].CacheCreationInputTokenCostAbove1hr, 1e-12)
	require.InDelta(t, 6e-6, data["us.anthropic.claude-3-haiku-20240307-v1:0"].CacheCreationInputTokenCostAbove1hr, 1e-12)
}
