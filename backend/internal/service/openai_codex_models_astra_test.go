package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const astraUpstreamManifest = `{
	"models":[{
		"slug":"gpt-6-astra","display_name":"6 Astra","description":"Upstream model description",
		"base_instructions":"Provider model instructions",
		"default_reasoning_level":"xhigh","multi_agent_reasoning_effort":"xhigh",
		"supported_reasoning_levels":[
			{"effort":"low","description":"Quick reasoning"},
			{"effort":"medium","description":"Balanced reasoning"},
			{"effort":"high","description":"Deep reasoning"},
			{"effort":"xhigh","description":"Native extra high","custom_level":{"enabled":true}},
			{"effort":"ultra","description":"Ultra","orchestration":{"version":2}}
		],
		"shell_type":"unified_exec","visibility":"list","supported_in_api":true,"priority":0,
		"support_verbosity":true,"truncation_policy":{"mode":"tokens","limit":10000},
		"experimental_supported_tools":[],"context_window":1000000,"use_responses_lite":true,
		"unknown_model":{"enabled":true}
	},{"slug":"gpt-6-astra-preview","display_name":"Preview","supported_reasoning_levels":[{"effort":"ultra","description":"Preview Ultra"}]},
	{"slug":"gpt-5.5","display_name":"GPT-5.5","supported_reasoning_levels":[{"effort":"xhigh","description":"Existing level"}]}],
	"unknown_top":{"revision":9007199254740993}
}`

func newAstraCodexManifestTestService(t *testing.T, apiKey bool, body string, etag string) (*OpenAIGatewayService, *Account) {
	t.Helper()
	if apiKey {
		upstream := &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
			if req.Header.Get("If-None-Match") == etag && etag != "" {
				return &http.Response{StatusCode: http.StatusNotModified, Header: http.Header{"Etag": []string{etag}}, Body: http.NoBody}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Etag": []string{etag}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		}}
		return newCodexModelsAPIKeyTestService(upstream), newCodexModelsAPIKeyTestAccount("https://astra.example/v1")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag && etag != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	original := chatgptCodexModelsURL
	chatgptCodexModelsURL = server.URL
	t.Cleanup(func() { chatgptCodexModelsURL = original })
	return &OpenAIGatewayService{}, newCodexModelsTestAccount()
}

func requireAstraCodexCapabilities(t *testing.T, body []byte, ultra bool) map[string]json.RawMessage {
	t.Helper()
	var envelope struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope))
	require.NotEmpty(t, envelope.Models)
	astra := envelope.Models[0]
	require.JSONEq(t, `"gpt-6-astra"`, string(astra["slug"]))
	require.JSONEq(t, `"GPT-6-Astra"`, string(astra["display_name"]))
	require.JSONEq(t, `"max"`, string(astra["multi_agent_reasoning_effort"]))
	var levels []struct {
		Effort      string `json:"effort"`
		Description string `json:"description"`
	}
	require.NoError(t, json.Unmarshal(astra["supported_reasoning_levels"], &levels))
	var efforts []string
	for _, level := range levels {
		efforts = append(efforts, level.Effort)
		require.NotEmpty(t, level.Description)
	}
	want := []string{"low", "medium", "high", "xhigh", "max"}
	if ultra {
		want = append(want, "ultra")
	}
	require.Equal(t, want, efforts)
	return astra
}

