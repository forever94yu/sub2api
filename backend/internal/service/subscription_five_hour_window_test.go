//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fiveHourWindowUserSubRepo struct {
	userSubRepoNoop

	sub            *UserSubscription
	resetCalls     int
	expectedStart  *time.Time
	newWindowStart time.Time
}

func (r *fiveHourWindowUserSubRepo) ResetFiveHourUsage(_ context.Context, _ int64, expectedWindowStart *time.Time, newWindowStart time.Time) error {
	r.resetCalls++
	r.expectedStart = expectedWindowStart
	r.newWindowStart = newWindowStart
	if r.sub != nil {
		r.sub.FiveHourUsageUSD = 0
		r.sub.FiveHourWindowStart = &newWindowStart
	}
	return nil
}

func (r *fiveHourWindowUserSubRepo) GetByID(_ context.Context, _ int64) (*UserSubscription, error) {
	cp := *r.sub
	return &cp, nil
}

func newFiveHourTestSub(now time.Time, windowStart *time.Time, usage float64) *UserSubscription {
	daily := now.Add(-time.Hour)
	return &UserSubscription{
		ID:                  1,
		UserID:              10,
		GroupID:             20,
		Status:              SubscriptionStatusActive,
		StartsAt:            now.Add(-72 * time.Hour),
		ExpiresAt:           now.Add(30 * 24 * time.Hour),
		DailyWindowStart:    &daily,
		WeeklyWindowStart:   &daily,
		MonthlyWindowStart:  &daily,
		FiveHourWindowStart: windowStart,
		FiveHourUsageUSD:    usage,
	}
}

func TestValidateAndCheckLimits_FiveHourLimitExceededWithinWindow(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	start := now.Add(-2 * time.Hour)
	limit := 10.0
	sub := newFiveHourTestSub(now, &start, 10.5)
	svc := NewSubscriptionService(groupRepoNoop{}, userSubRepoNoop{}, nil, nil, nil)
	svc.now = func() time.Time { return now }

	needsMaintenance, err := svc.ValidateAndCheckLimits(sub, &Group{FiveHourLimitUSD: &limit})

	require.ErrorIs(t, err, ErrFiveHourLimitExceeded)
	require.False(t, needsMaintenance)
}

func TestValidateAndCheckLimits_FiveHourWindowEndedAllowsNewWindow(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	start := now.Add(-5 * time.Hour)
	limit := 10.0
	sub := newFiveHourTestSub(now, &start, 25)
	svc := NewSubscriptionService(groupRepoNoop{}, userSubRepoNoop{}, nil, nil, nil)
	svc.now = func() time.Time { return now }

	needsMaintenance, err := svc.ValidateAndCheckLimits(sub, &Group{FiveHourLimitUSD: &limit})

	require.NoError(t, err)
	require.True(t, needsMaintenance)
	require.Zero(t, sub.FiveHourUsageUSD)
}

func TestValidateAndCheckLimits_NoFiveHourLimitSkipsWindowMaintenance(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	sub := newFiveHourTestSub(now, nil, 0)
	svc := NewSubscriptionService(groupRepoNoop{}, userSubRepoNoop{}, nil, nil, nil)
	svc.now = func() time.Time { return now }

	needsMaintenance, err := svc.ValidateAndCheckLimits(sub, &Group{})

	require.NoError(t, err)
	require.False(t, needsMaintenance)
}

func TestEnsureWindowMaintenance_OpensFiveHourWindowAtRequestTime(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 34, 56, 0, time.UTC)
	previous := now.Add(-7 * time.Hour)
	limit := 10.0
	sub := newFiveHourTestSub(now, &previous, 9)
	repo := &fiveHourWindowUserSubRepo{sub: sub}
	svc := NewSubscriptionService(groupRepoNoop{}, repo, nil, nil, nil)
	svc.now = func() time.Time { return now }

	refreshed, err := svc.EnsureWindowMaintenance(context.Background(), sub, &Group{FiveHourLimitUSD: &limit})

	require.NoError(t, err)
	require.Equal(t, 1, repo.resetCalls)
	require.Equal(t, previous, *repo.expectedStart)
	require.Equal(t, now, repo.newWindowStart, "新窗口应从本次请求时刻开始，而不是按 5 小时步长对齐")
	require.Zero(t, refreshed.FiveHourUsageUSD)
	require.Equal(t, now.Add(5*time.Hour), *refreshed.FiveHourResetTime())
}

func TestEnsureWindowMaintenance_KeepsActiveFiveHourWindow(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	start := now.Add(-4*time.Hour - 59*time.Minute)
	limit := 10.0
	sub := newFiveHourTestSub(now, &start, 3)
	repo := &fiveHourWindowUserSubRepo{sub: sub}
	svc := NewSubscriptionService(groupRepoNoop{}, repo, nil, nil, nil)
	svc.now = func() time.Time { return now }

	refreshed, err := svc.EnsureWindowMaintenance(context.Background(), sub, &Group{FiveHourLimitUSD: &limit})

	require.NoError(t, err)
	require.Zero(t, repo.resetCalls)
	require.Equal(t, 3.0, refreshed.FiveHourUsageUSD)
}

