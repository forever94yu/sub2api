//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type removedProviderMigrationFixture struct {
	platform       string
	accountID      int64
	groupID        int64
	apiKeyID       int64
	subscriptionID int64
	monitorID      int64
	templateID     int64
}

func requireMigration230ConstraintRejects(t *testing.T, tx *sql.Tx, query string, args ...any) {
	t.Helper()
	require.NoError(t, func() error {
		_, err := tx.Exec("SAVEPOINT migration_230_constraint_check")
		return err
	}())
	_, err := tx.Exec(query, args...)
	require.Error(t, err)
	require.NoError(t, func() error {
		_, rollbackErr := tx.Exec("ROLLBACK TO SAVEPOINT migration_230_constraint_check")
		return rollbackErr
	}())
}

func TestMigration230RemovesDedicatedProviderDataAndNarrowsConstraints(t *testing.T) {
	migrationSQL, err := dbmigrations.FS.ReadFile("230_remove_cn_provider_support.sql")
	require.NoError(t, err)

	tx := testTx(t)
	ctx := context.Background()
	prefix := "m230-" + uuid.NewString()[:8]

	// The integration database is migrated through 230 during package setup.
	// Drop 230's narrowed checks inside this rollback-only transaction so the
	// fixture can recreate a real 229-era data set before applying 230 again.
	for _, statement := range []string{
		`ALTER TABLE user_platform_quotas DROP CONSTRAINT user_platform_quotas_platform_check`,
		`ALTER TABLE channel_monitors DROP CONSTRAINT channel_monitors_provider_check`,
		`ALTER TABLE channel_monitor_request_templates DROP CONSTRAINT channel_monitor_request_templates_provider_check`,
		`ALTER TABLE composite_model_routes DROP CONSTRAINT composite_model_routes_target_platform_check`,
	} {
		_, err = tx.ExecContext(ctx, statement)
		require.NoError(t, err)
	}

	var userID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, role)
VALUES ($1, 'test-hash', 'admin')
RETURNING id
`, prefix+"@example.invalid").Scan(&userID))

	var genericAccountID, genericGroupID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO accounts (name, platform, type, credentials, extra)
VALUES ($1, 'openai', 'apikey',
        '{"api_key":"generic-secret","base_url":"https://api.deepseek.com/v1","model_mapping":{"deepseek-chat":"deepseek-chat"}}'::jsonb,
        '{"note":"generic OpenAI-compatible account"}'::jsonb)
RETURNING id
`, prefix+"-generic-account").Scan(&genericAccountID))
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO groups (name, platform)
VALUES ($1, 'openai')
RETURNING id
`, prefix+"-generic-group").Scan(&genericGroupID))

	fixtures := make([]removedProviderMigrationFixture, 0, 3)
	for _, platform := range []string{"kimi", "zhipu", "deepseek"} {
		fixture := removedProviderMigrationFixture{platform: platform}
		require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO accounts (name, platform, type, credentials, extra)
VALUES ($1, $2, 'apikey', '{"api_key":"secret"}'::jsonb, '{"provider_state":true}'::jsonb)
RETURNING id
`, prefix+"-account-"+platform, platform).Scan(&fixture.accountID))
		require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO groups (name, platform)
VALUES ($1, $2)
RETURNING id
`, prefix+"-group-"+platform, platform).Scan(&fixture.groupID))
		_, err = tx.ExecContext(ctx, `INSERT INTO account_groups (account_id, group_id) VALUES ($1, $2)`, fixture.accountID, fixture.groupID)
		require.NoError(t, err)

		apiKey := "sk-" + strings.ReplaceAll(uuid.NewString(), "-", "")
		require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO api_keys (user_id, key, name, group_id)
VALUES ($1, $2, $3, $4)
RETURNING id
`, userID, apiKey, prefix+"-key-"+platform, fixture.groupID).Scan(&fixture.apiKeyID))
		require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO user_subscriptions (user_id, group_id, starts_at, expires_at)
VALUES ($1, $2, now(), now() + interval '1 day')
RETURNING id
`, userID, fixture.groupID).Scan(&fixture.subscriptionID))

		_, err = tx.ExecContext(ctx, `