func TestFetchCodexModelsManifestAstraCapabilities(t *testing.T) {
	for _, apiKey := range []bool{false, true} {
		name := "OAuth"
		if apiKey {
			name = "APIKey"
		}
		t.Run(name, func(t *testing.T) {
			s, account := newAstraCodexManifestTestService(t, apiKey, astraUpstreamManifest, `"astra-upstream"`)
			manifest, err := s.FetchCodexModelsManifest(context.Background(), account, "0.145.0", "")
			require.NoError(t, err)
			require.False(t, manifest.NotModified)
			astra := requireAstraCodexCapabilities(t, manifest.Body, true)
			require.JSONEq(t, `"xhigh"`, string(astra["default_reasoning_level"]))
			var original, adjusted struct {
				Models     []map[string]json.RawMessage `json:"models"`
				UnknownTop json.RawMessage              `json:"unknown_top"`
			}
			require.NoError(t, json.Unmarshal([]byte(astraUpstreamManifest), &original))
			require.NoError(t, json.Unmarshal(manifest.Body, &adjusted))
			for field, value := range original.Models[0] {
				if field != "display_name" && field != "supported_reasoning_levels" && field != "multi_agent_reasoning_effort" &&
					(!apiKey || field != "use_responses_lite") {
					require.JSONEq(t, string(value), string(astra[field]), "preserve Astra field %s", field)
				}
			}
			require.JSONEq(t, `1000000`, string(astra["context_window"]), "preserve explicit context window")
			require.JSONEq(t, `1050000`, string(astra["max_context_window"]), "fill missing max context window")
			require.JSONEq(t, `["text","image"]`, string(astra["input_modalities"]), "fill missing input modalities")
			if apiKey {
				require.JSONEq(t, `false`, string(astra["use_responses_lite"]))
			} else {
				require.JSONEq(t, `true`, string(astra["use_responses_lite"]), "OAuth must preserve Responses Lite")
			}
			for i := 1; i < len(original.Models); i++ {
				before, err := json.Marshal(original.Models[i])
				require.NoError(t, err)
				after, err := json.Marshal(adjusted.Models[i])
				require.NoError(t, err)
				require.JSONEq(t, string(before), string(after))
			}
			require.Equal(t, string(original.UnknownTop), string(adjusted.UnknownTop))
			var levels []json.RawMessage
			require.NoError(t, json.Unmarshal(astra["supported_reasoning_levels"], &levels))
			require.JSONEq(t, `{"effort":"xhigh","description":"Native extra high","custom_level":{"enabled":true}}`, string(levels[3]))
			require.JSONEq(t, `{"effort":"ultra","description":"Ultra","orchestration":{"version":2}}`, string(levels[5]))
			require.NotEqual(t, `"astra-upstream"`, manifest.ETag)
		})
	}
}

func TestFetchCodexModelsManifestAstraStandardListCapabilities(t *testing.T) {
	const body = `{"object":"list","data":[{
		"id":"gpt-6-astra","object":"model","supports_search_tool":false,
		"apply_patch_tool_type":null,"comp_hash":"provider-hash","tool_mode":"provider-mode",
		"use_responses_lite":true
	},{"id":"gpt-5.5","object":"model","supports_search_tool":true,
		"context_window":250000,"max_context_window":250000,"input_modalities":["text"]}]}`
	s, account := newAstraCodexManifestTestService(t, true, body, `"astra-list"`)
	manifest, err := s.FetchCodexModelsManifest(context.Background(), account, "0.145.0", "")
	require.NoError(t, err)
	astra := requireAstraCodexCapabilities(t, manifest.Body, false)
	require.JSONEq(t, `1050000`, string(astra["context_window"]))
	require.JSONEq(t, `1050000`, string(astra["max_context_window"]))
	require.JSONEq(t, `["text","image"]`, string(astra["input_modalities"]))
	require.JSONEq(t, `false`, string(astra["supports_search_tool"]))
	require.JSONEq(t, `null`, string(astra["apply_patch_tool_type"]))
	require.JSONEq(t, `"provider-hash"`, string(astra["comp_hash"]))
	require.JSONEq(t, `"provider-mode"`, string(astra["tool_mode"]))
	require.JSONEq(t, `false`, string(astra["use_responses_lite"]), "API-key Astra must not advertise Responses Lite")
	// These non-optional fields are required by the Codex ModelInfo wire schema.
	require.JSONEq(t, `"unified_exec"`, string(astra["shell_type"]))
	require.JSONEq(t, `"list"`, string(astra["visibility"]))
	require.JSONEq(t, `true`, string(astra["supported_in_api"]))
	require.JSONEq(t, `0`, string(astra["priority"]))
	require.JSONEq(t, `false`, string(astra["support_verbosity"]))
	require.JSONEq(t, `{"mode":"bytes","limit":10000}`, string(astra["truncation_policy"]))
	require.JSONEq(t, `[]`, string(astra["experimental_supported_tools"]))
	requireCodexSynthesizedInstructions(t, astra["base_instructions"], "gpt-6-astra")
	require.Contains(t, string(astra["base_instructions"]), "You are Codex")
	var envelope struct {
		Models []json.RawMessage `json:"models"`
	}
	require.NoError(t, json.Unmarshal(manifest.Body, &envelope))
	require.Len(t, envelope.Models, 2)
	requireSynthesizedCodexModelSchema(t, envelope.Models[1], "gpt-5.5")
	require.NotContains(t, string(envelope.Models[1]), "supports_search_tool",
		"Astra-only synthesis must not copy tool capabilities onto sibling models")
	for _, field := range []string{"context_window", "max_context_window", "input_modalities"} {
		require.NotContains(t, string(envelope.Models[1]), field,
			"Astra-only synthesis must not copy %s onto sibling models", field)
	}
}

