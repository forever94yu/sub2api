-- Remove the dedicated Kimi, Zhipu GLM, and DeepSeek integrations.
-- Generic OpenAI-compatible accounts remain provider-neutral and are preserved.

DROP TABLE IF EXISTS pg_temp.sub2api_removed_accounts_230;
DROP TABLE IF EXISTS pg_temp.sub2api_removed_groups_230;
DROP TABLE IF EXISTS pg_temp.sub2api_removed_api_keys_230;
DROP TABLE IF EXISTS pg_temp.sub2api_removed_subscriptions_230;
DROP TABLE IF EXISTS pg_temp.sub2api_removed_monitors_230;
DROP TABLE IF EXISTS pg_temp.sub2api_removed_templates_230;
DROP TABLE IF EXISTS pg_temp.sub2api_removed_usage_logs_230;

CREATE TEMP TABLE sub2api_removed_accounts_230 ON COMMIT DROP AS
SELECT id
FROM accounts
WHERE platform IN ('kimi', 'zhipu', 'deepseek');

CREATE TEMP TABLE sub2api_removed_groups_230 ON COMMIT DROP AS
SELECT id
FROM groups
WHERE platform IN ('kimi', 'zhipu', 'deepseek');

CREATE TEMP TABLE sub2api_removed_api_keys_230 ON COMMIT DROP AS
SELECT id
FROM api_keys
WHERE group_id IN (SELECT id FROM sub2api_removed_groups_230);

CREATE TEMP TABLE sub2api_removed_subscriptions_230 ON COMMIT DROP AS
SELECT id
FROM user_subscriptions
WHERE group_id IN (SELECT id FROM sub2api_removed_groups_230);

CREATE TEMP TABLE sub2api_removed_monitors_230 ON COMMIT DROP AS
SELECT id
FROM channel_monitors
WHERE provider IN ('kimi', 'zhipu', 'deepseek')
   OR account_id IN (SELECT id FROM sub2api_removed_accounts_230);

CREATE TEMP TABLE sub2api_removed_templates_230 ON COMMIT DROP AS
SELECT id
FROM channel_monitor_request_templates
WHERE provider IN ('kimi', 'zhipu', 'deepseek');

CREATE TEMP TABLE sub2api_removed_usage_logs_230 ON COMMIT DROP AS
SELECT id
FROM usage_logs
WHERE account_id IN (SELECT id FROM sub2api_removed_accounts_230)
   OR group_id IN (SELECT id FROM sub2api_removed_groups_230)
   OR api_key_id IN (SELECT id FROM sub2api_removed_api_keys_230)
   OR subscription_id IN (SELECT id FROM sub2api_removed_subscriptions_230);

-- Preserve supported accounts/groups that happened to reference a removed row.
UPDATE accounts
SET parent_account_id = NULL,
    quota_dimension = 'global',
    updated_at = NOW()
WHERE parent_account_id IN (SELECT id FROM sub2api_removed_accounts_230)
  AND id NOT IN (SELECT id FROM sub2api_removed_accounts_230);

UPDATE groups
SET fallback_group_id = NULL,
    updated_at = NOW()
WHERE fallback_group_id IN (SELECT id FROM sub2api_removed_groups_230)
  AND id NOT IN (SELECT id FROM sub2api_removed_groups_230);

UPDATE groups
SET fallback_group_id_on_invalid_request = NULL,
    updated_at = NOW()
WHERE fallback_group_id_on_invalid_request IN (SELECT id FROM sub2api_removed_groups_230)
  AND id NOT IN (SELECT id FROM sub2api_removed_groups_230);

-- Purge provider-tagged runtime and historical rows, including tables whose
-- identifier columns intentionally have no foreign keys.
DELETE FROM batch_image_jobs
WHERE provider IN ('kimi', 'zhipu', 'deepseek')
   OR account_id IN (SELECT id FROM sub2api_removed_accounts_230)
   OR api_key_id IN (SELECT id FROM sub2api_removed_api_keys_230);

DELETE FROM channel_account_stats_pricing_intervals
WHERE pricing_id IN (
    SELECT id
    FROM channel_account_stats_model_pricing
    WHERE platform IN ('kimi', 'zhipu', 'deepseek')
);

DELETE FROM channel_account_stats_model_pricing
WHERE platform IN ('kimi', 'zhipu', 'deepseek');

DELETE FROM channel_model_pricing
WHERE platform IN ('kimi', 'zhipu', 'deepseek');

DELETE FROM channel_monitor_v2_error_metrics_1m
WHERE platform IN ('kimi', 'zhipu', 'deepseek')
   OR group_id IN (SELECT id FROM sub2api_removed_groups_230);