INSERT INTO usage_logs (user_id, api_key_id, account_id, group_id, subscription_id, request_id, model)
VALUES ($1, $2, $3, $4, $5, $6, $7)
`, userID, fixture.apiKeyID, fixture.accountID, fixture.groupID, fixture.subscriptionID, prefix+"-request-"+platform, platform+"-model")
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `INSERT INTO user_platform_quotas (user_id, platform) VALUES ($1, $2)`, userID, platform)
		require.NoError(t, err)

		require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO channel_monitor_request_templates (name, provider)
VALUES ($1, $2)
RETURNING id
`, prefix+"-template-"+platform, platform).Scan(&fixture.templateID))
		require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO channel_monitors
    (name, provider, endpoint, api_key_encrypted, primary_model, interval_seconds, created_by, account_id, template_id)
VALUES ($1, $2, 'https://example.invalid', 'encrypted', $3, 60, $4, $5, $6)
RETURNING id
`, prefix+"-monitor-"+platform, platform, platform+"-model", userID, fixture.accountID, fixture.templateID).Scan(&fixture.monitorID))
		_, err = tx.ExecContext(ctx, `
INSERT INTO channel_monitor_histories (monitor_id, model, status)
VALUES ($1, $2, 'operational')
`, fixture.monitorID, platform+"-model")
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `
INSERT INTO channel_monitor_daily_rollups (monitor_id, model, bucket_date)
VALUES ($1, $2, current_date)
`, fixture.monitorID, platform+"-model")
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `
INSERT INTO composite_model_routes (group_id, public_model, target_platform)
VALUES ($1, $2, $3)
`, fixture.groupID, platform+"-model", platform)
		require.NoError(t, err)

		_, err = tx.ExecContext(ctx, `
