package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type liveBillingAPIKeyRepo struct {
	APIKeyRepository
	key        *APIKey
	err        error
	reads      int
	quotaCosts []float64
	rateCosts  []float64
}

func (r *liveBillingAPIKeyRepo) GetByID(context.Context, int64) (*APIKey, error) {
	r.reads++
	if r.err != nil {
		return nil, r.err
	}
	if r.key == nil {
		return nil, ErrAPIKeyNotFound
	}
	copy := *r.key
	return &copy, nil
}

func (r *liveBillingAPIKeyRepo) IncrementQuotaUsed(_ context.Context, _ int64, amount float64) (float64, error) {
	r.quotaCosts = append(r.quotaCosts, amount)
	return amount, nil
}

func (r *liveBillingAPIKeyRepo) IncrementRateLimitUsage(_ context.Context, _ int64, amount float64) error {
	r.rateCosts = append(r.rateCosts, amount)
	return nil
}

type liveBillingAuthCache struct {
	APIKeyCache
	deleted   []string
	published []string
}

func (c *liveBillingAuthCache) DeleteAuthCache(_ context.Context, key string) error {
	c.deleted = append(c.deleted, key)
	return nil
}

func (c *liveBillingAuthCache) PublishAuthCacheInvalidation(_ context.Context, key string) error {
	c.published = append(c.published, key)
	return nil
}

type liveBillingExhaustedRepo struct {
	liveTestBillingRepo
}

func (r *liveBillingExhaustedRepo) Apply(ctx context.Context, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	result, err := r.liveTestBillingRepo.Apply(ctx, cmd)
	result.APIKeyQuotaExhausted = true
	return result, err
}

func TestApplyLiveUsageBillingPreservesKeyMetering(t *testing.T) {
	capped := &LiveAPIKeyBillingSnapshot{Quota: 10, RateLimit5h: 2, RateLimit1d: 3, RateLimit7d: 4}
	for _, tc := range []struct {
		name      string
		snapshot  *LiveAPIKeyBillingSnapshot
		current   *APIKey
		lookupErr error
		wantQuota bool
		wantRate  bool
	}{
		{name: "capped", snapshot: capped, current: &APIKey{Key: "current-key"}, wantQuota: true, wantRate: true},
		{name: "quota_only", snapshot: &LiveAPIKeyBillingSnapshot{Quota: 10}, wantQuota: true},
		{name: "5h_only", snapshot: &LiveAPIKeyBillingSnapshot{RateLimit5h: 2}, wantRate: true},
		{name: "1d_only", snapshot: &LiveAPIKeyBillingSnapshot{RateLimit1d: 3}, wantRate: true},
		{name: "7d_only", snapshot: &LiveAPIKeyBillingSnapshot{RateLimit7d: 4}, wantRate: true},
		{name: "unlimited", snapshot: &LiveAPIKeyBillingSnapshot{}},
		{name: "unlimited_lookup_failure", snapshot: &LiveAPIKeyBillingSnapshot{}, lookupErr: errors.New("database unavailable")},
		{name: "deleted_after_create", snapshot: capped, lookupErr: ErrAPIKeyNotFound, wantQuota: true, wantRate: true},
		{name: "lookup_failure", snapshot: capped, lookupErr: errors.New("database unavailable"), wantQuota: true, wantRate: true},
		{name: "legacy_capped", current: &APIKey{Quota: 10, RateLimit7d: 4}, wantQuota: true, wantRate: true},
		{name: "legacy_unlimited", current: &APIKey{}},
		{name: "legacy_deleted", lookupErr: ErrAPIKeyNotFound, wantQuota: true, wantRate: true},
		{name: "legacy_lookup_failure", lookupErr: errors.New("database unavailable"), wantQuota: true, wantRate: true},
	} {
		for _, subscription := range []bool{false, true} {
			mode := "balance"
			if subscription {
				mode = "subscription"
			}
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				keyRepo := &liveBillingAPIKeyRepo{key: tc.current, err: tc.lookupErr}
				billingRepo := &liveTestBillingRepo{}
				svc := &OpenAIGatewayService{
					cfg:              &config.Config{},
					apiKeyService:    &APIKeyService{apiKeyRepo: keyRepo},
					usageBillingRepo: billingRepo,
				}
				record := &LiveCallRecord{
					CallHash: "live-billing", APIKeyID: 22, UserID: 33, GroupID: 44,
					AccountID: 11, AccountType: AccountTypeOAuth, Platform: PlatformOpenAI,
					APIKeyBilling: tc.snapshot,
				}
				if subscription {
					record.SubscriptionID = 55
				}
				usageLog := &UsageLog{Model: "gpt-live", BillingType: BillingTypeBalance}
				if subscription {
					usageLog.BillingType = BillingTypeSubscription
				}
				cost := &CostBreakdown{TotalCost: 0.25, ActualCost: 0.5}
				require.NoError(t, svc.applyLiveUsageBilling(context.Background(), record, usageLog, cost))
				require.Len(t, billingRepo.commands, 1)
				cmd := billingRepo.commands[0]
				require.Equal(t, record.CallHash, cmd.RequestID)
				require.Equal(t, record.UserID, cmd.UserID)
				require.Equal(t, record.APIKeyID, cmd.APIKeyID)
				if subscription {
					require.Equal(t, &record.SubscriptionID, cmd.SubscriptionID)
					require.Equal(t, cost.ActualCost, cmd.SubscriptionCost)
					require.Zero(t, cmd.BalanceCost)
				} else {
					require.Nil(t, cmd.SubscriptionID)
					require.Equal(t, cost.ActualCost, cmd.BalanceCost)
					require.Zero(t, cmd.SubscriptionCost)
				}
				if tc.wantQuota {
					require.Equal(t, cost.ActualCost, cmd.APIKeyQuotaCost)
				} else {
					require.Zero(t, cmd.APIKeyQuotaCost)
				}
				if tc.wantRate {
					require.Equal(t, cost.ActualCost, cmd.APIKeyRateLimitCost)
				} else {
					require.Zero(t, cmd.APIKeyRateLimitCost)
				}
			})
		}
	}
}

