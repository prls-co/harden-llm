# Shared Garage adoption plan

## 1. Status and objective

- ID: `PLAN-HLLM-SHARED-GARAGE-001`.
- Updated: 2026-09-24.
- Status: H0-H2 implementation is merged to `main` in PR #54 (`73fc735`); hosted fast/release, post-merge fast, and CodeQL checks passed. Production still runs the old Harden-LLM Garage. The user selected an HLLM-first sequence; each other consumer follows independently through its own repository issue. The source/test audit and fresh requested gates are the next steps; no production runtime has moved.
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

Status: source implementation is merged; fresh requested gates and runtime apply remain pending. Hosted fast/release checks passed on implementation commit `411552c` (runs `35973663949`, `35973668157`, `35974172600`); post-merge main fast T0-T2 and CodeQL checks passed after PR #54 merged as `73fc735` (runs `35994817092`, `35994816817`). Plan/status updates #55 (`3925377`) and #56 (`81c0465`) are merged; their CodeQL runs passed. The private production descriptor allows only the planned gateway network difference and Caddy/Loki mounted-config content differences. Its check against current live-root branch `d294a96` reports equivalent and makes no change; that is the old source, not the target. A temporary descriptor pointed at a separate main checkout was read-only and showed path differences caused by that checkout. Final config check must run after the live bind-mounted checkout is advanced to `main` while affected clients are stopped. In the current rerun, `make test-fast` passed all 10 tasks after using a disk-backed temp directory. Local `make test-release` passed 24/28 tasks; its first failure was Docker network allocation exhaustion before smoke containers started, and three later tasks were cancelled. No test assertion failed or was changed. Get a fresh hosted release result before promotion.

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

Status: pending. This phase deploys Harden-LLM only; other persistent Garage owners remain untouched.

1. Refresh exact container/image identities, current Garage node/layout/health, volume names, bucket names/counts, network membership, and production descriptor. Confirm `garage-shared` source is merged `main` and its image/config match the current daemon. Recheck that Bin Eval still uses host port 3900.
2. Fast-forward the clean local `garage-shared` checkout to merged `origin/main`. Compare the running old daemon's RPC secret to the private HLLM env value without printing it. Prepare `/home/kirill/.config/garage-shared/runtime.env` with that same secret, mode `0600`; do not rotate it or record the value. Prepare a mode-`0600` Compose override containing `services.garage-shared.ports: !reset []`; render the combined owner Compose graph and verify it uses the two existing HLLM volumes and only `prls-observability`.
3. Stop only HLLM's `caddy`, `loki`, and `harden-llm-gateway`, then stop old `harden-llm-garage-1`. Confirm the old daemon is stopped before opening either volume. Do not stop Bin Eval Garage, Analytics Garage, tests, previews, or unrelated apps.
4. Start `garage-shared` with normal `/garage server` startup (no initialization flags) and the private override. Wait for healthy status; verify the retained node/layout and expected existing buckets. Never initialize or rename the adopted volumes.
5. Switch the production bind-mounted checkout to merged `main` while affected services are stopped. Run `production-config check`, then scoped `production-config apply` for `caddy,loki,harden-llm-gateway`. Reuse approved image identities; do not rebuild application images for a configuration-only change.
6. Verify HLLM readiness, gateway and Caddy resolution of `garage-shared`, a preexisting artifact read, a new scoped artifact write/read and signed public download, and Loki S3 write/query with existing bucket credentials. Keep signed URLs and secrets out of logs. Do not call a live LLM provider or launch a browser.
7. Confirm production Compose has no HLLM-owned Garage service, Garage volumes, or Garage dependency; remove the stopped old container without `-v` only after acceptance. Confirm integration/smoke/preview fixtures still own fresh isolated Garage storage.

Gate: Harden-LLM is healthy on `garage-shared`; existing HLLM artifact/Loki data remains readable; the private owner uses the retained volumes and publishes no host port; production HLLM source is on `main`. Record the Agent Platform interruption and keep its open issue as follow-up. Do not claim any other consumer migration is complete.

## 9. Phase H5 — close out

Status: pending.

