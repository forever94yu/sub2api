//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestGPT61SolSettlementPreservesRawFingerprintAndStoresDebit(t *testing.T) {
	for _, amount := range []struct {
		name      string
		raw, want float64
	}{
		{name: "double rounding boundary", raw: 0.00001696497, want: 0.00001696},
		{name: "exact half", raw: 0.000078125, want: 0.00007813},
		{name: "rounds to zero", raw: 0.000000002, want: 0},
	} {
		for _, legacy := range []bool{false, true} {
			for _, subscription := range []bool{false, true} {
				name := fmt.Sprintf("%s/legacy=%t/subscription=%t", amount.name, legacy, subscription)
				t.Run(name, func(t *testing.T) {
					groupID := int64(5)
					userRepo := &openAIRecordUsageUserRepoStub{}
					subRepo := &claudeSettlementSubscriptionRepo{}
					quota := &openAIRecordUsageAPIKeyQuotaStub{}
					cache := &BillingCacheService{cache: &billingCacheWorkerStub{}, cacheWriteChan: make(chan cacheWriteTask, 4)}
					p := &postUsageBillingParams{
						Cost:               &CostBreakdown{TotalCost: 0.0000509, ActualCost: amount.raw},
						User:               &User{ID: 1},
						APIKey:             &APIKey{ID: 2, Quota: 100, RateLimit5h: 100, GroupID: &groupID},
						Account:            &Account{ID: 3, Platform: PlatformOpenAI},
						Subscription:       &UserSubscription{ID: 4},
						IsSubscriptionBill: subscription,
						APIKeyService:      quota,
					}
					log := &UsageLog{Model: "gpt-6.1-sol", ActualCost: amount.raw, TotalCost: p.Cost.TotalCost}
					originalCost := *p.Cost
					rawCommand := buildUsageBillingCommand(name, log, p)
					settledCost := originalCost
					settledCost.ActualCost = amount.want
					settledParams := *p
					settledParams.Cost = &settledCost
					settledCommand := buildUsageBillingCommand(name, log, &settledParams)
					require.NotEqual(t, rawCommand.RequestFingerprint, settledCommand.RequestFingerprint,
						"this fixture must distinguish raw and prematurely rounded fingerprints")
					billingRepo := &openAIRecordUsageBillingRepoStub{}
					var repo UsageBillingRepository = billingRepo
					if legacy {
						repo = nil
					}
					applied, err := applyUsageBilling(context.Background(), name, log, p, &billingDeps{
						userRepo: userRepo, userSubRepo: subRepo, billingCacheService: cache,
					}, repo)
					require.NoError(t, err)
					require.True(t, applied)
					require.Equal(t, originalCost, *p.Cost, "retain raw input for retry fingerprints")
					require.Equal(t, originalCost.TotalCost, log.TotalCost, "base and component prices are unchanged")
					require.Equal(t, amount.want, log.ActualCost, "usage log must store the exact settled debit")
					if legacy {
						require.Equal(t, amount.want, quota.lastAmount)
						if subscription {
							require.Equal(t, amount.want, subRepo.amount)
						} else {
							require.Equal(t, amount.want, userRepo.lastAmount)
						}
						if amount.want == 0 {
							require.Zero(t, quota.quotaCalls)
							require.Zero(t, quota.rateLimitCalls)
							require.Zero(t, userRepo.deductCalls)
							require.Zero(t, subRepo.calls)
						}
					} else {
						require.Equal(t, rawCommand.RequestFingerprint, billingRepo.lastCmd.RequestFingerprint)
						require.Equal(t, amount.want, billingRepo.lastCmd.APIKeyQuotaCost)
						require.Equal(t, amount.want, billingRepo.lastCmd.APIKeyRateLimitCost)
						if subscription {
							require.Equal(t, amount.want, billingRepo.lastCmd.SubscriptionCost)
						} else {
							require.Equal(t, amount.want, billingRepo.lastCmd.BalanceCost)
						}
						if amount.want == 0 {
							require.Empty(t, cache.cacheWriteChan)
						} else {
							require.Len(t, cache.cacheWriteChan, 2)
							for len(cache.cacheWriteChan) > 0 {
								require.Equal(t, amount.want, (<-cache.cacheWriteChan).amount)
							}
						}
					}
				})
			}
		}
	}
}

func TestGPT61SolRecordUsageStoresSettledDebit(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tt := range []struct {
			name            string
			read, write     int
			rate, want      float64
			billingFailure  bool
			logWriteFailure bool
		}{
			{name: "double rounding boundary", read: 484, write: 1, rate: 0.3333, want: 0.00001696},
			{name: "nonzero usage rounds to zero", read: 1, rate: 0.01},
			{name: "billing failure", read: 484, write: 1, rate: 0.3333, billingFailure: true},
			{name: "log failure keeps settled debit", read: 484, write: 1, rate: 0.3333, want: 0.00001696, logWriteFailure: true},
		} {
			t.Run(fmt.Sprintf("%s/stream=%t", tt.name, stream), func(t *testing.T) {
				usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
				billingRepo := &openAIRecordUsageBillingRepoStub{}
				failure := errors.New("GPT-6.1 settlement fixture failure")
				if tt.billingFailure {
					billingRepo.err = failure
				}
				if tt.logWriteFailure {
					usageRepo.err = failure
				}
				svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, nil, nil, nil)
				svc.cfg.Default.RateMultiplier = tt.rate
				err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
					Result: &OpenAIForwardResult{
						RequestID: tt.name, Model: "gpt-6.1-sol", UpstreamModel: "gpt-6.1-sol", Stream: stream,
						Usage: OpenAIUsage{InputTokens: tt.read + tt.write, CacheReadInputTokens: tt.read, CacheCreationInputTokens: tt.write},
					},
					APIKey: &APIKey{ID: 2, Quota: 100}, User: &User{ID: 1}, Account: &Account{ID: 3, Platform: PlatformOpenAI},
					APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{},
				})
				if tt.billingFailure {
					require.ErrorIs(t, err, failure)
				} else {
					require.NoError(t, err)
					require.Equal(t, tt.want, billingRepo.lastCmd.BalanceCost)
					require.Equal(t, tt.want, billingRepo.lastCmd.APIKeyQuotaCost)
				}
				require.Equal(t, 1, usageRepo.calls, "zero debit still retains usage")
				require.Equal(t, 0, usageRepo.lastLog.InputTokens)
				require.Equal(t, tt.read, usageRepo.lastLog.CacheReadTokens)
				require.Equal(t, tt.write, usageRepo.lastLog.CacheCreationTokens)
				require.Equal(t, tt.want, usageRepo.lastLog.ActualCost)
				if tt.read == 484 {
					require.Equal(t, "0.0000509000", decimal.NewFromFloat(usageRepo.lastLog.TotalCost).StringFixed(10))
					require.Equal(t, "0.0000484000", decimal.NewFromFloat(usageRepo.lastLog.CacheReadCost).StringFixed(10))
					require.Equal(t, "0.0000025000", decimal.NewFromFloat(usageRepo.lastLog.CacheCreationCost).StringFixed(10))
				}
			})
		}
	}
}
