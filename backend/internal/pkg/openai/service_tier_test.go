package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceTierObserverTerminalOverridesEarlyDeclaration(t *testing.T) {
	var observer ServiceTierObserver
	fallback := "flex"
	observer.Observe([]byte(`{"response":{"service_tier":"priority"}}`), "response.created")
	observer.Observe([]byte(`{"response":{"service_tier":"default"}}`), "response.completed")
	observer.Observe([]byte(`{"service_tier":"fast"}`), "response.output_text.delta")
	require.Equal(t, "default", *observer.Resolve(&fallback))
	var nextTurn ServiceTierObserver
	require.Equal(t, "flex", *nextTurn.Resolve(&fallback))
}

func TestServiceTierObserverNormalizationAndFallback(t *testing.T) {
	for _, tc := range []struct{ name, payload, want string }{
		{"fast", `{"service_tier":" FAST "}`, "priority"},
		{"nested", `{"response":{"service_tier":"flex"},"service_tier":"priority"}`, "flex"},
		{"invalid_nested", `{"response":{"service_tier":12},"service_tier":"default"}`, "default"},
		{"unknown", `{"service_tier":"unknown"}`, "flex"},
		{"auto_not_actual", `{"service_tier":"auto"}`, "flex"},
		{"wrong_type", `{"service_tier":true}`, "flex"},
		{"null", `{"service_tier":null}`, "flex"},
		{"missing", `{"usage":{"input_tokens":4}}`, "flex"},
		{"malformed", `{"service_tier":"priority",`, "flex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var observer ServiceTierObserver
			fallback := "flex"
			observer.Observe([]byte(tc.payload), "response.completed")
			require.Equal(t, tc.want, *observer.Resolve(&fallback))
		})
	}
}

func TestServiceTierObserverChatUsageOverridesInitialChunk(t *testing.T) {
	var observer ServiceTierObserver
	observer.Observe([]byte(`{"service_tier":"priority","choices":[]}`), "")
	observer.Observe([]byte(`{"service_tier":"default","choices":[],"usage":{"prompt_tokens":4}}`), "")
	require.Equal(t, "default", *observer.Resolve(nil))
}

func TestServiceTierObserverInvalidTerminalPreservesKnownActualTier(t *testing.T) {
	var observer ServiceTierObserver
	fallback := "flex"
	observer.Observe([]byte(`{"response":{"service_tier":"priority"}}`), "response.created")
	observer.Observe([]byte(`{"response":{"service_tier":"unknown"}}`), "response.completed")
	require.Equal(t, "priority", *observer.Resolve(&fallback))
}

func TestResolveServiceTierFallbackAndNil(t *testing.T) {
	var observer *ServiceTierObserver
	require.Nil(t, observer.Resolve(nil))
	observer.Observe(nil, "")
	auto := "auto"
	require.Equal(t, "auto", *ResolveServiceTier(&auto, &auto))
	unknown := "unknown"
	require.Nil(t, ResolveServiceTier(&unknown, &unknown))
	fast := " FAST "
	require.Equal(t, "priority", *ResolveServiceTier(nil, &fast))
}
