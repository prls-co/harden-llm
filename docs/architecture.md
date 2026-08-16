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
| Go gateway | bearer auth, owner isolation, REST envelopes, local profiles, persistence adapters, process telemetry | browser cookies, CSRF, HTML, duplicate provider logic |
| Phoenix frontend | encrypted browser session, ephemeral token vault, CSRF, presentation, REST calls | database, durable jobs, provider SDKs, pricing, retries, storage |
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
| `harden-llm-web-logs` | bounded, redacted Phoenix JSON logs | Collector reads it; Phoenix stores no durable domain state |

The Harden LLM database and Garage pair are a separate failure and backup
domain from Langfuse. Sharing endpoints, buckets, credentials, databases, or
migrations across those domains is unsupported.

## Deployment scope

The certified topology is one Linux Docker host: fifteen backend services, or
sixteen with the Phoenix overlay. Caddy is the only public-port owner. The
gateway and Phoenix release images are stateless and non-root; horizontal or
multi-host deployment requires a later ADR, including a replacement for the
single-instance Phoenix ETS token vault.
