package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGPT6SolLunaSamplingFollowsReasoningEffort(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "openai/GPT-6-LUNA"} {
		for _, effort := range []string{"none", "", "medium", "max"} {
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
					if effort == "none" {
						require.NotNil(t, req.Temperature)
						require.NotNil(t, req.TopP)
					} else {
						require.Nil(t, req.Temperature)
						require.Nil(t, req.TopP)
					}
				}
			})
		}
	}
}
