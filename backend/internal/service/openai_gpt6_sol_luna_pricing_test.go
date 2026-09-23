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

var gpt6SolLunaOfficialRatesForTest = []struct {
	model                             string
	input, cached, cacheWrite, output float64
}{
	{model: "gpt-6-sol", input: 2e-6, cached: 0.2e-6, cacheWrite: 2.5e-6, output: 10e-6},
	{model: "gpt-6-luna", input: 0.1e-6, cached: 0.01e-6, cacheWrite: 0.125e-6, output: 0.5e-6},
}

func TestGPT6SolLunaDedicatedPricingFallbacks(t *testing.T) {
	sources := []struct {
		name string
		data map[string]*LiteLLMModelPricing
	}{
		{name: "empty remote", data: map[string]*LiteLLMModelPricing{}},
		{name: "legacy remote", data: map[string]*LiteLLMModelPricing{
			"gpt-5.1-codex": {InputCostPerToken: 1.25e-6, OutputCostPerToken: 10e-6},
			"gpt-5.6-sol":   openAIGPT56SolFallbackPricing,
			"gpt-5.6-luna":  openAIGPT56LunaFallbackPricing,
			"gpt-6-astra":   openAIGPT6AstraFallbackPricing,
		}},
	}
	for _, model := range gpt6SolLunaOfficialRatesForTest {
		t.Run(model.model+"/no pricing service", func(t *testing.T) {
			billing := NewBillingService(&config.Config{}, nil)
			pricing, err := billing.GetModelPricing(model.model)
			require.NoError(t, err)
			assertGPT6SolLunaPricing(t, pricing, model.input, model.cached, model.cacheWrite, model.output)
			require.True(t, billing.HasIdentifiedTokenPricing(model.model))
		})
		for _, source := range sources {
			t.Run(model.model+"/"+source.name, func(t *testing.T) {
				pricingSvc := &PricingService{pricingData: source.data}
				for _, name := range []string{model.model, "openai/" + model.model} {
					raw := pricingSvc.GetModelPricing(name)
					require.NotNil(t, raw)
					require.InDelta(t, model.input, raw.InputCostPerToken, 1e-12)
					require.InDelta(t, model.cached, raw.CacheReadInputTokenCost, 1e-12)
					require.InDelta(t, model.cacheWrite, raw.CacheCreationInputTokenCost, 1e-12)
					require.InDelta(t, model.output, raw.OutputCostPerToken, 1e-12)
					require.True(t, raw.SupportsServiceTier)
					require.True(t, raw.SupportsPromptCaching)
					pricing, err := NewBillingService(&config.Config{}, pricingSvc).GetModelPricing(name)
					require.NoError(t, err)
					assertGPT6SolLunaPricing(t, pricing, model.input, model.cached, model.cacheWrite, model.output)
				}
			})
		}
	}
}

func TestGPT6SolLunaBundledPricing(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)
	data, err := (&PricingService{}).parsePricingData(body)
	require.NoError(t, err)
	var catalog map[string]struct {
		ContextWindow                int      `json:"context_window"`
		MaxInputTokens               int      `json:"max_input_tokens"`
		MaxOutputTokens              int      `json:"max_output_tokens"`
		MaxTokens                    int      `json:"max_tokens"`
		InputFlex                    float64  `json:"input_cost_per_token_flex"`
		OutputFlex                   float64  `json:"output_cost_per_token_flex"`
		CacheReadFlex                float64  `json:"cache_read_input_token_cost_flex"`
		CacheWriteFlex               float64  `json:"cache_creation_input_token_cost_flex"`
		SupportedEndpoints           []string `json:"supported_endpoints"`
		SupportsNoneReasoningEffort  bool     `json:"supports_none_reasoning_effort"`
		SupportsXhighReasoningEffort bool     `json:"supports_xhigh_reasoning_effort"`
		SupportsMaxReasoningEffort   bool     `json:"supports_max_reasoning_effort"`
	}
	require.NoError(t, json.Unmarshal(body, &catalog))
	billing := NewBillingService(&config.Config{}, &PricingService{pricingData: data})
	for _, model := range gpt6SolLunaOfficialRatesForTest {
		t.Run(model.model, func(t *testing.T) {
			require.NotNil(t, data[model.model], "the bundled catalog must contain its own entry")
			pricing, err := billing.GetModelPricing(model.model)
			require.NoError(t, err)
			assertGPT6SolLunaPricing(t, pricing, model.input, model.cached, model.cacheWrite, model.output)
			entry, ok := catalog[model.model]
			require.True(t, ok)
			require.Equal(t, 1050000, entry.ContextWindow)
			require.Equal(t, 922000, entry.MaxInputTokens)
			require.Equal(t, 128000, entry.MaxOutputTokens)
			require.Equal(t, 128000, entry.MaxTokens)
			require.InDelta(t, model.input*0.5, entry.InputFlex, 1e-12)
			require.InDelta(t, model.output*0.5, entry.OutputFlex, 1e-12)
			require.InDelta(t, model.cached*0.5, entry.CacheReadFlex, 1e-12)
			require.InDelta(t, model.cacheWrite*0.5, entry.CacheWriteFlex, 1e-12)
			require.True(t, entry.SupportsNoneReasoningEffort)
			require.True(t, entry.SupportsXhighReasoningEffort)
			require.True(t, entry.SupportsMaxReasoningEffort)
			require.ElementsMatch(t, []string{"/v1/chat/completions", "/v1/batch", "/v1/responses"}, entry.SupportedEndpoints)
		})
	}
}

