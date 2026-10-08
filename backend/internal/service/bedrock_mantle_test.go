package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBedrockMantle55RequestBody(t *testing.T) {
	for _, model := range []string{"anthropic.claude-haiku-5-5", "anthropic.claude-sonnet-5-5"} {
		t.Run(model, func(t *testing.T) {
			body := []byte(`{"model":"client-alias","stream":true,"thinking":{"type":"adaptive"},"output_config":{"effort":"high"},"system":[{"type":"text","text":"unchanged","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"signed-prefix"}]},{"role":"user","content":"continue"}],"fallbacks":"default","fallback_credit_token":"credit"}`)
			out, err := PrepareBedrockRequestBodyWithTokens(body, model, nil, false)
			require.NoError(t, err)
			require.Equal(t, model, gjson.GetBytes(out, "model").String())
			require.True(t, gjson.GetBytes(out, "stream").Bool())
			require.Equal(t, "high", gjson.GetBytes(out, "output_config.effort").String())
			require.Equal(t, "1h", gjson.GetBytes(out, "system.0.cache_control.ttl").String())
			require.Equal(t, "signed-prefix", gjson.GetBytes(out, "messages.0.content.0.signature").String())
			for _, field := range []string{"anthropic_version", "anthropic_beta", "fallbacks", "fallback_credit_token"} {
				require.False(t, gjson.GetBytes(out, field).Exists(), field)
			}
		})
	}
}

func TestBedrockMantle55URLAndAuthentication(t *testing.T) {
	svc := &GatewayService{}
	for _, stream := range []bool{false, true} {
		for _, model := range []string{"anthropic.claude-haiku-5-5", "anthropic.claude-sonnet-5-5"} {
			t.Run(fmt.Sprintf("%s/%t", model, stream), func(t *testing.T) {
				require.Equal(t, "https://bedrock-mantle.eu-west-1.api.aws/anthropic/v1/messages", BuildBedrockURL("eu-west-1", model, stream))
				body := []byte(`{"model":"` + model + `","messages":[]}`)
				signer := NewBedrockSigner("test-id", "test-secret", "test-session", "eu-west-1")
				req, err := svc.buildUpstreamRequestBedrock(context.Background(), body, model, "eu-west-1", stream, signer)
				require.NoError(t, err)
				require.Contains(t, req.Header.Get("Authorization"), "/eu-west-1/bedrock-mantle/aws4_request")
				require.Equal(t, "2023-06-01", req.Header.Get("Anthropic-Version"))
				require.Equal(t, "test-session", req.Header.Get("X-Amz-Security-Token"))
				if stream {
					require.Equal(t, "text/event-stream", req.Header.Get("Accept"))
				}
				req, err = svc.buildUpstreamRequestBedrockAPIKey(context.Background(), body, model, "eu-west-1", stream, "test-token")
				require.NoError(t, err)
				require.Equal(t, "2023-06-01", req.Header.Get("Anthropic-Version"))
				require.Equal(t, "Bearer test-token", req.Header.Get("Authorization"))
			})
		}
	}
	legacy := "us.anthropic.claude-sonnet-4-5-20250929-v1:0"
	require.Contains(t, BuildBedrockURL("us-east-1", legacy, true), "bedrock-runtime.us-east-1.amazonaws.com/model/")
	req, err := svc.buildUpstreamRequestBedrock(context.Background(), []byte(`{}`), legacy, "us-east-1", false, NewBedrockSigner("id", "secret", "", "us-east-1"))
	require.NoError(t, err)
	require.Contains(t, req.Header.Get("Authorization"), "/us-east-1/bedrock/aws4_request")
	require.Empty(t, req.Header.Get("Anthropic-Version"))
}

func TestBedrockMantle55PreservesNativeBetaTokens(t *testing.T) {
	const beta = "thinking-binding-controls-2026-08-01"
	body := []byte(`{"model":"claude-haiku-5-5","thinking":{"type":"adaptive","block_binding":{"prefix_mismatch_behavior":"error"}},"messages":[{"role":"user","content":"hi"}]}`)
	svc := &GatewayService{}
	account := &Account{Platform: PlatformAnthropic, Type: AccountTypeBedrock}
	tokens, err := svc.resolveBedrockBetaTokensForRequest(context.Background(), account, beta, body, "anthropic.claude-haiku-5-5")
	require.NoError(t, err)
	require.Equal(t, []string{beta}, tokens)
	out, err := PrepareBedrockRequestBodyWithTokens(body, "anthropic.claude-haiku-5-5", tokens, false)
	require.NoError(t, err)
	require.Equal(t, "error", gjson.GetBytes(out, "thinking.block_binding.prefix_mismatch_behavior").String())
}

