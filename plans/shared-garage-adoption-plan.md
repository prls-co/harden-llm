# Shared Garage adoption plan

## 1. Status and objective

- ID: `PLAN-HLLM-SHARED-GARAGE-001`.
- Updated: 2026-09-24.
- Status: H0-H3 are complete. The deployed HLLM source/configuration revision is `a4308caf4d52326f33d29bb4070842e23992703f`; `main` is now `95ce67b5628e7b352e1a4ee6bbe8bce0babba253` after plan-only PRs #64-#66. Local focused checks and `make test-fast` passed; hosted browser-free `make test-release` run `36064129358` passed. H4 moved the existing Garage volumes to shared-owner `main` `a572e183b448b0492f7980e833da0eabbe5926ac`; owner and HLLM services are healthy, production configuration is equivalent, and the artifact inventory is healthy. Phase H6 restored the existing production tunnel connector; public HLLM web/API health and readiness return HTTP 200. The connector's host start owner and neighboring routes still need verification. A signed artifact download remains unverified because the production operator login returned HTTP 403 before a session was issued; treat that as separate artifact acceptance, not evidence that the Cloudflare route is still down. Each other consumer follows independently through its existing repository issue.
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

Status: runtime cutover applied on 2026-09-24. The shared Garage and local HLLM health/storage checks pass. External public-route acceptance is blocked by Cloudflare error 1033; no other consumer repository was changed or deployed.

1. Confirmed the production checkout and owner checkout are clean, advanced `garage-shared` to `main` at `a572e183b448b0492f7980e833da0eabbe5926ac`, and advanced the production bind-mounted checkout to HLLM `main` at `a4308caf4d52326f33d29bb4070842e23992703f`. The HLLM containers had already been manually stopped before this cutover; their initiating actor is unknown.
2. Reused the existing RPC secret without rotation. The mode-`0600` owner runtime file uses that same value. A mode-`0600` Compose override suppresses Garage's host port while Bin Eval still owns loopback 3900. Rendered Compose confirmed the two existing external HLLM volumes and only `prls-observability`.
3. Started Garage with normal `/garage server` startup and no initialization flags. It reports one healthy v2.3.0 node, uses `harden-llm_garage-metadata` and `harden-llm_garage-data`, publishes no host ports, and lists the existing `harden-llm-artifacts`, `prls-loki`, `prls-allure-reports`, and `prls-agent-artifacts` buckets.
4. Switched HLLM source to `main`; the exact production check showed only the planned gateway endpoint and network differences. Applied the gateway/Caddy changes through `production-config`. Started HLLM's stopped Postgres, Prometheus, Tempo, Loki, Grafana, OTel Collector, and web services with the already approved images; Langfuse web/worker, ClickHouse, MinIO, its Postgres, and Redis stayed running.
5. A direct Compose start initially selected the shared development release for web and OTel. `production-config check` caught the mismatch before acceptance. Restored the descriptor-pinned web image/release and OTel release through the existing apply path using a temporary descriptor; the permanent descriptor was not loosened. Final `production-config check` reports `equivalent`, and all managed services are healthy.
6. Local Caddy probes for web `/healthz`, web `/login`, API `/healthz`, and API `/readyz` returned 200. The read-only `audit-artifacts` inventory reports 5/5 referenced objects, zero missing or unreferenced objects, and `healthy=true`. A Loki label query using its configured `fake` tenant returned 3 labels. An unsigned local request to the artifact host returned the expected 403 from the protected object route; a signed public download has not been verified.
7. Removed the stopped `harden-llm-garage-1` container without deleting its volumes. Removed the two obsolete `HARDEN_LLM_GARAGE_RPC_SECRET` assignments from private HLLM environment files after verifying the unchanged value matches the shared owner's RPC secret. Production Compose/source no longer defines HLLM Garage; isolated integration, smoke, and preview Garage fixtures remain.
8. Public HTTPS requests to HLLM web/API health routes initially returned Cloudflare 530, error 1033 while local Caddy routes returned 200. Phase H6 found and started the dedicated stopped production connector; the same public health/readiness routes now return 200. The initial inspection saw only `shaman-api-cloudflared-1`; its separate project/network means it is not the HLLM origin connector.

