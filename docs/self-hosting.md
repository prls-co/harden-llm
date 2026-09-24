# Self-Hosting and Operations

The certified deployment is one Linux Docker host. Harden-LLM production Compose
has fourteen services and expects the separately managed `garage-shared`
service on the existing `prls-observability` network. The optional Phoenix
overlay adds one service. Run commands from the repository root with Docker
29+ and Compose 2.40+.

## Prepare the host

Allocate persistent storage for Docker volumes, including the retained
`harden-llm-web-sessions` volume, working DNS for the five public hostnames, and
inbound TCP 80/443. Copy `.env.example` to `.env`, set mode 0600, and replace
every placeholder. Generate every secret independently; do not
reuse application, Garage, Grafana, or Langfuse credentials.

Provision `garage-shared` from the `prls-co/garage-shared` repository on the
same Docker host before starting Harden-LLM. Its owner creates and operates the
external `prls-observability` network and keeps the existing Garage data and
metadata volumes. Do not start a repository-local production Garage service.

Use a public ACME account email as `HARDEN_LLM_TLS_MODE` in production. `internal`
uses Caddy's private CA and is appropriate only when clients explicitly trust it.

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
Cloudflare-tunneled production origin. Set the five `HARDEN_LLM_*_HOST` values
to the public names configured by the tunnel. If the tunnel validates Caddy's
private CA, keep `HARDEN_LLM_TLS_MODE=internal`; otherwise use the documented
public ACME email value and validate the resulting certificate path before
starting the stack.

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

## Bootstrap an operator

There is no public registration. Use a stable, non-email owner ID and provide
the password through standard input so it never appears in process arguments:

```bash
read -rsp 'Initial password: ' BOOTSTRAP_PASSWORD; echo
printf '%s\n' "$BOOTSTRAP_PASSWORD" | "${COMPOSE[@]}" run --rm -T \
  harden-llm-gateway bootstrap-user \
  --owner-id operator-01 --email operator@example.net --password-file -
unset BOOTSTRAP_PASSWORD
```

The command is create-only and fails for an existing owner or email; it is not
a password-reset path. Restrict Docker access: it is equivalent to root and can
read service configuration.

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
The Go REST API routes are independent and unchanged.

- `https://<api-host>/healthz` checks process liveness.
- `https://<api-host>/readyz` checks migrations and the Garage bucket.
- `https://<web-host>/healthz` checks Phoenix startup.
- Grafana is the operational entry point for Prometheus, Loki, and Tempo.
- Langfuse receives complete Go gateway traces only through the Collector.

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
Langfuse, but they cannot reconstruct the consumer widget's history.
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
[shared LLM configuration](shared-llm-configuration.md), and run the trusted
`sync-profiles` command separately for the existing guest and operator accounts.
The configuration check never runs profile synchronization as a validation
side effect.
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
deployed schema, retaining `harden-llm-web-sessions` for the current session
contract. Database migrations are forward-only. If compatibility is uncertain,
keep writes stopped and deploy a compatible forward fix; this deployment has no
data restore path.
After any recovery, verify login, profile probe, one deterministic run, artifact
download, and correlated Tempo/Loki/Prometheus/Langfuse diagnostics.

## Shutdown

`"${COMPOSE[@]}" down` preserves named volumes. Adding `--volumes` permanently
deletes application, artifact, telemetry, Langfuse, and frontend-session data;
it is reserved for disposable test projects and forces frontend reauthentication.
