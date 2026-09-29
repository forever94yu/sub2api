//go:build unit

package service

import (
	"context"
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

func TestSonnet55VertexCountTokensRequest(t *testing.T) {
	for _, tt := range []struct{ location, host string }{
		{"global", "aiplatform.googleapis.com"},
		{"us", "aiplatform.us.rep.googleapis.com"},
		{"eu", "aiplatform.eu.rep.googleapis.com"},
		{"asia-southeast1", "asia-southeast1-aiplatform.googleapis.com"},
	} {
		t.Run(tt.location, func(t *testing.T) {
			account := &Account{Platform: PlatformAnthropic, Type: AccountTypeServiceAccount, Credentials: map[string]any{
				"project_id": "test-project", "location": tt.location,
			}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
			c.Request.Header.Set("Authorization", "Bearer client-credential")
			c.Request.Header.Set("X-Api-Key", "client-credential")
			c.Request.Header.Set("X-Goog-Api-Key", "client-credential")
			c.Request.Header.Set("Cookie", "client-credential")
			c.Request.Header.Set("Anthropic-Version", "2023-06-01")
			c.Request.Header.Set("Anthropic-Beta", claude.BetaOAuth+","+claude.BetaInterleavedThinking)
			body := []byte(`{"model":"claude-sonnet-5-5","anthropic_version":"2023-06-01","stream":true,"max_tokens":2048,"temperature":1,"thinking":{"type":"between_tools"},"output_config":{"effort":"low"},"context_management":{"edits":[]},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"signed-progress"},{"type":"text","text":"answer"}]}]}`)
			svc := &GatewayService{}
			req, wire, err := svc.buildCountTokensRequest(context.Background(), c, account, body, "vertex-token", "service_account", "claude-sonnet-5-5", false)
			require.NoError(t, err)
			require.Equal(t, "https://"+tt.host+"/v1/projects/test-project/locations/"+tt.location+"/publishers/anthropic/models/count-tokens:rawPredict", req.URL.String())
			require.Equal(t, "Bearer vertex-token", getHeaderRaw(req.Header, "authorization"))
			for _, key := range []string{"x-api-key", "x-goog-api-key", "cookie", "anthropic-version"} {
				require.Empty(t, getHeaderRaw(req.Header, key), key)
			}
			require.Equal(t, claude.BetaInterleavedThinking, getHeaderRaw(req.Header, "anthropic-beta"))
			require.Equal(t, "claude-sonnet-5-5", gjson.GetBytes(wire, "model").String())
			require.Equal(t, vertexAnthropicVersion, gjson.GetBytes(wire, "anthropic_version").String())
			require.Equal(t, "between_tools", gjson.GetBytes(wire, "thinking.type").String())
			require.Equal(t, "signed-progress", gjson.GetBytes(wire, "messages.0.content.0.signature").String())
			for _, field := range []string{"stream", "max_tokens", "temperature", "context_management"} {
				require.False(t, gjson.GetBytes(wire, field).Exists(), field)
			}
			require.Equal(t, wire, readRequestBodyForTest(t, req))
		})
	}
}

func TestSonnet55VertexCountTokensForwardUsesMappedModelAndLocation(t *testing.T) {
	for _, model := range []string{"claude-sonnet-5-5", "claude-haiku-4-5-20251001"} {
		t.Run(model, func(t *testing.T) {
			effectiveModel := normalizeVertexAnthropicModelID(model)
			account := &Account{ID: 301, Platform: PlatformAnthropic, Type: AccountTypeServiceAccount, Credentials: map[string]any{
				"project_id": "test-project", "location": "us-east5",
				"model_mapping":          map[string]any{"alias": model},
				"vertex_model_locations": map[string]any{effectiveModel: "eu"},
				"service_account_json": map[string]any{
					"project_id": "test-project", "client_email": "unit@example.invalid", "private_key": "cached-test-key", "private_key_id": "unit",
				},
			}}
			key, err := parseVertexServiceAccountKey(account)
			require.NoError(t, err)
			cache := newClaudeTokenCacheStub()
			cache.tokens[vertexServiceAccountCacheKey(account, key)] = "vertex-token"
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"input_tokens":42}`))}}
			svc := newForwardPartialUsageServiceForTest(upstream)
			svc.claudeTokenProvider = NewClaudeTokenProvider(nil, cache, nil)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
			parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(`{"model":"alias","messages":[{"role":"user","content":"hello"}]}`)), PlatformAnthropic)
			require.NoError(t, err)
			require.NoError(t, svc.ForwardCountTokens(context.Background(), c, account, parsed))
			require.Equal(t, http.StatusOK, recorder.Code)
			require.JSONEq(t, `{"input_tokens":42}`, recorder.Body.String())
			require.NotNil(t, upstream.lastReq)
			require.Equal(t, "https://aiplatform.eu.rep.googleapis.com/v1/projects/test-project/locations/eu/publishers/anthropic/models/count-tokens:rawPredict", upstream.lastReq.URL.String())
			require.Equal(t, "Bearer vertex-token", getHeaderRaw(upstream.lastReq.Header, "authorization"))
			require.Equal(t, effectiveModel, gjson.GetBytes(upstream.lastBody, "model").String())
			require.Equal(t, upstream.lastBody, parsed.Body.Bytes())
		})
	}
}

func TestSonnet55VertexCountTokensUnsupportedLocationReturns404(t *testing.T) {
	for _, location := range []string{"us-east5", "europe-west1", ""} {
		t.Run(location, func(t *testing.T) {
			account := &Account{Platform: PlatformAnthropic, Type: AccountTypeServiceAccount, Credentials: map[string]any{"project_id": "test-project", "location": location}}
			upstream := &anthropicHTTPUpstreamRecorder{}
			svc := newForwardPartialUsageServiceForTest(upstream)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
			parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(`{"model":"claude-sonnet-5-5","messages":[{"role":"user","content":"hello"}]}`)), PlatformAnthropic)
			require.NoError(t, err)
			require.NoError(t, svc.ForwardCountTokens(context.Background(), c, account, parsed))
			require.Equal(t, http.StatusNotFound, recorder.Code)
			require.Contains(t, recorder.Body.String(), "count_tokens endpoint is not supported in Vertex location")
			require.Nil(t, upstream.lastReq)
		})
	}
}
