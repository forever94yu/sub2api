package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestClaudeHaiku55PricingAliasesAndVersionIsolation(t *testing.T) {
	for source, dynamic := range map[string]*PricingService{"fallback": nil, "catalog": newClaudeCatalogPricingService(t)} {
		svc := NewBillingService(&config.Config{}, dynamic)
		for _, model := range []string{
			"claude-haiku-5-5", "claude-haiku-5.5", "anthropic/claude-haiku-5-5",
			"models/claude-haiku-5-5", "anthropic.claude-haiku-5-5",
			"us.anthropic.claude-haiku-5-5-v1:0", "claude-haiku-5-5-20261007",
			"claude-haiku-5-5@20261007", "projects/p/locations/global/publishers/anthropic/models/claude-haiku-5-5",
		} {
			t.Run(source+"/"+model, func(t *testing.T) {
				pricing, err := svc.GetModelPricing(model)
				require.NoError(t, err)
				require.InDelta(t, 0.1e-6, pricing.InputPricePerToken, 1e-18)
				require.InDelta(t, 0.5e-6, pricing.OutputPricePerToken, 1e-18)
				require.InDelta(t, 0.125e-6, pricing.CacheCreation5mPrice, 1e-18)
				require.InDelta(t, 0.2e-6, pricing.CacheCreation1hPrice, 1e-18)
				require.InDelta(t, 0.01e-6, pricing.CacheReadPricePerToken, 1e-18)
				require.True(t, pricing.SupportsCacheBreakdown)
				require.True(t, svc.HasIdentifiedTokenPricing(model))
			})
		}
		for _, model := range []string{"claude-haiku-5", "claude-haiku-5-6", "claude-haiku-55", "claude-haiku-5-50", "claude-haiku-5-5-preview"} {
			pricing, err := svc.GetModelPricing(model)
			require.ErrorIs(t, err, ErrModelPricingUnavailable, model)
			require.Nil(t, pricing, model)
			require.False(t, svc.HasIdentifiedTokenPricing(model), model)
		}
	}
	for _, configured := range []string{"claude-haiku-4-5", "claude-haiku-5-5"} {
		dynamic := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
			configured: {InputCostPerToken: 7e-6, OutputCostPerToken: 14e-6},
		}}
		svc := NewBillingService(&config.Config{}, dynamic)
		for model, defaultInput := range map[string]float64{"claude-haiku-4-5": 1e-6, "claude-haiku-5-5": 0.1e-6} {
			pricing, err := svc.GetModelPricing(model)
			require.NoError(t, err)
			if model == configured {
				defaultInput = 7e-6
			}
			require.InDelta(t, defaultInput, pricing.InputPricePerToken, 1e-18, "configured=%s requested=%s", configured, model)
		}
	}
}

