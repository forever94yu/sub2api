package service

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPT61SolCodexCatalogDefaults(t *testing.T) {
	for _, tt := range []struct {
		slug   string
		mapped string
	}{
		{slug: "gpt-6.1-sol"},
		{slug: "openai/gpt-6.1-sol"},
		{slug: "GPT-6.1-SOL"},
		{slug: "custom-sol", mapped: "openai/gpt-6.1-sol"},
	} {
		t.Run(tt.slug, func(t *testing.T) {
			var account *Account
			if tt.mapped != "" {
				account = newCodexModelsAPIKeyTestAccount("https://upstream.example/v1")
				account.Credentials["model_mapping"] = map[string]any{tt.slug: tt.mapped}
			}
			body := []byte(fmt.Sprintf(`{"models":[{"slug":%q}]}`, tt.slug))
			got, err := adjustCodexModelsManifestForAccount(body, true, account)
			require.NoError(t, err)
			entry := gjson.GetBytes(got, "models.0")
			require.Equal(t, tt.slug, entry.Get("slug").String())
			require.Equal(t, "GPT-6.1 Sol", entry.Get("display_name").String())
			require.Equal(t, int64(1050000), entry.Get("context_window").Int())
			require.Equal(t, int64(1050000), entry.Get("max_context_window").Int())
			require.Equal(t, "medium", entry.Get("default_reasoning_level").String())
			require.Equal(t, "max", entry.Get("multi_agent_reasoning_effort").String())
			require.Equal(t, []string{"low", "medium", "high", "xhigh", "max"}, gpt6CatalogEfforts(entry))
			require.JSONEq(t, `["text","image"]`, entry.Get("input_modalities").Raw)
			require.Equal(t, "list", entry.Get("visibility").String())
			require.True(t, entry.Get("supported_in_api").Bool())
			require.NotEmpty(t, entry.Get("base_instructions").String())
			stable, err := adjustCodexModelsManifestForAccount(got, true, account)
			require.NoError(t, err)
			require.Equal(t, got, stable)
		})
	}
}

func TestGPT61SolCodexCatalogPreservesProviderMetadata(t *testing.T) {
	for _, standardList := range []bool{false, true} {
		for _, fields := range []string{
			`{"display_name":"Provider Sol","context_window":500000,"max_context_window":600000,"input_modalities":["text"],"default_reasoning_level":"high","multi_agent_reasoning_effort":"xhigh","supported_reasoning_levels":[{"effort":"high","description":"Provider high","custom":true}],"supports_search_tool":false,"use_responses_lite":true,"base_instructions":"Provider prompt"}`,
			`{"context_window":null,"max_context_window":null,"input_modalities":null,"base_instructions":null,"model_messages":{"instructions_template":"Provider {{ personality }}","instructions_variables":{"personality_default":"Custom"}}}`,
			`{"base_instructions":"","model_messages":{"instructions_template":""}}`,
		} {
			var source map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(fields), &source))
			idField, modelsField := "slug", "models"
			if standardList {
				idField, modelsField = "id", "data"
			}
			source[idField] = json.RawMessage(`"gpt-6.1-sol"`)
			body, err := json.Marshal(map[string]any{modelsField: []map[string]json.RawMessage{source}})
			require.NoError(t, err)
			if standardList {
				body = convertOpenAIModelListToCodexManifest(body)
			}
			got, err := adjustCodexModelsManifest(body, true)
			require.NoError(t, err)
			entry := gjson.GetBytes(got, "models.0")
			for field, raw := range source {
				if field == "id" {
					field = "slug"
				}
				require.JSONEq(t, string(raw), entry.Get(field).Raw, "standard=%v field=%s", standardList, field)
			}
		}
	}
}

func TestGPT61SolCodexStandardListMappedModel(t *testing.T) {
	account := newCodexModelsAPIKeyTestAccount("https://upstream.example/v1")
	account.Credentials["model_mapping"] = map[string]any{"custom-sol": "gpt-6.1-sol"}
	got := convertOpenAIModelListToCodexManifestForAccount([]byte(`{"data":[{"id":"custom-sol"}]}`), account)
	entry := gjson.GetBytes(got, "models.0")
	require.Equal(t, "custom-sol", entry.Get("slug").String())
	require.Equal(t, "GPT-6.1 Sol", entry.Get("display_name").String())
	require.Equal(t, int64(1050000), entry.Get("context_window").Int())
	require.Equal(t, []string{"low", "medium", "high", "xhigh", "max"}, gpt6CatalogEfforts(entry))
}

func TestGPT61SolCodexCatalogDoesNotInventAliases(t *testing.T) {
	for _, slug := range []string{"gpt-6.1", "gpt-6.1-sol-preview", "gpt-6.1-sol-2026-09-29", "gpt-6.1-sol-max"} {
		body := []byte(fmt.Sprintf(`{"models":[{"slug":%q}]}`, slug))
		got, err := adjustCodexModelsManifest(body, true)
		require.NoError(t, err)
		require.Equal(t, body, got)
	}
}
