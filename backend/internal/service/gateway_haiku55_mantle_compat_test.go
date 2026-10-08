package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestHaiku55MantleCompatibilityMappedModels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []string{"claude-haiku-5-5", "claude-sonnet-5-5"} {
		for _, auth := range []string{"apikey", "sigv4"} {
			for _, endpoint := range []string{"responses", "chat/completions"} {
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/stream=%t", model, auth, endpoint, stream), func(t *testing.T) {
						payload := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_mantle55\",\"model\":\"" + model + "\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":11}}}\n\n" +
							"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n" +
							"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"signed-mantle-progress\"}}\n\n" +
							"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
							"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
							"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"answer\"}}\n\n" +
							"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
							"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n" +
							"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
						upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}, "Request-Id": {"req_mantle55"}}, Body: io.NopCloser(strings.NewReader(payload))}}
						account := &Account{ID: 2055, Platform: PlatformAnthropic, Type: AccountTypeBedrock, Credentials: map[string]any{
							"auth_mode": auth, "api_key": "mantle-test-key", "aws_region": "eu-west-1",
							"aws_access_key_id": "MANTLEACCESS", "aws_secret_access_key": "mantle-test-secret", "aws_session_token": "mantle-test-session",
							"model_mapping": map[string]any{"mantle-alias": model},
						}}
						svc := newForwardPartialUsageServiceForTest(upstream)
						require.True(t, svc.isModelSupportedByAccount(account, "mantle-alias"))
						recorder := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(recorder)
						c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, nil)
						var result *ForwardResult
						var err error
						if endpoint == "responses" {
							body := []byte(fmt.Sprintf(`{"model":"mantle-alias","stream":%t,"input":"hello","reasoning":{"effort":"xhigh"}}`, stream))
							result, err = svc.ForwardAsResponses(context.Background(), c, account, body, nil)
						} else {
							body := []byte(fmt.Sprintf(`{"model":"mantle-alias","stream":%t,"messages":[{"role":"user","content":"hello"}],"reasoning_effort":"xhigh"}`, stream))
							result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, nil)
						}
						require.NoError(t, err)
						require.NotNil(t, result)
						require.Equal(t, "req_mantle55", result.RequestID)
						require.NotNil(t, upstream.lastReq)
						require.Equal(t, "https://bedrock-mantle.eu-west-1.api.aws/anthropic/v1/messages", upstream.lastReq.URL.String())
						if auth == "apikey" {
							require.Equal(t, "Bearer mantle-test-key", getHeaderRaw(upstream.lastReq.Header, "Authorization"))
						} else {
							require.Contains(t, getHeaderRaw(upstream.lastReq.Header, "Authorization"), "/eu-west-1/bedrock-mantle/aws4_request")
							require.Equal(t, "mantle-test-session", upstream.lastReq.Header.Get("X-Amz-Security-Token"))
						}
						require.Equal(t, "anthropic."+model, gjson.GetBytes(upstream.lastBody, "model").String())
						require.Equal(t, "adaptive", gjson.GetBytes(upstream.lastBody, "thinking.type").String())
						require.Equal(t, "xhigh", gjson.GetBytes(upstream.lastBody, "output_config.effort").String())
						require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
						require.False(t, gjson.GetBytes(upstream.lastBody, "anthropic_version").Exists())
						require.False(t, gjson.GetBytes(upstream.lastBody, "anthropic_beta").Exists())
						require.Equal(t, "mantle-alias", result.Model)
						require.Equal(t, "anthropic."+model, result.UpstreamModel)
						require.Equal(t, model, result.UpstreamResponseModel)
						require.NotNil(t, result.ReasoningEffort)
						require.Equal(t, "xhigh", *result.ReasoningEffort)
						require.Equal(t, 11, result.Usage.InputTokens)
						require.Equal(t, 5, result.Usage.OutputTokens)
						require.Contains(t, recorder.Body.String(), "answer")
						if endpoint == "responses" {
							require.Contains(t, recorder.Body.String(), "anthropic-thinking-v1:")
						}
					})
				}
			}
		}
	}
}
