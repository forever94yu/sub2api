package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClaudeUpdateBetaFieldsFollowFinalHeader(t *testing.T) {
	const body = `{"model":"claude-opus-5","context_management":{"edits":[]},"fallbacks":"default","fallback_credit_token":"credit","thinking":{"type":"adaptive","block_binding":"none"},"output_config":{"effort":"high"},"messages":[{"role":"system","content":[],"output_config":{"effort":"high"}},{"role":"user","content":"hello","output_config":{"effort":"low"}}]}`
	tests := []struct {
		name, header                                      string
		context, fallback, credit, binding, messageConfig bool
	}{
		{name: "no beta"},
		{name: "context only", header: "context-management-2025-06-27", context: true},
		{name: "fallback only", header: "server-side-fallback-2026-07-01", fallback: true, credit: true},
		{name: "credit only", header: "fallback-credit-2026-07-01", credit: true},
		{name: "thinking binding", header: "thinking-binding-controls-2026-08-01", binding: true},
		{name: "message config", header: "mid-conversation-output-config-2026-07-01", messageConfig: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, changed := sanitizeAnthropicBodyForBetaTokens([]byte(body), tt.header)
			require.True(t, changed)
			require.True(t, json.Valid(out))
			require.Equal(t, tt.context, gjson.GetBytes(out, "context_management").Exists())
			require.Equal(t, tt.fallback, gjson.GetBytes(out, "fallbacks").Exists())
			require.Equal(t, tt.credit, gjson.GetBytes(out, "fallback_credit_token").Exists())
			require.Equal(t, tt.binding, gjson.GetBytes(out, "thinking.block_binding").Exists())
			require.Equal(t, "high", gjson.GetBytes(out, "output_config.effort").String())
			messages := gjson.GetBytes(out, "messages").Array()
			if tt.messageConfig {
				require.Len(t, messages, 2)
				require.True(t, messages[0].Get("output_config").Exists())
			} else {
				require.Len(t, messages, 1)
				require.Equal(t, "user", messages[0].Get("role").String())
				require.Equal(t, "hello", messages[0].Get("content").String())
				require.False(t, messages[0].Get("output_config").Exists())
			}
		})
	}
}

func TestClaudeUpdateBedrockStripsUnsupportedFallbackFields(t *testing.T) {
	body := []byte(`{"model":"claude-opus-5","fallbacks":"default","fallback_credit_token":"credit","messages":[{"role":"user","content":"hello"}]}`)
	for name, out := range map[string][]byte{
		"invoke":      sanitizeBedrockFieldsForBetaTokens(body, nil),
		"claude code": sanitizeBedrockCCFields(body),
	} {
		t.Run(name, func(t *testing.T) {
			require.False(t, gjson.GetBytes(out, "fallbacks").Exists())
			require.False(t, gjson.GetBytes(out, "fallback_credit_token").Exists())
			require.Equal(t, "hello", gjson.GetBytes(out, "messages.0.content").String())
		})
	}
}

