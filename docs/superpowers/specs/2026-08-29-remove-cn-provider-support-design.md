# Remove Kimi, Zhipu GLM, and DeepSeek Support Design

**Date:** 2026-08-29

**Status:** Approved in chat

## Objective

Remove the dedicated Kimi/Moonshot/K3, Zhipu GLM, and DeepSeek integrations from Sub2API in one release. The removal covers application code, public API contracts, database data and constraints, frontend surfaces, configuration, model metadata, tests, release artifacts, and Docker images.

The generic OpenAI-compatible API-key path remains provider-neutral. This change does not add hostname or model-name blocking to custom OpenAI-compatible accounts.

## Chosen Approach

Ship a one-time complete removal in `v0.1.184`:

1. Remove all active backend and frontend integration surfaces.
2. Add a forward-only migration that physically deletes all related runtime and historical data, then narrows database constraints.
3. Verify the change against the complete local test environment and a real application/container startup.
4. Push the verified commit to `main`, publish a GitHub Release, and publish the Docker Hub multi-architecture tags.

A two-stage deprecation was rejected because these providers were never configured or used in the target installation. Compatibility tombstones were rejected because they would leave dead provider concepts in active code.

## Scope

### Backend

Remove the three providers from:

- Domain and service platform constants and allowed-platform collections.
- Account credential validation, account helpers, default endpoints, model normalization, and model support checks.
- Gateway routing, scheduling, model availability, OpenAI/Anthropic conversion exceptions, request shaping, usage recording, and upstream response handling.
- Composite-platform aliases, model-prefix inference, route validation, and route dispatch.
- Billing aliases and fallback prices, including Kimi K3, GLM, and DeepSeek model families.
- Channel platform discovery, channel monitoring, scheduling thresholds, quota/balance fetchers, rate-limit recovery, and scheduler snapshots.
- CN-provider quota, balance, probe, and background-check services.
- Admin CN-provider endpoints, request validators, route registration, handlers, dependency injection, and runtime lifecycle registration.
- Provider-specific configuration and allowed-host defaults.
- Ent schema enum validation and regenerated Ent/Wire output.

Requests that submit `kimi`, `zhipu`, or `deepseek` as an account, group, monitor, or composite-route platform must be rejected as unsupported. The dedicated `/admin/cn-providers/...` endpoints must no longer be registered.

Generic protocol bridges remain in place when their behavior is provider-neutral. Provider-named fixtures and comments in active compatibility code are replaced with neutral equivalents so they do not imply supported integrations.

### Frontend

Remove the three providers from:

- Account and group platform types and API contracts.
- Create-account and edit-account platform selectors, account-mode controls, protocol controls, endpoint presets, credential builders, and submit normalization.
- Provider quota and balance API clients and account usage components.
- Model whitelist data and provider-specific model lists.
- Channels, groups, composite routes, monitor forms, monitor filters, and template selectors.
- Platform/model icons, badges, colors, monitor formatting, and user monitor cards.
- English and Chinese platform labels, provider help text, endpoint text, quota/balance copy, and model examples.

The deleted provider-specific frontend modules must have no remaining imports. Generated frontend assets are rebuilt through the normal build and release pipelines rather than edited directly.

### Database

Add `backend/migrations/230_remove_cn_provider_support.sql` as a forward-only migration. It must be safe when no matching rows exist and atomic when matching rows do exist.

The migration captures affected account and group identifiers, then physically removes all Kimi, Zhipu, and DeepSeek data in foreign-key-safe order, including:

- Accounts, encrypted credentials, account-group relationships, and provider-specific account metadata.
- Provider groups and their directly associated runtime, subscription, API-key, routing, usage, audit, and history records.
- User platform quotas and provider entries in default-platform-quota settings.
- Channel monitors, request templates, quota snapshots, daily rollups, and monitor history.
- Composite model routes targeting the removed providers.
- Provider-specific model mappings, whitelist values, and related JSON configuration.

After data deletion, the migration drops and recreates the affected `CHECK` constraints without `kimi`, `zhipu`, or `deepseek`. Ent schema validation must match those constraints.

