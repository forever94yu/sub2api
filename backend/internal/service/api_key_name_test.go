//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type keyNameRepository struct {
	APIKeyRepository
	key APIKey
}

func (r *keyNameRepository) Create(_ context.Context, key *APIKey) error {
	key.ID = 1
	r.key = *key
	return nil
}

func (r *keyNameRepository) GetByID(context.Context, int64) (*APIKey, error) {
	key := r.key
	return &key, nil
}

func (r *keyNameRepository) Update(_ context.Context, key *APIKey, _ APIKeyUpdateFields) error {
	r.key = *key
	return nil
}

func TestAPIKeyNameRoundTripsWithoutHTMLEncoding(t *testing.T) {
	for _, name := range []string{"A&B", `<script>test</script>`, `literal &amp; and "quotes"`} {
		t.Run(name, func(t *testing.T) {
			repo := &keyNameRepository{}
			svc := &APIKeyService{apiKeyRepo: repo, userRepo: &revocationUserRepository{user: User{ID: 7}}, cfg: &config.Config{}}
			key, err := svc.Create(context.Background(), 7, CreateAPIKeyRequest{Name: name})
			require.NoError(t, err)
			require.Equal(t, name, key.Name)
			for range 3 {
				key, err = svc.Update(context.Background(), key.ID, 7, UpdateAPIKeyRequest{Name: &key.Name})
				require.NoError(t, err)
				require.Equal(t, name, key.Name)
				require.Equal(t, name, repo.key.Name)
			}
		})
	}
}
