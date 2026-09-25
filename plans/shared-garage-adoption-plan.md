# Shared Garage adoption plan

## 1. Status and objective

- ID: `PLAN-HLLM-SHARED-GARAGE-001`.
- Updated: 2026-09-24.
- Status: H0-H3 are complete and the H4 runtime cutover is applied. HLLM public health/readiness routing was restored. H4/H5 production acceptance remains open until H6 proves authenticated artifact download and production storage write/read/cleanup. The observed login HTTP 403 is not yet attributed to a response layer or cause.
- Recorded deployment: HLLM source/configuration revision `a4308caf4d52326f33d29bb4070842e23992703f`; shared-owner `main` `a572e183b448b0492f7980e833da0eabbe5926ac`. The production descriptor was equivalent and services were healthy at the recorded checks. PRs #64-#67 updated documentation, not application images.
- Retained validation: focused checks and local `make test-fast` passed; hosted browser-free `make test-release` run `36064129358` passed. Artifact inventory found 5/5 referenced keys. That inventory does not certify downloaded contents or production write permissions. Remaining acceptance is specified below; it has not run as part of this plan revision.
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

Status: verification and preparation complete. Focused Go/Compose/Node checks and local `make test-fast` passed; hosted browser-free `make test-release` run `36064129358` passed. The local release attempt passed 24 tasks but could not allocate the smoke network because Docker address pools were exhausted; no assertion failed or was weakened. Runtime configuration was applied from main `a4308caf4d52326f33d29bb4070842e23992703f`; the production descriptor is equivalent. No app image was rebuilt or pulled. The current main includes the completed plan record from PR #64; CodeQL checks passed in run `36068924182`.

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
5. Advanced the production bind-mounted checkout to `main` only after confirming the HLLM core services were stopped. Started the shared owner before HLLM clients and retained a same-volume rollback procedure. Source config was merged in PR #54; execution records are merged in PR #64. The production checkout was later fast-forwarded to `0a0349a2c826434dedc5fb4cbe5b91480a77ee85`, a plan-only update.

Gate: passed for source, hosted browser-free release, production config, and local storage/readiness checks. Public edge acceptance remains in H4. No browser or real-provider test was added; configuration-only deployment reused the approved images.

## 8. Phase H4 — participate in the shared cutover

Status: runtime cutover applied on 2026-09-24. Shared Garage, local HLLM health, and object inventory checks passed. Phase H6 restored public health/readiness routing after the 1033 outage. Authenticated artifact download and production write/read/cleanup remain required for acceptance; no other consumer repository was changed or deployed.

1. Confirmed the production checkout and owner checkout are clean, advanced `garage-shared` to `main` at `a572e183b448b0492f7980e833da0eabbe5926ac`, and advanced the production bind-mounted checkout to HLLM `main` at `a4308caf4d52326f33d29bb4070842e23992703f`. The HLLM containers had already been manually stopped before this cutover; their initiating actor is unknown.
2. Reused the existing RPC secret without rotation. The mode-`0600` owner runtime file uses that same value. A mode-`0600` Compose override suppresses Garage's host port while Bin Eval still owns loopback 3900. Rendered Compose confirmed the two existing external HLLM volumes and only `prls-observability`.
3. Started Garage with normal `/garage server` startup and no initialization flags. It reports one healthy v2.3.0 node, uses `harden-llm_garage-metadata` and `harden-llm_garage-data`, publishes no host ports, and lists the existing `harden-llm-artifacts`, `prls-loki`, `prls-allure-reports`, and `prls-agent-artifacts` buckets.
4. Switched HLLM source to `main`; the exact production check showed only the planned gateway endpoint and network differences. Applied the gateway/Caddy changes through `production-config`. Started HLLM's stopped Postgres, Prometheus, Tempo, Loki, Grafana, OTel Collector, and web services with the already approved images; Langfuse web/worker, ClickHouse, MinIO, its Postgres, and Redis stayed running.
5. A direct Compose start initially selected the shared development release for web and OTel. `production-config check` caught the mismatch before acceptance. Restored the descriptor-pinned web image/release and OTel release through the existing apply path using a temporary descriptor; the permanent descriptor was not loosened. Final `production-config check` reports `equivalent`, and all managed services are healthy.
6. Local Caddy probes for web `/healthz`, web `/login`, API `/healthz`, and API `/readyz` returned 200. The read-only `audit-artifacts` inventory reports 5/5 referenced objects, zero missing or unreferenced objects, and `healthy=true`. A Loki label query using its configured `fake` tenant returned 3 labels. An unsigned local request to the artifact host returned the expected 403 from the protected object route; a signed public download has not been verified.
7. Removed the stopped `harden-llm-garage-1` container without deleting its volumes. Removed the two obsolete `HARDEN_LLM_GARAGE_RPC_SECRET` assignments from private HLLM environment files after verifying the unchanged value matches the shared owner's RPC secret. Production Compose/source no longer defines HLLM Garage; isolated integration, smoke, and preview Garage fixtures remain.
8. Public HTTPS requests to HLLM web/API health routes initially returned Cloudflare 530, error 1033 while local Caddy routes returned 200. Phase H6 found and started the dedicated stopped production connector; the same public health/readiness routes now return 200. The initial inspection saw only `shaman-api-cloudflared-1`; its separate project/network means it is not the HLLM origin connector.

