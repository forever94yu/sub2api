//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPT61SolExactModelIdentity(t *testing.T) {
	const model = "gpt-6.1-sol"
	for _, input := range []string{model, strings.ToUpper(model), "openai/" + model, "gpt-image-proxy/" + model} {
		got, ok := normalizeKnownCodexModel(input)
		require.True(t, ok, input)
		require.Equal(t, model, got, input)
		require.False(t, isUnsupportedOpenAIGPT6Model(input), input)
		require.True(t, shouldAutoInjectPromptCacheKeyForCompat(input), input)
		require.True(t, isOpenAIGPT6Model(input), input)
	}
	for _, input := range []string{
		model + "-preview", model + "-2026-09-29", model + "-high", model + "-codex",
		model + "-gpt-5.5", model + "-openai-compact", strings.ReplaceAll(model, "-", "_"),
		"20260929-" + model, "gpt-image-" + model,
	} {
		_, ok := normalizeKnownCodexModel(input)
		require.False(t, ok, input)
		require.Empty(t, normalizeKnownOpenAICodexModel(input), input)
		require.Equal(t, []string{input}, usageBillingModelCandidates(input), input)
		require.False(t, shouldAutoInjectPromptCacheKeyForCompat(input), input)
	}
}

func TestGPT61SolReasoningEffortNormalization(t *testing.T) {
	for _, model := range []string{"gpt-6.1-sol", "openai/GPT-6.1-SOL"} {
		for _, tt := range []struct{ input, want string }{
			{"none", "low"}, {"minimal", "low"}, {"low", "low"}, {"medium", "medium"},
			{"high", "high"}, {"xhigh", "xhigh"}, {"max", "max"}, {"ultra", "max"},
		} {
			t.Run(model+"/"+tt.input, func(t *testing.T) {
				require.Equal(t, tt.want, normalizeOpenAIReasoningEffortForModel(tt.input, model))
				body := []byte(fmt.Sprintf(`{"model":%q,"reasoning":{"effort":%q},"reasoning_effort":%q}`, model, tt.input, tt.input))
				got, _ := ApplyOpenAIReasoningEffortPolicy(body, "", nil)
				require.Equal(t, tt.want, gjson.GetBytes(got, "reasoning.effort").String())
				require.Equal(t, tt.want, gjson.GetBytes(got, "reasoning_effort").String())
				effort := extractOpenAIReasoningEffortFromBody(got, model)
				require.NotNil(t, effort)
				require.Equal(t, tt.want, *effort)
				req := &apicompat.AnthropicRequest{OutputConfig: &apicompat.AnthropicOutputConfig{Effort: tt.input}}
				require.Equal(t, tt.want, openAICompatAnthropicReasoningEffort(req, model, tt.input))
			})
		}
	}
}

func TestGPT61SolWSNormalizesReasoningAndSampling(t *testing.T) {
	for _, effort := range []string{"none", "minimal", "max", "ultra"} {
		body := []byte(fmt.Sprintf(`{"type":"response.create","model":"custom-sol","reasoning":{"effort":%q},"temperature":0.7,"top_p":0.9,"top_logprobs":2,"logprobs":true,"include":["reasoning.encrypted_content","message.output_text.logprobs"]}`, effort))
		got := normalizeAstraReasoningEffortForWS(body, "gpt-6.1-sol", nil)
		want := "max"
		if effort == "none" || effort == "minimal" {
			want = "low"
		}
		require.Equal(t, want, gjson.GetBytes(got, "reasoning.effort").String())
		for _, field := range []string{"temperature", "top_p", "top_logprobs", "logprobs"} {
			require.False(t, gjson.GetBytes(got, field).Exists(), field)
		}
		require.JSONEq(t, `["reasoning.encrypted_content"]`, gjson.GetBytes(got, "include").Raw)
	}
}

func TestGPT61SolChatRejectsAllTools(t *testing.T) {
	for _, fields := range []string{
		`"tools":[{"type":"function","function":{"name":"lookup"}}]`,
		`"functions":[{"name":"lookup"}]`,
		`"tools":[{"type":"custom","custom":{"name":"lookup"}}]`,
		`"messages":[{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}]`,
		`"messages":[{"role":"assistant","function_call":{"name":"lookup","arguments":"{}"}}]`,
		`"messages":[{"role":"tool","tool_call_id":"call_1","content":"result"}]`,
		`"messages":[{"role":"function","name":"lookup","content":"result"}]`,
	} {
		for _, effort := range []string{"none", "low", "max", ""} {
			body := []byte(fmt.Sprintf(`{"model":"gpt-6.1-sol","reasoning_effort":%q,%s}`, effort, fields))
			err := validateOpenAIGPT6ChatTools(body)
			require.Error(t, err)
			require.Contains(t, err.Error(), "Responses")
		}
	}
	for _, body := range []string{
		`{"model":"gpt-6.1-sol"}`,
		`{"model":"gpt-6.1-sol","tools":[],"functions":[]}`,
		`{"model":"gpt-6.1-sol","tool_choice":"none","messages":[{"role":"assistant","content":"hello","tool_calls":[],"function_call":null}]}`,
	} {
		require.NoError(t, validateOpenAIGPT6ChatTools([]byte(body)))
	}
}

