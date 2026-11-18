package apicompat

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPT62CacheWriteLifetimesSurviveProtocolConversion(t *testing.T) {
	for _, detailsPath := range []string{"input_tokens_details", "prompt_tokens_details"} {
		for _, aggregate := range []int{0, 7000} {
			t.Run(fmt.Sprintf("%s/%d", detailsPath, aggregate), func(t *testing.T) {
				body := []byte(fmt.Sprintf(`{"input_tokens":12000,"output_tokens":50,"%s":{"cached_tokens":4000,"cache_write_tokens":%d,"cache_write_tokens_details":{"ttl_30m_tokens":3000,"ttl_12h_tokens":4000}},"cache_write_tokens":9999}`, detailsPath, aggregate))
				var usage ResponsesUsage
				require.NoError(t, json.Unmarshal(body, &usage))
				chat := chatUsageFromResponsesUsage(&usage)
				var nativeChat ChatUsage
				require.NoError(t, json.Unmarshal(body, &nativeChat))
				for _, tt := range []struct {
					value any
					path  string
				}{
					{usage, "input_tokens_details.cache_write_tokens_details"},
					{chat, "prompt_tokens_details.cache_write_tokens_details"},
					{nativeChat, "prompt_tokens_details.cache_write_tokens_details"},
					{ChatUsageToResponsesUsage(chat), "input_tokens_details.cache_write_tokens_details"},
					{anthropicUsageFromResponsesUsage(&usage), "cache_write_tokens_details"},
					{chatUsageToAnthropicUsage(chat), "cache_write_tokens_details"},
				} {
					encoded, err := json.Marshal(tt.value)
					require.NoError(t, err)
					short, long := int64(0), int64(0)
					if aggregate > 0 {
						short, long = 3000, 4000
					}
					require.Equal(t, short, gjson.GetBytes(encoded, tt.path+".ttl_30m_tokens").Int(), string(encoded))
					require.Equal(t, long, gjson.GetBytes(encoded, tt.path+".ttl_12h_tokens").Int(), string(encoded))
				}
			})
		}
	}
}

func TestGPT62CacheWriteLifetimesSurviveStreamingConversion(t *testing.T) {
	var response ResponsesResponse
	require.NoError(t, json.Unmarshal([]byte(`{"id":"resp_ttl","status":"completed","model":"gpt-6.2-sol","usage":{"input_tokens":8000,"output_tokens":50,"input_tokens_details":{"cache_write_tokens":7000,"cache_write_tokens_details":{"ttl_30m_tokens":3000,"ttl_12h_tokens":4000}}}}`), &response))
	state := NewResponsesEventToAnthropicState()
	events := ResponsesEventToAnthropicEvents(&ResponsesStreamEvent{Type: "response.created", Response: &ResponsesResponse{ID: "resp_ttl", Model: "gpt-6.2-sol"}}, state)
	events = append(events, ResponsesEventToAnthropicEvents(&ResponsesStreamEvent{Type: "response.completed", Response: &response}, state)...)
	chatState := NewChatCompletionsToAnthropicStreamState("gpt-6.2-sol")
	chatEvents := ChatCompletionsChunkToAnthropicEvents(&ChatCompletionsChunk{ID: "resp_ttl", Usage: chatUsageFromResponsesUsage(response.Usage)}, chatState)
	chatEvents = append(chatEvents, FinalizeChatCompletionsAnthropicStream(chatState)...)
	for _, stream := range [][]AnthropicStreamEvent{events, chatEvents} {
		reverse := NewAnthropicEventToResponsesState()
		found, foundReverse := false, false
		for _, event := range stream {
			if event.Type == "message_delta" && event.Usage != nil {
				found = true
				require.Equal(t, 1000, event.Usage.InputTokens)
				require.Equal(t, 7000, event.Usage.CacheCreationInputTokens)
				require.Equal(t, &CacheWriteTokensDetails{TTL30mTokens: 3000, TTL12hTokens: 4000}, event.Usage.CacheWriteTokensDetails)
				responseAgain := AnthropicToResponsesResponse(&AnthropicResponse{ID: "resp_ttl", Usage: *event.Usage})
				require.Equal(t, response.Usage.InputTokensDetails.CacheWriteTokensDetails, responseAgain.Usage.InputTokensDetails.CacheWriteTokensDetails)
			}
			for _, converted := range AnthropicEventToResponsesEvents(&event, reverse) {
				if converted.Type == "response.completed" {
					foundReverse = true
					require.Equal(t, response.Usage.InputTokensDetails.CacheWriteTokensDetails, converted.Response.Usage.InputTokensDetails.CacheWriteTokensDetails)
				}
			}
		}
		require.True(t, found)
		require.True(t, foundReverse)
	}
}