DELETE FROM channel_monitor_v2_error_metrics_rollup
WHERE platform IN ('kimi', 'zhipu', 'deepseek')
   OR group_id IN (SELECT id FROM sub2api_removed_groups_230);
DELETE FROM channel_monitor_v2_latency_histograms_1m
WHERE platform IN ('kimi', 'zhipu', 'deepseek')
   OR group_id IN (SELECT id FROM sub2api_removed_groups_230);
DELETE FROM channel_monitor_v2_latency_histograms_rollup
WHERE platform IN ('kimi', 'zhipu', 'deepseek')
   OR group_id IN (SELECT id FROM sub2api_removed_groups_230);
DELETE FROM channel_monitor_v2_metrics_1m
WHERE platform IN ('kimi', 'zhipu', 'deepseek')
   OR group_id IN (SELECT id FROM sub2api_removed_groups_230);
DELETE FROM channel_monitor_v2_metrics_rollup
WHERE platform IN ('kimi', 'zhipu', 'deepseek')
   OR group_id IN (SELECT id FROM sub2api_removed_groups_230);
DELETE FROM channel_monitor_v2_user_metrics_1m
WHERE platform IN ('kimi', 'zhipu', 'deepseek')
   OR group_id IN (SELECT id FROM sub2api_removed_groups_230);
DELETE FROM channel_monitor_v2_user_metrics_rollup
WHERE platform IN ('kimi', 'zhipu', 'deepseek')
   OR group_id IN (SELECT id FROM sub2api_removed_groups_230);

DELETE FROM content_moderation_logs
WHERE provider IN ('kimi', 'zhipu', 'deepseek')
   OR group_id IN (SELECT id FROM sub2api_removed_groups_230)
   OR api_key_id IN (SELECT id FROM sub2api_removed_api_keys_230);

DELETE FROM prompt_audit_events
WHERE provider IN ('kimi', 'zhipu', 'deepseek')
   OR group_id IN (SELECT id FROM sub2api_removed_groups_230)
   OR api_key_id IN (SELECT id FROM sub2api_removed_api_keys_230);
DELETE FROM prompt_audit_jobs
WHERE provider IN ('kimi', 'zhipu', 'deepseek')
   OR group_id IN (SELECT id FROM sub2api_removed_groups_230)
   OR api_key_id IN (SELECT id FROM sub2api_removed_api_keys_230);

DELETE FROM ops_error_logs
WHERE platform IN ('kimi', 'zhipu', 'deepseek')
   OR account_id IN (SELECT id FROM sub2api_removed_accounts_230)
   OR group_id IN (SELECT id FROM sub2api_removed_groups_230)
   OR api_key_id IN (SELECT id FROM sub2api_removed_api_keys_230);
DELETE FROM ops_system_logs
WHERE platform IN ('kimi', 'zhipu', 'deepseek')
   OR account_id IN (SELECT id FROM sub2api_removed_accounts_230)
   OR api_key_id IN (SELECT id FROM sub2api_removed_api_keys_230);
DELETE FROM ops_metrics_daily
WHERE platform IN ('kimi', 'zhipu', 'deepseek')
   OR group_id IN (SELECT id FROM sub2api_removed_groups_230);
DELETE FROM ops_metrics_hourly
WHERE platform IN ('kimi', 'zhipu', 'deepseek')
   OR group_id IN (SELECT id FROM sub2api_removed_groups_230);
DELETE FROM ops_system_metrics
WHERE platform IN ('kimi', 'zhipu', 'deepseek')
   OR group_id IN (SELECT id FROM sub2api_removed_groups_230);

DELETE FROM deleted_api_key_audits
WHERE api_key_id IN (SELECT id FROM sub2api_removed_api_keys_230);
DELETE FROM ops_ingress_reject_aggregates
WHERE api_key_id IN (SELECT id FROM sub2api_removed_api_keys_230);
DELETE FROM usage_billing_dedup
WHERE api_key_id IN (SELECT id FROM sub2api_removed_api_keys_230);
DELETE FROM usage_billing_dedup_archive
WHERE api_key_id IN (SELECT id FROM sub2api_removed_api_keys_230);

DELETE FROM billing_usage_entries
WHERE usage_log_id IN (SELECT id FROM sub2api_removed_usage_logs_230)
   OR api_key_id IN (SELECT id FROM sub2api_removed_api_keys_230)
   OR subscription_id IN (SELECT id FROM sub2api_removed_subscriptions_230);
