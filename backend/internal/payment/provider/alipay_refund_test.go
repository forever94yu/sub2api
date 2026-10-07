//go:build unit

package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/smartwalle/alipay/v3"
	"github.com/stretchr/testify/require"
)

type alipayRefundTransport func(*http.Request) (*http.Response, error)

func (f alipayRefundTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newAlipayRefundHTTPTestProvider(t *testing.T, respond func(string, map[string]string) map[string]string) *Alipay {
	t.Helper()
	privateKey, publicKey := generateTestKeyPair(t)
	client, err := alipay.New("refund-test-app", privateKey, true)
	require.NoError(t, err)
	require.NoError(t, client.LoadAliPayPublicKey(publicKey))
	client.Client = &http.Client{Transport: alipayRefundTransport(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, req.Method)
		require.NoError(t, req.ParseForm())
		require.Equal(t, "refund-test-app", req.Form.Get("app_id"))
		requestSignature := req.Form.Get("sign")
		req.Form.Del("sign")
		expectedSignature, err := client.SignValues(req.Form)
		require.NoError(t, err)
		require.Equal(t, base64.StdEncoding.EncodeToString(expectedSignature), requestSignature)
		var business map[string]string
		require.NoError(t, json.Unmarshal([]byte(req.Form.Get("biz_content")), &business))
		method := req.Form.Get("method")
		payload, err := json.Marshal(respond(method, business))
		require.NoError(t, err)
		signature, err := client.SignBytes(payload)
		require.NoError(t, err)
		body, err := json.Marshal(map[string]any{
			strings.ReplaceAll(method, ".", "_") + "_response": json.RawMessage(payload),
			"sign": base64.StdEncoding.EncodeToString(signature),
		})
		require.NoError(t, err)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
			Request:    req,
		}, nil
	})}
	return &Alipay{client: client, config: map[string]string{"appId": "refund-test-app"}}
}

func TestAlipayRefundPreservesRequestIDForQuery(t *testing.T) {
	const orderID = "sub2_refund_pending"
	const tradeNo = "alipay-original-trade"
	var requestID string
	var calls []string
	provider := newAlipayRefundHTTPTestProvider(t, func(method string, business map[string]string) map[string]string {
		calls = append(calls, method)
		require.Equal(t, orderID, business["out_trade_no"])
		switch method {
		case "alipay.trade.refund":
			requestID = business["out_request_no"]
			require.NotEmpty(t, requestID)
			require.Equal(t, "35.50", business["refund_amount"])
			return map[string]string{
				"code": "10000", "trade_no": tradeNo, "out_trade_no": orderID,
				"fund_change": "N", "refund_fee": "35.50",
			}
		case "alipay.trade.fastpay.refund.query":
			require.Equal(t, requestID, business["out_request_no"])
			require.Equal(t, tradeNo, business["trade_no"])
			return map[string]string{
				"code": "10000", "trade_no": tradeNo, "out_trade_no": orderID,
				"out_request_no": requestID, "refund_amount": "35.5", "refund_status": "REFUND_SUCCESS",
			}
		default:
			t.Fatalf("unexpected Alipay method: %s", method)
			return nil
		}
	})

	resp, err := provider.Refund(context.Background(), payment.RefundRequest{
		TradeNo: tradeNo, OrderID: orderID, Amount: "35.50", Reason: "test refund",
	})
	require.NoError(t, err)
	require.Equal(t, payment.ProviderStatusPending, resp.Status)
	require.Equal(t, requestID, resp.RefundID)
	queryProvider, ok := any(provider).(payment.RefundQueryProvider)
	require.True(t, ok, "pending Alipay refunds must support confirmation")
	confirmed, err := queryProvider.QueryRefund(context.Background(), payment.RefundQueryRequest{
		TradeNo: tradeNo, OrderID: orderID, RefundID: resp.RefundID, Amount: "35.50",
	})
	require.NoError(t, err)
	require.Equal(t, payment.ProviderStatusSuccess, confirmed.Status)
	require.Equal(t, requestID, confirmed.RefundID)
	require.Equal(t, []string{"alipay.trade.refund", "alipay.trade.fastpay.refund.query"}, calls)
}

func TestAlipayQueryRefundValidatesSignedResult(t *testing.T) {
	for _, tc := range []struct {
		name   string
		field  string
		value  string
		status string
		err    string
	}{
		{name: "success", status: payment.ProviderStatusSuccess},
		{name: "processing", field: "refund_status", value: "REFUND_PROCESSING", status: payment.ProviderStatusPending},
		{name: "missing status is ambiguous", field: "refund_status", value: "", status: payment.ProviderStatusPending},
		{name: "unknown status is pending", field: "refund_status", value: "FUTURE_STATUS", status: payment.ProviderStatusPending},
		{name: "confirmed failure", field: "refund_status", value: "REFUND_FAIL", status: payment.ProviderStatusFailed},
		{name: "query failure is not a refund failure", field: "code", value: "40004", err: "refund query"},
		{name: "wrong order", field: "out_trade_no", value: "another-order", err: "order mismatch"},
		{name: "wrong trade", field: "trade_no", value: "another-trade", err: "trade mismatch"},
		{name: "wrong refund", field: "out_request_no", value: "another-refund", err: "request mismatch"},
		{name: "wrong amount", field: "refund_amount", value: "35.49", err: "amount mismatch"},
		{name: "missing amount", field: "refund_amount", value: "", err: "amount"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := newAlipayRefundHTTPTestProvider(t, func(method string, business map[string]string) map[string]string {
				require.Equal(t, "alipay.trade.fastpay.refund.query", method)
				require.Equal(t, "order-refund-1234", business["out_request_no"])
				result := map[string]string{
					"code": "10000", "trade_no": "trade", "out_trade_no": "order",
					"out_request_no": "order-refund-1234", "refund_amount": "35.50", "refund_status": "REFUND_SUCCESS",
				}
				if tc.field != "" {
					result[tc.field] = tc.value
				}
				return result
			})
			queryProvider, ok := any(provider).(payment.RefundQueryProvider)
			require.True(t, ok, "pending Alipay refunds must support confirmation")
			resp, err := queryProvider.QueryRefund(context.Background(), payment.RefundQueryRequest{
				TradeNo: "trade", OrderID: "order", RefundID: "order-refund-1234", Amount: "35.50",
			})
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				require.Nil(t, resp)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.status, resp.Status)
		})
	}
}

func TestAlipayQueryRefundRejectsMissingLegacyRequestID(t *testing.T) {
	for _, refundID := range []string{"", "trade", "order-refund"} {
		t.Run(refundID, func(t *testing.T) {
			provider := newAlipayRefundHTTPTestProvider(t, func(string, map[string]string) map[string]string {
				t.Fatal("must not query with a missing original refund request ID")
				return nil
			})
			queryProvider, ok := any(provider).(payment.RefundQueryProvider)
			require.True(t, ok)
			resp, err := queryProvider.QueryRefund(context.Background(), payment.RefundQueryRequest{
				TradeNo: "trade", OrderID: "order", RefundID: refundID, Amount: "35.50",
			})
			require.Nil(t, resp)
			require.ErrorContains(t, err, "original refund request ID")
		})
	}
}
