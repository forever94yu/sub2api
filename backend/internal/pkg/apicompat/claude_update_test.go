package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClaudeUpdateOpus55ThinkingRoundTrip(t *testing.T) {
	response := &AnthropicResponse{ID: "msg_test", Model: "claude-opus-5-5", Content: []AnthropicContentBlock{
		{Type: "thinking", Thinking: "reason", Signature: "signed-reason"},
		{Type: "text", Text: "before"},
		{Type: "redacted_thinking", Data: "opaque"},
		{Type: "text", Text: "after"},
	}}
	converted := AnthropicToResponsesResponse(response)
	require.Len(t, converted.Output, 4)
	require.NotEmpty(t, converted.Output[0].EncryptedContent)
	require.NotEmpty(t, converted.Output[2].EncryptedContent)
	input, err := json.Marshal(converted.Output)
	require.NoError(t, err)
	roundTrip, err := ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-opus-5-5", Input: input})
	require.NoError(t, err)
	require.NotNil(t, roundTrip.Thinking)
	require.Equal(t, "adaptive", roundTrip.Thinking.Type)
	require.Equal(t, "medium", roundTrip.OutputConfig.Effort)
	var blocks []AnthropicContentBlock
	for _, message := range roundTrip.Messages {
		var content []AnthropicContentBlock
		require.NoError(t, json.Unmarshal(message.Content, &content))
		blocks = append(blocks, content...)
	}
	require.Len(t, blocks, 4)
	require.Equal(t, "signed-reason", blocks[0].Signature)
	require.Equal(t, "before", blocks[1].Text)
	require.Equal(t, "opaque", blocks[2].Data)
	require.Equal(t, "after", blocks[3].Text)
}

func TestClaudeUpdateOpus55ForcedToolsRejected(t *testing.T) {
	for _, choice := range []string{`"required"`, `{"type":"function","name":"lookup"}`} {
		_, err := ResponsesToAnthropicRequest(&ResponsesRequest{Model: "claude-opus-5-5", Input: json.RawMessage(`"hello"`), ToolChoice: json.RawMessage(choice)})
		require.Error(t, err)
	}
}

func TestClaudeUpdateStreamPreservesInterleavedTextAndThinking(t *testing.T) {
	state := NewAnthropicEventToResponsesState()
	var events []ResponsesStreamEvent
	feed := func(event *AnthropicStreamEvent) {
		events = append(events, AnthropicEventToResponsesEvents(event, state)...)
	}
	feed(&AnthropicStreamEvent{Type: "message_start", Message: &AnthropicResponse{ID: "msg", Model: "claude-opus-5-5"}})
	for index, kind := range []string{"text", "text", "thinking", "text"} {
		feed(&AnthropicStreamEvent{Type: "content_block_start", Index: &index, ContentBlock: &AnthropicContentBlock{Type: kind}})
		if kind == "thinking" {
			feed(&AnthropicStreamEvent{Type: "content_block_delta", Index: &index, Delta: &AnthropicDelta{Type: "thinking_delta", Thinking: "reason"}})
			feed(&AnthropicStreamEvent{Type: "content_block_delta", Index: &index, Delta: &AnthropicDelta{Type: "signature_delta", Signature: "signature"}})
		} else {
			feed(&AnthropicStreamEvent{Type: "content_block_delta", Index: &index, Delta: &AnthropicDelta{Type: "text_delta", Text: "text"}})
		}
		feed(&AnthropicStreamEvent{Type: "content_block_stop", Index: &index})
	}
	feed(&AnthropicStreamEvent{Type: "message_stop"})
	var partIndexes []int
	for _, event := range events {
		if event.Type == "response.content_part.added" {
			partIndexes = append(partIndexes, event.ContentIndex)
		}
	}
	require.Equal(t, []int{0, 1, 0}, partIndexes)
	require.Len(t, state.Outputs, 3)
	require.Len(t, state.Outputs[0].Content, 2)
	require.Equal(t, "text", state.Outputs[0].Content[0].Text)
	require.NotEmpty(t, state.Outputs[1].EncryptedContent)
	require.Equal(t, "text", state.Outputs[2].Content[0].Text)
}
