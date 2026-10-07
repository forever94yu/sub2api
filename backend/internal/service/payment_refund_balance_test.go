//go:build unit

package service

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

type refundAtomicBalanceRepo struct {
	mockUserRepo
	balance float64
}

func (r *refundAtomicBalanceRepo) GetByID(_ context.Context, id int64) (*User, error) {
	return &User{ID: id, Balance: r.balance}, nil
}

func (r *refundAtomicBalanceRepo) AdjustBalance(_ context.Context, _ int64, delta float64) (BalanceChange, error) {
	change := BalanceChange{Old: r.balance, New: r.balance + delta}
	if change.New < 0 {
		return change, ErrBalanceNegative
	}
	r.balance = change.New
	return change, nil
}

func (r *refundAtomicBalanceRepo) DeductAvailableBalance(_ context.Context, _ int64, amount float64) (float64, error) {
	deducted := math.Min(amount, math.Max(r.balance, 0))
	r.balance -= deducted
	return deducted, nil
}

func (r *refundAtomicBalanceRepo) DeductBalance(_ context.Context, _ int64, amount float64) error {
	r.balance -= amount
	return nil
}

func (r *refundAtomicBalanceRepo) UpdateBalance(_ context.Context, _ int64, amount float64) error {
	r.balance += amount
	return nil
}

type refundCaptureProvider struct {
	refundProviderTestDouble
	refunds     []payment.RefundRequest
	status      string
	queryStatus string
	refundID    string
	queries     []payment.RefundQueryRequest
	queryErr    error
	onRefund    func()
}

func (p *refundCaptureProvider) Refund(_ context.Context, req payment.RefundRequest) (*payment.RefundResponse, error) {
	if p.onRefund != nil {
		p.onRefund()
	}
	p.refunds = append(p.refunds, req)
	status := p.status
	if status == "" {
		status = payment.ProviderStatusSuccess
	}
	return &payment.RefundResponse{RefundID: p.refundID, Status: status}, nil
}

func (p *refundCaptureProvider) QueryRefund(_ context.Context, req payment.RefundQueryRequest) (*payment.RefundResponse, error) {
	p.queries = append(p.queries, req)
	if p.queryErr != nil {
		return nil, p.queryErr
	}
	return &payment.RefundResponse{RefundID: p.refundID, Status: p.queryStatus}, nil
}

func TestPendingRefundRetainsDeductionUntilProviderFinalizes(t *testing.T) {
	for _, status := range []string{payment.ProviderStatusSuccess, payment.ProviderStatusFailed} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)
			order := createPendingRefundOrderForTest(t, ctx, client, "retained-"+status)
			_, err := client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusCompleted).Save(ctx)
			require.NoError(t, err)
			repo := &refundAtomicBalanceRepo{balance: 100}
			svc := &PaymentService{entClient: client, userRepo: repo, loadBalancer: &captureLoadBalancer{}}
			provider := &refundCaptureProvider{status: payment.ProviderStatusPending, queryStatus: payment.ProviderStatusPending, refundID: "original-refund-request"}
			t.Cleanup(replacePaymentProviderFactoryForTest(t, provider))
			plan, early, err := svc.PrepareRefund(ctx, order.ID, 100, "pending refund", false, true)
			require.NoError(t, err)
			require.Nil(t, early)
			result, err := svc.ExecuteRefund(ctx, plan)
			require.NoError(t, err)
			require.False(t, result.Success)
			require.Zero(t, repo.balance, "accepted refunds must reserve the deduction until the provider finishes")
			detail, err := svc.latestRefundPendingDetail(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, provider.refundID, detail.RefundID)

			result, err = svc.QueryAndFinalizeRefund(ctx, order.ID)
			require.NoError(t, err)
			require.False(t, result.Success)
			require.Zero(t, repo.balance)
			require.Equal(t, provider.refundID, provider.queries[0].RefundID)
			provider.queryErr = errors.New("refund query temporarily unavailable")
			_, err = svc.QueryAndFinalizeRefund(ctx, order.ID)
			require.ErrorContains(t, err, "temporarily unavailable")
			require.Zero(t, repo.balance, "a query failure must not release reserved funds")
			provider.queryErr = nil
			provider.queryStatus = status

			result, err = svc.QueryAndFinalizeRefund(ctx, order.ID)
			require.NoError(t, err)
			saved, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			if status == payment.ProviderStatusSuccess {
				require.True(t, result.Success)
				require.Zero(t, repo.balance, "successful settlement must not debit the retained amount twice")
				require.Equal(t, 100.0, result.BalanceDeducted)
				require.Equal(t, OrderStatusRefunded, saved.Status)
			} else {
				require.False(t, result.Success)
				require.Equal(t, 100.0, repo.balance, "failed refunds must compensate the retained deduction")
				require.Equal(t, OrderStatusRefundFailed, saved.Status)
			}
			balanceAfterFinalization := repo.balance
			_, err = svc.QueryAndFinalizeRefund(ctx, order.ID)
			require.Error(t, err)
			require.Equal(t, balanceAfterFinalization, repo.balance, "repeated confirmation must not repeat the debit or compensation")
		})
	}
}

func TestLegacyPendingRefundRecordsDebtAfterConcurrentSpend(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createPendingRefundOrderForTest(t, ctx, client, "legacy-pending-spent")
	// Legacy pending refunds returned the debit to the user before confirmation.
	repo := &refundAtomicBalanceRepo{balance: 25}
	svc := &PaymentService{entClient: client, userRepo: repo, loadBalancer: &captureLoadBalancer{}}
	provider := &refundCaptureProvider{queryStatus: payment.ProviderStatusSuccess}
	t.Cleanup(replacePaymentProviderFactoryForTest(t, provider))

	result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 100.0, result.BalanceDeducted)
	require.Equal(t, -75.0, repo.balance, "confirmed cash refunds must remain fully represented in the balance ledger")
	saved, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusRefunded, saved.Status)
}

func TestExecuteRefundRequiresForceAfterConcurrentSpend(t *testing.T) {
	for _, force := range []bool{false, true} {
		name := "non-forced"
		if force {
			name = "forced"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)
			order := createPendingRefundOrderForTest(t, ctx, client, "concurrent-spend-"+name)
			_, err := client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusCompleted).Save(ctx)
			require.NoError(t, err)
			repo := &refundAtomicBalanceRepo{balance: 100}
			svc := &PaymentService{entClient: client, userRepo: repo, loadBalancer: &captureLoadBalancer{}}
			provider := &refundCaptureProvider{}
			t.Cleanup(replacePaymentProviderFactoryForTest(t, provider))

			plan, early, err := svc.PrepareRefund(ctx, order.ID, 100, "refund", force, true)
			require.NoError(t, err)
			require.Nil(t, early)
			// Another settled request consumes the balance after the refund precheck.
			repo.balance = 25
			result, err := svc.ExecuteRefund(ctx, plan)
			require.NoError(t, err)
			require.NotNil(t, result)
			saved, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			if !force {
				require.False(t, result.Success)
				require.True(t, result.RequireForce)
				require.Equal(t, 25.0, repo.balance)
				require.Empty(t, provider.refunds)
				require.Equal(t, OrderStatusCompleted, saved.Status)
				return
			}
			require.True(t, result.Success)
			require.Equal(t, 25.0, result.BalanceDeducted)
			require.Zero(t, repo.balance)
			require.Len(t, provider.refunds, 1)
			require.Equal(t, "100.00", provider.refunds[0].Amount)
			require.Equal(t, OrderStatusRefunded, saved.Status)
		})
	}
}
