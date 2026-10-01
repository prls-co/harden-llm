# Self-Hosting and Operations

The certified deployment is one Linux Docker host. Harden-LLM production Compose
has thirteen backend services and a one-shot Collector volume initializer; the
optional Phoenix overlay adds one application service. Shared `caddy-shared` and
`garage-shared` projects own ingress and object storage on the existing
`prls-observability` network. Run commands from the repository root with Docker
29+ and Compose 2.40+.

These instructions describe the target topology after the shared-Caddy handoff.
Production still uses its existing ingress owner until P05 is accepted in
[`shared-caddy-adoption-plan.md`](../plans/shared-caddy-adoption-plan.md). Do not
remove or redeploy the current production ingress while that phase is open.

## Prepare the host

Allocate persistent storage for Docker volumes and working DNS for the public
hostnames.
Shared Caddy owns inbound TCP 80/443. Copy `.env.example` to `.env`, set mode
0600, and replace every placeholder. Generate every secret independently; do
not reuse application, Garage, Grafana, or Langfuse credentials.

Provision `garage-shared` from the `prls-co/garage-shared` repository on the
same Docker host before starting Harden-LLM. Its owner creates and operates the
external `prls-observability` network and keeps the existing Garage data and
metadata volumes. Do not start a repository-local production Garage service.

Configure TLS and Caddy listener bindings in the `caddy-shared` repository; they
are not Harden-LLM environment inputs.

Building the gateway and Phoenix frontend requires a GitHub token with read
access to the private `prls-control-plane` Go module and `prls-web` dependency.
Set `PRIVATE_MODULE_TOKEN` in the build environment; Docker Compose passes it
only as a BuildKit secret while fetching dependencies. It is not a runtime
setting and is not copied into either image. CI and the trusted preview runner
mint a short-lived GitHub App token scoped to read those two repositories. For
local builds, use a read-only token with those repository scopes and keep it in
the invoking process environment only.

For an existing production project, install the nonsecret descriptor described
in [`docs/environment.md`](environment.md) and run the read-only check before
any Compose operation. It loads the production, shared-observability, and
shared-application sources from their approved paths; do not shell-source an
environment file or use a running container as a missing-value source.

```bash
node scripts/production-config.mjs check \
  --descriptor /home/kirill/.config/harden-llm/production.json
```

Before a release cutover, compare the application service being promoted with
its exact candidate SHA. A descriptor that is internally equivalent at an
older release must fail this gateway example:

```bash
node scripts/production-config.mjs check \
  --descriptor /home/kirill/.config/harden-llm/production.json \
  --services harden-llm-gateway \
  --expected-release <40-hex-commit-sha>
```

Check `harden-llm-web` separately with its own candidate SHA when promoting the
web application. Select both services under one `--expected-release` only when
they intentionally share that exact release identity. An ordinary combined
check without `--expected-release` still verifies overall descriptor/runtime
equivalence when their release identities differ.

Do not reuse development routing values such as `*.harden.localhost` for a
Cloudflare-tunneled production origin. Set application host variables to the
public names used by the shared Caddy routes. Configure the public artifact
route to match `HARDEN_LLM_ARTIFACT_EXTERNAL_ENDPOINT` and the Caddy TLS policy
in `caddy-shared`.

For first-time bootstrap, after all approved source files and the descriptor
are installed, define the exact project once in Bash:

```bash
OBSERVABILITY_ENV_FILE=/path/to/approved/observability.env
COMPOSE=(docker compose
  --env-file "$OBSERVABILITY_ENV_FILE"
  --env-file .env
  -f docker-compose.yml
  -f deploy/langfuse/docker-compose.upstream.yml
  -f deploy/langfuse/compose.private.yml
  -f deploy/frontend/compose.frontend.yml)
"${COMPOSE[@]}" config --quiet
"${COMPOSE[@]}" pull --ignore-buildable
"${COMPOSE[@]}" up -d --build --wait --wait-timeout 300
"${COMPOSE[@]}" ps
```

Omit the last file for the frontend-independent backend. Do not edit the pinned
upstream Langfuse fragment; follow its [update procedure](../deploy/langfuse/UPSTREAM.md).
For a running production project, do not substitute this generic bootstrap
sequence for the reproducibility check or scoped apply; it has no service
identity comparison and its `pull`/`build` behavior is intentionally broader.

## Human identity and product access