func TestApplyLiveUsageBillingUsesOriginalIdentityAfterKeyRebind(t *testing.T) {
	reboundGroup := int64(99)
	keyRepo := &liveBillingAPIKeyRepo{key: &APIKey{
		ID: 22, UserID: 88, GroupID: &reboundGroup, Status: StatusAPIKeyDisabled, Key: "current-key",
		User: &User{ID: 88}, Group: &Group{ID: reboundGroup},
	}}
	cache := &liveBillingAuthCache{}
	keyService := &APIKeyService{apiKeyRepo: keyRepo, cache: cache}
	billingRepo := &liveBillingExhaustedRepo{}
	svc := &OpenAIGatewayService{
		cfg: &config.Config{}, apiKeyService: keyService, usageBillingRepo: billingRepo,
	}
	record := &LiveCallRecord{
		CallHash: "original-live", APIKeyID: 22, UserID: 33, GroupID: 44, SubscriptionID: 55,
		AccountID: 11, AccountType: AccountTypeOAuth, Platform: PlatformOpenAI,
		APIKeyBilling: &LiveAPIKeyBillingSnapshot{Quota: 10, RateLimit5h: 2},
	}
	apiKey, force := svc.liveBillingAPIKey(context.Background(), record, &User{ID: record.UserID})
	require.False(t, force)
	require.Equal(t, record.UserID, apiKey.UserID)
	require.Equal(t, &record.GroupID, apiKey.GroupID)
	require.Equal(t, record.GroupID, apiKey.Group.ID)
	require.Equal(t, record.Platform, apiKey.Group.Platform)
	require.Equal(t, record.APIKeyBilling.Quota, apiKey.Quota)
	require.NoError(t, svc.applyLiveUsageBilling(context.Background(), record, &UsageLog{Model: "gpt-live"}, &CostBreakdown{TotalCost: 1, ActualCost: 0.5}))
	require.Len(t, billingRepo.commands, 1)
	require.Equal(t, record.UserID, billingRepo.commands[0].UserID)
	require.Equal(t, &record.SubscriptionID, billingRepo.commands[0].SubscriptionID)
	require.Equal(t, 0.5, billingRepo.commands[0].APIKeyQuotaCost)
	require.Equal(t, []string{keyService.authCacheKey("current-key")}, cache.deleted)
	require.Equal(t, cache.deleted, cache.published)
}

type liveBillingUserRepo struct {
	UserRepository
	deducted float64
}

func (r *liveBillingUserRepo) DeductBalance(_ context.Context, _ int64, amount float64) error {
	r.deducted += amount
	return nil
}

func TestApplyLiveUsageBillingLegacyRepositoryUsesRealKeyUpdater(t *testing.T) {
	keyRepo := &liveBillingAPIKeyRepo{key: &APIKey{ID: 22, Quota: 10, RateLimit5h: 2}}
	userRepo := &liveBillingUserRepo{}
	svc := &OpenAIGatewayService{
		cfg: &config.Config{}, userRepo: userRepo, apiKeyService: &APIKeyService{apiKeyRepo: keyRepo},
	}
	record := &LiveCallRecord{
		CallHash: "legacy-repo", APIKeyID: 22, UserID: 33, AccountID: 11, Platform: PlatformOpenAI,
		APIKeyBilling: &LiveAPIKeyBillingSnapshot{Quota: 10, RateLimit5h: 2},
	}
	require.NoError(t, svc.applyLiveUsageBilling(context.Background(), record, &UsageLog{}, &CostBreakdown{TotalCost: 1, ActualCost: 0.5}))
	require.Equal(t, 0.5, userRepo.deducted)
	require.Equal(t, []float64{0.5}, keyRepo.quotaCosts)
	require.Equal(t, []float64{0.5}, keyRepo.rateCosts)
}

func TestApplyLiveUsageBillingSkipsSimpleModeAndFreeCalls(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode string
		cost float64
	}{
		{name: "simple", mode: config.RunModeSimple, cost: 1},
		{name: "free", cost: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyRepo := &liveBillingAPIKeyRepo{err: errors.New("must not look up")}
			billingRepo := &liveTestBillingRepo{}
			svc := &OpenAIGatewayService{
				cfg: &config.Config{RunMode: tc.mode}, apiKeyService: &APIKeyService{apiKeyRepo: keyRepo},
				usageBillingRepo: billingRepo,
			}
			require.NoError(t, svc.applyLiveUsageBilling(context.Background(), &LiveCallRecord{}, &UsageLog{}, &CostBreakdown{ActualCost: tc.cost}))
			require.Zero(t, keyRepo.reads)
			require.Empty(t, billingRepo.commands)
		})
	}
}
