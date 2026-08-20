package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

type RedeemCodeStats struct {
	TotalCodes            int64            `json:"total_codes"`
	ActiveCodes           int64            `json:"active_codes"`
	UnusedCodes           int64            `json:"unused_codes"`
	UsedCodes             int64            `json:"used_codes"`
	ExpiredCodes          int64            `json:"expired_codes"`
	TotalValue            float64          `json:"total_value"`
	TotalValueDistributed float64          `json:"total_value_distributed"`
	ByType                map[string]int64 `json:"by_type"`
}

func (s *RedeemCodeStats) AsMap() map[string]any {
	if s == nil {
		return map[string]any{}
	}
	return map[string]any{
		"total_codes":             s.TotalCodes,
		"active_codes":            s.ActiveCodes,
		"unused_codes":            s.UnusedCodes,
		"used_codes":              s.UsedCodes,
		"expired_codes":           s.ExpiredCodes,
		"total_value":             s.TotalValue,
		"total_value_distributed": s.TotalValueDistributed,
		"by_type":                 s.ByType,
	}
}

func CollectRedeemCodeStats(ctx context.Context, repo RedeemCodeRepository) (*RedeemCodeStats, error) {
	stats := &RedeemCodeStats{
		ByType: map[string]int64{
			RedeemTypeBalance:      0,
			RedeemTypeConcurrency:  0,
			RedeemTypeSubscription: 0,
			RedeemTypeInvitation:   0,
		},
	}
	if repo == nil {
		return stats, nil
	}

	now := time.Now()
	for page := 1; ; page++ {
		params := pagination.PaginationParams{
			Page:      page,
			PageSize:  1000,
			SortBy:    "id",
			SortOrder: pagination.SortOrderAsc,
		}
		codes, pageResult, err := repo.List(ctx, params)
		if err != nil {
			return nil, err
		}
		for i := range codes {
			stats.addRedeemCode(codes[i], now)
		}
		if len(codes) < params.Limit() {
			break
		}
		if pageResult != nil && int64(page*params.Limit()) >= pageResult.Total {
			break
		}
	}
	return stats, nil
}

func (s *RedeemCodeStats) addRedeemCode(code RedeemCode, now time.Time) {
	s.TotalCodes++
	s.TotalValue += code.Value
	if code.Type != "" {
		s.ByType[code.Type]++
	}
	switch {
	case code.IsExpiredAt(now):
		s.ExpiredCodes++
	case code.Status == StatusUsed:
		s.UsedCodes++
		s.TotalValueDistributed += code.Value
	case code.Status == StatusUnused:
		s.ActiveCodes++
		s.UnusedCodes++
	}
}

func (s *adminServiceImpl) GetRedeemCodeStats(ctx context.Context) (*RedeemCodeStats, error) {
	return CollectRedeemCodeStats(ctx, s.redeemCodeRepo)
}

type ProxyStats struct {
	TotalAccounts  int64   `json:"total_accounts"`
	ActiveAccounts int64   `json:"active_accounts"`
	TotalRequests  int64   `json:"total_requests"`
	SuccessRate    float64 `json:"success_rate"`
	AverageLatency int64   `json:"average_latency"`
}

func (s *adminServiceImpl) GetProxyStats(ctx context.Context, proxyID int64) (*ProxyStats, error) {
	if _, err := s.proxyRepo.GetByID(ctx, proxyID); err != nil {
		return nil, err
	}
	accountCount, err := s.proxyRepo.CountAccountsByProxyID(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	stats := &ProxyStats{
		TotalAccounts:  accountCount,
		ActiveAccounts: accountCount,
	}
	type activeAccountCounter interface {
		CountActiveAccountsByProxyID(context.Context, int64) (int64, error)
	}
	if counter, ok := s.proxyRepo.(activeAccountCounter); ok {
		stats.ActiveAccounts, err = counter.CountActiveAccountsByProxyID(ctx, proxyID)
		if err != nil {
			return nil, err
		}
	}
	type usageRequestCounter interface {
		CountUsageRequestsByProxyID(context.Context, int64) (int64, error)
	}
	if counter, ok := s.proxyRepo.(usageRequestCounter); ok {
		stats.TotalRequests, err = counter.CountUsageRequestsByProxyID(ctx, proxyID)
		if err != nil {
			return nil, err
		}
	}
	if s.proxyLatencyCache == nil {
		return stats, nil
	}
	latencies, err := s.proxyLatencyCache.GetProxyLatencies(ctx, []int64{proxyID})
	if err != nil {
		return stats, nil
	}
	info := latencies[proxyID]
	if info == nil {
		return stats, nil
	}
	if info.Success {
		stats.SuccessRate = 100
	}
	if info.LatencyMs != nil {
		stats.AverageLatency = *info.LatencyMs
	}
	return stats, nil
}
