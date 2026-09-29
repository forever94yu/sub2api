package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSonnet55NativeRejectsInvalidSettingsBeforeForward(t *testing.T) {
	for _, endpoint := range []string{"messages", "messages/count_tokens"} {
		for _, field := range []string{
			`"thinking":{"type":"disabled"}`,
			`"thinking":{"type":"enabled","budget_tokens":1024}`,
			`"thinking":{"type":"between_tools","display":"omitted"}`,
			`"thinking":{"type":"between_tools","budget_tokens":0}`,
			`"thinking":{"type":"between_tools","block_binding":{}}`,
			`"thinking":{"type":"between_tools"},"output_config":{"effort":"xhigh"}`,
			`"thinking":{"type":"between_tools"},"output_config":{"effort":"max"}`,
			`"thinking":{"type":"between_tools"},"messages":[{"role":"user","content":"hello","output_config":{"effort":"low"}}]`,
			`"tool_choice":{"type":"any"}`,
			`"tool_choice":{"type":"tool","name":"lookup"}`,
		} {
			t.Run(endpoint+"/"+field, func(t *testing.T) {
				var fields map[string]json.RawMessage
				require.NoError(t, json.Unmarshal([]byte(`{`+field+`}`), &fields))
				fields["model"] = json.RawMessage(`"custom-sonnet"`)
				if _, exists := fields["messages"]; !exists {
					fields["messages"] = json.RawMessage(`[{"role":"user","content":"hello"}]`)
				}
				body, err := json.Marshal(fields)
				require.NoError(t, err)
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
				require.NoError(t, err)
				account := newAnthropicAPIKeyAccountForTest()
				account.Credentials["model_mapping"] = map[string]any{"custom-sonnet": "claude-sonnet-5-5"}
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

func TestSonnet55ImplicitAndBetweenToolsPreserveThinking(t *testing.T) {
	for _, thinking := range []string{"", `,"thinking":{"type":"adaptive"}`, `,"thinking":{"type":"between_tools"}`} {
		body := []byte(`{"model":"claude-sonnet-5-5","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"signed-progress"},{"type":"redacted_thinking","data":"opaque-progress"},{"type":"text","text":"answer"}]}]` + thinking + `}`)
		parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
		require.NoError(t, err)
		require.True(t, parsed.ThinkingEnabled, thinking)
		out := FilterThinkingBlocks(body, "claude-sonnet-5-5")
		require.Len(t, gjson.GetBytes(out, "messages.0.content").Array(), 3, thinking)
		require.Equal(t, "signed-progress", gjson.GetBytes(out, "messages.0.content.0.signature").String())
		require.Equal(t, "opaque-progress", gjson.GetBytes(out, "messages.0.content.1.data").String())
	}
}

func TestSonnet55OAuthPreservesBetweenToolsAndToolChoice(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-5-5","thinking":{"type":"between_tools"},"tool_choice":{"type":"none"},"messages":[{"role":"user","content":"hello"}]}`)
	out, _ := normalizeClaudeOAuthRequestBody(body, "claude-sonnet-5-5", claudeOAuthNormalizeOptions{})
	require.False(t, gjson.GetBytes(out, "temperature").Exists())
	require.Equal(t, "none", gjson.GetBytes(out, "tool_choice.type").String())
	require.Equal(t, `{"type":"between_tools"}`, gjson.GetBytes(out, "thinking").Raw)
	for _, model := range []string{"claude-sonnet-5", "claude-sonnet-5-50"} {
		legacy, _ := normalizeClaudeOAuthRequestBody([]byte(`{"model":"`+model+`","messages":[{"role":"user","content":"hello"}]}`), model, claudeOAuthNormalizeOptions{})
		require.Equal(t, 1.0, gjson.GetBytes(legacy, "temperature").Float(), model)
	}
}

func TestSonnet55BedrockModelID(t *testing.T) {
	for _, region := range []string{"us-east-1", "eu-west-1", "ap-northeast-1"} {
		account := &Account{Platform: PlatformAnthropic, Type: AccountTypeBedrock, Credentials: map[string]any{"aws_region": region}}
		for _, requested := range []string{"claude-sonnet-5-5", "anthropic.claude-sonnet-5-5"} {
			model, ok := ResolveBedrockModelID(account, requested)
			require.True(t, ok, requested)
			require.Equal(t, "anthropic.claude-sonnet-5-5", model)
		}
	}
}

func TestSonnet55ProviderAliasesRejectInvalidSettings(t *testing.T) {
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeServiceAccount, AccountTypeBedrock} {
		for _, endpoint := range []string{"messages", "messages/count_tokens"} {
			if accountType == AccountTypeBedrock && endpoint == "messages/count_tokens" {
				continue
			}
			for _, model := range []string{
				"claude-sonnet-5.5", "us.anthropic.claude-sonnet-5-5",
				"projects/test/locations/global/publishers/anthropic/models/claude-sonnet-5-5",
			} {
				t.Run(accountType+"/"+endpoint+"/"+model, func(t *testing.T) {
					body := []byte(`{"model":"alias-sonnet","thinking":{"type":"disabled"},"messages":[{"role":"user","content":"hello"}]}`)
					parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
					require.NoError(t, err)
					account := newAnthropicAPIKeyAccountForTest()
					account.Type = accountType
					account.Credentials["model_mapping"] = map[string]any{"alias-sonnet": model}
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

func TestSonnet55BetweenToolsAllowsUnchangedMessageEffort(t *testing.T) {
	for _, body := range []string{
		`{"thinking":{"type":"between_tools"},"messages":[{"role":"user","content":"hello","output_config":{"effort":"high"}}]}`,
		`{"thinking":{"type":"between_tools"},"output_config":{"effort":"low"},"messages":[{"role":"user","content":"hello","output_config":{"effort":"low"}}]}`,
		`{"thinking":{"type":"adaptive"},"output_config":{"effort":"xhigh"},"messages":[{"role":"user","content":"hello","output_config":{"effort":"low"}}]}`,
	} {
		require.NoError(t, validateClaudeModelRequest([]byte(body), "claude-sonnet-5-5"))
	}
	for _, model := range []string{"claude-sonnet-5", "claude-sonnet-5-50", "claude-sonnet-5-6", "gpt-6"} {
		require.NoError(t, validateClaudeModelRequest([]byte(`{"thinking":{"type":"disabled"},"tool_choice":{"type":"any"}}`), model))
	}
}

func TestSonnet55AutomaticBetasAllowModernToolsets(t *testing.T) {
	svc := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{InjectBetaForAPIKey: true}}}
	for _, tokenType := range []string{"apikey", "oauth"} {
		for _, model := range []string{"claude-sonnet-5-5", "claude-sonnet-5"} {
			body := []byte(`{"model":"` + model + `","tools":[{"type":"computer_toolset_20260801","name":"computer"}]}`)
			for _, countTokens := range []bool{false, true} {
				compute := svc.computeFinalAnthropicBeta
				if countTokens {
					compute = svc.computeFinalCountTokensAnthropicBeta
				}
				beta, set := compute(tokenType, false, model, http.Header{}, body, nil)
				require.True(t, set)
				if model == "claude-sonnet-5-5" {
					require.NotContains(t, beta, claude.BetaFineGrainedToolStreaming)
				} else if tokenType == "apikey" || !countTokens {
					require.Contains(t, beta, claude.BetaFineGrainedToolStreaming)
				}
				beta, set = compute(tokenType, false, model, http.Header{"Anthropic-Beta": {claude.BetaFineGrainedToolStreaming}}, body, nil)
				require.True(t, set)
				require.Contains(t, beta, claude.BetaFineGrainedToolStreaming)
			}
		}
	}
}

func TestSonnet55BedrockCountTokensRemainsUnsupported(t *testing.T) {
	parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(`{"model":"claude-sonnet-5-5","messages":[{"role":"user","content":"hello"}]}`)), PlatformAnthropic)
	require.NoError(t, err)
	upstream := &anthropicHTTPUpstreamRecorder{err: fmt.Errorf("unexpected upstream call")}
	svc := newForwardPartialUsageServiceForTest(upstream)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	require.NoError(t, svc.ForwardCountTokens(context.Background(), c, &Account{Platform: PlatformAnthropic, Type: AccountTypeBedrock}, parsed))
	require.Equal(t, http.StatusNotFound, c.Writer.Status())
	require.Nil(t, upstream.lastReq)
}

func TestSonnet55SignatureRetryPreservesBetweenTools(t *testing.T) {
	filters := map[string]func([]byte, string) []byte{
		"thinking":  FilterThinkingBlocksForRetry,
		"signature": FilterSignatureSensitiveBlocksForRetry,
	}
	for name, filter := range filters {
		for _, content := range []string{
			`[{"type":"text","text":"answer"}]`,
			`[{"type":"thinking","thinking":"progress","signature":"invalid-signature"},{"type":"redacted_thinking","data":"invalid-opaque"},{"type":"text","text":"answer"}]`,
			`[{"type":"thinking","thinking":"","signature":"invalid-signature"},{"type":"tool_use","name":"lookup","id":"tool-1","input":{}}]`,
		} {
			t.Run(name+"/"+content, func(t *testing.T) {
				body := []byte(`{"model":"claude-sonnet-5-5","thinking":{"type":"between_tools"},"output_config":{"effort":"low"},"messages":[{"role":"assistant","content":` + content + `}]}`)
				out := filter(body, "claude-sonnet-5-5")
				require.JSONEq(t, `{"type":"between_tools"}`, gjson.GetBytes(out, "thinking").Raw)
				require.Equal(t, "low", gjson.GetBytes(out, "output_config.effort").String())
				require.NotContains(t, string(out), "invalid-signature")
				require.NotContains(t, string(out), "invalid-opaque")
				require.NoError(t, validateClaudeModelRequest(out, "claude-sonnet-5-5"))
			})
		}
	}
}
