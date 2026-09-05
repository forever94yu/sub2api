//go:build integration

package repository

import (
	"context"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUserTokenRevocationIsPersistentAtomicAndFieldScoped(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	stored, err := client.User.Create().SetEmail("token-revocation@example.test").SetPasswordHash("test-hash").SetBalance(150).Save(ctx)
	require.NoError(t, err)
	repo := newUserRepositoryWithSQL(client, integrationDB)
	before, err := repo.GetByID(ctx, stored.ID)
	require.NoError(t, err)
	require.Zero(t, before.TokenVersion)

	const workers = 8
	errs := make(chan error, workers*2)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(2)
		go func() { defer wg.Done(); errs <- repo.IncrementTokenVersion(ctx, stored.ID) }()
		go func() { defer wg.Done(); errs <- repo.UpdateBalance(ctx, stored.ID, 1) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	loaded, err := newUserRepositoryWithSQL(client, integrationDB).GetByID(ctx, stored.ID)
	require.NoError(t, err)
	require.Equal(t, int64(workers), loaded.TokenVersion)
	require.Equal(t, 150.0+workers, loaded.Balance)
	require.Equal(t, before.PasswordHash, loaded.PasswordHash)
	require.Equal(t, before.Email, loaded.Email)

	require.NoError(t, repo.Delete(ctx, stored.ID))
	require.ErrorIs(t, repo.IncrementTokenVersion(ctx, stored.ID), service.ErrUserNotFound)
}