Migrations `224_user_platform_quotas_add_cn_providers.sql`, `226_channel_monitor_quota_mode.sql`, and `227_composite_routes_add_cn_providers.sql` are immutable migration history. They remain in the repository because changing or deleting an applied migration would cause checksum mismatches and prevent existing installations from starting. Migration 230 reverses their active database effect.

Historical source-freeze artifacts under `openspec/**/source-freeze/**` also remain untouched as audit evidence. They are excluded from the active-source removal scan.

## Failure Behavior

- Unsupported provider values return the existing structured unsupported-platform or validation error rather than reaching scheduling or forwarding.
- Removed admin routes return the normal not-found response.
- Migration cleanup and constraint replacement run in one transaction. Any unexpected foreign-key or schema failure rolls back the whole migration and prevents startup instead of leaving partial cleanup.
- The generic OpenAI-compatible account path does not reject vendor hostnames or model names; it continues to apply only generic OpenAI-compatible behavior.

## Test Strategy

Use test-driven development for behavior changes. Each production removal begins with a failing regression test that demonstrates the old provider support is still present.

### Backend and Database

- Request-binding and service tests reject the three platform identifiers for accounts, groups, monitors, quotas, and composite routes.
- Router/API-contract tests prove CN-provider routes are absent and gateway routes no longer dispatch those platforms.
- Scheduling, model-list, composite-routing, billing, monitoring, quota, balance, and rate-limit tests prove the providers and their model families are not recognized.
- Generic bridge tests use neutral fixtures and retain provider-independent reasoning/tool behavior.
- A migration integration test seeds every affected table with all three providers, applies migration 230, proves that no related data remains, and proves the narrowed constraints reject reinsertion.
- Existing migrations 224, 226, and 227 retain their checksum-tested contents.

### Frontend

- Modal tests prove account creation and editing do not render provider buttons, endpoint presets, or provider-specific controls.
- Group, channel, composite-route, and monitor tests prove the provider options are absent.
- Type, credentials-builder, model-whitelist, icon/badge, usage-cell, and locale tests are updated to the reduced platform set.
- Deleted API and component modules leave no orphan imports.

### Complete Local Acceptance

Run:

- Go formatting and generated-code checks.
- Complete backend tests with serialized package execution where required by the local environment.
- Tagged repository migration integration tests against the local PostgreSQL environment.
- Complete Vitest suite with bounded workers and sufficient Node heap.
- Frontend type checking, lint checking, and production build.
- Application startup with a real HTTP request to the setup/health surface.
- Local Docker image build and container/Compose startup smoke checks.
- Active-source removal scan for `kimi`, `moonshot`, provider-related `k3`, `zhipu`, `glm`, and `deepseek`, excluding immutable migrations, source-freeze artifacts, generated build snapshots, lockfile hashes, and unrelated generic fixture keys.

Dependency downloads may use the HTTP/HTTPS proxy at `127.0.0.1:10808` when needed.

## Release

Set the application version to `0.1.184` and create annotated tag `v0.1.184`. Release notes follow the repository's existing style and call out the intentionally breaking removal and destructive migration.

After local verification:

1. Commit the implementation and push `main` to `https://github.com/forever94yu/sub2api.git`.
2. Push `v0.1.184` and verify the GitHub Actions release workflow completes successfully.
3. Verify the GitHub Release is public, non-draft, non-prerelease, and contains the expected platform archives and checksums.
4. Verify Docker Hub publishes multi-architecture Linux AMD64 and ARM64 manifests for:
   - `forever94yu/sub2api:0.1.184`
   - `forever94yu/sub2api:latest`
   - `forever94yu/sub2api:0.1`
   - `forever94yu/sub2api:0`
5. Confirm all four Docker tags resolve to the expected release digest.

## Completion Criteria

The work is complete only when:

- No active backend, frontend, configuration, pricing, or documentation surface advertises or implements the three dedicated integrations.
- Database migration 230 removes all seeded provider data and prevents new constrained rows using the removed platform values.
- Generic OpenAI-compatible behavior still passes its existing regression tests.
- All required local tests, builds, startup checks, and Docker smoke checks pass.
- `main`, GitHub Release `v0.1.184`, and the Docker Hub tags are published and independently verified.