func TestClaudeUpdateOpus55ImplicitThinkingPreservesSignedHistory(t *testing.T) {
	body := []byte(`{"model":"claude-opus-5-5","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"reason","signature":"valid-signature"},{"type":"redacted_thinking","data":"opaque-data"},{"type":"text","text":"answer"}]}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)
	require.True(t, parsed.ThinkingEnabled)
	out := FilterThinkingBlocks(body, "claude-opus-5-5")
	require.Len(t, gjson.GetBytes(out, "messages.0.content").Array(), 3)
	require.Equal(t, "valid-signature", gjson.GetBytes(out, "messages.0.content.0.signature").String())
	require.Equal(t, "opaque-data", gjson.GetBytes(out, "messages.0.content.1.data").String())
	legacy := FilterThinkingBlocks(body, "claude-opus-5")
	require.Len(t, gjson.GetBytes(legacy, "messages.0.content").Array(), 1)
}

func TestClaudeUpdateOpus55OAuthDoesNotInjectSampling(t *testing.T) {
	body := []byte(`{"model":"claude-opus-5-5","messages":[{"role":"user","content":"hello"}],"tool_choice":{"type":"auto"}}`)
	out, _ := normalizeClaudeOAuthRequestBody(body, "claude-opus-5-5", claudeOAuthNormalizeOptions{})
	require.False(t, gjson.GetBytes(out, "temperature").Exists())
	require.Equal(t, "auto", gjson.GetBytes(out, "tool_choice.type").String())
}

func TestClaudeUpdateStreamingCacheZeroReplacesPreviousBucket(t *testing.T) {
	const delta = `{"type":"message_delta","usage":{"output_tokens":9,"cache_creation_input_tokens":6,"cache_creation":{"ephemeral_5m_input_tokens":6,"ephemeral_1h_input_tokens":0}}}`
	for _, passthrough := range []bool{false, true} {
		usage := &ClaudeUsage{InputTokens: 11, CacheReadInputTokens: 7, CacheCreationInputTokens: 20, CacheCreation1hTokens: 20}
		if passthrough {
			parseSSEUsagePassthrough(delta, usage)
		} else {
			var event map[string]any
			require.NoError(t, json.Unmarshal([]byte(delta), &event))
			mergeSSEUsagePatch(usage, (&GatewayService{}).extractSSEUsagePatch(event))
		}
		require.Equal(t, 0, usage.CacheCreation1hTokens, "passthrough=%v", passthrough)
		require.Equal(t, 6, usage.CacheCreation5mTokens)
		require.Equal(t, 6, usage.CacheCreationInputTokens)
		require.Equal(t, 11, usage.InputTokens)
		require.Equal(t, 7, usage.CacheReadInputTokens)
		require.Equal(t, 9, usage.OutputTokens)
	}
}

func TestClaudeUpdateBillingFingerprintFollowsUserAgent(t *testing.T) {
	body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.81.aaa; cc_entrypoint=cli; cch=00000;"}],"messages":[{"role":"user","content":"hello"}]}`)
	out := syncBillingHeaderVersion(body, "claude-cli/2.1.200 (external, cli)")
	require.Contains(t, gjson.GetBytes(out, "system.0.text").String(), "cc_version=2.1.200."+computeClaudeCodeFingerprint(body, "2.1.200"))
}

func TestClaudeUpdateForwardRetainsBillingMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, credential := range []string{"oauth", "apikey"} {
		for _, responseMode := range []string{"json", "stream", "partial"} {
			t.Run(credential+"/"+responseMode, func(t *testing.T) {
				stream := responseMode != "json"
				body := []byte(fmt.Sprintf(`{"model":"claude-opus-5-5","stream":%t,"speed":"fast","inference_geo":"us","messages":[{"role":"user","content":"hello"}]}`, stream))
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
				require.NoError(t, err)
				payload := `{"type":"message","id":"msg_test","model":"claude-opus-5-5","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":11,"output_tokens":5,"speed":"standard","inference_geo":"global"}}`
				contentType := "application/json"
				if stream {
					contentType = "text/event-stream"
					payload = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-opus-5-5\",\"content\":[],\"usage\":{\"input_tokens\":11,\"speed\":\"fast\",\"inference_geo\":\"us\"}}}\n\n" +
						"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":5,\"speed\":\"standard\",\"inference_geo\":\"global\"}}\n\n"
					if responseMode == "stream" {
						payload += "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
					}
				}
				upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(payload))}}
				svc := newForwardPartialUsageServiceForTest(upstream)
				account := newAnthropicOAuthAccountForPartialUsageTest()
				if credential == "apikey" {
					account = newAnthropicAPIKeyAccountForTest()
				}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				result, err := svc.Forward(context.Background(), c, account, parsed)
				if responseMode == "partial" {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				require.NotNil(t, result)
				require.NotNil(t, result.ServiceTier)
				require.Equal(t, "fast", *result.ServiceTier)
				require.Equal(t, "standard", result.UpstreamResponseServiceTier)
				require.Equal(t, "us", result.InferenceGeo)
				require.Equal(t, "global", result.UpstreamResponseInferenceGeo)
				require.Equal(t, 11, result.Usage.InputTokens)
				require.Equal(t, 5, result.Usage.OutputTokens)
			})
		}
	}
}

