//go:build integration

package repository

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type refundTestLoadBalancer struct{ config map[string]string }

func (lb *refundTestLoadBalancer) GetInstanceConfig(context.Context, int64) (map[string]string, error) {
	return lb.config, nil
}

func (*refundTestLoadBalancer) SelectInstance(context.Context, string, payment.PaymentType, payment.Strategy, float64) (*payment.InstanceSelection, error) {
	return nil, nil
}

func TestFailedRefundPreservesSubscription(t *testing.T) {
	for _, mode := range []string{"unchanged", "renewed", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			client := testEntClient(t)
			userRepo := NewUserRepository(client, integrationDB)
			subRepo := NewUserSubscriptionRepository(client)
			groupRepo := NewGroupRepository(client, integrationDB)
			subSvc := service.NewSubscriptionService(groupRepo, subRepo, nil, client, nil)
			t.Cleanup(subSvc.Stop)
			calls := 0
			var duringRefund func()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls++
				require.Equal(t, "/api.php", req.URL.Path)
				require.Equal(t, "refund", req.URL.Query().Get("act"))
				if duringRefund != nil {
					duringRefund()
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"code":0,"msg":"merchant balance insufficient"}`))
			}))
			defer upstream.Close()
			lb := &refundTestLoadBalancer{config: map[string]string{
				"pid": "refund-test", "pkey": "refund-test", "apiBase": upstream.URL,
				"notifyUrl": "http://127.0.0.1/notify", "returnUrl": "http://127.0.0.1/return",
			}}
			svc := service.NewPaymentService(client, nil, lb, nil, subSvc, nil, userRepo, groupRepo, nil)
			user, err := client.User.Create().SetEmail("refund-real-repo@example.com").SetPasswordHash("hash").SetUsername("refund-real-repo").Save(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.User.DeleteOneID(user.ID).Exec(mixins.SkipSoftDelete(ctx))) })
			group, err := client.Group.Create().SetName("refund-real-group").SetSubscriptionType(service.SubscriptionTypeSubscription).Save(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.Group.DeleteOneID(group.ID).Exec(mixins.SkipSoftDelete(ctx))) })
			expiresAt := time.Now().AddDate(0, 0, 10).UTC().Truncate(time.Microsecond)
			sub := &service.UserSubscription{UserID: user.ID, GroupID: group.ID, Status: service.SubscriptionStatusActive, StartsAt: time.Now().AddDate(0, 0, -20), ExpiresAt: expiresAt}
			require.NoError(t, subRepo.Create(ctx, sub))
			t.Cleanup(func() {
				require.NoError(t, client.UserSubscription.DeleteOneID(sub.ID).Exec(mixins.SkipSoftDelete(ctx)))
			})
			inst, err := client.PaymentProviderInstance.Create().SetProviderKey(payment.TypeEasyPay).SetName("local-easypay").SetConfig("{}").SetSupportedTypes("alipay").SetEnabled(true).SetRefundEnabled(true).Save(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, client.PaymentProviderInstance.DeleteOneID(inst.ID).Exec(ctx)) })
			order, err := client.PaymentOrder.Create().SetUserID(user.ID).SetUserEmail(user.Email).SetUserName(user.Username).
				SetAmount(100).SetPayAmount(100).SetFeeRate(0).SetRechargeCode("REFUND-REPO").SetOutTradeNo("refund_real_repo").
				SetPaymentType(payment.TypeAlipay).SetPaymentTradeNo("trade_real_repo").SetOrderType(payment.OrderTypeSubscription).
				SetSubscriptionGroupID(group.ID).SetSubscriptionDays(30).SetStatus(service.OrderStatusCompleted).
				SetExpiresAt(time.Now().Add(time.Hour)).SetPaidAt(time.Now()).SetClientIP("127.0.0.1").SetSrcHost("127.0.0.1").
				SetProviderInstanceID(strconv.FormatInt(inst.ID, 10)).Save(ctx)
			require.NoError(t, err)
			t.Cleanup(func() {
				_, err := client.PaymentAuditLog.Delete().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10))).Exec(ctx)
				require.NoError(t, err)
				require.NoError(t, client.PaymentOrder.DeleteOneID(order.ID).Exec(ctx))
			})
			if mode == "renewed" {
				duringRefund = func() {
					_, _, err := subSvc.AssignOrExtendSubscription(ctx, &service.AssignSubscriptionInput{
						UserID: user.ID, GroupID: group.ID, ValidityDays: 7, Notes: "renewed during refund",
					})
					require.NoError(t, err)
				}
			} else if mode == "revoked" {
				duringRefund = func() { require.NoError(t, subSvc.RevokeSubscription(ctx, sub.ID)) }
			}
			plan, early, err := svc.PrepareRefund(ctx, order.ID, 100, "refund test", false, true)
			require.NoError(t, err)
			require.Nil(t, early)
			result, err := svc.ExecuteRefund(ctx, plan)
			if mode == "revoked" {
				require.Error(t, err)
				_, err = subRepo.GetByID(ctx, sub.ID)
				require.ErrorIs(t, err, service.ErrSubscriptionNotFound, "refund rollback must preserve a separate admin revocation")
				return
			}
			require.NoError(t, err)
			require.False(t, result.Success)
			require.Equal(t, 1, calls)
			active, err := subRepo.GetByID(ctx, sub.ID)
			require.NoError(t, err, "a declined refund must restore the original paid subscription")
			require.Equal(t, service.SubscriptionStatusActive, active.Status)
			if mode == "renewed" {
				require.WithinDuration(t, expiresAt.AddDate(0, 0, 7), active.ExpiresAt, time.Second)
				require.Contains(t, active.Notes, "renewed during refund")
			} else {
				require.True(t, active.ExpiresAt.Equal(expiresAt))
			}
			savedOrder, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, service.OrderStatusCompleted, savedOrder.Status)
		})
	}
}
