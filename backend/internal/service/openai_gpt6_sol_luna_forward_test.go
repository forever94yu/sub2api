//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPT6SolLunaForwardNativeReasoning(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		for _, path := range []string{"/v1/responses", "/v1/chat/completions", "/v1/messages"} {
			for _, rawChat := range []bool{false, true} {
				for _, effort := range []string{"none", "minimal", "max", "ultra"} {
					t.Run(fmt.Sprintf("%s/%s/raw=%t/%s", model, path, rawChat, effort), func(t *testing.T) {
						account := rawChatCompletionsTestAccount()
						if rawChat {
							account = forceChatMessagesFallbackAccount()
						}
						account.Credentials["model_mapping"] = map[string]any{"custom-gpt6": model}
						upstream := &httpUpstreamRecorder{resp: serviceTierTestResponse("default", rawChat, !rawChat && path != "/v1/responses")}
						svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
						result := forwardAstraReasoningTest(t, svc, account, path, "custom-gpt6", effort, context.Background())
						want := effort
						if effort == "minimal" {
							want = "low"
						} else if effort == "ultra" {
							want = "max"
						}
						field := "reasoning.effort"
						if rawChat {
							field = "reasoning_effort"
						}
						require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
						require.Equal(t, want, gjson.GetBytes(upstream.lastBody, field).String())
						require.NotNil(t, result.ReasoningEffort)
						require.Equal(t, want, *result.ReasoningEffort)
					})
				}
			}
		}
	}
}

func TestGPT6SolLunaForwardSamplingUsesMappedModel(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		for _, path := range []string{"/v1/responses", "/v1/chat/completions"} {
			for _, effort := range []string{"none", "medium", ""} {
				t.Run(model+path+"/"+effort, func(t *testing.T) {
					account := rawChatCompletionsTestAccount()
					account.Credentials["model_mapping"] = map[string]any{"custom-gpt6": model}
					upstream := &httpUpstreamRecorder{resp: serviceTierTestResponse("default", false, path != "/v1/responses")}
					svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
					payload := map[string]any{
						"model": "custom-gpt6", "stream": false, "input": "hello", "temperature": 0.7,
						"top_p": 0.9, "top_logprobs": 2, "include": []string{"reasoning.encrypted_content", "message.output_text.logprobs"},
					}
					if effort != "" {
						payload["reasoning"] = map[string]string{"effort": effort}
					}
					body, err := json.Marshal(payload)
					require.NoError(t, err)
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
					if path == "/v1/responses" {
						_, err = svc.Forward(context.Background(), c, account, body)
					} else {
						_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
					}
					require.NoError(t, err)
					for _, field := range []string{"temperature", "top_p", "top_logprobs"} {
						require.Equal(t, effort == "none", gjson.GetBytes(upstream.lastBody, field).Exists(), field)
					}
					include := gjson.GetBytes(upstream.lastBody, "include").Raw
					require.Contains(t, include, "reasoning.encrypted_content")
					if effort == "none" {
						require.Contains(t, include, "message.output_text.logprobs")
					} else {
						require.NotContains(t, include, "message.output_text.logprobs")
					}
				})
			}
		}
	}
}

func TestGPT6SolLunaChatToolsRequireNoReasoning(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna", "gpt-5.5"} {
		for _, path := range []string{"/v1/responses", "/v1/chat/completions", "/v1/messages"} {
			for _, effort := range []string{"none", "medium", ""} {
				t.Run(model+path+"/"+effort, func(t *testing.T) {
					account := forceChatMessagesFallbackAccount()
					account.Credentials["model_mapping"] = map[string]any{"custom-tools": model}
					upstream := &httpUpstreamRecorder{resp: serviceTierTestResponse("default", true, false)}
					svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
					payload := map[string]any{"model": "custom-tools", "stream": false}
					switch path {
					case "/v1/responses":
						payload["input"] = "hello"
						payload["tools"] = []any{map[string]any{"type": "function", "name": "lookup", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}}}
						if effort != "" {
							payload["reasoning"] = map[string]string{"effort": effort}
						}
					case "/v1/messages":
						payload["messages"] = []any{map[string]string{"role": "user", "content": "hello"}}
						payload["max_tokens"] = 128
						payload["tools"] = []any{map[string]any{"name": "lookup", "input_schema": map[string]any{"type": "object", "properties": map[string]any{}}}}
						if effort != "" {
							payload["output_config"] = map[string]string{"effort": effort}
						}
					default:
						payload["messages"] = []any{map[string]string{"role": "user", "content": "hello"}}
						payload["tools"] = []any{map[string]any{"type": "function", "function": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}}}}
						if effort != "" {
							payload["reasoning_effort"] = effort
						}
					}
					body, err := json.Marshal(payload)
					require.NoError(t, err)
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
					switch path {
					case "/v1/responses":
						_, err = svc.Forward(context.Background(), c, account, body)
					case "/v1/messages":
						_, err = svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
					default:
						_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
					}
					if model != "gpt-5.5" && effort != "none" {
						require.Error(t, err)
						require.Equal(t, http.StatusBadRequest, recorder.Code)
						require.Equal(t, "invalid_request_error", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
						require.Contains(t, gjson.GetBytes(recorder.Body.Bytes(), "error.message").String(), "reasoning_effort=none")
						require.Nil(t, upstream.lastReq)
					} else {
						require.NoError(t, err)
						require.NotEmpty(t, gjson.GetBytes(upstream.lastBody, "tools").Array())
					}
				})
			}
		}
	}
}

func TestGPT6SolLunaNoneSamplingSurvivesOldModelAlias(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		for _, path := range []string{"/v1/messages", "/v1/chat/completions"} {
			for _, rawChat := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s%s/raw=%t", model, path, rawChat), func(t *testing.T) {
					account := rawChatCompletionsTestAccount()
					if rawChat {
						account = forceChatMessagesFallbackAccount()
					}
					account.Credentials["model_mapping"] = map[string]any{"gpt-5.5": model}
					upstream := &httpUpstreamRecorder{resp: serviceTierTestResponse("default", rawChat, !rawChat)}
					svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
					body := `{"model":"gpt-5.5","stream":false,"max_tokens":128,"messages":[{"role":"user","content":"hello"}],"reasoning_effort":"none","output_config":{"effort":"none"},"temperature":0.7,"top_p":0.9}`
					forwardAstraCompatRequest(t, svc, account, path, body)
					require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
					effortField := "reasoning.effort"
					if rawChat {
						effortField = "reasoning_effort"
					}
					require.Equal(t, "none", gjson.GetBytes(upstream.lastBody, effortField).String())
					require.InDelta(t, 0.7, gjson.GetBytes(upstream.lastBody, "temperature").Float(), 1e-12)
					require.InDelta(t, 0.9, gjson.GetBytes(upstream.lastBody, "top_p").Float(), 1e-12)
				})
			}
		}
	}
}
