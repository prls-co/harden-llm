# Harden-LLM Backend and REST API Test Specification

## 1. Title and metadata

- Project name: `harden-llm`
- Target repository: `/home/kirill/p/harden-llm`
- Contract source repository: `/home/kirill/p/utility-llm`
- Version: `1.3.0-backend-test-spec`
- Owners: package maintainers and self-hosted runtime implementers
- Date: 2026-07-12
- Document ID: `SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001`
- Related stack specification: `plans/from_utility-llm/self-hosted-go-stack-spec.md`
- Summary: This document is the canonical backend test catalog for building `harden-llm`. It defines one `TEST-###` namespace shared with the backend implementation plan. Tests guide the Go library, versioned REST/OpenAPI gateway, Harden-LLM Postgres records, Garage-backed trace artifacts and diagnostic attachments, provider endpoint security, OpenTelemetry/Grafana/Langfuse diagnostics, and full Docker Compose deployment. It contains no frontend, Phoenix, LiveView, React, browser-session, or asset tests. Langfuse retains its pinned upstream default Postgres, Redis, ClickHouse, and MinIO services; tests reject any local Garage substitution into Langfuse.

## 2. Test strategy

- Test execution occurs in `/home/kirill/p/harden-llm` unless a command explicitly names the source repository.
- `/home/kirill/p/utility-llm` is read only during fixture capture and JS contract verification.
- P00 creates `go.mod`, the test harness, fixture verification scripts, `api/openapi.yaml`, and canonical `Makefile` targets before later phases use those commands.
- Every behavior slice starts with failing target-repository coverage and ends with the same command passing.
- JS-to-Go parity is tested in the phase that ports each behavior. Final parity is an aggregate gate, not the first parity check.
- Provider tests use local `httptest` servers. Public internet and provider credentials are allowed only under the `live` build tag.
- Postgres and Garage tests use isolated projects and never reuse developer data.
- Compose tests use pinned Harden-LLM images, a release/SHA/hash-pinned upstream Langfuse Compose fragment, and an integration overlay that leaves only Caddy externally reachable.
- Langfuse is required in the Compose smoke and retains its upstream default Postgres, Redis, ClickHouse, and MinIO services.
- Garage is required only for Harden-LLM-owned trace artifacts and diagnostic attachments migrated from Firebase Storage. Harden-LLM never uses Langfuse's MinIO.
- Application code emits no direct Langfuse request. Collector fanout is the only Langfuse ingestion path.
- Every created test file contains `SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001` and one or more canonical `TEST-###` comments.

## 3. Canonical commands

P00 creates these targets in the target repository:

```bash
make test-static
make test-unit
make test-parity
make test-integration
make test-api
make test-observability
make test-compose
make test-race
make verify
```

Direct commands remain the smallest RED/GREEN controls in the definitions below. `make verify` aggregates formatting, build, static, unit, parity, integration, API/OpenAPI, observability, Compose artifact, race, vet, and vulnerability gates. Live tests are never included in `make verify`.

## 4. Fixtures and deterministic controls

```text
fixtures/
├── parity/
│   ├── manifest.json
│   ├── requests/
│   ├── responses/
│   ├── cache/
│   ├── schemas/
│   ├── traces/
│   ├── stats/
│   ├── profiles/
│   └── diagnostics/
├── providers/
├── gateway/
├── postgres/
├── artifacts/
├── observability/
└── redaction/
```

Fixture rules:

- `fixtures/parity/manifest.json` records the exact `utility-llm` Git SHA, capture command version, fixture schema version, and SHA-256 for every fixture.
- Fixture capture uses current deterministic JS tests and committed fake inputs; it never captures live provider output.
- JSON fixture keys are canonicalized before hashing.
- Timestamps, IDs, clocks, retry jitter, DNS results, and endpoint addresses are injected in tests.
- Fake secrets use realistic formats but are never real credentials.
- No deterministic test reads provider, Firebase, Langfuse, Grafana, or public-network credentials.
- Compose tests may pull pinned images before the timed readiness interval begins.
- `plans/implementation-status.json` records completed phases; TEST-005 requires traceability only for completed phases and requires all TEST IDs when P07 is complete.

## 5. Static and migration-foundation tests

### TEST-001: target module and repository layout

- Target: `internal/testkit/static_layout_test.go`
- Command: `go test ./internal/testkit/... -run TestTargetLayout -count=1`
- Setup: target repository checkout.
- Assertions:
  - `go.mod` declares `github.com/prls-co/harden-llm`.
  - Root package files declare `package hardenllm`.
  - P00 foundation paths exist: `cmd/harden-llm-gateway/main.go`, `api/openapi.yaml`, `internal/testkit`, `internal/artifacts`, `scripts`, `fixtures/parity`, and `plans/implementation-status.json`.
  - The root package is importable without importing `internal` packages.
- Pass criteria: command exits zero and reports one canonical target layout.
- Expected runtime: 5 seconds.

### TEST-002: public API and implementation boundaries

