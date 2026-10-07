//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyCreateExpiration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		precise bool
		days    int
	}{
		{"precise", `{"name":"hour-key","expires_at":"2099-10-07T13:15:00+08:00"}`, true, 0},
		{"precise takes precedence", `{"name":"hour-key","expires_at":"2099-10-07T13:15:00+08:00","expires_in_days":7}`, true, 0},
		{"legacy days", `{"name":"legacy","expires_in_days":7}`, false, 7},
		{"no expiration", `{"name":"unlimited"}`, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var req CreateAPIKeyRequest
			require.NoError(t, json.Unmarshal([]byte(tc.payload), &req))
			repo := &keyNameRepository{}
			svc := &APIKeyService{apiKeyRepo: repo, userRepo: &revocationUserRepository{user: User{ID: 7}}, cfg: &config.Config{}}
			before := time.Now().AddDate(0, 0, tc.days)
			key, err := svc.Create(context.Background(), 7, req)
			require.NoError(t, err)
			if tc.precise {
				require.NotNil(t, key.ExpiresAt)
				require.Equal(t, "2099-10-07T05:15:00Z", key.ExpiresAt.UTC().Format(time.RFC3339))
				require.Equal(t, key.ExpiresAt, repo.key.ExpiresAt)
			} else if tc.days > 0 {
				require.NotNil(t, key.ExpiresAt)
				require.False(t, key.ExpiresAt.Before(before))
				require.False(t, key.ExpiresAt.After(time.Now().AddDate(0, 0, tc.days)))
			} else {
				require.Nil(t, key.ExpiresAt)
			}
		})
	}
}

func TestAPIKeyExpirationUpdateAndClear(t *testing.T) {
	repo := &keyNameRepository{}
	svc := &APIKeyService{apiKeyRepo: repo, userRepo: &revocationUserRepository{user: User{ID: 7}}, cfg: &config.Config{}}
	key, err := svc.Create(context.Background(), 7, CreateAPIKeyRequest{Name: "editable"})
	require.NoError(t, err)
	expiresAt := time.Date(2099, 10, 7, 5, 15, 0, 0, time.UTC)
	key, err = svc.Update(context.Background(), key.ID, 7, UpdateAPIKeyRequest{ExpiresAt: &expiresAt})
	require.NoError(t, err)
	require.Equal(t, &expiresAt, key.ExpiresAt)
	require.Equal(t, &expiresAt, repo.key.ExpiresAt)
	key, err = svc.Update(context.Background(), key.ID, 7, UpdateAPIKeyRequest{ClearExpiration: true})
	require.NoError(t, err)
	require.Nil(t, key.ExpiresAt)
	require.Nil(t, repo.key.ExpiresAt)
}
