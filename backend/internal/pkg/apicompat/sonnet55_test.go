package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSonnet55ResponsesThinkingModes(t *testing.T) {
	for _, tt := range []struct {
		effort, thinking, outputEffort string
	}{
		{"", "adaptive", "high"},
		{"none", "between_tools", "high"},
		{"low", "adaptive", "low"},
		{"medium", "adaptive", "medium"},
		{"high", "adaptive", "high"},
		{"xhigh", "adaptive", "xhigh"},
		{"max", "adaptive", "max"},
	} {
		t.Run(tt.effort, func(t *testing.T) {
			var request ResponsesRequest
			require.NoError(t, json.Unmarshal([]byte(`{"model":"claude-sonnet-5-5","input":"hello","reasoning":{"effort":"`+tt.effort+`"}}`), &request))
			out, err := ResponsesToAnthropicRequest(&request)
			require.NoError(t, err)
			require.NotNil(t, out.Thinking)
			require.Equal(t, tt.thinking, out.Thinking.Type)
			require.Zero(t, out.Thinking.BudgetTokens)
			require.NotNil(t, out.OutputConfig)
			require.Equal(t, tt.outputEffort, out.OutputConfig.Effort)
		})
	}
}

func TestSonnet55ResponsesRejectForcedToolsAndUnsupportedEffort(t *testing.T) {
	for _, fields := range []string{
		`"tool_choice":"required"`,
		`"tool_choice":{"type":"function","name":"lookup"}`,
		`"reasoning":{"effort":"minimal"}`,
	} {
		var request ResponsesRequest
		require.NoError(t, json.Unmarshal([]byte(`{"model":"claude-sonnet-5-5","input":"hello",`+fields+`}`), &request))
		_, err := ResponsesToAnthropicRequest(&request)
		require.Error(t, err, fields)
	}
}

func TestSonnet55SignedThinkingRoundTrip(t *testing.T) {
	response := &AnthropicResponse{ID: "msg_sonnet55", Model: "claude-sonnet-5-5", Content: []AnthropicContentBlock{
		{Type: "thinking", Signature: "signed-progress"},
		{Type: "redacted_thinking", Data: "opaque-progress"},
		{Type: "text", Text: "answer"},
	}}
	converted := AnthropicToResponsesResponse(response)
	require.Len(t, converted.Output, 3)
	require.NotEmpty(t, converted.Output[0].EncryptedContent)
	require.NotEmpty(t, converted.Output[1].EncryptedContent)
	input, err := json.Marshal(converted.Output)
	require.NoError(t, err)
	var request ResponsesRequest
	require.NoError(t, json.Unmarshal([]byte(`{"model":"claude-sonnet-5-5","reasoning":{"effort":"none"}}`), &request))
	request.Input = input
	out, err := ResponsesToAnthropicRequest(&request)
	require.NoError(t, err)
	var blocks []AnthropicContentBlock
	require.NoError(t, json.Unmarshal(out.Messages[0].Content, &blocks))
	require.Len(t, blocks, 3)
	require.Equal(t, response.Content, blocks)
	for _, model := range []string{"claude-sonnet-5", "claude-sonnet-5-50", "gpt-6"} {
		response.Model = model
		legacy := AnthropicToResponsesResponse(response)
		for _, item := range legacy.Output {
			require.Empty(t, item.EncryptedContent, model)
		}
	}
}

func TestSonnet55StreamingRetainsEmptySignedThinking(t *testing.T) {
	state := NewAnthropicEventToResponsesState()
	index := 0
	for _, event := range []*AnthropicStreamEvent{
		{Type: "message_start", Message: &AnthropicResponse{ID: "msg_sonnet55", Model: "claude-sonnet-5-5"}},
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