INSERT INTO content_moderation_logs (request_id, user_id, api_key_id, group_id, provider, model)
VALUES ($1, $2, $3, $4, $5, $6)
`, prefix+"-moderation-"+platform, userID, fixture.apiKeyID, fixture.groupID, platform, platform+"-model")
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `
INSERT INTO ops_error_logs (request_id, api_key_id, account_id, group_id, platform, model, error_phase, error_type)
VALUES ($1, $2, $3, $4, $5, $6, 'upstream', 'test')
`, prefix+"-error-"+platform, fixture.apiKeyID, fixture.accountID, fixture.groupID, platform, platform+"-model")
		require.NoError(t, err)

		fixtures = append(fixtures, fixture)
	}

	_, err = tx.ExecContext(ctx, `UPDATE accounts SET parent_account_id = $1, quota_dimension = 'spark' WHERE id = $2`, fixtures[0].accountID, genericAccountID)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO account_groups (account_id, group_id) VALUES ($1, $2)`, genericAccountID, fixtures[0].groupID)
	require.NoError(t, err)

	for _, key := range []string{
		"default_platform_quotas",
		"auth_source_default_email_platform_quotas",
		"auth_source_default_linuxdo_platform_quotas",
		"account_scheduling_thresholds",
	} {
		_, err = tx.ExecContext(ctx, `
INSERT INTO settings (key, value)
VALUES ($1, '{"openai":{"daily_limit_usd":1},"kimi":{"daily_limit_usd":2},"zhipu":{"daily_limit_usd":3},"deepseek":{"daily_limit_usd":4}}')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
`, key)
		require.NoError(t, err)
	}
	_, err = tx.ExecContext(ctx, `
UPDATE channel_monitor_v2_config
SET platforms = platforms || '[{"platform":"kimi","enabled":true,"models":[]},{"platform":"zhipu","enabled":true,"models":[]},{"platform":"deepseek","enabled":true,"models":[]}]'::jsonb
WHERE id = 1
`)
	require.NoError(t, err)
	var monitorConfigVersionBefore int64
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT version FROM channel_monitor_v2_config WHERE id = 1`).Scan(&monitorConfigVersionBefore))
	var ruleID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO error_passthrough_rules (name, platforms)
VALUES ($1, '["openai","kimi","zhipu","deepseek"]'::jsonb)
RETURNING id
`, prefix+"-rule").Scan(&ruleID))

	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err, "migration must be safe to execute again")

	for _, query := range []string{
		`SELECT count(*) FROM accounts WHERE platform IN ('kimi','zhipu','deepseek')`,
		`SELECT count(*) FROM groups WHERE platform IN ('kimi','zhipu','deepseek')`,
		`SELECT count(*) FROM user_platform_quotas WHERE platform IN ('kimi','zhipu','deepseek')`,
		`SELECT count(*) FROM channel_monitors WHERE provider IN ('kimi','zhipu','deepseek')`,
		`SELECT count(*) FROM channel_monitor_request_templates WHERE provider IN ('kimi','zhipu','deepseek')`,
		`SELECT count(*) FROM composite_model_routes WHERE target_platform IN ('kimi','zhipu','deepseek')`,
		`SELECT count(*) FROM content_moderation_logs WHERE provider IN ('kimi','zhipu','deepseek')`,
		`SELECT count(*) FROM ops_error_logs WHERE platform IN ('kimi','zhipu','deepseek')`,
	} {
		var count int
		require.NoError(t, tx.QueryRowContext(ctx, query).Scan(&count), "query=%s", query)
		require.Zero(t, count, "query=%s", query)
	}

	var genericCredentials string
	var genericParentID sql.NullInt64
	var genericQuotaDimension string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT credentials::text, parent_account_id, quota_dimension FROM accounts WHERE id = $1`, genericAccountID).Scan(&genericCredentials, &genericParentID, &genericQuotaDimension))
	require.JSONEq(t, `{"api_key":"generic-secret","base_url":"https://api.deepseek.com/v1","model_mapping":{"deepseek-chat":"deepseek-chat"}}`, genericCredentials)
	require.False(t, genericParentID.Valid)
	require.Equal(t, "global", genericQuotaDimension)

	for _, key := range []string{
		"default_platform_quotas",
		"auth_source_default_email_platform_quotas",
		"auth_source_default_linuxdo_platform_quotas",
		"account_scheduling_thresholds",
	} {
		var value string
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&value))
		require.JSONEq(t, `{"openai":{"daily_limit_usd":1}}`, value, "key=%s", key)
	}

	var removedMonitorV2Platforms int
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT count(*)
FROM channel_monitor_v2_config c,
     jsonb_array_elements(c.platforms) item
WHERE item->>'platform' IN ('kimi','zhipu','deepseek')
`).Scan(&removedMonitorV2Platforms))
	require.Zero(t, removedMonitorV2Platforms)
	var monitorConfigVersionAfter int64
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT version FROM channel_monitor_v2_config WHERE id = 1`).Scan(&monitorConfigVersionAfter))
	require.Equal(t, monitorConfigVersionBefore+1, monitorConfigVersionAfter)

	var rulePlatforms string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT platforms::text FROM error_passthrough_rules WHERE id = $1`, ruleID).Scan(&rulePlatforms))
	require.JSONEq(t, `["openai"]`, rulePlatforms)

	requireMigration230ConstraintRejects(t, tx,
		`INSERT INTO user_platform_quotas (user_id, platform) VALUES ($1, 'kimi')`, userID)
	requireMigration230ConstraintRejects(t, tx,
		`INSERT INTO channel_monitor_request_templates (name, provider) VALUES ($1, 'zhipu')`, prefix+"-rejected-template")
	requireMigration230ConstraintRejects(t, tx, `
INSERT INTO channel_monitors
    (name, provider, endpoint, api_key_encrypted, primary_model, interval_seconds, created_by)
VALUES ($1, 'deepseek', 'https://example.invalid', 'encrypted', 'model', 60, $2)
`, prefix+"-rejected-monitor", userID)
	requireMigration230ConstraintRejects(t, tx, `
INSERT INTO composite_model_routes (group_id, public_model, target_platform)
VALUES ($1, $2, 'kimi')
`, genericGroupID, fmt.Sprintf("%s-rejected-route", prefix))
}