func TestGPT61SolMappedReasoningRespectsPolicy(t *testing.T) {
	account := rawChatCompletionsTestAccount()
	account.Credentials["model_mapping"] = map[string]any{"custom-sol": "gpt-6.1-sol"}
	ctx := WithOpenAIReasoningEffortPolicy(context.Background(), "high", []ReasoningEffortMapping{{From: "low", To: "high"}})
	body := []byte(`{"model":"custom-sol","reasoning":{"effort":"none"},"temperature":0.7}`)
	got := normalizeAstraReasoningEffortForAccount(ctx, account, body, "")
	require.Equal(t, "high", gjson.GetBytes(got, "reasoning.effort").String())
	require.False(t, gjson.GetBytes(got, "temperature").Exists())
}

func TestGPT61SolReasoningPolicyKeepsSupportedMinimum(t *testing.T) {
	for _, policy := range []struct {
		name, max string
		mappings  []ReasoningEffortMapping
	}{
		{name: "ceiling", max: "minimal"},
		{name: "mapping", mappings: []ReasoningEffortMapping{{From: "low", To: "minimal"}}},
	} {
		t.Run(policy.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-6.1-sol","reasoning":{"effort":"low"}}`)
			got, _ := ApplyOpenAIReasoningEffortPolicy(body, policy.max, policy.mappings)
			require.Equal(t, "low", gjson.GetBytes(got, "reasoning.effort").String())
			account := rawChatCompletionsTestAccount()
			account.Credentials["model_mapping"] = map[string]any{"custom-sol": "gpt-6.1-sol"}
			ctx := WithOpenAIReasoningEffortPolicy(context.Background(), policy.max, policy.mappings)
			aliasBody := []byte(`{"model":"custom-sol","reasoning":{"effort":"none"}}`)
			got = normalizeAstraReasoningEffortForAccount(ctx, account, aliasBody, "")
			require.Equal(t, "low", gjson.GetBytes(got, "reasoning.effort").String())
			got = normalizeAstraReasoningEffortForWS(aliasBody, "gpt-6.1-sol", &OpenAIWSIngressHooks{MaxReasoningEffort: policy.max, ReasoningEffortMappings: policy.mappings})
			require.Equal(t, "low", gjson.GetBytes(got, "reasoning.effort").String())
		})
	}
}

func TestGPT61SolMappedReasoningPolicyAppliesOnce(t *testing.T) {
	account := rawChatCompletionsTestAccount()
	account.Credentials["model_mapping"] = map[string]any{"custom-sol": "gpt-6.1-sol"}
	for _, tt := range []struct {
		input, want string
		mappings    []ReasoningEffortMapping
	}{
		{"high", "low", []ReasoningEffortMapping{{From: "high", To: "minimal"}, {From: "low", To: "high"}}},
		{"minimal", "high", []ReasoningEffortMapping{{From: "minimal", To: "max"}, {From: "low", To: "high"}}},
	} {
		hooks := &OpenAIWSIngressHooks{ReasoningEffortMappings: tt.mappings}
		for _, model := range []string{"gpt-6.1-sol", "custom-sol"} {
			for _, path := range []string{"reasoning.effort", "reasoning_effort"} {
				t.Run(model+"/"+path+"/"+tt.input, func(t *testing.T) {
					body := []byte(fmt.Sprintf(`{"model":%q,"reasoning":{"effort":%q}}`, model, tt.input))
					if path == "reasoning_effort" {
						body = []byte(fmt.Sprintf(`{"model":%q,"reasoning_effort":%q}`, model, tt.input))
					}
					// The handler snapshots effort, then applies policy before aliases resolve.
					ctx := WithOpenAIReasoningEffortPolicy(context.Background(), "", tt.mappings, body)
					firstPass, _ := ApplyOpenAIReasoningEffortPolicyFromContext(ctx, body)
					got := normalizeAstraReasoningEffortForAccount(ctx, account, firstPass, "")
					require.Equal(t, tt.want, gjson.GetBytes(got, path).String(), "HTTP policy must map once")
					require.Equal(t, model, gjson.GetBytes(got, "model").String())
					originalEfforts := captureOpenAIReasoningEffortInput(body)
					firstPass, _ = ApplyOpenAIReasoningEffortPolicy(body, "", tt.mappings)
					got = normalizeAstraReasoningEffortForWS(firstPass, "gpt-6.1-sol", hooks, originalEfforts)
					require.Equal(t, tt.want, gjson.GetBytes(got, path).String(), "WS policy must map once")
				})
			}
		}
	}
}