func TestBedrockMantle55ForwardJSONAndSSE(t *testing.T) {
	for _, mode := range []string{"json", "stream", "partial"} {
		t.Run(mode, func(t *testing.T) {
			stream := mode != "json"
			body := []byte(fmt.Sprintf(`{"model":"claude-haiku-5-5","stream":%t,"max_tokens":128,"thinking":{"type":"adaptive"},"output_config":{"effort":"medium"},"messages":[{"role":"user","content":"hello"}]}`, stream))
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
			require.NoError(t, err)
			payload := `{"type":"message","id":"msg_mantle","model":"anthropic.claude-haiku-5-5","content":[{"type":"thinking","thinking":"","signature":"signed-result"},{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":9,"output_tokens":7,"cache_read_input_tokens":11,"cache_creation_input_tokens":5,"cache_creation":{"ephemeral_5m_input_tokens":2,"ephemeral_1h_input_tokens":3}}}`
			contentType := "application/json"
			if stream {
				contentType = "text/event-stream"
				payload = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_mantle\",\"model\":\"anthropic.claude-haiku-5-5\",\"usage\":{\"input_tokens\":9,\"cache_read_input_tokens\":11,\"cache_creation_input_tokens\":5,\"cache_creation\":{\"ephemeral_5m_input_tokens\":2,\"ephemeral_1h_input_tokens\":3}}}}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\",\"signature\":\"signed-result\"}}\n\n" +
					"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}\n\n"
				if mode == "stream" {
					payload += "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
				}
			}
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}, "Request-Id": {"req_mantle"}}, Body: io.NopCloser(strings.NewReader(payload))}}
			svc := newForwardPartialUsageServiceForTest(upstream)
			account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeBedrock, Credentials: map[string]any{"auth_mode": "apikey", "api_key": "test-token", "aws_region": "us-east-1", "model_mapping": map[string]any{"claude-haiku-5-5": "anthropic.claude-haiku-5-5"}}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			c.Request.Header.Set("Anthropic-Beta", "thinking-binding-controls-2026-08-01")
			accepted := 0
			parsed.OnUpstreamAccepted = func() { accepted++ }
			result, err := svc.forwardBedrock(context.Background(), c, account, parsed, time.Now())
			if mode == "partial" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.NotNil(t, result)
			require.Equal(t, 1, accepted)
			require.Equal(t, "req_mantle", result.RequestID)
			require.Equal(t, 9, result.Usage.InputTokens)
			require.Equal(t, 7, result.Usage.OutputTokens)
			require.Equal(t, 11, result.Usage.CacheReadInputTokens)
			require.Equal(t, 2, result.Usage.CacheCreation5mTokens)
			require.Equal(t, 3, result.Usage.CacheCreation1hTokens)
			require.Equal(t, "anthropic.claude-haiku-5-5", result.UpstreamModel)
			require.Equal(t, "bedrock-mantle.us-east-1.api.aws", upstream.lastReq.URL.Host)
			require.Equal(t, "thinking-binding-controls-2026-08-01", upstream.lastReq.Header.Get("Anthropic-Beta"))
			require.Equal(t, "medium", gjson.GetBytes(upstream.lastBody, "output_config.effort").String())
			require.Contains(t, recorder.Body.String(), "signed-result")
		})
	}
}

func TestBedrockMantle55SSEErrorLifecycle(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(fmt.Sprintf("started=%t", started), func(t *testing.T) {
			const errorJSON = `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`
			payload := ""
			if started {
				payload = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"anthropic.claude-haiku-5-5\",\"usage\":{\"input_tokens\":9}}}\n\n"
			}
			payload += "event: error\ndata: " + errorJSON + "\n\n"
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}, "Request-Id": {"req_mantle_error"}},
				Body:       io.NopCloser(strings.NewReader(payload)),
			}}
			svc := newForwardPartialUsageServiceForTest(upstream)
			account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeBedrock, Credentials: map[string]any{"auth_mode": "apikey", "api_key": "test-token"}}
			repo := &gatewayForwardErrorPolicyRepoStub{}
			svc.rateLimitService = NewRateLimitService(repo, nil, svc.cfg, nil, nil)
			account.Credentials["temp_unschedulable_enabled"] = true
			account.Credentials["temp_unschedulable_rules"] = []any{map[string]any{
				"error_code": float64(529), "keywords": []any{"Overloaded"}, "duration_minutes": float64(10),
			}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(`{"model":"claude-haiku-5-5","stream":true,"max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`)), PlatformAnthropic)
			require.NoError(t, err)
			result, err := svc.forwardBedrock(context.Background(), c, account, parsed, time.Now())
			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.Nil(t, result, "failover attempts must not produce a second billable result")
			require.JSONEq(t, errorJSON, string(failoverErr.ResponseBody))
			if started {
				require.Equal(t, http.StatusForbidden, failoverErr.StatusCode)
				require.Empty(t, repo.modelRateLimitCalls)
				require.Contains(t, recorder.Body.String(), "message_start", "written output prevents the handler from retrying")
			} else {
				require.Equal(t, 529, failoverErr.StatusCode)
				require.Len(t, repo.modelRateLimitCalls, 1, "overload before output participates in account cooldown policy")
				require.Empty(t, recorder.Body.String(), "overload before output remains eligible for failover")
			}
		})
	}
}
