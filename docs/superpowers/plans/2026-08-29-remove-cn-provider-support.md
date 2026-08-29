# Remove Kimi, Zhipu GLM, and DeepSeek Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove every active dedicated Kimi/Moonshot/K3, Zhipu GLM, and DeepSeek integration and all corresponding database data, then publish verified release `v0.1.184` and matching Docker Hub images.

**Architecture:** Remove provider identifiers at the domain boundary first, then delete provider-specific runtime services and compatibility branches so unsupported values cannot enter scheduling or forwarding. A forward-only PostgreSQL migration physically purges all related rows before narrowing constraints; the frontend is reduced to the five remaining concrete platforms while the generic OpenAI-compatible path stays provider-neutral.

**Tech Stack:** Go 1.26.6, Gin, Ent, PostgreSQL, Redis, Vue 3, TypeScript 5.6, Vitest, pnpm 9, Docker Buildx, GitHub Actions, GoReleaser.

**Spec:** `docs/superpowers/specs/2026-08-29-remove-cn-provider-support-design.md`

## Global Constraints

- Keep generic OpenAI-compatible API-key accounts provider-neutral; do not blacklist provider hostnames or model names.
- Physically remove all matching runtime and historical data; the target installation has never configured or used these providers.
- Do not edit or delete migrations `224`, `226`, or `227`; migration checksums are immutable.
- Do not edit `openspec/**/source-freeze/**` or generated build snapshots.
- Use `backend/migrations/230_remove_cn_provider_support.sql` for forward-only cleanup and constraint narrowing.
- Use TDD: every behavior change starts with a test that fails for the expected old-support reason.
- Use proxy `http://127.0.0.1:10808` only when dependency or image downloads require it.
- Publish release `v0.1.184` and Docker Hub tags `0.1.184`, `latest`, `0.1`, and `0` for Linux AMD64 and ARM64.

---

### Task 1: Lock the Reduced Backend Platform Contract

**Files:**
- Modify: `backend/internal/handler/admin/group_handler_platform_test.go`
- Modify: `backend/internal/handler/admin/channel_handler_test.go`
- Modify: `backend/internal/handler/composite_platform_test.go`
- Modify: `backend/internal/handler/openai_gateway_cn_dispatch_test.go`
- Modify: `backend/internal/server/api_contract_test.go`
- Modify: `backend/internal/domain/constants.go`
- Modify: `backend/internal/service/domain_constants.go`
- Modify: `backend/internal/service/account.go`
- Modify: `backend/internal/service/account_service.go`
- Modify: `backend/internal/service/composite_platform.go`
- Modify: `backend/internal/service/composite_model_route.go`
- Modify: `backend/internal/handler/admin/group_handler.go`
- Modify: `backend/internal/handler/admin/channel_monitor_handler.go`
- Modify: `backend/internal/handler/admin/channel_handler.go`
- Modify: `backend/internal/handler/gateway_handler.go`
- Modify: `backend/internal/handler/openai_gateway_handler.go`
- Modify: `backend/internal/server/routes/gateway.go`

**Interfaces:**
- Consumes: Existing `PlatformAnthropic`, `PlatformOpenAI`, `PlatformGemini`, `PlatformAntigravity`, `PlatformGrok`, and `PlatformComposite` constants.
- Produces: A backend contract in which `kimi`, `zhipu`, and `deepseek` are unsupported platform strings and composite inference only returns supported concrete platforms.

- [ ] **Step 1: Reverse the platform-binding regression tests**

Replace the old allow-list expectations with the reduced set and explicit removed-provider rejection:

```go
func TestGroupPlatformBinding_AllowedPlatforms(t *testing.T) {
	allowed := []string{"anthropic", "openai", "gemini", "antigravity", "grok", "composite"}
	for _, platform := range allowed {
		t.Run("create_"+platform, func(t *testing.T) {
			var req CreateGroupRequest
			err := bindGroupPlatformJSON(t, &req,
				fmt.Sprintf(`{"name":"g","platform":%q}`, platform))
			require.NoError(t, err)
			require.Equal(t, platform, req.Platform)
		})
		t.Run("update_"+platform, func(t *testing.T) {
			var req UpdateGroupRequest
			err := bindGroupPlatformJSON(t, &req,
				fmt.Sprintf(`{"platform":%q}`, platform))
			require.NoError(t, err)
			require.Equal(t, platform, req.Platform)
		})
	}
}

func TestGroupPlatformBinding_RejectsRemovedPlatforms(t *testing.T) {
	for _, platform := range []string{"kimi", "zhipu", "deepseek"} {
		var createReq CreateGroupRequest
		require.Error(t, bindGroupPlatformJSON(t, &createReq,
			fmt.Sprintf(`{"name":"g","platform":%q}`, platform)))

		var routeReq CompositeRouteRequest
		require.Error(t, bindGroupPlatformJSON(t, &routeReq,
			fmt.Sprintf(`{"public_model":"m","target_platform":%q}`, platform)))
	}
}
```

Update composite and gateway tests so only Grok remains exempt from the OpenAI messages-dispatch gate and removed model prefixes no longer infer a concrete target.

- [ ] **Step 2: Run the focused tests and verify RED**

Run:

