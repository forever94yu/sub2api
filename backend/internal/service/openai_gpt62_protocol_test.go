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

func TestGPT62ExactModelIdentity(t *testing.T) {
	for _, model := range []string{"gpt-6.2-astra", "gpt-6.2-sol"} {
		for _, input := range []string{model, strings.ToUpper(model), "openai/" + model} {
			got, ok := normalizeKnownCodexModel(input)
			require.True(t, ok, input)
			require.Equal(t, model, got, input)
			require.True(t, shouldAutoInjectPromptCacheKeyForCompat(input), input)
			require.True(t, supportsOpenAIMaxReasoningEffort(input), input)
		}
		for _, input := range []string{model + "-preview", model + "-2026-11-18", model + "-high", model + "-codex", strings.ReplaceAll(model, "-", "_"), "20261118-" + model} {
			_, ok := normalizeKnownCodexModel(input)
			require.False(t, ok, input)
			require.Empty(t, normalizeKnownOpenAICodexModel(input), input)
			require.Equal(t, []string{input}, usageBillingModelCandidates(input), input)
		}
	}
}

func TestGPT62ReasoningPolicyAndPrefixPreservation(t *testing.T) {
	for _, model := range []string{"gpt-6.2-astra", "gpt-6.2-sol"} {
		for _, effort := range []string{"none", "minimal", "high"} {
			t.Run(model+"/"+effort, func(t *testing.T) {
				account := rawChatCompletionsTestAccount()
				account.Credentials["model_mapping"] = map[string]any{"custom-model": model}
				body := []byte(fmt.Sprintf(`{"model":"custom-model","instructions":"current request guidance","input":[{"role":"developer","content":"stable prefix"},{"role":"user","content":"hello"}],"reasoning":{"effort":%q},"temperature":0.7,"top_p":0.9,"logprobs":true,"top_logprobs":2,"include":["reasoning.encrypted_content","message.output_text.logprobs"]}`, effort))
				mappings := []ReasoningEffortMapping{{From: "high", To: "minimal"}, {From: "low", To: "high"}}
				ctx := WithOpenAIReasoningEffortPolicy(context.Background(), "", mappings, body)
				first, _ := ApplyOpenAIReasoningEffortPolicyFromContext(ctx, body)
				want := "high"
				if effort == "high" {
					want = "low"
				}
				for _, got := range [][]byte{
					normalizeAstraReasoningEffortForAccount(ctx, account, first, ""),
					normalizeAstraReasoningEffortForWS(first, model, &OpenAIWSIngressHooks{ReasoningEffortMappings: mappings}, captureOpenAIReasoningEffortInput(body)),
				} {
					require.Equal(t, want, gjson.GetBytes(got, "reasoning.effort").String())
					require.Equal(t, "current request guidance", gjson.GetBytes(got, "instructions").String())
					require.Equal(t, gjson.GetBytes(body, "input").Raw, gjson.GetBytes(got, "input").Raw)
					for _, field := range []string{"temperature", "top_p", "logprobs", "top_logprobs"} {
						require.False(t, gjson.GetBytes(got, field).Exists(), field)
					}
					require.JSONEq(t, `["reasoning.encrypted_content"]`, gjson.GetBytes(got, "include").Raw)
				}
			})
		}
	}
}

func TestGPT62NativeChatToolRequirements(t *testing.T) {
	for _, model := range []string{"gpt-6.2-astra", "gpt-6.2-sol"} {
		for _, fields := range []string{`"tools":[{"type":"function","function":{"name":"lookup"}}]`, `"functions":[{"name":"lookup"}]`, `"messages":[{"role":"tool","tool_call_id":"call_1","content":"result"}]`} {
			err := validateOpenAIGPT6ChatTools([]byte(fmt.Sprintf(`{"model":%q,"reasoning_effort":"none",%s}`, model, fields)))
			require.ErrorContains(t, err, model)
			require.ErrorContains(t, err, "Responses")
		}
		require.NoError(t, validateOpenAIGPT6ChatTools([]byte(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}]}`, model))))
	}
}

func TestGPT62NativeChatWithoutTools(t *testing.T) {
	for _, model := range []string{"gpt-6.2-astra", "gpt-6.2-sol"} {
		for _, path := range []string{"/v1/responses", "/v1/chat/completions", "/v1/messages"} {
			for _, effort := range []string{"none", "minimal", "max", "ultra"} {
				t.Run(model+path+"/"+effort, func(t *testing.T) {
					account := forceChatMessagesFallbackAccount()
					account.Credentials["model_mapping"] = map[string]any{"custom-gpt62": model}
					upstream := &httpUpstreamRecorder{resp: serviceTierTestResponse("default", true, false)}
					svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
					result := forwardAstraReasoningTest(t, svc, account, path, "custom-gpt62", effort, context.Background())
					want := "max"
					if effort == "none" || effort == "minimal" {
						want = "low"
					}
					require.True(t, strings.HasSuffix(upstream.lastReq.URL.Path, "/chat/completions"))
					require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
					require.Equal(t, want, gjson.GetBytes(upstream.lastBody, "reasoning_effort").String())
					require.NotNil(t, result.ReasoningEffort)
					require.Equal(t, want, *result.ReasoningEffort)
				})
			}
		}
	}
}

func TestGPT62ToolsForwardThroughResponses(t *testing.T) {
	for _, model := range []string{"gpt-6.2-astra", "gpt-6.2-sol"} {
		for _, oauth := range []bool{false, true} {
			for _, tt := range []struct{ path, fields string }{
				{"/v1/responses", `"input":"hello","reasoning":{"effort":"none"},"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{}}}]`},
				{"/v1/chat/completions", `"messages":[{"role":"user","content":"hello"}],"reasoning_effort":"none","tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{}}}}]`},
				{"/v1/messages", `"max_tokens":128,"messages":[{"role":"user","content":"hello"}],"output_config":{"effort":"none"},"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{}}}]`},
			} {
				t.Run(fmt.Sprintf("%s%s/oauth=%t", model, tt.path, oauth), func(t *testing.T) {
					account := rawChatCompletionsTestAccount()
					if oauth {
						account.Type = AccountTypeOAuth
						account.Credentials = map[string]any{"access_token": "oauth-test", "chatgpt_account_id": "account-test"}
					}
					body := []byte(fmt.Sprintf(`{"model":%q,"stream":false,"temperature":0.7,"top_p":0.9,%s}`, model, tt.fields))
					upstream := &httpUpstreamRecorder{resp: serviceTierTestResponse("default", false, tt.path != "/v1/responses")}
					svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(string(body)))
					var err error
					switch tt.path {
					case "/v1/messages":
						_, err = svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
					case "/v1/chat/completions":
						_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
					default:
						_, err = svc.Forward(context.Background(), c, account, body)
					}
					require.NoError(t, err)
					require.True(t, strings.HasSuffix(upstream.lastReq.URL.Path, "/responses"))
					require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
					require.Equal(t, "low", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
					require.Equal(t, "lookup", gjson.GetBytes(upstream.lastBody, "tools.0.name").String())
					require.False(t, gjson.GetBytes(upstream.lastBody, "temperature").Exists())
					require.False(t, gjson.GetBytes(upstream.lastBody, "top_p").Exists())
				})
			}
		}
	}
}
