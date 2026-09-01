# Self-Hosting and Operations

The certified deployment is one Linux Docker host. The backend has fifteen
services; the optional Phoenix overlay adds the sixteenth. Run commands from
the repository root with Docker 29+ and Compose 2.40+.

## Prepare the host

Allocate persistent storage for Docker volumes, including the retained
`harden-llm-web-sessions` volume, working DNS for the five public hostnames, and
inbound TCP 80/443. Copy `.env.example` to `.env`, set mode 0600, and replace
every placeholder. Generate every secret independently; do not
reuse application, Garage, Grafana, or Langfuse credentials.

Use a public ACME account email as `HARDEN_LLM_TLS_MODE` in production. `internal`
uses Caddy's private CA and is appropriate only when clients explicitly trust it.
Keep `.env` outside backups that leave the encrypted backup boundary.

Do not reuse development routing values such as `*.harden.localhost` for a
Cloudflare-tunneled production origin. Set the five `HARDEN_LLM_*_HOST` values
to the public names configured by the tunnel. If the tunnel validates Caddy's
private CA, keep `HARDEN_LLM_TLS_MODE=internal`; otherwise use the documented
public ACME email value and validate the resulting certificate path before
starting the stack.

For the full product, define the exact project once in Bash:

```bash
COMPOSE=(docker compose
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

## Back up and restore

Back up these failure domains independently:

1. Application Postgres: logical dump plus roles, or a tested cold volume snapshot.
2. Garage: `garage-metadata` and `garage-data` in one quiesced snapshot.
3. Langfuse: upstream Postgres, ClickHouse, Redis, and MinIO according to the
   pinned Langfuse release's procedures.
4. Prometheus, Loki, Tempo, and Grafana volumes when diagnostic retention matters.
5. `harden-llm-web-sessions` when preserving active frontend logins across host
   recovery matters; losing it requires frontend reauthentication.
6. `.env` and Caddy data through a separate encrypted secrets/PKI backup.

For a portable cold backup, run `"${COMPOSE[@]}" down` without `--volumes`,
snapshot all named volumes at the Docker volume-driver layer, then restart and
verify `/readyz`. Restore only while the project is down. Preserve the active
and historical `HARDEN_LLM_ENCRYPTION_KEYS`; losing an old key makes credentials
written with that key undecryptable. Never restore Garage data without its
matching metadata, or mix Harden LLM Garage volumes with Langfuse MinIO.

Test restoration on another host before treating a backup as valid.

## Reconcile retained execution history

The gateway image includes one bounded administrative command for the legacy
runless-trace migration. It does not use Tempo, Langfuse, Laminar, ClickHouse,
or logs as product data. Run it only after a matching Postgres and Garage
backup has passed an isolated restore test and normal writes are quiesced.

Dry-run is the default and must be scoped explicitly:

```bash
"${COMPOSE[@]}" run --rm --no-deps harden-llm-gateway \
  reconcile-history --all-owners
```

The JSON report contains redacted counts, no owner/run/trace/object identities,
and one deterministic `planDigest`. Apply fails closed on any unclassified,
truncated, changed, missing, or integrity-mismatched row. After reviewing the
dry-run report, pass the exact digest without putting credentials in arguments:

```bash
"${COMPOSE[@]}" run --rm --no-deps harden-llm-gateway \
  reconcile-history --all-owners --apply --digest '<exact-plan-digest>'
```

Repeat apply with the same digest; an already completed plan reports zero
candidates and zero applied traces. Before enabling the structural ownership
migration, require zero runless traces and inspect artifact reconciliation
metrics for zero pending operations and zero unavailable available-state rows.

The reconciliation command intentionally migrates only through schema version
4. This allows the new image to reconcile an older installation before normal
gateway startup applies schema version 5. Do not start the normal gateway with
runless traces still present: migration 5 rejects them instead of preserving a
compatibility path.

After migration and reconciliation, run the read-only reverse inventory:

```bash
"${COMPOSE[@]}" run --rm --no-deps harden-llm-gateway audit-artifacts
```

The report is redacted and count-only. `healthy:true` requires a complete
inventory, no available metadata with a missing body, and no unreferenced
object older than the 15-minute in-flight window. Young unreferenced objects
are reported but do not trigger deletion; rerun after the window and inspect
the durable operation backlog before taking any manual action.

## Upgrade, rotate, and roll back

Before an upgrade, take a tested backup, review ADRs and image-lock changes, run
`make verify` and `make test-compose`, then deploy only immutable release IDs and
digests. Validate the effective Compose project before `up -d`.

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
contract. Database migrations are forward-only; if compatibility is uncertain,
stop writes and restore the pre-upgrade failure-domain backups.
After any recovery, verify login, profile probe, one deterministic run, artifact
download, and correlated Tempo/Loki/Prometheus/Langfuse diagnostics.

## Shutdown

`"${COMPOSE[@]}" down` preserves named volumes. Adding `--volumes` permanently
deletes application, artifact, telemetry, Langfuse, and frontend-session data;
it is reserved for disposable test projects and forces frontend reauthentication.
