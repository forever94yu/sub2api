//go:build unit

package service

import (
	"context"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestRefundBalanceCachesTrackGatewayOutcome(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      string
		queryStatus string
		wantBalance float64
	}{
		{name: "success", status: payment.ProviderStatusSuccess},
		{name: "failure restores balance", status: payment.ProviderStatusFailed, wantBalance: 100},
		{name: "pending success", status: payment.ProviderStatusPending, queryStatus: payment.ProviderStatusSuccess},
		{name: "pending failure restores balance", status: payment.ProviderStatusPending, queryStatus: payment.ProviderStatusFailed, wantBalance: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)
			order := createPendingRefundOrderForTest(t, ctx, client, "cache-"+tc.name)
			_, err := client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusCompleted).Save(ctx)
			require.NoError(t, err)
			repo := &refundAtomicBalanceRepo{balance: 100}
			cache := &balanceEligibilityCacheStub{balance: 100, cacheMissAfterInvalidate: true}
			billing := NewBillingCacheService(cache, repo, nil, nil, nil, nil, &config.Config{}, nil)
			t.Cleanup(billing.Stop)
			auth := &authCacheInvalidatorStub{}
			svc := &PaymentService{entClient: client, userRepo: repo, loadBalancer: &captureLoadBalancer{}}
			svc.SetBalanceCacheInvalidators(billing, auth)
			provider := &refundCaptureProvider{status: tc.status, queryStatus: tc.queryStatus, onRefund: func() {
				balance, err := billing.GetUserBalance(ctx, order.UserID)
				require.NoError(t, err)
				require.Zero(t, balance, "the deducted balance must be visible while the gateway refund is running")
				require.Equal(t, []int64{order.UserID}, auth.userIDs)
			}}
			t.Cleanup(replacePaymentProviderFactoryForTest(t, provider))
			plan, early, err := svc.PrepareRefund(ctx, order.ID, 100, "cache refund", false, true)
			require.NoError(t, err)
			require.Nil(t, early)
			_, err = svc.ExecuteRefund(ctx, plan)
			require.NoError(t, err)
			if tc.status == payment.ProviderStatusPending {
				// A separate request may refill the zero balance while pending.
				cache.balance = 0
				cache.invalidated.Store(false)
				_, err = svc.QueryAndFinalizeRefund(ctx, order.ID)
				require.NoError(t, err)
			}
			balance, err := billing.GetUserBalance(ctx, order.UserID)
			require.NoError(t, err)
			require.Equal(t, tc.wantBalance, balance)
			wantInvalidations := int64(1)
			if tc.wantBalance > 0 {
				wantInvalidations = 2
			}
			require.Equal(t, wantInvalidations, cache.invalidateCalls.Load())
			require.Len(t, auth.userIDs, int(wantInvalidations))
		})
	}
}

func TestLegacyPendingRefundInvalidatesBalanceCaches(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createPendingRefundOrderForTest(t, ctx, client, "legacy-cache")
	repo := &refundAtomicBalanceRepo{balance: 100}
	cache := &balanceEligibilityCacheStub{balance: 100, cacheMissAfterInvalidate: true}
	billing := NewBillingCacheService(cache, repo, nil, nil, nil, nil, &config.Config{}, nil)
	t.Cleanup(billing.Stop)
	auth := &authCacheInvalidatorStub{}
	svc := &PaymentService{entClient: client, userRepo: repo, loadBalancer: &captureLoadBalancer{}}
	svc.SetBalanceCacheInvalidators(billing, auth)
	t.Cleanup(replacePaymentProviderFactoryForTest(t, &refundCaptureProvider{queryStatus: payment.ProviderStatusSuccess}))

	result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.ErrorIs(t, billing.CheckBillingEligibility(ctx, &User{ID: order.UserID}, nil, nil, nil, ""), ErrInsufficientBalance)
	require.Equal(t, []int64{order.UserID}, auth.userIDs)
}

func TestRefundCompensationInvalidatesCachesOnlyAfterCommit(t *testing.T) {
	for _, commit := range []bool{false, true} {
		name := "rollback"
		if commit {
			name = "commit"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)
			order := createPendingRefundOrderForTest(t, ctx, client, "cache-tx-"+name)
			cache := &balanceEligibilityCacheStub{cacheMissAfterInvalidate: true}
			billing := NewBillingCacheService(cache, nil, nil, nil, nil, nil, &config.Config{}, nil)
			t.Cleanup(billing.Stop)
			auth := &authCacheInvalidatorStub{}
			svc := &PaymentService{entClient: client, userRepo: &mockUserRepo{updateBalanceFn: func(ctx context.Context, id int64, amount float64) error {
				_, err := dbent.TxFromContext(ctx).User.UpdateOneID(id).AddBalance(amount).Save(ctx)
				return err
			}}}
			svc.SetBalanceCacheInvalidators(billing, auth)
			tx, err := client.Tx(ctx)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()
			require.NoError(t, svc.rollbackRefundDeduction(dbent.NewTxContext(ctx, tx), &RefundPlan{
				Order: order, DeductionType: payment.DeductionTypeBalance, BalanceToDeduct: 25,
			}))
			require.Zero(t, cache.invalidateCalls.Load(), "uncommitted compensation must not trigger an old-balance cache refill")
			require.Empty(t, auth.userIDs)
			if commit {
				require.NoError(t, tx.Commit())
				require.Equal(t, int64(1), cache.invalidateCalls.Load())
				require.Equal(t, []int64{order.UserID}, auth.userIDs)
			} else {
				require.NoError(t, tx.Rollback())
				require.Zero(t, cache.invalidateCalls.Load())
				require.Empty(t, auth.userIDs)
			}
		})
	}
}
