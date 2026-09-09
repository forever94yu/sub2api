package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultModelsIncludeBareGPT56Alias(t *testing.T) {
	require.Contains(t, DefaultModelIDs(), "gpt-5.6")
}

func TestDefaultModelsIncludeGPT6Astra(t *testing.T) {
	require.Contains(t, DefaultModelIDs(), "gpt-6-astra")
}

func TestDefaultModelsIncludeOfficialGPTImage25Variants(t *testing.T) {
	ids := DefaultModelIDs()
	for _, id := range []string{
		"gpt-image-2.5-sunburst",
		"gpt-image-2.5-sunburst-2026-09-08",
		"gpt-image-2.5-flare",
		"gpt-image-2.5-flare-2026-09-08",
	} {
		require.Contains(t, ids, id)
	}
	require.NotContains(t, ids, "gpt-image-2.5")
}

func TestDefaultModelsPreferConcreteGPT56SolForAccountTests(t *testing.T) {
	require.NotEmpty(t, DefaultModels)
	require.Equal(t, "gpt-5.6-sol", DefaultModels[0].ID)
}
