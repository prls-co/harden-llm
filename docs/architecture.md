# Architecture and Ownership

Harden LLM separates portable execution semantics from transport, UI, and
infrastructure. There is one implementation home for each concern.

```text
browser -> shared Caddy (`caddy-shared`) -> Phoenix LiveView -> Go REST gateway -> hardenllm.Client.Call
                    |                      |                  |                    `-> LLM provider
                    |                      |                  |                    `-> app Postgres
                    |                      |                  `-> shared Garage (`garage-shared`)
                    |                      `-> Control Plane identity/access API
Phoenix and gateway telemetry -> OTel Collector -> Tempo / Loki / Prometheus
Gateway traces only          -> OTel Collector -> Laminar
```

## Component boundaries

| Component | Owns | Must not own |
| --- | --- | --- |
| Root Go library | provider payloads, retries, repair, schema, cache identity, usage/cost, domain projections | environment loading, exporters, auth, SQL, HTTP routes |
| Go gateway | Control Plane-backed human authorization, UUID owner isolation, machine-token scope, REST envelopes, profile catalog backfill, product resources and persistence adapters | human accounts/passwords, browser cookies, CSRF, HTML, duplicate provider logic |
| Phoenix frontend | shared PRLS sign-in/account-selection UI, encrypted host-only product session, CSRF, presentation, REST calls | identity/account authority, shared-domain cookies, database, provider SDKs, pricing, retries, domain storage |
| Control Plane (`prls-control-plane`) | human accounts, authentication, current memberships and product access decisions | HLLM profiles, prompts, runs, traces, artifacts, or product data |
| Shared Caddy (`prls-co/caddy-shared`) | TLS, public host routing, security headers, request-size limits | application authorization; HLLM Compose ownership |
| Collector | the single telemetry fanout and redaction pipeline | application or provider results |

`api/openapi.yaml` is the only Go-to-Phoenix data contract; the shared
`@prls/access` client is the identity/access contract. Phoenix calls the gateway
server to server with its service bearer and the current Control Plane session
reference. The gateway checks current account/product access on every human
request. The encrypted `__Host-harden_llm_web` cookie has no `Domain` attribute,
so each product keeps a host-only browser session. No HLLM login/password store
or cross-subdomain cookie exists. An ambiguous `/api/v1/run` transport failure
is never automatically replayed by either layer.

## Storage ownership

| Store | Owner and contents | Isolation rule |
| --- | --- | --- |
| `harden-postgres-data` | product-owned state, encrypted profile credentials, runs, trace/artifact indexes, and account UUID owner keys | dedicated database, credentials, and migrations; Control Plane owns account records |
| `garage-shared` service and its retained metadata/data volumes | private redacted trace JSON and diagnostic attachments for adopted clients | separate bucket-scoped credentials per client |
| upstream `postgres` | Langfuse application records | unchanged upstream service |
| upstream `clickhouse` | Langfuse analytics | unchanged upstream service |
| upstream `minio` | Langfuse-owned objects | never receives Harden LLM artifacts |
| Prometheus/Loki/Tempo/Grafana volumes | operational diagnostics | no provider credentials or raw request/response bodies |
| `harden-llm-web-logs` | bounded, redacted Phoenix JSON logs | Collector reads it; no domain state |
| `harden-llm-web` browser cookie | encrypted host-only Control Plane session reference and selected account context | browser sends it only to the HLLM host; the gateway revalidates current access with Control Plane |

The Harden-LLM database remains product-owned. Garage runs in the separate
`garage-shared` repository on the existing `prls-observability` network; this
repository owns only Harden-LLM's bucket and client credentials. The Collector
exports new HLLM gateway traces to Laminar. The retained Langfuse stack and its
existing trace history remain separate pending consumer and retention review.
The HLLM-owned consumer, retention, and route decision is tracked in
[issue #83](https://github.com/prls-co/harden-llm/issues/83); this source status
does not establish the current deployed identity or prove that the UI/API has
no readers. Sharing a Langfuse bucket, credential, database, or migration with
Harden-LLM is unsupported.

`llm_runs` is the relational execution aggregate root. A mandatory exact
owner/run/trace foreign key makes the trace, observations, and artifact metadata
one cascade-owned subtree. The gateway persists that subtree only through
`SaveExecution`; Garage bytes cross the transaction boundary through the
PostgreSQL artifact journal and one bounded reconciler. Product reads and stats
never depend on Tempo, Loki, Prometheus, Langfuse, Laminar, or ClickHouse.

## Profile catalog ownership

`internal/profiles/default-profile-catalog.json` is the credential-free,
source-derived preset catalog. It is embedded in the gateway binary and
validated through the normal profile parser; the gateway has no runtime
dependency on `/home/kirill/p/utility-llm`.

When an owner first uses a profile/catalog operation, the gateway inserts any
missing entries from the 28-profile seed through one owner-locked Postgres
transaction. Existing rows, including custom profiles and operator edits, are
preserved. Seeded rows expose `configured:false` and a non-secret endpoint
binding identifier; provider execution remains unavailable until the owner
stores a credential.

## Deployment scope

The certified topology is one Linux Docker host. Harden-LLM Compose has thirteen
backend services plus its one-shot Collector volume initializer; the optional
Phoenix overlay adds one application service. Caddy and Garage are separately managed shared services. Only shared Caddy
publishes public ports. This is the target owner layout; production ingress
remains on the old owner until P05 acceptance in the [shared-Caddy transition
plan](../plans/shared-caddy-adoption-plan.md). The gateway and Phoenix release
images run non-root; the Phoenix image uses its encrypted host-only cookie and
does not require a session-vault volume. Horizontal or multi-host deployment
still requires an ADR for deployment coordination, but identity stays in the
Control Plane rather than a shared HLLM session store.

## Test feedback architecture

Test execution is a separate resource architecture around the application
boundaries:

```text
edit -> test-fast (T0 pure / T1 in-process / T2 client rules)
          | only when the changed invariant needs it
          v
       T3 pooled Postgres/Garage leases and race
          | only for native browser facts
          v
       T4 two Chromium canaries
          | release/deploy only
          v
       T5 Compose, deployed, or explicitly authorized live provider
```

`test/test-tiers.json` owns task selection, resource class, timeout, cleanup
owner, network policy, credentials declaration, and canonical IDs. The Node
runner owns scheduling, process-group cancellation, bounded output, service
service pool startup, exact cleanup, and evidence. Make and CI are delegates. Ordinary
T3 tasks share service processes but own unique database/prefix leases; the
Garage restart task holds the exclusive resource and cannot overlap the pool.

LiveView remains the server-side state owner: folds, profile state, reasoning,
cache, retries, uploads, parent messages, and embedded-instance independence
are tested through public events and diffs. Pure JavaScript decisions are
tested by Node and imported by production hooks. Chromium remains the owner of
native events, focus, CSS/layout, LiveSocket patching, and hook effects. No
Happy DOM or jsdom dependency is part of this architecture.

An expensive-tier defect must be evaluated for a cheap root-invariant
regression. If the invariant is representable at T0-T2, the regression belongs
there; the expensive test remains only for the distinct service, browser,
deployment, or provider boundary. A serial exception must identify the global
resource that prevents safe concurrency.