Create human accounts and grant Harden LLM product access in the PRLS Control
Plane. HLLM has no local registration, user bootstrap, or password-reset path.
Configure `HARDEN_LLM_CONTROL_PLANE_URL` and the protected
`HARDEN_LLM_CONTROL_PLANE_INTERNAL_TOKEN`; the gateway checks current account
and product access for every human request. Phoenix keeps an encrypted
host-only `__Host-harden_llm_web` cookie; sessions are not shared across product
subdomains. Set `HARDEN_LLM_STATIC_TOKEN_ACCOUNT_ID` only when a machine client
needs direct API access as one explicit Control Plane account.

## Rehome existing product data

An existing database with local `users` rows cannot start the new gateway until
each old owner maps one-to-one to a real Control Plane account UUID. Grant
Harden LLM access to each target account first. The mapping must not merge
owners; combining histories, profiles, credentials, or artifacts is not
supported by this migration.

Stop the HLLM gateway and frontend while leaving Postgres and Garage available.
Write a mode-0600 JSON mapping file containing every legacy `localOwnerId` and
its verified `accountId`, then run the one-shot command using the candidate
gateway image and production Compose environment:

```json
{"owners":[{"localOwnerId":"old-owner-id-from-database","accountId":"00000000-0000-4000-8000-000000000000"}]}
```

```bash
IDENTITY_MAP=/path/to/identity-map.json
docker compose run --rm -T --no-deps \
  -v "$IDENTITY_MAP:/run/identity-map.json:ro" \
  harden-llm-gateway rehome-identities \
  --mapping-file /run/identity-map.json
```

The command migrates through owner-reference cascade version 9, re-encrypts
provider credentials with the new account UUID in their authenticated binding,
copies and verifies Garage objects, updates relational owner/object keys,
checks that no legacy owner references remain, removes old object prefixes, and
then applies migration 10 to drop the HLLM `users` and `user_sessions` tables.
It prints counts and readiness only. An already-committed target state can be
retried before migration 10 completes. Do not restart gateway or web writes
until the command and post-migration readiness both pass.

Afterward, configure `HARDEN_LLM_STATIC_TOKEN_ACCOUNT_ID` and
`HARDEN_LLM_PROFILE_ACCOUNT_IDS` with the intended Control Plane UUIDs, run the
trusted profile sync for those explicit accounts, and verify current sign-in,
product access, profile/run ownership, and retained trace/artifact reads. This
schema change is forward-only: older images that expect local identity tables
cannot be used after migration 10. Retain an independent pre-cutover database
and Garage copy until release acceptance; recovery requires restoring the whole
pre-cutover environment rather than running an older image against migrated
data.

## Profile presets

The first profile/catalog operation for an owner backfills the current
utility-llm preset catalog: 28 credential-free profiles, while retaining any
existing custom or operator-edited rows. Seeding is protected by an
owner-scoped Postgres transaction and inserts only missing preset IDs. Each
preset must be configured with the owner's provider credential before it can
run; the API never returns the stored secret.

## Health and diagnostics

The authenticated application lives at `/`; `/?trace_id=<id>` restores a result.
There are no `/workspace` or `/history` routes or legacy redirects. Separate
frontend routes remain for `/login`, `/logout`, `/session/expired`, `/profiles`,
`/profiles/bundle`, `/embed/llm`, `/traces/:trace_id`, and artifact downloads at
`/traces/:trace_id/artifacts/:artifact_id`. `/healthz` is the frontend health probe.
The Go REST resource routes remain independent; the former HLLM-owned human
login, session, and logout API routes have been removed. Browser sign-in is
handled through the PRLS Control Plane.

