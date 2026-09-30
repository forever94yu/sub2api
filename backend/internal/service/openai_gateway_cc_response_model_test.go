//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestChatFallbackPreservesUpstreamResponseModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const jsonBody = `{"id":"chatcmpl_model","object":"chat.completion","model":"gpt-6.1-sol","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
	const firstChunk = "data: " + `{"id":"chatcmpl_model","object":"chat.completion.chunk","model":"gpt-6.1-sol","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":null}]}` + "\n\n"
	const conflictingChunk = "data: " + `{"id":"chatcmpl_model","object":"chat.completion.chunk","model":"gpt-6-sol","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}` + "\n\n"
	const done = "data: [DONE]\n\n"
	for _, endpoint := range []string{"/v1/messages", "/v1/responses"} {
		for _, tt := range []struct {
			name, payload, observed string
			stream, conflict        bool
			readErr                 error
		}{
			{name: "JSON", payload: jsonBody, observed: "gpt-6.1-sol"},
			{name: "SSE", payload: firstChunk + done, observed: "gpt-6.1-sol", stream: true},
			{name: "SSE conflict", payload: firstChunk + conflictingChunk + done, observed: "gpt-6.1-sol", stream: true, conflict: true},
			{name: "SSE interrupted", payload: firstChunk, observed: "gpt-6.1-sol", stream: true, readErr: io.ErrUnexpectedEOF},
			{name: "JSON without model", payload: `{"id":"chatcmpl_empty","choices":[]}`},
			{name: "SSE without model", payload: done, stream: true},
		} {
			t.Run(endpoint+"/"+tt.name, func(t *testing.T) {
				request := map[string]any{"model": "gpt-5.4", "stream": tt.stream}
				if endpoint == "/v1/messages" {
					request["max_tokens"] = 32
					request["messages"] = []map[string]string{{"role": "user", "content": "hello"}}
				} else {
					request["input"] = "hello"
				}
				body, err := json.Marshal(request)
				require.NoError(t, err)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				contentType := "application/json"
				if tt.stream {
					contentType = "text/event-stream"
				}
				var responseBody io.ReadCloser = io.NopCloser(strings.NewReader(tt.payload))
				if tt.readErr != nil {
					responseBody = &errTailReader{data: []byte(tt.payload), err: tt.readErr}
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{contentType}},
					Body:       responseBody,
				}}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
				var result *OpenAIForwardResult
				if endpoint == "/v1/messages" {
					result, err = svc.ForwardAsAnthropic(context.Background(), c, forceChatMessagesFallbackAccount(), body, "", "")
				} else {
					result, err = svc.Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
				}
				if tt.readErr != nil {
					require.ErrorIs(t, err, tt.readErr)
				} else {
					require.NoError(t, err)
				}
				require.NotNil(t, result)
				require.Equal(t, "gpt-5.4", result.Model)
				require.Equal(t, "gpt-5.4", result.BillingModel)
				require.Equal(t, "gpt-5.4", result.UpstreamModel)
				require.Equal(t, tt.observed, result.UpstreamResponseModel)
				require.Equal(t, tt.conflict, result.UpstreamResponseModelConflict)
			})
		}
	}
}