func TestEnsureWindowMaintenance_GroupWithoutFiveHourLimitDoesNotOpenWindow(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	sub := newFiveHourTestSub(now, nil, 0)
	repo := &fiveHourWindowUserSubRepo{sub: sub}
	svc := NewSubscriptionService(groupRepoNoop{}, repo, nil, nil, nil)
	svc.now = func() time.Time { return now }

	refreshed, err := svc.EnsureWindowMaintenance(context.Background(), sub, &Group{})

	require.NoError(t, err)
	require.Zero(t, repo.resetCalls)
	require.Nil(t, refreshed.FiveHourWindowStart)
}

func TestNormalizeExpiredWindows_ClearsEndedFiveHourWindow(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	ended := now.Add(-5 * time.Hour)
	active := now.Add(-time.Hour)
	subs := []UserSubscription{
		*newFiveHourTestSub(now, &ended, 8),
		*newFiveHourTestSub(now, &active, 4),
	}

	normalizeExpiredWindowsAt(subs, now)

	require.Nil(t, subs[0].FiveHourWindowStart)
	require.Zero(t, subs[0].FiveHourUsageUSD)
	require.Equal(t, active, *subs[1].FiveHourWindowStart)
	require.Equal(t, 4.0, subs[1].FiveHourUsageUSD)
}

func TestCalculateProgress_IncludesFiveHourWindow(t *testing.T) {
	now := time.Now()
	start := now.Add(-time.Hour)
	limit := 20.0
	sub := newFiveHourTestSub(now, &start, 5)
	svc := NewSubscriptionService(groupRepoNoop{}, userSubRepoNoop{}, nil, nil, nil)

	progress := svc.calculateProgress(sub, &Group{Name: "g", FiveHourLimitUSD: &limit})

	require.NotNil(t, progress.FiveHour)
	require.Equal(t, 20.0, progress.FiveHour.LimitUSD)
	require.Equal(t, 5.0, progress.FiveHour.UsedUSD)
	require.Equal(t, 15.0, progress.FiveHour.RemainingUSD)
	require.InDelta(t, 25.0, progress.FiveHour.Percentage, 1e-9)
	require.Equal(t, start.Add(5*time.Hour), progress.FiveHour.ResetsAt)
}

func TestRenewedSubscriptionTermClearsFiveHourWindow(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	start := now.Add(-time.Hour)
	existing := newFiveHourTestSub(now, &start, 7)

	renewed := renewedSubscriptionTerm(existing, "", now, now.AddDate(0, 0, 30))

	require.Nil(t, renewed.FiveHourWindowStart)
	require.Zero(t, renewed.FiveHourUsageUSD)
}

func TestAdminResetQuota_ResetFiveHourOnly(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	start := now.Add(-time.Hour)
	sub := newFiveHourTestSub(now, &start, 7)
	sub.DailyUsageUSD = 3
	stub := &resetQuotaUserSubRepoStub{sub: sub}
	svc := newResetQuotaSvc(stub)

	result, err := svc.AdminResetQuota(context.Background(), sub.ID, true, false, false, false)

	require.NoError(t, err)
	require.True(t, stub.resetFiveHourCalled)
	require.False(t, stub.resetDailyCalled)
	require.Zero(t, result.FiveHourUsageUSD)
	require.Nil(t, result.FiveHourWindowStart)
	require.Equal(t, 3.0, result.DailyUsageUSD)
}

type fiveHourSubscriptionCache struct {
	BillingCache
	data *SubscriptionCacheData
}

func (c *fiveHourSubscriptionCache) GetSubscriptionCache(context.Context, int64, int64) (*SubscriptionCacheData, error) {
	cp := *c.data
	return &cp, nil
}

func TestCheckSubscriptionEligibility_FiveHourLimit(t *testing.T) {
	limit := 10.0
	group := &Group{ID: 20, SubscriptionType: SubscriptionTypeSubscription, Status: StatusActive, FiveHourLimitUSD: &limit}
	activeStart := time.Now().Add(-time.Hour)
	endedStart := time.Now().Add(-6 * time.Hour)
	tests := []struct {
		name    string
		start   *time.Time
		usage   float64
		wantErr error
	}{
		{name: "active window at limit", start: &activeStart, usage: 10, wantErr: ErrFiveHourLimitExceeded},
		{name: "active window below limit", start: &activeStart, usage: 9.99},
		{name: "ended window usage ignored", start: &endedStart, usage: 50},
		{name: "window not opened", start: nil, usage: 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &BillingCacheService{cache: &fiveHourSubscriptionCache{data: &SubscriptionCacheData{
				Status:              SubscriptionStatusActive,
				ExpiresAt:           time.Now().Add(24 * time.Hour),
				FiveHourWindowStart: tt.start,
				FiveHourUsage:       tt.usage,
			}}}

			err := s.checkSubscriptionEligibility(context.Background(), 1, group, nil)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}
