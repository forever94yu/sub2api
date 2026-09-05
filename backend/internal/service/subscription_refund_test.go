//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRefundSubscriptionRollbackPreservesConcurrentRenewal(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	windowStart := now.AddDate(0, 0, -1)
	before := UserSubscription{
		ID: 7, UserID: 11, GroupID: 13, Status: SubscriptionStatusActive,
		StartsAt: now.AddDate(0, 0, -20), ExpiresAt: now.AddDate(0, 0, 10),
		DailyWindowStart: &windowStart, DailyUsageUSD: 3,
	}
	repo := &lockingRenewalRepo{stale: before, current: before}
	svc := NewSubscriptionService(&subscriptionGroupRepoStub{group: &Group{ID: 13, SubscriptionType: SubscriptionTypeSubscription}}, repo, nil, nil, nil)
	svc.now = func() time.Time { return now }
	adjustment, err := svc.deductRefundSubscription(ctx, before.ID, 30, nil)
	require.NoError(t, err)
	require.Equal(t, SubscriptionStatusExpired, repo.current.Status)
	require.Equal(t, before.DailyUsageUSD, repo.current.DailyUsageUSD)
	require.Equal(t, windowStart, *repo.current.DailyWindowStart)

	now = now.Add(time.Hour)
	renewed, _, err := svc.AssignOrExtendSubscription(ctx, &AssignSubscriptionInput{
		UserID: 11, GroupID: 13, ValidityDays: 7, Notes: "renewal during refund",
	})
	require.NoError(t, err)
	require.NoError(t, svc.rollbackRefundSubscription(ctx, adjustment))
	require.Equal(t, before.ExpiresAt.AddDate(0, 0, 7), repo.current.ExpiresAt)
	require.Equal(t, SubscriptionStatusActive, repo.current.Status)
	require.Equal(t, renewed.Notes, repo.current.Notes)
	require.Equal(t, renewed.StartsAt, repo.current.StartsAt)
	require.Equal(t, renewed.DailyWindowStart, repo.current.DailyWindowStart)
	require.Equal(t, 3, repo.lockReads, "deduction, renewal and rollback must all lock the current row")
}

func TestRefundSubscriptionRollbackPreservesManualSuspension(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	before := UserSubscription{ID: 7, UserID: 11, GroupID: 13, ExpiresAt: now.AddDate(0, 0, 10), Status: SubscriptionStatusActive}
	repo := &lockingRenewalRepo{stale: before, current: before}
	svc := NewSubscriptionService(groupRepoNoop{}, repo, nil, nil, nil)
	svc.now = func() time.Time { return now }
	adjustment, err := svc.deductRefundSubscription(context.Background(), before.ID, 30, nil)
	require.NoError(t, err)
	repo.current.Status = SubscriptionStatusSuspended
	require.NoError(t, svc.rollbackRefundSubscription(context.Background(), adjustment))
	require.Equal(t, before.ExpiresAt, repo.current.ExpiresAt)
	require.Equal(t, SubscriptionStatusSuspended, repo.current.Status)
}

func TestPendingSubscriptionRefundOnlyDeductsUnelapsedOriginalTerm(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	before := UserSubscription{ID: 7, UserID: 11, GroupID: 13, ExpiresAt: now.AddDate(0, 0, 10), Status: SubscriptionStatusActive}
	repo := &lockingRenewalRepo{stale: before, current: before}
	svc := NewSubscriptionService(groupRepoNoop{}, repo, nil, nil, nil)
	svc.now = func() time.Time { return now }
	adjustment, err := svc.deductRefundSubscription(context.Background(), before.ID, 30, nil)
	require.NoError(t, err)
	require.NoError(t, svc.rollbackRefundSubscription(context.Background(), adjustment))
	// The pending refund was rolled back, then the user renewed for seven days.
	repo.current.ExpiresAt = before.ExpiresAt.AddDate(0, 0, 7)
	now = now.AddDate(0, 0, 5)
	_, err = svc.deductRefundSubscription(context.Background(), before.ID, 30, adjustment)
	require.NoError(t, err)
	require.Equal(t, now.AddDate(0, 0, 7), repo.current.ExpiresAt)
}

func TestExtendSubscriptionLocksCurrentTerm(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	before := UserSubscription{ID: 7, UserID: 11, GroupID: 13, ExpiresAt: now.AddDate(0, 0, 10), Status: SubscriptionStatusActive}
	repo := &lockingRenewalRepo{stale: before, current: before}
	svc := NewSubscriptionService(groupRepoNoop{}, repo, nil, nil, nil)
	updated, err := svc.ExtendSubscription(context.Background(), before.ID, 7)
	require.NoError(t, err)
	require.Equal(t, before.ExpiresAt.AddDate(0, 0, 7), updated.ExpiresAt)
	require.Equal(t, 1, repo.lockReads)
}
