//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

type pendingRefundProviderStub struct {
	refundQueryProviderTestDouble
}

func TestPendingRefundSettlementUsesRecordedActualDeduction(t *testing.T) {
	for _, tc := range []struct {
		name          string
		detail        string
		wantDeduction float64
		wantReported  float64
	}{
		{"actual deduction", `{"deductBalance":true,"deductionType":"balance","balanceRolledBack":25,"deductionRollbackOK":true}`, 25, 25},
		{"failed rollback", `{"deductBalance":true,"deductionType":"balance","balanceDeducted":25,"deductionRollbackOK":false}`, 0, 25},
		{"legacy pending", `{"deductionRollbackOK":true}`, 100, 100},
		{"legacy zero actual", `{"balanceRolledBack":0,"deductionRollbackOK":true}`, 0, 0},
		{"legacy clamped actual", `{"balanceRolledBack":25,"deductionRollbackOK":true}`, 25, 25},
		{"legacy rollback failure", `{"deductionRollbackOK":false}`, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)
			order := createPendingRefundOrderForTest(t, ctx, client, "recorded-"+tc.name)
			_, err := client.PaymentAuditLog.Update().SetDetail(tc.detail).Save(ctx)
			require.NoError(t, err)
			var deducted float64
			svc := &PaymentService{entClient: client, loadBalancer: &captureLoadBalancer{}, userRepo: &mockUserRepo{
				deductAvailableBalanceFn: func(ctx context.Context, id int64, amount float64) (float64, error) {
					deducted += amount
					return amount, nil
				},
			}}
			restore := replacePaymentProviderFactoryForTest(t, &refundQueryProviderTestDouble{refundResponse: &payment.RefundResponse{Status: payment.ProviderStatusSuccess}})
			defer restore()
			result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
			require.NoError(t, err)
			require.True(t, result.Success)
			require.Equal(t, tc.wantDeduction, deducted)
			require.Equal(t, tc.wantReported, result.BalanceDeducted)
		})
	}
}

func TestMarkRefundPendingRollsBackCompensationWhenAuditFails(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createPendingRefundOrderForTest(t, ctx, client, "pending-audit-failure")
	_, err := client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusRefunding).Save(ctx)
	require.NoError(t, err)
	_, err = client.User.UpdateOneID(order.UserID).SetBalance(60).Save(ctx)
	require.NoError(t, err)
	client.PaymentAuditLog.Use(func(next dbent.Mutator) dbent.Mutator {
		return dbent.MutateFunc(func(context.Context, dbent.Mutation) (dbent.Value, error) {
			return nil, errors.New("audit unavailable")
		})
	})
	svc := &PaymentService{entClient: client, userRepo: &mockUserRepo{
		updateBalanceFn: func(ctx context.Context, id int64, amount float64) error {
			tx := dbent.TxFromContext(ctx)
			require.NotNil(t, tx)
			_, err := tx.User.UpdateOneID(id).AddBalance(amount).Save(ctx)
			return err
		},
	}}
	_, err = svc.markRefundPending(ctx, &RefundPlan{
		OrderID: order.ID, Order: order, RefundAmount: 40, DeductBalance: true,
		DeductionType: payment.DeductionTypeBalance, BalanceToDeduct: 40,
	}, &payment.RefundResponse{Status: payment.ProviderStatusPending})
	require.ErrorContains(t, err, "audit unavailable")
	user, err := client.User.Get(ctx, order.UserID)
	require.NoError(t, err)
	require.Equal(t, 60.0, user.Balance, "a failed pending state save must not leave a committed compensation")
	savedOrder, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusRefunding, savedOrder.Status)
}

func TestFailedPendingRefundRetriesOutstandingCompensation(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createPendingRefundOrderForTest(t, ctx, client, "pending-compensation")
	_, err := client.PaymentAuditLog.Update().SetDetail(`{"deductBalance":true,"deductionType":"balance","balanceDeducted":25,"deductionRollbackOK":false}`).Save(ctx)
	require.NoError(t, err)
	svc := &PaymentService{entClient: client, loadBalancer: &captureLoadBalancer{}, userRepo: &mockUserRepo{
		updateBalanceFn: func(ctx context.Context, id int64, amount float64) error {
			tx := dbent.TxFromContext(ctx)
			require.NotNil(t, tx)
			_, err := tx.User.UpdateOneID(id).AddBalance(amount).Save(ctx)
			return err
		},
	}}
	restore := replacePaymentProviderFactoryForTest(t, &refundQueryProviderTestDouble{refundResponse: &payment.RefundResponse{Status: payment.ProviderStatusFailed}})
	defer restore()
	result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.NoError(t, err)
	require.False(t, result.Success)
	user, err := client.User.Get(ctx, order.UserID)
	require.NoError(t, err)
	require.Equal(t, 25.0, user.Balance)
}

func TestPrepareRefundRejectsPendingResubmission(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createPendingRefundOrderForTest(t, ctx, client, "pending-resubmit")
	svc := &PaymentService{entClient: client}
	_, _, err := svc.PrepareRefund(ctx, order.ID, 100, "retry", false, false)
	require.ErrorContains(t, err, "pending refund must be queried")
}

func (*pendingRefundProviderStub) Refund(context.Context, payment.RefundRequest) (*payment.RefundResponse, error) {
	return &payment.RefundResponse{RefundID: "refund-choice", Status: payment.ProviderStatusPending}, nil
}

func TestPendingRefundPreservesNoDeduction(t *testing.T) {
	for _, orderType := range []string{payment.OrderTypeBalance, payment.OrderTypeSubscription} {
		t.Run(orderType, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)
			order := createPendingRefundOrderForTest(t, ctx, client, "no-deduction")
			_, err := client.PaymentAuditLog.Delete().Exec(ctx)
			require.NoError(t, err)
			order, err = client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusCompleted).SetOrderType(orderType).Save(ctx)
			require.NoError(t, err)
			_, err = client.User.UpdateOneID(order.UserID).SetBalance(150).Save(ctx)
			require.NoError(t, err)
			svc := &PaymentService{entClient: client, loadBalancer: &captureLoadBalancer{}, userRepo: &mockUserRepo{
				deductAvailableBalanceFn: func(ctx context.Context, id int64, amount float64) (float64, error) {
					db := client
					if tx := dbent.TxFromContext(ctx); tx != nil {
						db = tx.Client()
					}
					_, err := db.User.UpdateOneID(id).AddBalance(-amount).Save(ctx)
					return amount, err
				},
			}}
			restore := replacePaymentProviderFactoryForTest(t, &pendingRefundProviderStub{refundQueryProviderTestDouble{
				refundResponse: &payment.RefundResponse{RefundID: "refund-choice", Status: payment.ProviderStatusSuccess},
			}})
			defer restore()
			plan, early, err := svc.PrepareRefund(ctx, order.ID, 100, "refund without balance deduction", false, false)
			require.NoError(t, err)
			require.Nil(t, early)
			require.False(t, plan.DeductBalance)
			pending, err := svc.ExecuteRefund(ctx, plan)
			require.NoError(t, err)
			require.False(t, pending.Success)
			result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
			require.NoError(t, err)
			require.True(t, result.Success)
			after, err := client.User.Get(ctx, order.UserID)
			require.NoError(t, err)
			require.Equal(t, 150.0, after.Balance, "pending refund must preserve the explicit no-deduction choice")
			require.Zero(t, result.BalanceDeducted)
			require.Zero(t, result.SubDaysDeducted)
		})
	}
}
