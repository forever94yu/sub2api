package admin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateGroupRequestReasoningEffortMappingsTriState(t *testing.T) {
	t.Run("omitted means unchanged", func(t *testing.T) {
		var req UpdateGroupRequest
		require.NoError(t, json.Unmarshal([]byte(`{}`), &req))
		require.Nil(t, req.ReasoningEffortMappings)
	})

	t.Run("empty array means clear", func(t *testing.T) {
		var req UpdateGroupRequest
		require.NoError(t, json.Unmarshal([]byte(`{"reasoning_effort_mappings":[]}`), &req))
		require.NotNil(t, req.ReasoningEffortMappings)
		require.Empty(t, *req.ReasoningEffortMappings)
	})

	t.Run("non empty array means replace", func(t *testing.T) {
		var req UpdateGroupRequest
		require.NoError(t, json.Unmarshal([]byte(`{"reasoning_effort_mappings":[{"from":"max","to":"xhigh"}]}`), &req))
		require.NotNil(t, req.ReasoningEffortMappings)
		require.Len(t, *req.ReasoningEffortMappings, 1)
		require.Equal(t, "max", (*req.ReasoningEffortMappings)[0].From)
		require.Equal(t, "xhigh", (*req.ReasoningEffortMappings)[0].To)
	})
}

func TestGroupRequestFiveHourLimitBinding(t *testing.T) {
	var create CreateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{"name":"g","five_hour_limit_usd":12.5}`), &create))
	require.True(t, create.FiveHourLimitUSD.IsSet())
	require.Equal(t, 12.5, *create.FiveHourLimitUSD.ToServiceInput())

	var omitted UpdateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{}`), &omitted))
	require.False(t, omitted.FiveHourLimitUSD.IsSet())

	var cleared UpdateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{"five_hour_limit_usd":null}`), &cleared))
	require.True(t, cleared.FiveHourLimitUSD.IsSet())
	require.Nil(t, cleared.FiveHourLimitUSD.ToServiceInput())
}