```powershell
Set-Location backend
go test -tags unit ./internal/handler/admin ./internal/handler ./internal/server -run 'TestGroupPlatformBinding|TestComposite|TestAllowOpenAICompatibleMessagesDispatch|TestAPIContracts' -count=1
```

Expected: FAIL because request validators, composite inference, gateway platform lists, and routes still include the three providers.

- [ ] **Step 3: Remove platform constants and public acceptance paths**

Delete the three constants and all `IsKimi`, `IsZhipu`, `IsDeepseek`, `IsCNProvider`, default endpoint, provider alias, provider prefix, and allowed-platform branches. Reduce handler `oneof` tags to:

```go
binding:"omitempty,oneof=anthropic openai gemini antigravity grok composite"
```

For concrete composite targets use:

```go
binding:"required,oneof=anthropic openai gemini antigravity grok"
```

Keep account creation fail-closed through the existing `unsupported platform: %s` error in `testAccountCredentials`. Remove the three providers from gateway route selection, model-list aggregation, channel vendor mapping, and messages-dispatch exemptions.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run the Step 2 command again.

Expected: PASS; no test can construct a supported group or route for the removed providers.

- [ ] **Step 5: Commit the backend contract change**

```powershell
git add backend/internal/domain backend/internal/service/domain_constants.go backend/internal/service/account.go backend/internal/service/account_service.go backend/internal/service/composite_platform.go backend/internal/service/composite_model_route.go backend/internal/handler backend/internal/server
git commit -m "refactor: remove CN provider platform contracts"
```

### Task 2: Physically Purge Provider Data and Narrow Database Constraints

**Files:**
- Create: `backend/migrations/230_remove_cn_provider_support.sql`
- Create: `backend/internal/repository/remove_cn_provider_support_migration_integration_test.go`
- Modify: `backend/ent/schema/user_platform_quota.go`
- Modify: `backend/ent/schema/channel_monitor.go`
- Modify: `backend/ent/schema/channel_monitor_request_template.go`
- Regenerate: `backend/ent/migrate/schema.go`
- Regenerate: `backend/ent/channelmonitor/channelmonitor.go`
- Regenerate: `backend/ent/channelmonitorrequesttemplate/channelmonitorrequesttemplate.go`

**Interfaces:**
- Consumes: The embedded migration filesystem `migrations.FS`, repository integration `testTx(t)`, and the existing PostgreSQL schema after migration 229.
- Produces: Migration 230, reduced Ent validators, and database constraints allowing only supported platform values.

- [ ] **Step 1: Write the migration integration test**

Create an integration test that reads migration 230, seeds all three removed providers, executes the SQL inside `testTx(t)`, and verifies physical deletion plus constraint rejection:

```go
//go:build integration

package repository

func TestMigration230RemovesCNProviderDataAndNarrowsConstraints(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	migrationSQL, err := dbmigrations.FS.ReadFile("230_remove_cn_provider_support.sql")
	require.NoError(t, err)
	var userID int64
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&userID))

	for _, platform := range []string{"kimi", "zhipu", "deepseek"} {
		var accountID, groupID, apiKeyID, subscriptionID, monitorID, templateID int64
		require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO accounts (name, platform, type, credentials) VALUES ($1, $2, 'apikey', '{"api_key":"secret"}'::jsonb) RETURNING id`, "removed-"+platform, platform).Scan(&accountID))
		require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO groups (name, platform) VALUES ($1, $2) RETURNING id`, "removed-"+platform, platform).Scan(&groupID))
		_, err = tx.ExecContext(ctx, `INSERT INTO account_groups (account_id, group_id) VALUES ($1, $2)`, accountID, groupID)
		require.NoError(t, err)
		require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO api_keys (user_id, key, name, group_id) VALUES ($1, $2, $3, $4) RETURNING id`, userID, "sk-removed-"+platform, "removed-"+platform, groupID).Scan(&apiKeyID))
		require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO user_subscriptions (user_id, group_id, starts_at, expires_at) VALUES ($1, $2, now(), now() + interval '1 day') RETURNING id`, userID, groupID).Scan(&subscriptionID))
		_, err = tx.ExecContext(ctx, `INSERT INTO usage_logs (user_id, api_key_id, account_id, group_id, subscription_id, request_id, model) VALUES ($1, $2, $3, $4, $5, $6, $7)`, userID, apiKeyID, accountID, groupID, subscriptionID, "removed-"+platform, platform+"-model")
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `INSERT INTO user_platform_quotas (user_id, platform) VALUES ($1, $2)`, userID, platform)
		require.NoError(t, err)
		require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO channel_monitor_request_templates (name, provider) VALUES ($1, $2) RETURNING id`, "removed-"+platform, platform).Scan(&templateID))
		require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO channel_monitors (name, provider, endpoint, api_key_encrypted, primary_model, interval_seconds, created_by, account_id, template_id) VALUES ($1, $2, 'https://example.invalid', 'encrypted', $3, 60, $4, $5, $6) RETURNING id`, "removed-"+platform, platform, platform+"-model", userID, accountID, templateID).Scan(&monitorID))
		_, err = tx.ExecContext(ctx, `INSERT INTO channel_monitor_histories (monitor_id, model, status) VALUES ($1, $2, 'operational')`, monitorID, platform+"-model")
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `INSERT INTO channel_monitor_daily_rollups (monitor_id, model, bucket_date) VALUES ($1, $2, current_date)`, monitorID, platform+"-model")
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `INSERT INTO composite_model_routes (group_id, public_model, target_platform) VALUES ($1, $2, $3)`, groupID, platform+"-model", platform)
		require.NoError(t, err)
	}

	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)

	for _, query := range []string{
		`SELECT count(*) FROM accounts WHERE platform IN ('kimi','zhipu','deepseek')`,
		`SELECT count(*) FROM groups WHERE platform IN ('kimi','zhipu','deepseek')`,
		`SELECT count(*) FROM user_platform_quotas WHERE platform IN ('kimi','zhipu','deepseek')`,
		`SELECT count(*) FROM channel_monitors WHERE provider IN ('kimi','zhipu','deepseek')`,
		`SELECT count(*) FROM channel_monitor_request_templates WHERE provider IN ('kimi','zhipu','deepseek')`,
		`SELECT count(*) FROM composite_model_routes WHERE target_platform IN ('kimi','zhipu','deepseek')`,
		`SELECT count(*) FROM usage_logs WHERE model ~* '(kimi|zhipu|deepseek)'`,
	} {
		var count int
		require.NoError(t, tx.QueryRowContext(ctx, query).Scan(&count))
		require.Zero(t, count, "query=%s", query)
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO user_platform_quotas (user_id, platform) SELECT id, 'kimi' FROM users ORDER BY id LIMIT 1`)
	require.Error(t, err)
}
```

- [ ] **Step 2: Run the migration test and verify RED**

Run:

```powershell
Set-Location backend
go test -tags integration ./internal/repository -run TestMigration230RemovesCNProviderDataAndNarrowsConstraints -count=1
```

Expected: FAIL because `230_remove_cn_provider_support.sql` does not exist.

- [ ] **Step 3: Implement the forward-only migration**

Use transaction-local temporary ID tables and foreign-key-safe deletion. The migration must follow this shape:

```sql
CREATE TEMP TABLE removed_cn_accounts ON COMMIT DROP AS
SELECT id FROM accounts WHERE platform IN ('kimi', 'zhipu', 'deepseek');

