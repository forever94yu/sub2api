//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAstraReasoningForwardUsesNativeEffort(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/chat/completions", "/v1/messages"} {
		for _, rawChat := range []bool{false, true} {
			for _, model := range []string{"gpt-6-astra", "custom-astra"} {
				for _, effort := range []string{"low", "medium", "high", "xhigh", "max", "ultra"} {
					t.Run(fmt.Sprintf("%s/raw=%t/%s/%s", path, rawChat, model, effort), func(t *testing.T) {
						account := rawChatCompletionsTestAccount()
						if rawChat {
							account = forceChatMessagesFallbackAccount()
						}
						account.Credentials["model_mapping"] = map[string]any{"custom-astra": "gpt-6-astra"}
						upstream := &httpUpstreamRecorder{resp: serviceTierTestResponse("default", rawChat, !rawChat && path != "/v1/responses")}
						svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
						result := forwardAstraReasoningTest(t, svc, account, path, model, effort, context.Background())
						want := effort
						if effort == "ultra" {
							want = "max"
						}
						field := "reasoning.effort"
						if rawChat {
							field = "reasoning_effort"
						}
						require.Equal(t, want, gjson.GetBytes(upstream.lastBody, field).String(), "actual upstream request")
						require.NotNil(t, result.ReasoningEffort)
						require.Equal(t, want, *result.ReasoningEffort, "usage metadata must match the request")
					})
				}
			}
		}
	}
}

func TestAstraUltraAliasRespectsGroupReasoningPolicy(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/chat/completions", "/v1/messages"} {
		t.Run(path, func(t *testing.T) {
			account := rawChatCompletionsTestAccount()
			account.Credentials["model_mapping"] = map[string]any{"custom-astra": "gpt-6-astra"}
			upstream := &httpUpstreamRecorder{resp: serviceTierTestResponse("default", false, path != "/v1/responses")}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			ctx := WithOpenAIReasoningEffortPolicy(context.Background(), "high", []ReasoningEffortMapping{{From: "max", To: "medium"}})
			result := forwardAstraReasoningTest(t, svc, account, path, "custom-astra", "ultra", ctx)
			require.Equal(t, "medium", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
			require.NotNil(t, result.ReasoningEffort)
			require.Equal(t, "medium", *result.ReasoningEffort)
		})
	}
}

func TestAstraReasoningNormalizationLeavesOtherModelsUntouched(t *testing.T) {
	for _, model := range []string{"gpt-5.4", "gpt-5.6-sol", "gpt-6-astra-preview", "custom-astra"} {
		for _, effort := range []string{"xhigh", "max", "ultra"} {
			t.Run(model+"/"+effort, func(t *testing.T) {
				body := []byte(fmt.Sprintf(`{"model":%q,"reasoning":{"effort":%q},"reasoning_effort":%q}`, model, effort, effort))
				got, changed := ApplyOpenAIReasoningEffortPolicy(body, "", nil)
				require.False(t, changed)
				require.Equal(t, body, got)
			})
		}
	}
}

func TestAstraReasoningAccountNormalizationDoesNotChainMappings(t *testing.T) {
	account := rawChatCompletionsTestAccount()
	account.Credentials["model_mapping"] = map[string]any{"custom-astra": "gpt-6-astra"}
	ctx := WithOpenAIReasoningEffortPolicy(context.Background(), "high", []ReasoningEffortMapping{{From: "max", To: "medium"}, {From: "medium", To: "low"}})
	for _, model := range []string{"gpt-6-astra", "custom-astra"} {
		t.Run(model, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":%q,"reasoning":{"effort":"ultra"}}`, model))
			body, _ = ApplyOpenAIReasoningEffortPolicyFromContext(ctx, body)
			body = normalizeAstraReasoningEffortForAccount(ctx, account, body, "")
			require.Equal(t, "medium", gjson.GetBytes(body, "reasoning.effort").String())
		})
	}
}

func forwardAstraReasoningTest(t *testing.T, svc *OpenAIGatewayService, account *Account, path, model, effort string, ctx context.Context) *OpenAIForwardResult {
	t.Helper()
	body := fmt.Sprintf(`{"model":%q,"stream":false,"messages":[{"role":"user","content":"hello"}],"reasoning_effort":%q}`, model, effort)
	if path == "/v1/responses" {
		body = fmt.Sprintf(`{"model":%q,"stream":false,"input":"hello","reasoning":{"effort":%q}}`, model, effort)
	} else if path == "/v1/messages" {
		body = fmt.Sprintf(`{"model":%q,"stream":false,"max_tokens":128,"messages":[{"role":"user","content":"hello"}],"output_config":{"effort":%q}}`, model, effort)
	}
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	var result *OpenAIForwardResult
	var err error
	switch path {
	case "/v1/messages":
		result, err = svc.ForwardAsAnthropic(ctx, c, account, []byte(body), "", "")
	case "/v1/chat/completions":
		result, err = svc.ForwardAsChatCompletions(ctx, c, account, []byte(body), "", "")
	default:
		result, err = svc.Forward(ctx, c, account, []byte(body))
	}
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, c.Writer.Status())
	return result
}
