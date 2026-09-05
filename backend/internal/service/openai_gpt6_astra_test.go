package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	openaipkg "github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

func TestNormalizeKnownOpenAICodexModel_GPT6Astra(t *testing.T) {
	for _, model := range []string{
		"gpt-6-astra",
		"GPT-6-ASTRA",
		"openai/gpt-6-astra",
		"gpt-image-proxy/gpt-6-astra",
	} {
		t.Run(model, func(t *testing.T) {
			normalized, ok := normalizeKnownCodexModel(model)
			require.True(t, ok)
			require.Equal(t, "gpt-6-astra", normalized)
		})
	}
}

func TestGPT6AstraPreservesMaxReasoningEffort(t *testing.T) {
	for _, model := range []string{"gpt-6-astra", "openai/gpt-6-astra"} {
		t.Run(model, func(t *testing.T) {
			require.Equal(t, "max", normalizeOpenAIReasoningEffortForModel("max", model))
		})
	}
	require.Equal(t, "xhigh", normalizeOpenAIReasoningEffortForModel("max", "gpt-6-astra-preview"))

	reasoningEffort := extractOpenAIReasoningEffortFromBody(
		[]byte(`{"model":"gpt-6-astra","reasoning":{"effort":"max"}}`),
		"gpt-6-astra",
	)
	require.NotNil(t, reasoningEffort)
	require.Equal(t, "max", *reasoningEffort)

	req := &apicompat.AnthropicRequest{
		OutputConfig: &apicompat.AnthropicOutputConfig{Effort: "max"},
	}
	require.Equal(t, "max", openAICompatAnthropicReasoningEffort(req, "gpt-6-astra", "xhigh"))
}

func TestGPT6AstraRejectsUnsupportedAliases(t *testing.T) {
	pricingSvc := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"gpt-6-astra":              openAIGPT6AstraFallbackPricing,
		openaipkg.DefaultTestModel: openAIGPT54FallbackPricing,
	}}
	billingSvc := NewBillingService(&config.Config{}, pricingSvc)

	for _, model := range []string{
		"gpt-6-astra-openai-compact",
		"gpt-6-astra-20260905",
		"gpt-6-astra-2026-09-05",
		"gpt-6-astra-high",
		"gpt-6-astra-preview",
		"gpt-6-20260905-astra",
		"gpt-20260905-6-astra",
		"gpt-6-v1:0-astra",
		"gpt-6-astra-opus",
		"gpt-6-astra-haiku",
		"gpt-6-astra-gemini-3.1-pro",
		"20260905-gpt-6-astra",
		"v1:0-gpt-6-astra",
		"20260905-gpt-6-astra-opus",
		"gpt_6_astra",
		"gpt 6 astra",
		"vendor-gpt-6-build-astra-opus",
		"gpt-image-gpt-6-astra",
	} {
		t.Run(model, func(t *testing.T) {
			require.Empty(t, normalizeKnownOpenAICodexModel(model))
			_, known := normalizeKnownCodexModel(model)
			require.False(t, known)
			require.Equal(t, []string{model}, usageBillingModelCandidates(model))
			require.Nil(t, pricingSvc.GetIdentifiedModelPricing(model))
			require.Nil(t, pricingSvc.GetModelPricing(model))
			_, err := billingSvc.GetModelPricing(model)
			require.ErrorIs(t, err, ErrModelPricingUnavailable)
		})
	}
}

