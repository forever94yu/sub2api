package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func TestGatewayCompatibilityRegressionFullForwardPreservesChatContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ name, body string }{
		{"developer", `{"model":"gpt-5.2","messages":[{"role":"developer","content":"Always produce JSON"},{"role":"user","content":"Hello"}]}`},
		{"tool_choice", `{"model":"gpt-5.2","messages":[{"role":"user","content":"Weather?"}],"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{}}}}],"tool_choice":{"type":"function","function":{"name":"get_weather"}}}`},
		{"tool_choice_apikey", `{"model":"gpt-5.2","messages":[{"role":"user","content":"Weather?"}],"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{}}}}],"tool_choice":{"type":"function","function":{"name":"get_weather"}}}`},
		{"legacy_history", `{"model":"gpt-5.2","messages":[{"role":"user","content":"Weather?"},{"role":"assistant","content":null,"function_call":{"name":"get_weather","arguments":"{}"}},{"role":"function","name":"get_weather","content":"Sunny"}],"functions":[{"name":"get_weather","parameters":{"type":"object","properties":{}}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tc.body))
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"status\":\"completed\",\"model\":\"gpt-5.2\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"OK\"}]}],\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n\n"))}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			account := &Account{ID: 991, Name: "local-review", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1, Credentials: map[string]any{"access_token": "local-test", "chatgpt_account_id": "local-test"}}
			if tc.name == "tool_choice_apikey" {
				account.Type = AccountTypeAPIKey
				account.Credentials = map[string]any{"api_key": "local-test"}
			}
			_, err := svc.ForwardAsChatCompletions(context.Background(), c, account, []byte(tc.body), "", "")
			if err != nil {
				t.Fatalf("forward failed before contract check: %v", err)
			}
			valid := false
			switch tc.name {
			case "developer":
				valid = gjson.GetBytes(upstream.lastBody, "input.0.role").String() == "developer" || strings.Contains(gjson.GetBytes(upstream.lastBody, "instructions").String(), "Always produce JSON")
			case "tool_choice", "tool_choice_apikey":
				valid = gjson.GetBytes(upstream.lastBody, "tool_choice.name").String() == "get_weather"
			case "legacy_history":
				for _, item := range gjson.GetBytes(upstream.lastBody, "input").Array() {
					if item.Get("type").String() == "function_call" {
						valid = true
					}
				}
			}
			if !valid {
				t.Fatalf("production ForwardAsChatCompletions does not repair %s; upstream_url=%s upstream_body=%s", tc.name, upstream.lastReq.URL, upstream.lastBody)
			}
		})
	}
}

const compatRegressionAnthropicStart = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_review\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-5\",\"stop_reason\":null,\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n\n"
const compatRegressionAnthropicText = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial answer\"}}\n\n"

func TestGatewayCompatibilityRegressionAnthropicBridgeRejectsInterruptedResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"cc_buffered", "cc_stream", "responses_buffered", "responses_stream"} {
		for _, ending := range []string{"eof", "read_error", "sse_error"} {
			t.Run(mode+"/"+ending, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				payload := compatRegressionAnthropicStart + compatRegressionAnthropicText
				if ending == "sse_error" {
					payload += "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n"
				}
				body := io.NopCloser(strings.NewReader(payload))
				if ending == "read_error" {
					body = &openAIChatStreamReadErrorCloser{payload: []byte(payload), err: io.ErrUnexpectedEOF}
				}
				resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: body}
				svc := &GatewayService{}
				var err error
				switch mode {
				case "cc_buffered":
					_, err = svc.handleCCBufferedFromAnthropic(resp, c, "claude-sonnet-4-5", "claude-sonnet-4-5", nil, time.Now())
				case "cc_stream":
					_, err = svc.handleCCStreamingFromAnthropic(resp, c, "claude-sonnet-4-5", "claude-sonnet-4-5", nil, time.Now(), true)
				case "responses_buffered":
					_, err = svc.handleResponsesBufferedStreamingResponse(resp, c, "claude-sonnet-4-5", "claude-sonnet-4-5", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
				case "responses_stream":
					_, err = svc.handleResponsesStreamingResponse(resp, c, "claude-sonnet-4-5", "claude-sonnet-4-5", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
				}
				if err == nil {
					t.Fatalf("interrupted upstream accepted as success; status=%d response=%s", rec.Code, rec.Body.String())
				}
				if strings.Contains(rec.Body.String(), `"finish_reason":"stop"`) || strings.Contains(rec.Body.String(), `"status":"completed"`) || strings.Contains(rec.Body.String(), "[DONE]") {
					t.Fatalf("interrupted stream must not synthesize success: %s", rec.Body.String())
				}
				var failover *UpstreamFailoverError
				if strings.HasSuffix(mode, "_stream") {
					if errors.As(err, &failover) {
						t.Fatalf("committed stream must not trigger another response: %v", err)
					}
					if !strings.Contains(rec.Body.String(), `"error"`) {
						t.Fatalf("client must receive a protocol error: %s", rec.Body.String())
					}
				} else if !errors.As(err, &failover) {
					t.Fatalf("uncommitted upstream failure should permit failover: %v", err)
				}
			})
		}
	}
}

func TestGatewayCompatibilityRegressionAnthropicBufferedToolArgumentsRemainValidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"cc_buffered", "responses_buffered"} {
		t.Run(mode, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			payload := compatRegressionAnthropicStart + "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"get_weather\",\"input\":{}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"city\\\":\\\"Paris\\\"}\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":15}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
			resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload))}
			svc := &GatewayService{}
			var err error
			if mode == "cc_buffered" {
				_, err = svc.handleCCBufferedFromAnthropic(resp, c, "claude-sonnet-4-5", "claude-sonnet-4-5", nil, time.Now())
			} else {
				_, err = svc.handleResponsesBufferedStreamingResponse(resp, c, "claude-sonnet-4-5", "claude-sonnet-4-5", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
			}
			if err != nil {
				t.Fatal(err)
			}
			path := "choices.0.message.tool_calls.0.function.arguments"
			if mode == "responses_buffered" {
				path = "output.0.arguments"
			}
			args := gjson.Get(rec.Body.String(), path).String()
			if !gjson.Valid(args) {
				t.Fatalf("tool arguments corrupted by initial input {}; arguments=%q response=%s", args, rec.Body.String())
			}
		})
	}
}

func TestGatewayCompatibilityRegressionRefusalSurvivesServiceResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		name := "buffered"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			payload := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_refusal\",\"model\":\"gpt-5.2\",\"status\":\"in_progress\"}}\n\ndata: {\"type\":\"response.refusal.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"I cannot assist with that request.\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_refusal\",\"model\":\"gpt-5.2\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"refusal\",\"refusal\":\"I cannot assist with that request.\"}]}],\"usage\":{\"input_tokens\":10,\"output_tokens\":8}}}\n\n"
			resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload))}
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			account := &Account{ID: 991, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
			var result *OpenAIForwardResult
			var err error
			if stream {
				result, err = svc.handleChatStreamingResponse(resp, c, account, "gpt-5.2", "gpt-5.2", "gpt-5.2", time.Now(), 1)
			} else {
				result, err = svc.handleChatBufferedStreamingResponse(resp, c, account, "gpt-5.2", "gpt-5.2", "gpt-5.2", time.Now())
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(rec.Body.String(), "I cannot assist with that request.") {
				t.Fatalf("actual HTTP response omits refusal; status=%d usage=%+v body=%s", rec.Code, result.Usage, rec.Body.String())
			}
		})
	}
}

func TestGatewayCompatibilityRegressionAnthropicStreamDrainsUsageAfterClientDisconnect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"cc_stream", "responses_stream"} {
		t.Run(mode, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			c.Writer = &openAIChatFailingWriter{ResponseWriter: c.Writer, failAfter: 1}
			payload := compatRegressionAnthropicStart + compatRegressionAnthropicText + "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":15}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
			resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload))}
			svc := &GatewayService{}
			var result *ForwardResult
			var err error
			if mode == "cc_stream" {
				result, err = svc.handleCCStreamingFromAnthropic(resp, c, "claude-sonnet-4-5", "claude-sonnet-4-5", nil, time.Now(), true)
			} else {
				result, err = svc.handleResponsesStreamingResponse(resp, c, "claude-sonnet-4-5", "claude-sonnet-4-5", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
			}
			if err != nil {
				t.Logf("returned error: %v", err)
			}
			if result == nil || result.Usage.OutputTokens != 15 {
				t.Fatalf("client disconnect lost terminal usage already available upstream; result=%+v", result)
			}
			if !result.ClientDisconnect {
				t.Fatal("disconnect must be marked in the result")
			}
		})
	}
}

func TestGatewayCompatibilityRegressionCanceledClientStillCollectsTerminalUsage(t *testing.T) {
	for _, responses := range []bool{false, true} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
		payload := compatRegressionAnthropicStart + compatRegressionAnthropicText + "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":15}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
		resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload))}
		svc := &GatewayService{}
		var result *ForwardResult
		var err error
		if responses {
			result, err = svc.handleResponsesStreamingResponse(resp, c, "claude", "claude", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
		} else {
			result, err = svc.handleCCStreamingFromAnthropic(resp, c, "claude", "claude", nil, time.Now(), true)
		}
		if err != nil || result == nil || result.Usage.OutputTokens != 15 || !result.ClientDisconnect {
			t.Fatalf("canceled client must still settle terminal usage: result=%+v err=%v", result, err)
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("canceled client must not receive output: %s", rec.Body.String())
		}
	}
}

func TestGatewayCompatibilityRegressionDisconnectDrainHasDeadlineDespitePings(t *testing.T) {
	for _, responses := range []bool{false, true} {
		name := "chat"
		if responses {
			name = "responses"
		}
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Writer = &openAIChatFailingWriter{ResponseWriter: c.Writer, failAfter: 1}
			reader, writer := io.Pipe()
			t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
			writerDone := make(chan struct{})
			go func() {
				defer close(writerDone)
				if _, err := io.WriteString(writer, compatRegressionAnthropicStart+compatRegressionAnthropicText); err != nil {
					return
				}
				ticker := time.NewTicker(25 * time.Millisecond)
				defer ticker.Stop()
				for range ticker.C {
					if _, err := io.WriteString(writer, "event: ping\ndata: {\"type\":\"ping\"}\n\n"); err != nil {
						return
					}
				}
			}()
			svc := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1}}}
			done := make(chan error, 1)
			go func() {
				resp := &http.Response{Header: http.Header{}, Body: reader}
				var err error
				if responses {
					_, err = svc.handleResponsesStreamingResponse(resp, c, "claude", "claude", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
				} else {
					_, err = svc.handleCCStreamingFromAnthropic(resp, c, "claude", "claude", nil, time.Now(), true)
				}
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("incomplete drain must report an error")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("drain did not stop despite its deadline")
			}
			select {
			case <-writerDone:
			case <-time.After(time.Second):
				t.Fatal("upstream Body.Close did not unblock the writer")
			}
		})
	}
}

func TestGatewayCompatibilityRegressionBufferedCancelInterruptsRead(t *testing.T) {
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := (&GatewayService{}).handleResponsesBufferedStreamingResponse(&http.Response{Header: http.Header{}, Body: reader}, c, "claude", "claude", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request cancellation did not interrupt blocked Read")
	}
}

func TestGatewayCompatibilityRegressionBufferedToolFragmentsAndInvalidIndices(t *testing.T) {
	for _, responses := range []bool{false, true} {
		for _, fragmented := range []bool{false, true} {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			payload := compatRegressionAnthropicStart
			payload += "event: content_block_start\ndata: " + `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"lookup","input":{}}}` + "\n\n"
			// A malformed index must never be used as a Go slice subscript.
			payload += "event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":-1,"delta":{"type":"input_json_delta","partial_json":"ignored"}}` + "\n\n"
			want := `{}`
			if fragmented {
				payload += "event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}` + "\n\n"
				payload += "event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"Paris\"}"}}` + "\n\n"
				want = `{"city":"Paris"}`
			}
			payload += "event: message_delta\ndata: " + `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":15}}` + "\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
			resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload))}
			svc := &GatewayService{}
			var err error
			path := "choices.0.message.tool_calls.0.function.arguments"
			if responses {
				_, err = svc.handleResponsesBufferedStreamingResponse(resp, c, "claude", "claude", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
				path = "output.0.arguments"
			} else {
				_, err = svc.handleCCBufferedFromAnthropic(resp, c, "claude", "claude", nil, time.Now())
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := gjson.Get(rec.Body.String(), path).String(); got != want {
				t.Fatalf("responses=%v fragmented=%v arguments=%q want=%q", responses, fragmented, got, want)
			}
		}
	}
}