CREATE TEMP TABLE removed_cn_groups ON COMMIT DROP AS
SELECT id FROM groups WHERE platform IN ('kimi', 'zhipu', 'deepseek');

DELETE FROM usage_logs
WHERE account_id IN (SELECT id FROM removed_cn_accounts)
   OR group_id IN (SELECT id FROM removed_cn_groups)
   OR api_key_id IN (SELECT id FROM api_keys WHERE group_id IN (SELECT id FROM removed_cn_groups))
   OR subscription_id IN (SELECT id FROM user_subscriptions WHERE group_id IN (SELECT id FROM removed_cn_groups));
DELETE FROM channel_monitor_histories WHERE monitor_id IN (SELECT id FROM channel_monitors WHERE provider IN ('kimi', 'zhipu', 'deepseek'));
DELETE FROM channel_monitor_daily_rollups WHERE monitor_id IN (SELECT id FROM channel_monitors WHERE provider IN ('kimi', 'zhipu', 'deepseek'));
DELETE FROM channel_monitors WHERE provider IN ('kimi', 'zhipu', 'deepseek') OR account_id IN (SELECT id FROM removed_cn_accounts);
DELETE FROM channel_monitor_request_templates WHERE provider IN ('kimi', 'zhipu', 'deepseek');
DELETE FROM composite_model_routes WHERE target_platform IN ('kimi', 'zhipu', 'deepseek') OR group_id IN (SELECT id FROM removed_cn_groups);
DELETE FROM user_platform_quotas WHERE platform IN ('kimi', 'zhipu', 'deepseek');
DELETE FROM account_groups WHERE account_id IN (SELECT id FROM removed_cn_accounts) OR group_id IN (SELECT id FROM removed_cn_groups);
DELETE FROM user_subscriptions WHERE group_id IN (SELECT id FROM removed_cn_groups);
DELETE FROM api_keys WHERE group_id IN (SELECT id FROM removed_cn_groups);
DELETE FROM accounts WHERE id IN (SELECT id FROM removed_cn_accounts);
DELETE FROM groups WHERE id IN (SELECT id FROM removed_cn_groups);

ALTER TABLE user_platform_quotas DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;
ALTER TABLE user_platform_quotas ADD CONSTRAINT user_platform_quotas_platform_check
CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok'));

ALTER TABLE channel_monitors DROP CONSTRAINT IF EXISTS channel_monitors_provider_check;
ALTER TABLE channel_monitors ADD CONSTRAINT channel_monitors_provider_check
CHECK (provider IN ('openai', 'anthropic', 'gemini', 'grok', 'antigravity'));

ALTER TABLE channel_monitor_request_templates DROP CONSTRAINT IF EXISTS channel_monitor_request_templates_provider_check;
ALTER TABLE channel_monitor_request_templates ADD CONSTRAINT channel_monitor_request_templates_provider_check
CHECK (provider IN ('openai', 'anthropic', 'gemini', 'grok', 'antigravity'));