func TestFetchCodexModelsManifestAstraPreservesExplicitNullMetadata(t *testing.T) {
	const body = `{"models":[{"slug":"gpt-6-astra","context_window":null,"max_context_window":null,"input_modalities":null,"supports_search_tool":false}]}`
	s, account := newAstraCodexManifestTestService(t, false, body, `"astra-null"`)
	manifest, err := s.FetchCodexModelsManifest(context.Background(), account, "0.145.0", "")
	require.NoError(t, err)
	astra := requireAstraCodexCapabilities(t, manifest.Body, false)
	for _, field := range []string{"context_window", "max_context_window", "input_modalities"} {
		require.JSONEq(t, `null`, string(astra[field]), "preserve explicit %s", field)
	}
	require.JSONEq(t, `false`, string(astra["supports_search_tool"]))
}

func TestConvertOpenAIModelListToCodexManifestRejectsInvalidAstraToolFields(t *testing.T) {
	body := []byte(`{"object":"list","data":[{
		"id":"gpt-6-astra","supports_search_tool":"yes","apply_patch_tool_type":{},
		"comp_hash":17,"tool_mode":[],"use_responses_lite":"true"
	}]}`)
	converted := convertOpenAIModelListToCodexManifest(body)
	var envelope struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	require.NoError(t, json.Unmarshal(converted, &envelope))
	require.Len(t, envelope.Models, 1)
	for _, field := range []string{"supports_search_tool", "apply_patch_tool_type", "comp_hash", "tool_mode", "use_responses_lite"} {
		require.NotContains(t, envelope.Models[0], field, "invalid %s must not be advertised", field)
	}
}

