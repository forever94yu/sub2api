//go:build unit

package service

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPT62CacheWriteLifetimesAcrossTransports(t *testing.T) {
	for _, detailsPath := range []string{"input_tokens_details", "prompt_tokens_details"} {
		for _, aggregate := range []int{0, 7000} {
			t.Run(fmt.Sprintf("%s/%d", detailsPath, aggregate), func(t *testing.T) {
				usageJSON := fmt.Sprintf(`{"input_tokens":12000,"output_tokens":50,"%s":{"cached_tokens":4000,"cache_write_tokens":%d,"cache_write_tokens_details":{"ttl_30m_tokens":3000,"ttl_12h_tokens":4000}},"cache_write_tokens":9999}`, detailsPath, aggregate)
				body := []byte(`{"type":"response.completed","response":{"usage":` + usageJSON + `}}`)
				parsed, ok := extractOpenAIUsageFromJSONBytes(body)
				require.True(t, ok)
				streamed, websocket := OpenAIUsage{}, OpenAIUsage{}
				(&OpenAIGatewayService{}).parseSSEUsageBytes(body, &streamed)
				parseOpenAIWSResponseUsageFromCompletedEvent(body, &websocket)
				for _, usage := range []OpenAIUsage{parsed, streamed, websocket} {
					require.Equal(t, 12000, usage.InputTokens)
					require.Equal(t, 4000, usage.CacheReadInputTokens)
					require.Equal(t, aggregate, usage.CacheCreationInputTokens)
					encoded, err := json.Marshal(usage)
					require.NoError(t, err)
					short, long := int64(0), int64(0)
					if aggregate > 0 {
						short, long = 3000, 4000
					}
					require.Equal(t, short, gjson.GetBytes(encoded, "cache_creation_30m_tokens").Int())
					require.Equal(t, long, gjson.GetBytes(encoded, "cache_creation_12h_tokens").Int())
				}
			})
		}
	}
}

func TestGPT62UltrafastRequestTier(t *testing.T) {
	got := extractOpenAIServiceTierFromBody([]byte(`{"model":"gpt-6.2-sol","service_tier":"ultrafast"}`))
	require.NotNil(t, got)
	require.Equal(t, "ultrafast", *got)
}
