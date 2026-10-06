package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

type wsBillingKeyRepo struct {
	APIKeyRepository
	key *APIKey
	err error
}

func (r *wsBillingKeyRepo) GetByKey(context.Context, string) (*APIKey, error) {
	return r.key, r.err
}

func healthyWSBillingKey() *APIKey {
	groupID := int64(3)
	return &APIKey{
		ID: 1, UserID: 2, Key: "test-key", Status: StatusActive, GroupID: &groupID,
		User:  &User{ID: 2, Status: StatusActive, Balance: 10},
		Group: &Group{ID: groupID, Status: StatusActive, Platform: PlatformOpenAI},
	}
}

func TestRefreshWebSocketAPIKey(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*APIKey)
		want   error
	}{
		{"healthy", func(*APIKey) {}, nil},
		{"quota exhausted", func(k *APIKey) { k.Quota, k.QuotaUsed = 1, 1 }, ErrAPIKeyQuotaExhausted},
		{"revoked", func(k *APIKey) { k.Status = StatusAPIKeyDisabled }, ErrWebSocketAuthentication},
		{"expired", func(k *APIKey) { past := time.Now().Add(-time.Second); k.ExpiresAt = &past }, ErrAPIKeyExpired},
		{"quota status", func(k *APIKey) { k.Status = StatusAPIKeyQuotaExhausted }, ErrAPIKeyQuotaExhausted},
		{"user disabled", func(k *APIKey) { k.User.Status = StatusDisabled }, ErrWebSocketAuthentication},
		{"group disabled", func(k *APIKey) { k.Group.Status = StatusDisabled }, ErrWebSocketAuthentication},
		{"group changed", func(k *APIKey) { id := int64(4); k.GroupID = &id }, ErrWebSocketAuthentication},
		{"exclusive permission removed", func(k *APIKey) { k.Group.IsExclusive = true }, ErrWebSocketAuthentication},
		{"exclusive permission retained", func(k *APIKey) { k.Group.IsExclusive = true; k.User.AllowedGroups = []int64{3} }, nil},
		{"ip denied", func(k *APIKey) { k.IPWhitelist = []string{"192.0.2.1"} }, ErrWebSocketAuthentication},
	} {
		t.Run(tt.name, func(t *testing.T) {
			initial, fresh := healthyWSBillingKey(), healthyWSBillingKey()
			tt.mutate(fresh)
			svc := &APIKeyService{apiKeyRepo: &wsBillingKeyRepo{key: fresh}, cfg: &config.Config{}}
			got, err := svc.RefreshWebSocketAPIKey(context.Background(), initial, "127.0.0.1")
			if tt.want != nil {
				require.ErrorIs(t, err, tt.want)
				require.Nil(t, got)
			} else {
				require.NoError(t, err)
				require.Same(t, fresh, got)
			}
		})
	}
}

func TestRefreshWebSocketAPIKeySimpleModeStillAuthenticates(t *testing.T) {
	initial, fresh := healthyWSBillingKey(), healthyWSBillingKey()
	past := time.Now().Add(-time.Second)
	fresh.ExpiresAt, fresh.Status = &past, StatusAPIKeyQuotaExhausted
	fresh.Quota, fresh.QuotaUsed, fresh.User.Balance = 1, 2, 0
	svc := &APIKeyService{apiKeyRepo: &wsBillingKeyRepo{key: fresh}, cfg: &config.Config{RunMode: config.RunModeSimple}}
	_, err := svc.RefreshWebSocketAPIKey(context.Background(), initial, "127.0.0.1")
	require.NoError(t, err)
	fresh.Status = StatusAPIKeyDisabled
	_, err = svc.RefreshWebSocketAPIKey(context.Background(), initial, "127.0.0.1")
	require.ErrorIs(t, err, ErrWebSocketAuthentication)
}

type wsBillingSubRepo struct {
	UserSubscriptionRepository
	sub             *UserSubscription
	freshAfterReset *UserSubscription
	resets          int
}

func (r *wsBillingSubRepo) GetActiveByUserIDAndGroupID(context.Context, int64, int64) (*UserSubscription, error) {
	if r.sub == nil {
		return nil, ErrSubscriptionNotFound
	}
	copy := *r.sub
	return &copy, nil
}

func (r *wsBillingSubRepo) ResetDailyUsage(context.Context, int64, *time.Time, time.Time) error {
	r.resets++
	return nil
}

func (r *wsBillingSubRepo) GetByID(context.Context, int64) (*UserSubscription, error) {
	copy := *r.freshAfterReset
	return &copy, nil
}

func TestCheckWebSocketBillingEligibilityFreshBalancesAndRateLimits(t *testing.T) {
	svc := &BillingCacheService{cfg: &config.Config{}}
	key := healthyWSBillingKey()
	_, err := svc.CheckWebSocketBillingEligibility(context.Background(), key, PlatformOpenAI)
	require.NoError(t, err)
	key.User.Balance = 0
	_, err = svc.CheckWebSocketBillingEligibility(context.Background(), key, PlatformOpenAI)
	require.ErrorIs(t, err, ErrInsufficientBalance)
	key.User.Balance = 10
	now := time.Now()
	key.RateLimit5h, key.Usage5h, key.Window5hStart = 1, 1, &now
	_, err = svc.CheckWebSocketBillingEligibility(context.Background(), key, PlatformOpenAI)
	require.ErrorIs(t, err, ErrAPIKeyRateLimit5hExceeded)
}

