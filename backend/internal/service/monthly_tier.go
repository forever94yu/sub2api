package service

import (
	"fmt"
	"math"
	"time"

	"github.com/shopspring/decimal"
)

// MonthlyTierPolicy 替代余额消费的固定倍率；原价累计不含任何分组/用户倍率。
type MonthlyTierPolicy struct {
	Enabled    bool      `json:"enabled"`
	Threshold  float64   `json:"threshold"`
	FirstRate  float64   `json:"first_rate"`
	ExcessRate float64   `json:"excess_rate"`
	StartsAt   time.Time `json:"starts_at"`
}

func ValidateMonthlyTierPolicy(p *MonthlyTierPolicy, platform, subscriptionType string) error {
	if p == nil || !p.Enabled {
		return nil
	}
	if platform != PlatformAnthropic || subscriptionType == SubscriptionTypeSubscription {
		return fmt.Errorf("monthly tier pricing requires an Anthropic balance group")
	}
	for _, n := range []float64{p.Threshold, p.FirstRate, p.ExcessRate} {
		if math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 {
			return fmt.Errorf("monthly tier values must be finite and positive")
		}
	}
	if p.Threshold > 1e9 || p.FirstRate > 100 || p.ExcessRate > 100 || p.FirstRate > p.ExcessRate {
		return fmt.Errorf("invalid monthly tier threshold or rates")
	}
	return nil
}

// MonthlyTierMonth 固定北京时间自然月，不受服务器时区或浏览器时区影响。
func MonthlyTierMonth(at time.Time) string {
	return at.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01")
}

// MonthlyTierCharge 对跨门槛请求拆分，采用累计函数之差避免小额请求累计舍入误差。
func MonthlyTierCharge(before, amount decimal.Decimal, policy MonthlyTierPolicy) (decimal.Decimal, error) {
	if err := ValidateMonthlyTierPolicy(&policy, PlatformAnthropic, SubscriptionTypeStandard); err != nil {
		return decimal.Zero, err
	}
	if !policy.Enabled || before.IsNegative() || amount.IsNegative() {
		return decimal.Zero, fmt.Errorf("invalid monthly tier calculation")
	}
	threshold := decimal.NewFromFloat(policy.Threshold)
	first, excess := decimal.NewFromFloat(policy.FirstRate), decimal.NewFromFloat(policy.ExcessRate)
	total := func(v decimal.Decimal) decimal.Decimal {
		low := decimal.Min(v, threshold)
		return low.Mul(first).Add(v.Sub(low).Mul(excess)).Round(UsageBillingMonetaryScale)
	}
	return total(before.Add(amount)).Sub(total(before)), nil
}

// MonthlyTierBilling 固定请求结算使用的规则与月份；重试不能改写已结算结果。
type MonthlyTierBilling struct {
	GroupID           int64
	Month             string
	RawCost           float64
	Policy            MonthlyTierPolicy
	TrackKeyQuota     bool
	TrackKeyRateLimit bool
}

type MonthlyTierSettlement struct {
	GroupID     int64   `json:"group_id"`
	Month       string  `json:"month"`
	RawCost     float64 `json:"raw_cost"`
	ChargedCost float64 `json:"charged_cost"`
	RawBefore   float64 `json:"raw_before"`
	RawAfter    float64 `json:"raw_after"`
}
