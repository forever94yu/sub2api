package claude

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHaiku55ModelCatalog(t *testing.T) {
	require.Contains(t, DefaultModelIDs(), "claude-haiku-5-5")
	for _, model := range DefaultModels {
		if model.ID == "claude-haiku-5-5" {
			require.Equal(t, "Claude Haiku 5.5", model.DisplayName)
			require.Equal(t, "2026-10-07T00:00:00Z", model.CreatedAt)
		}
	}
	require.Equal(t, "claude-haiku-5-5", NormalizeModelID("claude-haiku-5-5"))
}

func TestHaiku55ExactAliasesEnableAdaptiveThinkingAndEffort(t *testing.T) {
	for _, model := range []string{
		"claude-haiku-5-5", " CLAUDE-HAIKU-5-5 ", "claude-haiku-5.5",
		"models/claude-haiku-5-5", "anthropic/claude-haiku-5-5", "vertex_ai/claude-haiku-5-5",
		"projects/test/locations/us-east5/publishers/anthropic/models/claude-haiku-5-5",
		"anthropic.claude-haiku-5-5", "us.anthropic.claude-haiku-5-5",
		"eu.anthropic.claude-haiku-5-5", "apac.anthropic.claude-haiku-5-5", "global.anthropic.claude-haiku-5-5",
		"claude-haiku-5-5-thinking", "claude-haiku-5-5-latest",
		"claude-haiku-5-5-20261007", "claude-haiku-5-5@20261007", "anthropic.claude-haiku-5-5-v1:0",
	} {
		t.Run(model, func(t *testing.T) {
			require.True(t, IsHaiku55(model))
			require.True(t, HasAdaptiveThinkingDefault(model))
			require.Equal(t, []string{"low", "medium", "high", "xhigh", "max"}, EffortLevelsForModel(model))
		})
	}
	for _, model := range []string{
		"claude-haiku-4-5", "claude-haiku-5", "claude-haiku-5-50", "claude-haiku-5.50",
		"claude-haiku-5-6", "claude-haiku-5-5-preview", "claude-haiku-5-5-extra",
		"claude-haiku-5-5-2026100x", "us.anthropic.claude-haiku-5-6-v1:0", "gpt-5.5", "",
	} {
		t.Run(model, func(t *testing.T) {
			require.False(t, IsHaiku55(model))
			require.False(t, HasAdaptiveThinkingDefault(model))
			require.Nil(t, EffortLevelsForModel(model))
		})
	}
	for _, model := range []string{"claude-sonnet-5-5", "claude-opus-5-5"} {
		require.False(t, IsHaiku55(model), model)
		require.True(t, HasAdaptiveThinkingDefault(model), model)
	}
}
