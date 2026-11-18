package openai

import (
	"math"

	"github.com/tidwall/gjson"
)

// CacheWriteTokensDetails contains mutually exclusive OpenAI cache-write lifetimes.
type CacheWriteTokensDetails struct {
	TTL30mTokens int `json:"ttl_30m_tokens"`
	TTL12hTokens int `json:"ttl_12h_tokens"`
}

func ParseCacheWriteTokensDetails(usage gjson.Result, aggregate int) *CacheWriteTokensDetails {
	if aggregate <= 0 {
		return nil
	}
	for _, path := range []string{
		"input_tokens_details.cache_write_tokens_details",
		"prompt_tokens_details.cache_write_tokens_details",
		"cache_write_tokens_details",
	} {
		details := usage.Get(path)
		if !details.Exists() {
			continue
		}
		if !details.IsObject() {
			return nil
		}
		return &CacheWriteTokensDetails{
			TTL30mTokens: nonNegativeUsageInt(details.Get("ttl_30m_tokens")),
			TTL12hTokens: nonNegativeUsageInt(details.Get("ttl_12h_tokens")),
		}
	}
	return nil
}

func nonNegativeUsageInt(value gjson.Result) int {
	if value.Type != gjson.Number || value.Num < 0 || value.Num >= float64(math.MaxInt) || math.Trunc(value.Num) != value.Num {
		return 0
	}
	return int(value.Int())
}