- Target: `internal/testkit/static_boundaries_test.go`
- Command: `go test ./internal/testkit/... -run TestImplementationBoundaries -count=1`
- Setup: target repository checkout; Go AST/package loader.
- Assertions:
  - Root package exposes `New`, `Client.Call`, `Options`, `Request`, `Result`, `Profile`, `ProfileCatalog`, `CredentialResolver`, `EndpointPolicy`, `CacheStore`, `ArtifactStore`, and `ArtifactRef` only as documented public execution surfaces.
  - Built-in provider adapter types and constructors are not exported.
  - The gateway imports the root package and cannot import internal runtime/provider/retry/schema/cache-key packages directly.
  - Provider payloads, retry classifiers, schema transforms, cache hashing, pricing, trace projection, and redaction each have one internal implementation home.
  - No simple/detailed execution split or expanded-result option exists.
- Pass criteria: AST/dependency scan and root external-package compile test pass.
- Expected runtime: 10 seconds.

### TEST-003: forbidden target dependencies and duplicate telemetry paths

- Target: `internal/testkit/static_dependencies_test.go`
- Command: `go test ./internal/testkit/... -run TestForbiddenDependencies -count=1`
- Setup: backend-owned source and test paths, `go.mod`, backend build manifests, base Compose config, and base Caddy config. `frontend/` and `deploy/frontend/` are owned by the separate frontend specification and are excluded from content scans.
- Assertions:
  - Backend production/test/deploy code contains no Firebase, Firestore, Firebase Auth, Functions, Hosting, or Storage dependency.
  - Backend code contains no Phoenix, LiveView, React, Vite, HTML-template, browser-session, or frontend-asset implementation.
  - Go production dependencies contain no application SQLite, Temporal, Sentry, MinIO-specific client, or Langfuse SDK/client.
  - Collector configuration contains exactly one Langfuse OTLP/HTTP exporter.
  - Application code has no direct Langfuse host/key configuration or ingestion request.
  - Garage appears only in the Harden-LLM artifact implementation/deployment, while MinIO appears only in the byte-for-byte pinned upstream Langfuse fragment and its deployment secrets.
  - Harden-LLM application configuration contains no MinIO endpoint or credential; Langfuse configuration contains no Garage endpoint or credential.
- Pass criteria: scan reports zero forbidden paths and one Collector-owned Langfuse path.
- Expected runtime: 10 seconds.

### TEST-004: source fixture provenance and integrity

- Target: `scripts/verify-parity-fixtures.mjs`
- Command: `node scripts/verify-parity-fixtures.mjs`
- Setup: committed parity fixture tree and manifest.
- Assertions:
  - Source SHA is a full Git commit.
  - Every manifest entry exists and matches its SHA-256.
  - No untracked fixture exists outside the manifest.
  - Fixture schema versions are supported.
  - Secret scan passes.
- Pass criteria: script exits zero with counts for every parity fixture class.
- Expected runtime: 10 seconds.

### TEST-005: canonical test traceability

- Target: `internal/testkit/static_traceability_test.go`
- Command: `go test ./internal/testkit/... -run TestTraceability -count=1`
- Setup: implementation plan, this test specification, and target test files.
- Assertions:
  - Every `TEST-###` referenced by the plan is defined once in this specification.
  - Every test allocated to a completed phase appears in at least one target test file.
  - Completion of P07 requires every defined test ID to appear in the target test tree.
  - No alternate test-ID prefix exists.
  - Duplicate test IDs are rejected.
- Pass criteria: all active phase IDs map to executable test files without a second namespace.
- Expected runtime: 5 seconds.

## 6. Root library, retry, schema, and cache tests

### TEST-006: root client result and runtime record

- Target: `client_test.go`
- Command: `go test . -run TestClientCallResult -count=1`
- Setup: fake provider, fake cache, fixed clock and IDs.
- Assertions:
  - `Client.Call` returns one `Result` with `Output`, call/trace IDs, usage, cost, attempts, and cache facts.
  - `Result.Output` matches the source JS direct-return fixture.
  - Result metadata and telemetry hooks receive values from the same normalized internal record.
  - No exporter or deployment environment is required by the library.
- Pass criteria: text, structured, cache-hit, and error table cases pass.
- Expected runtime: 10 seconds.

### TEST-007: observability context merge and cache exclusion

- Target: `internal/runtime/context_test.go`
- Command: `go test ./internal/runtime/... -run TestObservabilityContext -count=1`
- Setup: overlapping request/default contexts and canonical cache fixtures.
- Assertions:
  - Merge order is deterministic and documented.
  - Standard IDs, environment, release, prompt labels, tags, and metadata normalize consistently.
  - Context changes do not alter cache hashes.
  - High-cardinality context is never promoted to metric labels.
- Pass criteria: all merge and exclusion fixtures pass.
- Expected runtime: 5 seconds.

### TEST-008: retry classification, budget, backoff, and cancellation

- Target: `internal/retry/retry_test.go`
- Command: `go test ./internal/retry/... -run TestRetryContract -count=1`
- Setup: table of rate-limit, server, network, parse, schema, refusal, invalid-request, auth, cancellation, and timeout errors; fake timer; jitter seed `12001`.
- Assertions:
  - `maxAttempts` is total attempts.
  - Retry categories match source fixtures.
  - `Retry-After` and exponential backoff are capped.
  - Cancellation before an attempt or during wait stops immediately.
  - Attempt and wait metadata are exact.
