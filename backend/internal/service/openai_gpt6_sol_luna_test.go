package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	openaipkg "github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPT6SolLunaExactModelIdentity(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		t.Run(model, func(t *testing.T) {
			for _, input := range []string{model, strings.ToUpper(model), "openai/" + model, "gpt-image-proxy/" + model} {
				got, ok := normalizeKnownCodexModel(input)
				require.True(t, ok, input)
				require.Equal(t, model, got, input)
				require.True(t, shouldAutoInjectPromptCacheKeyForCompat(input), input)
			}
			require.Contains(t, openaipkg.DefaultModelIDs(), model)
			for _, input := range []string{
				model + "-preview", model + "-2026-09-22", model + "-high", model + "-codex",
				model + "-gpt-5.5", model + "-openai-compact", strings.ReplaceAll(model, "-", "_"),
				"20260922-" + model, "gpt-image-" + model,
			} {
				_, ok := normalizeKnownCodexModel(input)
				require.False(t, ok, input)
				require.Empty(t, normalizeKnownOpenAICodexModel(input), input)
				require.Equal(t, []string{input}, usageBillingModelCandidates(input), input)
				require.False(t, shouldAutoInjectPromptCacheKeyForCompat(input), input)
			}
		})
	}
}

func TestGPT6SolLunaReasoningEffortNormalization(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "openai/gpt-6-luna"} {
		for _, tt := range []struct{ input, want string }{
			{"none", "none"}, {"minimal", "low"}, {"low", "low"}, {"medium", "medium"},
			{"high", "high"}, {"xhigh", "xhigh"}, {"max", "max"}, {"ultra", "max"},
		} {
			t.Run(model+"/"+tt.input, func(t *testing.T) {
				require.Equal(t, tt.want, normalizeOpenAIReasoningEffortForModel(tt.input, model))
				body := []byte(fmt.Sprintf(`{"model":%q,"reasoning":{"effort":%q}}`, model, tt.input))
				got, _ := ApplyOpenAIReasoningEffortPolicy(body, "", nil)
				require.Equal(t, tt.want, gjson.GetBytes(got, "reasoning.effort").String())
				effort := extractOpenAIReasoningEffortFromBody(got, model)
				require.NotNil(t, effort)
				require.Equal(t, tt.want, *effort)
				req := &apicompat.AnthropicRequest{OutputConfig: &apicompat.AnthropicOutputConfig{Effort: tt.input}}
				require.Equal(t, tt.want, openAICompatAnthropicReasoningEffort(req, model, tt.input))
			})
		}
	}
	for _, effort := range []string{"none", "minimal"} {
		require.Empty(t, normalizeOpenAIReasoningEffortForModel(effort, "gpt-6-astra"))
		body := []byte(fmt.Sprintf(`{"model":"gpt-6-astra","reasoning":{"effort":%q}}`, effort))
		got, changed := ApplyOpenAIReasoningEffortPolicy(body, "", nil)
		require.False(t, changed)
		require.Equal(t, body, got)
	}
}

