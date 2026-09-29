package claude

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSonnet55ModelCatalog(t *testing.T) {
	require.Contains(t, DefaultModelIDs(), "claude-sonnet-5-5")
	for _, model := range DefaultModels {
		if model.ID == "claude-sonnet-5-5" {
			require.Equal(t, "Claude Sonnet 5.5", model.DisplayName)
			require.Equal(t, "2026-09-28T00:00:00Z", model.CreatedAt)
		}
	}
	require.Equal(t, "claude-sonnet-5-5", NormalizeModelID("claude-sonnet-5-5"))
	require.Equal(t, []string{"low", "medium", "high", "xhigh", "max"}, EffortLevelsForModel("claude-sonnet-5-5"))
}

func TestSonnet55ExactModelAliases(t *testing.T) {
	for _, model := range []string{
		"claude-sonnet-5-5", " CLAUDE-SONNET-5-5 ", "claude-sonnet-5.5",
		"models/claude-sonnet-5-5", "anthropic/claude-sonnet-5-5", "vertex_ai/claude-sonnet-5-5",
		"projects/test/locations/us-east5/publishers/anthropic/models/claude-sonnet-5-5",
		"anthropic.claude-sonnet-5-5", "us.anthropic.claude-sonnet-5-5",
		"eu.anthropic.claude-sonnet-5-5", "apac.anthropic.claude-sonnet-5-5", "global.anthropic.claude-sonnet-5-5",
		"claude-sonnet-5-5-thinking", "claude-sonnet-5-5-latest",
		"claude-sonnet-5-5-20260928", "claude-sonnet-5-5@20260928", "anthropic.claude-sonnet-5-5-v1:0",
	} {
		t.Run(model, func(t *testing.T) {
			require.True(t, IsSonnet55(model))
			require.True(t, HasAdaptiveThinkingDefault(model))
		})
	}
	for _, model := range []string{
		"claude-sonnet-5", "claude-sonnet-5-50", "claude-sonnet-5.50", "claude-sonnet-5-6", "claude-sonnet-5.6",
		"claude-sonnet-5-5-preview", "claude-sonnet-5-5-extra", "claude-sonnet-5-5-2026092x",
		"projects/test/locations/global/publishers/anthropic/models/claude-sonnet-5-50",
		"us.anthropic.claude-sonnet-5-6-v1:0", "gpt-5.5", "claude-opus-5-5", "",
	} {
		t.Run(model, func(t *testing.T) {
			require.False(t, IsSonnet55(model))
		})
	}
}
