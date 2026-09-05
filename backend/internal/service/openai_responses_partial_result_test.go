//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestForwardResponsesPartialStreamPreservesBillingMetadata(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, tc := range []struct {
			actualTier  string
			requestTier string
			wantTier    string
		}{
			{actualTier: "fast", requestTier: "default", wantTier: "priority"},
			{actualTier: "default", requestTier: "priority", wantTier: "default"},
		} {
			name := fmt.Sprintf("passthrough=%t/actual=%s", passthrough, tc.actualTier)
			t.Run(name, func(t *testing.T) {
				result, err, _ := forwardPartialResponsesStream(t, passthrough, tc.requestTier, failedPartialResponsesSSE(tc.actualTier, false))

				require.Error(t, err)
				require.Contains(t, err.Error(), "upstream response failed")
				require.NotNil(t, result)
				require.Equal(t, "resp_partial", result.ResponseID)
				require.Equal(t, 11, result.Usage.InputTokens)
				require.Equal(t, 3, result.Usage.OutputTokens)
				require.NotNil(t, result.ServiceTier)
				require.Equal(t, tc.wantTier, *result.ServiceTier)
				require.NotNil(t, result.ReasoningEffort)
				require.Equal(t, "high", *result.ReasoningEffort)
				require.Zero(t, result.ImageCount)
			})
		}
	}
}

func TestForwardResponsesImagePartialStreamPreservesBillingMetadata(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%t", passthrough), func(t *testing.T) {
			result, err, _ := forwardPartialResponsesStream(t, passthrough, "default", failedPartialResponsesSSE("fast", true))

			require.Error(t, err)
			require.NotNil(t, result)
			require.Equal(t, 11, result.Usage.InputTokens)
			require.Equal(t, 3, result.Usage.OutputTokens)
			require.NotNil(t, result.ServiceTier)
			require.Equal(t, "priority", *result.ServiceTier)
			require.Equal(t, 1, result.ImageCount)
			require.Equal(t, []string{"1024x1024"}, result.ImageOutputSizes)
		})
	}
}

func TestForwardResponsesTextPartialReadErrorReturnsBillingResult(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%t", passthrough), func(t *testing.T) {
			payload := strings.Join([]string{
				`data: {"type":"response.created","response":{"id":"resp_partial","model":"gpt-6-astra","service_tier":"fast"}}`,
				`data: {"type":"response.output_text.delta","delta":"partial"}`,
			}, "\n\n") + "\n\n"

			result, err, _ := forwardPartialResponsesStream(t, passthrough, "default", payload)

			require.ErrorIs(t, err, io.ErrUnexpectedEOF)
			require.NotNil(t, result)
			require.Equal(t, "resp_partial", result.ResponseID)
			require.NotNil(t, result.ServiceTier)
			require.Equal(t, "priority", *result.ServiceTier)
			require.Zero(t, result.ImageCount)
		})
	}
}

func TestForwardResponsesPartialStreamKeepsFailoverResultNil(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%t", passthrough), func(t *testing.T) {
			result, err, _ := forwardPartialResponsesStream(t, passthrough, "priority", partialResponsesWithoutTerminalSSE("fast"))

			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.Nil(t, result)
		})
	}
}

func TestForwardResponsesCyberPolicyResultRemainsNil(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%t", passthrough), func(t *testing.T) {
			payload := "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_cyber\",\"service_tier\":\"fast\",\"error\":{\"code\":\"cyber_policy\",\"message\":\"blocked\"},\"usage\":{\"input_tokens\":11,\"output_tokens\":3}}}\n\n"
			result, err, c := forwardPartialResponsesStream(t, passthrough, "priority", payload)

			require.Error(t, err)
			require.Nil(t, result)
			require.NotNil(t, GetOpsCyberPolicy(c))
		})
	}
}

func forwardPartialResponsesStream(t *testing.T, passthrough bool, requestTier, payload string) (*OpenAIForwardResult, error, *gin.Context) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	body := fmt.Sprintf(`{"model":"gpt-6-astra","service_tier":%q,"stream":true,"input":"hello","reasoning":{"effort":"high"}}`, requestTier)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	account := rawChatCompletionsTestAccount()
	if passthrough {
		account.Extra = map[string]any{"openai_passthrough": true}
	}
	svc := &OpenAIGatewayService{
		cfg: rawChatCompletionsTestConfig(),
		httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
				"x-request-id": []string{"req_partial"},
			},
			Body: &openAIChatStreamReadErrorCloser{payload: []byte(payload), err: io.ErrUnexpectedEOF},
		}},
	}

	result, err := svc.Forward(context.Background(), c, account, []byte(body))
	return result, err, c
}

func failedPartialResponsesSSE(serviceTier string, includeImage bool) string {
	lines := []string{
		fmt.Sprintf(`data: {"type":"response.created","response":{"id":"resp_partial","model":"gpt-6-astra","service_tier":%q}}`, serviceTier),
	}
	if includeImage {
		lines = append(lines, `data: {"type":"response.output_item.done","item":{"id":"ig_partial","type":"image_generation_call","result":"aW1hZ2U=","size":"1024x1024"}}`)
	} else {
		lines = append(lines, `data: {"type":"response.output_text.delta","delta":"partial"}`)
	}
	lines = append(lines, fmt.Sprintf(`data: {"type":"response.failed","response":{"id":"resp_partial","model":"gpt-6-astra","service_tier":%q,"error":{"code":"upstream_error","message":"partial failure"},"usage":{"input_tokens":11,"output_tokens":3}}}`, serviceTier))
	return strings.Join(lines, "\n\n") + "\n\n"
}

func partialResponsesWithoutTerminalSSE(serviceTier string) string {
	return fmt.Sprintf(`data: {"type":"response.created","response":{"id":"resp_partial","model":"gpt-6-astra","service_tier":%q}}`, serviceTier) + "\n\n"
}
