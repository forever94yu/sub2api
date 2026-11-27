package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/shopspring/decimal"
)

func readMonthlyTierSettlement(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand) (*service.MonthlyTierSettlement, error) {
	var s service.MonthlyTierSettlement
	err := tx.QueryRowContext(ctx, `SELECT group_id, month, raw_cost, charged_cost, raw_before, raw_after
	FROM monthly_tier_settlements WHERE request_id=$1 AND api_key_id=$2`, cmd.RequestID, cmd.APIKeyID).
		Scan(&s.GroupID, &s.Month, &s.RawCost, &s.ChargedCost, &s.RawBefore, &s.RawAfter)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &s, err
}

// applyMonthlyTier 在去重成功后、余额扣款前调用；使用同一事务的行锁串行化用户月度累计。
func applyMonthlyTier(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand, result *service.UsageBillingApplyResult) error {
	m := cmd.MonthlyTier
	if m == nil {
		return nil
	}
	if m.GroupID <= 0 || cmd.UserID <= 0 || cmd.SubscriptionID != nil || math.IsNaN(m.RawCost) || math.IsInf(m.RawCost, 0) || m.RawCost < 0 {
		return fmt.Errorf("invalid monthly tier billing command")
	}
	if _, err := time.Parse("2006-01", m.Month); err != nil {
		return fmt.Errorf("invalid monthly tier period")
	}
	if err := service.ValidateMonthlyTierPolicy(&m.Policy, service.PlatformAnthropic, service.SubscriptionTypeStandard); err != nil {
		return err
	}
	// 优惠流水永久保留，即使普通去重归档已清理，也不能重复扣款。
	if old, err := readMonthlyTierSettlement(ctx, tx, cmd); err != nil {
		return err
	} else if old != nil {
		return fmt.Errorf("monthly tier settlement exists without billing dedup")
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO monthly_tier_usage(user_id,group_id,month) VALUES($1,$2,$3)
	ON CONFLICT(user_id,group_id,month) DO NOTHING`, cmd.UserID, m.GroupID, m.Month)
	if err != nil {
		return err
	}
	var before decimal.Decimal
	if err = tx.QueryRowContext(ctx, `SELECT raw_cost FROM monthly_tier_usage
	WHERE user_id=$1 AND group_id=$2 AND month=$3 FOR UPDATE`, cmd.UserID, m.GroupID, m.Month).Scan(&before); err != nil {
		return err
	}
	raw := decimal.NewFromFloat(m.RawCost).Round(10)
	charge, err := service.MonthlyTierCharge(before, raw, m.Policy)
	if err != nil {
		return err
	}
	after := before.Add(raw)
	policy, err := json.Marshal(m.Policy)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO monthly_tier_settlements
	(request_id,api_key_id,user_id,group_id,month,raw_cost,charged_cost,raw_before,raw_after,policy)
	VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, cmd.RequestID, cmd.APIKeyID, cmd.UserID, m.GroupID, m.Month, raw.String(), charge.String(), before.String(), after.String(), string(policy))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE monthly_tier_usage SET raw_cost=$4,charged_cost=charged_cost+$5,updated_at=NOW()
	WHERE user_id=$1 AND group_id=$2 AND month=$3`, cmd.UserID, m.GroupID, m.Month, after.String(), charge.String())
	if err != nil {
		return err
	}
	debit, _ := charge.Float64()
	cmd.BalanceCost = debit
	if m.TrackKeyQuota || cmd.APIKeyQuotaCost > 0 {
		cmd.APIKeyQuotaCost = debit
	}
	if m.TrackKeyRateLimit || cmd.APIKeyRateLimitCost > 0 {
		cmd.APIKeyRateLimitCost = debit
	}
	rawFloat, _ := raw.Float64()
	beforeFloat, _ := before.Float64()
	afterFloat, _ := after.Float64()
	result.MonthlyTier = &service.MonthlyTierSettlement{GroupID: m.GroupID, Month: m.Month, RawCost: rawFloat, ChargedCost: debit, RawBefore: beforeFloat, RawAfter: afterFloat}
	return nil
}
