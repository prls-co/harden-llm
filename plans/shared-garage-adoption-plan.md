# Shared Garage adoption plan

## 1. Status and objective

- ID: `PLAN-HLLM-SHARED-GARAGE-001`.
- Updated: 2026-09-23.
- Status: implementation planned; this document changes no runtime configuration.
- Issue: [Harden-LLM #53](https://github.com/prls-co/harden-llm/issues/53).
- Canonical cross-repository order: [shared Garage transition plan](https://github.com/prls-co/garage-shared/blob/main/plans/shared-garage-transition-plan.md), tracked in [garage-shared #1](https://github.com/prls-co/garage-shared/issues/1).

Remove production Garage ownership from Harden-LLM and consume `http://garage-shared:3900`. The user explicitly accepts breaking configuration changes and interruptions to prioritize cleanliness, robustness, and maintainability. This revision supersedes the earlier compatibility-alias and simultaneous Analytics migration proposal.

The shared repository owns the daemon, server configuration, runtime RPC secret, and adopted volumes. Harden-LLM owns artifact behavior, client credentials, Caddy artifact routing, Loki configuration, and isolated test/preview fixtures. No Go API, OpenAPI schema, frontend feature, backup subsystem, secret rotation, or provider integration is required.

## 2. Decisions and reasons

1. Use only `garage-shared:3900` in persistent runtime configuration. No old alias, endpoint fallback, or parallel deployment path remains. A controlled interruption removes the need for compatibility machinery.
2. Keep the image/configuration and physical volume names during transfer. `harden-llm_garage-metadata` and `harden-llm_garage-data` identify retained data; renaming them creates an unnecessary data migration.
3. Add the gateway to `prls-observability` while retaining its private network. Caddy and Loki already join the shared network. Final networking must be declarative, not a manual connection.
4. Preserve public artifact origin, bucket/object keys, region, signatures, TTLs, Loki schema/retention, and credentials. Internal ownership changes must not silently invalidate stored references.
5. Retain isolated integration, smoke, and previews. The smoke overlay currently supplies only initialization flags and inherits the rest of Garage from production; it must become self-contained before the root service is removed.
6. Analytics migrates after current shared consumers work. Langfuse keeps its upstream storage; this plan does not replace MinIO or move traces.

## 3. File map

| Files | Required result |
| --- | --- |
| `docker-compose.yml` | No production `garage` service, its volumes, or `depends_on: garage`; final gateway endpoint; gateway joins private and shared networks. |
| `deploy/caddy/Caddyfile`, `deploy/loki/loki.yaml` | Upstream `garage-shared:3900`; signing/public routes and Loki data layout preserved. |
| `deploy/images.lock.json` | Remove Garage from production image map. Retain explicit pins in fixtures; no new lock schema. |
| `deploy/garage/garage.toml` → `deploy/test/garage.toml` | Make remaining test/preview ownership explicit; production server config belongs to `garage-shared`. Update all references. |
| `deploy/test/compose.integration.yml`, `deploy/test/compose.smoke.yml` | Self-contained pinned fixture Garage, fresh project-owned data, no persistent shared dependency. |
| `scripts/preview-environment.mjs`, `scripts/test/preview_policy_test.mjs` | Reference new fixture path; preserve each preview's Garage/network/routing/data. |
| `internal/deploytest/compose_caddy_test.go`, `internal/deploytest/prls_observability_test.go` | Exact production topology/image checks reflect external ownership and final client endpoint/network. |
| `internal/smoke/harness_compose.go` and smoke fixture assertions | Final DNS name resolves to test-owned Garage, with network/mount isolation. |
| `scripts/production-config.mjs`, `scripts/test/production_config_test.mjs`, `config/production-config.example.json` | Inspect for affected assumptions; change only actual local-Garage dependencies. Preserve descriptor/scope validation. |
| `plans/from_utility-llm/harden-llm-self-hosted-test-spec.md`, `docs/requirements-traceability.md`, active deploy docs | Update current TEST-030/033/034 ownership/fixture descriptions; retain historical evidence. |
| Private `/home/kirill/.config/harden-llm/production.json` and effective host environment | Remove managed Garage identity/overrides, use approved updated Compose root, retain actual application identities. Never print/commit private values. |

## 4. Phase H0 — inspect and define regressions

Status: pending. Read `AGENTS.md` and `docs/liveview-go-testing-guidelines.md` before implementation.

1. Confirm branch, actual production Compose source/descriptor, image identities, and Garage mounts/networks in central Phase 0's execution record. Source HEAD does not establish live configuration. If this checkout supplies live bind-mounted files, implement in a separate worktree and apply configuration only during the central interruption.
2. Search active source for `garage:3900`, `harden-llm-garage-1`, `deploy/garage`, and production service/image/volume lists. Classify as persistent runtime, isolated test/preview, or historical documentation. Do not globally replace `garage`.
3. Identify TEST-030 topology, TEST-033 effective Compose contract, TEST-034 smoke, TEST-043 exclusive restart, and TEST-233/234/235 production-config coverage. Keep canonical `SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001` references and assertion purposes.
4. Add cheap regressions before implementation: no production-owned Garage/volumes/dependency; gateway/Caddy/Loki use final endpoint; gateway has both networks; no Garage/admin host port; tests/previews cannot use persistent shared storage. Use existing Go/static and Node tests.

Gate: expected failures describe changed ownership/endpoint; unaffected storage/API assertions remain intact.

## 5. Phase H1 — remove production ownership

Status: pending.

1. Remove root `garage` service and `garage-metadata`/`garage-data` declarations. This is a source change; never delete physical Docker volumes. Remove gateway/Caddy dependencies on `garage`.
2. Set gateway endpoint to `http://garage-shared:3900`. Explicitly list both `harden-private` and `prls-observability`; overriding inherited networks must not disconnect Postgres. Keep client bucket/credential settings.
3. Update Caddy/Loki upstreams. Preserve artifact hostname, request host/path/query behavior, authentication, path-style S3 settings, region, Loki schema periods, and retention.
4. Remove Garage from the production image map. Update exact service/volume/image-count assertions precisely; do not weaken them to exists-only checks. Keep Langfuse provenance and MinIO separation checks.
5. Update TEST-033's current specification to external Garage consumption rather than local production bootstrap. Retain equivalent initialization/lifecycle coverage in isolated TEST-034/043 and shared-owner checks. Update TEST-030 and traceability; do not rewrite historical certification.

Gate: production resolves without local Garage, gateway networking is explicit, and topology assertions remain exact.

## 6. Phase H2 — make isolated fixtures complete

Status: pending. Required because smoke currently inherits its Garage definition from production.

1. Move configuration to `deploy/test/garage.toml`; update integration mounts and preview-copy code/fixtures. Keep local fixtures so testing does not require the shared repo checkout.
2. Give smoke service `garage` its own pinned image, empty-layout initialization command, generated fixture key/bucket/RPC settings, config mount, health check, and project-owned metadata/data volumes. No live credentials or external persistent volumes.
3. Give isolated smoke Garage alias `garage-shared` on the smoke network so production gateway/Caddy resolve to the fixture. Its service identifier `garage` can remain for existing lifecycle discovery. Smoke's logical `prls-observability` must still have a unique generated name and `external: false`, never the live network.
4. Add smoke-only healthy-Garage dependencies for gateway/Caddy where fixture startup needs them. Ensure smoke Loki targets its fixture. Inspect the merged Compose model, not only overlay text.
5. Update `assertLiveStorageOwnership` in `internal/smoke/harness_compose.go` to require the final endpoint, map it to the smoke Garage, and assert network/mount isolation. Keep artifact and Langfuse separation checks; do not accept any hostname merely containing `garage`.
6. Keep preview and standalone integration endpoints local (`garage:3900` where appropriate). Keep TEST-043 on its exclusive fixture; it must never restart shared Garage. Retain explicit fixture image pins without cross-repo runtime dependencies.

Gate: fixtures boot from private empty volumes; smoke exercises production routing with isolated storage; previews keep independent endpoints/data.

## 7. Phase H3 — verify and prepare deployment

Status: pending. Record actual commands/results, not planned tests as passing.

1. Run focused checks for changed ownership/preview/configuration code, then the broad fast gate:

   ```sh
   go test ./internal/deploytest ./internal/testkit -count=1
   node --test scripts/test/preview_policy_test.mjs scripts/test/production_config_test.mjs
   PATH=/home/kirill/.local/elixir-1.20.2/bin:/home/kirill/.local/otp-28.4.3/bin:$PATH make test-fast
   ```

2. Run TEST-033 with fixture values through real Compose rendering:

   ```sh
   go test ./internal/deploytest/... -tags=compose -run TestComposeCaddyContract -count=1
   make test-production-config
   ```

3. Run the browser-free cross-system gate on the final implementation candidate:

   ```sh
   PATH=/home/kirill/.local/elixir-1.20.2/bin:/home/kirill/.local/otp-28.4.3/bin:$PATH make test-release
   git diff HEAD --check
   ```

   The release selector already includes integration, exclusive Garage restart, backend Compose smoke, and `make verify`. Do not prepend separate full integration/verify runs merely to duplicate certification. Focused reruns while fixing failures are appropriate. Never drop a gate, weaken assertions, or retry ambiguous operations to obtain green output.
4. Resolve production configuration privately; expected differences are Garage removal, final endpoints/config mounts, and gateway networking. Remove obsolete Garage descriptor service/overrides. Do not relax validation to allow unknown services. Keep application image identities, provider/profile/account settings, and unrelated services intact.
5. Prepare clean deployable and rollback revisions, including Caddy/Loki files and private descriptor inputs. Commit/push/merge under repository policy with runtime apply coordinated by central Phase 3; prevent an automatic endpoint switch before storage is ready.

Gate: checks pass and runtime apply/rollback inputs are concrete. No browser or real provider test is added. Configuration-only deployment reuses unchanged application images; rebuild only if application code changed.

## 8. Phase H4 — participate in the shared cutover

Status: pending. Execute only within central Phase 3; do not operate the storage volumes independently.

1. Stop affected writers and coordinate Caddy/Loki interruption using the central consumer list. The central sequence alone removes/replaces the daemon.
2. After shared Garage is healthy, apply prepared gateway/Caddy/Loki configuration using the existing trusted `production-config` procedure and service-scoped checks. Preserve descriptor/identity protections; do not edit rendered secret-filled Compose.
3. Verify gateway/Caddy resolve `garage-shared`, readiness succeeds, and scoped artifact operations work. Read a pre-cutover artifact and a new signed download through the public artifact route without recording secret URLs/tokens.
4. Check Loki writes/queries with its own key and unchanged retention/schema. Participate in central restart/reconnect acceptance. Observe Langfuse health separately; do not manufacture an LLM call to test storage.
5. Confirm ordinary Harden-LLM deploy cannot recreate production Garage. Inspect active source, descriptor, and service labels. Test/preview Garage remains confined to its own projects.

Gate: storage works through shared ownership, old/new artifact checks pass, and actual deployed identities are recorded.

## 9. Phase H5 — close out

Status: pending.

1. Record branch, merged configuration SHA, component image identities, environment URLs, shared-owner revision/image, and HTTP/storage results. A documentation SHA is not an application image SHA.
2. Remove obsolete active production instructions and empty `deploy/garage/`. Keep fixtures and historical evidence. Update both plans, issue #53, and Ops records.
3. Record problems/fixes and aftercare: artifact links, S3 errors, readiness, Loki continuity/retention, and disk usage after the next normal run/release. Analytics follow-up stays on its own issue.
4. Verify task changes are pushed/merged and deployed configuration matches acceptance. Record unresolved task-owned files/gates. Cross-repository completion follows the central plan's criteria.

Rollback uses central Section 10: stop new Garage before restoring the old owner from its recorded revision, then restore clients. No compatibility endpoint remains in the accepted configuration. Missing objects, unresolved credentials, a second writer, or failed release/storage checks stop the affected cutover; accepted downtime does not justify masking failures.

Preparation evidence: source, pinned CLI startup help, and selected live Docker identities reviewed. No implementation, application test, runtime change, data copy, or deployment occurred while writing this plan. Documentation validation is recorded in its PR; H0–H5 implementation gates remain pending.