func TestGPT61SolReasoningSnapshotPreservesOriginalFields(t *testing.T) {
	account := rawChatCompletionsTestAccount()
	account.Credentials["model_mapping"] = map[string]any{"custom-sol": "gpt-6.1-sol"}
	for _, fields := range []string{``, `,"reasoning":{"effort":null}`, `,"reasoning_effort":123`} {
		body := []byte(`{"model":"custom-sol","input":"original prompt"` + fields + `}`)
		ctx := WithOpenAIReasoningEffortPolicy(context.Background(), "low", nil, body)
		originalEfforts := captureOpenAIReasoningEffortInput(body)
		for i := range body {
			body[i] = ' '
		}
		forwardBody := []byte(`{"model":"gpt-6.1-sol","input":"transformed prompt","reasoning":{"effort":"high"},"reasoning_effort":"high","tools":[{"type":"web_search"}]}`)
		for _, got := range [][]byte{
			normalizeAstraReasoningEffortForAccount(ctx, account, forwardBody, ""),
			normalizeAstraReasoningEffortForWS(forwardBody, "gpt-6.1-sol", &OpenAIWSIngressHooks{MaxReasoningEffort: "low"}, originalEfforts),
		} {
			require.Equal(t, "gpt-6.1-sol", gjson.GetBytes(got, "model").String())
			require.Equal(t, "transformed prompt", gjson.GetBytes(got, "input").String())
			require.Equal(t, "web_search", gjson.GetBytes(got, "tools.0.type").String())
			for _, path := range []string{"reasoning.effort", "reasoning_effort"} {
				want, exists := originalEfforts[path]
				value := gjson.GetBytes(got, path)
				require.Equal(t, exists, value.Exists(), path)
				if exists {
					require.Equal(t, want, value.Raw, path)
				}
			}
		}
	}
}

func TestGPT61SolToolsForwardThroughResponses(t *testing.T) {
	for _, oauth := range []bool{false, true} {
		for _, tt := range []struct{ path, body string }{
			{"/v1/responses", `{"model":"gpt-6.1-sol","stream":false,"input":"hello","reasoning":{"effort":"none"},"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{}}}]}`},
			{"/v1/chat/completions", `{"model":"gpt-6.1-sol","stream":false,"messages":[{"role":"user","content":"hello"}],"reasoning_effort":"none","tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{}}}}]}`},
			{"/v1/messages", `{"model":"gpt-6.1-sol","stream":false,"max_tokens":128,"messages":[{"role":"user","content":"hello"}],"output_config":{"effort":"none"},"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{}}}]}`},
		} {
			t.Run(fmt.Sprintf("%s/oauth=%t", tt.path, oauth), func(t *testing.T) {
				account := rawChatCompletionsTestAccount()
				if oauth {
					account.Type = AccountTypeOAuth
					account.Credentials = map[string]any{"access_token": "oauth-test", "chatgpt_account_id": "account-test"}
				}
				upstream := &httpUpstreamRecorder{resp: serviceTierTestResponse("default", false, tt.path != "/v1/responses")}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
				var err error
				switch tt.path {
				case "/v1/messages":
					_, err = svc.ForwardAsAnthropic(context.Background(), c, account, []byte(tt.body), "", "")
				case "/v1/chat/completions":
					_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, []byte(tt.body), "", "")
				default:
					_, err = svc.Forward(context.Background(), c, account, []byte(tt.body))
				}
				require.NoError(t, err)
				require.True(t, strings.HasSuffix(upstream.lastReq.URL.Path, "/responses"))
				require.Equal(t, "gpt-6.1-sol", gjson.GetBytes(upstream.lastBody, "model").String())
				require.Equal(t, "low", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
				require.Equal(t, "lookup", gjson.GetBytes(upstream.lastBody, "tools.0.name").String())
			})
		}
	}
}
