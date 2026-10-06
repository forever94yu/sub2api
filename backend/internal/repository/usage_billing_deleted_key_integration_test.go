//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestUsageBillingRepositoryApply_DeletedKeyStillSettles(t *testing.T) {
	for _, billingMode := range []string{"balance", "subscription"} {
		for _, counters := range []string{"quota", "rate", "both"} {
			t.Run(billingMode+"/"+counters, func(t *testing.T) {
				ctx := context.Background()
				client := testEntClient(t)
				repo := NewUsageBillingRepository(client, integrationDB)
				user := mustCreateUser(t, client, &service.User{
					Email: "deleted-key-" + uuid.NewString() + "@example.com", PasswordHash: "hash", Balance: 100,
				})
				group := mustCreateGroup(t, client, &service.Group{
					Name: "deleted-key-" + uuid.NewString(), Platform: service.PlatformOpenAI,
					SubscriptionType: service.SubscriptionTypeSubscription,
				})
				key := mustCreateApiKey(t, client, &service.APIKey{
					UserID: user.ID, GroupID: &group.ID, Key: "sk-deleted-key-" + uuid.NewString(),
					Name: "deleted-key", Quota: 50, RateLimit5h: 50,
				})
				account := mustCreateAccount(t, client, &service.Account{
					Name: "deleted-key-" + uuid.NewString(), Type: service.AccountTypeAPIKey,
					Extra: map[string]any{"quota_limit": 100.0},
				})
				cmd := &service.UsageBillingCommand{
					RequestID: uuid.NewString(), APIKeyID: key.ID, UserID: user.ID,
					AccountID: account.ID, AccountType: service.AccountTypeAPIKey, AccountQuotaCost: 2.5,
				}
				if billingMode == "subscription" {
					sub := mustCreateSubscription(t, client, &service.UserSubscription{UserID: user.ID, GroupID: group.ID})
					cmd.SubscriptionID = &sub.ID
					cmd.SubscriptionCost = 1.25
				} else {
					cmd.BalanceCost = 1.25
				}
				if counters != "rate" {
					cmd.APIKeyQuotaCost = 1.25
				}
				if counters != "quota" {
					cmd.APIKeyRateLimitCost = 1.25
				}
				require.NoError(t, NewAPIKeyRepository(client, integrationDB).DeleteWithAudit(ctx, key.ID))
				result, err := repo.Apply(ctx, cmd)
				require.NoError(t, err)
				require.True(t, result.Applied)
				require.False(t, result.APIKeyQuotaExhausted)
				duplicate, err := repo.Apply(ctx, cmd)
				require.NoError(t, err)
				require.False(t, duplicate.Applied)

				var balance, quotaUsed, usage5h, accountUsage float64
				require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
				if billingMode == "subscription" {
					require.InDelta(t, 100, balance, 0.000001)
					var daily, weekly, monthly float64
					require.NoError(t, integrationDB.QueryRowContext(ctx,
						"SELECT daily_usage_usd, weekly_usage_usd, monthly_usage_usd FROM user_subscriptions WHERE id = $1", *cmd.SubscriptionID).
						Scan(&daily, &weekly, &monthly))
					for _, usage := range []float64{daily, weekly, monthly} {
						require.InDelta(t, 1.25, usage, 0.000001)
					}
				} else {
					require.InDelta(t, 98.75, balance, 0.000001)
				}
				require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT quota_used, usage_5h FROM api_keys WHERE id = $1", key.ID).Scan(&quotaUsed, &usage5h))
				require.Zero(t, quotaUsed)
				require.Zero(t, usage5h)
				require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COALESCE((extra->>'quota_used')::numeric, 0) FROM accounts WHERE id = $1", account.ID).Scan(&accountUsage))
				require.InDelta(t, 2.5, accountUsage, 0.000001)
				var dedupCount int
				require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id = $1 AND api_key_id = $2", cmd.RequestID, key.ID).Scan(&dedupCount))
				require.Equal(t, 1, dedupCount)
			})
		}
	}
}