func TestGPT6SolLunaCompletesMissingDynamicPolicyFields(t *testing.T) {
	for _, model := range gpt6SolLunaOfficialRatesForTest {
		t.Run(model.model, func(t *testing.T) {
			raw := &LiteLLMModelPricing{
				InputCostPerToken:               3e-6,
				InputCostPerTokenPriority:       6e-6,
				OutputCostPerToken:              13e-6,
				OutputCostPerTokenPriority:      26e-6,
				CacheReadInputTokenCost:         0.3e-6,
				CacheReadInputTokenCostPriority: 0.6e-6,
			}
			pricingSvc := &PricingService{pricingData: map[string]*LiteLLMModelPricing{model.model: raw}}
			pricing, err := NewBillingService(&config.Config{}, pricingSvc).GetModelPricing(model.model)
			require.NoError(t, err)
			assertGPT6SolLunaPricing(t, pricing, 3e-6, 0.3e-6, 3.75e-6, 13e-6)
			require.Zero(t, raw.CacheCreationInputTokenCost, "defaulting must not mutate the remote catalog")
			require.Zero(t, raw.LongContextInputTokenThreshold)
		})
	}
}

func TestGPT6SolLunaTierAndLongContextBoundaries(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	tiers := []struct {
		name       string
		multiplier float64
	}{
		{name: "default", multiplier: 1},
		{name: "priority", multiplier: 2},
		{name: "fast", multiplier: 2},
		{name: "flex", multiplier: 0.5},
	}
	for _, model := range gpt6SolLunaOfficialRatesForTest {
		for _, totalInput := range []int{271999, 272000, 272001} {
			for _, tier := range tiers {
				t.Run(fmt.Sprintf("%s/%d/%s", model.model, totalInput, tier.name), func(t *testing.T) {
					tokens := UsageTokens{InputTokens: 100000, CacheCreationTokens: 100000, CacheReadTokens: totalInput - 200000, OutputTokens: 10}
					serviceTier := tier.name
					if normalized := normalizeOpenAIServiceTier(serviceTier); normalized != nil {
						serviceTier = *normalized
					}
					cost, err := billing.CalculateCostWithServiceTier(model.model, tokens, 1.3, serviceTier)
					require.NoError(t, err)
					inputMultiplier, outputMultiplier := 1.0, 1.0
					if totalInput == 272001 {
						inputMultiplier, outputMultiplier = 2.0, 1.5
					}
					input := 100000 * model.input * tier.multiplier * inputMultiplier
					write := 100000 * model.cacheWrite * tier.multiplier * inputMultiplier
					read := float64(totalInput-200000) * model.cached * tier.multiplier * inputMultiplier
					output := 10 * model.output * tier.multiplier * outputMultiplier
					require.InDelta(t, input, cost.InputCost, 1e-12)
					require.InDelta(t, write, cost.CacheCreationCost, 1e-12)
					require.InDelta(t, read, cost.CacheReadCost, 1e-12)
					require.InDelta(t, output, cost.OutputCost, 1e-12)
					require.InDelta(t, input+write+read+output, cost.TotalCost, 1e-12)
					require.InDelta(t, (input+write+read+output)*1.3, cost.ActualCost, 1e-12)
					require.Equal(t, totalInput == 272001, cost.LongContextBillingApplied)
				})
			}
		}
	}
}

func TestGPT6SolLunaCustomPricingPreservesExplicitPricesAndOptIn(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	resolver := NewModelPricingResolver(nil, billing)
	for _, model := range gpt6SolLunaOfficialRatesForTest {
		for _, cacheWrite := range []float64{0, 11e-6} {
			for _, groupEnabled := range []bool{false, true} {
				for _, accountEnabled := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/write%g/group%t/account%t", model.model, cacheWrite, groupEnabled, accountEnabled), func(t *testing.T) {
						input, output, read := 7e-6, 9e-6, 0.4e-6
						group := &Group{
							LongContextPricingEnabled: groupEnabled,
							ModelPricing: []ChannelModelPricing{{
								Models:          []string{model.model},
								BillingMode:     BillingModeToken,
								InputPrice:      &input,
								OutputPrice:     &output,
								CacheWritePrice: &cacheWrite,
								CacheReadPrice:  &read,
							}},
						}
						cost, err := billing.CalculateCostUnified(CostInput{
							Ctx:                       context.Background(),
							Model:                     model.model,
							Group:                     group,
							Tokens:                    UsageTokens{InputTokens: 100000, CacheCreationTokens: 100000, CacheReadTokens: 72001, OutputTokens: 10},
							RateMultiplier:            1,
							ServiceTier:               "priority",
							Resolver:                  resolver,
							LongContextBillingEnabled: &accountEnabled,
						})
						require.NoError(t, err)
						inputMultiplier, outputMultiplier := 1.0, 1.0
						if groupEnabled && accountEnabled {
							inputMultiplier, outputMultiplier = 2.0, 1.5
						}
						require.InDelta(t, 0.7*inputMultiplier, cost.InputCost, 1e-12)
						require.InDelta(t, 100000*cacheWrite*inputMultiplier, cost.CacheCreationCost, 1e-12)
						require.InDelta(t, 0.0288004*inputMultiplier, cost.CacheReadCost, 1e-12)
						require.InDelta(t, 0.00009*outputMultiplier, cost.OutputCost, 1e-12)
						require.Equal(t, groupEnabled && accountEnabled, cost.LongContextBillingApplied)
					})
				}
			}
		}
		pricing, err := billing.GetModelPricing(model.model)
		require.NoError(t, err)
		assertGPT6SolLunaPricing(t, pricing, model.input, model.cached, model.cacheWrite, model.output)
	}
}

