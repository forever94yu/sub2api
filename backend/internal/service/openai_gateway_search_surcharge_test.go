//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIGatewayServiceRecordUsage_GrokSearchSettlementMatchesLog(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, subscription := range []bool{false, true} {
			t.Run(fmt.Sprintf("legacy=%t/subscription=%t", legacy, subscription), func(t *testing.T) {
				usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
				userRepo := &openAIRecordUsageUserRepoStub{}
				subRepo := &claudeSettlementSubscriptionRepo{}
				quota := &openAIRecordUsageAPIKeyQuotaStub{}
				billingRepo := &openAIRecordUsageBillingRepoStub{}
				svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)
				if legacy {
					svc.usageBillingRepo = nil
				}
				svc.billingService = NewBillingService(svc.cfg, &PricingService{pricingData: map[string]*LiteLLMModelPricing{
					"grok-4.3": {InputCostPerToken: 1.25e-6},
				}})
				group := &Group{ID: 5, Platform: PlatformGrok, RateMultiplier: 0.3333}
				input := &OpenAIRecordUsageInput{
					Result: &OpenAIForwardResult{RequestID: "grok-search-exact-half", Model: "grok-4.3", Usage: OpenAIUsage{InputTokens: 40}, SearchCount: 9},
					APIKey: &APIKey{ID: 2, Quota: 100, RateLimit5h: 100, GroupID: &group.ID, Group: group},
					User:   &User{ID: 1}, Account: &Account{ID: 3, Platform: PlatformGrok}, APIKeyService: quota,
				}
				if subscription {
					group.SubscriptionType = SubscriptionTypeSubscription
					input.Subscription = &UserSubscription{ID: 4}
				}
				require.NoError(t, svc.RecordUsage(context.Background(), input))
				require.NotNil(t, usageRepo.lastLog)
				// (40 * 1.25 / 1e6 + 9 * 5 / 1000) * .3333 = .015015165.
				require.Equal(t, 0.01501517, usageRepo.lastLog.ActualCost)
				if legacy {
					require.Equal(t, 0.01501517, quota.lastAmount)
					if subscription {
						require.Equal(t, 0.01501517, subRepo.amount)
					} else {
						require.Equal(t, 0.01501517, userRepo.lastAmount)
					}
				} else {
					require.NotNil(t, billingRepo.lastCmd)
					require.Equal(t, 0.01501517, billingRepo.lastCmd.APIKeyQuotaCost)
					require.Equal(t, 0.01501517, billingRepo.lastCmd.APIKeyRateLimitCost)
					if subscription {
						require.Equal(t, 0.01501517, billingRepo.lastCmd.SubscriptionCost)
					} else {
						require.Equal(t, 0.01501517, billingRepo.lastCmd.BalanceCost)
					}
				}
			})
		}
	}
}

func TestCalculateOpenAIRecordUsageCost_SearchIsAdditiveToTokens(t *testing.T) {
	t.Parallel()

	price := 10.0 // $10 / 1k searches → 100 searches = $1.0
	svc := &OpenAIGatewayService{
		billingService: newTestBillingService(),
	}
	apiKey := &APIKey{
		Group: &Group{
			SearchPricePer1k: &price,
		},
	}

	// claude-sonnet-4 fallback: Input $3/MTok, Output $15/MTok
	// 1000 in + 500 out → 0.003 + 0.0075 = 0.0105
	// + 100 searches → +1.0 → total 1.0105
	cost, err := svc.calculateOpenAIRecordUsageCost(
		context.Background(),
		&OpenAIForwardResult{SearchCount: 100},
		apiKey,
		[]string{"claude-sonnet-4"},
		1.0,
		1.0,
		1.0,
		1.0,
		UsageTokens{InputTokens: 1000, OutputTokens: 500},
		"",
		boolPtr(false),
		time.Time{},
	)
	require.NoError(t, err)
	require.NotNil(t, cost)
	require.InDelta(t, 1.0105, cost.ActualCost, 1e-9)
	require.InDelta(t, 1.0105, cost.TotalCost, 1e-9)
}

func TestCalculateOpenAIRecordUsageCost_SearchOnlyWhenNoTokenPricing(t *testing.T) {
	t.Parallel()

	price := 10.0
	svc := &OpenAIGatewayService{
		billingService: newTestBillingService(),
	}
	apiKey := &APIKey{
		Group: &Group{SearchPricePer1k: &price},
	}
	// Empty model list: token path fails; search-only surcharge still bills.
	cost, err := svc.calculateOpenAIRecordUsageCost(
		context.Background(),
		&OpenAIForwardResult{SearchCount: 100},
		apiKey,
		nil,
		1.0,
		1.0,
		1.0,
		1.0,
		UsageTokens{},
		"",
		boolPtr(false),
		time.Time{},
	)
	require.NoError(t, err)
	require.NotNil(t, cost)
	require.InDelta(t, 1.0, cost.ActualCost, 1e-9)
}

func TestGroupMediaPricingLooksIncomplete_VideoModelPricesComplete(t *testing.T) {
	t.Parallel()
	require.True(t, groupMediaPricingLooksIncomplete(nil))
	require.True(t, groupMediaPricingLooksIncomplete(&Group{}))
	require.False(t, groupMediaPricingLooksIncomplete(&Group{
		VideoModelPrices: map[string]map[string]float64{
			"grok-imagine-video": {"720p": 0.1},
		},
	}))
	price := 10.0
	require.False(t, groupMediaPricingLooksIncomplete(&Group{SearchPricePer1k: &price}))
	require.False(t, groupMediaPricingLooksIncomplete(&Group{AudioRealtimePricePerMin: &price}))
	// Legacy video price alone still marks complete (existing path).
	require.False(t, groupMediaPricingLooksIncomplete(&Group{VideoPrice720P: &price}))
}

func TestCalculateOpenAIRecordUsageCost_TokenPricingErrorNotSwallowedBySearch(t *testing.T) {
	t.Parallel()

	price := 10.0
	svc := &OpenAIGatewayService{
		billingService: newTestBillingService(),
	}
	apiKey := &APIKey{
		Group: &Group{SearchPricePer1k: &price},
	}
	// Unknown model → token pricing fails; search must not replace that with $0/$search bill.
	cost, err := svc.calculateOpenAIRecordUsageCost(
		context.Background(),
		&OpenAIForwardResult{SearchCount: 100},
		apiKey,
		[]string{"totally-unknown-model-xyz-no-pricing"},
		1.0,
		1.0,
		1.0,
		1.0,
		UsageTokens{InputTokens: 1000, OutputTokens: 500},
		"",
		boolPtr(false),
		time.Time{},
	)
	require.Error(t, err)
	require.Nil(t, cost)
}
