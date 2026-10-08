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

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestHaiku55NativeRejectsUnsupportedSettings(t *testing.T) {
	for _, field := range []string{
		`"thinking":{"type":"enabled","budget_tokens":1024}`,
		`"thinking":{"type":"adaptive","budget_tokens":1024}`,
		`"thinking":{"type":"between_tools"}`,
		`"thinking":{"type":"disabled"},"output_config":{"effort":"xhigh"}`,
		`"thinking":{"type":"disabled"},"output_config":{"effort":"max"}`,
		`"thinking":{"type":"disabled"},"messages":[{"role":"user","content":"hello","output_config":{"effort":"low"}}]`,
		`"output_config":{"effort":"minimal"}`,
		`"temperature":0`, `"temperature":"1"`, `"top_p":1`, `"top_p":0.5`, `"top_k":0`,
		`"temperature":1,"top_p":0.99`,
		`"messages":[{"role":"assistant","content":"prefill"}]`,
	} {
		t.Run(field, func(t *testing.T) {
			require.Error(t, validateClaudeModelRequest([]byte(`{`+field+`}`), "claude-haiku-5-5"))
		})
	}
}

func TestHaiku55NativeAllowsForcedToolsAndDisabledThinking(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"temperature":1}`, `{"top_p":0.99}`,
		`{"tool_choice":{"type":"any"}}`, `{"tool_choice":{"type":"tool","name":"lookup"}}`,
		`{"tool_choice":"required"}`, `{"tool_choice":{"type":"function","function":{"name":"lookup"}}}`,
		`{"thinking":{"type":"adaptive","display":"summarized"},"output_config":{"effort":"max"}}`,
		`{"thinking":{"type":"disabled"}}`,
		`{"thinking":{"type":"disabled"},"output_config":{"effort":"low"}}`,
		`{"thinking":{"type":"disabled"},"output_config":{"effort":"high"}}`,
		`{"thinking":{"type":"disabled"},"messages":[{"role":"user","content":"hello","output_config":{"effort":"medium"}}]}`,
		`{"thinking":{"type":"adaptive"},"output_config":{"effort":"max"},"messages":[{"role":"user","content":"hello","output_config":{"effort":"low"}}]}`,
	} {
		require.NoError(t, validateClaudeModelRequest([]byte(body), "claude-haiku-5-5"), body)
	}
	for _, model := range []string{"claude-haiku-4-5", "claude-haiku-5", "claude-haiku-5-50", "claude-haiku-5-5-preview", "gpt-6"} {
		require.NoError(t, validateClaudeModelRequest([]byte(`{"thinking":{"type":"enabled","budget_tokens":1024},"temperature":0.3}`), model), model)
	}
}

func TestHaiku55MappedProvidersRejectBeforeForward(t *testing.T) {
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeServiceAccount, AccountTypeBedrock} {
		for _, endpoint := range []string{"messages", "messages/count_tokens"} {
			if accountType == AccountTypeBedrock && endpoint == "messages/count_tokens" {
				continue
			}
			for _, model := range []string{"claude-haiku-5-5", "anthropic.claude-haiku-5-5", "projects/test/locations/global/publishers/anthropic/models/claude-haiku-5-5"} {
				t.Run(accountType+"/"+endpoint+"/"+model, func(t *testing.T) {
					body := []byte(`{"model":"alias-haiku","thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"hello"}]}`)
					parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
					require.NoError(t, err)
					account := newAnthropicAPIKeyAccountForTest()
					account.Type = accountType
					account.Credentials["model_mapping"] = map[string]any{"alias-haiku": model}
					upstream := &anthropicHTTPUpstreamRecorder{err: fmt.Errorf("unexpected upstream call")}
					svc := newForwardPartialUsageServiceForTest(upstream)
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, nil)
					if endpoint == "messages" {
						_, err = svc.Forward(context.Background(), c, account, parsed)
					} else {
						err = svc.ForwardCountTokens(context.Background(), c, account, parsed)
					}
					require.Error(t, err)
					require.Equal(t, http.StatusBadRequest, c.Writer.Status())
					require.Nil(t, upstream.lastReq)
				})
			}
		}
	}
}

func TestHaiku55ImplicitThinkingAndExplicitDisabledState(t *testing.T) {
	for _, tt := range []struct {
		thinking string
		enabled  bool
	}{
		{"", true}, {`,"thinking":{"type":"adaptive"}`, true}, {`,"thinking":{"type":"disabled"}`, false},
	} {
		body := []byte(`{"model":"claude-haiku-5-5","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"signed-progress"},{"type":"redacted_thinking","data":"opaque-progress"},{"type":"text","text":"answer"}]},{"role":"user","content":"continue"}]` + tt.thinking + `}`)
		parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
		require.NoError(t, err)
		require.Equal(t, tt.enabled, parsed.ThinkingEnabled, tt.thinking)
		out := FilterThinkingBlocks(body, "claude-haiku-5-5")
		require.Len(t, gjson.GetBytes(out, "messages.0.content").Array(), 3, tt.thinking)
		require.Equal(t, "signed-progress", gjson.GetBytes(out, "messages.0.content.0.signature").String())
		require.Equal(t, "opaque-progress", gjson.GetBytes(out, "messages.0.content.1.data").String())
	}
}

