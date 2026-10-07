//go:build unit

package repository

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestS3ImageStorageConnectionChecksBucket(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodHead || r.URL.Path != "/images" || r.Header.Get("Authorization") == "" {
					t.Errorf("expected signed HEAD /images, got %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			storage, err := NewS3ImageStorage(context.Background(), &config.ImageStorageConfig{
				Endpoint: server.URL, Region: "auto", Bucket: "images",
				AccessKeyID: "test-ak", SecretAccessKey: "test-sk", ForcePathStyle: true,
			})
			require.NoError(t, err)
			tester, ok := any(storage).(interface{ TestConnection(context.Context) error })
			require.True(t, ok, "S3 image storage must support a real connection check")
			err = tester.TestConnection(context.Background())
			if status == http.StatusOK {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.EqualValues(t, 1, requests.Load())
		})
	}
}
