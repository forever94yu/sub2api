package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Model availability is an upstream contract, not the value of our default constant.
type imageOAuthModelPolicyUpstream struct {
	httpUpstreamRecorder
}

func (u *imageOAuthModelPolicyUpstream) Do(req *http.Request, proxyURL string, accountID int64, concurrency int) (*http.Response, error) {
	resp, err := u.httpUpstreamRecorder.Do(req, proxyURL, accountID, concurrency)
	if err != nil {
		return nil, err
	}
	model := gjson.GetBytes(u.lastBody, "model").String()
	if req.URL.Host == "chatgpt.com" && (model == "gpt-5.4-mini" || model == "gpt-5.4") {
		_ = resp.Body.Close()
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(fmt.Sprintf(
				`{"error":{"type":"invalid_request_error","message":"The '%s' model is not supported when using Codex with a ChatGPT account."}}`, model))),
		}, nil
	}
	return resp, nil
}

func newImageOAuthModelPolicyUpstream() *imageOAuthModelPolicyUpstream {
	return &imageOAuthModelPolicyUpstream{httpUpstreamRecorder: httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"data: " + `{"type":"response.completed","response":{"id":"resp_image","status":"completed","created_at":1710000000,"model":"gpt-5.6-luna","usage":{"input_tokens":11,"output_tokens":17},"tool_usage":{"image_gen":{"input_tokens":11,"output_tokens":17,"input_tokens_details":{"image_tokens":3,"cached_tokens":2},"output_tokens_details":{"image_tokens":13},"images":1}},"output":[{"id":"ig_image","type":"image_generation_call","status":"completed","result":"aW1hZ2U=","output_format":"png","size":"1024x1024"}]}}` + "\n\ndata: [DONE]\n\n")),
		},
	}}
}

func imageOAuthModelTestAccount() *Account {
	return &Account{
		ID: 196, Name: "image-oauth-model-test", Platform: PlatformOpenAI,
		Type: AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{"access_token": "synthetic-token", "chatgpt_account_id": "synthetic-account"},
	}
}

func TestImageOAuthMainModelRetirement_AccountTest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, imageModel := range []string{
		"gpt-image-2.5-sunburst", "gpt-image-2.5-sunburst-2026-09-08",
		"gpt-image-2.5-flare", "gpt-image-2.5-flare-2026-09-08", "gpt-image-2",
	} {
		t.Run(imageModel, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/196/test", nil)
			upstream := newImageOAuthModelPolicyUpstream()
			svc := &AccountTestService{httpUpstream: upstream}

			err := svc.testOpenAIImageOAuth(c, context.Background(), imageOAuthModelTestAccount(), imageModel, "draw a blue square")
			require.NoError(t, err)
			require.Equal(t, "gpt-5.6-luna", gjson.GetBytes(upstream.lastBody, "model").String())
			require.Equal(t, imageModel, gjson.GetBytes(upstream.lastBody, "tools.0.model").String())
			require.Contains(t, rec.Body.String(), "data:image/png;base64,aW1hZ2U=")
			require.Contains(t, rec.Body.String(), `"success":true`)
		})
	}
}

func TestImageOAuthMainModelRetirement_ForwardImages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"/v1/images/generations", "/v1/images/edits"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", endpoint, stream), func(t *testing.T) {
				payload := map[string]any{
					"model": "gpt-image-2.5-flare-2026-09-08", "prompt": "draw a blue square", "stream": stream,
				}
				if endpoint == "/v1/images/edits" {
					payload["images"] = []any{map[string]any{"image_url": "data:image/png;base64,aW1hZ2U="}}
				}
				body, err := json.Marshal(payload)
				require.NoError(t, err)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				upstream := newImageOAuthModelPolicyUpstream()
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
				parsed, err := svc.ParseOpenAIImagesRequest(c, body)
				require.NoError(t, err)

				result, err := svc.ForwardImages(context.Background(), c, imageOAuthModelTestAccount(), body, parsed, "")
				require.NoError(t, err)
				require.Equal(t, "gpt-5.6-luna", gjson.GetBytes(upstream.lastBody, "model").String())
				require.Equal(t, "gpt-image-2.5-flare-2026-09-08", gjson.GetBytes(upstream.lastBody, "tools.0.model").String())
				require.Equal(t, "gpt-image-2.5-flare-2026-09-08", result.Model)
				require.Equal(t, "gpt-image-2.5-flare-2026-09-08", result.UpstreamModel)
				require.Equal(t, 1, result.ImageCount)
				require.Equal(t, 3, result.Usage.ImageInputTokens)
				require.Equal(t, 2, result.Usage.CacheReadInputTokens)
				require.Equal(t, 13, result.Usage.ImageOutputTokens)
				require.Contains(t, rec.Body.String(), "aW1hZ2U=")
			})
		}
	}
}

func TestImageOAuthMainModelRetirement_ForwardResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name        string
		account     string
		model       string
		wantModel   string
		defaultTool bool
	}{
		{"oauth_image_only", AccountTypeOAuth, "gpt-image-2.5-flare-2026-09-08", "gpt-5.6-luna", false},
		{"oauth_explicit_text_model", AccountTypeOAuth, "gpt-6-astra", "gpt-6-astra", false},
		{"oauth_default_image_tool", AccountTypeOAuth, "gpt-6-astra", "gpt-6-astra", true},
		{"apikey_image_only", AccountTypeAPIKey, "gpt-image-2.5-flare-2026-09-08", "gpt-5.4-mini", false},
		{"apikey_explicit_text_model", AccountTypeAPIKey, "gpt-5.4-mini", "gpt-5.4-mini", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := map[string]any{"type": "image_generation", "quality": "max"}
			wantToolModel := "gpt-image-2.5-flare-2026-09-08"
			if tc.defaultTool {
				wantToolModel = "gpt-image-2.5-sunburst"
			} else {
				tool["model"] = "gpt-image-2.5-flare-2026-09-08"
			}
			body, err := json.Marshal(map[string]any{
				"model": tc.model, "input": "draw a blue square", "stream": false,
				"tools": []any{tool},
			})
			require.NoError(t, err)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set("api_key", &APIKey{Group: &Group{AllowImageGeneration: true}})
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			account := imageOAuthModelTestAccount()
			account.Type = tc.account
			if tc.account == AccountTypeAPIKey {
				account.Credentials = map[string]any{"api_key": "synthetic-key", "base_url": "https://api.openai.com"}
				account.Extra = map[string]any{"use_responses_api": true}
			}
			upstream := newImageOAuthModelPolicyUpstream()
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}

			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tc.wantModel, gjson.GetBytes(upstream.lastBody, "model").String())
			require.Equal(t, wantToolModel, gjson.GetBytes(upstream.lastBody, "tools.0.model").String())
			require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "tools.0.quality").String())
			require.Equal(t, wantToolModel, result.BillingModel)
		})
	}
}