func TestDefaultPricingIncludesOfficialGPT6AstraRates(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)

	pricingData, err := (&PricingService{}).parsePricingData(data)
	require.NoError(t, err)
	pricing := pricingData["gpt-6-astra"]
	require.NotNil(t, pricing)
	require.InDelta(t, 10e-6, pricing.InputCostPerToken, 1e-12)
	require.InDelta(t, 20e-6, pricing.InputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 1e-6, pricing.CacheReadInputTokenCost, 1e-12)
	require.InDelta(t, 2e-6, pricing.CacheReadInputTokenCostPriority, 1e-12)
	require.InDelta(t, 12.5e-6, pricing.CacheCreationInputTokenCost, 1e-12)
	require.InDelta(t, 25e-6, pricing.CacheCreationInputTokenCostPriority, 1e-12)
	require.InDelta(t, 50e-6, pricing.OutputCostPerToken, 1e-12)
	require.InDelta(t, 100e-6, pricing.OutputCostPerTokenPriority, 1e-12)
	require.Equal(t, 272000, pricing.LongContextInputTokenThreshold)
	require.InDelta(t, 2.0, pricing.LongContextInputCostMultiplier, 1e-12)
	require.InDelta(t, 1.5, pricing.LongContextOutputCostMultiplier, 1e-12)
	require.True(t, pricing.SupportsServiceTier)
	require.True(t, pricing.SupportsPromptCaching)

	var catalog map[string]struct {
		MaxInputTokens             int      `json:"max_input_tokens"`
		MaxOutputTokens            int      `json:"max_output_tokens"`
		MaxTokens                  int      `json:"max_tokens"`
		SupportedEndpoints         []string `json:"supported_endpoints"`
		SupportsMaxReasoningEffort bool     `json:"supports_max_reasoning_effort"`
	}
	require.NoError(t, json.Unmarshal(data, &catalog))
	entry, ok := catalog["gpt-6-astra"]
	require.True(t, ok)
	require.Equal(t, 1050000, entry.MaxInputTokens)
	require.Equal(t, 128000, entry.MaxOutputTokens)
	require.Equal(t, 128000, entry.MaxTokens)
	require.True(t, entry.SupportsMaxReasoningEffort)
	require.ElementsMatch(t, []string{
		"/v1/chat/completions",
		"/v1/batch",
		"/v1/responses",
	}, entry.SupportedEndpoints)
}

func TestGPT6AstraDedicatedFallbackUsesOfficialRates(t *testing.T) {
	t.Run("pricing service", func(t *testing.T) {
		pricingSvc := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
			"gpt-5.4": {InputCostPerToken: 2.5e-6},
		}}
		pricing, err := NewBillingService(&config.Config{}, pricingSvc).GetModelPricing("gpt-6-astra")
		require.NoError(t, err)
		assertGPT6AstraPricing(t, pricing)
	})

	t.Run("billing service", func(t *testing.T) {
		pricing, err := NewBillingService(&config.Config{}, nil).GetModelPricing("gpt-6-astra")
		require.NoError(t, err)
		assertGPT6AstraPricing(t, pricing)
	})
}

func TestGPT6AstraCompletesMissingDynamicPolicyFields(t *testing.T) {
	pricingSvc := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"gpt-6-astra": {
			InputCostPerToken:               10e-6,
			InputCostPerTokenPriority:       20e-6,
			OutputCostPerToken:              50e-6,
			OutputCostPerTokenPriority:      100e-6,
			CacheReadInputTokenCost:         1e-6,
			CacheReadInputTokenCostPriority: 2e-6,
		},
	}}

	pricing, err := NewBillingService(&config.Config{}, pricingSvc).GetModelPricing("gpt-6-astra")
	require.NoError(t, err)
	assertGPT6AstraPricing(t, pricing)
}

