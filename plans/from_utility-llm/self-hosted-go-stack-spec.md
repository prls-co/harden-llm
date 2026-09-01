# Harden-LLM Backend and REST API Stack Specification

## 1. Title and metadata

- Project name: `harden-llm`
- Target repository: `/home/kirill/harden-llm`
- Go module: `github.com/prls-co/harden-llm`
- Contract source repository: `/home/kirill/utility-llm`
- Version: `1.3.0-backend-spec`
- Owners: package maintainers and self-hosted runtime implementers
- Date: 2026-07-12
- Document ID: `SPEC-HARDEN-LLM-SELF-HOSTED-GO-001`
- Summary: This specification defines the self-hosted, free, Go backend for `harden-llm`: one importable root library and one versioned REST API gateway. Application records live in Harden-LLM Postgres, while Harden-LLM-owned trace artifacts and diagnostic attachments replace Firebase Storage in Garage. OpenTelemetry Collector, Prometheus, Loki, Tempo, Grafana, and self-hosted Langfuse provide diagnostics. Langfuse retains its upstream default dependency graph, including its own Postgres, Redis, ClickHouse, and MinIO services; Harden-LLM neither substitutes Garage into Langfuse nor uses Langfuse's MinIO. This backend contains no browser UI, Phoenix, LiveView, React, or frontend asset pipeline. The separately specified Phoenix LiveView application consumes only the published REST/OpenAPI contract.

## 2. Canonical stack

| Layer | Canonical choice | Responsibility |
| --- | --- | --- |
| Core language | Go | Runtime behavior, provider integrations, retry policy, cache identity, schema handling, pricing, trace projection, redaction, and public library API. |
| Public library | Root package `github.com/prls-co/harden-llm`, package name `hardenllm` | The only public implementation API. Internal behavior packages are not public compatibility surfaces. |
| REST gateway | `cmd/harden-llm-gateway` | Thin HTTP application over the root library for Phoenix LiveView and other non-Go clients. |
| HTTP routing | Go `net/http` with `chi` | API routes, auth, request decoding, limits, and middleware. |
| REST contract | OpenAPI 3.1 in `api/openapi.yaml` | Canonical machine-readable paths, schemas, bearer authentication, envelopes, errors, and examples for every non-health endpoint. |
| Public edge | Caddy | TLS, security headers, request-body limits, and reverse proxy for the API, Grafana, Langfuse, and Harden-LLM artifact hostnames. |
| Application database | Postgres | Users, sessions, profiles, encrypted credentials, client state, runs, domain trace records, observations, cache records, stats, and migrations. |
| Application object storage | Garage | Private Harden-LLM JSON trace artifacts and diagnostic attachments migrated from Firebase Storage. Garage is not a Langfuse dependency. |
| Langfuse relational database | Upstream default dedicated Postgres service | Langfuse transactional state, credentials, migrations, and lifecycle remain owned by the upstream Langfuse Compose topology. |
| Langfuse analytics | ClickHouse | Langfuse traces, observations, scores, and analytical queries. |
| Langfuse queue/cache | Redis | Upstream default Langfuse queue and cache dependency. |
| Langfuse object storage | MinIO | Upstream default Langfuse event and media blob storage. Harden-LLM application code and credentials never use it. |
| Telemetry pipeline | OpenTelemetry Collector Contrib | Receive OTLP traces, metrics, and logs; redact/process/batch signals; and export them to the canonical backends. |
| Metrics | Prometheus | Scrape Collector and service metrics for operational dashboards and alerts. |
| Logs | Loki | Store structured `slog` JSON records with trace/span correlation. |
| Traces | Tempo | Store operational distributed traces. |
| Dashboards | Grafana | Explore and correlate Prometheus metrics, Loki logs, and Tempo traces. |
| LLM diagnostics | Langfuse OSS | LLM-specific traces, sessions, prompts, token/cost analysis, scores, and run inspection. |
| Background workflows | None in v1 | Provider retries stay in the library. Temporal is not installed for v1. |

## 3. Design decisions

