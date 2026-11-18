package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestGPT62OfficialPricingAndContextBoundaries(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)
	data, err := (&PricingService{}).parsePricingData(body)
	require.NoError(t, err)
	for _, source := range []struct {
		name    string
		pricing *PricingService
	}{
		{"offline", nil},
		{"old remote", &PricingService{pricingData: map[string]*LiteLLMModelPricing{}}},
		{"bundled", &PricingService{pricingData: data}},
	} {
		billing := NewBillingService(&config.Config{}, source.pricing)
		for _, rate := range []struct {
			model                      string
			input, read, write, output float64
		}{
			{"gpt-6.2-astra", 8e-6, .4e-6, 10e-6, 40e-6},
			{"gpt-6.2-sol", 2e-6, .1e-6, 2.5e-6, 10e-6},
		} {
			for _, input := range []int{271999, 272000, 272001} {
				for _, tier := range []struct {
					name   string
					factor float64
				}{
					{"default", 1}, {"priority", 2}, {"fast", 2}, {"ultrafast", 3}, {"flex", 1},
				} {
					t.Run(fmt.Sprintf("%s/%s/%d/%s", source.name, rate.model, input, tier.name), func(t *testing.T) {
						require.True(t, billing.HasIdentifiedTokenPricing(rate.model))
						cost, err := billing.CalculateCostUnified(CostInput{
							Ctx: context.Background(), Model: rate.model, ServiceTier: tier.name,
							PricingAt:      time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC),
							Tokens:         UsageTokens{InputTokens: input - 120000, CacheReadTokens: 100000, CacheCreationTokens: 20000, OutputTokens: 100},
							RateMultiplier: 1.3,
						})
						require.NoError(t, err)
						inFactor, outFactor := tier.factor, tier.factor
						if input > 272000 {
							inFactor *= 2
							outFactor *= 1.5
						}
						require.InDelta(t, float64(input-120000)*rate.input*inFactor, cost.InputCost, 1e-12)
						require.InDelta(t, 100000*rate.read*inFactor, cost.CacheReadCost, 1e-12)
						require.InDelta(t, 20000*rate.write*inFactor, cost.CacheCreationCost, 1e-12)
						require.InDelta(t, 100*rate.output*outFactor, cost.OutputCost, 1e-12)
						require.InDelta(t, cost.TotalCost*1.3, cost.ActualCost, 1e-12)
						require.Equal(t, input > 272000, cost.LongContextBillingApplied)
					})
				}
			}
		}
	}
}

func TestGPT62UltrafastPromotionUsesRequestTime(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	cutoff := time.Date(2027, 3, 1, 8, 0, 0, 0, time.UTC)
	for _, resolver := range []*ModelPricingResolver{nil, NewModelPricingResolver(nil, billing)} {
		for _, item := range []struct {
			at   time.Time
			cost float64
		}{
			{cutoff.Add(-time.Nanosecond), .006}, {cutoff, .012},
		} {
			cost, err := billing.CalculateCostUnified(CostInput{
				Ctx: context.Background(), Model: "gpt-6.2-sol", ServiceTier: "ultrafast",
				PricingAt: item.at, Tokens: UsageTokens{InputTokens: 1000}, RateMultiplier: 1, Resolver: resolver,
			})
			require.NoError(t, err)
			require.InDelta(t, item.cost, cost.TotalCost, 1e-12)
		}
	}
}

func TestImage26HasDedicatedOfflinePricing(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	for _, model := range []string{"gpt-image-2.6-sunburst", "gpt-image-2.6-flare"} {
		require.True(t, billing.HasIdentifiedTokenPricing(model))
		cost, err := billing.CalculateCost(model, UsageTokens{InputTokens: 1500, ImageInputTokens: 1000, OutputTokens: 800, ImageOutputTokens: 800}, 2)
		require.NoError(t, err)
		require.InDelta(t, .0025, cost.InputCost, 1e-12)
		require.InDelta(t, .008, cost.ImageInputCost, 1e-12)
		require.InDelta(t, .024, cost.ImageOutputCost, 1e-12)
		require.Zero(t, cost.OutputCost)
		require.InDelta(t, .069, cost.ActualCost, 1e-12)
	}
}

func TestGPT62MixedCacheLifetimesAndCustomOverrides(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	for _, tier := range []struct {
		name       string
		multiplier float64
	}{
		{"default", 1}, {"priority", 2}, {"ultrafast", 3},
	} {
		for _, long := range []bool{false, true} {
			tokens := UsageTokens{InputTokens: 1000, CacheReadTokens: 4000, CacheCreationTokens: 7000,
				CacheCreation30mTokens: 3000, CacheCreation12hTokens: 4000, OutputTokens: 100}
			factor := tier.multiplier
			if long {
				tokens.InputTokens += 272000
				factor *= 2
			}
			cost, err := billing.CalculateCostUnified(CostInput{
				Ctx: context.Background(), Model: "gpt-6.2-sol", Tokens: tokens, RateMultiplier: 1,
				ServiceTier: tier.name, PricingAt: time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC),
				Resolver: NewModelPricingResolver(nil, billing),
			})
			require.NoError(t, err)
			require.InDelta(t, .0275*factor, cost.CacheCreationCost, 1e-12)
		}
	}
	for _, price := range []float64{0, 7e-6} {
		cost, err := billing.calculateCostInternal("gpt-6.2-sol", UsageTokens{
			CacheCreationTokens: 7000, CacheCreation30mTokens: 3000, CacheCreation12hTokens: 4000,
		}, 1, "priority", &ChannelModelPricing{CacheWritePrice: &price})
		require.NoError(t, err)
		require.InDelta(t, 7000*price, cost.CacheCreationCost, 1e-12, "explicit flat cache pricing covers both TTLs")
	}
}