1. Record `main` SHA, owner SHA/image, HLLM image identities, environment URLs, Compose project/service state, and HTTP/storage results. A documentation SHA is not an application image SHA.
2. Remove only production-owner instructions/configuration that still claim Harden-LLM owns Garage. After acceptance, remove the unused `HARDEN_LLM_GARAGE_RPC_SECRET` entry from the two private HLLM env files; the owner keeps the unchanged value as `GARAGE_RPC_SECRET`. Keep isolated fixtures and historical certification records. Update this plan and issue #53; update the central owner plan with the HLLM-first outcome.
3. Record problems/fixes and aftercare: artifact reads, signed downloads, S3 errors, readiness, Loki continuity/retention, and disk usage after the next normal run/release. Keep other consumer issues open until their own deployed checks pass.
4. Verify the source is pushed/merged to `main`, production is on that source, and the old HLLM Garage container is removed without deleting volumes. Do not claim the overall multi-repo transition is complete.

If shared Garage fails before acceptance, stop it first, switch the stopped production checkout back to the recorded pre-cutover revision, restart the old HLLM Garage on the same volumes, then restore affected HLLM services. Do not run two daemons against the adopted volumes. No data backup or snapshot is part of this plan.

Preparation evidence: source, pinned CLI startup help, and selected live Docker identities were reviewed. H0-H2 source implementation is merged and browser-free checks are recorded below. The latest read-only inventory still shows the old Harden-LLM, Bin Eval, and Analytics persistent Garage containers; `garage-shared` is not running. No production runtime change, data copy, or deployment has occurred.

### Execution log

- 2026-09-24 — H0 complete: read repository instructions and the full test feedback policy. The production checkout is a bind-mounted source, so all implementation remains in `/home/kirill/p/harden-llm-shared-garage` until the coordinated cutover.
- 2026-09-24 — H1 source changes: removed the production Garage service, root Garage volumes, dependencies, image-lock entry, and obsolete HLLM RPC environment variable. Gateway, Caddy, and Loki now target `garage-shared:3900`; the gateway retains its private network and explicitly joins `prls-observability`. The production test still asserts an exact service list and image manifest.
- 2026-09-24 — H2 source changes: moved Garage configuration under `deploy/test/garage.toml`, updated integration/preview references, and made smoke provide a fresh isolated Garage fixture that owns the `garage-shared` alias on a unique non-external observability network. Smoke assertions now inspect the endpoint, image/command, aliases, ports, named volumes, and network identity.
- 2026-09-24 — H1/H2 checks passed: `go test ./internal/deploytest ./internal/testkit -count=1`; `go test ./internal/deploytest/... -tags=compose -run TestComposeCaddyContract -count=1`; and a synthetic full smoke Compose render with disposable values (no containers started). `git diff HEAD --check` passed.
- 2026-09-24 — `make test-fast` was attempted twice. The first run hit `disk quota exceeded` because `/tmp` was 97% full; the second moved Go's temp directory to the persistent filesystem and passed nine of ten tasks, with `frontend-deterministic` failing during isolated `quic` dependency compilation. A direct `MIX_ENV=test mix deps.compile quic --force` using the same pinned Elixir/OTP and task build path passed after that failure, so no test assertion or source code was changed. Hosted CI is required for a clean full-gate result; `make test-release` remains pending. A temporary dependency symlink to the clean main checkout was used because `mix.lock` hashes matched and must be removed before commit.
- 2026-09-24 — shared owner source is pushed at `garage-shared/dd5d1ece1bee5a8075878e0d5fc656913e95a63d`; its GitHub Actions config check passed. This does not establish a production runtime change.
- 2026-09-24 — H1/H2 source is pushed in Harden-LLM PR #54 at `411552c2aea4a2e45018f19aff0279dabba96ee1`. Exact-head hosted fast T0-T2 and browser-free release gates passed (runs `35973663949`, `35973668157`, `35974172600`). The current shared-owner candidate is `garage-shared` PR #2 at `e0b507c`, replacing the earlier owner candidate recorded above; its hosted Compose config check passed in run `35990868197`. Source checks do not establish production deployment.
- 2026-09-24 — The canonical plan identified one concrete production blocker outside Harden-LLM: Bin Eval's active default Garage key matches a value in its public `.env.example`. No value was copied or changed. The coordinated transfer needs the user's decision on replacing only that exposed key; no blanket key rotation is proposed. At that checkpoint the decision was still pending; the user's later preserve-unchanged choice, recorded below, resolves this credential decision.
- 2026-09-24 — Harden-LLM PR #54 was merged to `main` as `73fc73584a7095ba95538c9a7291553e0d8ca2c5`. Its implementation commit passed the recorded browser-free release gate; post-merge fast T0-T2 and CodeQL checks passed. The merge did not apply the production descriptor, start Garage, copy data, or deploy containers.
- 2026-09-24 — Harden-LLM phase-status checkpoint: H0-H2 are complete and merged. H3 source gates passed, including the exact service/image topology checks and browser-free release selector; private production descriptor cleanup, clean rollback inputs, and immediate preflight remain pending. H4 is blocked on the central cutover. Plan/status PR #55 merged at `3925377` after all CodeQL checks passed (`35995455079`). Ops documentation PR #5 merged at `d96f74b`. Read-only Docker inventory still shows the old three persistent owners and no `garage-shared`; no runtime was changed.
- 2026-09-24 — Production preflight preparation: the private descriptor at `/home/kirill/.config/harden-llm/production.json` now permits `networks` for `harden-llm-gateway` and `mount-contents` for Caddy/Loki, matching only the planned H1 changes. The current live-root `production-config check` reports equivalent, applied no changes, and verifies runtime identities. A temporary descriptor pointed at main source `3925377` to inspect target differences; it reported the expected gateway artifact endpoint/network change and temporary path differences, then stopped without apply. The production source checkout is a bind-mounted path and remains on `d294a96`; changing it before stopping Caddy/Loki would alter live config. The final same-path check and apply are still pending.