func TestGPT6SolLunaCodexCatalogFillsOnlyMissingMetadata(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		t.Run(model, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"models":[{"slug":%q}]}`, model))
			got, err := adjustCodexModelsManifest(body, false)
			require.NoError(t, err)
			entry := gjson.GetBytes(got, "models.0")
			require.Equal(t, int64(1050000), entry.Get("context_window").Int())
			require.Equal(t, int64(1050000), entry.Get("max_context_window").Int())
			require.Equal(t, "medium", entry.Get("default_reasoning_level").String())
			require.Equal(t, "max", entry.Get("multi_agent_reasoning_effort").String())
			require.Equal(t, []string{"none", "low", "medium", "high", "xhigh", "max"}, gpt6CatalogEfforts(entry))
			require.JSONEq(t, `["text","image"]`, entry.Get("input_modalities").Raw)
			require.NotEmpty(t, entry.Get("base_instructions").String())
			stable, err := adjustCodexModelsManifest(got, false)
			require.NoError(t, err)
			require.Equal(t, got, stable)

			provider := []byte(fmt.Sprintf(`{"models":[{"slug":%q,"display_name":"Provider model","context_window":500000,"max_context_window":600000,"input_modalities":["text"],"default_reasoning_level":"high","multi_agent_reasoning_effort":"xhigh","supported_reasoning_levels":[{"effort":"high","description":"Provider high","custom":true}],"use_responses_lite":true,"base_instructions":"Provider prompt"}]}`, model))
			adjusted, err := adjustCodexModelsManifest(provider, true)
			require.NoError(t, err)
			originalFields := gjson.GetBytes(provider, "models.0").Map()
			for field, value := range originalFields {
				require.JSONEq(t, value.Raw, gjson.GetBytes(adjusted, "models.0."+field).Raw, field)
			}
		})
	}
}

func TestGPT6SolLunaCodexStandardListPreservesProviderMetadata(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		body := []byte(fmt.Sprintf(`{"data":[{"id":%q,"context_window":500000,"max_context_window":600000,"input_modalities":["text"],"default_reasoning_level":"high","supported_reasoning_levels":[{"effort":"high","description":"Provider high"}],"supports_search_tool":false,"use_responses_lite":true}]}`, model))
		converted := convertOpenAIModelListToCodexManifest(body)
		got, err := adjustCodexModelsManifest(converted, true)
		require.NoError(t, err)
		entry := gjson.GetBytes(got, "models.0")
		require.Equal(t, int64(500000), entry.Get("context_window").Int())
		require.Equal(t, int64(600000), entry.Get("max_context_window").Int())
		require.Equal(t, "high", entry.Get("default_reasoning_level").String())
		require.Equal(t, []string{"high"}, gpt6CatalogEfforts(entry))
		require.True(t, entry.Get("use_responses_lite").Bool())
		require.False(t, entry.Get("supports_search_tool").Bool())
	}
}

func TestGPT6SolLunaCodexStandardListPreservesInstructionSources(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		for _, tt := range []struct {
			name         string
			fields       string
			wantBase     string
			wantFallback bool
			keepMessages bool
		}{
			{name: "provider template", fields: `{"model_messages":{"instructions_template":"Provider {{ personality }}","instructions_variables":{"personality_default":"Provider default"}}}`, keepMessages: true},
			{name: "empty template", fields: `{"model_messages":{"instructions_template":""}}`, keepMessages: true},
			{name: "empty base", fields: `{"base_instructions":""}`, wantBase: `""`},
			{name: "null messages", fields: `{"model_messages":null}`, keepMessages: true, wantFallback: true},
			{name: "null base with template", fields: `{"base_instructions":null,"model_messages":{"instructions_template":"Provider instructions"}}`, wantBase: `null`, keepMessages: true},
			{name: "invalid template", fields: `{"model_messages":{"instructions_template":123}}`, wantFallback: true},
			{name: "invalid variables", fields: `{"model_messages":{"instructions_template":"Provider instructions","instructions_variables":[]}}`, wantFallback: true},
		} {
			t.Run(model+"/"+tt.name, func(t *testing.T) {
				var source map[string]json.RawMessage
				require.NoError(t, json.Unmarshal([]byte(tt.fields), &source))
				source["id"], _ = json.Marshal(model)
				body, err := json.Marshal(map[string]any{"data": []map[string]json.RawMessage{source}})
				require.NoError(t, err)
				got := convertOpenAIModelListToCodexManifest(body)
				entry := gjson.GetBytes(got, "models.0")
				if tt.keepMessages {
					require.JSONEq(t, string(source["model_messages"]), entry.Get("model_messages").Raw)
				} else {
					require.False(t, entry.Get("model_messages").Exists())
				}
				switch {
				case tt.wantFallback:
					require.NotEmpty(t, entry.Get("base_instructions").String())
				case tt.wantBase != "":
					require.JSONEq(t, tt.wantBase, entry.Get("base_instructions").Raw)
				default:
					require.False(t, entry.Get("base_instructions").Exists())
				}
			})
		}
	}
}

func gpt6CatalogEfforts(entry gjson.Result) []string {
	var levels []struct {
		Effort string `json:"effort"`
	}
	_ = json.Unmarshal([]byte(entry.Get("supported_reasoning_levels").Raw), &levels)
	efforts := make([]string, 0, len(levels))
	for _, level := range levels {
		efforts = append(efforts, level.Effort)
	}
	return efforts
}