| Topic | Verdict | Rationale |
| --- | --- | --- |
| Use Go | DECISION | Provider latency, retries, JSON processing, telemetry, and database writes dominate this service. Go provides direct deployment, bounded concurrency, mature HTTP support, and stable OTel traces/metrics without the complexity of C++, Rust, or a BEAM deployment. |
| Implement in the existing `harden-llm` repository | DECISION | `github.com/prls-co/harden-llm` already exists and matches the intended Go module path. `utility-llm` remains a read-only contract source during migration. |
| Expose one root Go package | DECISION | A small root API prevents every internal provider, retry, schema, and telemetry package from becoming a permanent public contract. |
| Return one detailed result type | DECISION | `Client.Call` returns output plus normalized usage, cost, cache, attempt, and trace metadata. There is no simple/detailed mode split and no expanded-result option. JS direct output parity is asserted against `Result.Output`. |
| Keep the gateway thin | DECISION | HTTP handlers own transport, auth, authorization, and persistence orchestration. They call the root library and do not construct provider payloads or reimplement retries, schema behavior, pricing, cache identity, or redaction. |
| Use Postgres for application records | DECISION | Concurrent profile, session, trace, artifact-index, history, stats, and cache writes require transactions, indexes, JSONB, and migrations. SQLite is not an application database in v1. Garage may use its upstream-supported SQLite metadata engine internally without creating a Harden-LLM application persistence contract. |
| Seed the current profile catalog on first use | DECISION | Embed the credential-free 28-profile utility-llm catalog and insert missing entries under an owner advisory lock; preserve any existing/custom row and never seed credentials. |
| Keep the upstream Langfuse dependency graph | DECISION | The first release runs the pinned official Langfuse Compose topology instead of replacing or tuning its owned dependencies. Langfuse retains its own Postgres, Redis, ClickHouse, and MinIO services. Dependency migration is accepted only after upstream Langfuse makes and supports that migration. |
| Use Garage only for Harden-LLM artifacts | DECISION | Firebase Storage currently owns linked JSON traces and diagnostic attachments. Garage replaces that application-owned surface. MinIO remains opaque to Harden-LLM and is used only by Langfuse. |
| Use one OTel export path | DECISION | The application emits OTel once to the Collector. The Collector exports operational traces to Tempo and complete `harden-llm` traces to Langfuse over OTLP/HTTP. The library and gateway do not contain a direct Langfuse SDK/exporter. |
| Use `slog` JSON as the logging API | DECISION | OTel Go logs remain less mature than traces and metrics. Application code logs once through `slog`; one composed handler writes JSON to stdout and mirrors the same record through a pinned OTel slog bridge to the Collector. |
| Keep Postgres domain traces distinct from OTel traces | DECISION | Postgres stores the redacted domain record needed by REST clients. Tempo stores operational spans. Langfuse stores a derived LLM diagnostic view. Their ownership and schemas do not overlap. |
| Publish one frontend-independent REST contract | DECISION | `api/openapi.yaml` and conformance tests are the only backend/frontend contract. The backend does not render HTML, own LiveView state, or import frontend code. |
| Use Caddy | DECISION | One edge process provides automatic TLS, security headers, and private upstream routing for the API and diagnostic UIs. |
| Use local bearer auth | DECISION | The gateway owns bootstrap-created email/password users, Argon2id hashes, and opaque hashed server-side sessions. Login returns the opaque token once for `Authorization: Bearer`; the backend does not issue browser cookies or implement browser CSRF. |
| Enforce outbound endpoint safety | DECISION | Profiles can contain provider base URLs. The gateway must prevent SSRF, metadata-service access, unsafe redirects, DNS rebinding, and credential forwarding to unintended hosts. |
| Do not add request idempotency infrastructure in v1 | DECISION | The initial gateway executes synchronous calls without a distributed idempotency ledger or run queue. The API documents that clients must not automatically retry an ambiguous `/api/v1/run` response. |
| Do not add scheduled retention or backup automation in v1 | DECISION | Retention schedulers, backup orchestration, restore drills, and Temporal workflows are deferred. The v1 schema includes timestamps needed for future policies. |

## 4. Scope

### In scope

- Port current provider routing, retries, schema validation/repair, cache identity, usage, pricing, profiles, traces, stats, diagnostics, and redaction behavior into Go.
- Produce one importable root Go package and one REST gateway.
- Publish and enforce an OpenAPI 3.1 contract for every REST route, envelope, authentication requirement, and error shape.
- Replace Firebase Auth, Firestore, Functions, and Storage backend responsibilities with local bearer auth, Harden-LLM Postgres, Garage, and Go HTTP handlers.
- Store redacted linked JSON trace artifacts and diagnostic attachments in a private Garage bucket and their owner-scoped indexes in Harden-LLM Postgres.
- Run the pinned upstream Langfuse OSS Compose topology with its own Postgres, Redis, ClickHouse, and MinIO services without dependency substitution.
- Emit OTel traces and metrics and correlated `slog` JSON logs.
- Fan out complete `harden-llm` OTel traces from the Collector to Tempo and Langfuse.
- Provide one Docker Compose topology for a Linux host or VM.
- Protect provider HTTP requests with one shared endpoint-security policy.

### Out of scope

- Firebase runtime or deployment compatibility in `harden-llm`.
- An application SQLite database, Sentry, Temporal, Kubernetes, Nomad, or managed cloud variants.
- Replacing, patching, or locally migrating any Langfuse-owned Postgres, Redis, ClickHouse, or MinIO dependency.
- A second Langfuse exporter in Go code.
- A generic pass-through LLM proxy that bypasses the library contracts.
- Distributed request idempotency, asynchronous run queues, and global scheduling.
- Automated retention, backup, restore, or disaster-recovery workflows.
- OIDC, SAML, public user registration, password-email workflows, and an admin UI.
- Browser UI, HTML rendering, Phoenix, LiveView, React, frontend sessions, frontend CSRF, and frontend assets. These are owned by `phoenix-liveview-frontend-spec.md`.

## 5. Backend-owned repository structure

The following tree is the backend-owned build and release scope. The separately specified `frontend/` application and `deploy/frontend/` overlay may coexist in the repository, but the Go module, backend `Makefile` gates, base Compose file, and backend release image do not import, build, or package them.

```text
/home/kirill/harden-llm/
├── go.mod
├── client.go
├── types.go
├── profiles.go
├── cache.go
├── artifacts.go
├── telemetry.go
├── client_test.go
├── cmd/
│   └── harden-llm-gateway/
│       └── main.go
├── api/
│   └── openapi.yaml
├── internal/
│   ├── runtime/
│   ├── providers/
│   ├── retry/
│   ├── schema/
│   ├── cachekey/
│   ├── pricing/
│   ├── profiles/
│   ├── traces/
│   ├── stats/
│   ├── diagnostics/
│   ├── artifacts/
│   ├── redaction/
│   ├── gateway/
│   ├── postgres/
│   │   ├── migrations/
│   │   └── queries/
│   ├── deploytest/
│   ├── smoke/
│   └── testkit/
├── fixtures/
│   ├── parity/
│   ├── providers/
│   ├── gateway/
│   ├── observability/
│   ├── artifacts/
│   └── redaction/
├── scripts/
│   └── capture-utility-llm-fixtures.mjs
├── plans/
│   ├── from_utility-llm/
│   │   ├── self-hosted-go-stack-spec.md
│   │   ├── harden-llm-self-hosted-implementation-plan.md
│   │   ├── harden-llm-self-hosted-test-spec.md
│   │   └── phoenix-liveview-frontend-spec.md
│   └── implementation-status.json
├── deploy/
│   ├── caddy/
│   ├── otel/
│   ├── prometheus/
│   ├── loki/
│   ├── tempo/
│   ├── grafana/
│   ├── garage/
│   ├── langfuse/
│   │   ├── docker-compose.upstream.yml
│   │   ├── compose.private.yml
│   │   └── UPSTREAM.md
│   ├── postgres/
│   └── test/
│       └── compose.smoke.yml
├── docker-compose.yml
├── .env.example
└── Makefile
```

