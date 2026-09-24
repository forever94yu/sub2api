package claude

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/mod/semver"
)

func TestCatalogUpdateSupportsNewClaudeModels(t *testing.T) {
	for _, model := range []string{"claude-fable-5-1", "claude-opus-5-5"} {
		require.Contains(t, DefaultModelIDs(), model)
		require.Equal(t, model, NormalizeModelID(model))
	}
	require.GreaterOrEqual(t, semver.Compare("v"+CLICurrentVersion, "v2.1.251"), 0,
		"Fable 5.1 rejects older CLI versions")
}