func TestClaudeUpdateOpus55RejectsIncompatibleSettingsBeforeForward(t *testing.T) {
	for _, field := range []string{`"thinking":{"type":"disabled"}`, `"thinking":{"type":"enabled","budget_tokens":1024}`, `"tool_choice":{"type":"any"}`, `"tool_choice":"required"`} {
		t.Run(field, func(t *testing.T) {
			body := []byte(`{"model":"custom-alias",` + field + `,"messages":[{"role":"user","content":"hello"}]}`)
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
			require.NoError(t, err)
			account := newAnthropicAPIKeyAccountForTest()
			account.Credentials["model_mapping"] = map[string]any{"custom-alias": "claude-opus-5-5"}
			upstream := &anthropicHTTPUpstreamRecorder{err: fmt.Errorf("should never reach upstream")}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			result, err := newForwardPartialUsageServiceForTest(upstream).Forward(context.Background(), c, account, parsed)
			require.Error(t, err)
			require.Nil(t, result)
			require.Equal(t, http.StatusBadRequest, c.Writer.Status())
			require.Nil(t, upstream.lastReq)
		})
	}
}

func TestClaudeUpdateCompatibilityStreamPreservesCacheTTL(t *testing.T) {
	usage := &ClaudeUsage{}
	var start, delta apicompat.AnthropicUsage
	require.NoError(t, json.Unmarshal([]byte(`{"input_tokens":11,"cache_read_input_tokens":7,"cache_creation_input_tokens":20,"cache_creation":{"ephemeral_1h_input_tokens":20}}`), &start))
	mergeAnthropicUsage(usage, start)
	require.Equal(t, 20, usage.CacheCreation1hTokens)
	require.NoError(t, json.Unmarshal([]byte(`{"output_tokens":5,"cache_creation_input_tokens":6,"cache_creation":{"ephemeral_5m_input_tokens":6,"ephemeral_1h_input_tokens":0}}`), &delta))
	mergeAnthropicUsage(usage, delta)
	require.Equal(t, 11, usage.InputTokens)
	require.Equal(t, 7, usage.CacheReadInputTokens)
	require.Equal(t, 6, usage.CacheCreationInputTokens)
	require.Equal(t, 0, usage.CacheCreation1hTokens)
	require.Equal(t, 6, usage.CacheCreation5mTokens)
}

func TestClaudeUpdateMappedCompatibilityRequest(t *testing.T) {
	for _, endpoint := range []string{"responses", "chat/completions"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", endpoint, stream), func(t *testing.T) {
				const payload = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"model\":\"claude-opus-5-5\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":11,\"cache_creation_input_tokens\":20,\"cache_creation\":{\"ephemeral_1h_input_tokens\":20},\"inference_geo\":\"global\"}}}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"reason\"}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"signed-reason\"}}\n\n" +
					"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"answer\"}}\n\n" +
					"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
					"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
				upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(payload))}}
				account := newAnthropicAPIKeyAccountForTest()
				account.Credentials["model_mapping"] = map[string]any{"custom-alias": "claude-opus-5-5"}
				svc := newForwardPartialUsageServiceForTest(upstream)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, nil)
				var result *ForwardResult
				var err error
				if endpoint == "responses" {
					body := []byte(fmt.Sprintf(`{"model":"custom-alias","stream":%t,"input":"hello"}`, stream))
					result, err = svc.ForwardAsResponses(context.Background(), c, account, body, nil)
				} else {
					body := []byte(fmt.Sprintf(`{"model":"custom-alias","stream":%t,"messages":[{"role":"user","content":"hello"}]}`, stream))
					result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, nil)
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, "custom-alias", result.Model)
				require.Equal(t, "claude-opus-5-5", result.UpstreamModel)
				require.Equal(t, "claude-opus-5-5", result.UpstreamResponseModel)
				require.False(t, result.UpstreamResponseModelConflict)
				require.Equal(t, "claude-opus-5-5", gjson.GetBytes(upstream.lastBody, "model").String())
				require.Equal(t, "adaptive", gjson.GetBytes(upstream.lastBody, "thinking.type").String())
				require.NotNil(t, result.ReasoningEffort)
				require.Equal(t, "medium", *result.ReasoningEffort)
				require.Equal(t, 20, result.Usage.CacheCreation1hTokens)
				require.Equal(t, 0, result.Usage.CacheCreation5mTokens)
				require.Equal(t, "global", result.UpstreamResponseInferenceGeo)
				require.Contains(t, recorder.Body.String(), "answer")
				if endpoint == "responses" {
					require.Contains(t, recorder.Body.String(), "anthropic-thinking-v1:")
				}
			})
		}
	}
}