- 2026-09-24 — Analytics PR #12 at `568590b`, which pins Masked Recall `fabee32`, passed both exact-head hosted checks (`36014590252`, `36014584475`), including all nine portal/browser journeys. Masked Recall PR #20 at `da527c8` pins Analytics `568590b`; its exact-head checks (`36015017366`, `36015009635`) remain in progress. Earlier Masked Recall full-flow runs failed on Temporal-facing checks (workflow POST 503 and portal fixture worker-deployment context deadline); their cause is unknown. A fresh isolated local Temporal/Garage E2E passed five repetitions. Do not claim the full Masked Recall gate is green from that narrower result or Analytics's consumer pass.
- 2026-09-24 — The user chose to preserve the Bin Eval key unchanged after its value was found in the public `.env.example`. It remains unchanged and uncopied. The shared Garage cutover must carry that exact key privately and retain its bucket-only grant.
- 2026-09-24 — Bin Eval main CI run `36006286297` has a passing deterministic job and queued live job. GitHub reports `shaman-bin-eval-live` offline while local systemd reports `prls-ci-runner.service` active as `prls-ci`. Restart and noninteractive sudo require unavailable interactive host authorization; no process was started or stopped. The merged dynamic-port fix removes the previous test-port collisions.
- 2026-09-24 — Read-only runtime inventory still shows three persistent Garage owners (Harden-LLM, Bin Eval, PRLS Analytics) and no `garage-shared` container. No production container, network, volume, credential, or object changed.

Risks and follow-up checks:

- Current production bind mounts point at `/home/kirill/p/harden-llm`, which is not on `main`. Stop affected services before switching source; keep their current image identities unchanged.
- The old daemon's RPC secret currently appears in both private HLLM env files. Verify it matches the running daemon, move the same value to the shared owner's mode-`0600` runtime file, and remove the obsolete HLLM entries only after successful acceptance.
- Bin Eval's old Garage owns host port 3900. The HLLM-first owner must not publish that port. Keep the no-host-port override with private owner deployment inputs until Bin Eval moves.
- Agent Platform runtime clients currently use the old `garage:3900` alias on `prls-observability`; replacing the old HLLM daemon removes that endpoint. Its service may fail artifact operations until Agent Platform issue #20 is deployed. Do not change its code here.
- The adopted Garage is one node on one host. No high-availability or backup feature is being added. Rollback reopens the same volumes with the old daemon after the new daemon stops.
- The shared host's `/tmp` tmpfs is 98% full and Docker's default address pools are exhausted. Go temporary files can use `/var/tmp`; do not prune other applications' networks. Use the browser-free hosted release workflow for a clean full-selector result.
- Recheck public artifact signing and Loki S3 access after cutover. Keep tests/previews isolated and ensure HLLM production Compose cannot recreate its own Garage.