- `https://<api-host>/healthz` checks process liveness.
- `https://<api-host>/readyz` checks migrations and the Garage bucket.
- `https://<web-host>/healthz` checks Phoenix startup.
- Grafana is the operational entry point for Prometheus, Loki, and Tempo.
- New HLLM gateway traces go from the Collector to Laminar. The separately
  retained Langfuse UI/history stack is not an HLLM trace-export destination.
  Issue [#83](https://github.com/prls-co/harden-llm/issues/83) tracks its reader,
  retention, and route review. Do not stop the stack or delete its data before
  that owner review records the chosen disposition and recovery requirements.

Use `"${COMPOSE[@]}" logs --since 15m <service>` sparingly. Logs are redacted by
contract, but still treat them as operational data. The API never exposes
Prometheus, Collector, Postgres, Garage administration, or provider endpoints.

If readiness fails, inspect the first unhealthy dependency instead of extending
the 300-second budget. Timeout increases require the RCA in
[`ker/timeouts/`](../ker/timeouts/README.md).

## Execution data and artifact inventory

Execution reads use schema v2 only. The retained-v1 decoder and the one-off
`reconcile-history` command were removed after the authorized 2026-09-10
run/cache purge. Forward-only database migrations remain; no new legacy-data
migration or fallback is provided.

History deletion removes an owner's runs, traces, observations, and artifact
bodies through the journaled coordinator. It does not delete operation-cache
outputs or external telemetry. Purging those requires explicit scope; never
delete database or object-store volumes to clear one owner's run data.

Normal artifact crash recovery and the read-only reverse inventory remain:

```bash
"${COMPOSE[@]}" run --rm --no-deps harden-llm-gateway audit-artifacts
```

The report is redacted and count-only. `healthy:true` requires a complete
inventory, no available metadata with a missing body, and no unreferenced
object older than the 15-minute in-flight window. Young unreferenced objects
are reported but do not trigger deletion; rerun after the window and inspect
the durable operation backlog before taking any manual action.

## Upgrade, rotate, and roll back

This deployment has no node-data backup or restore procedure; the owner accepts
loss of persistent application data. LLM observability traces are sent to
Laminar, but they cannot reconstruct the consumer widget's history. Existing
Langfuse history remains separate.
For the current codebase-reduction release, the deployed-to-candidate change
contains no database migration or Postgres storage-code change. Review ADRs and
image-lock changes, and run `make test-release`. This browser-free gate includes
`make verify` and the backend Compose check; do not repeat them separately or
launch a browser/live-provider canary automatically.
Deploy only immutable release IDs and digests, validate the effective Compose
project before `up -d`, and rebuild/recreate only affected application services
with `--no-deps` when their dependencies are unchanged. Retain rollback images
and keep existing data/session volumes attached during image rollbacks. Verify
public health/readiness and authenticated read-only routes; report browser and
live-provider checks as not run unless
separately authorized. A loss of Postgres/Garage still loses HardLLM history.

Run the production-config check/apply for runtime settings, then inject shared
provider settings through the approved process environment as described in
[shared LLM configuration](shared-llm-configuration.md). Run the trusted
`sync-profiles --account-id <uuid>` command for each explicitly selected
Control Plane account. The configuration check never runs profile
synchronization as a validation side effect.
Keep production's infrastructure credentials, bearer token, encryption keys,
and sessions independent of development. Shared-observability variables may
also require the injection described in [the environment reference](environment.md).

Keep the frontend's `opentelemetry_exporter` before `opentelemetry` in both
the Mix dependency list and explicit release applications. `extra_applications`
alone does not guarantee the generated release boot order. Starting the SDK
before the exporter's gRPC dependencies can fail initialization and discard
early spans. This follows the [OpenTelemetry release guidance](https://github.com/open-telemetry/opentelemetry-erlang#design).
WEB-TEST-009 checks the configuration; the Compose test checks the actual boot
script, startup diagnostics, and end-to-end frontend/gateway trace correlation.

Treat active Loki schema periods as immutable. Before any Loki configuration
deployment, run `make validate-loki-schema`. A newly appended period must use a
strictly future UTC `from` date; a same-day or past activation is rejected
because pre-cutover writes for that UTC table may already exist. Deploy the
configuration before that future date, verify old and new queries, and only
then record its exact fingerprint in
`deploy/loki/schema-periods.lock.yaml`. Never edit or remove an accepted period
to roll back an object-store transition; use a new future period and a tested
data-migration plan.

To rotate credential encryption, add a new key ID to
`HARDEN_LLM_ENCRYPTION_KEYS`, keep old keys present, and switch
`HARDEN_LLM_ACTIVE_ENCRYPTION_KEY_ID`. New writes use the active key; existing
records remain readable. Remove an old key only after a deliberate re-encryption
migration proves no row references it.

Rollback the gateway/frontend images only to a version compatible with the
deployed schema. The account-UUID owner migration and local-identity removal are
forward-only; after that migration, do not restore an image that expects the old
user/session tables. If compatibility is uncertain,
keep writes stopped and deploy a compatible forward fix; this deployment has no
data restore path.
After any recovery, verify login, profile probe, one deterministic run, artifact
download, and correlated Tempo/Loki/Prometheus/Laminar diagnostics.

## Shutdown

`"${COMPOSE[@]}" down` preserves named volumes. Adding `--volumes` permanently
deletes application, artifact, telemetry, Langfuse, and frontend-session data;
it is reserved for disposable test projects and forces frontend reauthentication.