Gate: on-host Harden-LLM health, artifact inventory, and the recorded Loki query passed; Garage uses the existing volumes and publishes no host port; production HLLM source is from `main`. Public web/API health and readiness were restored. H6 must still prove authenticated artifact download and production write/read/cleanup before H4 is fully accepted. Agent Platform's issue remains open because its client still targets the retired `garage` alias. No other consumer migration is complete.

## 9. Phase H5 — close out

Status: partially complete; operational findings are recorded here and in issues #53 and `garage-shared#1`. Execute H6.A-H6.C to finish HLLM production acceptance. Docker's existing startup and restart configuration is sufficient; a new startup service, host reboot, and Cloudflare dashboard inspection are not acceptance gates.

1. Record source/owner SHAs, pinned HLLM image identities, environment URLs, Compose state, and HTTP/storage results. A documentation SHA is not an application image SHA.
2. The production source cleanup, private RPC-variable cleanup, and old HLLM container removal are complete. Keep isolated fixtures and historical certification records. Execution records are merged in PR #64, and current evidence is posted to HLLM issue #53 and owner issue #1.
3. Complete H6.A-H6.C in order and use their evidence for closeout; do not repeat their checks just to fill this phase. Record the 403 diagnosis (or explicitly that it was not reproduced), authenticated download integrity, temporary-object write/read/deletion, session cleanup, production identities, and final public probes. Future routine log/trace continuity and disk checks are normal operations, not an indefinite acceptance wait.
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
- 2026-09-24 — Public HLLM hosts initially returned Cloudflare HTTP 530 / error 1033. Local Caddy probes passed. The running `shaman-api-cloudflared-1` connector uses `shaman-api_default`; its HLLM route ownership was not established and it was not restarted. Phase H6 records recovery through the separate HLLM connector.
- 2026-09-24 — A direct Compose start briefly selected the shared development release for web/OTel. Production-config detected the identity mismatch; the exact descriptor-pinned web image and OTel release were restored through the existing apply code with a temporary descriptor. The permanent descriptor was not changed, no image was pulled, and final `production-config check` is equivalent.
- 2026-09-24 — Removed the old stopped `harden-llm-garage-1` container without `-v`; existing volumes remain mounted by the shared owner. Removed the obsolete Garage RPC assignment from both mode-`0600` HLLM env files only after value equality with the unchanged shared-owner secret was verified privately. No key was rotated and no value was recorded. A stale isolated-install test container and its two test-only volumes were also removed.
- 2026-09-24 — Review confirmed Docker is enabled and active, and the running tunnel connector uses `unless-stopped`. The proposed additional startup service and reboot check were removed. Source review showed the login handler returns 401 for invalid credentials, while the observed public response was 403; its source remains unclassified. The revised remaining phases diagnose that response and require real download and write/read/cleanup evidence. This revision adds no new runtime result.

Risks and follow-up checks:

- Public HLLM health/readiness routing was restored. Diagnose the observed login 403 before changing credentials or access rules. Authenticated artifact download and a temporary-object storage write/read/deletion remain release requirements, not deferred follow-ups.
- Revoke every session created by acceptance checks and delete only the uniquely named temporary object. Failed cleanup keeps acceptance open; never broaden deletion to a prefix, bucket, or retained artifact.
- Agent Platform issue #20 remains open because its client still targets the retired `garage:3900` alias. Verify after that repository's own migration; no consumer source was edited here.
- Keep the shared owner's no-host-port override until Bin Eval migrates. Check port 3900 ownership before removing the override.
- The owner is one node on one host. High availability, snapshots, and backups are intentionally out of scope. Never run two Garage daemons against the adopted volumes.
- At the next normal HLLM artifact write and telemetry cycle, inspect S3 errors, Loki continuity/retention, Langfuse trace ingestion, and disk use. Do not prune unrelated Docker networks to compensate for the host's address-pool constraint.

Follow-up consumer issues already exist and remain independently owned: [Bin Eval #1](https://github.com/kirilligum/self-imp-bin-eval/issues/1), [Analytics #11](https://github.com/prls-co/prls-analytics/issues/11), [Masked Recall #17](https://github.com/prls-co/masked-recall/issues/17), [Product Opportunity Agent #39](https://github.com/prls-co/product-opportunity-agent/issues/39), [Synthetic Dataset Agent #37](https://github.com/prls-co/synthetic-product-dataset-agent/issues/37), and [Agent Platform Infra #20](https://github.com/prls-co/agent-platform-infra/issues/20). The shared owner coordination issue is [garage-shared #1](https://github.com/prls-co/garage-shared/issues/1). These issues were already open; no duplicates were created. Keep them open until each consumer completes its own deployment.

Next: execute H6.A to locate the 403, H6.B to prove artifact and storage behavior, and H6.C to close out. Other consumer issues remain independently owned follow-up work.

## 10. Phase H6 — diagnose and restore the public Cloudflare route

Status: immediate route recovery is complete. Starting the existing connector at `2026-09-24T23:41:51Z` restored public web/API health and readiness without changing DNS, application images/configuration, or Garage. The initiating cause of its SIGTERM remains unknown. H6.A-H6.C below are pending and finish the original HLLM shared-Garage acceptance task.

### Root-cause findings

| Evidence | Observation | Assessment |
| --- | --- | --- |
| Public routing before/after | Public HLLM hosts returned 530/1033 while local Caddy health/readiness returned 200. Starting the existing connector changed public web `/healthz`, web `/login`, API `/healthz`, and API `/readyz` to 200. | The stopped connector explains the observed route outage. Cloudflare defines 1033 as no healthy connector available ([official troubleshooting guide](https://developers.cloudflare.com/cloudflare-one/troubleshooting/tunnel/)). Health endpoints do not certify authenticated artifact access. |
| Termination | `shaman-harden-llm-cloudflared-1` exited with code 0, `OOMKilled=false`, at `2026-09-24T18:38:34Z`, following SIGTERM at `18:38:33Z`. Retained Docker events did not identify an actor. Earlier DNS/QUIC errors were also followed by successful connections. | The immediate failure is established; the stop trigger is unknown. Neither those network errors nor SIGTERM alone identifies who stopped it or why. |
| Tunnel and affected routes | Mounted configuration names tunnel `b9686ab5-270b-4bd6-9aa7-a271c5a02f9d`, matching the historical `koldun-harden-llm` record, and sends HLLM hosts to `https://caddy:443`. It also serves Grafana, Langfuse, Allure, Agent Platform, Masked Recall, Product Opportunity, Synthetic Dataset, Analytics, and AI Knowledge hosts. | Check known neighboring routes once after recovery. The separate `shaman-api` project/network is not the verified HLLM connector and was untouched. |
| Existing startup behavior | Docker is enabled and active. The running connector uses `unless-stopped` from `~/.config/cloudflared/shaman-harden-llm/docker-compose.yml`. | Docker already owns normal restart and boot behavior; explicitly stopped containers remain stopped. A missing separate systemd unit is not a defect. Retain the existing policy; add no startup service or reboot gate ([Docker restart policy documentation](https://docs.docker.com/engine/containers/start-containers-automatically/)). |
| Login response | An attempted public operator login returned 403 and yielded no session token. The reviewed `internal/gateway/httpapi/httpapi.go` login handler maps invalid credentials to 401. | The 403 is unclassified. Confirm the deployed revision and response source before blaming credentials, changing access rules, or treating authentication as a deferred task. |
| Artifact inventory | `audit-artifacts` reported 5/5 referenced objects and `healthy=true`. `internal/gateway/command/artifact_inventory.go` compares object listings with database references. | This proves inventory consistency for that scan. It does not prove object-body integrity, authenticated public downloads, or write permissions. H6.B provides those checks. |

### H6.A — identify and resolve the login 403

Reason: the remaining acceptance attempt stopped before reaching an artifact. Find the response's source so the repair addresses the failing layer and preserves existing accounts and access rules.

Reference files: `api/openapi.yaml` (`POST /api/v1/auth/login`, session/logout routes), `internal/gateway/httpapi/httpapi.go` (`login`), `internal/gateway/auth/service.go` (`Login`), `deploy/caddy/Caddyfile`, and `scripts/preview-environment.mjs` (`authCheck` request shape).

1. Confirm the deployed gateway revision and inspect its login handler. Reconstruct the failed request's method, host, path, JSON fields, and content type. Use the production operator credentials from the private approved configuration; distinguish them from the guest `TEST_LOGIN`/`TEST_PASSWORD` pair.
2. Compare one bounded public request with the equivalent request through the existing verified Caddy origin route. Preserve the API host/SNI, JSON `email`/`password`, and `Content-Type: application/json`; validate TLS with the existing private CA. Record only status, safe response headers, request ID, and redacted error classification. Keep credentials, cookies, tokens, signed URLs, and raw diagnostic bodies out of output and source files.
3. Classify the difference. Public 403 with origin success points to the public request path; inspect the specific edge/access/routing response. Origin 401 calls for checking the existing account/configuration; origin 400 calls for checking the request; origin 503 calls for dependency inspection. A 403 at the origin requires finding the actual responding middleware/service. If the contract-correct request succeeds through both paths, record the earlier 403 as not reproduced unless evidence explains it, and proceed to H6.B. None of these statuses alone authorizes credential rotation, broad access-rule removal, or account recreation.
4. Fix only the demonstrated cause. A malformed probe needs a corrected request; a confirmed source defect needs its smallest fix and a cheap regression under the existing canonical test IDs; a confirmed deployment defect needs a scoped configuration correction. Repeat the request after that change, not as blind retries. Revoke every successful diagnostic session in cleanup, including sessions created by origin comparisons.
5. For application code changes, run the affected tests and `make test-fast` with the pinned toolchain, then merge the verified fix to `main` and deploy the affected component using its existing deployment path. For configuration changes, validate the affected configuration and public behavior. Retain H3 certification for unchanged implementation; repeat an expensive gate only for a newly changed boundary. Documentation alone requires `git diff HEAD --check`, not an application rebuild.

Gate: the intended production account can log in through the public API and `GET /api/v1/auth/session` returns 200. Record the proven cause/correction, or the successful request evidence and explicit limitation that the earlier 403 was not reproduced. If the required credential or operator access is unavailable, record the exact missing input; HLLM acceptance stays open. An HTTP 403 alone is not an explanation.

### H6.B — prove authenticated downloads and production storage operations

Reason: one existing artifact exercises ownership checks, signing, the public route, and retained content. One temporary object exercises the deployed write/read permissions without invoking an LLM provider. Inventory alone proves neither behavior.

Reference files: `api/openapi.yaml` (history, trace, artifact routes), `internal/smoke/harness_compose.go` (`artifactLocation`, `fetchArtifact` as assertion references), `internal/artifacts/garage.go` (`Put`, `Get`, `Inspect`, `DeleteMany`), and `artifacts.go` (SHA-256 and byte-count metadata). Reuse the request/client logic, not the full smoke stack or its provider fixtures.

1. Create a fresh authorized session after H6.A, keep its token in memory, and register logout cleanup. Select an available existing artifact owned by that account through history/trace metadata. Read its stored expected SHA-256 and byte count before downloading. Preserve owner authorization; do not seed fake production metadata to manufacture a fixture. If no usable artifact is available to an authorized account, record that specific fixture gap.
2. Request `GET /api/v1/traces/{traceID}/artifacts/{artifactID}` with that session and redirects disabled. Require 303 and a signed HTTPS location on the configured public artifact host. Fetch that location with a separate request without the API bearer token; require 200 and a SHA-256/byte-count match against the stored metadata. Do not print the signed location or artifact body. Verify that the same object URL without its signature is denied; an unsigned host-root 403 is insufficient.
3. Run existing Garage client/SDK tooling from the gateway's storage network path using its effective endpoint, bucket, region, and current bucket credentials. Write a small known JSON payload under a unique key such as `acceptance/hllm-shared-<uuid>.json`, after confirming that key is absent. Use the existing immutable/conditional write behavior. Read it back and require exact bytes, expected SHA-256, and byte count. This is a temporary storage check; it adds no production metadata, provider request, permanent command, or service.
4. In cleanup, delete only that exact temporary key with `DeleteMany` (one key) and confirm absence with `Inspect`. Track the key before attempting the write so a timeout still permits checking and cleaning up that object. Preserve an operation's original failure even when cleanup succeeds; record cleanup failures explicitly. Never delete an existing artifact, prefix, bucket, or volume.
5. Revoke the acceptance session through the existing logout endpoint and confirm it is no longer accepted. Report only statuses, integrity-match results, and cleanup results. Do not retain bearer material or downloaded production content.

Gate: existing owner-authorized public artifact download matches stored metadata; unsigned access to that object is denied; the temporary production object passes write/read integrity and confirmed deletion; all created sessions are revoked. A direct S3 success cannot substitute for the authenticated public download, and a download cannot substitute for the write check. Any failure keeps HLLM acceptance open.

### H6.C — confirm final health and close out once

Reason: confirm that the scoped repair preserved the deployed configuration and the other routes sharing its connector. Record completion with the checks above instead of introducing a new lifecycle system or waiting indefinitely for future traffic.

1. Run the existing production configuration check and require `equivalent`; confirm the shared owner and HLLM services are healthy. Require public web `/healthz`, web `/login`, API `/healthz`, and API `/readyz` to return 200.

   ```sh
   node scripts/production-config.mjs check --descriptor /home/kirill/.config/harden-llm/production.json
   ```

2. Make one bounded pass over known health routes for the existing neighboring ingress hostnames, using their documented expected responses. Identify any failure caused by this repair and correct it before closeout. Record unrelated or previously present consumer defects with their existing owner/issue; their Garage migrations remain outside this task.
3. Preserve the distinction between the confirmed stopped-connector failure and its unknown SIGTERM trigger. Retained Docker events already lacked an actor. Use only available, narrowly relevant audit evidence if it identifies one; absence of that evidence does not block completion. Cloudflare dashboard/DNS inspection is diagnostic work only when an observed edge problem requires it, not an extra release gate. Preserve the existing Docker restart policy; no additional startup service, watchdog, host reboot, or boot-maintenance gate is required.
4. Update this plan's execution record and existing transition tracking once with the 403 diagnosis or non-reproduction evidence, required test results for any fix, source/config/image identities, public endpoints, H6.B integrity and cleanup results, and neighboring-route results. Mark H4-H6 accepted only when all required gates pass. Keep other repositories' migration issues independent. Future normal telemetry continuity and disk checks remain routine follow-up observations.

Final acceptance: H6.A public login/session, H6.B authenticated artifact download and temporary-object write/read/cleanup, H6.C public health and production configuration, and no introduced neighboring-route regression. Source tests and existing inventory results support these checks but do not replace them. No browser or live-provider call is needed.

### Failure handling and bounded scope

- **403 still unclassified:** keep the recorded response classification and public/origin comparison; report the exact unresolved layer or missing operator access. Do not infer a credential defect or declare artifact acceptance complete.
- **Artifact mismatch or storage permission failure:** preserve the failed assertion and diagnose the specific metadata, signing, public routing, endpoint, or bucket-permission boundary. Clean up the owned temporary object and sessions; do not compensate by weakening the check.
- **1033 recurs:** inspect the verified connector and, if needed, Cloudflare tunnel/DNS state. The configured tunnel ID is known; ordinary proxied `dig A` answers do not reveal its target. Do not switch to the unrelated `shaman-api` connector.
- **502 after tunnel recovery:** validate connector-to-Caddy DNS/network/TLS with the existing private CA; keep TLS validation enabled.
- **Neighboring-route regression:** identify the failing hostname and its intended origin before making a narrow route correction. Do not stop the shared connector or restore a hypothetical saved ingress configuration by default.
- **Cleanup fails:** retain the exact owned key/session identification privately and report the cleanup failure. Acceptance remains open until the owned temporary object and sessions are removed/revoked.

This remaining work uses existing application, storage, deployment, and verification paths. It adds no backup/snapshot feature, secret rotation, new monitoring system, alternate Garage alias, or consumer migration.
