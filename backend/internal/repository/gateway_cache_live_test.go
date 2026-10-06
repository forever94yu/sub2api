package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayCacheLiveCallIdentityAndController(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	cache, ok := NewGatewayCache(client).(service.LiveCallStore)
	require.True(t, ok)
	otherInstance, ok := NewGatewayCache(client).(service.LiveCallStore)
	require.True(t, ok)
	record := &service.LiveCallRecord{
		CallID:                "call_secret",
		CallHash:              HashLiveCallID("call_secret"),
		AccountID:             11,
		APIKeyID:              22,
		UserID:                33,
		GroupID:               44,
		LeaseID:               "lease",
		Model:                 "gpt-live-test",
		AttestationCiphertext: "encrypted-attestation",
		CreatedAt:             time.Now(),
		ExpiresAt:             time.Now().Add(time.Hour),
		Controller:            service.LiveControllerPending,
	}
	require.NoError(t, cache.SaveLiveCall(context.Background(), record, time.Hour))

	loaded, err := otherInstance.GetLiveCall(context.Background(), record.CallHash)
	require.NoError(t, err)
	require.Equal(t, record.CallID, loaded.CallID)
	require.Equal(t, record.AccountID, loaded.AccountID)
	require.Equal(t, record.AttestationCiphertext, loaded.AttestationCiphertext)

	claimed, err := cache.ClaimLiveController(context.Background(), record.CallHash, service.LiveControllerObserver, "observer-1")
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = cache.ClaimLiveController(context.Background(), record.CallHash, service.LiveControllerProxy, "proxy-1")
	require.NoError(t, err)
	require.True(t, claimed)
	controller, err := cache.GetLiveController(context.Background(), record.CallHash)
	require.NoError(t, err)
	require.Equal(t, service.LiveControllerProxy, controller)

	released, err := cache.ReleaseLiveController(context.Background(), record.CallHash, "proxy-1")
	require.NoError(t, err)
	require.True(t, released)
	closed, err := cache.MarkLiveCallClosed(context.Background(), record.CallHash, time.Hour)
	require.NoError(t, err)
	require.True(t, closed)
	closed, err = cache.MarkLiveCallClosed(context.Background(), record.CallHash, time.Hour)
	require.NoError(t, err)
	require.False(t, closed)
}

func TestGatewayCacheLiveCallBillingSnapshotRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name     string
		snapshot *service.LiveAPIKeyBillingSnapshot
	}{
		{name: "capped", snapshot: &service.LiveAPIKeyBillingSnapshot{Quota: 10, RateLimit5h: 2, RateLimit1d: 3, RateLimit7d: 4}},
		{name: "unlimited", snapshot: &service.LiveAPIKeyBillingSnapshot{}},
		{name: "pre_upgrade"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			redisServer := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
			t.Cleanup(func() { _ = client.Close() })
			first, ok := NewGatewayCache(client).(service.LiveCallStore)
			require.True(t, ok)
			second, ok := NewGatewayCache(client).(service.LiveCallStore)
			require.True(t, ok)
			record := &service.LiveCallRecord{
				CallID: "call_secret", CallHash: HashLiveCallID("call_secret"), APIKeyID: 22,
				APIKeyBilling: tc.snapshot, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
			}
			require.NoError(t, first.SaveLiveCall(context.Background(), record, time.Hour))
			loaded, err := second.GetLiveCall(context.Background(), record.CallHash)
			require.NoError(t, err)
			require.Equal(t, tc.snapshot, loaded.APIKeyBilling)
			fields, err := client.HGetAll(context.Background(), liveCallKey(record.CallHash)).Result()
			require.NoError(t, err)
			require.NotContains(t, fields, "api_key")
			if tc.snapshot != nil {
				require.Contains(t, fields, "api_key_quota")
				require.Contains(t, fields, "api_key_rate_limit_7d")
			} else {
				require.NotContains(t, fields, "api_key_quota")
			}
		})
	}
}

func TestGatewayCacheLiveCallIncompleteBillingSnapshotUsesLegacyFallback(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache, ok := NewGatewayCache(client).(service.LiveCallStore)
	require.True(t, ok)
	record := &service.LiveCallRecord{
		CallID: "call_secret", CallHash: HashLiveCallID("call_secret"),
		APIKeyBilling: &service.LiveAPIKeyBillingSnapshot{Quota: 10},
	}
	require.NoError(t, cache.SaveLiveCall(context.Background(), record, time.Hour))
	require.NoError(t, client.HDel(context.Background(), liveCallKey(record.CallHash), "api_key_rate_limit_1d").Err())
	loaded, err := cache.GetLiveCall(context.Background(), record.CallHash)
	require.NoError(t, err)
	require.Nil(t, loaded.APIKeyBilling)
}