func TestFetchCodexModelsManifestStandardListPreservesValidatedAstraCoreFields(t *testing.T) {
	tests := []struct {
		name           string
		modelID        string
		mappedModel    string
		fields         string
		wantContext    string
		wantMaxContext string
		wantModalities string
	}{
		{
			name:           "custom limits and text only",
			modelID:        "gpt-6-astra",
			fields:         `"context_window":200000,"max_context_window":180000,"input_modalities":["text"]`,
			wantContext:    `200000`,
			wantMaxContext: `180000`,
			wantModalities: `["text"]`,
		},
		{
			name:           "explicit nulls",
			modelID:        "gpt-6-astra",
			fields:         `"context_window":null,"max_context_window":null,"input_modalities":null`,
			wantContext:    `null`,
			wantMaxContext: `null`,
			wantModalities: `null`,
		},
		{
			name:           "malformed values use missing field defaults",
			modelID:        "gpt-6-astra",
			fields:         `"context_window":"200000","max_context_window":false,"input_modalities":["text",17]`,
			wantContext:    `1050000`,
			wantMaxContext: `1050000`,
			wantModalities: `["text","image"]`,
		},
		{
			name:           "account mapped Astra alias",
			modelID:        "custom-astra",
			mappedModel:    "openai/gpt-6-astra",
			fields:         `"context_window":300000,"max_context_window":280000,"input_modalities":["text"]`,
			wantContext:    `300000`,
			wantMaxContext: `280000`,
			wantModalities: `["text"]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"object":"list","data":[{"id":%q,%s}]}`, tt.modelID, tt.fields)
			s, account := newAstraCodexManifestTestService(t, true, body, `"astra-core"`)
			if tt.mappedModel != "" {
				account.Credentials["model_mapping"] = map[string]any{tt.modelID: tt.mappedModel}
			}
			manifest, err := s.FetchCodexModelsManifest(context.Background(), account, "0.145.0", "")
			require.NoError(t, err)
			var envelope struct {
				Models []map[string]json.RawMessage `json:"models"`
			}
			require.NoError(t, json.Unmarshal(manifest.Body, &envelope))
			require.Len(t, envelope.Models, 1)
			model := envelope.Models[0]
			require.JSONEq(t, fmt.Sprintf("%q", tt.modelID), string(model["slug"]))
			require.JSONEq(t, tt.wantContext, string(model["context_window"]))
			require.JSONEq(t, tt.wantMaxContext, string(model["max_context_window"]))
			require.JSONEq(t, tt.wantModalities, string(model["input_modalities"]))
		})
	}
}

func TestFetchCodexModelsManifestAPIKeyDisablesResponsesLiteForExactAstraTargets(t *testing.T) {
	tests := []struct {
		name     string
		slug     string
		mapped   string
		wantLite bool
	}{
		{name: "canonical", slug: "gpt-6-astra"},
		{name: "provider prefixed", slug: "openai/gpt-6-astra"},
		{name: "mapped custom alias", slug: "custom-astra", mapped: "openai/gpt-6-astra"},
		{name: "preview is not Astra", slug: "gpt-6-astra-preview", wantLite: true},
		{name: "dated alias is not Astra", slug: "gpt-6-astra-20260905", wantLite: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"models": []map[string]any{{"slug": tt.slug, "use_responses_lite": true}}})
			require.NoError(t, err)
			s, account := newAstraCodexManifestTestService(t, true, string(body), `"astra-lite"`)
			if tt.mapped != "" {
				account.Credentials["model_mapping"] = map[string]any{tt.slug: tt.mapped}
			}
			manifest, err := s.FetchCodexModelsManifest(context.Background(), account, "0.145.0", "")
			require.NoError(t, err)
			var envelope struct {
				Models []map[string]json.RawMessage `json:"models"`
			}
			require.NoError(t, json.Unmarshal(manifest.Body, &envelope))
			require.Len(t, envelope.Models, 1)
			require.JSONEq(t, fmt.Sprintf("%t", tt.wantLite), string(envelope.Models[0]["use_responses_lite"]))
		})
	}
}

func TestFetchCodexModelsManifestCacheTracksAstraAliasMapping(t *testing.T) {
	const upstreamBody = `{"models":[{"slug":"custom-astra","use_responses_lite":true}]}`
	const upstreamETag = `"custom-astra-manifest"`
	requests := 0
	upstream := &codexModelsHTTPUpstreamStub{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
		requests++
		require.Empty(t, req.Header.Get("If-None-Match"), "each mapping representation needs its own upstream cache entry")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Etag": []string{upstreamETag}},
			Body:       io.NopCloser(strings.NewReader(upstreamBody)),
		}, nil
	}}
	s := newCodexModelsAPIKeyTestService(upstream)
	account := newCodexModelsAPIKeyTestAccount("https://astra.example/v1")

	fetchLite := func() bool {
		manifest, err := s.FetchCodexModelsManifest(context.Background(), account, "0.145.0", "")
		require.NoError(t, err)
		var envelope struct {
			Models []map[string]json.RawMessage `json:"models"`
		}
		require.NoError(t, json.Unmarshal(manifest.Body, &envelope))
		require.Len(t, envelope.Models, 1)
		var lite bool
		require.NoError(t, json.Unmarshal(envelope.Models[0]["use_responses_lite"], &lite))
		return lite
	}

	require.True(t, fetchLite(), "unmapped custom model preserves the provider value")
	account.Credentials["model_mapping"] = map[string]any{"custom-astra": "openai/gpt-6-astra"}
	require.False(t, fetchLite(), "mapping the same public model to Astra must not reuse the unmapped representation")
	delete(account.Credentials, "model_mapping")
	require.True(t, fetchLite(), "removing the mapping restores the original cached representation")
	require.Equal(t, 2, requests, "each distinct mapping representation is fetched once")
}

