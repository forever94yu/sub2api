package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/stretchr/testify/require"
)

func TestClaudeIdentityUpdateFloorsCachedVersion(t *testing.T) {
	cache := &stubIdentityCache{fingerprint: &Fingerprint{
		UserAgent:               "claude-cli/2.1.220 (external, claude-desktop-3p)",
		StainlessPackageVersion: "0.91.1",
		UpdatedAt:               time.Now().Unix(),
	}}
	fp, err := NewIdentityService(cache).GetOrCreateFingerprint(context.Background(), 1,
		headersWithUA("claude-cli/2.1.75 (external, cli)"))
	require.NoError(t, err)
	require.Equal(t, "claude-cli/"+claude.CLIVersion()+" (external, claude-desktop-3p)", fp.UserAgent)
	require.Equal(t, "0.91.1", fp.StainlessPackageVersion)
	require.Equal(t, 1, cache.setCalls)
}
