//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestBillingFingerprintPreservesLegacyClaudeAmounts(t *testing.T) {
	for _, tt := range []struct {
		model               string
		input, output, read float64
		tokens              UsageTokens
		geo                 string
	}{
		{"claude-sonnet-4-6", 3, 15, 0.3, UsageTokens{InputTokens: 5, OutputTokens: 4}, "us"},
		{"claude-haiku-4-5", 1, 5, 0.1, UsageTokens{CacheReadTokens: 113, CacheCreationTokens: 46, CacheCreation5mTokens: 17, CacheCreation1hTokens: 29}, ""},
		{"claude-3-5-haiku", 0.8, 4, 0.08, UsageTokens{InputTokens: 31, OutputTokens: 7, CacheReadTokens: 113, CacheCreationTokens: 46, CacheCreation5mTokens: 17, CacheCreation1hTokens: 29}, ""},
		{"claude-3-haiku", 0.25, 1.25, 0.025, UsageTokens{InputTokens: 31, OutputTokens: 7, CacheReadTokens: 113, CacheCreationTokens: 46, CacheCreation5mTokens: 17, CacheCreation1hTokens: 29}, ""},
	} {
		t.Run(tt.model, func(t *testing.T) {
			billing := NewBillingService(&config.Config{}, nil)
			rate := 0.3333
			// These are the pre-upgrade fallback conversions and operation order.
			inputPrice, outputPrice, readPrice := tt.input/1e6, tt.output/1e6, tt.read/1e6
			fivePrice, hourPrice := tt.input*1.25/1e6, tt.input*2/1e6
			cacheCost := float64(tt.tokens.CacheCreation5mTokens)*fivePrice + float64(tt.tokens.CacheCreation1hTokens)*hourPrice
			legacyTotal := float64(tt.tokens.InputTokens)*inputPrice + float64(tt.tokens.OutputTokens)*outputPrice + cacheCost + float64(tt.tokens.CacheReadTokens)*readPrice
			legacyActual := legacyTotal * rate
			if tt.geo == "us" {
				legacyTotal *= 1.1
				legacyActual *= 1.1
				require.Equal(t, "0.0000274973", fmt.Sprintf("%.10f", legacyActual))
			}
			cost, err := billing.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: tt.model, Tokens: tt.tokens, RateMultiplier: rate, InferenceGeo: tt.geo})
			require.NoError(t, err)
			for _, subscription := range []bool{false, true} {
				p := &postUsageBillingParams{
					Cost: cost, User: &User{ID: 1}, APIKey: &APIKey{ID: 2, Quota: 100, RateLimit5h: 100},
					Account:               &Account{ID: 3, Type: AccountTypeAPIKey, Platform: PlatformAnthropic, Extra: map[string]any{"quota_limit": 100.0}},
					AccountRateMultiplier: 0.7501, APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{},
					Subscription: &UserSubscription{ID: 4}, IsSubscriptionBill: subscription,
				}
				usage := &UsageLog{Model: tt.model, InputTokens: tt.tokens.InputTokens, OutputTokens: tt.tokens.OutputTokens,
					CacheCreationTokens: tt.tokens.CacheCreationTokens, CacheReadTokens: tt.tokens.CacheReadTokens}
				legacy := &UsageBillingCommand{
					RequestID: "pre-upgrade-retry", UserID: 1, APIKeyID: 2, AccountID: 3, AccountType: AccountTypeAPIKey, Model: tt.model,
					InputTokens: tt.tokens.InputTokens, OutputTokens: tt.tokens.OutputTokens, CacheCreationTokens: tt.tokens.CacheCreationTokens, CacheReadTokens: tt.tokens.CacheReadTokens,
					APIKeyQuotaCost: legacyActual, APIKeyRateLimitCost: legacyActual, AccountQuotaCost: legacyTotal * p.AccountRateMultiplier,
				}
				if subscription {
					legacy.SubscriptionID = &p.Subscription.ID
					legacy.SubscriptionCost = legacyActual
				} else {
					legacy.BalanceCost = legacyActual
				}
				legacy.Normalize()
				current := buildUsageBillingCommand(legacy.RequestID, usage, p)
				require.Equal(t, legacy.RequestFingerprint, current.RequestFingerprint, "an upgrade must preserve the original request fingerprint")
			}
			require.Equal(t, legacyTotal, cost.TotalCost)
			require.Equal(t, legacyActual, cost.ActualCost)
		})
	}
}

func TestBillingSearchCombinationKeepsExactSettlement(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	cost, err := billing.CalculateCost("claude-haiku-5-5", UsageTokens{CacheCreationTokens: 400, CacheCreation5mTokens: 400}, 0.3333)
	require.NoError(t, err)
	search := billing.CalculateSearchCost(9, nil, 0.3333)
	addCostBreakdownTotals(cost, search)
	// (400 * .125 / 1e6 + 9 * 5 / 1000) * .3333 = .015015165.
	require.Equal(t, 0.01501517, cost.settledActualCost())
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
			usage := &UsageLog{Model: "claude-haiku-5-5", ActualCost: cost.ActualCost, TotalCost: cost.TotalCost}
			billingRepo := &openAIRecordUsageBillingRepoStub{}
			var repo UsageBillingRepository = billingRepo
			if legacy {
				repo = nil
			}
			applied, err := applyUsageBilling(context.Background(), "search-precision", usage, p, &billingDeps{userRepo: userRepo, userSubRepo: subRepo}, repo)
			require.NoError(t, err)
			require.True(t, applied)
			require.Equal(t, 0.01501517, usage.ActualCost)
			if legacy {
				require.Equal(t, 0.01501517, quota.lastAmount)
				if subscription {
					require.Equal(t, 0.01501517, subRepo.amount)
				} else {
					require.Equal(t, 0.01501517, userRepo.lastAmount)
				}
			} else {
				require.Equal(t, 0.01501517, billingRepo.lastCmd.APIKeyQuotaCost)
				require.Equal(t, 0.01501517, billingRepo.lastCmd.APIKeyRateLimitCost)
				if subscription {
					require.Equal(t, 0.01501517, billingRepo.lastCmd.SubscriptionCost)
				} else {
					require.Equal(t, 0.01501517, billingRepo.lastCmd.BalanceCost)
				}
			}
		}
	}
}

func TestBillingLegacyLongContextPreservesFloatOrder(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	for _, rate := range []float64{0.3333, 0.7501} {
		for _, extra := range []float64{1.1, 1.0001} {
			inputPrice, outputPrice, readPrice := 3e-6, 15e-6, 0.3e-6
			inRangeTotal := inputPrice + 4*outputPrice + 22*readPrice
			outRangeTotal := 99 * inputPrice
			legacyTotal := inRangeTotal + outRangeTotal
			legacyActual := inRangeTotal*rate + outRangeTotal*(rate*extra)
			cost, err := billing.CalculateCostWithLongContext("claude-sonnet-4-6", UsageTokens{InputTokens: 100, OutputTokens: 4, CacheReadTokens: 22}, rate, 23, extra)
			require.NoError(t, err)
			require.Equal(t, legacyTotal, cost.TotalCost)
			require.Equal(t, legacyActual, cost.ActualCost)
		}
	}
}