- Pass criteria: all categories and attempt-count tables pass without wall-clock sleeps.
- Expected runtime: 10 seconds.

### TEST-009: structured repair and backup-profile plan

- Target: `internal/runtime/repair_backup_test.go`
- Command: `go test ./internal/runtime/... ./internal/retry/... -run 'TestStructuredRepair|TestBackupProfiles' -count=1`
- Setup: malformed/schema-invalid outputs and profile graphs with cycles, duplicates, missing references, and depth boundaries.
- Assertions:
  - Repair consumes the existing attempt budget and validates `{ repair, data }`.
  - Backup profiles apply only to current availability categories.
  - Current flat-reference, cycle, duplicate, missing-reference, and maximum-depth behavior matches source fixtures.
  - Caller context remains the overall deadline across candidates.
- Pass criteria: repair and backup fixtures match source behavior exactly.
- Expected runtime: 10 seconds.

### TEST-010: contracted schema validation and parse diagnostics

- Target: `internal/schema/schema_test.go`
- Command: `go test ./internal/schema/... -run TestSchemaContract -count=1`
- Setup: valid/invalid contracted JSON Schema and parser fixtures.
- Assertions:
  - Supported keywords normalize consistently.
  - Unsupported shapes fail closed.
  - Parse diagnostics and schema errors use stable categories and safe excerpts.
  - Repair data extraction returns only validated `data`.
- Pass criteria: Go output matches schema and parser parity fixtures.
- Expected runtime: 10 seconds.

### TEST-011: cache identity, modes, and replay parity

- Target: `internal/cachekey/cache_test.go`, `client_cache_test.go`
- Command: `go test . ./internal/cachekey/... -run 'TestCacheIdentity|TestCacheReplay' -count=1`
- Setup: source cache-hash fixtures and fake provider/cache store.
- Assertions:
  - Semantic request fields alter the hash.
  - telemetry, retries, deadline, cancellation, and UI context do not alter the hash.
  - `off`, `cache`, and `refresh` modes are exact.
  - A cache hit skips provider invocation and replays normalized usage/cost/cache facts.
- Pass criteria: every Go hash equals its source fixture and replay behavior passes.
- Expected runtime: 10 seconds.

## 7. Provider and endpoint-security tests

### TEST-012: provider request payload parity

- Target: `internal/providers/requests_test.go`
- Command: `go test ./internal/providers/... -run TestProviderRequestParity -count=1`
- Setup: local HTTP servers and request goldens for OpenAI-compatible Chat, OpenAI Responses, Gemini GenerateContent, Anthropic Messages, and generic OpenAI-compatible endpoints.
- Assertions:
  - Paths, methods, headers, model IDs, prompts, schemas, tools, reasoning options, token limits, and native options match source fixtures.
  - Contracted-only options never leak into native mode.
  - Unknown native options follow the current provider-specific contract.
- Pass criteria: canonicalized captured requests match every golden.
- Expected runtime: 15 seconds.

### TEST-013: provider response and error normalization

- Target: `internal/providers/normalization_test.go`
- Command: `go test ./internal/providers/... -run TestProviderNormalization -count=1`
- Setup: success, refusal, empty, malformed, usage, cost, 4xx, 429, 5xx, timeout, and network fixtures.
- Assertions:
  - Output, usage, cost, finish/refusal status, and safe raw hashes normalize consistently.
  - Error status, category, retryability, and safe metadata match source fixtures.
  - Secrets and raw authorization values do not appear in results or errors.
- Pass criteria: all provider tables match normalized goldens.
- Expected runtime: 15 seconds.

### TEST-014: provider endpoint SSRF and credential-origin policy

- Target: `internal/providers/endpoint_policy_test.go`
- Command: `go test ./internal/providers/... -run TestEndpointPolicy -count=1`
- Setup: injected DNS resolver/dialer; IPv4/IPv6 public, private, loopback, link-local, multicast, metadata, redirect, rebinding, TLS, and header fixtures.
- Assertions:
  - Public HTTPS is accepted unless restricted by configured hosts.
  - Unsafe schemes, userinfo, redirects, private/special addresses, and DNS rebinding are rejected.
  - Exact private host/CIDR allowlist entries permit intended self-hosted providers.
  - Credentials are bound to normalized scheme/host/port and never follow a redirect.
  - Hop-by-hop, host, forwarded, and proxy headers are removed.
  - TLS verification cannot be disabled through provider options.
- Pass criteria: adversarial table passes for IPv4 and IPv6 with zero unintended dials.
- Expected runtime: 15 seconds.

## 8. Usage, profiles, traces, and diagnostics tests

### TEST-015: usage and pricing parity

- Target: `internal/pricing/usage_cost_test.go`
- Command: `go test ./internal/pricing/... -run TestUsageCostParity -count=1`
- Setup: reported-cost, calculated-cost, cache-read, cache-creation, reasoning-token, and unknown-cost fixtures.
- Assertions:
  - Usage groups and token totals match source behavior.
  - Reported cost wins where required; otherwise versioned pricing snapshots calculate cost.
  - Unknown cost remains unknown and is never coerced to zero.