func TestHaiku55OAuthPreservesSamplingDefaultsAndForcedTools(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-5-5","top_p":0.99,"thinking":{"type":"disabled"},"tool_choice":{"type":"tool","name":"lookup"},"tools":[{"name":"lookup","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"hello"}]}`)
	out, model := normalizeClaudeOAuthRequestBody(body, "claude-haiku-5-5", claudeOAuthNormalizeOptions{})
	require.Equal(t, "claude-haiku-5-5", model)
	require.False(t, gjson.GetBytes(out, "temperature").Exists())
	require.Equal(t, 0.99, gjson.GetBytes(out, "top_p").Float())
	require.Equal(t, "tool", gjson.GetBytes(out, "tool_choice.type").String())
	require.Equal(t, "disabled", gjson.GetBytes(out, "thinking.type").String())
	require.NoError(t, validateClaudeModelRequest(out, model))
}

func TestHaiku55ProviderModelIDs(t *testing.T) {
	for _, region := range []string{"us-east-1", "eu-west-1", "ap-northeast-1"} {
		account := &Account{Platform: PlatformAnthropic, Type: AccountTypeBedrock, Credentials: map[string]any{"aws_region": region}}
		for _, requested := range []string{"claude-haiku-5-5", "anthropic.claude-haiku-5-5"} {
			model, ok := ResolveBedrockModelID(account, requested)
			require.True(t, ok, requested)
			require.Equal(t, "anthropic.claude-haiku-5-5", model)
		}
	}
	require.Equal(t, "claude-haiku-5-5", normalizeVertexAnthropicModelID("claude-haiku-5-5"))
	vertexBody, err := buildVertexAnthropicRequestBody([]byte(`{"model":"claude-haiku-5-5","thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(vertexBody, "model").Exists())
	require.Equal(t, "adaptive", gjson.GetBytes(vertexBody, "thinking.type").String())
	require.Equal(t, "max", gjson.GetBytes(vertexBody, "output_config.effort").String())
}

func TestHaiku55SignatureRetryPreservesDisabledThinking(t *testing.T) {
	for name, filter := range map[string]func([]byte, string) []byte{
		"thinking": FilterThinkingBlocksForRetry, "signature": FilterSignatureSensitiveBlocksForRetry,
	} {
		for _, content := range []string{
			`[{"type":"text","text":"answer"}]`,
			`[{"type":"thinking","thinking":"progress","signature":"invalid-signature"},{"type":"redacted_thinking","data":"invalid-opaque"},{"type":"text","text":"answer"}]`,
		} {
			t.Run(name+"/"+content, func(t *testing.T) {
				body := []byte(`{"model":"claude-haiku-5-5","thinking":{"type":"disabled"},"output_config":{"effort":"low"},"messages":[{"role":"assistant","content":` + content + `},{"role":"user","content":"continue"}]}`)
				out := filter(body, "claude-haiku-5-5")
				require.True(t, json.Valid(out))
				require.JSONEq(t, `{"type":"disabled"}`, gjson.GetBytes(out, "thinking").Raw)
				require.Equal(t, "low", gjson.GetBytes(out, "output_config.effort").String())
				require.NotContains(t, string(out), "invalid-signature")
				require.NotContains(t, string(out), "invalid-opaque")
				require.NoError(t, validateClaudeModelRequest(out, "claude-haiku-5-5"))
			})
		}
	}
}

func TestHaiku55MappedCompatibilityRequestsAllowForcedTools(t *testing.T) {
	const payload = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_haiku55\",\"model\":\"claude-haiku-5-5\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":11}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool_1\",\"name\":\"lookup\",\"input\":{}}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":5}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	for _, endpoint := range []string{"responses", "chat/completions"} {
		for _, stream := range []bool{false, true} {
			for _, effort := range []string{"", "none"} {
				t.Run(fmt.Sprintf("%s/stream=%t/effort=%s", endpoint, stream, effort), func(t *testing.T) {
					upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(payload))}}
					account := newAnthropicAPIKeyAccountForTest()
					account.Credentials["model_mapping"] = map[string]any{"custom-haiku": "claude-haiku-5-5"}
					svc := newForwardPartialUsageServiceForTest(upstream)
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, nil)
					var result *ForwardResult
					var err error
					if endpoint == "responses" {
						body := []byte(fmt.Sprintf(`{"model":"custom-haiku","stream":%t,"input":"hello","reasoning":{"effort":"%s"},"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],"tool_choice":"required"}`, stream, effort))
						result, err = svc.ForwardAsResponses(context.Background(), c, account, body, nil)
					} else {
						body := []byte(fmt.Sprintf(`{"model":"custom-haiku","stream":%t,"messages":[{"role":"user","content":"hello"}],"reasoning_effort":"%s","tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"tool_choice":"required"}`, stream, effort))
						result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, nil)
					}
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, "custom-haiku", result.Model)
					require.Equal(t, "claude-haiku-5-5", result.UpstreamModel)
					require.Equal(t, "claude-haiku-5-5", result.UpstreamResponseModel)
					require.Equal(t, "claude-haiku-5-5", gjson.GetBytes(upstream.lastBody, "model").String())
					wantThinking := "adaptive"
					if effort == "none" {
						wantThinking = "disabled"
					}
					require.Equal(t, wantThinking, gjson.GetBytes(upstream.lastBody, "thinking.type").String())
					require.Equal(t, "any", gjson.GetBytes(upstream.lastBody, "tool_choice.type").String())
					require.NotNil(t, result.ReasoningEffort)
					require.Equal(t, "medium", *result.ReasoningEffort)
					require.Equal(t, 11, result.Usage.InputTokens)
					require.Equal(t, 5, result.Usage.OutputTokens)
					require.Contains(t, recorder.Body.String(), "lookup")
				})
			}
		}
	}
}