Gate: on-host Harden-LLM health and retained artifact/Loki reads pass; Garage uses the existing volumes and publishes no host port; production HLLM source is on `main`. Phase H6 restored public web/API health and readiness. Public signed-download acceptance remains open. Agent Platform's issue remains open because its client still targets the retired `garage` alias. No other consumer migration is complete.

## 9. Phase H5 — close out

Status: partially complete; operational findings are recorded here and in issues #53 and `garage-shared#1`. Public health routing has been restored. The connector's host start owner, neighboring-route checks, signed-download verification, and next normal telemetry/write observation remain separate follow-ups.

1. Record source/owner SHAs, pinned HLLM image identities, environment URLs, Compose state, and HTTP/storage results. A documentation SHA is not an application image SHA.
2. The production source cleanup, private RPC-variable cleanup, and old HLLM container removal are complete. Keep isolated fixtures and historical certification records. Execution records are merged in PR #64, and current evidence is posted to HLLM issue #53 and owner issue #1.
3. After Phase H6, recheck public HLLM web/API health and API readiness, then verify one signed artifact download. At the next normal log/trace write, confirm Loki retention/continuity and Langfuse trace ingestion; review disk use without adding backup or migration tooling.
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

Risks and follow-up checks:

- The HLLM Cloudflare route is restored: public web/API health and readiness return 200. Verify the connector's normal host start owner and known neighboring routes. Signed artifact acceptance remains open separately because the production operator login returned 403 before issuing a session.
- Agent Platform issue #20 remains open because its client still targets the retired `garage:3900` alias. Verify after that repository's own migration; no consumer source was edited here.
- Keep the shared owner's no-host-port override until Bin Eval migrates. Check port 3900 ownership before removing the override.
- The owner is one node on one host. High availability, snapshots, and backups are intentionally out of scope. Never run two Garage daemons against the adopted volumes.
- At the next normal HLLM artifact write and telemetry cycle, inspect S3 errors, Loki continuity/retention, Langfuse trace ingestion, and disk use. Do not prune unrelated Docker networks to compensate for the host's address-pool constraint.