func TestClaudeHaiku55PricingLoadsMissingPersistedEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old-pricing.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"claude-haiku-4-5":{"input_cost_per_token":0.000007,"output_cost_per_token":0.000014}}`), 0600))
	cfg := &config.Config{}
	cfg.Pricing.FallbackFile = filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json")
	dynamic := &PricingService{cfg: cfg}
	require.NoError(t, dynamic.loadPricingData(path))
	pricing := dynamic.GetIdentifiedModelPricing("claude-haiku-5-5")
	require.NotNil(t, pricing)
	require.InDelta(t, 0.1e-6, pricing.InputCostPerToken, 1e-18)
	require.Equal(t, 100000, pricing.LongContextInputTokenThreshold)
	require.Equal(t, 5.0, pricing.LongContextInputCostMultiplier)
	require.Equal(t, 5.0, pricing.LongContextOutputCostMultiplier)
	require.Equal(t, 7e-6, dynamic.GetIdentifiedModelPricing("claude-haiku-4-5").InputCostPerToken)

	explicit := &LiteLLMModelPricing{InputCostPerToken: 7e-6, OutputCostPerToken: 14e-6}
	merged := dynamic.mergeFallbackPricingData(map[string]*LiteLLMModelPricing{"claude-haiku-5-5": explicit})
	require.Same(t, explicit, merged["claude-haiku-5-5"])
}

func TestClaudeHaiku55PricingCompletesKnownOfficialDynamicCards(t *testing.T) {
	for _, tt := range []struct {
		name      string
		model     string
		overrides map[string]any
		wantInput float64
		wantLong  bool
	}{
		{"missing policy", "claude-haiku-5-5", nil, 0.1000005, true},
		{"missing required flag", "claude-haiku-5-5", map[string]any{"long_context_input_token_threshold": 100000, "long_context_input_cost_multiplier": 5, "long_context_output_cost_multiplier": 5}, 0.1000005, true},
		{"custom input price", "claude-haiku-5-5", map[string]any{"input_cost_per_token": 0.7e-6}, 0.1400007, false},
		{"custom threshold", "claude-haiku-5-5", map[string]any{"long_context_input_token_threshold": 150000, "long_context_input_cost_multiplier": 2, "long_context_output_cost_multiplier": 3}, 0.0200001, false},
		{"explicit zero threshold", "claude-haiku-5-5", map[string]any{"long_context_input_token_threshold": 0}, 0.0200001, false},
		{"explicit optional policy", "claude-haiku-5-5", map[string]any{"long_context_input_token_threshold": 100000, "long_context_input_cost_multiplier": 5, "long_context_output_cost_multiplier": 5, "long_context_pricing_required": false}, 0.0200001, false},
		{"partner price", "anthropic.claude-haiku-5-5", map[string]any{"litellm_provider": "bedrock"}, 0.0200001, false},
		{"another version", "claude-haiku-4-5", nil, 0.0200001, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			card := map[string]any{
				"litellm_provider": "anthropic", "input_cost_per_token": 0.1e-6, "output_cost_per_token": 0.5e-6,
				"cache_creation_input_token_cost": 0.125e-6, "cache_creation_input_token_cost_above_1hr": 0.2e-6, "cache_read_input_token_cost": 0.01e-6,
			}
			for field, value := range tt.overrides {
				card[field] = value
			}
			body, err := json.Marshal(map[string]any{tt.model: card})
			require.NoError(t, err)
			dynamic := &PricingService{}
			dynamic.pricingData, err = dynamic.parsePricingData(body)
			require.NoError(t, err)
			svc := NewBillingService(&config.Config{}, dynamic)
			disabled := false
			cost, err := svc.CalculateCostUnified(CostInput{
				Ctx: context.Background(), Model: tt.model, Tokens: UsageTokens{InputTokens: 200001}, RateMultiplier: 1,
				Group: &Group{LongContextPricingEnabled: false}, LongContextBillingEnabled: &disabled, Resolver: NewModelPricingResolver(nil, svc),
			})
			require.NoError(t, err)
			require.InDelta(t, tt.wantInput, cost.InputCost, 1e-12)
			require.Equal(t, tt.wantLong, cost.LongContextBillingApplied)
		})
	}
}

func TestClaudeHaiku55PricingPromptBoundariesAndModifiers(t *testing.T) {
	// Haiku's tier depends on the full prompt, including cache reads and writes.
	// All token categories use the selected rate for the whole request.
	for source, dynamic := range map[string]*PricingService{"fallback": nil, "catalog": newClaudeCatalogPricingService(t)} {
		svc := NewBillingService(&config.Config{}, dynamic)
		for _, withResolver := range []bool{false, true} {
			for _, tt := range []struct {
				name                                string
				tokens                              UsageTokens
				long                                bool
				input, output, write5, write1, read float64
			}{
				{"below", UsageTokens{InputTokens: 39999, OutputTokens: 200, CacheCreationTokens: 30000, CacheCreation5mTokens: 10000, CacheCreation1hTokens: 20000, CacheReadTokens: 30000}, false, 0.1, 0.5, 0.125, 0.2, 0.01},
				{"exact", UsageTokens{InputTokens: 40000, OutputTokens: 200, CacheCreationTokens: 30000, CacheCreation5mTokens: 10000, CacheCreation1hTokens: 20000, CacheReadTokens: 30000}, false, 0.1, 0.5, 0.125, 0.2, 0.01},
				{"input crosses", UsageTokens{InputTokens: 40001, OutputTokens: 200, CacheCreationTokens: 30000, CacheCreation5mTokens: 10000, CacheCreation1hTokens: 20000, CacheReadTokens: 30000}, true, 0.5, 2.5, 0.625, 1, 0.05},
				{"read crosses", UsageTokens{InputTokens: 40000, OutputTokens: 200, CacheCreationTokens: 30000, CacheCreation5mTokens: 10000, CacheCreation1hTokens: 20000, CacheReadTokens: 30001}, true, 0.5, 2.5, 0.625, 1, 0.05},
				{"5m crosses", UsageTokens{InputTokens: 40000, OutputTokens: 200, CacheCreationTokens: 30001, CacheCreation5mTokens: 10001, CacheCreation1hTokens: 20000, CacheReadTokens: 30000}, true, 0.5, 2.5, 0.625, 1, 0.05},
				{"1h crosses", UsageTokens{InputTokens: 40000, OutputTokens: 200, CacheCreationTokens: 30001, CacheCreation5mTokens: 10000, CacheCreation1hTokens: 20001, CacheReadTokens: 30000}, true, 0.5, 2.5, 0.625, 1, 0.05},
				{"details only", UsageTokens{InputTokens: 40000, OutputTokens: 200, CacheCreation5mTokens: 10000, CacheCreation1hTokens: 20001, CacheReadTokens: 30000}, true, 0.5, 2.5, 0.625, 1, 0.05},
				{"output excluded", UsageTokens{InputTokens: 40000, OutputTokens: 128000, CacheCreationTokens: 30000, CacheCreation5mTokens: 10000, CacheCreation1hTokens: 20000, CacheReadTokens: 30000}, false, 0.1, 0.5, 0.125, 0.2, 0.01},
			} {
				for _, modifier := range []struct {
					tier, geo string
					multiple  float64
				}{{"standard", "global", 1}, {"fast", "global", 1}, {"priority", "global", 1}, {"flex", "global", 1}, {"standard", "us", 1.1}, {"fast", "us", 1.1}} {
					t.Run(fmt.Sprintf("%s/resolver%t/%s/%s/%s", source, withResolver, tt.name, modifier.tier, modifier.geo), func(t *testing.T) {
						input := CostInput{Model: "claude-haiku-5-5", Tokens: tt.tokens, RateMultiplier: 1.25, ServiceTier: modifier.tier, InferenceGeo: modifier.geo}
						if withResolver {
							input.Ctx = context.Background()
							input.Resolver = NewModelPricingResolver(nil, svc)
						}
						cost, err := svc.CalculateCostUnified(input)
						require.NoError(t, err)
						inputCost := float64(tt.tokens.InputTokens) * tt.input / 1e6 * modifier.multiple
						outputCost := float64(tt.tokens.OutputTokens) * tt.output / 1e6 * modifier.multiple
						writeCost := (float64(tt.tokens.CacheCreation5mTokens)*tt.write5 + float64(tt.tokens.CacheCreation1hTokens)*tt.write1) / 1e6 * modifier.multiple
						readCost := float64(tt.tokens.CacheReadTokens) * tt.read / 1e6 * modifier.multiple
						require.InDelta(t, inputCost, cost.InputCost, 1e-12)
						require.InDelta(t, outputCost, cost.OutputCost, 1e-12)
						require.InDelta(t, writeCost, cost.CacheCreationCost, 1e-12)
						require.InDelta(t, readCost, cost.CacheReadCost, 1e-12)
						require.InDelta(t, inputCost+outputCost+writeCost+readCost, cost.TotalCost, 1e-12)
						require.InDelta(t, (inputCost+outputCost+writeCost+readCost)*1.25, cost.ActualCost, 1e-12)
						require.Equal(t, tt.long, cost.LongContextBillingApplied)
					})
				}
			}
		}
	}
}

func TestClaudeHaiku55PricingOfficialTierDoesNotDependOnLongContextOptIn(t *testing.T) {
	for source, dynamic := range map[string]*PricingService{"fallback": nil, "catalog": newClaudeCatalogPricingService(t)} {
		svc := NewBillingService(&config.Config{}, dynamic)
		for _, withResolver := range []bool{false, true} {
			for _, enabled := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/resolver%t/enabled%t", source, withResolver, enabled), func(t *testing.T) {
					input := CostInput{
						Model: "claude-haiku-5-5", Tokens: UsageTokens{InputTokens: 100001, OutputTokens: 200},
						RateMultiplier: 1, LongContextBillingEnabled: &enabled,
					}
					if withResolver {
						input.Ctx = context.Background()
						input.Resolver = NewModelPricingResolver(nil, svc)
						input.Group = &Group{LongContextPricingEnabled: enabled}
					}
					cost, err := svc.CalculateCostUnified(input)
					require.NoError(t, err)
					require.InDelta(t, 0.0500005, cost.InputCost, 1e-12)
					require.InDelta(t, 0.0005, cost.OutputCost, 1e-12)
					require.True(t, cost.LongContextBillingApplied)
				})
			}
		}
	}
}

func TestClaudeHaiku55PricingCacheAggregateControlsThresholdAndCharges(t *testing.T) {
	svc := NewBillingService(&config.Config{}, nil)
	for _, tt := range []struct {
		name                  string
		aggregate, five, hour int
		wantWrite, wantTotal  float64
		wantLong              bool
	}{
		{"overlapping details capped by aggregate", 30000, 50000, 50000, 0.004875, 0.009275, false},
		{"missing details use 5m", 30001, 0, 1, 0.018751, 0.040751, true},
		{"aggregate only uses 5m", 30001, 0, 0, 0.018750625, 0.040750625, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cost, err := svc.CalculateCost("claude-haiku-5-5", UsageTokens{
				InputTokens: 40000, OutputTokens: 200, CacheReadTokens: 30000,
				CacheCreationTokens: tt.aggregate, CacheCreation5mTokens: tt.five, CacheCreation1hTokens: tt.hour,
			}, 1)
			require.NoError(t, err)
			require.InDelta(t, tt.wantWrite, cost.CacheCreationCost, 1e-12)
			require.InDelta(t, tt.wantTotal, cost.TotalCost, 1e-12)
			require.Equal(t, tt.wantLong, cost.LongContextBillingApplied)
		})
	}
}

func TestClaudeHaiku55PricingAccountStatsIncludesCacheTTL(t *testing.T) {
	for source, dynamic := range map[string]*PricingService{"fallback": nil, "catalog": newClaudeCatalogPricingService(t)} {
		svc := NewBillingService(&config.Config{}, dynamic)
		for _, tt := range []struct {
			input, aggregate int
			wantCost         float64
		}{
			{40000, 30000, 0.00965},
			{40000, 0, 0.00965},
			{40001, 30000, 0.0482505},
			{40001, 0, 0.0482505},
		} {
			t.Run(fmt.Sprintf("%s/input%d/cache%d", source, tt.input, tt.aggregate), func(t *testing.T) {
				cost := tryModelFilePricing(svc, "claude-haiku-5-5", UsageTokens{
					InputTokens: tt.input, OutputTokens: 200, CacheReadTokens: 30000,
					CacheCreationTokens: tt.aggregate, CacheCreation5mTokens: 10000, CacheCreation1hTokens: 20000,
				}, "standard")
				require.NotNil(t, cost)
				require.InDelta(t, tt.wantCost, *cost, 1e-12)
			})
		}
	}
}

func TestClaudeHaiku55PricingDirectChannelFlatRatesOverrideOfficialTier(t *testing.T) {
	svc := NewBillingService(&config.Config{}, nil)
	input, output, write, read := 0.1e-6, 0.5e-6, 0.125e-6, 0.01e-6
	channel := &ChannelModelPricing{InputPrice: &input, OutputPrice: &output, CacheWritePrice: &write, CacheReadPrice: &read}
	cost, err := svc.calculateCostInternal("claude-haiku-5-5", UsageTokens{InputTokens: 100001, OutputTokens: 200}, 1, "priority", channel)
	require.NoError(t, err)
	require.InDelta(t, 0.0100001, cost.InputCost, 1e-12)
	require.InDelta(t, 0.0001, cost.OutputCost, 1e-12)
	require.False(t, cost.LongContextBillingApplied)

	official, err := svc.CalculateCost("claude-haiku-5-5", UsageTokens{InputTokens: 100001}, 1)
	require.NoError(t, err)
	require.InDelta(t, 0.0500005, official.InputCost, 1e-12, "custom overrides must not mutate shared fallback prices")
}

func TestClaudeHaiku55PricingCustomFlatRatesOverrideOfficialTiers(t *testing.T) {
	svc := NewBillingService(&config.Config{}, nil)
	resolver := NewModelPricingResolver(nil, svc)
	for _, write := range []float64{0, 0.8e-6} {
		for _, groupEnabled := range []bool{false, true} {
			for _, accountEnabled := range []bool{false, true} {
				t.Run(fmt.Sprintf("write%g/group%t/account%t", write, groupEnabled, accountEnabled), func(t *testing.T) {
					input, output, read := 0.7e-6, 0.9e-6, 0.04e-6
					group := &Group{LongContextPricingEnabled: groupEnabled, ModelPricing: []ChannelModelPricing{{
						Models: []string{"claude-haiku-5-5"}, InputPrice: &input, OutputPrice: &output, CacheWritePrice: &write, CacheReadPrice: &read,
					}}}
					cost, err := svc.CalculateCostUnified(CostInput{
						Ctx: context.Background(), Model: "claude-haiku-5-5", Group: group,
						Tokens:         UsageTokens{InputTokens: 40000, OutputTokens: 200, CacheCreationTokens: 30001, CacheCreation5mTokens: 10000, CacheCreation1hTokens: 20001, CacheReadTokens: 30000},
						RateMultiplier: 1.25, ServiceTier: "priority", InferenceGeo: "us", Resolver: resolver, LongContextBillingEnabled: &accountEnabled,
					})
					require.NoError(t, err)
					multiple := 1.1
					require.InDelta(t, 0.028*multiple, cost.InputCost, 1e-12)
					require.InDelta(t, 0.00018*multiple, cost.OutputCost, 1e-12)
					require.InDelta(t, 30001*write*multiple, cost.CacheCreationCost, 1e-12)
					require.InDelta(t, 0.0012*multiple, cost.CacheReadCost, 1e-12)
					require.InDelta(t, (0.028+0.00018+30001*write+0.0012)*multiple*1.25, cost.ActualCost, 1e-12)
					require.False(t, cost.LongContextBillingApplied)
				})
			}
		}
	}
}

func TestClaudeHaiku55PricingCustomIntervalsAvoidDoublePremium(t *testing.T) {
	svc := NewBillingService(&config.Config{}, nil)
	resolver := NewModelPricingResolver(nil, svc)
	ptrFloat := func(value float64) *float64 { return &value }
	firstMax := 100000
	configured := &ChannelModelPricing{
		Platform: "anthropic", Models: []string{"claude-haiku-5-5"}, BillingMode: BillingModeToken,
		Intervals: []PricingInterval{
			{MinTokens: 0, MaxTokens: &firstMax, InputPrice: ptrFloat(0.7e-6), OutputPrice: ptrFloat(0.9e-6), CacheWritePrice: ptrFloat(0.8e-6), CacheReadPrice: ptrFloat(0.04e-6)},
			{MinTokens: 100000, InputPrice: ptrFloat(1.4e-6), OutputPrice: ptrFloat(1.8e-6), CacheWritePrice: ptrFloat(1.6e-6), CacheReadPrice: ptrFloat(0.08e-6)},
		},
	}
	resolved := resolver.resolveConfiguredPricing(configured, "claude-haiku-5-5", PricingSourceChannel)
	resolved.longContextPricingEnabled = true
	for _, aggregate := range []int{30001, 0} {
		cost, err := resolver.billingService.CalculateCostUnified(CostInput{
			Ctx: context.Background(), Model: "claude-haiku-5-5", Resolver: resolver, Resolved: resolved,
			Tokens:         UsageTokens{InputTokens: 40000, OutputTokens: 200, CacheCreationTokens: aggregate, CacheCreation5mTokens: 10000, CacheCreation1hTokens: 20001, CacheReadTokens: 30000},
			RateMultiplier: 1.25, ServiceTier: "fast", InferenceGeo: "us",
		})
		require.NoError(t, err)
		require.InDelta(t, 0.056*1.1, cost.InputCost, 1e-12)
		require.InDelta(t, 0.00036*1.1, cost.OutputCost, 1e-12)
		require.InDelta(t, 0.0480016*1.1, cost.CacheCreationCost, 1e-12)
		require.InDelta(t, 0.0024*1.1, cost.CacheReadCost, 1e-12)
		require.InDelta(t, 0.1067616*1.1*1.25, cost.ActualCost, 1e-12)
		require.False(t, cost.LongContextBillingApplied)
	}
}

func TestClaudeHaiku55PricingPreservesExplicitDynamicAndPartnerRates(t *testing.T) {
	for _, model := range []string{"claude-haiku-5-5", "anthropic.claude-haiku-5-5", "projects/p/locations/us-east5/publishers/anthropic/models/claude-haiku-5-5"} {
		custom := &LiteLLMModelPricing{
			InputCostPerToken: 0.7e-6, OutputCostPerToken: 0.9e-6, CacheCreationInputTokenCost: 0.8e-6, CacheCreationInputTokenCostAbove1hr: 1.6e-6, CacheReadInputTokenCost: 0.04e-6,
			LongContextInputTokenThreshold: 150000, LongContextInputCostMultiplier: 2, LongContextOutputCostMultiplier: 3,
		}
		dynamic := &PricingService{pricingData: map[string]*LiteLLMModelPricing{model: custom}}
		svc := NewBillingService(&config.Config{}, dynamic)
		for _, prompt := range []int{100001, 150000, 150001} {
			cost, err := svc.CalculateCostUnified(CostInput{
				Model: model, Tokens: UsageTokens{InputTokens: prompt - 60000, OutputTokens: 200, CacheCreationTokens: 30000, CacheCreation5mTokens: 10000, CacheCreation1hTokens: 20000, CacheReadTokens: 30000},
				RateMultiplier: 1.25, ServiceTier: "fast", InferenceGeo: "us",
			})
			require.NoError(t, err)
			geo, inMultiple, outMultiple := 1.0, 1.0, 1.0
			if model == "claude-haiku-5-5" {
				geo = 1.1
			}
			if prompt > 150000 {
				inMultiple, outMultiple = 2, 3
			}
			require.InDelta(t, float64(prompt-60000)*0.7e-6*inMultiple*geo, cost.InputCost, 1e-12)
			require.InDelta(t, 0.00018*outMultiple*geo, cost.OutputCost, 1e-12)
			require.InDelta(t, 0.04*inMultiple*geo, cost.CacheCreationCost, 1e-12)
			require.InDelta(t, 0.0012*inMultiple*geo, cost.CacheReadCost, 1e-12)
			require.Equal(t, prompt > 150000, cost.LongContextBillingApplied)
		}
		require.Equal(t, 150000, custom.LongContextInputTokenThreshold)
		require.Equal(t, 0.7e-6, custom.InputCostPerToken)
	}
}
