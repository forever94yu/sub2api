package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

type MonthlyTierProgress struct {
	GroupID     int64             `json:"group_id"`
	GroupName   string            `json:"group_name"`
	Month       string            `json:"month"`
	RawCost     float64           `json:"raw_cost"`
	ChargedCost float64           `json:"charged_cost"`
	Remaining   float64           `json:"remaining"`
	CurrentRate float64           `json:"current_rate"`
	Policy      MonthlyTierPolicy `json:"policy"`
	ResetsAt    time.Time         `json:"resets_at"`
}

// MonthlyTierProgressForUser 只展示该用户持有密钥或已有月度消费的分组，不接受客户端指定其他用户。
func (s *UsageService) MonthlyTierProgressForUser(ctx context.Context, userID int64, at time.Time) ([]MonthlyTierProgress, error) {
	if s == nil || s.entClient == nil || userID <= 0 {
		return nil, fmt.Errorf("monthly tier progress unavailable")
	}
	month := MonthlyTierMonth(at)
	rows, err := s.entClient.QueryContext(ctx, `SELECT g.id,g.name,g.monthly_tier_policy,COALESCE(u.raw_cost,0),COALESCE(u.charged_cost,0)
	FROM groups g LEFT JOIN monthly_tier_usage u ON u.group_id=g.id AND u.user_id=$1 AND u.month=$2
	WHERE g.deleted_at IS NULL AND g.monthly_tier_policy->>'enabled'='true'
	AND (u.user_id IS NOT NULL OR EXISTS(SELECT 1 FROM api_keys k WHERE k.group_id=g.id AND k.user_id=$1 AND k.deleted_at IS NULL))
	ORDER BY g.id`, userID, month)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]MonthlyTierProgress, 0)
	local := at.In(time.FixedZone("Asia/Shanghai", 8*60*60))
	reset := time.Date(local.Year(), local.Month()+1, 1, 0, 0, 0, 0, local.Location())
	for rows.Next() {
		p := MonthlyTierProgress{Month: month, ResetsAt: reset}
		var raw []byte
		if err := rows.Scan(&p.GroupID, &p.GroupName, &raw, &p.RawCost, &p.ChargedCost); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &p.Policy); err != nil {
			return nil, err
		}
		p.Remaining = math.Max(0, p.Policy.Threshold-p.RawCost)
		p.CurrentRate = p.Policy.FirstRate
		if p.Remaining == 0 {
			p.CurrentRate = p.Policy.ExcessRate
		}
		result = append(result, p)
	}
	return result, rows.Err()
}