Follow-up consumer issues already exist and remain independently owned: [Bin Eval #1](https://github.com/kirilligum/self-imp-bin-eval/issues/1), [Analytics #11](https://github.com/prls-co/prls-analytics/issues/11), [Masked Recall #17](https://github.com/prls-co/masked-recall/issues/17), [Product Opportunity Agent #39](https://github.com/prls-co/product-opportunity-agent/issues/39), [Synthetic Dataset Agent #37](https://github.com/prls-co/synthetic-product-dataset-agent/issues/37), and [Agent Platform Infra #20](https://github.com/prls-co/agent-platform-infra/issues/20). The shared owner coordination issue is [garage-shared #1](https://github.com/prls-co/garage-shared/issues/1). These issues are open; do not create duplicates.

- 2026-09-24 — Analytics PR #12 merged to main as dce9a9ceca67ba3f8f97ebc8489e0f925b63937f; its exact-head checks passed (36014590252, 36014584475). Masked Recall PR #20 now pins that merged SHA at 7e9e25e386caef0e8d4b0bc5040743c7b164d431; exact-head full gates 36018240398 and 36018231427 are still running. The previous head da527c8 passed both full gates, but the final stable-pin source must pass before merge. Bin Eval's live job 36006286297 remains queued because shaman-bin-eval-live is offline. No Garage runtime or key has changed.

- 2026-09-24 — Masked Recall PR #20 merged to main as 1f945aea00f0c3050c3b8c1fea3b1b4a3c8c89ef. Its exact-head full gates passed (36018231427, 36018240398), as did main gate 36021305039 (22m44s). Analytics PR #13 (2a6170b) updates the Analytics source pin to this SHA; its exact-head gates 36024079138 and 36024085726 are running. Bin Eval live gate 36006286297 remains queued because its dedicated runner is offline. No production runtime, volume, object, or key has changed.

- 2026-09-24 — Analytics PR #13 merged as 0c31ed3 after its exact-head gates passed (36024079138, 36024085726); main push gate 36027536399 is running. Masked Recall exact-head and main gates passed on merged revision 1f945aea. The one-time S3 copy procedure was validated on two isolated Garage v2.3 instances with the existing Analytics AWS SDK: two objects/8,229 bytes copied and SHA-256/metadata verified, then rerun idempotently; test volumes and networks were removed. No production data/runtime/key changed. Bin Eval live CI remains queued while its runner is offline.
- 2026-09-24 — Analytics main run 36027536399 failed in `tests/integration/model-recall-preparation.test.ts` `beforeAll` after 120 seconds; the 22 tests in that suite did not execute, while 25 other test files passed. Its exact-head PR #13 gates passed (36024079138 and 36024085726). Analytics PR #14 at 82182f8 moves compilation of the same real MRR integration test binary to a separate named CI step and reuses it in integration/browser tests; local typecheck, build, YAML parse, and whitespace checks passed, with full hosted checks pending. Logs do not prove this compile caused the timeout. No production service, volume, object, or credential changed.
- 2026-09-24 — Analytics PR #14 merged as 1fa95d7. Exact-head CI runs 36030720161 and 36030794634 passed; merged-main run 36032465849 passed including the real integration suite (114 passed, one existing opt-in skip), browser journeys, topology and image builds. The stand-alone producer compile measured 98s, 91s, and 102s; this leaves the exact stalled await in old run 36027536399 unknown, while removing compilation from the 120-second fixture hook. No production service, volume, object, or credential changed. Bin Eval live run 36006286297 remains queued on its offline runner.

- 2026-09-24 — User selected a one-at-a-time rollout with Harden-LLM first. Existing follow-up issues are open in Bin Eval #1, Analytics #11, Masked Recall #17, Product Opportunity Agent #39, Synthetic Dataset Agent #37, and Agent Platform Infra #20; no duplicate issues are needed. Live inspection confirmed the HLLM source checkout is not on `main`, the old HLLM Garage is healthy on the existing named volumes, `garage-shared` is absent, and Bin Eval's separate Garage owns loopback port 3900. Agent Platform still resolves the old `garage` alias on `prls-observability`; its temporary interruption is called out above. No service, volume, object, key, or other repository source was changed.

- 2026-09-24 — Fresh HLLM validation on main-based worktree `codex/hllm-first-shared-garage`: focused Go topology/fixture tests, TEST-033 Compose contract, 25 preview/production-config Node tests, and `git diff --check` passed. Private comparison confirmed the current Garage container RPC secret exactly matches both private HLLM env sources; only booleans were printed. First `make test-fast` attempt hit `/tmp` quota in Go link; the rerun used task-owned `/var/tmp` space and passed all 10 tasks after fetching only the lockfile-pinned frontend dependencies into this isolated worktree. `make test-release` passed 24 tasks, then its smoke Compose task failed before container start because Docker reported all predefined address pools subnetted; three later tasks were cancelled by the test selector. No unrelated networks were pruned. A hosted release run is required for a clean environment result. No production service, volume, object, or secret changed.

Next: dispatch and pass the browser-free release workflow for this branch, merge the plan/status to `main`, then perform the bounded HLLM-only production cutover. Stop if retained volume identity, node/layout, existing bucket inventory, shared health, artifact access, or Loki checks differ from the recorded baseline.
