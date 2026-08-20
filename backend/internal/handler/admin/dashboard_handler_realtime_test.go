package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type realtimeMetricsProviderStub struct{}

func (realtimeMetricsProviderStub) GetDashboardOverview(context.Context, *service.OpsDashboardFilter) (*service.OpsDashboardOverview, error) {
	avg := 400
	return &service.OpsDashboardOverview{
		QPS:       service.OpsRateSummary{Current: 1.5},
		Duration:  service.OpsPercentiles{Avg: &avg},
		ErrorRate: 0.125,
	}, nil
}

func TestDashboardRealtimeMetricsUsesLiveOpsOverview(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewDashboardHandler(nil, nil, realtimeMetricsProviderStub{})
	router := gin.New()
	router.GET("/realtime", handler.GetRealtimeMetrics)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/realtime", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"active_requests":1`)
	require.Contains(t, rec.Body.String(), `"requests_per_minute":90`)
	require.Contains(t, rec.Body.String(), `"average_response_time":400`)
	require.Contains(t, rec.Body.String(), `"error_rate":0.125`)
}