- Pass criteria: normalized usage/cost equals every golden.
- Expected runtime: 10 seconds.

### TEST-016: domain trace, observations, and stats parity

- Target: `internal/traces/parity_test.go`, `internal/stats/parity_test.go`
- Command: `go test ./internal/traces/... ./internal/stats/... -run TestParity -count=1`
- Setup: successful, failed, timeout, cache-hit, retried, and repaired call fixtures.
- Assertions:
  - Domain trace status, usage, cost, attempts, cache facts, and monitoring summary match source fixtures.
  - Observation sequence covers cache lookup, attempts, retry waits, repair, and cache write.
  - Success, failure, and parse-error cases produce canonical redacted trace-artifact projections, deterministic artifact kinds, and safe object-key components matching captured Firebase Storage semantics.
  - Strict stats totals and merge behavior match source fixtures.
- Pass criteria: canonical JSON outputs match trace/stats goldens.
- Expected runtime: 10 seconds.

### TEST-017: profile catalog validation and parity

- Target: `internal/profiles/profile_test.go`
- Command: `go test ./internal/profiles/... -run TestProfileParity -count=1`
- Setup: current profile catalog fixtures, invalid names/endpoints/defaults, and backup graphs.
- Assertions:
  - Profile shape, API inference types, pricing, model list, defaults, and backup references match source behavior.
  - Graph validation preserves duplicate, cycle, missing-reference, and maximum-depth rules.
  - No nested backup object or alternate compatibility shape is accepted.
- Pass criteria: source catalog goldens round-trip and invalid fixtures fail with stable fields.
- Expected runtime: 10 seconds.

### TEST-018: credential encryption and bundle contract

- Target: `internal/profiles/credentials_test.go`
- Command: `go test ./internal/profiles/... -run TestCredentialBundle -count=1`
- Setup: deterministic test key IDs, fixed nonces through injected random reader, fake credentials, and source bundle fixtures.
- Assertions:
  - AES-256-GCM uses random production nonces, key IDs, and owner/credential/origin AAD.
  - Wrong key, wrong AAD, or modified ciphertext fails.
  - API state never exposes raw keys or ciphertext internals.
  - Canonical encrypted bundles round-trip only with the required key.
- Pass criteria: crypto tamper tables and bundle parity pass.
- Expected runtime: 10 seconds.

### TEST-019: diagnostics bundle and redaction

- Target: `internal/diagnostics/bundle_test.go`
- Command: `go test ./internal/diagnostics/... -run TestDiagnosticsBundle -count=1`
- Setup: failed-call fixture with fake secrets in prompts, headers, URLs, errors, config, traces, and logs.
- Assertions:
  - Bundle includes safe runtime identity, attempts, timing, cache, usage, cost, endpoint host, and environment fingerprint.
  - Shared redaction removes provider keys, bearer tokens, cookies, URL userinfo/query secrets, encryption keys, and ciphertext internals.
  - Diagnostic output matches source semantic fields while attachment references use Harden-LLM Garage artifact identities instead of Firebase URLs or APIs.
  - An artifact-store failure adds one bounded redacted persistence observation and does not change an otherwise successful provider result.
- Pass criteria: secret leak count is zero and canonical bundle validation passes.
- Expected runtime: 10 seconds.

## 9. Postgres, Garage, auth, and gateway tests

### TEST-020: migrations and repository contracts

- Target: `internal/postgres/repository_test.go`
- Command: `go test ./internal/postgres/... -tags=integration -run TestRepositoryContract -count=1`
- Setup: empty isolated Postgres database; concurrent migration runners.
- Assertions:
  - Migrations apply once under advisory lock and record versions.
  - Required tables, constraints, owner columns, timestamps, and indexes exist.
  - Profiles, credentials, state, runs, traces, artifact indexes, observations, cache, stats, and sessions round-trip.
  - Application migrations and credentials name only the Harden-LLM Postgres service and cannot address the upstream Langfuse Postgres service.
- Pass criteria: clean and already-migrated starts pass without races or cross-service credentials.
- Expected runtime: 90 seconds.

### TEST-021: Postgres cache concurrency

- Target: `internal/postgres/cache_test.go`
- Command: `go test ./internal/postgres/... -tags=integration -run TestCacheConcurrency -count=1`
- Setup: concurrent identical owner/version/hash writes and reads.
- Assertions:
  - Unique constraints and upsert semantics leave one canonical row.
  - Concurrent reads never observe malformed partial JSON.
  - Different owners and cache versions remain isolated.
- Pass criteria: repeated concurrent table cases pass under `-race` in TEST-036.
- Expected runtime: 30 seconds.

### TEST-040: Garage artifact-store contract

