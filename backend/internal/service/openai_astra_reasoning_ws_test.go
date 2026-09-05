package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type astraReasoningWSConn struct{ *stagedPassthroughConn }

func (c *astraReasoningWSConn) WriteJSON(ctx context.Context, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.WriteFrame(ctx, coderws.MessageText, payload)
}

func TestAstraWebSocketReasoningIsNativeAndTurnLocal(t *testing.T) {
	for _, mode := range []string{OpenAIWSIngressModePassthrough, OpenAIWSIngressModeCtxPool} {
		for _, model := range []string{"gpt-6-astra", "custom-astra"} {
			for _, capped := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/capped=%t", mode, model, capped), func(t *testing.T) {
					gin.SetMode(gin.TestMode)
					ctx, cancel := context.WithCancelCause(context.Background())
					defer cancel(context.Canceled)
					upstream := newStagedPassthroughConn()
					results := make(chan *OpenAIForwardResult, 8)
					hooks := &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, err error) {
						if err == nil && result != nil {
							results <- result
						}
					}}
					if capped {
						hooks.MaxReasoningEffort = "high"
						hooks.ReasoningEffortMappings = []ReasoningEffortMapping{{From: "max", To: "medium"}, {From: "medium", To: "low"}}
					}
					account := passthroughLifecycleAccount()
					account.Credentials["model_mapping"] = map[string]any{"custom-astra": "gpt-6-astra"}
					account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
					cfg := passthroughLifecycleConfig()
					svc := newPassthroughLifecycleService(cfg, upstream)
					if mode == OpenAIWSIngressModeCtxPool {
						cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
						cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
						pool := newOpenAIWSConnPool(cfg)
						pool.setClientDialerForTest(&stagedPassthroughDialer{conn: &astraReasoningWSConn{upstream}})
						svc.openaiWSPool = pool
						defer pool.Close()
					}
					server, serverErr := startPassthroughHookRecordingServer(t, ctx,
						svc, account, hooks)
					defer server.Close()
					dialCtx, cancelDial := context.WithTimeout(ctx, 3*time.Second)
					client, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
					cancelDial()
					require.NoError(t, err)
					defer func() { _ = client.CloseNow() }()

					for turn, effort := range []string{"ultra", "xhigh", "ultra", "low", "medium", "high", "max"} {
						// Follow-up turns omit model to exercise the session's mapped model.
						modelField := ""
						if turn == 0 {
							modelField = fmt.Sprintf(`,"model":%q`, model)
						}
						payload := fmt.Sprintf(`{"type":"response.create"%s,"reasoning":{"effort":%q}}`, modelField, effort)
						writeCtx, cancelWrite := context.WithTimeout(ctx, 3*time.Second)
						err := client.Write(writeCtx, coderws.MessageText, []byte(payload))
						cancelWrite()
						require.NoError(t, err)
						forwarded := requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
						want := effort
						if effort == "ultra" {
							want = "max"
						}
						if capped {
							switch want {
							case "max":
								want = "medium"
							case "medium":
								want = "low"
							case "xhigh":
								want = "high"
							}
						}
						require.Equal(t, want, gjson.GetBytes(forwarded, "reasoning.effort").String(), "turn %d upstream", turn)
						upstream.Send(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_astra_%d","usage":{"input_tokens":1,"output_tokens":1}}}`, turn))
						_, err = readPassthroughLifecycleFrame(t, client, 3*time.Second)
						require.NoError(t, err)
						select {
						case result := <-results:
							require.NotNil(t, result.ReasoningEffort)
							require.Equal(t, want, *result.ReasoningEffort, "turn %d usage", turn)
						case <-time.After(3 * time.Second):
							t.Fatal("missing turn usage")
						}
					}
					require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
					select {
					case <-serverErr:
					case <-time.After(3 * time.Second):
						t.Fatal("websocket session did not exit")
					}
				})
			}
		}
	}
}
