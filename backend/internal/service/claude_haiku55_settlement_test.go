//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestClaudeHaiku55SettlementDecimalArithmetic(t *testing.T) {
	for _, tt := range []struct {
		name       string
		tokens     UsageTokens
		rate       float64
		geo        string
		channel    float64
		raw, debit string
	}{
		{"long 5m exact half", UsageTokens{CacheCreationTokens: 100001, CacheCreation5mTokens: 100001, OutputTokens: 100}, 1, "", 0, "0.062750625", "0.06275063"},
		{"long 5m group rate", UsageTokens{CacheCreationTokens: 100001, CacheCreation5mTokens: 100001, OutputTokens: 100}, 0.3333, "", 0, "0.0209147833125", "0.02091478"},
		{"long 5m geography", UsageTokens{CacheCreationTokens: 100001, CacheCreation5mTokens: 100001, OutputTokens: 100}, 1, "us", 0, "0.0690256875", "0.06902569"},
		{"long 5m group and geography", UsageTokens{CacheCreationTokens: 100001, CacheCreation5mTokens: 100001, OutputTokens: 100}, 0.3333, "us", 0, "0.02300626164375", "0.02300626"},
		{"long 5m channel group geography", UsageTokens{CacheCreationTokens: 100001, CacheCreation5mTokens: 100001, OutputTokens: 100}, 1.25, "us", 1.1, "0.0949103203125", "0.09491032"},
		{"short 5m exact half", UsageTokens{CacheCreationTokens: 1, CacheCreation5mTokens: 1}, 1, "", 0, "0.000000125", "0.00000013"},
		{"long 1h group rate", UsageTokens{CacheCreationTokens: 100001, CacheCreation1hTokens: 100001, OutputTokens: 100}, 0.3333, "", 0, "0.0334136583", "0.03341366"},
		{"mixed cache exact half", UsageTokens{InputTokens: 99998, OutputTokens: 100, CacheCreationTokens: 3, CacheCreation5mTokens: 1, CacheCreation1hTokens: 2}, 1, "", 0, "0.050251625", "0.05025163"},
		{"tiny double rounding", UsageTokens{CacheReadTokens: 1, CacheCreationTokens: 1, CacheCreation5mTokens: 1}, 0.3333, "", 0, "0.0000000449955", "0.00000004"},
		{"below half", UsageTokens{CacheReadTokens: 1}, 0.499999, "", 0, "0.00000000499999", "0"},
		{"exact half", UsageTokens{CacheReadTokens: 1}, 0.5, "", 0, "0.000000005", "0.00000001"},
		{"above half", UsageTokens{CacheReadTokens: 1}, 0.500001, "", 0, "0.00000000500001", "0.00000001"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			billing := NewBillingService(&config.Config{}, nil)
			input := CostInput{Ctx: context.Background(), Model: "claude-haiku-5-5", Tokens: tt.tokens, RateMultiplier: tt.rate, InferenceGeo: tt.geo}
			if tt.channel != 0 {
				pricing, err := billing.GetModelPricing(input.Model)
				require.NoError(t, err)
				input.Resolver = NewModelPricingResolver(nil, billing)
				input.Resolved = channelTimeResolvedForTest(pricing, nil)
				input.Resolved.channelPricing.TimePricing.Periods[0].Multiplier = tt.channel
				input.PricingAt = time.Date(2026, 10, 8, 1, 0, 0, 0, time.UTC)
			}
			cost, err := billing.CalculateCostUnified(input)
			require.NoError(t, err)
			want := decimal.RequireFromString(tt.debit).InexactFloat64()
			for _, subscription := range []bool{false, true} {
				for _, legacy := range []bool{false, true} {
					quota := &openAIRecordUsageAPIKeyQuotaStub{}
					userRepo := &openAIRecordUsageUserRepoStub{}
					subRepo := &claudeSettlementSubscriptionRepo{}
					p := &postUsageBillingParams{
						Cost: cost, User: &User{ID: 1}, APIKey: &APIKey{ID: 2, Quota: 100, RateLimit5h: 100},
						Account: &Account{ID: 3, Platform: PlatformAnthropic}, APIKeyService: quota,
						Subscription: &UserSubscription{ID: 4}, IsSubscriptionBill: subscription,
					}
					usage := &UsageLog{Model: input.Model, TotalCost: cost.TotalCost, ActualCost: cost.ActualCost}
					billingRepo := &openAIRecordUsageBillingRepoStub{}
					var repo UsageBillingRepository = billingRepo
					if legacy {
						repo = nil
					}
					applied, err := applyUsageBilling(context.Background(), tt.name, usage, p, &billingDeps{userRepo: userRepo, userSubRepo: subRepo}, repo)
					require.NoError(t, err)
					require.True(t, applied)
					require.Equal(t, want, usage.ActualCost, "usage log: subscription=%t legacy=%t", subscription, legacy)
					if legacy {
						require.Equal(t, want, quota.lastAmount)
						if subscription {
							require.Equal(t, want, subRepo.amount)
						} else {
							require.Equal(t, want, userRepo.lastAmount)
						}
					} else {
						require.Equal(t, want, billingRepo.lastCmd.APIKeyQuotaCost)
						require.Equal(t, want, billingRepo.lastCmd.APIKeyRateLimitCost)
						if subscription {
							require.Equal(t, want, billingRepo.lastCmd.SubscriptionCost)
						} else {
							require.Equal(t, want, billingRepo.lastCmd.BalanceCost)
						}
					}
				}
			}
			amount := cost.actualBillingAmount()
			require.NotNil(t, amount.precise)
			require.True(t, decimal.RequireFromString(tt.raw).Equal(*amount.precise), "exact cost remains unrounded: %s", amount.precise)
		})
	}
}