- Target: `internal/artifacts/garage_test.go`
- Command: `go test ./internal/artifacts/... -tags=integration -run TestGarageArtifactStore -count=1`
- Setup: isolated pinned `dxflrs/garage:v2.3.0` Compose project using `/garage server --single-node --default-bucket`, persistent temporary metadata/data volumes, `db_engine = "sqlite"`, `replication_factor = 1`, `consistency_mode = "consistent"`, a private test bucket supplied through Garage's default-bucket environment variables, fixed clock, and fake credentials.
- Assertions:
  - The Garage-backed implementation writes and reads canonical `application/json` bytes for trace, redacted parse-failure response, and diagnostic-event artifacts.
  - Returned key, SHA-256, byte length, and content type match the exact stored bytes; unique artifact IDs prevent overwrite dependence.
  - A short-lived presigned GET succeeds before expiry and fails after the injected expiry boundary.
  - Unsafe object-key input, wrong bucket credentials, and cross-prefix reads fail.
  - Harden-LLM artifact configuration contains no MinIO endpoint or credential and the Garage test configuration contains no Langfuse setting.
  - Timeout, unavailable-store, and failed-upload cases return bounded typed errors without leaking endpoint credentials.
  - Restarting Garage with the same metadata/data volumes and default-bucket credentials preserves the object and does not require a custom bootstrap job.
- Pass criteria: all real-Garage round trips, restart persistence, expiry, isolation, ownership-boundary, and failure tables pass.
- Expected runtime: 90 seconds.

### TEST-022: auth, owner isolation, and transactional profile save

- Target: `internal/gateway/auth_profile_test.go`
- Command: `go test ./internal/gateway/... -tags=integration -run TestAuthProfileContract -count=1`
- Setup: two bootstrap users, isolated Postgres, fake safe provider endpoint, fixed session clock.
- Assertions:
  - Argon2id login returns one opaque bearer token once and stores only its SHA-256 digest with expiry and owner metadata.
  - Protected routes require exactly one valid `Authorization: Bearer` credential.
  - Logout, expiry, revocation, malformed schemes, duplicated authorization headers, and unknown tokens fail with one non-enumerating envelope.
  - Login/session responses, logs, traces, and database rows do not disclose the token after initial login.
  - The backend sets no session cookie and has no CSRF or CORS wildcard path.
  - Users cannot read or mutate each other's profiles, history, traces, state, cache, or bundles.
  - Profile probe runs before the short database commit and failed probe leaves prior state unchanged.
  - Probe and model refresh use TEST-014 endpoint policy.
- Pass criteria: auth/session/isolation and profile transaction tables pass.
- Expected runtime: 60 seconds.

### TEST-023: gateway health, envelope, decoding, and limits

- Target: `internal/gateway/http_contract_test.go`
- Command: `go test ./internal/gateway/... -run TestHTTPContract -count=1`
- Setup: `httptest` server and fake readiness checks.
- Assertions:
  - `/healthz` ignores downstream state; `/readyz` requires Postgres and current migrations.
  - Non-health responses use `{ state, result, error }`.
  - Unknown fields, trailing JSON, multiple values, oversized bodies/fields, and unknown routes fail consistently.
  - Responses disable caching and errors contain no secrets.
- Pass criteria: request/response table passes for success and failure cases.
- Expected runtime: 15 seconds.

### TEST-024: gateway state, profiles, models, history, and trace routes

- Target: `internal/gateway/resource_routes_test.go`
- Command: `go test ./internal/gateway/... -tags=integration -run TestResourceRoutes -count=1`
- Setup: authenticated user, seeded Postgres state and Garage artifact, fake provider, fixed IDs and clock.
- Assertions:
  - Every resource route in the stack specification uses `/api/v1` and owner authorization.
  - Bundle replacement is atomic.
  - Model refresh preserves the previous list on provider failure.
  - History pagination is stable by timestamp and ID.
  - `/api/v1/traces/{traceID}` and `/api/v1/traces/{traceID}/artifacts/{artifactID}` use the authenticated owner.
  - Artifact access returns a short-lived Garage presigned redirect only after authorization; object keys and durable public URLs are absent from API state.
- Pass criteria: route table and persistence assertions pass.
- Expected runtime: 90 seconds.

### TEST-025: gateway run route uses the root library

- Target: `internal/gateway/run_test.go`
- Command: `go test ./internal/gateway/... -tags=integration -run TestRunRoute -count=1`
- Setup: saved profile, fake provider, Postgres cache/trace/artifact-index stores, fake `ArtifactStore`, and injected root `Client` constructor.
- Assertions:
  - Run resolves/decrypts profile state and calls `Client.Call` once.
  - Text and structured output return `Result.Output` plus redacted state.
  - Domain run/trace/history records use the normalized `Result` metadata.
  - Successful artifact references create available owner-scoped Postgres metadata; failed artifact persistence leaves no available row and does not change the provider result.
  - Invalid request or unsafe endpoint fails before provider invocation.
  - The gateway enforces the 60-second contract maximum, rejects a configured or requested increase, permits a shorter deployment/request deadline, returns the documented 504 `run_timeout`, cancels the root call, and never retries the HTTP operation.
  - Handler files contain no provider payload, retry, schema, pricing, or cache-key logic.
