package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHaiku55ResponsesThinkingModes(t *testing.T) {
	for _, tt := range []struct {
		effort, thinking, outputEffort string
	}{
		{"", "adaptive", "medium"},
		{"none", "disabled", "medium"},
		{"low", "adaptive", "low"},
		{"medium", "adaptive", "medium"},
		{"high", "adaptive", "high"},
		{"xhigh", "adaptive", "xhigh"},
		{"max", "adaptive", "max"},
	} {
		t.Run(tt.effort, func(t *testing.T) {
			var request ResponsesRequest
			require.NoError(t, json.Unmarshal([]byte(`{"model":"claude-haiku-5-5","input":"hello","reasoning":{"effort":"`+tt.effort+`"}}`), &request))
			out, err := ResponsesToAnthropicRequest(&request)
			require.NoError(t, err)
			require.NotNil(t, out.Thinking)
			require.Equal(t, tt.thinking, out.Thinking.Type)
			require.Zero(t, out.Thinking.BudgetTokens)
			require.NotNil(t, out.OutputConfig)
			require.Equal(t, tt.outputEffort, out.OutputConfig.Effort)
		})
	}
	var request ResponsesRequest
	require.NoError(t, json.Unmarshal([]byte(`{"model":"claude-haiku-5-5","input":"hello","reasoning":{"effort":"minimal"}}`), &request))
	_, err := ResponsesToAnthropicRequest(&request)
	require.ErrorContains(t, err, "reasoning effort")
}

func TestHaiku55ResponsesAndChatAllowForcedTools(t *testing.T) {
	for _, tt := range []struct{ responses, chat, want string }{
		{`"required"`, `"required"`, `{"type":"any"}`},
		{`{"type":"function","name":"lookup"}`, `{"type":"function","function":{"name":"lookup"}}`, `{"type":"tool","name":"lookup"}`},
	} {
		var request ResponsesRequest
		require.NoError(t, json.Unmarshal([]byte(`{"model":"claude-haiku-5-5","input":"hello","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],"tool_choice":`+tt.responses+`}`), &request))
		out, err := ResponsesToAnthropicRequest(&request)
		require.NoError(t, err)
		require.JSONEq(t, tt.want, string(out.ToolChoice))
		require.NotNil(t, out.Thinking)
		require.Equal(t, "adaptive", out.Thinking.Type)

		var chatRequest ChatCompletionsRequest
		require.NoError(t, json.Unmarshal([]byte(`{"model":"claude-haiku-5-5","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"tool_choice":`+tt.chat+`,"reasoning_effort":"none"}`), &chatRequest))
		responses, err := ChatCompletionsToResponses(&chatRequest)
		require.NoError(t, err)
		out, err = ResponsesToAnthropicRequest(responses)
		require.NoError(t, err)
		require.JSONEq(t, tt.want, string(out.ToolChoice))
		require.NotNil(t, out.Thinking)
		require.Equal(t, "disabled", out.Thinking.Type)
	}
}

func TestHaiku55ResponsesRejectSamplingAndAssistantPrefill(t *testing.T) {
	for _, fields := range []string{
		`"temperature":0.5`, `"top_p":1`, `"temperature":1,"top_p":0.99`,
		`"input":[{"role":"assistant","content":"prefill"}]`,
	} {
		var body map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(`{`+fields+`}`), &body))
		body["model"] = json.RawMessage(`"claude-haiku-5-5"`)
		if _, exists := body["input"]; !exists {
			body["input"] = json.RawMessage(`"hello"`)
		}
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		var request ResponsesRequest
		require.NoError(t, json.Unmarshal(raw, &request))
		_, err = ResponsesToAnthropicRequest(&request)
		require.Error(t, err, fields)
	}
	for _, field := range []string{`"temperature":1`, `"top_p":0.99`} {
		var request ResponsesRequest
		require.NoError(t, json.Unmarshal([]byte(`{"model":"claude-haiku-5-5","input":"hello",`+field+`}`), &request))
		_, err := ResponsesToAnthropicRequest(&request)
		require.NoError(t, err, field)
	}
}

func TestHaiku55SignedThinkingRoundTrip(t *testing.T) {
	response := &AnthropicResponse{ID: "msg_haiku55", Model: "claude-haiku-5-5", Content: []AnthropicContentBlock{
		{Type: "thinking", Signature: "signed-progress"},
		{Type: "redacted_thinking", Data: "opaque-progress"},
		{Type: "text", Text: "answer"},
	}}
	converted := AnthropicToResponsesResponse(response)
	require.Len(t, converted.Output, 3)
	require.NotEmpty(t, converted.Output[0].EncryptedContent)
	require.NotEmpty(t, converted.Output[1].EncryptedContent)
	inputItems := append(converted.Output, ResponsesOutput{Type: "message", Role: "user", Content: []ResponsesContentPart{{Type: "input_text", Text: "continue"}}})
	input, err := json.Marshal(inputItems)
	require.NoError(t, err)
	out, err := ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-haiku-5-5", Input: input})
	require.NoError(t, err)
	var blocks []AnthropicContentBlock
	require.NoError(t, json.Unmarshal(out.Messages[0].Content, &blocks))
	require.Equal(t, response.Content, blocks)
	var wireBlocks []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out.Messages[0].Content, &wireBlocks))
	require.JSONEq(t, `""`, string(wireBlocks[0]["thinking"]))
	for _, model := range []string{"claude-haiku-4-5", "claude-haiku-5-50", "claude-haiku-5-5-preview"} {
		response.Model = model
		for _, item := range AnthropicToResponsesResponse(response).Output {
			require.Empty(t, item.EncryptedContent, model)
		}
	}
}

func TestHaiku55StreamingRetainsSignatureOnlyThinking(t *testing.T) {
	state := NewAnthropicEventToResponsesState()
	index := 0
	for _, event := range []*AnthropicStreamEvent{
		{Type: "message_start", Message: &AnthropicResponse{ID: "msg_haiku55", Model: "claude-haiku-5-5"}},
		{Type: "content_block_start", Index: &index, ContentBlock: &AnthropicContentBlock{Type: "thinking"}},
		{Type: "content_block_delta", Index: &index, Delta: &AnthropicDelta{Type: "signature_delta", Signature: "signed-progress"}},
		{Type: "content_block_stop", Index: &index},
	} {
		AnthropicEventToResponsesEvents(event, state)
	}
	events := AnthropicEventToResponsesEvents(&AnthropicStreamEvent{Type: "message_stop"}, state)
	require.NotEmpty(t, events)
	last := events[len(events)-1]
	require.NotNil(t, last.Response)
	require.Len(t, last.Response.Output, 1)
	require.Contains(t, last.Response.Output[0].EncryptedContent, "anthropic-thinking-v1:")
}