func TestGPT6AstraPricingAcrossServiceTiersAndLongContext(t *testing.T) {
	svc := NewBillingService(&config.Config{}, nil)
	tokens := UsageTokens{
		InputTokens:         100000,
		CacheCreationTokens: 100000,
		CacheReadTokens:     73000,
		OutputTokens:        10,
	}
	tests := []struct {
		name        string
		serviceTier string
		normalized  string
		input       float64
		cacheWrite  float64
		cacheRead   float64
		output      float64
		total       float64
	}{
		{name: "standard", input: 2, cacheWrite: 2.5, cacheRead: 0.146, output: 0.00075, total: 4.64675},
		{name: "priority", serviceTier: "priority", normalized: "priority", input: 4, cacheWrite: 5, cacheRead: 0.292, output: 0.0015, total: 9.2935},
		{name: "fast", serviceTier: "fast", normalized: "priority", input: 4, cacheWrite: 5, cacheRead: 0.292, output: 0.0015, total: 9.2935},
		{name: "flex", serviceTier: "flex", normalized: "flex", input: 1, cacheWrite: 1.25, cacheRead: 0.073, output: 0.000375, total: 2.323375},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceTier := tt.serviceTier
			if normalized := normalizeOpenAIServiceTier(serviceTier); normalized != nil {
				serviceTier = *normalized
			}
			require.Equal(t, tt.normalized, serviceTier)
			cost, err := svc.CalculateCostWithServiceTier("gpt-6-astra", tokens, 1, serviceTier)
			require.NoError(t, err)
			require.True(t, cost.LongContextBillingApplied)
			require.InDelta(t, tt.input, cost.InputCost, 1e-12)
			require.InDelta(t, tt.cacheWrite, cost.CacheCreationCost, 1e-12)
			require.InDelta(t, tt.cacheRead, cost.CacheReadCost, 1e-12)
			require.InDelta(t, tt.output, cost.OutputCost, 1e-12)
			require.InDelta(t, tt.total, cost.TotalCost, 1e-12)
		})
	}

	boundary, err := svc.CalculateCost("gpt-6-astra", UsageTokens{
		InputTokens:         100000,
		CacheCreationTokens: 100000,
		CacheReadTokens:     72000,
		OutputTokens:        10,
	}, 1)
	require.NoError(t, err)
	require.False(t, boundary.LongContextBillingApplied)
	require.InDelta(t, 1.0, boundary.InputCost, 1e-12)
	require.InDelta(t, 1.25, boundary.CacheCreationCost, 1e-12)
	require.InDelta(t, 0.072, boundary.CacheReadCost, 1e-12)
	require.InDelta(t, 0.0005, boundary.OutputCost, 1e-12)
}

func TestGPT6AstraResponsesUsageParsesAndRecordsPriorityBilling(t *testing.T) {
	body := []byte(`{
		"id":"resp_astra",
		"model":"gpt-6-astra",
		"service_tier":"priority",
		"usage":{
			"input_tokens":1000,
			"input_tokens_details":{"cached_tokens":100,"cache_write_tokens":200},
			"output_tokens":50,
			"output_tokens_details":{"reasoning_tokens":10},
			"total_tokens":1050
		}
	}`)
	usage, ok := extractOpenAIUsageFromJSONBytes(body)
	require.True(t, ok)
	require.Equal(t, 1000, usage.InputTokens)
	require.Equal(t, 200, usage.CacheCreationInputTokens)
	require.Equal(t, 100, usage.CacheReadInputTokens)
	require.Equal(t, 50, usage.OutputTokens)

	serviceTier := extractOpenAIServiceTierFromBody(body)
	require.NotNil(t, serviceTier)
	require.Equal(t, "priority", *serviceTier)

	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(
		usageRepo,
		&openAIRecordUsageUserRepoStub{},
		&openAIRecordUsageSubRepoStub{},
		nil,
	)
	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID:   "resp_astra",
			Usage:       usage,
			Model:       "gpt-6-astra",
			ServiceTier: serviceTier,
			Duration:    time.Second,
		},
		APIKey:  &APIKey{ID: 1060},
		User:    &User{ID: 2060},
		Account: &Account{ID: 3060, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
	})
	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, 700, usageRepo.lastLog.InputTokens)
	require.Equal(t, 200, usageRepo.lastLog.CacheCreationTokens)
	require.Equal(t, 100, usageRepo.lastLog.CacheReadTokens)
	require.Equal(t, 1050, usageRepo.lastLog.TotalTokens())
	require.Equal(t, "priority", *usageRepo.lastLog.ServiceTier)
	require.InDelta(t, 0.014, usageRepo.lastLog.InputCost, 1e-12)
	require.InDelta(t, 0.005, usageRepo.lastLog.CacheCreationCost, 1e-12)
	require.InDelta(t, 0.0002, usageRepo.lastLog.CacheReadCost, 1e-12)
	require.InDelta(t, 0.005, usageRepo.lastLog.OutputCost, 1e-12)
	require.InDelta(t, 0.0242, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.02662, usageRepo.lastLog.ActualCost, 1e-12)
}

