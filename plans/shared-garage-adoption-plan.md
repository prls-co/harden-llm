# Shared Garage adoption plan

## 1. Status and objective

- Plan: `PLAN-HLLM-SHARED-GARAGE-001`.
- Status: planned; no consumer configuration or live service was changed in this pass.
- Date: 2026-09-23.
- Target service owner: private repository [`prls-co/garage-shared`](https://github.com/prls-co/garage-shared).
- Objective: remove physical ownership of the shared Garage daemon from this application repository while preserving the current S3 service contract and its existing data volumes.

The service already is shared: Agent Platform containers on `prls-observability` use the Garage instance currently declared in this repository. The new repository centralizes ownership of that existing daemon; it must not launch a second Garage against the same volumes. PRLS Analytics currently defines a separate Garage instance and needs its own consumer migration.

This plan does not add backup or restore tooling. The ownership cutover reuses the existing local volumes so the repository change itself does not discard objects. This Hardening-LLM decision does not change recovery requirements owned by other consumers.

## 2. Evidence and current boundaries

Recheck live state before the cutover; these observations are specific to 2026-09-23.

| Finding | Evidence | Consequence |
| --- | --- | --- |
| Production Compose defines Garage in this repository and the gateway uses `http://garage:3900`. | `docker-compose.yml` | Replace the local service dependency with the shared endpoint and network. |
| Caddy proxies artifact requests to `garage:3900`; Loki stores S3 chunks in bucket `prls-loki`. | `deploy/caddy/Caddyfile`, `deploy/loki/loki.yaml` | Caddy and Loki also need to reach the shared service. |
| The running Garage mounts Docker volumes `harden-llm_garage-metadata` and `harden-llm_garage-data` and is attached to `prls-observability`. | Docker inspection | Declare those existing volumes external in the new owner Compose file. Never run old and new Garage processes against them at once. |
| Current Garage metadata contains buckets `harden-llm-artifacts`, `prls-allure-reports`, `prls-loki`, and `prls-agent-artifacts`. | `/garage bucket list` in the running v2.3.0 container | Preserve the service data and all current bucket/key permissions through cutover. Do not print key secret material. |
| Agent Platform config uses Garage for agent artifacts and Allure reports; its service lock currently identifies this repository as the physical Garage owner. | `prls-co/agent-platform-infra` `compose/shared/compose.yaml` and `shared-services.lock.yaml` | Update the consumer endpoint and ownership lock as part of the coordinated change. |
| PRLS Analytics declares an additional Garage instance. | `prls-analytics/deploy/compose.service.yaml` | Give Analytics a distinct bucket and key on the shared endpoint, then remove its long-lived Garage service. |
| Hardening-LLM test and preview Compose files create their own Garage containers. | `deploy/test/compose.integration.yml`, `deploy/test/compose.smoke.yml`, `deploy/preview/compose.yml` | Keep these isolated; tests and previews must not write to shared runtime buckets. |

## 3. Design decisions

1. `garage-shared` owns the one long-lived Garage process, its pinned image, Garage configuration, and deployment runbook.
2. Keep the current S3 service on the existing private `prls-observability` Docker network during this ownership transfer. Publish no Garage host ports. The service advertises `garage-shared:3900`; retain the `garage` alias only until every existing consumer has moved.
3. Reuse the existing named metadata and data volumes by declaring them external in `garage-shared`. This avoids copying data and avoids two Garage daemons sharing writable state.
4. Keep a separate bucket and least-privilege S3 key for every consumer. Do not share an application key across projects. Record bucket names and key identifiers only; never put secrets in Git, issues, or logs.
5. Preserve ephemeral integration-test and preview Garage instances. They exercise the real S3 boundary without coupling tests to persistent shared data.
6. Do not add backup jobs, snapshot tools, restore automation, a second Garage instance, or an application storage abstraction.

## 4. Phased implementation

### Phase 0: inventory and cutover preflight

1. Confirm the current `harden-llm-garage-1` image digest, volume names, network membership, health, and Compose project at execution time.
2. Record bucket names, owning application, expected read/write operations, and the existing key identifier for each bucket. Keep secret values out of the record.
3. Confirm the endpoint and network owner for every persistent consumer: Hardening-LLM, Agent Platform/Allure, Loki, and PRLS Analytics.
4. Confirm each integration and preview Garage remains test-owned and isolated.
5. Stop if any client still relies on a bucket without an identified owner/key or if the current volume names differ from the external volume names declared in `garage-shared`.

### Phase 1: make the shared service repository deployable

1. In `prls-co/garage-shared`, pin the current Garage image digest and copy the current production `garage.toml` without changing its data layout or S3 region.
2. Define a single `garage` service with the existing metadata/data volumes as external volumes and attach it to external network `prls-observability` with DNS alias `garage-shared`.
3. Keep the current RPC secret and default key/bucket values in the approved private runtime environment. No rotation is part of this migration.
4. Confirm the Compose file publishes no host ports and does not create a replacement Garage volume.
5. Run `docker compose --env-file <private-runtime-env> config --quiet`; do not print rendered environment values.

### Phase 2: switch Hardening-LLM configuration to the shared DNS name

1. In root `docker-compose.yml`, change `HARDEN_LLM_ARTIFACT_ENDPOINT` to `http://garage-shared:3900`.
2. Attach `harden-llm-gateway` to `prls-observability` so it can resolve the shared service. Keep its application-private network.
3. Remove production `depends_on: garage` entries from the gateway and Caddy. Cross-project service health is checked by the shared service and app health checks, not Compose `depends_on` across projects.
4. Change the Caddy artifact upstream in `deploy/caddy/Caddyfile` and the Loki S3 endpoint in `deploy/loki/loki.yaml` to `garage-shared:3900`.
5. Remove the production Garage service and its volume declarations from root `docker-compose.yml`. Do not delete the Docker volumes; `garage-shared` references the existing names as external volumes. The isolated test Compose files define their own volumes.
6. Keep `deploy/garage/garage.toml`, the image pin, and isolated Garage services needed by integration, smoke, and preview Compose. Do not accidentally point these test/preview services at production.
7. Update static configuration assertions without weakening their checks: production clients must use the shared DNS name; test/preview stacks must retain their isolated Garage service.

### Phase 3: coordinated consumer cutover

1. Merge and prepare the `garage-shared` service configuration and consumer changes in Harden-LLM, Agent Platform, and PRLS Analytics.
2. Ensure the private environment source supplies all required Garage values to the new Compose project. Do not copy values into repository files or shell command output.
3. Ensure the four existing buckets and their permissions are present before switching clients. Add the Analytics bucket and key only if the Analytics issue confirms its required name and operations.
4. Confirm no second Garage process is using the shared volumes. Stop the old Hardening-LLM Garage service gracefully.
5. Start Garage from `garage-shared`, mounting the same existing volumes. Confirm service health and `/garage status` before restarting or moving any consumer.
6. Verify an S3 read/write operation with each application’s own scoped credentials, then check Hardening-LLM, Loki, Agent Platform/Allure, and Analytics readiness and an application-owned artifact read.
7. Verify production health routes and review logs for S3 authorization, DNS, or object-not-found errors. Do not run browser tests or real provider calls for this storage cutover.
8. Update `prls-co/agent-platform-infra/shared-services.lock.yaml` to name `prls-co/garage-shared` as the physical owner and record the observed image, endpoint, and source revision.

### Phase 4: remove stale ownership and close out

1. Confirm the old Hardening-LLM Garage service is absent and exactly one long-lived Garage container is healthy.
2. Remove stale documentation that describes Garage as owned solely by Hardening-LLM. Keep the isolated test/preview service docs and configs.
3. Record the deployed source SHA, image digest, host, network, external volume names, buckets, and browser-free acceptance results in the relevant release/operations records. Do not record credential values.
4. Keep this plan updated with actual test results, any cutover interruption, unresolved consumers, risks, and follow-up checks.

## 5. Verification and acceptance gates

Run these in the affected repositories after implementing their issues:

1. Hardening-LLM: `git diff HEAD --check`, `make test-fast`, `make test-integration`, then `make verify` and `make test-release` for the cross-system release. The Garage restart/persistence test must continue to use its test-owned Garage container.
2. Hardening-LLM Compose: validate production config with the approved private environment; validate test and preview Compose independently. No command output may expose resolved secrets.
3. PRLS Analytics and Agent Platform: run each repository's documented browser-free unit/integration and deployment-config gates; retain their isolated integration storage.
4. Live checks: shared Garage healthy, all application S3 operations succeed with dedicated keys, Hardening-LLM readiness succeeds, Loki writes and reads its configured bucket, and Allure/Agent Platform artifacts resolve. Record actual observations; a passing local Compose check is not deployment evidence.
5. Browser tests, browser canaries, and public LLM provider calls remain opt-in and are not part of this plan.

The migration is complete only when all persistent consumers use the shared DNS name, no long-lived consumer-owned Garage remains, test/preview Garage instances remain isolated, no public host port is exposed, and the above checks pass.

## 6. Rollback, risks, and aftercare

- **Rollback:** if the new service fails before consumer validation, stop it first and restart the old Hardening-LLM Garage service with the same image and volumes. Never run both processes against the same volumes concurrently. Revert consumer endpoints only if the shared alias is unavailable.
- **Shared outage:** one Garage outage affects Hardening-LLM artifact access, Loki's S3-backed logs, Agent Platform artifacts/Allure, and Analytics after migration. The host is already a single-node service; sharing reduces duplicate daemons but does not add availability.
- **Credentials:** separate buckets are not sufficient without key-level permissions. Verify every app key can access only its intended bucket and operations.
- **Retention:** confirm Loki's configured 2880-hour retention and each artifact consumer's own cleanup policy after cutover; do not add a second generic retention/backup subsystem.
- **Recovery ownership:** this plan adds no backup or restore tooling. The Hardening-LLM decision to avoid backups for its data does not revise recovery policy for Analytics or Agent Platform. Preserve each consumer's existing policy until its owner decides. A single-node Garage can lose data with host or volume failure; do not claim that every consumer has accepted that loss.
- **Aftercare:** inspect Garage health, S3 error counts, volume growth, Loki retention, and artifact links after the first application release and again after normal use. Investigate a real consumer failure before changing bucket policy or restarting the shared service.

## 7. Stop conditions

Stop before production cutover if any of these is true:

- The old Garage process cannot be stopped cleanly or its mounted volumes cannot be identified.
- A consumer’s bucket/key ownership or permissions are unknown.
- The shared network or private runtime environment is unavailable to the new owner project.
- The new configuration would start two daemons on the same Garage volumes.
- Required repository gates fail or any consumer cannot complete an S3 read/write check.

Do not work around a stop condition by making the service public, sharing an administrator key, dropping test assertions, or creating a second persistent Garage instance.