- Pass criteria: success/failure/cache tables pass and boundary scan remains green.
- Expected runtime: 60 seconds.

## 10. REST and migration-boundary tests

### TEST-026: OpenAPI and router conformance

- Target: `api/openapi.yaml`, `internal/gateway/openapi_contract_test.go`
- Command: `go test ./internal/gateway/... -run TestOpenAPIContract -count=1`
- Setup: parsed OpenAPI 3.1 document, live chi router metadata, request/response fixtures, and deterministic examples.
- Assertions:
  - The document is valid OpenAPI 3.1 and defines stable operation IDs, request schemas, success/error envelopes, examples, limits, and the opaque bearer security scheme.
  - Every implemented non-health route exists once in OpenAPI and every OpenAPI operation maps to one router operation.
  - Login is the only unauthenticated `/api/v1` operation; all other application operations require bearer auth.
  - Contract fixtures for state, profile, bundle, model refresh, history, run, trace, artifact redirect, auth, pagination, and errors validate against the document.
  - No schema, operation, description, or extension depends on Phoenix, LiveView, React, browser cookies, or frontend implementation types.
- Pass criteria: OpenAPI parsing, route parity, security, and request/response fixture tables pass.
- Expected runtime: 20 seconds.

### TEST-027: backend-owned paths contain no Firebase or frontend implementation

- Target: `internal/testkit/firebase_frontend_absence_test.go`
- Command: `go test ./internal/testkit/... -run TestFirebaseFrontendAbsent -count=1`
- Setup: root Go files, `cmd/`, `internal/`, `api/`, backend fixture/scripts, `go.mod`, the backend `Makefile` gates, base deployment files, and base Compose/Caddy manifests. Planning documents, `frontend/`, and `deploy/frontend/` are excluded from literal-name scans.
- Assertions:
  - No backend dependency, import, environment name, configuration, deploy script, server, emulator, or production code calls Firebase Auth, Firestore, Functions, Hosting, or Storage.
  - No backend package contains Phoenix, LiveView, React, Vite, JSX, HEEx, HTML-template, frontend asset, browser-cookie, or browser-CSRF implementation code.
  - Backend tests and release commands do not build or test any frontend application.
  - The base fifteen-service Compose topology and base Caddy configuration do not depend on or route a frontend service; the optional frontend overlay is tested under the frontend specification.
  - Fixture provenance may name the source repository but cannot create a runtime dependency.
- Pass criteria: the scoped backend dependency/AST/filesystem scan exits zero.
- Expected runtime: 10 seconds.

## 11. Observability and diagnostics tests

### TEST-028: OTel spans, metrics, and bounded attributes

- Target: `internal/runtime/telemetry_test.go`, `internal/gateway/telemetry_test.go`
- Command: `go test ./internal/runtime/... ./internal/gateway/... -run TestOTelContract -count=1`
- Setup: in-memory trace exporter and metric reader; fixed trace IDs.
- Assertions:
  - Required HTTP, auth, profile, provider, attempt, schema, retry, cache, database, Garage artifact, and persistence spans exist.
  - GenAI attributes carry safe provider/model/call metadata and normalized usage/cost.
  - Prometheus dimensions are limited to the bounded label allowlist.
  - Prompt/response bodies and secret-shaped values are absent from general OTel spans and metrics.
- Pass criteria: required signal coverage is 100% and forbidden attribute count is zero.
- Expected runtime: 20 seconds.

### TEST-029: `slog` JSON correlation and redaction

- Target: `internal/gateway/logging_test.go`
- Command: `go test ./internal/gateway/... -run TestStructuredLogging -count=1`
- Setup: buffer-backed JSON handler during successful and failed traced requests.
- Assertions:
  - Every line is valid JSON.
  - Trace/span IDs and safe request/run/call/profile/model/provider/outcome/category fields appear where available.
  - Prompts, responses, credentials, auth headers, cookies, ciphertext, and unsafe URLs are absent.
  - One application log call creates one record; no parallel logging implementation exists.
- Pass criteria: schema and secret scans pass for every captured log line.
- Expected runtime: 10 seconds.

### TEST-030: Collector pipelines and single Langfuse fanout

- Target: `internal/deploytest/collector_test.go`
- Command: `go test ./internal/deploytest/... -run TestCollectorPipelines -count=1`
- Setup: parsed `deploy/otel/collector.yaml` and fake OTLP endpoints for traces, metrics, and logs.
- Assertions:
  - Traces export to Tempo.
  - Complete `service.name=harden-llm-gateway` traces export once to Langfuse over OTLP/HTTP with root and children preserved.
  - Metrics expose one Prometheus scrape endpoint.
  - OTel log records mirrored from the composed `slog` handler export to Loki OTLP/HTTP.
  - Memory limiter, batch, bounded queues, retry limits, and redaction processors are present.
  - Langfuse's own service spans do not loop back into Langfuse ingestion.
- Pass criteria: configuration parse and fake-endpoint signal counts match expectations.
- Expected runtime: 20 seconds.

### TEST-031: telemetry backend failure and bounded shutdown