Rules:

- The module root is the public `hardenllm` package.
- Internal provider, retry, schema, cache-key, pricing, trace, stats, diagnostics, and redaction packages are imported only inside the module.
- The gateway imports the root package. It does not import internal runtime packages to bypass the public contract.
- Public extension points are limited to `CredentialResolver`, `CacheStore`, `ArtifactStore`, endpoint-policy configuration, and telemetry options. Built-in provider and Garage implementations are private.
- The backend repository surfaces defined by this specification contain no HTML templates, LiveView modules, React/Vite code, frontend assets, or frontend session implementation.
- `api/openapi.yaml` is checked against the live router and response schemas. Phoenix consumes that contract but is not imported, generated, or tested by the backend plan.
- Fixture capture records the exact source `utility-llm` Git SHA and produces committed deterministic JSON; the target has no runtime dependency on the source repository.

## 6. Go library contract

The canonical public API is one root package and one call method:

```go
package hardenllm

type Options struct {
  Credentials    CredentialResolver
  Cache          CacheStore
  Artifacts      ArtifactStore
  EndpointPolicy EndpointPolicy
  TracerProvider trace.TracerProvider
  MeterProvider  metric.MeterProvider
  Logger         *slog.Logger
}

type Request struct {
  ProfileID       string
  Profiles        ProfileCatalog
  SystemPrompt    string
  UserPrompt      string
  CallType        CallType
  Schema          json.RawMessage
  ReasoningEffort ReasoningEffort
  ProviderOptions map[string]any
  Context         ObservabilityContext
  CacheMode       CacheMode
  CacheVersion    string
  RetryPolicy     RetryPolicy
}

type Result struct {
  Output    any
  CallID    string
  TraceID   string
  Usage     Usage
  Cost      Cost
  Attempts  []Attempt
  Cache     CacheResult
  Artifacts []ArtifactRef
}

type ArtifactStore interface {
  Put(ctx context.Context, key string, content []byte, contentType string) (ArtifactRef, error)
  PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

type ArtifactRef struct {
  Key         string
  SHA256      string
  SizeBytes   int64
  ContentType string
}

type Client struct { /* private fields */ }

func New(options Options) (*Client, error)
func (client *Client) Call(ctx context.Context, request Request) (Result, error)
```

Contract requirements:

- `Call` is the only execution method.
- Built-in provider selection comes from `Profile.APIInferenceType`; callers never construct internal provider adapters.
- Direct Go callers supply a one-or-more-entry `ProfileCatalog` and a `CredentialResolver`. The gateway supplies owner-scoped saved profiles and an in-memory resolver backed by encrypted Postgres records.
- An owner receives any missing current 28-profile source-derived presets on the first profile/catalog/runtime operation. Seeding is one owner-locked Postgres transaction, credential-free, and never overwrites an existing row.
- `Result.Output` matches the current JS direct return for equivalent deterministic inputs.
- Result metadata and emitted telemetry derive from the same internal normalized call record.
- That normalized record includes the immutable selected target, every
  call-global attempt and prepared target, provider/cache/none result source,
  result accounting, and current provider accounting. The gateway never
  reconstructs these facts from mutable profile rows.
- Cache identity excludes observability context, retry controls, timeout/deadline, cancellation state, and UI context.
- `maxAttempts` means the call-global total provider-invocation budget across
  primary, retry, repair, and backup candidates.
- Parse/schema retry and semantic repair consume the same attempt budget.
- Backup profiles are resolved from flat `backupProfiles` references with current cycle, duplicate, missing-reference, and maximum-depth behavior preserved.
- Candidate profiles retain retry classification and backoff policy, but cannot
  reset or exceed the call-global attempt budget. The caller context remains the
  final overall deadline.
- Provider payloads, errors, and diagnostic data pass through the shared redactor before persistence or emission.
- `ArtifactStore` is optional for direct library callers. When configured, the library writes canonical redacted JSON trace artifacts and diagnostic attachments and returns immutable references; an artifact-store failure is recorded diagnostically but does not change an otherwise successful provider result.
- The self-hosted gateway supplies the one Garage-backed `ArtifactStore`. The library has no MinIO or Langfuse storage configuration.
- The library creates spans and metrics through injected or no-op OTel providers. It does not initialize exporters, read deployment environment variables, or contact Langfuse.
- The library logs through the injected `slog.Logger` and never owns process-global logging configuration.

## 7. REST gateway contract

