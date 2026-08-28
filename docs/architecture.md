# Architecture and Ownership

Harden LLM separates portable execution semantics from transport, UI, and
infrastructure. There is one implementation home for each concern.

```text
browser -> Caddy -> Phoenix LiveView -> Go REST gateway -> hardenllm.Client.Call
                                           |                  |
                                           |                  `-> LLM provider
                                           |-> app Postgres
                                           |-> Garage
                                           `-> OTel Collector -> Tempo / Loki / Prometheus / Langfuse
```

## Component boundaries

| Component | Owns | Must not own |
| --- | --- | --- |
| Root Go library | provider payloads, retries, repair, schema, cache identity, usage/cost, domain projections | environment loading, exporters, auth, SQL, HTTP routes |
| Go gateway | bearer auth, owner isolation, REST envelopes, profile catalog backfill, local profiles, persistence adapters, process telemetry | browser cookies, CSRF, HTML, duplicate provider logic |
| Phoenix frontend | encrypted browser session, encrypted durable token vault, CSRF, presentation, REST calls | database, durable jobs, provider SDKs, pricing, retries, storage |
| Caddy | TLS, public host routing, security headers, request-size limits | application authorization |
| Collector | the single telemetry fanout and redaction pipeline | application or provider results |

`api/openapi.yaml` is the only Go-to-Phoenix contract. Phoenix calls the gateway
server to server; browsers never call the API directly. An ambiguous `/api/v1/run`
transport failure is never automatically replayed by either layer.

## Storage ownership

| Store | Owner and contents | Isolation rule |
| --- | --- | --- |
| `harden-postgres-data` | local users, token digests, state, encrypted profile credentials, runs, trace/artifact indexes | dedicated database, credentials, and migrations |
| `garage-metadata` + `garage-data` | private redacted trace JSON and diagnostic attachments | gateway-only S3 credentials; restore both volumes together |
| upstream `postgres` | Langfuse application records | unchanged upstream service |
| upstream `clickhouse` | Langfuse analytics | unchanged upstream service |
| upstream `minio` | Langfuse-owned objects | never receives Harden LLM artifacts |
| Prometheus/Loki/Tempo/Grafana volumes | operational diagnostics | no provider credentials or raw request/response bodies |
| `harden-llm-web-logs` | bounded, redacted Phoenix JSON logs | Collector reads it; no domain state |
| `harden-llm-web-sessions` | encrypted Phoenix bearer-token vault records | single Phoenix replica only; losing it requires frontend reauthentication |

The Harden LLM database and Garage pair are a separate failure and backup
domain from Langfuse. Sharing endpoints, buckets, credentials, databases, or
migrations across those domains is unsupported.

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

The certified topology is one Linux Docker host: fifteen backend services, or
sixteen with the Phoenix overlay. Caddy is the only public-port owner. The
gateway and Phoenix release images run non-root; the Phoenix image uses the
retained `harden-llm-web-sessions` volume for one encrypted single-replica token
vault. Horizontal or multi-host deployment requires a later ADR with a shared
vault design.

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