- Target: `internal/gateway/telemetry_failure_test.go`
- Command: `go test ./internal/gateway/... -run TestTelemetryFailureIsolation -count=1`
- Setup: failing/hanging OTLP exporter, fixed 2-second shutdown budget, fake provider.
- Assertions:
  - Provider result is unchanged when Collector is unavailable.
  - Telemetry queues remain bounded and failure is reported safely to stderr/logging fallback.
  - Gateway shutdown attempts flush and returns within the configured budget.
  - No goroutine remains blocked after shutdown.
- Pass criteria: call succeeds, shutdown duration is at most 2 seconds plus 250 ms test margin, and leak check passes.
- Expected runtime: 10 seconds.

### TEST-032: Grafana dashboard and datasource artifacts

- Target: `internal/deploytest/grafana_test.go`
- Command: `go test ./internal/deploytest/... -run TestGrafanaArtifacts -count=1`
- Setup: provisioned datasource YAML and dashboard JSON.
- Assertions:
  - Prometheus, Loki, and Tempo datasources use stable provisioned UIDs.
  - Dashboards contain required gateway, provider, retry, cache, usage/cost, schema/repair, Postgres, Garage artifact, Collector, and persistence panels.
  - Queries use only defined bounded labels.
  - Trace/log correlation links use trace IDs.
- Pass criteria: all artifacts parse and required panels/queries are present.
- Expected runtime: 10 seconds.

## 12. Deployment tests

### TEST-033: Compose, Langfuse dependencies, and Caddy artifacts

- Target: `internal/deploytest/compose_caddy_test.go`
- Command: `go test ./internal/deploytest/... -tags=compose -run TestComposeCaddyContract -count=1`
- Setup: effective `docker compose config`, Caddyfile, Harden-LLM image manifest, `deploy/langfuse/docker-compose.upstream.yml`, and `deploy/langfuse/UPSTREAM.md` provenance record.
- Assertions:
  - All fifteen required services exist: Caddy, gateway, Harden-LLM Postgres, Garage, Collector, Prometheus, Loki, Tempo, Grafana, Langfuse web/worker, upstream Langfuse Postgres, ClickHouse, Redis, and MinIO.
  - `docker-compose.upstream.yml` matches the recorded released Langfuse commit and SHA-256 byte for byte and retains its default Postgres, Redis, ClickHouse, and MinIO dependency graph.
  - The Langfuse integration overlay changes only generated secrets, public URL, shared private network membership, and host-port exposure; it does not replace or share a Langfuse dependency.
  - Named volumes and health checks exist; Harden-LLM-owned image tags/digests are pinned and upstream Langfuse image choices match the pinned fragment.
  - Garage uses the pinned v2.3 single-node/default-bucket startup path with persistent metadata/data volumes and maps one bucket-scoped credential into Garage and gateway environment names without a custom bootstrap service.
  - Only Caddy publishes externally reachable host ports in the effective production topology.
  - Caddy routes API, Grafana, and Langfuse hostnames and applies TLS, body limits, and security headers without serving frontend assets.
  - The base Caddyfile has one trusted `conf.d` import extension point, no frontend fragment, and no duplicated backend route definitions.
  - Caddy routes the Garage S3 API on the configured artifact hostname while Garage administration/RPC routes remain private.
  - No Phoenix/LiveView or other frontend service is part of the fifteen-service backend topology.
  - Langfuse headless user/organization/project/key initialization supplies the Collector ingestion credentials without a setup step.
  - Harden-LLM uses only Garage for artifacts; Langfuse uses only its upstream MinIO. Their endpoints, buckets, and credentials do not cross.
  - No Firebase, application SQLite, Sentry, Temporal, or locally substituted Langfuse dependency exists.
- Pass criteria: parser tests and `docker compose config --quiet` pass.
- Expected runtime: 20 seconds.

### TEST-034: full Compose signal and application smoke

- Target: `internal/smoke/compose_smoke_test.go`
- Command: `go test ./internal/smoke/... -tags=compose -run TestComposeSmoke -count=1`
- Setup: clean named test project, production Compose plus pinned upstream Langfuse fragment, private integration overlay, and `deploy/test/compose.smoke.yml`; reference hardware; images already available; generated non-production secrets.
- Assertions:
  - All fifteen services become healthy within 300 seconds.
  - The test-only private `fake-provider` service is reachable only by the gateway and publishes no host port.
  - API routes through Caddy and gateway readiness reaches Harden-LLM Postgres and Garage.
  - Login returns an opaque bearer token that authenticates the smoke lifecycle without a browser cookie or CSRF path.
  - One fake-provider run creates application state and an available artifact index in Harden-LLM Postgres.
  - The linked canonical redacted trace artifact is fetched from Garage through an authenticated gateway route and short-lived Caddy artifact-host URL; its SHA-256 and byte length match Postgres.
  - Its trace reaches Tempo and Langfuse exactly once, metric reaches Prometheus, and correlated log reaches Loki.
  - Langfuse event ingestion succeeds with the unchanged upstream MinIO endpoint and no Garage setting in Langfuse.
  - Grafana datasources are healthy.
  - MinIO is used only by Langfuse, and Garage is used only by Harden-LLM.