type compatRegressionBlockedBody struct {
	initial     *strings.Reader
	readStarted chan struct{}
	closed      chan struct{}
	closeOnce   sync.Once
}

func (b *compatRegressionBlockedBody) Read(p []byte) (int, error) {
	if b.initial.Len() > 0 {
		return b.initial.Read(p)
	}
	close(b.readStarted)
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *compatRegressionBlockedBody) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

func TestGatewayCompatibilityRegressionCanceledClientWithoutMoreEvents(t *testing.T) {
	for _, responses := range []bool{false, true} {
		name := "cc"
		if responses {
			name = "responses"
		}
		t.Run(name, func(t *testing.T) {
			body := &compatRegressionBlockedBody{initial: strings.NewReader(compatRegressionAnthropicStart), readStarted: make(chan struct{}), closed: make(chan struct{})}
			defer func() { _ = body.Close() }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
			svc := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1}}}
			done := make(chan *ForwardResult, 1)
			go func() {
				resp := &http.Response{Header: http.Header{}, Body: body}
				var result *ForwardResult
				if responses {
					result, _ = svc.handleResponsesStreamingResponse(resp, c, "claude", "claude", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
				} else {
					result, _ = svc.handleCCStreamingFromAnthropic(resp, c, "claude", "claude", nil, time.Now(), true)
				}
				done <- result
			}()
			select {
			case <-body.readStarted:
			case <-time.After(time.Second):
				t.Fatal("reader did not reach its blocking read")
			}
			cancel()
			select {
			case result := <-done:
				if result == nil || !result.ClientDisconnect {
					t.Fatalf("canceled client must be marked even without another upstream event: %+v", result)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("cancellation drain deadline did not interrupt blocked Read")
			}
		})
	}
}
