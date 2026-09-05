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

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func serviceTierTestResponse(tier string, chat, stream bool) *http.Response {
	tierField := ""
	if tier != "" {
		tierField = fmt.Sprintf(`,"service_tier":%q`, tier)
	}
	response := fmt.Sprintf(`{"id":"resp_tier","object":"response","model":"gpt-6-astra","status":"completed"%s,"output":[{"type":"message","id":"msg_tier","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1000,"input_tokens_details":{"cached_tokens":100,"cache_write_tokens":200},"output_tokens":50,"total_tokens":1050}}`, tierField)
	if chat {
		choice := `{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}`
		if stream {
			choice = `{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}`
		}
		response = fmt.Sprintf(`{"id":"chatcmpl_tier","object":"chat.completion","model":"gpt-6-astra"%s,"choices":[%s],"usage":{"prompt_tokens":1000,"prompt_tokens_details":{"cached_tokens":100,"cache_write_tokens":200},"completion_tokens":50,"total_tokens":1050}}`, tierField, choice)
	}
	contentType := "application/json"
	if stream {
		contentType = "text/event-stream"
		if !chat {
			response = `{"type":"response.completed","response":` + response + `}`
		}
		response = "data: " + response + "\n\ndata: [DONE]\n\n"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(response))}
}

func forwardServiceTierTest(t *testing.T, svc *OpenAIGatewayService, account *Account, path, tier string, stream bool) *OpenAIForwardResult {
	t.Helper()
	body := fmt.Sprintf(`{"model":"gpt-6-astra","service_tier":%q,"stream":%t,"max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`, tier, stream)
	if path == "/v1/responses" {
		body = fmt.Sprintf(`{"model":"gpt-6-astra","service_tier":%q,"stream":%t,"input":"hello"}`, tier, stream)
	}
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if path == "/v1/messages" && tier == "priority" {
		c.Request.Header.Set("anthropic-beta", claude.BetaFastMode)
	}
	var result *OpenAIForwardResult
	var err error
	switch path {
	case "/v1/messages":
		result, err = svc.ForwardAsAnthropic(context.Background(), c, account, []byte(body), "", "")
	case "/v1/chat/completions":
		result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, []byte(body), "", "")
	default:
		result, err = svc.Forward(context.Background(), c, account, []byte(body))
	}
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, rec.Code)
	return result
}

func TestOpenAIServiceTierBillingUsesActualUpstreamTier(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/chat/completions", "/v1/messages"} {
		for _, chat := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				for _, tc := range []struct {
					request, response, want string
					cost                    float64
				}{
					{"priority", "default", "default", 0.0121},
					{"auto", "fast", "priority", 0.0242},
					{"priority", "flex", "flex", 0.00605},
				} {
					t.Run(fmt.Sprintf("%s/chat=%t/stream=%t/%s", path, chat, stream, tc.response), func(t *testing.T) {
						account := rawChatCompletionsTestAccount()
						if chat {
							account = forceChatMessagesFallbackAccount()
						}
						upstreamStream := stream || (!chat && path != "/v1/responses")
						upstream := &httpUpstreamRecorder{resp: serviceTierTestResponse(tc.response, chat, upstreamStream)}
						svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
						result := forwardServiceTierTest(t, svc, account, path, tc.request, stream)
						require.NotNil(t, result.ServiceTier)
						require.Equal(t, tc.want, *result.ServiceTier)
						usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
						billing := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
						require.NoError(t, billing.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: result, APIKey: &APIKey{ID: 1, UserID: 2}, User: &User{ID: 2}, Account: account}))
						require.InDelta(t, tc.cost, usageRepo.lastLog.TotalCost, 1e-12)
					})
				}
			}
		}
	}
}

func TestOpenAIServiceTierBillingUsesPostPolicyFallback(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/chat/completions", "/v1/messages"} {
		for _, chat := range []bool{false, true} {
			if chat && path == "/v1/messages" {
				continue
			}
			for _, tc := range []struct{ name, action, request, want string }{
				{"filter", BetaPolicyActionFilter, "priority", ""},
				{"force", OpenAIFastPolicyActionForcePriority, "auto", "priority"},
			} {
				if path == "/v1/messages" && tc.name == "force" {
					continue // Messages has no native service_tier to force from auto.
				}
				t.Run(fmt.Sprintf("%s/chat=%t/%s", path, chat, tc.name), func(t *testing.T) {
					svc := newOpenAIGatewayServiceWithSettings(t, &OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{{Scope: BetaPolicyScopeAll, Action: tc.action}}})
					svc.cfg = rawChatCompletionsTestConfig()
					upstream := &httpUpstreamRecorder{resp: serviceTierTestResponse("", chat, !chat && path != "/v1/responses")}
					svc.httpUpstream = upstream
					account := rawChatCompletionsTestAccount()
					if chat {
						account = forceChatMessagesFallbackAccount()
					}
					result := forwardServiceTierTest(t, svc, account, path, tc.request, false)
					require.Equal(t, tc.want, gjson.GetBytes(upstream.lastBody, "service_tier").String())
					if tc.want == "" {
						require.Nil(t, result.ServiceTier)
					} else {
						require.NotNil(t, result.ServiceTier)
						require.Equal(t, tc.want, *result.ServiceTier)
					}
				})
			}
		}
	}
}

func TestOpenAIServiceTierBillingPassthroughUsesResponse(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			account := rawChatCompletionsTestAccount()
			account.Extra = map[string]any{"openai_passthrough": true}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: &httpUpstreamRecorder{resp: serviceTierTestResponse("default", false, stream)}}
			result := forwardServiceTierTest(t, svc, account, "/v1/responses", "priority", stream)
			require.NotNil(t, result.ServiceTier)
			require.Equal(t, "default", *result.ServiceTier)
		})
	}
}

func TestOpenAIServiceTierBillingKeepsPartialStreamMetadata(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/v1/messages"} {
		for _, tc := range []struct{ tier, want string }{{"fast", "priority"}, {"default", "default"}, {"", "priority"}} {
			t.Run(path+"/"+tc.tier, func(t *testing.T) {
				tierField := ""
				if tc.tier != "" {
					tierField = fmt.Sprintf(`,"service_tier":%q`, tc.tier)
				}
				payload := fmt.Sprintf("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_partial\",\"model\":\"gpt-6-astra\"%s}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n", tierField)
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       &openAIChatStreamReadErrorCloser{payload: []byte(payload), err: io.ErrUnexpectedEOF},
				}}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
				body := []byte(`{"model":"gpt-6-astra","service_tier":"priority","reasoning_effort":"high","stream":true,"max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`)
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Request.Header.Set("anthropic-beta", claude.BetaFastMode)
				var result *OpenAIForwardResult
				var err error
				if path == "/v1/messages" {
					result, err = svc.ForwardAsAnthropic(context.Background(), c, rawChatCompletionsTestAccount(), body, "", "")
				} else {
					result, err = svc.ForwardAsChatCompletions(context.Background(), c, rawChatCompletionsTestAccount(), body, "", "")
				}
				require.Error(t, err)
				require.NotNil(t, result)
				require.NotNil(t, result.ServiceTier, "billable partial results retain actual or post-policy tier")
				require.Equal(t, tc.want, *result.ServiceTier)
			})
		}
	}
}