func TestFetchCodexModelsManifestAstraInstructionSources(t *testing.T) {
	tests := []struct {
		name         string
		fields       string
		wantBase     string
		wantFallback bool
	}{
		{name: "missing both", fields: `{}`, wantFallback: true},
		{name: "null both", fields: `{"base_instructions":null,"model_messages":null}`, wantFallback: true},
		{name: "messages missing template", fields: `{"model_messages":{"tools":{"custom":true}}}`, wantFallback: true},
		{name: "null template", fields: `{"model_messages":{"instructions_template":null,"instructions_variables":null}}`, wantFallback: true},
		{name: "provider base instructions", fields: `{"base_instructions":"Provider instructions"}`, wantBase: `"Provider instructions"`},
		{name: "explicit empty base instructions", fields: `{"base_instructions":""}`, wantBase: `""`},
		{name: "provider template", fields: `{"model_messages":{"instructions_template":"Provider {{ personality }}","instructions_variables":{"personality_default":"Default personality"}}}`},
		{name: "explicit empty template", fields: `{"model_messages":{"instructions_template":""}}`},
		{name: "provider base and template", fields: `{"base_instructions":"Legacy instructions","model_messages":{"instructions_template":"Canonical instructions"}}`, wantBase: `"Legacy instructions"`},
		{name: "null base with valid template", fields: `{"base_instructions":null,"model_messages":{"instructions_template":"Canonical instructions"}}`, wantBase: `null`},
	}
	for _, apiKey := range []bool{false, true} {
		auth := "OAuth"
		if apiKey {
			auth = "APIKey"
		}
		for _, tt := range tests {
			t.Run(auth+"/"+tt.name, func(t *testing.T) {
				var fields map[string]json.RawMessage
				require.NoError(t, json.Unmarshal([]byte(tt.fields), &fields))
				fields["slug"] = json.RawMessage(`"gpt-6-astra"`)
				body, err := json.Marshal(map[string]any{"models": []map[string]json.RawMessage{fields}})
				require.NoError(t, err)
				s, account := newAstraCodexManifestTestService(t, apiKey, string(body), `"astra-instructions"`)
				manifest, err := s.FetchCodexModelsManifest(context.Background(), account, "0.145.0", "")
				require.NoError(t, err)
				astra := requireAstraCodexCapabilities(t, manifest.Body, false)
				if tt.wantFallback {
					requireCodexSynthesizedInstructions(t, astra["base_instructions"], "gpt-6-astra")
				} else if tt.wantBase == "" {
					require.NotContains(t, astra, "base_instructions")
				} else {
					require.JSONEq(t, tt.wantBase, string(astra["base_instructions"]))
				}
				if original, exists := fields["model_messages"]; exists {
					require.JSONEq(t, string(original), string(astra["model_messages"]))
				} else {
					require.NotContains(t, astra, "model_messages")
				}
			})
		}
	}
}

