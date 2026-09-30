//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type claudeSettlementSubscriptionRepo struct {
	UserSubscriptionRepository
	amount float64
	calls  int
}

func (r *claudeSettlementSubscriptionRepo) IncrementUsage(_ context.Context, _ int64, amount float64) error {
	r.amount = amount
	r.calls++
	return nil
}

func TestClaudeSettlementRoundsEveryBillingSink(t *testing.T) {
	for _, amount := range []struct {
		name      string
		raw, want float64
	}{
		{"below double rounding boundary", 0.00001696497, 0.00001696},
		{"exact half", 0.000078125, 0.00007813},
		{"rounds to zero", 0.000000002, 0},
	} {
		for _, platform := range []string{PlatformAnthropic, PlatformOpenAI, PlatformGemini} {
			for _, legacy := range []bool{false, true} {
				for _, subscription := range []bool{false, true} {
					name := amount.name + "/" + platform
					if legacy {
						name += "/legacy"
					} else {
						name += "/atomic"
					}
					if subscription {
						name += "/subscription"
					} else {
						name += "/balance"
					}
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
							Account:            &Account{ID: 3, Platform: platform},
							Subscription:       &UserSubscription{ID: 4},
							IsSubscriptionBill: subscription,
							APIKeyService:      quota,
						}
						log := &UsageLog{Model: "claude-sonnet-5-5", ActualCost: amount.raw, TotalCost: p.Cost.TotalCost}
						originalCost := *p.Cost
						rawCommand := buildUsageBillingCommand(name, log, p)
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
						require.Equal(t, originalCost, *p.Cost, "the original amount must remain available for retry fingerprints")
						want := amount.raw
						if platform == PlatformAnthropic || platform == PlatformOpenAI {
							want = amount.want
						}
						require.Equal(t, want, log.ActualCost)
						require.Equal(t, originalCost.TotalCost, log.TotalCost)
						if legacy {
							require.Equal(t, want, quota.lastAmount)
							if subscription {
								require.Equal(t, want, subRepo.amount)
							} else {
								require.Equal(t, want, userRepo.lastAmount)
							}
							if want == 0 {
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
							if want == 0 {
								require.Empty(t, cache.cacheWriteChan)
							} else {
								require.Len(t, cache.cacheWriteChan, 2)
								for len(cache.cacheWriteChan) > 0 {
									require.Equal(t, want, (<-cache.cacheWriteChan).amount, "queued cache amount must equal the settled debit")
								}
							}
						}
					})
				}
			}
		}
	}
}

func TestClaudeSettlementRecordUsageStoresDebit(t *testing.T) {
	for _, longContext := range []bool{false, true} {
		for _, tt := range []struct {
			name            string
			read, write     int
			rate, want      float64
			billingFailure  bool
			logWriteFailure bool
		}{
			{name: "double rounding boundary", read: 242, write: 1, rate: 0.3333, want: 0.00001696},
			{name: "free after rounding", read: 1, rate: 0.01},
			{name: "billing failure", read: 242, write: 1, rate: 0.3333, billingFailure: true},
			{name: "log write failure retains debit", read: 242, write: 1, rate: 0.3333, want: 0.00001696, logWriteFailure: true},
		} {
			name := tt.name
			if longContext {
				name += "/long context entry"
			}
			t.Run(name, func(t *testing.T) {
				usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
				billingRepo := &openAIRecordUsageBillingRepoStub{}
				failure := errors.New("settlement fixture failure")
				if tt.billingFailure {
					billingRepo.err = failure
				}
				if tt.logWriteFailure {
					usageRepo.err = failure
				}
				svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, nil, nil)
				svc.cfg.Default.RateMultiplier = tt.rate
				input := &RecordUsageInput{
					Result: &ForwardResult{RequestID: name, Model: "claude-sonnet-5-5", Usage: ClaudeUsage{
						CacheReadInputTokens: tt.read, CacheCreationInputTokens: tt.write, CacheCreation5mTokens: tt.write,
					}},
					APIKey: &APIKey{ID: 2, Quota: 100}, User: &User{ID: 1}, Account: &Account{ID: 3, Platform: PlatformAnthropic},
					APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{},
				}
				var err error
				if longContext {
					err = svc.RecordUsageWithLongContext(context.Background(), &RecordUsageLongContextInput{
						Result: input.Result, APIKey: input.APIKey, User: input.User, Account: input.Account, APIKeyService: input.APIKeyService,
					})
				} else {
					err = svc.RecordUsage(context.Background(), input)
				}
				if tt.billingFailure {
					require.ErrorIs(t, err, failure)
				} else {
					require.NoError(t, err)
					require.Equal(t, tt.want, billingRepo.lastCmd.BalanceCost)
					require.Equal(t, tt.want, billingRepo.lastCmd.APIKeyQuotaCost)
				}
				require.Equal(t, 1, usageRepo.calls, "zero debit still retains token usage")
				require.Equal(t, tt.read, usageRepo.lastLog.CacheReadTokens)
				require.Equal(t, tt.want, usageRepo.lastLog.ActualCost)
				if tt.read == 242 {
					require.Equal(t, "0.0000509000", decimal.NewFromFloat(usageRepo.lastLog.TotalCost).StringFixed(10))
					require.Equal(t, "0.0000484000", decimal.NewFromFloat(usageRepo.lastLog.CacheReadCost).StringFixed(10))
					require.Equal(t, "0.0000025000", decimal.NewFromFloat(usageRepo.lastLog.CacheCreationCost).StringFixed(10))
				}
			})
		}
	}
}