func TestGPT6SolLunaPricingPolicyPreservesCustomLongContext(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	for _, model := range gpt6SolLunaOfficialRatesForTest {
		t.Run(model.model, func(t *testing.T) {
			custom := &ModelPricing{
				InputPricePerToken:                 7e-6,
				InputPricePerTokenPriority:         13e-6,
				CacheCreationPricePerToken:         0,
				CacheCreationPricePerTokenPriority: 0,
				CacheCreationPriceExplicit:         true,
				LongContextInputThreshold:          300000,
				LongContextInputMultiplier:         3,
				LongContextOutputMultiplier:        4,
			}
			pricing := billing.applyModelSpecificPricingPolicy(model.model, custom)
			require.InDelta(t, 7e-6, pricing.InputPricePerToken, 1e-12)
			require.InDelta(t, 13e-6, pricing.InputPricePerTokenPriority, 1e-12)
			require.Zero(t, pricing.CacheCreationPricePerToken)
			require.Zero(t, pricing.CacheCreationPricePerTokenPriority)
			require.Equal(t, 300000, pricing.LongContextInputThreshold)
			require.InDelta(t, 3, pricing.LongContextInputMultiplier, 1e-12)
			require.InDelta(t, 4, pricing.LongContextOutputMultiplier, 1e-12)
		})
	}
}

func TestGPT6SolLunaDoesNotChangeExistingOpenAIRates(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)
	data, err := (&PricingService{}).parsePricingData(body)
	require.NoError(t, err)
	models := []struct {
		model                             string
		input, cached, cacheWrite, output float64
	}{
		{model: "gpt-6-astra", input: 10e-6, cached: 1e-6, cacheWrite: 12.5e-6, output: 50e-6},
		{model: "gpt-5.6-sol", input: 5e-6, cached: 0.5e-6, cacheWrite: 6.25e-6, output: 30e-6},
		{model: "gpt-5.6-terra", input: 2e-6, cached: 0.2e-6, cacheWrite: 2.5e-6, output: 12e-6},
		{model: "gpt-5.6-luna", input: 0.2e-6, cached: 0.02e-6, cacheWrite: 0.25e-6, output: 1.2e-6},
	}
	for _, model := range models {
		require.NotNil(t, data[model.model])
		for name, pricingSvc := range map[string]*PricingService{"fallback": nil, "bundled": {pricingData: data}} {
			t.Run(model.model+"/"+name, func(t *testing.T) {
				pricing, err := NewBillingService(&config.Config{}, pricingSvc).GetModelPricing(model.model)
				require.NoError(t, err)
				assertGPT6SolLunaPricing(t, pricing, model.input, model.cached, model.cacheWrite, model.output)
			})
		}
	}
}

func assertGPT6SolLunaPricing(t *testing.T, pricing *ModelPricing, input, cached, cacheWrite, output float64) {
	t.Helper()
	require.NotNil(t, pricing)
	require.InDelta(t, input, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, cached, pricing.CacheReadPricePerToken, 1e-12)
	require.InDelta(t, cacheWrite, pricing.CacheCreationPricePerToken, 1e-12)
	require.InDelta(t, output, pricing.OutputPricePerToken, 1e-12)
	require.InDelta(t, input*2, pricing.InputPricePerTokenPriority, 1e-12)
	require.InDelta(t, cached*2, pricing.CacheReadPricePerTokenPriority, 1e-12)
	require.InDelta(t, cacheWrite*2, pricing.CacheCreationPricePerTokenPriority, 1e-12)
	require.InDelta(t, output*2, pricing.OutputPricePerTokenPriority, 1e-12)
	require.Equal(t, 272000, pricing.LongContextInputThreshold)
	require.InDelta(t, 2.0, pricing.LongContextInputMultiplier, 1e-12)
	require.InDelta(t, 1.5, pricing.LongContextOutputMultiplier, 1e-12)
}
