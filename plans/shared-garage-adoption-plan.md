# Shared Garage adoption plan

## 1. Status and objective

- ID: `PLAN-HLLM-SHARED-GARAGE-001`.
- Updated: 2026-09-24.
- Status: H0-H3 are complete. The production source is on `main` at `a4308caf4d52326f33d29bb4070842e23992703f`; local focused checks and `make test-fast` passed, and hosted browser-free `make test-release` run `36064129358` passed. H4 moved the existing Garage volumes to the shared owner at `a572e183b448b0492f7980e833da0eabbe5926ac`; the owner and HLLM services are healthy, production configuration is equivalent, and the read-only artifact inventory is healthy. Public HTTPS probes still receive Cloudflare 530 / error 1033, so public-route acceptance remains open. Each other consumer follows independently through its existing repository issue.
- Issue: [Harden-LLM #53](https://github.com/prls-co/harden-llm/issues/53).
- Canonical cross-repository order: [shared Garage transition plan](https://github.com/prls-co/garage-shared/blob/main/plans/shared-garage-transition-plan.md), tracked in [garage-shared #1](https://github.com/prls-co/garage-shared/issues/1).

Remove production Garage ownership from Harden-LLM and make its gateway, Caddy artifact route, and Loki use `http://garage-shared:3900`. The user explicitly accepts breaking configuration changes and interruptions to prioritize cleanliness, robustness, and maintainability. Move Harden-LLM first; do not wait for, edit, or deploy consumer repositories in this phase.

The `garage-shared` repository owns the daemon and server configuration. Harden-LLM consumes it with existing bucket credentials and existing metadata/data volumes; no object copy, snapshot, backup subsystem, blanket secret rotation, API change, or provider integration is part of this cutover. Integration, smoke, and preview Garage instances remain isolated fixtures, not production legacy. Existing consumers have open follow-up issues in their own repositories and move later, one at a time. Agent Platform currently reaches the old Harden-LLM daemon through the `garage` network alias; its artifact access will be interrupted when that daemon is replaced and resumes when its own issue is deployed. Bin Eval's separate Garage remains untouched and continues to own host loopback port 3900.

## 2. Decisions and reasons

1. Use only `garage-shared:3900` in persistent runtime configuration. No old alias, endpoint fallback, or parallel deployment path remains. A controlled interruption removes the need for compatibility machinery.
2. Keep the image/configuration and physical volume names during transfer. `harden-llm_garage-metadata` and `harden-llm_garage-data` identify retained data; renaming them creates an unnecessary data migration.
3. Add the gateway to `prls-observability` while retaining its private network. Caddy and Loki already join the shared network. Final networking must be declarative, not a manual connection. Other clients change only in their own follow-up issue after this HLLM-first phase.
4. Preserve public artifact origin, bucket/object keys, region, signatures, TTLs, Loki schema/retention, and credentials. Internal ownership changes must not silently invalidate stored references.
5. Retain isolated integration, smoke, and previews. The smoke overlay currently supplies only initialization flags and inherits the rest of Garage from production; it must become self-contained before the root service is removed.
6. Keep Langfuse's upstream storage; this plan does not replace MinIO or move traces. Keep non-HLLM Garage stores and clients running until their repo-owned issue is handled.

7. The Garage owner normally publishes S3 on loopback port 3900 for host-native clients. Bin Eval still owns that port during the HLLM-first phase, so run the owner with a private Compose override that publishes no host port. HLLM uses Docker networking and does not need a host binding. Keep the override with the private owner deployment inputs until Bin Eval migrates.

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
| `plans/from_utility-llm/harden-llm-self-hosted-test-spec.md`, `docs/requirements-traceability.md`, active deploy docs | Update current TEST-033/034 ownership/fixture descriptions; retain historical evidence. |
| Private `/home/kirill/.config/harden-llm/production.json` and effective host environment | Remove managed Garage identity/overrides, use approved updated Compose root, retain actual application identities. Never print/commit private values. |

## 4. Phase H0 — inspect and define regressions

Status: complete. Read `AGENTS.md` and `docs/liveview-go-testing-guidelines.md`; source was implemented in an isolated worktree. Recheck the current production checkout and mounted files immediately before changing runtime.

1. Confirm production Compose source/descriptor, image identities, Garage mounts/networks, current bucket inventory, and the owner Compose revision. Source HEAD does not establish live configuration. The production checkout currently supplies live bind-mounted files; stop affected Harden-LLM services before switching it to `main`.
2. Search active source for `garage:3900`, `harden-llm-garage-1`, `deploy/garage`, and production service/image/volume lists. Classify as persistent runtime, isolated test/preview, or historical documentation. Do not globally replace `garage`.
3. Identify TEST-033 effective Compose contract, TEST-034 smoke, TEST-043 exclusive restart, and TEST-233/234/235 production-config coverage. Keep canonical `SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001` references and assertion purposes.
4. Keep cheap regressions for: no production-owned Garage/volumes/dependency; gateway/Caddy/Loki use the final endpoint; gateway has both networks; tests/previews cannot use persistent shared storage. Add coverage only if current tests miss one of these invariants.

Gate: expected failures describe changed ownership/endpoint; unaffected storage/API assertions remain intact.

## 5. Phase H1 — remove production ownership

Status: implementation merged to `main` in PR #54 as `73fc73584a7095ba95538c9a7291553e0d8ca2c5`. Smoke now supplies a pinned fixture Garage with generated credentials, project-owned metadata/data volumes, and an isolated network alias for the production endpoint. Integration and preview fixture paths remain local. Focused topology/fixture checks and browser-free release verification passed; no persistent shared storage was started.

1. Remove root `garage` service and `garage-metadata`/`garage-data` declarations. This is a source change; never delete physical Docker volumes. Remove gateway/Caddy dependencies on `garage`.
2. Set gateway endpoint to `http://garage-shared:3900`. Explicitly list both `harden-private` and `prls-observability`; overriding inherited networks must not disconnect Postgres. Keep client bucket/credential settings.
3. Update Caddy/Loki upstreams. Preserve artifact hostname, request host/path/query behavior, authentication, path-style S3 settings, region, Loki schema periods, and retention.
4. Remove Garage from the production image map. Update exact service/volume/image-count assertions precisely; do not weaken them to exists-only checks. Keep Langfuse provenance and MinIO separation checks.
5. Update TEST-033's current specification to external Garage consumption rather than local production bootstrap. Retain equivalent initialization/lifecycle coverage in isolated TEST-034/043 and shared-owner checks. Update TEST-030 and traceability; do not rewrite historical certification.

Gate: production resolves without local Garage, gateway networking is explicit, and topology assertions remain exact.

## 6. Phase H2 — make isolated fixtures complete

Status: implementation merged to `main` in PR #54 as `73fc73584a7095ba95538c9a7291553e0d8ca2c5`. Exact production topology, final S3 endpoint, explicit gateway networks, and unchanged artifact/Loki behavior passed the browser-free source/release checks recorded under H3. Runtime descriptor/apply remains separate.

1. Move configuration to `deploy/test/garage.toml`; update integration mounts and preview-copy code/fixtures. Keep local fixtures so testing does not require the shared repo checkout.
2. Give smoke service `garage` its own pinned image, empty-layout initialization command, generated fixture key/bucket/RPC settings, config mount, health check, and project-owned metadata/data volumes. No live credentials or external persistent volumes.
3. Give isolated smoke Garage alias `garage-shared` on the smoke network so production gateway/Caddy resolve to the fixture. Its service identifier `garage` can remain for existing lifecycle discovery. Smoke's logical `prls-observability` must still have a unique generated name and `external: false`, never the live network.
4. Add smoke-only healthy-Garage dependencies for gateway/Caddy where fixture startup needs them. Ensure smoke Loki targets its fixture. Inspect the merged Compose model, not only overlay text.
5. Update `assertSmokeStorageOwnership` in `internal/smoke/harness_compose.go` to require the final endpoint, map it to the smoke Garage, and assert network/mount isolation. Keep artifact and Langfuse separation checks; do not accept any hostname merely containing `garage`.
6. Keep preview and standalone integration endpoints local (`garage:3900` where appropriate). Keep TEST-043 on its exclusive fixture; it must never restart shared Garage. Retain explicit fixture image pins without cross-repo runtime dependencies.

Gate: fixtures boot from private empty volumes; smoke exercises production routing with isolated storage; previews keep independent endpoints/data.

## 7. Phase H3 — verify and prepare deployment

Status: verification and preparation complete. Focused Go/Compose/Node checks and local `make test-fast` passed; hosted browser-free `make test-release` run `36064129358` passed. The local release attempt passed 24 tasks but could not allocate the smoke network because Docker address pools were exhausted; no assertion failed or was weakened. HLLM source is on `main` at `a4308caf4d52326f33d29bb4070842e23992703f`, and the production descriptor is equivalent after the runtime change. No app image was rebuilt or pulled. This execution record is being updated in PR #64.

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
4. Confirmed the private descriptor has no HLLM Garage entry and the only production differences are the planned gateway endpoint/network changes. Its final post-apply check reports `equivalent`; application image identities and provider/profile/account settings remain pinned.
5. Advanced the production bind-mounted checkout to `main` only after confirming the HLLM core services were stopped. Started the shared owner before HLLM clients and retained a same-volume rollback procedure. The source config was already merged in PR #54; this plan's execution record is in PR #64.

Gate: passed for source, hosted browser-free release, production config, and local storage/readiness checks. Public edge acceptance remains in H4. No browser or real-provider test was added; configuration-only deployment reused the approved images.

## 8. Phase H4 — participate in the shared cutover

Status: runtime cutover applied on 2026-09-24. The shared Garage and local HLLM health/storage checks pass. External public-route acceptance is blocked by Cloudflare error 1033; no other consumer repository was changed or deployed.

1. Confirmed the production checkout and owner checkout are clean, advanced `garage-shared` to `main` at `a572e183b448b0492f7980e833da0eabbe5926ac`, and advanced the production bind-mounted checkout to HLLM `main` at `a4308caf4d52326f33d29bb4070842e23992703f`. The HLLM containers had already been manually stopped before this cutover; their initiating actor is unknown.
2. Reused the existing RPC secret without rotation. The mode-`0600` owner runtime file uses that same value. A mode-`0600` Compose override suppresses Garage's host port while Bin Eval still owns loopback 3900. Rendered Compose confirmed the two existing external HLLM volumes and only `prls-observability`.
3. Started Garage with normal `/garage server` startup and no initialization flags. It reports one healthy v2.3.0 node, uses `harden-llm_garage-metadata` and `harden-llm_garage-data`, publishes no host ports, and lists the existing `harden-llm-artifacts`, `prls-loki`, `prls-allure-reports`, and `prls-agent-artifacts` buckets.
4. Switched HLLM source to `main`; the exact production check showed only the planned gateway endpoint and network differences. Applied the gateway/Caddy changes through `production-config`. Started HLLM's stopped Postgres, Prometheus, Tempo, Loki, Grafana, OTel Collector, and web services with the already approved images; Langfuse web/worker, ClickHouse, MinIO, its Postgres, and Redis stayed running.
5. A direct Compose start initially selected the shared development release for web and OTel. `production-config check` caught the mismatch before acceptance. Restored the descriptor-pinned web image/release and OTel release through the existing apply path using a temporary descriptor; the permanent descriptor was not loosened. Final `production-config check` reports `equivalent`, and all managed services are healthy.
6. Local Caddy probes for web `/healthz`, web `/login`, API `/healthz`, and API `/readyz` returned 200. The read-only `audit-artifacts` inventory reports 5/5 referenced objects, zero missing or unreferenced objects, and `healthy=true`. A Loki label query using its configured `fake` tenant returned 3 labels. An unsigned local request to the artifact host returned the expected 403 from the protected object route; a signed public download has not been verified.
7. Removed the stopped `harden-llm-garage-1` container without deleting its volumes. Removed the two obsolete `HARDEN_LLM_GARAGE_RPC_SECRET` assignments from private HLLM environment files after verifying the unchanged value matches the shared owner's RPC secret. Production Compose/source no longer defines HLLM Garage; isolated integration, smoke, and preview Garage fixtures remain.
8. Public HTTPS requests to HLLM web/API health routes returned Cloudflare 530, error 1033. Local Caddy routes return 200, so the deployed application is healthy on-host, but external routing is not accepted. The available `shaman-api-cloudflared-1` container is in `shaman-api_default`; its ownership of the HLLM route is unverified, so it was not restarted.

Gate: on-host Harden-LLM health and retained artifact/Loki reads pass; Garage uses the existing volumes and publishes no host port; production HLLM source is on `main`. Public signed-download acceptance remains blocked until the Cloudflare route stops returning 1033. Agent Platform's issue remains open because its client still targets the retired `garage` alias. No other consumer migration is complete.

## 9. Phase H5 — close out

Status: partially complete; operational findings are recorded here and in issues #53 and `garage-shared#1`. External route verification and post-cutover monitoring remain.

1. Record source/owner SHAs, pinned HLLM image identities, environment URLs, Compose state, and HTTP/storage results. A documentation SHA is not an application image SHA.
2. The production source cleanup, private RPC-variable cleanup, and old HLLM container removal are complete. Keep isolated fixtures and historical certification records. Merge this execution-plan update and post the same evidence to HLLM issue #53 and owner issue #1.
3. Resolve Cloudflare error 1033, then recheck public HLLM web/API health, API readiness, and a signed artifact download. At the next normal log/trace write, confirm Loki retention/continuity and Langfuse trace ingestion; review disk use without adding backup or migration tooling.
4. Keep other consumer issues open until each consumer completes its own deployment and checks. Do not claim the overall multi-repo transition is complete.

If shared Garage fails before acceptance, stop it first, switch the stopped production checkout back to the recorded pre-cutover revision, restart the old HLLM Garage on the same volumes, then restore affected HLLM services. Do not run two daemons against the adopted volumes. No data backup or snapshot is part of this plan.

Preparation evidence: source, pinned Garage startup behavior, exact Docker image/volume identities, private environment source modes, and production descriptor were reviewed. No object copy, snapshot, or secret rotation occurred. HLLM's existing Garage volumes now have one owner service, `garage-shared`; consumer code and separate Garage instances remain unchanged.

### Execution log

- 2026-09-24 — H1/H2 source implementation merged in Harden-LLM PR #54 (`73fc73584a7095ba95538c9a7291553e0d8ca2c5`). Production Compose no longer owns Garage; persistent gateway/Caddy/Loki use `garage-shared:3900`; smoke, integration, and previews retain isolated fixtures.
- 2026-09-24 — Focused topology/fixture Go tests, TEST-033 Compose contract, 25 Node preview/production-config tests, and `git diff HEAD --check` passed. `make test-fast` passed all 10 tasks after moving Go temporary files off the constrained `/tmp`. Local `make test-release` passed 24 tasks but could not allocate the smoke network because Docker address pools were exhausted; hosted browser-free release run `36064129358` passed. Browser and live-provider suites were not run.
- 2026-09-24 — HLLM production containers were already manually stopped before this cutover; the initiating actor is unknown. `garage-shared` was advanced to main `a572e183b448b0492f7980e833da0eabbe5926ac`; Harden-LLM production source is main `a4308caf4d52326f33d29bb4070842e23992703f`.
- 2026-09-24 — Shared Garage started healthy on the existing `harden-llm_garage-metadata` and `harden-llm_garage-data` volumes, one v2.3.0 node, and only `prls-observability`; it publishes no host port. Existing buckets include `harden-llm-artifacts`, `prls-loki`, `prls-allure-reports`, and `prls-agent-artifacts`. Bin Eval's separate Garage and port 3900 were untouched.
- 2026-09-24 — HLLM services are healthy and the original production descriptor is equivalent. Pinned identities: gateway release `d581942634b7197cea2d09069f73ebc89ff75bce`, image `sha256:75f237ed40ec23ddc31b14ddcff78f008e2630a22fc9bd4debc8c99ff33bb01a`; web same release, image `sha256:f989a6fdca2b27aa008d4b70100d9911bd6a4278dda3d0e1b71f7e889fd83d38`; OTel release `729804cdfab9ff5c2e9dd75c64609cddd6773557`, image `sha256:125bdbeb7590cc1952c5b3430ecf14063568980c2c93d5b38676cc0446ed8108`. Caddy and Loki retained their locked images.
- 2026-09-24 — Local web `/healthz`, web `/login`, API `/healthz`, and API `/readyz` returned 200. Read-only artifact audit returned 5/5 referenced objects, zero missing/unreferenced objects, `healthy=true`. Loki query for its configured `fake` tenant returned 3 labels. The local artifact host route returned 403 for an unsigned request; no signed download was recorded.
- 2026-09-24 — Public HLLM hosts returned Cloudflare HTTP 530 / error 1033. This is an external routing blocker: Caddy local probes pass, but public health and signed-download acceptance remain open. The running `shaman-api-cloudflared-1` connector uses `shaman-api_default`; its HLLM route ownership was not established and it was not restarted.
- 2026-09-24 — A direct Compose start briefly selected the shared development release for web/OTel. Production-config detected the identity mismatch; the exact descriptor-pinned web image and OTel release were restored through the existing apply code with a temporary descriptor. The permanent descriptor was not changed, no image was pulled, and final `production-config check` is equivalent.
- 2026-09-24 — Removed the old stopped `harden-llm-garage-1` container without `-v`; existing volumes remain mounted by the shared owner. Removed the obsolete Garage RPC assignment from both mode-`0600` HLLM env files only after value equality with the unchanged shared-owner secret was verified privately. No key was rotated and no value was recorded. A stale isolated-install test container and its two test-only volumes were also removed.

Risks and follow-up checks:

- Restore the Cloudflare route for `harden-llm.prls.co`, `harden-llm-api.prls.co`, and `harden-llm-artifacts.prls.co`. Then require public web/API health, API readiness, and one signed artifact download before marking public production acceptance complete.
- Agent Platform issue #20 remains open because its client still targets the retired `garage:3900` alias. Verify after that repository's own migration; no consumer source was edited here.
- Keep the shared owner's no-host-port override until Bin Eval migrates. Check port 3900 ownership before removing the override.
- The owner is one node on one host. High availability, snapshots, and backups are intentionally out of scope. Never run two Garage daemons against the adopted volumes.
- At the next normal HLLM artifact write and telemetry cycle, inspect S3 errors, Loki continuity/retention, Langfuse trace ingestion, and disk use. Do not prune unrelated Docker networks to compensate for the host's address-pool constraint.

Follow-up consumer issues already exist and remain independently owned: [Bin Eval #1](https://github.com/kirilligum/self-imp-bin-eval/issues/1), [Analytics #11](https://github.com/prls-co/prls-analytics/issues/11), [Masked Recall #17](https://github.com/prls-co/masked-recall/issues/17), [Product Opportunity Agent #39](https://github.com/prls-co/product-opportunity-agent/issues/39), [Synthetic Dataset Agent #37](https://github.com/prls-co/synthetic-product-dataset-agent/issues/37), and [Agent Platform Infra #20](https://github.com/prls-co/agent-platform-infra/issues/20). The shared owner coordination issue is [garage-shared #1](https://github.com/prls-co/garage-shared/issues/1). These issues were already open; no duplicates were created. Keep them open until each consumer completes its own deployment.

Next: restore the public HLLM Cloudflare route, verify public health/readiness and signed artifact download, then observe the next normal write/trace cycle. Other consumer issues remain separate follow-up work.