func TestCheckWebSocketBillingEligibilityFreshSubscription(t *testing.T) {
	now := time.Now()
	day := timezone.StartOfDay(now)
	limit := 1.0
	key := healthyWSBillingKey()
	key.Group.SubscriptionType, key.Group.DailyLimitUSD = SubscriptionTypeSubscription, &limit
	key.Group.WeeklyLimitUSD, key.Group.MonthlyLimitUSD = &limit, &limit
	base := UserSubscription{ID: 8, UserID: key.UserID, GroupID: key.Group.ID, Status: SubscriptionStatusActive,
		StartsAt: now.Add(-48 * time.Hour), ExpiresAt: now.Add(48 * time.Hour),
		DailyWindowStart: &day, WeeklyWindowStart: &now, MonthlyWindowStart: &now}
	for _, tt := range []struct {
		name   string
		mutate func(*UserSubscription)
		want   error
	}{
		{"healthy replacement", func(s *UserSubscription) { s.ID = 9 }, nil},
		{"daily exhausted", func(s *UserSubscription) { s.DailyUsageUSD = 1 }, ErrDailyLimitExceeded},
		{"weekly exhausted", func(s *UserSubscription) { s.WeeklyUsageUSD = 1 }, ErrWeeklyLimitExceeded},
		{"monthly exhausted", func(s *UserSubscription) { s.MonthlyUsageUSD = 1 }, ErrMonthlyLimitExceeded},
		{"expired", func(s *UserSubscription) { s.ExpiresAt = now.Add(-time.Second) }, ErrSubscriptionInvalid},
		{"revoked", func(s *UserSubscription) { s.Status = SubscriptionStatusSuspended }, ErrSubscriptionInvalid},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sub := base
			tt.mutate(&sub)
			svc := &BillingCacheService{cfg: &config.Config{}, subRepo: &wsBillingSubRepo{sub: &sub}}
			got, err := svc.CheckWebSocketBillingEligibility(context.Background(), key, PlatformOpenAI)
			if tt.want != nil {
				require.ErrorIs(t, err, tt.want)
			} else {
				require.NoError(t, err)
				require.Equal(t, int64(9), got.ID)
			}
		})
	}
	stale, refreshed := base, base
	yesterday := day.AddDate(0, 0, -1)
	stale.DailyWindowStart, stale.DailyUsageUSD = &yesterday, 1
	refreshed.DailyUsageUSD = 1
	repo := &wsBillingSubRepo{sub: &stale, freshAfterReset: &refreshed}
	svc := &BillingCacheService{cfg: &config.Config{}, subRepo: repo}
	_, err := svc.CheckWebSocketBillingEligibility(context.Background(), key, PlatformOpenAI)
	require.ErrorIs(t, err, ErrDailyLimitExceeded, "a competing reset winner's fresh usage must be rechecked")
	require.Equal(t, 1, repo.resets)
	refreshed.DailyUsageUSD = 0
	got, err := svc.CheckWebSocketBillingEligibility(context.Background(), key, PlatformOpenAI)
	require.NoError(t, err, "a successfully reset expired window permits the next turn")
	require.Zero(t, got.DailyUsageUSD)
}

func TestRefreshWebSocketAPIKeyDeletedOrRecreated(t *testing.T) {
	initial := healthyWSBillingKey()
	repo := &wsBillingKeyRepo{err: ErrAPIKeyNotFound}
	svc := &APIKeyService{apiKeyRepo: repo, cfg: &config.Config{}}
	_, err := svc.RefreshWebSocketAPIKey(context.Background(), initial, "127.0.0.1")
	require.True(t, errors.Is(err, ErrAPIKeyNotFound))
	repo.err, repo.key = nil, healthyWSBillingKey()
	repo.key.ID++
	_, err = svc.RefreshWebSocketAPIKey(context.Background(), initial, "127.0.0.1")
	require.ErrorIs(t, err, ErrWebSocketAuthentication)
}

type wsBillingQuotaCache struct {
	BillingCache
	entry *UserPlatformQuotaCacheEntry
}

func (c *wsBillingQuotaCache) GetUserPlatformQuotaCache(context.Context, int64, string) (*UserPlatformQuotaCacheEntry, bool, error) {
	return c.entry, true, nil
}

type wsBillingRPMCache struct {
	UserRPMCache
	count int
}

func (c *wsBillingRPMCache) IncrementUserRPM(context.Context, int64) (int, error) {
	c.count++
	return c.count, nil
}

func TestCheckWebSocketBillingEligibilityPreservesPlatformQuotaAndRPM(t *testing.T) {
	now := time.Now()
	limit := 1.0
	cache := &wsBillingQuotaCache{entry: &UserPlatformQuotaCacheEntry{
		SchemaVersion: UserPlatformQuotaCacheSchemaV1, DailyLimitUSD: &limit, DailyUsageUSD: 1,
		DailyWindowStart: &now, WeeklyWindowStart: &now, MonthlyWindowStart: &now,
	}}
	rpm := &wsBillingRPMCache{}
	key := healthyWSBillingKey()
	key.User.RPMLimit = 1
	svc := &BillingCacheService{cfg: &config.Config{}, cache: cache, userRPMCache: rpm,
		userPlatformQuotaRepo: &wsBillingQuotaRepo{}}
	_, err := svc.CheckWebSocketBillingEligibility(context.Background(), key, PlatformOpenAI)
	require.ErrorIs(t, err, ErrUserPlatformDailyQuotaExhausted)
	require.Zero(t, rpm.count, "financial rejection must not consume RPM")
	cache.entry.DailyUsageUSD = 0
	_, err = svc.CheckWebSocketBillingEligibility(context.Background(), key, PlatformOpenAI)
	require.NoError(t, err)
	_, err = svc.CheckWebSocketBillingEligibility(context.Background(), key, PlatformOpenAI)
	require.ErrorIs(t, err, ErrUserRPMExceeded)
}

type wsBillingQuotaRepo struct {
	UserPlatformQuotaRepository
}