func TestFetchCodexModelsManifestAstraCanonicalRepresentationPreservesETag(t *testing.T) {
	const body = ` {"models":[{
		"slug":"gpt-6-astra","display_name":"GPT-6-Astra","multi_agent_reasoning_effort":"max",
		"base_instructions":"Provider model instructions",
		"context_window":1050000,"max_context_window":1050000,"input_modalities":["text","image"],
		"default_reasoning_level":"low","shell_type":"unified_exec","visibility":"list",
		"supported_in_api":true,"priority":1,"support_verbosity":true,
		"truncation_policy":{"mode":"tokens","limit":10000},"experimental_supported_tools":[],
		"supported_reasoning_levels":[
			{"effort":"low","description":"Low"},{"effort":"medium","description":"Medium"},
			{"effort":"high","description":"High"},{"effort":"xhigh","description":"Extra high"},
			{"effort":"max","description":"Max"}
		]
	}]} `
	for _, apiKey := range []bool{false, true} {
		name := "OAuth"
		if apiKey {
			name = "APIKey"
		}
		t.Run(name, func(t *testing.T) {
			s, account := newAstraCodexManifestTestService(t, apiKey, body, `W/"astra-canonical"`)
			manifest, err := s.FetchCodexModelsManifest(context.Background(), account, "0.145.0", "")
			require.NoError(t, err)
			require.Equal(t, body, string(manifest.Body))
			require.Equal(t, `W/"astra-canonical"`, manifest.ETag)
			requireAstraCodexCapabilities(t, manifest.Body, false)
		})
	}
}

func TestFetchCodexModelsManifestAstraReplacesLegacyClientETag(t *testing.T) {
	for _, apiKey := range []bool{false, true} {
		name := "OAuth"
		if apiKey {
			name = "APIKey"
		}
		t.Run(name, func(t *testing.T) {
			s, account := newAstraCodexManifestTestService(t, apiKey, astraUpstreamManifest, `"astra-legacy"`)
			manifest, err := s.FetchCodexModelsManifest(context.Background(), account, "0.145.0", `"astra-legacy"`)
			require.NoError(t, err)
			require.False(t, manifest.NotModified, "legacy ETag must receive the corrected representation")
			requireAstraCodexCapabilities(t, manifest.Body, true)
			require.NotEqual(t, `"astra-legacy"`, manifest.ETag)
			cached, err := s.FetchCodexModelsManifest(context.Background(), account, "0.145.0", "W/"+manifest.ETag)
			require.NoError(t, err)
			require.True(t, cached.NotModified, "current representation must support conditional requests")
			require.Equal(t, manifest.ETag, cached.ETag)
			require.Empty(t, cached.Body)
		})
	}
}

func TestFetchCodexModelsManifestAstraCacheRevalidationPreservesCorrection(t *testing.T) {
	s, account := newAstraCodexManifestTestService(t, true, astraUpstreamManifest, `"astra-cached-upstream"`)
	manifest, err := s.FetchCodexModelsManifest(context.Background(), account, "0.145.0", "")
	require.NoError(t, err)
	requireAstraCodexCapabilities(t, manifest.Body, true)
	var cacheKey string
	s.codexModelsManifestCache.mu.Lock()
	for key, entry := range s.codexModelsManifestCache.entries {
		cacheKey = key
		entry.expiresAt = time.Now().Add(-time.Second)
		s.codexModelsManifestCache.entries[key] = entry
	}
	s.codexModelsManifestCache.mu.Unlock()
	require.NotEmpty(t, cacheKey)
	stale, err := s.FetchCodexModelsManifest(context.Background(), account, "0.145.0", `"astra-cached-upstream"`)
	require.NoError(t, err)
	require.False(t, stale.NotModified)
	require.Equal(t, manifest.Body, stale.Body)
	require.Eventually(t, func() bool {
		_, state := s.codexModelsManifestCache.get(cacheKey, time.Now())
		return state == codexModelsManifestCacheFresh
	}, time.Second, 10*time.Millisecond)
	refreshed, err := s.FetchCodexModelsManifest(context.Background(), account, "0.145.0", "")
	require.NoError(t, err)
	require.Equal(t, manifest.Body, refreshed.Body)
	require.Equal(t, manifest.ETag, refreshed.ETag)
	require.Equal(t, `"astra-cached-upstream"`, refreshed.upstreamETag)
}