All application routes are versioned under `/api/v1`.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/healthz` | Process liveness without downstream dependencies. |
| `GET` | `/readyz` | Postgres migration state and Garage artifact-bucket connectivity readiness. |
| `POST` | `/api/v1/auth/login` | Verify local credentials and return a new opaque bearer session token once. |
| `POST` | `/api/v1/auth/logout` | Revoke the active session. |
| `GET` | `/api/v1/auth/session` | Return redacted current-user/session state. |
| `GET` | `/api/v1/state` | Return owner-scoped client preference and prompt-draft state. |
| `POST` | `/api/v1/state` | Persist validated client-owned state fields. |
| `GET` | `/api/v1/history` | Return owner-scoped paginated run history. |
| `DELETE` | `/api/v1/history/{historyID}` | Delete one owned history record. |
| `DELETE` | `/api/v1/history` | Clear the authenticated user's history. |
| `GET` | `/api/v1/profiles/bundle` | Export the profile catalog and encrypted credentials. |
| `PUT` | `/api/v1/profiles/bundle` | Atomically replace the user's profile bundle. |
| `PUT` | `/api/v1/profiles/{profileID}` | Validate, probe when required, encrypt credentials, and save a profile. |
| `DELETE` | `/api/v1/profiles/{profileID}` | Delete an owned profile and credential references. |
| `POST` | `/api/v1/profiles/{profileID}/models:refresh` | Refresh model discovery through the profile endpoint. |
| `POST` | `/api/v1/run` | Execute one synchronous library call through a saved profile. |
| `GET` | `/api/v1/traces/{traceID}` | Return one authenticated owner-scoped redacted domain trace. |
| `GET` | `/api/v1/traces/{traceID}/artifacts/{artifactID}` | Authorize an owned artifact and redirect to a short-lived Garage presigned GET URL. |

All non-health responses use:

```json
{
  "state": {},
  "result": null,
  "error": null
}
```

Errors use:

```json
{
  "code": "invalid_request",
  "message": "Human-readable summary.",
  "fieldErrors": {
    "field": "Stable field error."
  }
}
```

`api/openapi.yaml` is the canonical REST contract. It uses OpenAPI 3.1, defines the bearer security scheme and every request/response schema, includes stable operation IDs and examples, and contains no frontend-specific types. Router conformance tests fail when a documented operation is missing from the gateway, an implemented route is absent from OpenAPI, or an envelope/schema diverges.

Authentication contract:

- `POST /api/v1/auth/login` is the only unauthenticated non-health operation. A successful response returns the opaque session token once as `result.accessToken`, plus its expiry and redacted user/session data.
- The gateway stores only a SHA-256 token digest with owner, creation, expiry, and revocation metadata.
- Every other `/api/v1` operation requires `Authorization: Bearer <opaque-token>`.
- Tokens never appear in URLs, response state after login, logs, traces, metrics, diagnostics, or error messages.
- Logout revokes the current token. Expired, revoked, malformed, duplicated-header, or unknown tokens return the same stable unauthenticated envelope.
- The REST API does not issue browser cookies and does not implement CSRF. Browser session and CSRF controls belong to the separately specified Phoenix frontend, which calls the REST API server to server.
- CORS is disabled by default. A future direct-browser client requires an explicit origin allowlist and ADR; wildcard origins are forbidden.
- The gateway's default and contract maximum synchronous `/api/v1/run` duration is 60 seconds, preserving the source default. Deployment may lower the cap, and a request or saved profile may select a shorter duration, but neither may exceed 60 seconds.
- When that deadline expires, the gateway cancels the root call and returns the documented `run_timeout` error with HTTP 504. The gateway never retries an ambiguous run request.

Gateway responsibilities:

- Authenticate and authorize owner-scoped state.
- Enforce the canonical opaque bearer-session policy on protected routes.
- Apply strict JSON decoding, body limits, and field limits.
- Apply the endpoint-security policy before every profile probe, model refresh, and provider call.
- Call the root Go library for all runtime behavior.
- Persist application records in Postgres.
- Load the embedded profile seed and perform owner-scoped catalog backfill without importing credentials or runtime discovery state.
- Persist Harden-LLM trace artifacts and diagnostic attachments through the injected Garage-backed artifact store, then persist their owner-scoped references in Postgres.
- Configure OTel SDK/exporters and the process `slog` logger.
- Shut down HTTP and telemetry providers within a bounded grace period.

Gateway non-responsibilities:

- Provider payload construction or response normalization.
- Retry, repair, backup-profile, cache-key, pricing, trace-projection, or redaction logic.
- Direct Langfuse API or SDK calls.
- Retrying ambiguous client requests to `/api/v1/run`.
- Rendering HTML, maintaining LiveView state, serving frontend assets, or implementing browser sessions/CSRF.

## 8. Provider endpoint-security contract

The same endpoint-security implementation is used by profile probes, model refreshes, and runtime provider calls.

- Accept `https` endpoints by default.
- Permit plain HTTP only for exact admin-configured development/private allowlist entries.
- Reject URL userinfo, fragments, unsupported schemes, malformed ports, and non-canonical hosts.
- Resolve all A and AAAA records before dialing and reject loopback, link-local, multicast, all-zero, documentation, carrier-grade NAT, and private addresses unless the exact host or CIDR is configured in the private allowlist.
- Revalidate the selected dial address and bind the HTTP transport to that validated address to prevent DNS rebinding.
- Disable redirects. A provider redirect is a safe normalized error and never forwards credentials.
- Never forward `Host`, `Connection`, proxy headers, transfer-encoding controls, or other hop-by-hop user headers.
- Construct authorization headers from the credential bound to the normalized endpoint origin.
- Do not send one origin's credential to a different scheme, host, or port.
- Use normal TLS verification in production. No provider option can enable `InsecureSkipVerify`.
- Apply connection, TLS-handshake, response-header, and overall call deadlines through the shared provider HTTP client.
- Emit only normalized host metadata; never log query strings, userinfo, credentials, or raw authorization headers.

## 9. Reverse proxy contract

Caddy is the only service publishing host ports by default.

Canonical hostnames:

- `HARDEN_LLM_API_HOST`: `/api/v1`, `/healthz`, and `/readyz`.
- `HARDEN_LLM_GRAFANA_HOST`: Grafana UI.
- `HARDEN_LLM_LANGFUSE_HOST`: Langfuse UI and internal browser-facing API.
- `HARDEN_LLM_ARTIFACT_HOST`: TLS endpoint for short-lived presigned Garage artifact reads; Garage administration endpoints are not routed.

Caddy must:

- Terminate TLS and persist certificate state in named volumes.
- Reverse proxy the API hostname to `harden-llm-gateway:8080` without serving HTML or frontend assets.
- Import trusted route fragments from `/etc/caddy/conf.d/*.caddy`. The base image/configuration supplies no frontend fragment; the separately tested frontend overlay may mount one without replacing backend routes.
- Reverse proxy Grafana, Langfuse, and the Garage S3 artifact hostname without exposing their container ports on the host.
- Never expose Garage administration or RPC routes. The backend artifact endpoint remains the authorization boundary for presigned reads.
- Do not route Harden-LLM artifact requests to MinIO. Langfuse's MinIO service and S3 configuration remain owned by the pinned upstream Langfuse Compose fragment.
- Apply request-body limits before the gateway.
- Set CSP, HSTS, `X-Content-Type-Options`, `Referrer-Policy`, and frame restrictions appropriate to each hostname.
- Preserve WebSocket/streaming behavior required by Grafana or Langfuse.
- Ignore untrusted incoming forwarded headers unless an explicitly configured upstream proxy is trusted.

## 10. Postgres application data contract

The `harden_llm` database is the source of truth for application state.

| Table | Purpose |
| --- | --- |
| `users` | Local identities and Argon2id password metadata. |
| `user_sessions` | Hashed opaque session tokens, expiry, and revocation. |
| `llm_profiles` | Redacted profile configuration, backup references, and credential-free preset rows inserted by catalog backfill. |
| `llm_endpoint_credentials` | Versioned AES-GCM credential ciphertext and metadata. |
| `llm_client_state` | Per-user client preferences and prompt drafts exposed by `/api/v1/state`. |
| `llm_runs` | Run history and result summary. |
| `llm_traces` | Redacted domain trace and normalized call metadata. |
| `llm_trace_observations` | Domain observations for cache, attempt, retry, and persistence events. |
| `llm_artifacts` | Owner-scoped Garage object key, kind, content type, SHA-256, byte length, availability state, and timestamps. |
| `llm_artifact_operations` | Durable typed publication/deletion intents, integrity metadata, bounded retry state, and completion timestamps; never artifact content. |
| `llm_artifact_delete_batches` | Owner-scoped execution, clear-history, and retained-data deletion plans that linearize metadata and object lifecycle. |
| `llm_operation_cache` | Owner-scoped operation-cache records. |
| `schema_migrations` | Applied application migration versions. |

Rules:

- Harden-LLM migrations and credentials apply only to the application Postgres service. Langfuse runs its own upstream Postgres service and migration lifecycle.
- Owner ID is mandatory on every user-owned application row.
- Credential ciphertext is separate from profile JSON.
- JSONB payloads are normalized and redacted before persistence.
- `llm_runs` is the execution aggregate root. New execution facts are stored
  once in a versioned result document plus typed query columns. `llm_traces` is
  a one-to-one execution identity child; its REST representation is projected
  from the aggregate rather than a second independently mutable execution
  document. `llm_traces.run_id` is mandatory and its exact
  `(owner_id, run_id, trace_id)` binding references the run with
  `ON DELETE CASCADE`. Standalone traces have no v1 producer or lifecycle.
- `SaveExecution` is the only production write path for run, trace,
  observations, and artifact metadata. It inserts the aggregate root before
  children in one transaction. Relational deletion removes only the run after
  Garage deletion has converged; PostgreSQL cascades the child metadata.
- Canonical accounting has result and current-provider views. Five exclusive
  token components derive prompt, completion, and total. Usage completeness and
  cost certainty are explicit; missing or inconsistent values are not zero.
- Cache entries retain immutable result producer identity and result accounting.
  Cache hits have no current provider attempt/accounting. Cache v1 records are
  invalidated at the canonical execution cut rather than dual-read.
- Garage artifact bytes are private, canonical JSON, redacted before upload, and referenced by immutable object key, SHA-256, and byte length. Raw credentials and unredacted provider envelopes are never persisted.
- A typed PostgreSQL publication intent commits before Garage upload. Available
  artifact metadata then commits with its run, trace, and observations while
  consuming the exact intent. A failed upload creates no available row;
  interrupted publication is resolved from the durable journal rather than a
  best-effort cleanup path.
- Artifact access requires owner authorization through the gateway; Postgres stores object keys, never durable public URLs. Presigned GET URLs are short lived.
- Timestamps and indexes support a future retention policy, but v1 performs no scheduled deletion.
- `GET /api/v1/stats` computes owner-scoped canonical totals directly from typed
  `llm_runs` execution fields. It distinguishes result accounting from current
  provider accounting and returns usage/cost coverage for the overall and
  cached subsets. A separately maintained aggregate projection is prohibited
  because it can drift from history.
- User-initiated history deletion first records a durable plan and marks
  artifact metadata non-actionable, then removes Garage bodies and finalizes
  relational deletion. A Garage failure retains the run and retryable journal
  state while unavailable/deleting artifacts cannot be presigned.
- Migration application uses one canonical runner and a Postgres advisory lock so concurrent gateway starts do not race. The retained-history command is intentionally bounded through migration 4, allowing a direct-upgrade binary to remove legacy runless traces before normal startup applies structural ownership migration 5.

### Harden-LLM artifact contract

- Garage owns only the private `harden-llm-artifacts` bucket. Langfuse never receives its endpoint or credentials.
- Supported v1 artifact kinds are redacted call trace JSON, redacted parse-failure response JSON, and diagnostic-event JSON attachments migrated from Firebase Storage.
- Object keys are generated from an owner-scoped prefix, trace ID, artifact kind, and unique artifact ID. User input cannot supply a raw object key, and an artifact ID is never reused.
- JSON is canonicalized once, redacted once, hashed with SHA-256, and uploaded with `application/json`. The exact stored bytes define `sha256` and `size_bytes` in `llm_artifacts`.
- The gateway records a typed PostgreSQL publication intent before Garage PUT
  and consumes it with execution metadata in the execution transaction. Typed
  deletion intents precede object removal. Idempotent object operations and one
  bounded in-process reconciler converge interrupted operations; no distributed
  transaction or second recovery service is introduced.
- A bounded read-only administrative inventory compares Garage keys with live
  artifact metadata and incomplete journal operations. It emits counts only,
  fails closed on truncation, missing available objects, or aged unreferenced
  objects, and never performs blind deletion.
- Reads authorize the Postgres owner/trace relationship before requesting a presigned Garage URL. URLs expire in at most five minutes and are not stored.
- Garage timeouts and failures are bounded. Artifact persistence failure cannot change a completed provider result, cache result, or normalized usage/cost; it creates a redacted persistence-failure observation and metric.
- Garage S3 and administration credentials are different. Only the S3 API is reachable through Caddy, and only the gateway receives bucket credentials.

## 11. Observability and diagnostics contract

### Signal ownership

| Data | Source of truth | Purpose |
| --- | --- | --- |
| Run/profile/history state | Postgres | Product behavior and REST API responses. |
| Linked trace artifacts and diagnostic attachments | Garage plus Postgres index | Private redacted JSON payload inspection and Firebase Storage parity. |
| Operational traces | Tempo | HTTP, database, provider, retry, cache, and shutdown diagnostics. |
| Structured logs | Loki | Correlated events and failure context. |
| Operational metrics | Prometheus | Rates, latency, errors, saturation, and alert inputs. |
| LLM diagnostic view | Langfuse | Sessions, generations, prompts, usage, cost, scores, and human investigation. |

### Application instrumentation

- OTel traces cover HTTP requests, auth, profile save, model refresh, provider call, provider attempt, schema validation, retry wait, cache lookup/write, Postgres calls, Garage artifact writes, and trace persistence.
- OTel metrics cover request and provider counts/latency, retry categories, cache outcomes, schema/repair outcomes, token/cost totals, Postgres/Garage persistence failures, and database latency.
- Prometheus labels are limited to bounded dimensions such as route template, HTTP method, provider family, outcome, error category, cache outcome, and call type. User, request, run, trace, profile, model ID, prompt, base URL, and raw error text are not metric labels.
- One `slog` call produces a JSON stdout record and one OTel log record through a composed handler. Both carry the same trace/span correlation and safe fields.
- The OTel slog bridge is pinned and isolated behind gateway logging setup so its beta API is not part of the public library contract.
- Prompt and response content is excluded from Tempo, Loki, and Prometheus by default.
- Redacted prompt/response projections may be attached to Langfuse generation spans because Langfuse is the designated LLM diagnostic store.

### Collector pipelines

- OTLP gRPC/HTTP receivers accept application traces and metrics.
- OTLP gRPC/HTTP receivers accept application traces, metrics, and mirrored `slog` log records.
- All operational traces export to Tempo.
- Complete traces with `service.name=harden-llm-gateway` export to Langfuse over OTLP/HTTP. The filter must preserve the root span and all children for those traces.
- Metrics export through a Prometheus scrape endpoint.
- Logs export to Loki through its native OTLP HTTP endpoint.
- Memory limiting, batching, bounded queues, retry limits, and redaction processors are configured in the Collector.
- Collector, Tempo, Loki, Prometheus, Grafana, or Langfuse failures never change a provider call result.
- The gateway records exporter/flush failures to stderr and performs bounded OTel shutdown on process termination.

### Required dashboards

- Gateway health, request rates, latency, and errors.
- Provider latency, errors, retry categories, and timeout rates.
- Cache outcomes and saved token/cost estimates.
- Token and cost trends using bounded dimensions.
- Schema and repair outcomes.
- Postgres latency and errors.
- Collector accepted, refused, queued, retried, and dropped telemetry.
- Trace and domain-persistence failures.

## 12. Security contract

- Public traffic reaches only Caddy over HTTPS.
- Internal service ports have no host bindings by default.
- Passwords use fixed documented Argon2id parameters.
- Session values are opaque random bearer tokens returned only at login; only SHA-256 token digests are stored.
- Protected routes reject missing, malformed, expired, revoked, unknown, or ambiguous bearer credentials with one non-enumerating response shape.
- The backend sets no browser session cookie and has no CSRF bypass or CORS wildcard. Browser protections belong to the Phoenix frontend.
- Provider credentials use AES-256-GCM with a random nonce, key identifier, and AAD containing owner ID, credential ID, and normalized endpoint origin.
- The active encryption key is supplied by a mounted secret or environment variable and never stored in Postgres.
- Logs, OTel signals, Langfuse spans, diagnostics bundles, API responses, and test evidence use the same internal redaction package.
- Garage and MinIO use separate buckets, credentials, endpoints, and owners. Harden-LLM receives no Langfuse MinIO credentials, and Langfuse receives no Garage credentials.
- Provider endpoint safety follows Section 8.
- Request sizes are bounded for prompts, schemas, provider options, headers, state, and bundle imports.
- Diagnostic UIs retain their own authentication and are reached only through Caddy.

## 13. Deployment topology

```text
internet
  |
  v
caddy :80/:443
  |-- api host      -> harden-llm-gateway:8080
  |-- grafana host  -> grafana:3000
  |-- langfuse host -> langfuse-web:3000
  `-- artifact host -> garage:3900 S3 API only

harden-llm-gateway
  |-- harden-postgres:5432 / harden_llm database
  |-- garage:3900 / private harden-llm-artifacts bucket
  `-- otel-collector:4317

otel-collector
  |-- tempo:4317
  |-- loki:3100/otlp
  |-- prometheus scrape endpoint
  `-- langfuse-web OTLP/HTTP endpoint

grafana
  |-- prometheus
  |-- loki
  `-- tempo

langfuse-web + langfuse-worker
  |-- postgres:5432 / upstream Langfuse database
  |-- clickhouse
  |-- redis
  `-- minio
```

The pinned `deploy/langfuse/docker-compose.upstream.yml` is copied byte-for-byte from one released Langfuse revision. `deploy/langfuse/UPSTREAM.md` records the release, commit, source URL, and SHA-256. `compose.private.yml` may set generated secrets, attach the shared private network, remove direct public port exposure, and set the public Langfuse URL; it cannot replace, reconfigure, or share Langfuse-owned Postgres, Redis, ClickHouse, or MinIO dependencies. A future upstream Langfuse release may migrate those dependencies; Harden-LLM adopts that migration only by updating the pinned upstream fragment and rerunning the full smoke.

Required Compose services:

The production topology contains fifteen services: nine Harden-LLM/observability services and the six services in the pinned upstream Langfuse fragment.

- `caddy`
- `harden-llm-gateway`
- `harden-postgres`
- `garage`
- `otel-collector`
- `prometheus`
- `loki`
- `tempo`
- `grafana`
- `langfuse-web`
- `langfuse-worker`
- `postgres`
- `clickhouse`
- `redis`
- `minio`

`deploy/test/compose.smoke.yml` adds one private `fake-provider` service only for TEST-034. It is not included in the production Compose file, receives no host port, and is the sole private endpoint entry in the smoke gateway allowlist.

Named volumes are required for Caddy state, Harden-LLM Postgres, Garage metadata/data, Prometheus, Loki, Tempo, Grafana, and every volume in the pinned upstream Langfuse Compose fragment. Harden-LLM-owned images use explicit release tags or digests and never `latest`. The upstream Langfuse fragment is pinned by release commit and content hash; its dependency image selections are preserved rather than locally substituted.

Fresh v1 Garage fixtures use the pinned `dxflrs/garage:v2.3.0` image and Garage's supported `/garage server --single-node --default-bucket` startup path. Compose maps the existing Harden-LLM artifact key, secret, and bucket values to `GARAGE_DEFAULT_ACCESS_KEY`, `GARAGE_DEFAULT_SECRET_KEY`, and `GARAGE_DEFAULT_BUCKET`; the gateway receives the same bucket-scoped values under the `HARDEN_LLM_ARTIFACT_*` names. Metadata and object data use persistent named volumes. No custom layout script, bootstrap container, or application-held Garage administration credential is required for v1.

The production Compose service starts retained Garage metadata with `/garage server` and does not repeat the single-node/default-bucket bootstrap flags. Garage refuses those flags once the persisted cluster layout has advanced beyond its initial version, while the existing bucket and key state remain owned by the retained metadata. The isolated integration fixture keeps the fresh-volume bootstrap path above.

The v1 Garage profile uses the pinned `dxflrs/garage:v2.3.0` image, persistent metadata/data volumes, `db_engine = "sqlite"`, `replication_factor = 1`, and `consistency_mode = "consistent"`. It is a single-host, non-HA baseline intended to get the service running without inventing a cluster. The risk and lack of host-failure tolerance are explicit; multi-node Garage, automated backups, and restore orchestration remain future work.

Reference host for the full stack:

- Linux Docker host.
- 8 vCPU.
- 24 GiB RAM.
- At least 100 GiB persistent disk for initial use.
- Compose readiness budget: 300 seconds, based on Langfuse's documented two-to-three-minute startup plus health-check margin.

## 14. Configuration contract

Application variables:

| Variable | Purpose |
| --- | --- |
| `HARDEN_LLM_API_HOST` | Public REST API hostname. |
| `HARDEN_LLM_GRAFANA_HOST` | Public Grafana hostname. |
| `HARDEN_LLM_LANGFUSE_HOST` | Public Langfuse hostname. |
| `HARDEN_LLM_ARTIFACT_HOST` | Public TLS hostname used by short-lived Garage presigned artifact URLs. |
| `HARDEN_LLM_ARTIFACT_ENDPOINT` | Private Garage S3 endpoint used by the gateway. |
| `HARDEN_LLM_ARTIFACT_EXTERNAL_ENDPOINT` | Caddy Garage S3 endpoint used when generating client-reachable presigned URLs. |
| `HARDEN_LLM_ARTIFACT_BUCKET` | Private Garage bucket for Harden-LLM trace artifacts and diagnostic attachments. |
| `HARDEN_LLM_ARTIFACT_ACCESS_KEY_ID` | Garage key ID scoped to the Harden-LLM artifact bucket. |
| `HARDEN_LLM_ARTIFACT_SECRET_ACCESS_KEY` | Garage secret supplied only to the gateway. |
| `HARDEN_LLM_ARTIFACT_PRESIGN_TTL` | Maximum lifetime for authenticated artifact download URLs. |
| `HARDEN_LLM_DATABASE_URL` | Application Postgres DSN. |
| `HARDEN_LLM_ENCRYPTION_KEYS` | Key-ID to base64url 32-byte key mapping. |
| `HARDEN_LLM_ACTIVE_ENCRYPTION_KEY_ID` | Key used for new credential writes. |
| `HARDEN_LLM_OTEL_EXPORTER_OTLP_ENDPOINT` | Collector endpoint. |
| `HARDEN_LLM_SERVICE_NAME` | OTel service name; default `harden-llm-gateway`. |
| `HARDEN_LLM_ENVIRONMENT` | Deployment environment. |
| `HARDEN_LLM_RELEASE` | Release identifier. |
| `HARDEN_LLM_SESSION_TTL` | Session lifetime. |
| `HARDEN_LLM_MAX_RUN_DURATION_MS` | Effective synchronous `/api/v1/run` cap in milliseconds; default `60000`, required range `1..60000`. Requests may lower but not raise it. |
| `HARDEN_LLM_PROVIDER_ALLOWED_HOSTS` | Optional restriction for public provider hosts. |
| `HARDEN_LLM_PROVIDER_PRIVATE_ALLOWLIST` | Exact private hosts/CIDRs allowed by the administrator. |

Deployment configuration also supplies separate generated secrets for the application DB role, Garage RPC/admin/bucket access, Langfuse upstream Postgres, Langfuse auth/encryption, ClickHouse, Redis, MinIO, Grafana, and Caddy hostnames. Langfuse headless initialization uses its supported environment variables to create one initial user, organization, project, public key, and secret key; the Collector receives those project keys only through its deployment environment. `.env.example` contains names and safe examples only. Production startup rejects documented default secret values without changing the Langfuse-owned service graph.

## 15. Temporal boundary

Temporal is not installed or linked in v1. Library provider retries, parse/schema repair, backup-profile selection, cache behavior, and cost/usage accounting are synchronous library concerns.

A future Temporal design may schedule profile smoke, model refresh, stats recomputation, or hosted verification. A provider-call Activity must use `MaximumAttempts: 1` unless a future idempotency design proves that retry cannot duplicate a provider invocation. Temporal cannot be added by modifying v1 retry behavior in place; it requires a separate ADR and plan.

## 16. Migration contract

- `/home/kirill/utility-llm` is the behavior and fixture source during the port.
- `/home/kirill/harden-llm` is the only target implementation and deployment repository.
- P00 records the source Git SHA and captures deterministic provider payloads, cache hashes, trace/stats projections, profile bundles, schema cases, and diagnostics fixtures.
- Fixture capture includes current Firebase Storage trace-artifact keys, canonical JSON payloads, diagnostic attachment paths, hashes, and failure behavior. The target implements those application-owned semantics through Garage without copying Firebase APIs.
- Every behavior slice adds and passes its JS-to-Go parity tests before persistence, HTTP, or deployment work depends on it.
- Current Firebase server/API behavior informs backend fixtures only. No React component, browser client, Firebase package, import, environment variable, source file, emulator, deploy script, hosting config, or credential is copied into the backend implementation.
- The OpenAPI document replaces implicit coupling to the old React/Firebase client. The Phoenix frontend is validated independently against this published contract.
- The final target scan rejects `firebase`, `firestore`, `gcloud`, Firebase environment names, and old unversioned API routes outside fixture provenance documents.
- The target has no runtime, build, test, or release dependency on the source repository after parity fixtures are committed.
- Intentional Go contract differences are recorded in ADRs and fixture manifests; they are not hidden behind compatibility wrappers.

## 17. Verification gates

Minimum v1 verification:

- Root-package API and import-boundary tests.
- Unit tests for runtime, retry, schema, providers, cache identity, usage, pricing, profiles, traces, stats, diagnostics, and redaction.
- Per-slice JS-to-Go golden parity before each implementation phase exits.
- Endpoint-security tests for SSRF, DNS rebinding, redirects, TLS, origin-bound credentials, and header filtering.
- Postgres migration, repository, auth, cache, and owner-isolation integration tests.
- Gateway tests for all `/api/v1` endpoints and strict envelopes.
- OpenAPI 3.1 validation and router/request/response conformance tests.
- Static backend scans proving there is no Firebase, React/Vite, Phoenix/LiveView, HTML-template, or frontend-asset implementation path.
- OTel trace/metric, `slog` correlation, Collector fanout, telemetry-outage, and bounded-shutdown tests.
- Garage integration tests for canonical redacted bytes, object metadata, short-lived presigning, owner-scoped Postgres references, and non-fatal storage failure behavior.
- Compose artifact and smoke tests for all fifteen required services and the strict MinIO/Langfuse versus Garage/Harden-LLM ownership boundary.
- Static scans for duplicate implementation paths, direct Langfuse exporters, Firebase, application SQLite, Sentry, Temporal, application MinIO use, and Langfuse Garage substitution.
- `go test -race` for deterministic unit and Postgres/gateway integration suites.
- `go vet`, formatting, build, and vulnerability checks.
- Optional live provider smoke only with explicit local credentials.

## 18. Fixed v1 answers

- Implementation repository: `/home/kirill/harden-llm`.
- Source contract repository: `/home/kirill/utility-llm`.
- Public package: module root `hardenllm` only.
- Execution API: one `Client.Call` method returning one detailed `Result`.
- Application database: dedicated Harden-LLM Postgres only; Garage's internal SQLite metadata engine is not an application database contract.
- Harden-LLM object storage: Garage for redacted JSON trace artifacts and diagnostic attachments, indexed by Postgres.
- Reverse proxy: Caddy.
- General diagnostics: OTel Collector, Prometheus, Loki, Tempo, and Grafana.
- LLM diagnostics: required self-hosted Langfuse OSS.
- Langfuse dependencies: the pinned upstream default services, including its own Postgres, Redis, ClickHouse, and MinIO. Harden-LLM does not substitute, share, or migrate them.
- Langfuse bootstrap: headless initial user/organization/project/API keys; no browser setup is required before Collector export.
- Langfuse export: Collector OTLP/HTTP fanout only.
- Logging: `slog` JSON collected by OTel Collector Contrib and stored in Loki.
- Auth: bootstrap-created local users with opaque bearer sessions; only token digests are persisted.
- Frontend boundary: OpenAPI 3.1 REST only. Phoenix LiveView is specified separately and is not part of the backend plan or Compose service count.
- Provider endpoint policy: public HTTPS by default; private access only by exact administrator allowlist.
- Firebase: absent from the target repository and deployment.
- Temporal, application SQLite, Sentry, request idempotency infrastructure, scheduled retention, backup automation, multi-node Garage, and local Langfuse dependency migration: out of v1 scope.

## 19. References

- Go modules: https://go.dev/doc/modules/gomod-ref
- OpenTelemetry Go: https://opentelemetry.io/docs/languages/go/
- OpenTelemetry Collector: https://opentelemetry.io/docs/collector/
- OpenTelemetry GenAI conventions: https://opentelemetry.io/docs/specs/semconv/gen-ai/
- Langfuse self-hosting: https://langfuse.com/self-hosting
- Langfuse infrastructure requirements: https://langfuse.com/self-hosting/configuration/scaling
- Langfuse OpenTelemetry integration: https://langfuse.com/integrations/native/opentelemetry
- Langfuse upstream Compose: https://github.com/langfuse/langfuse/blob/main/docker-compose.yml
- Garage documentation: https://garagehq.deuxfleurs.fr/documentation/
- Grafana Loki: https://grafana.com/docs/loki/latest/
- Grafana Tempo: https://grafana.com/docs/tempo/latest/
- Prometheus: https://prometheus.io/docs/introduction/overview/
- Caddy: https://caddyserver.com/docs/
- OWASP SSRF prevention: https://cheatsheetseries.owasp.org/cheatsheets/Server_Side_Request_Forgery_Prevention_Cheat_Sheet.html
- Phoenix LiveView frontend specification: `plans/from_utility-llm/phoenix-liveview-frontend-spec.md`
