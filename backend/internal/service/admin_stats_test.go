package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type redeemStatsRepoStub struct {
	RedeemCodeRepository
	codes []RedeemCode
}

func (r *redeemStatsRepoStub) List(_ context.Context, params pagination.PaginationParams) ([]RedeemCode, *pagination.PaginationResult, error) {
	start := params.Offset()
	limit := params.Limit()
	if start >= len(r.codes) {
		return []RedeemCode{}, &pagination.PaginationResult{Total: int64(len(r.codes)), Page: params.Page, PageSize: params.PageSize}, nil
	}
	end := start + limit
	if end > len(r.codes) {
		end = len(r.codes)
	}
	return r.codes[start:end], &pagination.PaginationResult{Total: int64(len(r.codes)), Page: params.Page, PageSize: params.PageSize}, nil
}

func TestCollectRedeemCodeStatsAggregatesRepositoryData(t *testing.T) {
	now := time.Now()
	expiredAt := now.Add(-time.Minute)
	future := now.Add(time.Hour)
	repo := &redeemStatsRepoStub{codes: []RedeemCode{
		{ID: 1, Type: RedeemTypeBalance, Value: 5, Status: StatusUsed},
		{ID: 2, Type: RedeemTypeConcurrency, Value: 3, Status: StatusUnused, ExpiresAt: &future},
		{ID: 3, Type: RedeemTypeSubscription, Value: 7, Status: StatusExpired},
		{ID: 4, Type: RedeemTypeInvitation, Value: 3, Status: StatusUnused, ExpiresAt: &expiredAt},
	}}

	stats, err := CollectRedeemCodeStats(context.Background(), repo)
	require.NoError(t, err)

	require.Equal(t, int64(4), stats.TotalCodes)
	require.Equal(t, int64(1), stats.ActiveCodes)
	require.Equal(t, int64(1), stats.UsedCodes)
	require.Equal(t, int64(2), stats.ExpiredCodes)
	require.Equal(t, 18.0, stats.TotalValue)
	require.Equal(t, 5.0, stats.TotalValueDistributed)
	require.Equal(t, int64(1), stats.ByType[RedeemTypeBalance])
	require.Equal(t, int64(1), stats.ByType[RedeemTypeConcurrency])
	require.Equal(t, int64(1), stats.ByType[RedeemTypeSubscription])
	require.Equal(t, int64(1), stats.ByType[RedeemTypeInvitation])
}

type proxyStatsRepoStub struct {
	ProxyRepository
	proxy *Proxy
	count int64
}

func (r *proxyStatsRepoStub) GetByID(_ context.Context, id int64) (*Proxy, error) {
	if r.proxy != nil {
		return r.proxy, nil
	}
	return &Proxy{ID: id, Status: StatusActive}, nil
}

func (r *proxyStatsRepoStub) CountAccountsByProxyID(context.Context, int64) (int64, error) {
	return r.count, nil
}

type proxyStatsLatencyCacheStub struct {
	ProxyLatencyCache
	info *ProxyLatencyInfo
}

func (c *proxyStatsLatencyCacheStub) GetProxyLatencies(_ context.Context, _ []int64) (map[int64]*ProxyLatencyInfo, error) {
	return map[int64]*ProxyLatencyInfo{9: c.info}, nil
}

func TestAdminServiceGetProxyStatsUsesAccountCountAndLatencyCache(t *testing.T) {
	latency := int64(123)
	svc := &adminServiceImpl{
		proxyRepo: &proxyStatsRepoStub{
			proxy: &Proxy{ID: 9, Status: StatusActive},
			count: 3,
		},
		proxyLatencyCache: &proxyStatsLatencyCacheStub{
			info: &ProxyLatencyInfo{Success: true, LatencyMs: &latency},
		},
	}

	stats, err := svc.GetProxyStats(context.Background(), 9)
	require.NoError(t, err)

	require.Equal(t, int64(3), stats.TotalAccounts)
	require.Equal(t, int64(3), stats.ActiveAccounts)
	require.Equal(t, int64(0), stats.TotalRequests)
	require.Equal(t, 100.0, stats.SuccessRate)
	require.Equal(t, int64(123), stats.AverageLatency)
}
