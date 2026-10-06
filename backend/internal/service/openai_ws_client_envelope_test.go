package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIWSClientEnvelope(t *testing.T) {
	for _, tt := range []struct {
		name    string
		payload string
		invalid bool
	}{
		{"response create", ` { "type": "response.create", "model": "gpt-5.4" } `, false},
		{"session update", `{"type":"session.update","session":{"model":"gpt-5.4"}}`, false},
		{"escaped discriminator", `{"\u0074ype":"response.create","model":"gpt-5.4"}`, false},
		{"nested type fields", `{"type":"conversation.item.create","item":{"type":"message","content":[{"type":"input_text","text":"hello"}]}}`, false},
		{"missing type preserves native default", `{"model":"gpt-5.4"}`, false},
		{"duplicate type", `{"type":"session.update","type":"response.create"}`, true},
		{"escaped duplicate type", `{"type":"session.update","\u0074ype":"response.create"}`, true},
		{"escaped first type", `{"\u0074ype":"session.update","type":"response.create"}`, true},
		{"same duplicate values", `{"type":"response.create","type":"response.create"}`, true},
		{"multiple documents", `{"type":"session.update"}{"type":"response.create"}`, true},
		{"array", `[{"type":"response.create"}]`, true},
		{"malformed", `{"type":`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			payload := []byte(tt.payload)
			err := validateOpenAIWSClientEnvelope(payload)
			if tt.invalid {
				require.ErrorIs(t, err, ErrOpenAIWSInvalidClientEnvelope)
				var closeErr *OpenAIWSClientCloseError
				require.ErrorAs(t, err, &closeErr)
				require.Equal(t, coderws.StatusPolicyViolation, closeErr.StatusCode())
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.payload, string(payload), "envelope validation must preserve payload bytes")
		})
	}
}

func TestOpenAIWSClientEnvelopeRejectsFirstFrameInAllModes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{OpenAIWSIngressModePassthrough, OpenAIWSIngressModeCtxPool, OpenAIWSIngressModeHTTPBridge} {
		t.Run(mode, func(t *testing.T) {
			upstream := newStagedPassthroughConn()
			svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
			account := passthroughLifecycleAccount()
			account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
			server, serverErr := startPassthroughLifecycleServer(t, context.Background(), svc, account)
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			require.NoError(t, err)
			defer func() { _ = client.CloseNow() }()
			require.NoError(t, client.Write(ctx, coderws.MessageText,
				[]byte(`{"type":"session.update","\u0074ype":"response.create","model":"gpt-5.4"}`)))
			select {
			case err := <-serverErr:
				require.ErrorIs(t, err, ErrOpenAIWSInvalidClientEnvelope)
			case <-ctx.Done():
				t.Fatal("ambiguous first frame was not rejected")
			}
			require.Empty(t, upstream.writes)
			recorder, ok := svc.httpUpstream.(*httpUpstreamRecorder)
			require.True(t, ok)
			require.Empty(t, recorder.requests)
		})
	}
}

func TestOpenAIWSClientEnvelopeHTTPBridgeRejectsSubsequentFrame(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_envelope\",\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\ndata: [DONE]\n\n")),
	}}
	svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), newStagedPassthroughConn())
	svc.httpUpstream = upstream
	account := passthroughLifecycleAccount()
	account.Extra["openai_apikey_responses_websockets_v2_mode"] = OpenAIWSIngressModeHTTPBridge
	server, serverErr := startPassthroughLifecycleServer(t, context.Background(), svc, account)
	defer server.Close()
	client := dialPassthroughLifecycleClient(t, server)
	defer func() { _ = client.CloseNow() }()
	_, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, client.Write(ctx, coderws.MessageText,
		[]byte(`{"type":"response.create","\u0074ype":"response.create","model":"gpt-5.4"}`)))
	select {
	case err := <-serverErr:
		require.ErrorIs(t, err, ErrOpenAIWSInvalidClientEnvelope)
	case <-ctx.Done():
		t.Fatal("ambiguous subsequent frame was not rejected")
	}
	require.Len(t, upstream.requests, 1)
}

func TestOpenAIWSClientEnvelopePreservesOrdinaryPassthroughFrames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := newStagedPassthroughConn()
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_envelope","usage":{"input_tokens":2,"output_tokens":1}}}`)
	svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
	server, _ := startPassthroughLifecycleServer(t, context.Background(), svc, passthroughLifecycleAccount())
	defer server.Close()
	client := dialPassthroughLifecycleClient(t, server)
	defer func() { _ = client.CloseNow() }()
	_, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
	require.NoError(t, err)
	requirePassthroughUpstreamWrite(t, upstream, time.Second)
	for _, frame := range []string{
		` { "type" : "session.update", "session" : {"model":"gpt-5.4"} } `,
		`{"type":"response.cancel","response_id":"resp_envelope"}`,
		`{"type":"conversation.item.create","item":{"type":"message","content":[{"type":"input_text","text":"hello"}]}}`,
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := client.Write(ctx, coderws.MessageText, []byte(frame))
		cancel()
		require.NoError(t, err)
		require.Equal(t, frame, string(requirePassthroughUpstreamWrite(t, upstream, time.Second)))
	}
}
