//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func monthlyTierFixture(t *testing.T) (service.UsageBillingRepository, int64, int64, []int64) {
	t.Helper()
	c := testEntClient(t)
	u := mustCreateUser(t, c, &service.User{Email: uuid.NewString() + "@monthly.example.com", PasswordHash: "hash", Balance: 10000})
	g := mustCreateGroup(t, c, &service.Group{Name: "monthly-" + uuid.NewString(), Platform: service.PlatformAnthropic, SubscriptionType: service.SubscriptionTypeStandard})
	keys := []int64{}
	for i := 0; i < 2; i++ {
		k := mustCreateApiKey(t, c, &service.APIKey{UserID: u.ID, GroupID: &g.ID, Key: "sk-" + uuid.NewString(), Name: "monthly", Quota: 10000})
		keys = append(keys, k.ID)
	}
	return NewUsageBillingRepository(c, integrationDB), u.ID, g.ID, keys
}

func monthlyTierCommand(user, group, key int64, month string, raw float64) *service.UsageBillingCommand {
	return &service.UsageBillingCommand{RequestID: uuid.NewString(), UserID: user, APIKeyID: key, Model: "claude-opus-5-5", BalanceCost: raw * 0.7, APIKeyQuotaCost: raw * 0.7, APIKeyRateLimitCost: raw * 0.7,
		MonthlyTier: &service.MonthlyTierBilling{GroupID: group, Month: month, RawCost: raw, Policy: service.MonthlyTierPolicy{Enabled: true, Threshold: 500, FirstRate: 0.7, ExcessRate: 1.2, StartsAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}}}
}