DELETE FROM usage_logs
WHERE id IN (SELECT id FROM sub2api_removed_usage_logs_230);

DELETE FROM channel_monitor_histories
WHERE monitor_id IN (SELECT id FROM sub2api_removed_monitors_230);
DELETE FROM channel_monitor_daily_rollups
WHERE monitor_id IN (SELECT id FROM sub2api_removed_monitors_230);
DELETE FROM channel_monitors
WHERE id IN (SELECT id FROM sub2api_removed_monitors_230);

UPDATE channel_monitors
SET template_id = NULL,
    updated_at = NOW()
WHERE template_id IN (SELECT id FROM sub2api_removed_templates_230);
DELETE FROM channel_monitor_request_templates
WHERE id IN (SELECT id FROM sub2api_removed_templates_230);

DELETE FROM scheduled_test_plans
WHERE account_id IN (SELECT id FROM sub2api_removed_accounts_230);
DELETE FROM scheduler_outbox
WHERE account_id IN (SELECT id FROM sub2api_removed_accounts_230)
   OR group_id IN (SELECT id FROM sub2api_removed_groups_230);

DELETE FROM composite_model_routes
WHERE target_platform IN ('kimi', 'zhipu', 'deepseek')
   OR group_id IN (SELECT id FROM sub2api_removed_groups_230);
DELETE FROM user_platform_quotas
WHERE platform IN ('kimi', 'zhipu', 'deepseek');

DELETE FROM payment_orders
WHERE subscription_group_id IN (SELECT id FROM sub2api_removed_groups_230);
DELETE FROM subscription_plans
WHERE group_id IN (SELECT id FROM sub2api_removed_groups_230);
DELETE FROM redeem_codes
WHERE group_id IN (SELECT id FROM sub2api_removed_groups_230);
DELETE FROM usage_group_daily_rollups
WHERE group_id IN (SELECT id FROM sub2api_removed_groups_230);
DELETE FROM orphan_allowed_groups_audit
WHERE group_id IN (SELECT id FROM sub2api_removed_groups_230);
DELETE FROM groups_video_price_backup_220
WHERE platform IN ('kimi', 'zhipu', 'deepseek')
   OR group_id IN (SELECT id FROM sub2api_removed_groups_230);

DELETE FROM user_allowed_groups
WHERE group_id IN (SELECT id FROM sub2api_removed_groups_230);
DELETE FROM user_group_rate_multipliers
WHERE group_id IN (SELECT id FROM sub2api_removed_groups_230);
DELETE FROM channel_groups
WHERE group_id IN (SELECT id FROM sub2api_removed_groups_230);
DELETE FROM account_groups
WHERE account_id IN (SELECT id FROM sub2api_removed_accounts_230)
   OR group_id IN (SELECT id FROM sub2api_removed_groups_230);

DELETE FROM user_subscriptions
WHERE id IN (SELECT id FROM sub2api_removed_subscriptions_230);
DELETE FROM api_keys
WHERE id IN (SELECT id FROM sub2api_removed_api_keys_230);
DELETE FROM accounts
WHERE id IN (SELECT id FROM sub2api_removed_accounts_230);
DELETE FROM groups
WHERE id IN (SELECT id FROM sub2api_removed_groups_230);

-- Remove deleted IDs from selector arrays without turning a mixed supported
-- rule into a provider-wide rule.
DELETE FROM channel_account_stats_pricing_rules rule
WHERE (rule.account_ids && ARRAY(SELECT id FROM sub2api_removed_accounts_230)
       OR rule.group_ids && ARRAY(SELECT id FROM sub2api_removed_groups_230))
  AND cardinality(ARRAY(
        SELECT account_id
        FROM unnest(rule.account_ids) AS account_id
        WHERE account_id NOT IN (SELECT id FROM sub2api_removed_accounts_230)
      )) = 0
  AND cardinality(ARRAY(
        SELECT group_id
        FROM unnest(rule.group_ids) AS group_id
        WHERE group_id NOT IN (SELECT id FROM sub2api_removed_groups_230)
      )) = 0;

UPDATE channel_account_stats_pricing_rules rule
SET account_ids = ARRAY(
        SELECT account_id
        FROM unnest(rule.account_ids) AS account_id
        WHERE account_id NOT IN (SELECT id FROM sub2api_removed_accounts_230)
    ),
    group_ids = ARRAY(
        SELECT group_id
        FROM unnest(rule.group_ids) AS group_id
        WHERE group_id NOT IN (SELECT id FROM sub2api_removed_groups_230)
    ),
    updated_at = NOW()