func TestClaudeUpdateCompatibilityResponseModelConflictOnPartialStream(t *testing.T) {
	for _, endpoint := range []string{"responses", "chat/completions"} {
		t.Run(endpoint, func(t *testing.T) {
			const payload = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"model\":\"claude-opus-5-5\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":11}}}\n\n" +
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"model\":\"claude-sonnet-4-6\",\"usage\":{\"output_tokens\":5}}\n\n"
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(payload))}}
			account := newAnthropicAPIKeyAccountForTest()
			account.Credentials["model_mapping"] = map[string]any{"custom-alias": "claude-opus-5-5"}
			svc := newForwardPartialUsageServiceForTest(upstream)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, nil)
			body := []byte(`{"model":"custom-alias","stream":true,"input":"hello","messages":[{"role":"user","content":"hello"}]}`)
			var result *ForwardResult
			var err error
			if endpoint == "responses" {
				result, err = svc.ForwardAsResponses(context.Background(), c, account, body, nil)
			} else {
				result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, nil)
			}
			require.Error(t, err)
			require.NotNil(t, result)
			require.Equal(t, "custom-alias", result.Model)
			require.Equal(t, "claude-opus-5-5", result.UpstreamModel)
			require.Equal(t, "claude-opus-5-5", result.UpstreamResponseModel)
			require.True(t, result.UpstreamResponseModelConflict)
			require.Equal(t, 11, result.Usage.InputTokens)
			require.Equal(t, 5, result.Usage.OutputTokens)
		})
	}
}

func TestClaudeUpdateUnknownPriceFailsBeforeUpstream(t *testing.T) {
	for _, endpoint := range []string{"messages", "responses", "chat/completions"} {
		t.Run(endpoint, func(t *testing.T) {
			account := newAnthropicAPIKeyAccountForTest()
			upstream := &anthropicHTTPUpstreamRecorder{err: fmt.Errorf("must not call upstream")}
			svc := newForwardPartialUsageServiceForTest(upstream)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, nil)
			ctx, _ := WithGatewayTokenRequestPricing(context.Background())
			body := []byte(`{"model":"claude-future-unknown","messages":[{"role":"user","content":"hello"}],"input":"hello"}`)
			var result *ForwardResult
			var err error
			switch endpoint {
			case "messages":
				parsed, parseErr := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
				require.NoError(t, parseErr)
				result, err = svc.Forward(ctx, c, account, parsed)
			case "responses":
				result, err = svc.ForwardAsResponses(ctx, c, account, body, nil)
			case "chat/completions":
				result, err = svc.ForwardAsChatCompletions(ctx, c, account, body, nil)
			}
			require.ErrorIs(t, err, ErrModelPricingUnavailable)
			require.Nil(t, result)
			require.Nil(t, upstream.lastReq)
			require.Equal(t, http.StatusInternalServerError, recorder.Code)
			require.Contains(t, recorder.Body.String(), "pricing_error")
		})
	}
}

func TestClaudeUpdateBedrockStripsDirectAPIControls(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-6","max_tokens":128,"service_tier":"auto","speed":"fast","inference_geo":"us","thinking":{"type":"adaptive","block_binding":"none"},"messages":[{"role":"system","content":[],"output_config":{"effort":"high"}},{"role":"user","content":"hello"}]}`)
	for _, ccCompat := range []bool{false, true} {
		out, err := PrepareBedrockRequestBodyWithTokens(body, "us.anthropic.claude-opus-4-6-v1", nil, ccCompat)
		require.NoError(t, err)
		for _, field := range []string{"service_tier", "speed", "inference_geo", "thinking.block_binding"} {
			require.False(t, gjson.GetBytes(out, field).Exists(), "ccCompat=%v field=%s", ccCompat, field)
		}
		require.Equal(t, "adaptive", gjson.GetBytes(out, "thinking.type").String())
		require.Len(t, gjson.GetBytes(out, "messages").Array(), 1)
		require.Equal(t, "hello", gjson.GetBytes(out, "messages.0.content").String())
	}
}
