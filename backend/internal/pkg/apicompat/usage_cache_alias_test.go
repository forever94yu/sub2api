package apicompat

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsageCacheAliasesSurviveConversions(t *testing.T) {
	for _, tt := range []struct {
		name, fields string
		read, write  int
	}{
		{"input aliases", `"cache_read_input_tokens":100,"cache_creation_input_tokens":200`, 100, 200},
		{"write input alias", `"cache_read_tokens":100,"cache_write_input_tokens":200`, 100, 200},
		{"short aliases", `"cached_tokens":100,"cache_write_tokens":200`, 100, 200},
		{"creation alias", `"cache_read_input_tokens":100,"cache_creation_tokens":200`, 100, 200},
		{"details override roots", `"cache_read_input_tokens":100,"cache_write_tokens":200,"input_tokens_details":{"cached_tokens":7,"cache_write_tokens":9},"prompt_tokens_details":{"cached_tokens":7,"cache_write_tokens":9}`, 7, 9},
		{"explicit zero overrides roots", `"cache_read_input_tokens":100,"cache_write_tokens":200,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":0}`, 0, 0},
		{"write zero overrides creation alias", `"input_tokens_details":{"cached_tokens":7,"cache_write_tokens":0,"cache_creation_tokens":19},"prompt_tokens_details":{"cached_tokens":7,"cache_write_tokens":0,"cache_creation_tokens":19}`, 7, 0},
		{"mixed detail precedence", `"input_tokens_details":{"cached_tokens":7,"cache_creation_tokens":19},"prompt_tokens_details":{"cached_tokens":8,"cache_write_tokens":9}`, 7, 9},
		{"input detail zero overrides prompt details", `"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"prompt_tokens_details":{"cached_tokens":8,"cache_write_tokens":9}`, 0, 0},
		{"roots fill absent detail fields", `"cache_read_input_tokens":100,"cache_write_tokens":200,"input_tokens_details":{"audio_tokens":3},"prompt_tokens_details":{"audio_tokens":3}`, 100, 200},
		{"root write alias precedence", `"cache_write_tokens":9,"cache_creation_input_tokens":19,"cache_write_input_tokens":29,"cache_creation_tokens":39`, 0, 9},
		{"root creation input alias precedence", `"cache_write_tokens":0,"cache_creation_input_tokens":19,"cache_write_input_tokens":29,"cache_creation_tokens":39`, 0, 19},
		{"input null overrides roots and prompt details", `"cache_read_input_tokens":100,"cache_write_tokens":200,"input_tokens_details":{"cached_tokens":null,"cache_write_tokens":null},"prompt_tokens_details":{"cached_tokens":7,"cache_write_tokens":9}`, 0, 0},
		{"prompt null overrides roots", `"cache_read_input_tokens":100,"cache_write_tokens":200,"prompt_tokens_details":{"cached_tokens":null,"cache_write_tokens":null}`, 0, 0},
		{"input creation null overrides roots", `"cache_read_input_tokens":100,"cache_write_tokens":200,"input_tokens_details":{"cache_creation_tokens":null}`, 100, 0},
		{"prompt creation null overrides roots", `"cache_read_input_tokens":100,"cache_write_tokens":200,"prompt_tokens_details":{"cache_creation_tokens":null}`, 100, 0},
	} {
		for _, chat := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/chat=%t", tt.name, chat), func(t *testing.T) {
				var usage *ResponsesUsage
				if chat {
					var native ChatUsage
					body := []byte(`{"prompt_tokens":1000,"completion_tokens":50,"total_tokens":1050,"completion_tokens_details":{"reasoning_tokens":17},` + tt.fields + `}`)
					require.NoError(t, json.Unmarshal(body, &native))
					require.Equal(t, 17, native.CompletionTokensDetails.ReasoningTokens)
					assertCacheAliasAnthropicUsage(t, chatUsageToAnthropicUsage(&native), tt.read, tt.write)
					usage = ChatUsageToResponsesUsage(&native)
				} else {
					body := []byte(`{"input_tokens":1000,"output_tokens":50,"total_tokens":1050,"output_tokens_details":{"reasoning_tokens":17},` + tt.fields + `}`)
					require.NoError(t, json.Unmarshal(body, &usage))
					require.Equal(t, 17, chatUsageFromResponsesUsage(usage).CompletionTokensDetails.ReasoningTokens)
				}
				require.Equal(t, 1000, usage.InputTokens)
				require.Equal(t, 50, usage.OutputTokens, "reasoning is already included in output")
				require.Equal(t, 1050, usage.TotalTokens)
				assertCacheAliasAnthropicUsage(t, anthropicUsageFromResponsesUsage(usage), tt.read, tt.write)
				convertedChat := chatUsageFromResponsesUsage(usage)
				assertCacheAliasAnthropicUsage(t, chatUsageToAnthropicUsage(convertedChat), tt.read, tt.write)
				assertCacheAliasAnthropicUsage(t, anthropicUsageFromResponsesUsage(ChatUsageToResponsesUsage(convertedChat)), tt.read, tt.write)
			})
		}
	}
}

func assertCacheAliasAnthropicUsage(t *testing.T, usage AnthropicUsage, read, write int) {
	t.Helper()
	require.Equal(t, read, usage.CacheReadInputTokens)
	require.Equal(t, write, usage.CacheCreationInputTokens)
	require.Equal(t, 1000-read-write, usage.InputTokens)
	require.Equal(t, 50, usage.OutputTokens)
}

func TestChatUsageCacheAliasesPreserveNonCacheDetails(t *testing.T) {
	var usage ChatUsage
	require.NoError(t, json.Unmarshal([]byte(`{
		"prompt_tokens":1000,
		"completion_tokens":50,
		"cache_read_input_tokens":100,
		"cache_creation_input_tokens":200,
		"prompt_tokens_details":{"audio_tokens":3},
		"completion_tokens_details":{
			"reasoning_tokens":17,
			"audio_tokens":4,
			"accepted_prediction_tokens":5,
			"rejected_prediction_tokens":6
		}
	}`), &usage))
	require.Equal(t, 1000, usage.PromptTokens)
	require.Equal(t, 50, usage.CompletionTokens)
	require.Zero(t, usage.TotalTokens, "cache aliases must not change total-token parsing")
	require.Equal(t, &ChatTokenDetails{CachedTokens: 100, CacheCreationTokens: 200, AudioTokens: 3}, usage.PromptTokensDetails)
	require.Equal(t, &ChatTokenDetails{
		ReasoningTokens:          17,
		AudioTokens:              4,
		AcceptedPredictionTokens: 5,
		RejectedPredictionTokens: 6,
	}, usage.CompletionTokensDetails)
}