WHERE rule.account_ids && ARRAY(SELECT id FROM sub2api_removed_accounts_230)
   OR rule.group_ids && ARRAY(SELECT id FROM sub2api_removed_groups_230);

UPDATE channel_monitor_v2_config config
SET group_ids = ARRAY(
        SELECT group_id
        FROM unnest(config.group_ids) AS group_id
        WHERE group_id NOT IN (SELECT id FROM sub2api_removed_groups_230)
    ),
    platforms = COALESCE((
        SELECT jsonb_agg(item ORDER BY ordinal)
        FROM jsonb_array_elements(config.platforms) WITH ORDINALITY AS entries(item, ordinal)
        WHERE item->>'platform' NOT IN ('kimi', 'zhipu', 'deepseek')
    ), '[]'::jsonb),
    version = version + 1,
    updated_at = NOW()
WHERE config.group_ids && ARRAY(SELECT id FROM sub2api_removed_groups_230)
   OR EXISTS (
        SELECT 1
        FROM jsonb_array_elements(config.platforms) AS item
        WHERE item->>'platform' IN ('kimi', 'zhipu', 'deepseek')
   );

UPDATE error_passthrough_rules rule
SET platforms = COALESCE((
        SELECT jsonb_agg(item ORDER BY ordinal)
        FROM jsonb_array_elements(rule.platforms) WITH ORDINALITY AS entries(item, ordinal)
        WHERE trim(both '"' from item::text) NOT IN ('kimi', 'zhipu', 'deepseek')
    ), '[]'::jsonb),
    updated_at = NOW()
WHERE jsonb_typeof(rule.platforms) = 'array';

-- Setting values are text and may contain user-supplied malformed JSON. Skip
-- malformed values so an unrelated setting cannot block this data migration.
DO $$
DECLARE
    setting_row RECORD;
    cleaned JSONB;
BEGIN
    FOR setting_row IN
        SELECT key, value
        FROM settings
        WHERE key IN ('default_platform_quotas', 'account_scheduling_thresholds')
           OR key LIKE 'auth_source_default_%_platform_quotas'
    LOOP
        BEGIN
            cleaned := setting_row.value::jsonb;
            IF jsonb_typeof(cleaned) = 'object' THEN
                cleaned := cleaned - 'kimi' - 'zhipu' - 'deepseek';
                UPDATE settings
                SET value = cleaned::text,
                    updated_at = NOW()
                WHERE key = setting_row.key;
            END IF;
        EXCEPTION WHEN invalid_text_representation THEN
            NULL;
        END;
    END LOOP;
END $$;

ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;
ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok')) NOT VALID;
ALTER TABLE user_platform_quotas
    VALIDATE CONSTRAINT user_platform_quotas_platform_check;

ALTER TABLE channel_monitors
    DROP CONSTRAINT IF EXISTS channel_monitors_provider_check;
ALTER TABLE channel_monitors
    ADD CONSTRAINT channel_monitors_provider_check
    CHECK (provider IN ('openai', 'anthropic', 'gemini', 'grok', 'antigravity')) NOT VALID;
ALTER TABLE channel_monitors
    VALIDATE CONSTRAINT channel_monitors_provider_check;

ALTER TABLE channel_monitor_request_templates
    DROP CONSTRAINT IF EXISTS channel_monitor_request_templates_provider_check;
ALTER TABLE channel_monitor_request_templates
    ADD CONSTRAINT channel_monitor_request_templates_provider_check
    CHECK (provider IN ('openai', 'anthropic', 'gemini', 'grok', 'antigravity')) NOT VALID;
ALTER TABLE channel_monitor_request_templates
    VALIDATE CONSTRAINT channel_monitor_request_templates_provider_check;

ALTER TABLE composite_model_routes
    DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;
ALTER TABLE composite_model_routes
    ADD CONSTRAINT composite_model_routes_target_platform_check
    CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok')) NOT VALID;
ALTER TABLE composite_model_routes
    VALIDATE CONSTRAINT composite_model_routes_target_platform_check;

DROP TABLE IF EXISTS pg_temp.sub2api_removed_usage_logs_230;
DROP TABLE IF EXISTS pg_temp.sub2api_removed_templates_230;
DROP TABLE IF EXISTS pg_temp.sub2api_removed_monitors_230;
DROP TABLE IF EXISTS pg_temp.sub2api_removed_subscriptions_230;
DROP TABLE IF EXISTS pg_temp.sub2api_removed_api_keys_230;
DROP TABLE IF EXISTS pg_temp.sub2api_removed_groups_230;
DROP TABLE IF EXISTS pg_temp.sub2api_removed_accounts_230;
