package service

import (
	"context"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"math"
	"testing"
	"time"
)

type monthlyTierRepoStub struct {
	UsageBillingRepository
	command *UsageBillingCommand
	result  *UsageBillingApplyResult
}

func (r *monthlyTierRepoStub) Apply(_ context.Context, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	r.command = cmd
	return r.result, nil
}

func TestMonthlyTierUsageLogMatchesTransactionalDebit(t *testing.T) {
	policy := &MonthlyTierPolicy{Enabled: true, Threshold: 500, FirstRate: 0.7, ExcessRate: 1.2}
	group := &Group{ID: 7, Platform: PlatformAnthropic, MonthlyTierPolicy: policy}
	p := &postUsageBillingParams{User: &User{ID: 1}, APIKey: &APIKey{ID: 2, GroupID: &group.ID, Group: group}, Account: &Account{ID: 3, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}, Cost: &CostBreakdown{TotalCost: 30, ActualCost: 21}}
	for _, applied := range []bool{true, false} {
		u := &UsageLog{CreatedAt: time.Date(2026, 11, 30, 16, 0, 0, 0, time.UTC), Model: "claude-opus-5-5"}
		r := &monthlyTierRepoStub{result: &UsageBillingApplyResult{Applied: applied, MonthlyTier: &MonthlyTierSettlement{RawCost: 30, ChargedCost: 31}}}
		ok, err := applyUsageBilling(context.Background(), "test-monthly", u, p, &billingDeps{}, r)
		require.NoError(t, err)
		require.Equal(t, applied, ok)
		require.Equal(t, "2026-12", r.command.MonthlyTier.Month)
		require.Equal(t, 30.0, r.command.MonthlyTier.RawCost)
		require.Equal(t, 31.0, u.ActualCost)
		require.InDelta(t, 31.0/30, u.RateMultiplier, 1e-12)
		require.Equal(t, 21.0, p.Cost.ActualCost, "caller cost snapshot must not mutate")
	}
}

func TestMonthlyTierValidationAndNoLegacyFallback(t *testing.T) {
	p := MonthlyTierPolicy{Enabled: true, Threshold: 500, FirstRate: 0.7, ExcessRate: 1.2}
	for _, bad := range []MonthlyTierPolicy{
		{Enabled: true, Threshold: math.NaN(), FirstRate: 0.7, ExcessRate: 1.2},
		{Enabled: true, Threshold: 500, FirstRate: 0, ExcessRate: 1.2},
		{Enabled: true, Threshold: 500, FirstRate: 2, ExcessRate: 1.2},
	} {
		require.Error(t, ValidateMonthlyTierPolicy(&bad, PlatformAnthropic, SubscriptionTypeStandard))
	}
	require.Error(t, ValidateMonthlyTierPolicy(&p, PlatformOpenAI, SubscriptionTypeStandard))
	require.Error(t, ValidateMonthlyTierPolicy(&p, PlatformAnthropic, SubscriptionTypeSubscription))
	g := &Group{ID: 1, Platform: PlatformAnthropic, MonthlyTierPolicy: &p}
	params := &postUsageBillingParams{User: &User{ID: 1}, APIKey: &APIKey{ID: 2, GroupID: &g.ID, Group: g}, Account: &Account{ID: 3, Platform: PlatformAnthropic}, Cost: &CostBreakdown{TotalCost: 10, ActualCost: 7}}
	_, err := applyUsageBilling(context.Background(), "monthly-no-repo", &UsageLog{}, params, &billingDeps{}, nil)
	require.ErrorContains(t, err, "atomic billing repository")
}

func TestMonthlyTierCharge(t *testing.T) {
	p := MonthlyTierPolicy{Enabled: true, Threshold: 500, FirstRate: 0.7, ExcessRate: 1.2}
	for _, tt := range []struct{ before, amount, want string }{
		{"0", "100", "70"}, {"0", "500", "350"}, {"0", "600", "470"},
		{"490", "30", "31"}, {"500", "30", "36"}, {"0", "1000", "950"},
		{"499.99999999", "0.00000002", "0.00000002"}, {"500", "0", "0"},
	} {
		t.Run(tt.before+"+"+tt.amount, func(t *testing.T) {
			got, err := MonthlyTierCharge(decimal.RequireFromString(tt.before), decimal.RequireFromString(tt.amount), p)
			require.NoError(t, err)
			require.True(t, got.Equal(decimal.RequireFromString(tt.want)), got.String())
		})
	}
	// 分多次结算与一次结算一致，不受单笔舍入影响。
	before, sum := decimal.Zero, decimal.Zero
	for i := 0; i < 100; i++ {
		amount := decimal.RequireFromString("0.000000015")
		v, err := MonthlyTierCharge(before, amount, p)
		require.NoError(t, err)
		sum = sum.Add(v)
		before = before.Add(amount)
	}
	want, err := MonthlyTierCharge(decimal.Zero, before, p)
	require.NoError(t, err)
	require.True(t, sum.Equal(want))
}

func TestMonthlyTierMonthShanghaiBoundary(t *testing.T) {
	for raw, want := range map[string]string{"2026-11-30T15:59:59Z": "2026-11", "2026-11-30T16:00:00Z": "2026-12", "2026-12-31T16:00:00Z": "2027-01"} {
		at, err := time.Parse(time.RFC3339, raw)
		require.NoError(t, err)
		require.Equal(t, want, MonthlyTierMonth(at))
	}
}