func TestMonthlyTierConcurrentKeysAndIdempotency(t *testing.T) {
	repo, user, group, keys := monthlyTierFixture(t)
	ctx := context.Background()
	const n = 30
	cmds := make([]*service.UsageBillingCommand, n)
	for i := range cmds {
		cmds[i] = monthlyTierCommand(user, group, keys[i%2], "2026-11", 20)
	}
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for _, cmd := range cmds {
		wg.Add(1)
		go func(c *service.UsageBillingCommand) {
			defer wg.Done()
			r, e := repo.Apply(ctx, c)
			if e == nil && (r == nil || !r.Applied || r.MonthlyTier == nil) {
				e = fmt.Errorf("missing monthly settlement")
			}
			errs <- e
		}(cmd)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var raw, charged, balance, quota float64
	var count int
	require.NoError(t, integrationDB.QueryRow(`SELECT raw_cost,charged_cost FROM monthly_tier_usage WHERE user_id=$1 AND group_id=$2 AND month='2026-11'`, user, group).Scan(&raw, &charged))
	require.Equal(t, 600.0, raw)
	require.Equal(t, 470.0, charged)
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, user).Scan(&balance))
	require.Equal(t, 9530.0, balance)
	require.NoError(t, integrationDB.QueryRow(`SELECT SUM(quota_used) FROM api_keys WHERE user_id=$1`, user).Scan(&quota))
	require.Equal(t, 470.0, quota)
	// 同一命令对象和重建对象重试均不得再次扣费。
	for _, cmd := range cmds {
		r, err := repo.Apply(ctx, cmd)
		require.NoError(t, err)
		require.False(t, r.Applied)
		require.NotNil(t, r.MonthlyTier)
	}
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM monthly_tier_settlements WHERE user_id=$1`, user).Scan(&count))
	require.Equal(t, n, count)
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, user).Scan(&balance))
	require.Equal(t, 9530.0, balance)
}

func TestMonthlyTierCrossingPeriodAndIsolation(t *testing.T) {
	repo, user, group, keys := monthlyTierFixture(t)
	ctx := context.Background()
	first, err := repo.Apply(ctx, monthlyTierCommand(user, group, keys[0], "2026-11", 490))
	require.NoError(t, err)
	require.Equal(t, 343.0, first.MonthlyTier.ChargedCost)
	second, err := repo.Apply(ctx, monthlyTierCommand(user, group, keys[1], "2026-11", 30))
	require.NoError(t, err)
	require.Equal(t, 31.0, second.MonthlyTier.ChargedCost)
	next, err := repo.Apply(ctx, monthlyTierCommand(user, group, keys[1], "2026-12", 30))
	require.NoError(t, err)
	require.Equal(t, 21.0, next.MonthlyTier.ChargedCost)
	// 改规则不重算已经结算的历史费用，只应用于新请求。
	change := monthlyTierCommand(user, group, keys[0], "2026-11", 10)
	change.MonthlyTier.Policy.ExcessRate = 1.3
	changed, err := repo.Apply(ctx, change)
	require.NoError(t, err)
	require.Equal(t, 13.0, changed.MonthlyTier.ChargedCost)
	other := mustCreateUser(t, testEntClient(t), &service.User{Email: uuid.NewString() + "@monthly.example.com", PasswordHash: "hash", Balance: 100})
	k := mustCreateApiKey(t, testEntClient(t), &service.APIKey{UserID: other.ID, GroupID: &group, Key: "sk-" + uuid.NewString(), Name: "other"})
	x, err := repo.Apply(ctx, monthlyTierCommand(other.ID, group, k.ID, "2026-11", 30))
	require.NoError(t, err)
	require.Equal(t, 21.0, x.MonthlyTier.ChargedCost)
}

func TestMonthlyTierTransactionRollback(t *testing.T) {
	repo, user, group, keys := monthlyTierFixture(t)
	cmd := monthlyTierCommand(user, group, keys[0], "2026-11", 100)
	// 后续账号配额更新失败，必须回滚月度累计、账单和余额。
	cmd.AccountID = -1
	cmd.AccountType = service.AccountTypeAPIKey
	cmd.AccountQuotaCost = 10
	_, err := repo.Apply(context.Background(), cmd)
	require.Error(t, err)
	for _, table := range []string{"monthly_tier_usage", "monthly_tier_settlements"} {
		var count int
		require.NoError(t, integrationDB.QueryRow("SELECT count(*) FROM "+table+" WHERE user_id=$1", user).Scan(&count))
		require.Zero(t, count)
	}
	var balance float64
	require.NoError(t, integrationDB.QueryRow(`SELECT balance FROM users WHERE id=$1`, user).Scan(&balance))
	require.Equal(t, 10000.0, balance)
}

func TestMonthlyTierProgressDoesNotExposeOtherUsers(t *testing.T) {
	repo, user, group, keys := monthlyTierFixture(t)
	policy := `{"enabled":true,"threshold":500,"first_rate":0.7,"excess_rate":1.2,"starts_at":"2026-11-01T00:00:00Z"}`
	_, err := integrationDB.Exec(`UPDATE groups SET monthly_tier_policy=$1 WHERE id=$2`, policy, group)
	require.NoError(t, err)
	_, err = repo.Apply(context.Background(), monthlyTierCommand(user, group, keys[0], "2026-11", 100))
	require.NoError(t, err)
	svc := service.NewUsageService(nil, nil, testEntClient(t), nil)
	at := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	progress, err := svc.MonthlyTierProgressForUser(context.Background(), user, at)
	require.NoError(t, err)
	require.Len(t, progress, 1)
	require.Equal(t, 400.0, progress[0].Remaining)
	require.Equal(t, 70.0, progress[0].ChargedCost)
	require.Equal(t, "2026-12-01T00:00:00+08:00", progress[0].ResetsAt.Format(time.RFC3339))
	other, err := svc.MonthlyTierProgressForUser(context.Background(), user+1000000, at)
	require.NoError(t, err)
	require.Empty(t, other)
}

func TestMonthlyTierTinyChargeUpdatesKeyCounters(t *testing.T) {
	repo, user, group, keys := monthlyTierFixture(t)
	_, err := repo.Apply(context.Background(), monthlyTierCommand(user, group, keys[0], "2026-11", 500))
	require.NoError(t, err)
	cmd := monthlyTierCommand(user, group, keys[1], "2026-11", 0.0000000043)
	cmd.MonthlyTier.TrackKeyQuota = true
	cmd.MonthlyTier.TrackKeyRateLimit = true
	r, err := repo.Apply(context.Background(), cmd)
	require.NoError(t, err)
	require.Equal(t, 0.00000001, r.MonthlyTier.ChargedCost)
	var quota, window float64
	require.NoError(t, integrationDB.QueryRow(`SELECT quota_used,usage_5h FROM api_keys WHERE id=$1`, keys[1]).Scan(&quota, &window))
	require.Equal(t, r.MonthlyTier.ChargedCost, quota)
	require.Equal(t, quota, window)
}