ALTER TABLE composite_model_routes DROP CONSTRAINT IF EXISTS composite_model_routes_target_platform_check;
ALTER TABLE composite_model_routes ADD CONSTRAINT composite_model_routes_target_platform_check
CHECK (target_platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok'));
```

Remove matching keys from `settings.default_platform_quotas` and scrub provider-specific model mappings/whitelists from JSON columns without deleting neutral OpenAI-compatible custom endpoint data.

Before finalizing the delete list, run this read-only foreign-key inventory and add a delete or nulling statement for every returned referencing table so migration 230 cannot fail on a populated installation:

```sql
SELECT conrelid::regclass AS child_table,
       confrelid::regclass AS parent_table,
       pg_get_constraintdef(oid) AS definition
FROM pg_constraint
WHERE contype = 'f'
  AND confrelid IN ('accounts'::regclass, 'groups'::regclass,
                    'api_keys'::regclass, 'user_subscriptions'::regclass,
                    'channel_monitors'::regclass,
                    'channel_monitor_request_templates'::regclass)
ORDER BY parent_table::text, child_table::text;
```

- [ ] **Step 4: Reduce Ent validators and regenerate Ent code**

Remove the three values from schema validation, then run:

```powershell
Set-Location backend
go generate ./ent
```

Expected: generated enum/validation output contains only the remaining providers.

- [ ] **Step 5: Run migration and schema tests and verify GREEN**

Run:

```powershell
Set-Location backend
go test -tags integration ./internal/repository -run TestMigration230RemovesCNProviderDataAndNarrowsConstraints -count=1
go test ./migrations ./ent/... -count=1
```

Expected: PASS, including a second execution of the migration SQL inside a fresh transaction with no matching rows.

- [ ] **Step 6: Commit the database change**

```powershell
git add backend/migrations/230_remove_cn_provider_support.sql backend/internal/repository/remove_cn_provider_support_migration_integration_test.go backend/ent
git commit -m "feat: purge removed provider data"
```

### Task 3: Delete Provider-Specific Services, Routes, Configuration, and Wiring

**Files:**
- Delete: `backend/internal/handler/admin/cn_provider_handler.go`
- Delete: `backend/internal/service/cn_provider_balance_service.go`
- Delete: `backend/internal/service/cn_provider_balance_check_service.go`
- Delete: `backend/internal/service/cn_provider_quota_service.go`
- Delete: `backend/internal/service/cn_provider_probe_url.go`
- Delete: `backend/internal/service/ratelimit_cn_providers.go`
- Delete: `backend/internal/service/cn_provider_balance_check_service_test.go`
- Delete: `backend/internal/service/cn_provider_foraccount_test.go`
- Delete: `backend/internal/service/cn_provider_probe_url_test.go`
- Delete: `backend/internal/service/cn_providers_test.go`
- Modify: `backend/internal/config/config.go`
- Modify: `backend/internal/config/config_test.go`
- Modify: `backend/internal/service/ratelimit_service.go`
- Modify: `backend/internal/service/channel_monitor_quota_fetcher.go`
- Modify: `backend/internal/service/channel_monitor_checker.go`
- Modify: `backend/internal/service/channel_monitor_const.go`
- Modify: `backend/internal/service/channel_monitor_service.go`
- Modify: `backend/internal/service/channel_monitor_validate.go`
- Modify: `backend/internal/service/account_scheduling_threshold_eval.go`
- Modify: `backend/internal/domain/channel_monitor_quota.go`
- Modify: `backend/internal/handler/wire.go`
- Modify: `backend/internal/service/wire.go`
- Modify: `backend/internal/server/routes/admin.go`
- Modify: `backend/cmd/server/wire_gen.go`
- Modify: `deploy/config.example.yaml`

**Interfaces:**
- Consumes: Remaining OpenAI, Anthropic, Gemini, Antigravity, and Grok quota/monitor services.
- Produces: A dependency graph and runtime lifecycle with no CN-provider services or routes.

- [ ] **Step 1: Add route and configuration absence assertions**

Add source/API contract assertions that `/api/v1/admin/cn-providers` is not registered and `GatewayConfig` no longer exposes a `CNProviders` field. Update monitor-provider test tables to the five remaining values.

```go
func TestRemovedCNProviderRoutesAreNotRegistered(t *testing.T) {
	routes := registeredRouteSignatures(t)
	require.NotContains(t, routes, "GET /api/v1/admin/cn-providers/accounts/:id/quota")
	require.NotContains(t, routes, "GET /api/v1/admin/cn-providers/accounts/:id/balance")
}
```

- [ ] **Step 2: Run focused tests and verify RED**

Run:

```powershell
Set-Location backend
go test -tags unit ./internal/config ./internal/server ./internal/service -run 'CNProvider|ChannelMonitor|SchedulingThreshold|RateLimit' -count=1
```

Expected: FAIL because routes, config, services, monitor providers, and scheduling branches still exist.

- [ ] **Step 3: Remove services and dependency wiring**

Delete the provider-only files. Remove `registerCNProviderRoutes`, handler fields/providers, service provider-set entries, background service construction/start/stop hooks, quota fetcher dependencies, and CN-specific rate-limit recovery. Regenerate Wire:

```powershell
Set-Location backend
go generate ./cmd/server
```

Remove `gateway.cn_providers` and Kimi/Moonshot hosts from `deploy/config.example.yaml` and configuration defaults/validation.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run the Step 2 command, then:

```powershell
Set-Location backend
go test ./cmd/server ./internal/handler/... ./internal/server/... -count=1
```

Expected: PASS with no missing Wire provider or lifecycle dependency.

- [ ] **Step 5: Commit the runtime deletion**

```powershell
git add -A backend/internal/config backend/internal/domain backend/internal/handler backend/internal/server backend/internal/service backend/cmd/server/wire_gen.go deploy/config.example.yaml
git commit -m "refactor: delete CN provider runtime services"
```

### Task 4: Remove Model, Gateway, Billing, and Compatibility Special Cases

**Files:**
- Modify: `backend/internal/service/billing_service.go`
- Modify: `backend/internal/service/billing_service_test.go`
- Modify: `backend/internal/service/openai_model_mapping.go`
- Modify: `backend/internal/service/openai_oauth_model_support_test.go`
- Modify: `backend/internal/service/openai_gateway_scheduling.go`
- Modify: `backend/internal/service/openai_gateway_model_availability.go`
- Modify: `backend/internal/service/openai_gateway_request_body.go`
- Modify: `backend/internal/service/openai_gateway_passthrough.go`
- Modify: `backend/internal/service/openai_gateway_forward.go`
- Modify: `backend/internal/service/openai_gateway_chat_completions.go`
- Modify: `backend/internal/service/openai_gateway_chat_completions_raw.go`
- Modify: `backend/internal/service/openai_gateway_chat_completions_anthropic_native.go`
- Modify: `backend/internal/service/openai_gateway_messages_anthropic_native.go`
- Modify: `backend/internal/service/openai_gateway_messages_chat_fallback.go`
- Modify: `backend/internal/service/openai_gateway_cc_pipeline.go`
- Modify: `backend/internal/service/openai_gateway_responses_anthropic_native.go`
- Modify: `backend/internal/service/openai_gateway_usage.go`
- Modify: `backend/internal/service/openai_gateway_count_tokens.go`
- Modify: `backend/internal/service/openai_apikey_responses_probe.go`
- Modify: `backend/internal/service/gateway_anthropic_passthrough.go`
- Modify: `backend/internal/service/gateway_forward.go`
- Modify: `backend/internal/service/gateway_forward_as_chat_completions.go`
- Modify: `backend/internal/service/gateway_request.go`
- Modify: `backend/internal/service/gateway_service.go`
- Modify: `backend/internal/service/gateway_upstream_response.go`
- Modify: `backend/internal/service/gateway_websearch_block_filter.go`
- Modify: `backend/internal/service/thinking_protocol.go`
- Modify: `backend/internal/service/upstream_models.go`
- Modify: `backend/internal/service/upstream_billing_probe.go`
- Modify: `backend/internal/pkg/openai_compat/upstream_capability.go`
- Modify: `backend/internal/pkg/apicompat/chatcompletions_anthropic_bridge.go`
- Modify: `backend/internal/pkg/apicompat/chatcompletions_responses_bridge.go`
- Modify: `backend/internal/service/openai_gateway_cn_fixes_test.go`
- Modify: `backend/internal/service/openai_gateway_chat_completions_raw_test.go`
- Modify: `backend/internal/service/openai_gateway_responses_chat_fallback_test.go`
- Modify: `backend/internal/service/gateway_forward_as_chat_completions_test.go`
- Modify: `backend/internal/service/gateway_forward_as_responses_test.go`
- Modify: `backend/internal/service/gateway_request_test.go`
- Modify: `backend/internal/service/gateway_streaming_test.go`
- Modify: `backend/internal/service/gateway_websearch_block_filter_test.go`
- Modify: `backend/internal/service/thinking_protocol_test.go`
- Modify: `backend/internal/service/thinking_protocol_filter_integration_test.go`
- Modify: `backend/internal/service/upstream_billing_probe_multiplatform_test.go`
- Modify: `backend/internal/service/upstream_models_test.go`
- Modify: `backend/internal/pkg/apicompat/chatcompletions_anthropic_bridge_test.go`
- Modify: `backend/internal/pkg/apicompat/chatcompletions_responses_test.go`
- Modify: `backend/internal/pkg/apicompat/chatcompletions_responses_stream_lifecycle_test.go`
- Modify: `backend/internal/pkg/apicompat/responses_to_anthropic_cc_chain_test.go`
- Modify: `backend/internal/pkg/apicompat/responses_to_anthropic_parallel_tool_test.go`
- Modify: `backend/resources/model-pricing/model_prices_and_context_window.json`

**Interfaces:**
- Consumes: Generic OpenAI-compatible and Anthropic-compatible bridge behavior.
- Produces: No dedicated model-family recognition, pricing fallback, request mutation, or response exception for the removed providers.

- [ ] **Step 1: Write negative model and billing tests**

Replace provider-price test cases with assertions that dedicated fallback matching is absent while neutral unknown-model behavior remains:

```go
func TestRemovedProviderModelsHaveNoDedicatedFallbackPricing(t *testing.T) {
	svc := newTestBillingService()
	for _, model := range []string{"kimi-k3", "k3", "glm-5.2", "deepseek-v4"} {
		pricing, err := svc.GetModelPricing(model)
		require.Error(t, err, "model=%s", model)
		require.Nil(t, pricing, "model=%s", model)
	}
}
```

Update OAuth support and composite inference tests so provider model families receive no dedicated allow/deny handling. Preserve generic explicit account model mappings with neutral identifiers such as `vendor/custom-model`.

- [ ] **Step 2: Run focused tests and verify RED**

Run:

```powershell
Set-Location backend
go test ./internal/service ./internal/pkg/openai_compat ./internal/pkg/apicompat -run 'RemovedProvider|ModelPricing|ModelSupport|Thinking|Websearch|ChatCompletions|Responses' -count=1
```

Expected: FAIL because fallback prices, prefixes, K3 aliases, gateway branches, and provider-named bridge behavior remain.

- [ ] **Step 3: Remove dedicated model and protocol branches**

Delete fallback-pricing entries and ordering rules for Kimi/K3, GLM, and DeepSeek. Remove their model prefixes and exact K3 aliases from OpenAI OAuth model support logic, composite model inference, thinking protocol exceptions, request shaping, token counting, web-search filtering, and upstream probe/response handling.

Remove DeepSeek entries from `model_prices_and_context_window.json`. Replace provider-named generic bridge fixtures/comments with neutral model IDs without weakening reasoning-content, tool-call, stream lifecycle, or Anthropic conversion coverage.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run the Step 2 command.

Expected: PASS; generic bridge regressions remain green and no dedicated provider model receives special pricing or routing.

- [ ] **Step 5: Commit model and gateway cleanup**

```powershell
git add backend/internal/service backend/internal/pkg backend/resources/model-pricing/model_prices_and_context_window.json
git commit -m "refactor: remove CN provider model handling"
```

### Task 5: Remove Frontend Account and Provider API Surfaces

**Files:**
- Modify: `frontend/src/types/index.ts`
- Modify: `frontend/src/api/admin/index.ts`
- Delete: `frontend/src/api/admin/cnProviders.ts`
- Modify: `frontend/src/api/admin/settings.ts`
- Modify: `frontend/src/components/account/CreateAccountModal.vue`
- Modify: `frontend/src/components/account/EditAccountModal.vue`
- Modify: `frontend/src/components/account/credentialsBuilder.ts`
- Delete: `frontend/src/components/account/CnBaseUrlPresets.vue`
- Delete: `frontend/src/components/account/CNProviderQuotaCell.vue`
- Delete: `frontend/src/components/account/CNProviderBalanceCell.vue`
- Modify: `frontend/src/components/account/AccountUsageCell.vue`
- Modify: `frontend/src/components/account/ModelWhitelistSelector.vue`
- Modify: `frontend/src/composables/useModelWhitelist.ts`
- Modify: `frontend/src/components/account/__tests__/CreateAccountModal.spec.ts`
- Modify: `frontend/src/components/account/__tests__/EditAccountModal.spec.ts`
- Modify: `frontend/src/components/account/__tests__/credentialsBuilder.spec.ts`
- Modify: `frontend/src/components/account/__tests__/AccountUsageCell.spec.ts`
- Modify: `frontend/src/components/account/__tests__/ModelWhitelistSelector.spec.ts`

**Interfaces:**
- Consumes: Remaining `AccountPlatform` values and generic account credential helpers.
- Produces: Account creation/editing and account usage UI with no provider-specific controls, endpoints, or API calls.

- [ ] **Step 1: Add frontend absence tests**

Add source/render tests before deleting production UI:

```ts
it('does not expose removed provider account platforms', () => {
  const source = readFileSync(resolve('src/components/account/CreateAccountModal.vue'), 'utf8')
  expect(source).not.toMatch(/selectCNPlatform|CN_BASE_URL_PRESETS|cnProviders/)
  expect(source).not.toContain('platform="kimi"')
  expect(source).not.toContain('platform="zhipu"')
  expect(source).not.toContain('platform="deepseek"')
})
```

Add matching edit-modal, usage-cell, credentials-builder export, and whitelist assertions.

- [ ] **Step 2: Run focused tests and verify RED**

Run:

```powershell
Set-Location frontend
$env:NODE_OPTIONS='--max-old-space-size=4096'
pnpm exec vitest run src/components/account/__tests__ --reporter=basic --pool=forks --maxWorkers=1 --minWorkers=1
```

Expected: FAIL because the provider buttons, controls, helpers, API clients, and usage branches still exist.

- [ ] **Step 3: Delete account surfaces and reduce types**

Reduce the unions to:

```ts
export type AccountPlatform = 'anthropic' | 'openai' | 'gemini' | 'antigravity' | 'grok'
export type GroupPlatform = AccountPlatform | 'composite'
```

Remove CN account mode/protocol state, preset calculations, submit branches, quota/balance fetches, and deleted component imports. Delete the dedicated API and components, and remove their barrel export.

- [ ] **Step 4: Run focused tests, typecheck, and verify GREEN**

Run:

```powershell
Set-Location frontend
$env:NODE_OPTIONS='--max-old-space-size=4096'
pnpm exec vitest run src/components/account/__tests__ --reporter=basic --pool=forks --maxWorkers=1 --minWorkers=1
pnpm run typecheck
```

Expected: PASS with no TypeScript references to deleted modules or platform values.

- [ ] **Step 5: Commit frontend account removal**

```powershell
git add -A frontend/src/types frontend/src/api/admin frontend/src/components/account frontend/src/composables/useModelWhitelist.ts
git commit -m "refactor: remove CN provider account UI"
```

### Task 6: Remove Frontend Channel, Monitor, Presentation, and Locale Surfaces

**Files:**
- Modify: `frontend/src/api/admin/channelMonitor.ts`
- Modify: `frontend/src/constants/channelMonitor.ts`
- Modify: `frontend/src/views/admin/ChannelsView.vue`
- Modify: `frontend/src/views/admin/GroupsView.vue`
- Modify: `frontend/src/components/admin/channel/types.ts`
- Modify: `frontend/src/components/admin/monitor/MonitorFormDialog.vue`
- Modify: `frontend/src/components/admin/monitor/MonitorFiltersBar.vue`
- Modify: `frontend/src/components/admin/monitor/MonitorTemplateManagerDialog.vue`
- Modify: `frontend/src/components/user/monitor/ProviderIcon.vue`
- Modify: `frontend/src/components/user/monitor/MonitorCard.vue`
- Modify: `frontend/src/composables/useChannelMonitorFormat.ts`
- Modify: `frontend/src/components/common/PlatformIcon.vue`
- Modify: `frontend/src/components/common/PlatformTypeBadge.vue`
- Modify: `frontend/src/components/common/ModelIcon.vue`
- Modify: `frontend/src/components/common/GroupBadge.vue`
- Modify: `frontend/src/utils/platformColors.ts`
- Modify: `frontend/src/i18n/locales/en/admin/accounts.ts`
- Modify: `frontend/src/i18n/locales/zh/admin/accounts.ts`
- Modify: `frontend/src/i18n/locales/en/admin/overview.ts`
- Modify: `frontend/src/i18n/locales/zh/admin/overview.ts`
- Modify: `frontend/src/i18n/locales/en/dashboard.ts`
- Modify: `frontend/src/i18n/locales/zh/dashboard.ts`
- Modify: `frontend/src/views/admin/__tests__/GroupsView.compositePlatforms.spec.ts`
- Modify: `frontend/src/views/admin/__tests__/channelPlatformOptions.spec.ts`
- Modify: `frontend/src/components/admin/monitor/__tests__/MonitorPrimaryModelCell.spec.ts`
- Modify: `frontend/src/components/user/__tests__/MonitorCard.quota.spec.ts`

**Interfaces:**
- Consumes: Reduced `GroupPlatform`, monitor `Provider`, and remaining platform presentation helpers.
- Produces: Admin/user surfaces with only supported providers and no provider-specific labels or visual identity.

- [ ] **Step 1: Reverse channel and composite-route option tests**

Update the existing source tests:

```ts
it('excludes removed providers from composite route targets', () => {
  const source = readFileSync(resolve('src/views/admin/GroupsView.vue'), 'utf8')
  const options = source.slice(
    source.indexOf('const compositeRoutePlatformOptions'),
    source.indexOf('const compositeRouteEndpointOptions')
  )
  expect(options).not.toMatch(/kimi|zhipu|deepseek/i)
})
```

Add equivalent assertions for channel platform arrays and monitor selectors. Replace Kimi monitor fixtures with OpenAI or Grok fixtures.

- [ ] **Step 2: Run focused tests and verify RED**

Run:

```powershell
Set-Location frontend
$env:NODE_OPTIONS='--max-old-space-size=4096'
pnpm exec vitest run src/views/admin/__tests__/GroupsView.compositePlatforms.spec.ts src/views/admin/__tests__/channelPlatformOptions.spec.ts src/components/admin/monitor/__tests__/MonitorPrimaryModelCell.spec.ts src/components/user/__tests__/MonitorCard.quota.spec.ts --reporter=basic --pool=forks --maxWorkers=1 --minWorkers=1
```

Expected: FAIL because options, icons, formatters, and locale keys still include the removed providers.

- [ ] **Step 3: Remove options, presentation branches, and copy**

Remove provider values from channel/monitor unions and arrays, group/composite options, icon SVG branches, model-icon detection, badges, color maps, monitor rendering, and both locale trees. Replace provider-specific examples with supported neutral examples.

- [ ] **Step 4: Run focused and full frontend static checks**

Run:

```powershell
Set-Location frontend
$env:NODE_OPTIONS='--max-old-space-size=4096'
pnpm exec vitest run src/views/admin/__tests__/GroupsView.compositePlatforms.spec.ts src/views/admin/__tests__/channelPlatformOptions.spec.ts src/components/admin/monitor/__tests__/MonitorPrimaryModelCell.spec.ts src/components/user/__tests__/MonitorCard.quota.spec.ts --reporter=basic --pool=forks --maxWorkers=1 --minWorkers=1
pnpm run typecheck
pnpm run lint:check
```

Expected: PASS with no unused imports, missing locale keys, or impossible platform comparisons.

- [ ] **Step 5: Commit frontend channel and monitor cleanup**

```powershell
git add frontend/src
git commit -m "refactor: remove CN provider admin surfaces"
```

### Task 7: Active-Source Cleanup, Versioning, and Complete Local Verification

**Files:**
- Verify only: the Tasks 1-6 file list is exhaustive; this task stops on any unexplained active-source match.
- Modify: `backend/cmd/server/VERSION`

**Interfaces:**
- Consumes: Completed backend, migration, generated-code, and frontend changes.
- Produces: Version `0.1.184`, a clean active-source scan, and complete local acceptance evidence.

- [ ] **Step 1: Run the active-source removal scan**

Run:

```powershell
rg -i -n --hidden --glob '!.git/**' --glob '!frontend/node_modules/**' --glob '!frontend/dist/**' --glob '!backend/internal/web/dist/**' --glob '!build/**' --glob '!frontend/pnpm-lock.yaml' --glob '!backend/go.sum' --glob '!backend/migrations/224_user_platform_quotas_add_cn_providers.sql' --glob '!backend/migrations/226_channel_monitor_quota_mode.sql' --glob '!backend/migrations/227_composite_routes_add_cn_providers.sql' --glob '!backend/migrations/*cn_providers_migration_test.go' --glob '!backend/migrations/channel_monitor_quota_mode_migration_test.go' --glob '!openspec/**/source-freeze/**' '(kimi|moonshot|zhipu|deepseek|\bglm(?:[-_0-9]|\b)|\bk3(?:-256k)?\b)' .
```

Expected: Only explicitly reviewed incidental `k3` fixture/key matches; no active provider support references.

- [ ] **Step 2: Remove every active-source residue and set the version**

Replace provider-named generic fixtures with neutral strings, delete stale comments and imports, and set:

```text
0.1.184
```

in `backend/cmd/server/VERSION`.

- [ ] **Step 3: Run complete backend verification**

Run:

```powershell
Set-Location backend
Set-Location ..
$goFiles = git diff --name-only origin/main...HEAD -- '*.go'
if ($goFiles) { gofmt -w $goFiles }
Set-Location backend
go test -p 1 ./...
go test -tags integration ./internal/repository -run TestMigration230RemovesCNProviderDataAndNarrowsConstraints -count=1
go run ./cmd/server -version
```

Expected: all tests PASS and version output reports `0.1.184`.

- [ ] **Step 4: Run complete frontend verification**

Run:

```powershell
Set-Location frontend
$env:NODE_OPTIONS='--max-old-space-size=4096'
pnpm exec vitest run --reporter=basic --pool=forks --maxWorkers=1 --minWorkers=1
pnpm run typecheck
pnpm run lint:check
pnpm run build
```

Expected: all Vitest files/tests pass; typecheck, lint, and build exit zero.

- [ ] **Step 5: Run application and Docker smoke tests**

Start the server on an unused loopback port with local PostgreSQL/Redis, request `/setup/status`, and require HTTP 200. Start Docker Desktop if the engine is not running, then run:

```powershell
docker compose -f deploy/docker-compose.yml config
docker build -f Dockerfile -t sub2api:0.1.184-local .
docker image inspect sub2api:0.1.184-local
```

Run the repository Docker security/resource/reference tests through the available Bash environment. Start a disposable application container against the local dependencies, require a successful HTTP response, then remove only that disposable test container.

- [ ] **Step 6: Review the final implementation diff and commit**

```powershell
git diff --check
git status --short
git add -A
if (git diff --cached --quiet) { Write-Output 'No final cleanup commit needed' } else { git commit -m "chore: release 0.1.184" }
```

If all implementation changes were already committed and only the version changed, the final commit contains only `backend/cmd/server/VERSION` and any test-driven cleanup residue.

### Task 8: Push, Publish GitHub Release, and Verify Docker Hub

**Files:**
- No source files should change after verification.

**Interfaces:**
- Consumes: Clean verified `main` at version `0.1.184` and existing GitHub/Docker Hub authentication.
- Produces: Public GitHub Release `v0.1.184` and four verified Docker Hub manifest tags.

- [ ] **Step 1: Verify release preconditions**

Run:

```powershell
git status --short --branch
git log -8 --oneline --decorate
gh auth status
docker info
```

Expected: clean worktree, local `main` ahead only by reviewed commits, authenticated GitHub CLI, and running Docker engine.

- [ ] **Step 2: Push `main`**

```powershell
git push origin main
```

Expected: `origin/main` resolves to the verified implementation commit.

- [ ] **Step 3: Create and push the annotated release tag**

Create `v0.1.184` with release notes that state the provider integrations and all stored provider data are removed, generic OpenAI-compatible accounts remain neutral, and migration 230 is destructive:

```powershell
git tag -a v0.1.184 -m "Sub2API 0.1.184" -m "Remove the dedicated Kimi, Zhipu GLM, and DeepSeek integrations and purge their stored data. Preserve generic OpenAI-compatible accounts and immutable migration history."
git push origin v0.1.184
```

- [ ] **Step 4: Wait for and verify the release workflow**

Use `gh run list`, `gh run watch`, and `gh release view v0.1.184 --json` to prove the workflow succeeded and the release is public, non-draft, non-prerelease, with archives and checksums.

- [ ] **Step 5: Verify all Docker Hub tags and architectures**

Run:

```powershell
docker buildx imagetools inspect forever94yu/sub2api:0.1.184
docker buildx imagetools inspect forever94yu/sub2api:latest
docker buildx imagetools inspect forever94yu/sub2api:0.1
docker buildx imagetools inspect forever94yu/sub2api:0
```

Expected: all tags resolve to the same release digest and include `linux/amd64` and `linux/arm64` manifests.

- [ ] **Step 6: Fetch release automation updates and verify final repository state**

```powershell
git fetch origin main --tags
git status --short --branch
git log -5 --oneline --decorate
```

Expected: local `main` matches `origin/main`, `v0.1.184` points to the intended release commit, and the worktree is clean. If the release workflow creates a VERSION sync commit, fast-forward local `main` non-destructively before reporting completion.
