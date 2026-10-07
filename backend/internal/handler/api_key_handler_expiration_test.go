//go:build unit

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type expirationKeyRepository struct {
	service.APIKeyRepository
	key *service.APIKey
}

func (r *expirationKeyRepository) Create(_ context.Context, key *service.APIKey) error {
	key.ID = 1
	r.key = key
	return nil
}

type expirationUserRepository struct{ service.UserRepository }

func (*expirationUserRepository) GetByID(context.Context, int64) (*service.User, error) {
	return &service.User{ID: 7}, nil
}

func TestAPIKeyHandlerCreatePreciseExpiration(t *testing.T) {
	for _, tc := range []struct {
		name, expiresAt string
		status          int
	}{
		{"precise", "2099-10-07T13:15:00+08:00", http.StatusOK},
		{"invalid", "not-a-date", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &expirationKeyRepository{}
			svc := service.NewAPIKeyService(repo, &expirationUserRepository{}, nil, nil, nil, nil, &config.Config{})
			handler := NewAPIKeyHandler(svc)
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/keys", strings.NewReader(`{"name":"hour-key","expires_at":"`+tc.expiresAt+`"}`))
			ctx.Request.Header.Set("Content-Type", "application/json")
			ctx.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
			handler.Create(ctx)
			require.Equal(t, tc.status, response.Code, response.Body.String())
			if tc.status == http.StatusOK {
				require.NotNil(t, repo.key.ExpiresAt)
				require.Equal(t, "2099-10-07T05:15:00Z", repo.key.ExpiresAt.UTC().Format(time.RFC3339))
			} else {
				require.Nil(t, repo.key)
			}
		})
	}
}