Follow-up consumer issues already exist and remain independently owned: [Bin Eval #1](https://github.com/kirilligum/self-imp-bin-eval/issues/1), [Analytics #11](https://github.com/prls-co/prls-analytics/issues/11), [Masked Recall #17](https://github.com/prls-co/masked-recall/issues/17), [Product Opportunity Agent #39](https://github.com/prls-co/product-opportunity-agent/issues/39), [Synthetic Dataset Agent #37](https://github.com/prls-co/synthetic-product-dataset-agent/issues/37), and [Agent Platform Infra #20](https://github.com/prls-co/agent-platform-infra/issues/20). The shared owner coordination issue is [garage-shared #1](https://github.com/prls-co/garage-shared/issues/1). These issues were already open; no duplicates were created. Keep them open until each consumer completes its own deployment.

Next: verify the connector's normal host start owner and known neighboring routes. Keep signed artifact verification as a separate acceptance follow-up, then observe the next normal write/trace cycle. Other consumer issues remain separate follow-up work.

## 10. Phase H6 — diagnose and restore the public Cloudflare route

Status: RCA and immediate route recovery completed on 2026-09-24. The stopped HLLM production-tunnel connector was the direct route failure. The existing connector was started at `2026-09-24T23:41:51Z`; public web/API health and readiness return 200. DNS, application images/configuration, and Garage were not changed. The stop initiator, host start owner, and current Cloudflare-side connector inventory remain unverified. A signed artifact read is separately unverified because the operator login returned HTTP 403 before issuing a session.

### Root-cause findings

| Evidence | Observation | Assessment |
| --- | --- | --- |
| Public requests | `harden-llm.prls.co/healthz`, `harden-llm-api.prls.co/healthz`, and `harden-llm-artifacts.prls.co/` return HTTP 530; the error page reports 1033. | Failure occurs at the Cloudflare Tunnel edge, before the local application origin. Cloudflare defines 1033 as no healthy `cloudflared` instance available for the tunnel ([official troubleshooting guide](https://developers.cloudflare.com/cloudflare-one/troubleshooting/tunnel/)). |
| Local origin | HLLM's production descriptor is equivalent; Caddy, web, gateway, and shared Garage are healthy. Local Caddy health/readiness probes return 200. | HLLM application and Garage health do not explain the public 1033. Do not change app or storage code as the first repair. |
| Dedicated connector | `shaman-harden-llm-cloudflared-1`, Compose project `shaman-harden-llm`, is `exited` with exit code 0, `OOMKilled=false`, and finish time `2026-09-24T18:38:34Z`. Its logs end with a graceful SIGTERM at `18:38:33Z`, not a crash. | The known HLLM connector is stopped. Its exact stop initiator is unknown; retained Docker events did not identify the actor. |
| Tunnel identity and routes | The stopped container's mounted config identifies tunnel `b9686ab5-270b-4bd6-9aa7-a271c5a02f9d`, matching the production tunnel ID in the release record. Its ingress sends the three HLLM hostnames to `https://caddy:443`. It also serves Grafana, Langfuse, Allure, Agent Platform, Masked Recall, Product Opportunity, Synthetic Dataset, Analytics, and AI Knowledge hostnames. | This is the dedicated multi-host production connector. Restarting it has a wider public-routing effect than HLLM alone, so preflight its full existing route config and verify known neighboring routes after recovery. The config and credential values were not printed or committed. |
| Other running connector | `shaman-api-cloudflared-1` belongs to Compose project `shaman-api` and network `shaman-api_default`, not `harden-llm_harden-private`. | It cannot reach this connector's `caddy:443` origin and is not a substitute. Do not restart or reconfigure it for this repair. |
| Connector logs | Earlier QUIC and Docker-DNS resolver errors were followed by successful registered connections. Immediately before exit the resolver reported connection refused, then the process received SIGTERM. | These network errors may be contributing symptoms, but the evidence does not prove they caused the shutdown. The known connector was stopped at inspection; Cloudflare dashboard state and any other remote connectors still need checking before claiming it is the only one. |
| Host start ownership | Docker labels identify Compose file `~/.config/cloudflared/shaman-harden-llm/docker-compose.yml`; its service uses restart policy `unless-stopped`. Current system and user systemd unit listings show no matching Cloudflare/HLLM Compose unit. | The connector is running now, but its normal boot/start owner was not identified. The absent matching unit is not proof that no other startup path exists. Verify the owner before adding or changing lifecycle automation. |
| Before/after check | Starting the existing connector changed HLLM public web `/healthz`, web `/login`, API `/healthz`, and API `/readyz` from HTTP 530 to HTTP 200 without DNS or application changes. | This confirms the stopped dedicated connector caused the observed HLLM 1033 outage. |

The historical production record identifies the tunnel as `koldun-harden-llm`. The stopped connector's tunnel ID matches that record. Public DNS is Cloudflare-proxied, so ordinary `dig A` output only shows Cloudflare anycast addresses; it does not prove the current CNAME target. Do a read-only DNS/tunnel-dashboard comparison before changing DNS or creating a connector.

### Remediation sequence

1. **Confirm ownership and current Cloudflare state.** In Cloudflare's Tunnels view, read the status and connector inventory for `koldun-harden-llm` / `b9686ab5-270b-4bd6-9aa7-a271c5a02f9d`. Read the proxied DNS records for the three HLLM hostnames and confirm they target that tunnel. Compare the currently configured ingress hostnames and origins with the mounted configuration of `shaman-harden-llm-cloudflared-1`. Do not print or copy tunnel credentials. This remains aftercare; the verified tunnel ID already matched the production record before restart.
2. **Check the private origin.** Re-run `production-config check` and confirm HLLM Caddy, web, and gateway health. Confirm Caddy is reachable on `harden-llm_harden-private` with the existing private CA validation. Keep TLS verification enabled. This separates a future 502/origin failure from the current 1033/tunnel failure.
3. **Restore only the existing HLLM connector.** Complete: started the existing `shaman-harden-llm-cloudflared-1` container with its current config, credentials, image, and private network. No new tunnel or DNS/ingress change was made; `shaman-api`, HLLM application services, and Garage were not restarted.
4. **Verify tunnel recovery.** Complete for HLLM public routing: the existing connector is running, and the four public HLLM health/login/readiness paths return 200. Cloudflare dashboard connection status was not available in this session. If route errors recur, inspect sanitized diagnostics and test DNS plus outbound tunnel connectivity; do not make firewall changes without evidence. Prior raw connector logs included request query strings, so never paste unfiltered logs into chat, plans, or issues.
5. **Verify public HLLM behavior.** Public web `/healthz`, web `/login`, API `/healthz`, and API `/readyz` return 200. The unsigned artifact-host root returns 403 as expected. A signed artifact verification was attempted through the authorized API, but the production operator login returned HTTP 403 before issuing a session token; no signed URL or artifact body was accessed, and no credential value was recorded. Do not retry or provision credentials as part of this route fix. Re-run the production config check; it remains equivalent.
6. **Check the shared tunnel's blast radius.** Still pending: probe the existing production health routes for other configured hostnames with known checks and record failures as their own issues. Do not modify another consumer's source or storage as part of HLLM recovery. HLLM acceptance is not blocked by unrelated app defects, but a regression caused by this connector must be fixed before closeout.
7. **Verify lifecycle ownership and close the route incident.** Still pending: inspect the authorized host audit for why the connector received SIGTERM; if retained evidence does not identify an actor, record the trigger as unknown and stop investigating. Identify the startup path for the Compose project at `~/.config/cloudflared/shaman-harden-llm/docker-compose.yml`. Docker reports `unless-stopped`, and no matching system/user systemd unit was found, but that does not rule out another owner. If there is no supported start-on-boot path and the service owner expects one, add the smallest owner-managed startup entry only; do not add an HLLM application change, watchdog, or unconditional restart policy. Verify behavior during the next planned host maintenance, not by causing a production reboot. Record the connector state, public probes, neighboring-route results, and config/source identities here and in issue #53. Keep signed artifact authorization as a separate acceptance item.

### Acceptance and failure behavior

- **Route recovery complete:** the existing connector is running; public web `/healthz`, web `/login`, API `/healthz`, and API `/readyz` return 200; the production descriptor is equivalent; no other service or Garage state changed. This gate is met.
- **Route incident closeout:** verify the connector's owner-managed start path and check known neighboring routes for regressions. Cloudflare dashboard connection status and the stop initiator may remain unconfirmed if their audit sources have no evidence; record that limitation rather than inventing a cause.
- **Separate artifact acceptance:** a signed read of a referenced artifact succeeds and matches expected content. This remains open because the production API operator login returned 403 before issuing a session; it does not negate restored health/readiness routing.
- **Still 1033:** compare current DNS target and Cloudflare tunnel status/connector IDs. Do not change HLLM Caddy, app code, or Garage; the request still has not reached the origin.
- **Tunnel healthy but 502:** validate connector-to-Caddy DNS/network/TLS using the existing private CA. Do not disable TLS verification to mask an origin issue.
- **Hostname routes to a different tunnel:** update only the confirmed incorrect Cloudflare DNS/config entry after recording the before/after value and verifying the target tunnel. Never guess or point records at the unrelated `shaman-api` tunnel.
- **Unexpected neighboring-route regression:** do not stop the multi-host connector or restore a hypothetical saved configuration by default. Identify the failing hostname and compare its current owner-managed ingress route and origin with the intended route. Change only a confirmed incorrect route; otherwise leave the connector running and record the unrelated service failure for its owner.

No application rebuild, Garage restart, data copy, backup/snapshot, credential rotation, browser test, live-provider request, or new monitoring system is required for the immediate route repair. Public HLLM health/readiness recovery is complete; owner-start verification and neighboring-route checks close this tunnel incident. Signed-artifact acceptance remains a separate production check.
