package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type revocationUserRepository struct {
	UserRepository
	user User
	err  error
}

func (r *revocationUserRepository) GetByID(context.Context, int64) (*User, error) {
	u := r.user
	return &u, nil
}

func (r *revocationUserRepository) IncrementTokenVersion(context.Context, int64) error {
	if r.err != nil {
		return r.err
	}
	r.user.TokenVersion++
	return nil
}

type revocationRefreshCache struct {
	RefreshTokenCache
	err  error
	data *RefreshTokenData
}

func (c *revocationRefreshCache) DeleteUserRefreshTokens(context.Context, int64) error {
	return c.err
}

func (c *revocationRefreshCache) GetRefreshToken(context.Context, string) (*RefreshTokenData, error) {
	return c.data, nil
}

func (c *revocationRefreshCache) DeleteTokenFamily(context.Context, string) error {
	return c.err
}

func TestRevokeAllUserTokensInvalidatesAccessAndLegacyRefresh(t *testing.T) {
	for _, cacheFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "healthy_cache", true: "cache_unavailable"}[cacheFails], func(t *testing.T) {
			repo := &revocationUserRepository{user: User{ID: 7, Email: "revoke@example.test", PasswordHash: "hash", Status: StatusActive, Balance: 150}}
			cache := &revocationRefreshCache{}
			if cacheFails {
				cache.err = errors.New("redis unavailable")
			}
			svc := &AuthService{userRepo: repo, refreshTokenCache: cache, cfg: &config.Config{JWT: config.JWTConfig{Secret: strings.Repeat("r", 32), ExpireHour: 24}}}
			ctx := context.Background()
			oldToken, err := svc.GenerateToken(ctx, &repo.user)
			require.NoError(t, err)
			oldClaims, err := svc.ValidateToken(oldToken)
			require.NoError(t, err)
			cache.data = &RefreshTokenData{UserID: repo.user.ID, TokenVersion: oldClaims.TokenVersion, FamilyID: "old-family", ExpiresAt: time.Now().Add(time.Hour)}
			require.NoError(t, svc.RevokeAllUserTokens(ctx, repo.user.ID))
			require.NotEqual(t, oldClaims.TokenVersion, resolvedTokenVersion(&repo.user), "revocation must invalidate access tokens even if Redis cleanup fails")
			_, err = svc.RefreshToken(ctx, oldToken)
			require.ErrorIs(t, err, ErrTokenRevoked)
			_, err = svc.RefreshTokenPair(ctx, refreshTokenPrefix+"old-session")
			require.ErrorIs(t, err, ErrTokenRevoked, "stale Redis sessions must not bypass persisted revocation")
			newToken, err := svc.GenerateToken(ctx, &repo.user)
			require.NoError(t, err)
			newClaims, err := svc.ValidateToken(newToken)
			require.NoError(t, err)
			require.Equal(t, resolvedTokenVersion(&repo.user), newClaims.TokenVersion)
			require.Equal(t, 150.0, repo.user.Balance)
		})
	}
}

func TestRevokeAllUserTokensPropagatesPersistenceFailure(t *testing.T) {
	writeErr := errors.New("database write failed")
	repo := &revocationUserRepository{user: User{ID: 7}, err: writeErr}
	svc := &AuthService{userRepo: repo}
	require.ErrorIs(t, svc.RevokeAllUserTokens(context.Background(), 7), writeErr)
}
