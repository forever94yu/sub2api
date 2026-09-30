package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGPT61SolConversionRemovesSampling(t *testing.T) {
	for _, model := range []string{"gpt-6.1-sol", "openai/GPT-6.1-SOL"} {
		for _, effort := range []string{"none", "minimal", "", "low", "medium", "max"} {
			t.Run(model+"/"+effort, func(t *testing.T) {
				temperature, topP := 0.7, 0.9
				chat, err := ChatCompletionsToResponses(&ChatCompletionsRequest{
					Model: model, Messages: []ChatMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}},
					ReasoningEffort: effort, Temperature: &temperature, TopP: &topP,
				})
				require.NoError(t, err)
				messages, err := AnthropicToResponses(&AnthropicRequest{
					Model: model, Messages: []AnthropicMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}},
					OutputConfig: &AnthropicOutputConfig{Effort: effort}, Temperature: &temperature, TopP: &topP,
				})
				require.NoError(t, err)
				for _, req := range []*ResponsesRequest{chat, messages} {
					require.Nil(t, req.Temperature)
					require.Nil(t, req.TopP)
				}
				directChat, err := AnthropicToChatCompletionsRequest(&AnthropicRequest{
					Model: model, Messages: []AnthropicMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}},
					OutputConfig: &AnthropicOutputConfig{Effort: effort}, Temperature: &temperature, TopP: &topP,
				})
				require.NoError(t, err)
				require.Nil(t, directChat.Temperature)
				require.Nil(t, directChat.TopP)
			})
		}
	}
}
