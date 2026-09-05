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

func forwardAstraCompatRequest(t *testing.T, svc *OpenAIGatewayService, account *Account, path, body string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	var err error
	if path == "/v1/messages" {
		_, err = svc.ForwardAsAnthropic(context.Background(), c, account, []byte(body), "", "")
	} else {
		_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, []byte(body), "", "")
	}
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, c.Writer.Status())
}

func TestGPT6AstraForwardSamplingUsesMappedModel(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/chat/completions"} {
		for _, model := range []string{"gpt-6-astra", "custom-astra-alias"} {
			t.Run(path+"/"+model, func(t *testing.T) {
				account := rawChatCompletionsTestAccount()
				account.Credentials["model_mapping"] = map[string]any{"custom-astra-alias": "gpt-6-astra"}
				upstream := &httpUpstreamRecorder{resp: serviceTierTestResponse("default", false, true)}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
				body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}],"max_tokens":128,"temperature":0.7,"top_p":1,"stream":false}`, model)
				forwardAstraCompatRequest(t, svc, account, path, body)
				require.Equal(t, "gpt-6-astra", gjson.GetBytes(upstream.lastBody, "model").String())
				require.False(t, gjson.GetBytes(upstream.lastBody, "temperature").Exists())
				require.False(t, gjson.GetBytes(upstream.lastBody, "top_p").Exists())
			})
		}
	}
}

func TestGPT6AstraResponsesShapeSamplingUsesMappedModel(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		mappedModel  string
		wantModel    string
		wantSampling bool
	}{
		{name: "direct Astra", model: "gpt-6-astra", wantModel: "gpt-6-astra"},
		{name: "mapped alias", model: "custom-astra-alias", mappedModel: "gpt-6-astra", wantModel: "gpt-6-astra"},
		{name: "non-Astra model", model: "gpt-4o", wantModel: "gpt-4o", wantSampling: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := rawChatCompletionsTestAccount()
			if tt.mappedModel != "" {
				account.Credentials["model_mapping"] = map[string]any{tt.model: tt.mappedModel}
			}
			upstream := &httpUpstreamRecorder{resp: serviceTierTestResponse("default", false, true)}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			body := fmt.Sprintf(`{"model":%q,"input":[{"role":"user","content":"hello"}],"temperature":0.7,"top_p":0.9,"stream":false}`, tt.model)

			forwardAstraCompatRequest(t, svc, account, "/v1/chat/completions", body)

			require.Equal(t, tt.wantModel, gjson.GetBytes(upstream.lastBody, "model").String())
			require.Equal(t, tt.wantSampling, gjson.GetBytes(upstream.lastBody, "temperature").Exists())
			require.Equal(t, tt.wantSampling, gjson.GetBytes(upstream.lastBody, "top_p").Exists())
			if tt.wantSampling {
				require.InDelta(t, 0.7, gjson.GetBytes(upstream.lastBody, "temperature").Float(), 1e-9)
				require.InDelta(t, 0.9, gjson.GetBytes(upstream.lastBody, "top_p").Float(), 1e-9)
			}
		})
	}
}

func TestGPT6AstraMessagesForwardReusesCacheAndResponse(t *testing.T) {
	account := rawChatCompletionsTestAccount()
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		serviceTierTestResponse("default", false, true),
		serviceTierTestResponse("default", false, true),
	}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	forwardAstraCompatRequest(t, svc, account, "/v1/messages", `{"model":"gpt-6-astra","max_tokens":128,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	firstKey := gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String()
	require.NotEmpty(t, firstKey)
	require.Empty(t, gjson.GetBytes(upstream.lastBody, "previous_response_id").String())
	forwardAstraCompatRequest(t, svc, account, "/v1/messages", `{"model":"gpt-6-astra","max_tokens":128,"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"ok"},{"role":"user","content":"continue"}],"stream":false}`)
	require.Equal(t, firstKey, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String())
	require.Equal(t, "resp_tier", gjson.GetBytes(upstream.lastBody, "previous_response_id").String())
	var userMessages []string
	for _, item := range gjson.GetBytes(upstream.lastBody, "input").Array() {
		require.NotEqual(t, "assistant", item.Get("role").String())
		if item.Get("role").String() == "user" {
			userMessages = append(userMessages, item.Get("content.0.text").String())
		}
	}
	require.Equal(t, []string{"continue"}, userMessages)
}

func TestGPT6AstraOAuthCompatKeepsStableSession(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/chat/completions"} {
		t.Run(path, func(t *testing.T) {
			account := rawChatCompletionsTestAccount()
			account.Type = AccountTypeOAuth
			account.Credentials = map[string]any{"access_token": "oauth-test", "chatgpt_account_id": "account-test"}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				serviceTierTestResponse("default", false, true),
				serviceTierTestResponse("default", false, true),
			}}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			forwardAstraCompatRequest(t, svc, account, path, `{"model":"gpt-6-astra","max_tokens":128,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
			session := upstream.lastReq.Header.Get("session_id")
			require.NotEmpty(t, session)
			forwardAstraCompatRequest(t, svc, account, path, `{"model":"gpt-6-astra","max_tokens":128,"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"ok"},{"role":"user","content":"continue"}],"stream":false}`)
			require.Equal(t, session, upstream.lastReq.Header.Get("session_id"))
		})
	}
}