- Pass criteria: end-to-end correlation IDs are found in every intended backend with zero public non-Caddy ports.
- Expected runtime: 360 seconds.

## 13. Aggregate and live tests

### TEST-035: aggregate parity gate

- Target: all parity-bearing Go tests and `scripts/verify-parity-fixtures.mjs`
- Command: `make test-parity`
- Setup: committed fixture manifest and all completed library slices.
- Assertions:
  - Fixture integrity passes.
  - Request, response, retry, schema, cache, usage/cost, trace/stats, profile, bundle, and diagnostics parity tests pass.
  - Every intentional difference has an ADR and fixture-manifest annotation.
- Pass criteria: target exits zero without reading the source repository at runtime.
- Expected runtime: 120 seconds.

### TEST-036: full deterministic certification

- Target: all backend-owned paths and the base fifteen-service deployment; `frontend/` and `deploy/frontend/` are excluded
- Command: `make verify`
- Setup: Go and Node dependencies installed, isolated Harden-LLM Postgres and Garage, pinned Harden-LLM images, and recorded upstream Langfuse fragment/images.
- Assertions:
  - Formatting, build, static, unit, parity, integration, API/OpenAPI, observability, Compose artifact, race, vet, and `govulncheck` gates pass.
  - Integration packages also run under `-race`.
  - No live provider credential is required.
- Pass criteria: `make verify` exits zero.
- Expected runtime: 900 seconds.

### TEST-037: live provider smoke

- Target: `internal/providers/live_test.go`
- Command: `go test ./internal/providers/... -tags=live -run TestLiveProviders -count=1`
- Setup: explicit local provider credentials and model IDs; endpoint policy enabled.
- Assertions:
  - Configured providers return tiny text and supported structured output.
  - Usage/cost contract is valid where provider data is available.
  - No live output enters committed fixtures or evidence without redaction.
- Pass criteria: every explicitly configured provider passes.
- Expected runtime: 240 seconds.

### TEST-038: live self-hosted gateway lifecycle

- Target: `internal/smoke/live_gateway_test.go`
- Command: `go test ./internal/smoke/... -tags=live -run TestLiveGatewayLifecycle -count=1`
- Setup: running full stack, bootstrap test user, explicit provider credential.
- Assertions:
  - Login, profile save/probe, model refresh, run, trace retrieval, authenticated Garage artifact retrieval, bundle export, profile deletion, and test-data cleanup pass.
  - Correlated diagnostics appear in Grafana backends and Langfuse without secret leakage.
- Pass criteria: lifecycle completes and cleanup removes test application records.
- Expected runtime: 360 seconds.

### TEST-039: timeout RCA policy guard

- Target: `internal/testkit/timeout_policy_test.go`
- Command: `go test ./internal/testkit/... -run TestTimeoutPolicy -count=1`
- Setup: target diff, timeout baseline manifest, `ker/` and evidence metadata.
- Assertions:
  - A timeout increase requires an RCA recording exact phase, start proof, failed timings, comparable successes, p95/max, configured timeout, headroom, root cause, and rationale.
  - The unchanged baseline records the 60-second gateway maximum run duration and the frontend-independent backend gate does not infer or pad a client timeout.
  - The initial 300-second Compose readiness budget records its Langfuse startup basis and is not treated as a later increase.
- Pass criteria: unchanged/reduced timeouts pass; unsupported increases fail.
- Expected runtime: 10 seconds.

## 14. Evidence requirements

Each phase records under ignored `plans/evidence/harden-llm/<run-id>/`:

- target and source Git SHAs;
- exact commands and exit codes;
- Go, Node, Docker, and Compose versions;
- test and evaluation results;
- fixture manifest hash;
- redacted environment-variable names, never values;
- secret-scan result;
- Compose service readiness timings when applicable;
- pinned upstream Langfuse release, commit, Compose SHA-256, and resolved image digests when applicable;
- live-test status and cleanup result when applicable.

## 15. Phase allocation

| Phase | Tests first implemented or activated |
| --- | --- |
| P00 | TEST-001 through TEST-005 |
| P01 | TEST-006 through TEST-011 |
| P02 | TEST-012 through TEST-019 |
| P03 | TEST-020 through TEST-022 and TEST-040 |
| P04 | TEST-023 through TEST-027 |
| P05 | TEST-028 through TEST-032 |
| P06 | TEST-033 and TEST-034 |
| P07 | TEST-035 through TEST-039 |

## 16. Completion criteria

The backend v1 test program is complete when TEST-001 through TEST-036, TEST-039, and TEST-040 pass, TEST-037 and TEST-038 pass when explicit live certification is required, all backend target test files use the single `TEST-###` namespace, OpenAPI and router behavior conform, backend-owned paths have no Firebase or frontend implementation surface, backend gates do not invoke `frontend/`, Collector fanout is the only Langfuse export path, Garage is the only Harden-LLM artifact store, Langfuse retains its pinned upstream MinIO dependency, and the full fifteen-service Compose smoke proves correlated API, artifact, Tempo, Loki, Prometheus, Grafana, and Langfuse diagnostics.