func TestGPT6AstraStreamingUsageParsesCacheWriteTokens(t *testing.T) {
	usage := &OpenAIUsage{}
	(&OpenAIGatewayService{}).parseSSEUsage(`{
		"type":"response.completed",
		"response":{
			"id":"resp_astra_stream",
			"model":"gpt-6-astra",
			"service_tier":"flex",
			"usage":{
				"input_tokens":900,
				"input_tokens_details":{"cached_tokens":80,"cache_write_tokens":120},
				"output_tokens":40,
				"total_tokens":940
			}
		}
	}`, usage)

	require.Equal(t, 900, usage.InputTokens)
	require.Equal(t, 120, usage.CacheCreationInputTokens)
	require.Equal(t, 80, usage.CacheReadInputTokens)
	require.Equal(t, 40, usage.OutputTokens)
}

func TestGPT6AstraChatCompletionsUsageParsesCacheWriteTokens(t *testing.T) {
	body := []byte(`{
		"id":"chatcmpl_astra",
		"model":"gpt-6-astra",
		"usage":{
			"prompt_tokens":900,
			"prompt_tokens_details":{"cached_tokens":80,"cache_write_tokens":120},
			"completion_tokens":40,
			"total_tokens":940
		}
	}`)

	usage, ok := extractOpenAIUsageFromJSONBytes(body)
	require.True(t, ok)
	require.Equal(t, 900, usage.InputTokens)
	require.Equal(t, 120, usage.CacheCreationInputTokens)
	require.Equal(t, 80, usage.CacheReadInputTokens)
	require.Equal(t, 40, usage.OutputTokens)
}

func TestGPT6AstraRecordUsageAppliesOptedInLongContextPricing(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(
		usageRepo,
		&openAIRecordUsageUserRepoStub{},
		&openAIRecordUsageSubRepoStub{},
		nil,
	)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID: "resp_astra_long_context",
			Usage: OpenAIUsage{
				InputTokens:              272001,
				CacheCreationInputTokens: 100000,
				CacheReadInputTokens:     72001,
				OutputTokens:             10,
			},
			Model:    "gpt-6-astra",
			Duration: time.Second,
		},
		APIKey: openAIRecordUsageAPIKeyWithGroup(svc, 1061, true),
		User:   &User{ID: 2061},
		Account: &Account{
			ID:       3061,
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Extra: map[string]any{
				openAILongContextBillingEnabledKey: true,
			},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.True(t, usageRepo.lastLog.LongContextBillingApplied)
	require.Equal(t, 100000, usageRepo.lastLog.InputTokens)
	require.Equal(t, 100000, usageRepo.lastLog.CacheCreationTokens)
	require.Equal(t, 72001, usageRepo.lastLog.CacheReadTokens)
	require.InDelta(t, 2.0, usageRepo.lastLog.InputCost, 1e-12)
	require.InDelta(t, 2.5, usageRepo.lastLog.CacheCreationCost, 1e-12)
	require.InDelta(t, 0.144002, usageRepo.lastLog.CacheReadCost, 1e-12)
	require.InDelta(t, 0.00075, usageRepo.lastLog.OutputCost, 1e-12)
}

func assertGPT6AstraPricing(t *testing.T, pricing *ModelPricing) {
	t.Helper()
	require.NotNil(t, pricing)
	require.InDelta(t, 10e-6, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 20e-6, pricing.InputPricePerTokenPriority, 1e-12)
	require.InDelta(t, 1e-6, pricing.CacheReadPricePerToken, 1e-12)
	require.InDelta(t, 2e-6, pricing.CacheReadPricePerTokenPriority, 1e-12)
	require.InDelta(t, 12.5e-6, pricing.CacheCreationPricePerToken, 1e-12)
	require.InDelta(t, 25e-6, pricing.CacheCreationPricePerTokenPriority, 1e-12)
	require.InDelta(t, 50e-6, pricing.OutputPricePerToken, 1e-12)
	require.InDelta(t, 100e-6, pricing.OutputPricePerTokenPriority, 1e-12)
	require.Equal(t, 272000, pricing.LongContextInputThreshold)
	require.InDelta(t, 2.0, pricing.LongContextInputMultiplier, 1e-12)
	require.InDelta(t, 1.5, pricing.LongContextOutputMultiplier, 1e-12)
}
