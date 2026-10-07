package provider

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func newEasyPayNotificationTestProvider(t *testing.T) *EasyPay {
	t.Helper()
	provider, err := NewEasyPay("notification-test", map[string]string{
		"pid":         "1000",
		"pkey":        "test-merchant-secret",
		"apiBase":     "https://pay.example.com",
		"notifyUrl":   "https://site.example.com/api/v1/payment/webhook/easypay",
		"returnUrl":   "https://site.example.com/payment/result",
		"paymentMode": paymentModePopup,
	})
	require.NoError(t, err)
	return provider
}

func TestEasyPayNotifyRejectsReusedOrderSignature(t *testing.T) {
	t.Parallel()
	e := newEasyPayNotificationTestProvider(t)
	returnPrefix := "https://site.example.com/payment/result?order_id=99&out_trade_no=ORDER123&status=success"
	response, err := e.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID:     "ORDER123",
		PaymentType: payment.TypeAlipay,
		Subject:     "balance recharge",
		Amount:      "15.25",
		ReturnURL:   returnPrefix + "&trade_status=TRADE_SUCCESS",
	})
	require.NoError(t, err)
	payURL, err := url.Parse(response.PayURL)
	require.NoError(t, err)

	// Only the public checkout URL is needed to construct the forged callback.
	callback := payURL.Query()
	callback.Set("return_url", returnPrefix)
	callback.Set("trade_status", "TRADE_SUCCESS")
	notification, err := e.VerifyNotification(context.Background(), callback.Encode(), nil)
	require.Error(t, err, "a checkout signature must not authorize a successful payment notification")
	require.Nil(t, notification)
}

func TestEasyPayNotifyRejectsCompleteOrderReplay(t *testing.T) {
	t.Parallel()
	e := newEasyPayNotificationTestProvider(t)
	response, err := e.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID: "ORDER123", PaymentType: payment.TypeAlipay, Subject: "balance recharge", Amount: "15.25",
	})
	require.NoError(t, err)
	payURL, err := url.Parse(response.PayURL)
	require.NoError(t, err)
	notification, err := e.VerifyNotification(context.Background(), payURL.RawQuery, nil)
	require.Error(t, err)
	require.Nil(t, notification)
}

func signedEasyPayNotification(e *EasyPay, extra map[string]string) url.Values {
	params := map[string]string{
		"pid":          "1000",
		"trade_no":     "TRADE123",
		"out_trade_no": "ORDER123",
		"type":         payment.TypeAlipay,
		"name":         "balance recharge",
		"money":        "15.25",
		"trade_status": tradeStatusSuccess,
	}
	for key, value := range extra {
		params[key] = value
	}
	params["sign"] = easyPaySign(params, e.config["pkey"])
	params["sign_type"] = signTypeMD5
	query := make(url.Values, len(params))
	for key, value := range params {
		query.Set(key, value)
	}
	return query
}

func TestEasyPayNotifyAcceptsGenuineNotifications(t *testing.T) {
	t.Parallel()
	for _, param := range []string{"", "merchant reference&context=value"} {
		t.Run(param, func(t *testing.T) {
			t.Parallel()
			e := newEasyPayNotificationTestProvider(t)
			query := signedEasyPayNotification(e, map[string]string{"param": param})
			notification, err := e.VerifyNotification(context.Background(), query.Encode(), nil)
			require.NoError(t, err)
			require.NotNil(t, notification)
			require.Equal(t, payment.ProviderStatusSuccess, notification.Status)
			require.Equal(t, "ORDER123", notification.OrderID)
			require.Equal(t, "TRADE123", notification.TradeNo)
			require.Equal(t, 15.25, notification.Amount)
			require.Equal(t, "1000", notification.Metadata["pid"])
		})
	}
}

func TestEasyPayNotifyRejectsUnknownParameters(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"notify_url", "return_url", "cid", "device", "clientip", "extra"} {
		for _, value := range []string{"", "unexpected-value"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				t.Parallel()
				e := newEasyPayNotificationTestProvider(t)
				query := signedEasyPayNotification(e, map[string]string{key: value})
				notification, err := e.VerifyNotification(context.Background(), query.Encode(), nil)
				require.Error(t, err)
				require.Nil(t, notification)
			})
		}
	}
}

func TestEasyPayNotifyRejectsEncodedUnknownParameter(t *testing.T) {
	t.Parallel()
	e := newEasyPayNotificationTestProvider(t)
	query := signedEasyPayNotification(e, map[string]string{"return_url": ""})
	raw := strings.Replace(query.Encode(), "return_url=", "%72eturn_url=", 1)
	notification, err := e.VerifyNotification(context.Background(), raw, nil)
	require.Error(t, err)
	require.Nil(t, notification)
}
