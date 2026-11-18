package openai_ws_v2

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestParseUsageCacheReadAliasesAndCanonicalZero(t *testing.T) {
	for _, tt := range []struct {
		fields string
		want   int
	}{
		{`"cache_read_input_tokens":12`, 12},
		{`"cache_read_tokens":12`, 12},
		{`"cached_tokens":12`, 12},
		{`"input_tokens_details":{"cached_tokens":0},"cache_read_input_tokens":12`, 0},
		{`"input_tokens_details":{"cached_tokens":null},"cached_tokens":12`, 0},
		{`"prompt_tokens_details":{"cached_tokens":4},"cache_read_tokens":12`, 4},
	} {
		t.Run(tt.fields, func(t *testing.T) {
			state := &relayState{}
			body := []byte(fmt.Sprintf(`{"type":"response.completed","response":{"usage":{"input_tokens":100,"output_tokens":10,%s}}}`, tt.fields))
			got := parseUsageAndAccumulate(state, body, "response.completed", nil)
			require.Equal(t, tt.want, got.CacheReadInputTokens)
			require.Equal(t, 100, got.InputTokens)
			require.Equal(t, got, state.usage)
		})
	}
}

func TestParseUsageCacheWriteLifetimes(t *testing.T) {
	for _, path := range []string{"input_tokens_details", "prompt_tokens_details"} {
		for _, aggregate := range []int{0, 7000} {
			body := []byte(fmt.Sprintf(`{"type":"response.completed","response":{"usage":{"input_tokens":12000,"output_tokens":10,"%s":{"cache_write_tokens":%d,"cache_write_tokens_details":{"ttl_30m_tokens":3000,"ttl_12h_tokens":4000}}}}}`, path, aggregate))
			state := &relayState{}
			got := parseUsageAndAccumulate(state, body, "response.completed", nil)
			require.Equal(t, aggregate, got.CacheCreationInputTokens)
			encoded, err := json.Marshal(got)
			require.NoError(t, err)
			short, long := int64(0), int64(0)
			if aggregate > 0 {
				short, long = 3000, 4000
			}
			require.Equal(t, short, gjson.GetBytes(encoded, "CacheCreation30mTokens").Int())
			require.Equal(t, long, gjson.GetBytes(encoded, "CacheCreation12hTokens").Int())
			require.Equal(t, got, state.usage)
		}
	}
}
