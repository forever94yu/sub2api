//go:build unit

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

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func solLunaUsageResponse(t *testing.T, model, tier string, chat, stream bool) *http.Response {
	t.Helper()
	usage := map[string]any{
		"input_tokens":          1000,
		"input_tokens_details":  map[string]int{"cached_tokens": 100, "cache_write_tokens": 200},
		"output_tokens":         50,
		"output_tokens_details": map[string]int{"reasoning_tokens": 17},
		"total_tokens":          1050,
	}
	response := map[string]any{
		"id": "resp_sol_luna", "object": "response", "model": model, "status": "completed", "usage": usage,
		"output": []any{map[string]any{
			"type": "message", "id": "msg_sol_luna", "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": "ok"}},
		}},
	}
	if tier != "" {
		response["service_tier"] = tier
	}
	if chat {
		response["object"] = "chat.completion"
		delete(response, "output")
		response["usage"] = map[string]any{
			"prompt_tokens": 1000, "prompt_tokens_details": usage["input_tokens_details"],
			"completion_tokens": 50, "completion_tokens_details": usage["output_tokens_details"], "total_tokens": 1050,
		}
		choice := map[string]any{"index": 0, "finish_reason": "stop"}
		if stream {
			choice["delta"] = map[string]string{"content": "ok"}
		} else {
			choice["message"] = map[string]string{"role": "assistant", "content": "ok"}
		}
		response["choices"] = []any{choice}
	}
	var payload any = response
	contentType := "application/json"
	if stream && !chat {
		payload = map[string]any{"type": "response.completed", "response": response}
	}
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	body := string(encoded)
	if stream {
		contentType = "text/event-stream"
		body = "data: " + body + "\n\ndata: [DONE]\n\n"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestGPT6SolLunaForwardUsageAndActualTierBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []struct {
		id           string
		standardCost float64
	}{
		{"gpt-6-sol", 0.00242},
		{"gpt-6-luna", 0.000121},
	} {
		for _, path := range []string{"/v1/responses", "/v1/chat/completions", "/v1/messages"} {
			for _, chat := range []bool{false, true} {
				for _, stream := range []bool{false, true} {
					for _, tier := range []struct {
						response, want string
						multiplier     float64
					}{
						{"default", "default", 1},
						{"fast", "priority", 2},
						{"priority", "priority", 2},
						{"flex", "flex", 0.5},
						{"", "priority", 2},
					} {
						t.Run(fmt.Sprintf("%s%s/chat=%t/stream=%t/tier=%s", model.id, path, chat, stream, tier.response), func(t *testing.T) {
							wantTier, wantMultiplier := tier.want, tier.multiplier
							if path == "/v1/messages" && chat && tier.response == "" {
								// The direct Messages-to-Chat bridge has no native request tier.
								wantTier, wantMultiplier = "", 1
							}
							account := rawChatCompletionsTestAccount()
							if chat {
								account = forceChatMessagesFallbackAccount()
							}
							upstream := &httpUpstreamRecorder{resp: solLunaUsageResponse(t, model.id, tier.response, chat, stream || (!chat && path != "/v1/responses"))}
							svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
							body := fmt.Sprintf(`{"model":%q,"service_tier":"priority","stream":%t,"max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`, model.id, stream)
							if path == "/v1/responses" {
								body = fmt.Sprintf(`{"model":%q,"service_tier":"priority","stream":%t,"input":"hello"}`, model.id, stream)
							}
							rec := httptest.NewRecorder()
							c, _ := gin.CreateTestContext(rec)
							c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
							c.Request.Header.Set("Content-Type", "application/json")
							c.Request.Header.Set("anthropic-beta", claude.BetaFastMode)
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
							require.Equal(t, http.StatusOK, rec.Code)
							require.Equal(t, model.id, gjson.GetBytes(upstream.lastBody, "model").String())
							if wantTier == "" {
								require.Nil(t, result.ServiceTier)
								require.False(t, gjson.GetBytes(upstream.lastBody, "service_tier").Exists())
							} else {
								require.NotNil(t, result.ServiceTier)
								require.Equal(t, wantTier, *result.ServiceTier)
							}
							usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
							billing := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
							require.NoError(t, billing.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: result, APIKey: &APIKey{ID: 1, UserID: 2}, User: &User{ID: 2}, Account: account}))
							log := usageRepo.lastLog
							require.NotNil(t, log)
							require.Equal(t, model.id, log.Model)
							require.Equal(t, 700, log.InputTokens)
							require.Equal(t, 200, log.CacheCreationTokens)
							require.Equal(t, 100, log.CacheReadTokens)
							require.Equal(t, 50, log.OutputTokens)
							require.Equal(t, 1050, log.TotalTokens())
							require.Equal(t, result.ServiceTier, log.ServiceTier)
							require.InDelta(t, model.standardCost*wantMultiplier, log.TotalCost, 1e-12)
							require.InDelta(t, model.standardCost*wantMultiplier*1.1, log.ActualCost, 1e-12)
						})
					}
				}
			}
		}
	}
}

func TestGPT6SolLunaWebSocketUsageRecordsLongContext(t *testing.T) {
	for _, model := range []struct {
		id   string
		cost float64
	}{
		{"gpt-6-sol", 0.464777},
		{"gpt-6-luna", 0.02323885},
	} {
		for _, eventType := range []string{"response.completed", "response.done"} {
			t.Run(model.id+"/"+eventType, func(t *testing.T) {
				body := []byte(fmt.Sprintf(`{"type":%q,"response":{"id":"resp_ws_sol_luna","model":%q,"service_tier":"flex","usage":{"input_tokens":272001,"input_tokens_details":{"cached_tokens":72000,"cache_write_tokens":100000},"output_tokens":50,"output_tokens_details":{"reasoning_tokens":17},"total_tokens":272051}}}`, eventType, model.id))
				usage := OpenAIUsage{}
				parseOpenAIWSResponseUsageFromCompletedEvent(body, &usage)
				observer := &upstreamResponseModelObserver{}
				observer.ObserveOpenAI(body, eventType)
				tier := observer.OpenAIServiceTier(nil)
				require.NotNil(t, tier)
				usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
				svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
				err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
					Result:  &OpenAIForwardResult{RequestID: "resp_ws_sol_luna", Model: model.id, Usage: usage, ServiceTier: tier},
					APIKey:  openAIRecordUsageAPIKeyWithGroup(svc, 1, true),
					User:    &User{ID: 2},
					Account: &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{openAILongContextBillingEnabledKey: true}},
				})
				require.NoError(t, err)
				log := usageRepo.lastLog
				require.NotNil(t, log)
				require.True(t, log.LongContextBillingApplied)
				require.Equal(t, 100001, log.InputTokens)
				require.Equal(t, 100000, log.CacheCreationTokens)
				require.Equal(t, 72000, log.CacheReadTokens)
				require.Equal(t, 272051, log.TotalTokens())
				require.Equal(t, "flex", *log.ServiceTier)
				require.InDelta(t, model.cost, log.TotalCost, 1e-12)
			})
		}
	}
}
