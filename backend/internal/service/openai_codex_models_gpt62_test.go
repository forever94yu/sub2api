package service

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPT62CodexDiscoveryPreservesProviderCapabilities(t *testing.T) {
	for _, model := range []struct{ id, name string }{{"gpt-6.2-astra", "GPT-6.2 Astra"}, {"gpt-6.2-sol", "GPT-6.2 Sol"}} {
		for _, mapped := range []bool{false, true} {
			account := newCodexModelsAPIKeyTestAccount("https://upstream.example/v1")
			slug := model.id
			if mapped {
				slug = "custom-model"
				account.Credentials["model_mapping"] = map[string]any{slug: "openai/" + model.id}
			}
			for _, standard := range []bool{false, true} {
				for _, fields := range []string{"", `,"context_window":500000,"base_instructions":"Provider prompt","use_responses_lite":true`} {
					body := []byte(fmt.Sprintf(`{"models":[{"slug":%q%s}]}`, slug, fields))
					if standard {
						body = convertOpenAIModelListToCodexManifestForAccount([]byte(fmt.Sprintf(`{"data":[{"id":%q%s}]}`, slug, fields)), account)
					}
					got, err := adjustCodexModelsManifestForAccount(body, true, account)
					require.NoError(t, err)
					entry := gjson.GetBytes(got, "models.0")
					require.Equal(t, slug, entry.Get("slug").String())
					require.Equal(t, model.name, entry.Get("display_name").String())
					require.Equal(t, []string{"low", "medium", "high", "xhigh", "max"}, gpt6CatalogEfforts(entry))
					require.Equal(t, "medium", entry.Get("default_reasoning_level").String())
					if fields == "" {
						require.Equal(t, int64(1050000), entry.Get("context_window").Int())
					} else {
						require.Equal(t, int64(500000), entry.Get("context_window").Int())
						require.Equal(t, "Provider prompt", entry.Get("base_instructions").String())
						require.True(t, entry.Get("use_responses_lite").Bool())
					}
				}
			}
		}
	}
}
