package service

import (
	"context"
	"encoding/json"
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
				if field != "display_name" && field != "supported_reasoning_levels" && field != "multi_agent_reasoning_effort" {
					require.JSONEq(t, string(value), string(astra[field]), "preserve Astra field %s", field)
				}
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
	const body = `{"object":"list","data":[{"id":"gpt-6-astra","object":"model"},{"id":"gpt-5.5","object":"model"}]}`
	s, account := newAstraCodexManifestTestService(t, true, body, `"astra-list"`)
	manifest, err := s.FetchCodexModelsManifest(context.Background(), account, "0.145.0", "")
	require.NoError(t, err)
	astra := requireAstraCodexCapabilities(t, manifest.Body, false)
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
