# Harden-LLM Backend and REST API Test Specification

## 1. Title and metadata

- Project name: `harden-llm`
- Target repository: `/home/kirill/harden-llm`
- Contract source repository: `/home/kirill/utility-llm`
- Version: `1.3.4-resource-scale-follow-up`
- Owners: package maintainers and self-hosted runtime implementers
- Date: 2026-09-15
- Document ID: `SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001`
- Related stack specification: `plans/from_utility-llm/self-hosted-go-stack-spec.md`
- Summary: This document is the canonical backend test catalog for building `harden-llm`. It defines one `TEST-###` namespace shared with the backend implementation plan. Tests guide the Go library, versioned REST/OpenAPI gateway, Harden-LLM Postgres records, Garage-backed trace artifacts and diagnostic attachments, provider endpoint security, OpenTelemetry/Grafana/Langfuse diagnostics, and full Docker Compose deployment. It contains no frontend, Phoenix, LiveView, React, browser-session, or asset tests. Langfuse retains its pinned upstream default Postgres, Redis, ClickHouse, and MinIO services; tests reject any local Garage substitution into Langfuse.
- Resource-efficiency addendum date: `2026-09-21`; plan: `plans/production-scale-efficiency-plan.md`.

## 2. Test strategy

- Test execution occurs in `/home/kirill/harden-llm` unless a command explicitly names the source repository.
- `/home/kirill/utility-llm` is read only during fixture capture and JS contract verification.
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
- Phoenix/browser cases use the separate `WEB-TEST-###` namespace from
  `SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001`. Backend traceability scans exclude
  that prefix so `WEB-TEST-041` is not misclassified as a missing backend
  `TEST-041` definition.

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

### Parallel feedback hierarchy addendum

The separate `PLAN-HARDEN-LLM-TEST-FEEDBACK-002` contract assigns each case to
the lowest sufficient tier (the lowest sufficient fidelity tier). T0-T2 are the credential-free, offline
coding loop; T3 owns real service and race boundaries; T4 owns native browser
behavior; and T5 owns full Compose, deployed, or explicitly authorized live
provider behavior. A lower tier may replace an environment boundary, but it
must preserve the exact assertion oracle. When an expensive-tier defect is
found, add a cheap root-invariant regression whenever that invariant is
representable below the boundary. Do not add a DOM emulator, weaken an
assertion, or serialize a test without a named global resource and rationale.

The canonical selection source is `test/test-tiers.json`; the canonical
execution path is `scripts/run-test-tier.mjs`. Make and CI targets are thin
delegates. Ordinary Postgres/Garage integration uses runner-owned services and
unique leases after TEST-042 proves isolation; destructive Garage restart uses
the exclusive resource in TEST-043. These additions do not alter the backend
runtime contract or the meaning of `make verify`.

### TEST-041: tier policy, command composition, and fast boundary

- Target: `internal/testkit/test_tier_policy_test.go`, `scripts/verify-test-tiers.mjs`, `test/test-tiers.json`.
- Command: `go test ./internal/testkit/... -run TestTestTierPolicy -count=1`.
- Assertions: every task has one tier/resource/cleanup/test-ID owner; `test-fast` is T0-T2, offline, credential-free, and container-free; Make/CI delegate to the canonical runner; `make verify` retains its certified dependency list; cancellation and output policy are present.
- Pass criteria: policy validation passes without selecting integration, browser, Compose, live, or vulnerability work in the fast loop.
- Higher-fidelity fact left elsewhere: actual service, browser, release, and public behavior is covered by TEST-042/043, WEB-TEST-047/048, TEST-055, and TEST-056.

### TEST-042: shared service namespace isolation

- Target: `internal/integrationtest/isolation_test.go` and runner-owned integration services.
- Command: `node scripts/run-test-tier.mjs --task integration-isolation`.
- Assertions: two Postgres database leases and two Garage prefix leases can run concurrently; sentinels cannot be read, overwritten, deleted, or listed across leases; releasing one lease retains the other; injected child failure and interruption cleanup remove only exact resources and the randomized test project.
- Pass criteria: the pinned real Postgres/Garage services pass all sentinels with zero residual databases, prefixes, containers, volumes, or runner directories.
- Higher-fidelity fact left elsewhere: destructive Garage restart persistence is TEST-043; consumer/race execution is TEST-053.

### TEST-043: exclusive Garage lifecycle

- Target: `internal/testkit/test_tier_policy_test.go`, `internal/artifacts/garage_restart_test.go`, and `scripts/run-test-tier.mjs`.
- Command: `go test ./internal/testkit/... -run TestExclusiveGarageResourcePolicy -count=1` plus the manifest task `garage-restart-exclusive`.
- Assertions: exactly one exclusive Garage resource exists; the restart test uses `integration && garageexclusive` and a dedicated service; ordinary consumers use leases and cannot overlap it.
- Pass criteria: restart persistence remains asserted and measured normal/race tasks have zero exclusive overlap and zero cleanup leaks.

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
  - Every fixture names at least one executable semantic test consumer or an ADR-backed intentional difference.
  - Every semantic consumer target exists, carries the canonical specification and `TEST-###` markers, and contains direct fixture-read evidence.
- Pass criteria: script exits zero with counts for every parity fixture class and no unclassified source fixture.
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
- Setup: the current source retry-decision matrix plus rate-limit, server, network, parse, schema, refusal, invalid-request, auth, cancellation, timeout, empty-response, and OpenAI Responses provider-directive errors; fake timer; jitter seed `12001`.
- Assertions:
  - `maxAttempts` is total attempts.
  - Retry categories match source fixtures; `empty_response` requires the coded error identity rather than message wording.
  - A statusless/code-less OpenAI Responses retry directive with a bounded request ID becomes `provider_retry` and repeats the selected target only when that category is explicitly enabled (ADR-HLLM-020).
  - Calculated exponential backoff is capped; valid 429/503 `Retry-After` is a minimum bounded by the caller deadline, not the backoff cap.
  - Cancellation before an attempt or during wait stops immediately.
  - Attempt and wait metadata are exact.
- Pass criteria: all categories and attempt-count tables pass without wall-clock sleeps.
- Expected runtime: 10 seconds.

### TEST-009: structured repair on the selected target

- Target: `internal/runtime/repair_backup_test.go` (renamed to `repair_test.go` by recovery P01.S03).
- Command: `go test ./internal/runtime/... -run TestStructuredRepair -count=1`
- Setup: malformed/schema-invalid output, direct repaired values and transient failures during repair.
- Assertions: repair consumes the shared attempt budget, requests the original schema, preserves target/context and returns the direct validated value; fallback/escalation controls are rejected under ADR-HLLM-020.
- Pass criteria: repair values, identity, budget and cancellation assertions pass without alternate routing.
- Expected runtime: 10 seconds.

### TEST-010: contracted schema validation and parse diagnostics

- Target: `internal/schema/schema_test.go`
- Command: `go test ./internal/schema/... -run TestSchemaContract -count=1`
- Setup: valid/invalid contracted JSON Schema and parser fixtures.
- Assertions:
  - Supported keywords normalize consistently.
  - Unsupported shapes fail closed.
  - Parse diagnostics and schema errors use stable categories and safe excerpts.
  - Raw response length matches the source JavaScript UTF-16 code-unit contract, including non-BMP input, while excerpts remain bounded and redacted.
  - Initial and repair responses return the direct validated value without an envelope or coercion (ADR-HLLM-020).
- Pass criteria: Go output matches the contracted schema and strict parser acceptance controls; intentional source differences are recorded in ADR-HLLM-020.
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

- Target: `internal/providers/requests_test.go`, `internal/gateway/run_validation_test.go`
- Command: `go test ./internal/providers/... -run TestProviderRequestParity -count=1`; `go test ./internal/gateway/... -run 'TestValidateRunInput' -count=1`
- Setup: local HTTP servers and request goldens for OpenAI-compatible Chat, OpenAI Responses, Gemini GenerateContent, Anthropic Messages, and generic OpenAI-compatible endpoints.
- Assertions:
  - Paths, methods, headers, model IDs, prompts, schemas, tools, reasoning options, token limits, and native options match source fixtures.
  - Contracted-only options never leak into native mode.
  - Unknown native options follow the current provider-specific contract.
  - Utility-compatible request option names such as `max_tokens` and
    `max_output_tokens` pass the gateway boundary, while credential-shaped
    option names remain rejected.
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

- Target: `internal/profiles/default_catalog_test.go`,
  `internal/profiles/profile_test.go`,
  `internal/providers/default_profile_catalog_test.go`, and the tagged
  `internal/gateway/profile_seed_test.go`
- Commands:
  - `go test ./internal/profiles/... ./internal/providers/... -run 'Test(DefaultCatalogParity|ProfileParity|DefaultProfileCatalogParity)' -count=1`
  - `go test ./internal/gateway/... -tags=integration -run TestDefaultProfileSeedParity -count=1`
- Setup: current source catalog at utility-llm revision `5c0309e` / `0.15.0`,
  the 28 credential-free preset entries, invalid names/endpoints/defaults,
  removed-control rejection, fixed endpoint resolver, and isolated owner-scoped Postgres.
- Assertions:
  - The embedded seed contains exactly the current 28 profile names and
    matches provider, API inference type, base URL, model ID, pricing,
    reasoning, defaults, and structured-output capability.
  - Seed rows contain no credentials or runtime discovery state, OpenRouter
    pricing remains provider-reported, and catalog serialization round-trips.
  - Every seeded profile prepares both text and structured operations through
    the shared endpoint policy without serializing the fixture credential.
  - Concurrent first use inserts every missing preset for an owner with an
    existing custom row, exposes seeded rows as unconfigured, and never
    overwrites the existing operator profile; an empty owner receives exactly
    the 28 presets.
  - Runtime catalog assembly accepts credential-free seed rows without
    blocking configured profiles; every seeded profile's missing-credential
    boundary returns `ErrCredentialNotConfigured`, while an attempted run
    without the matching endpoint credential returns `credential_required`,
    persists a failed history item, and never dials the provider.
  - Profile shape, API inference types, pricing, model list, defaults, and
    complete recovery policies follow ADR-HLLM-020; independent profile data retain source parity.
  - Backup/escalation fields are rejected; each profile selects exactly one target.
  - No alternate or old recovery-policy shape is accepted.
- Pass criteria: the current 28-profile seed and all-profile deterministic
  preparation matrix pass; invalid fixtures fail with stable fields; the
  tagged seed test passes with isolated Postgres.
- Expected runtime: 10 seconds unit; 90 seconds integration.

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

- Target: `internal/gateway/run_test.go`, `internal/gateway/http_contract_test.go`
- Command: `go test ./internal/gateway -run TestHTTPRunDurationLimit -count=1` and `go test ./internal/gateway/... -tags=integration -run TestRunRoute -count=1`
- Setup: saved profile, fake provider, Postgres cache/trace/artifact-index stores, fake `ArtifactStore`, and injected root `Client` constructor.
- Assertions:
  - Run resolves/decrypts profile state and calls `Client.Call` once.
  - Text and structured output return `Result.Output` plus redacted state.
  - Domain run/trace/history records use the normalized `Result` metadata.
  - Successful artifact references create available owner-scoped Postgres metadata; failed artifact persistence leaves no available row and does not change the provider result.
  - Invalid request or unsafe endpoint fails before provider invocation.
  - The gateway enforces the 60-second contract maximum, rejects a configured or requested increase, permits a shorter deployment/request deadline, returns the documented 504 `run_timeout`, cancels the root call, and never retries the HTTP operation.
  - Deployment bounds are checked at T1 without Postgres. T3 proves exactly one root call is canceled by the real RunService deadline, which begins after profile lookup. The HTTP deadline separately covers profile I/O and must return 504 without retrying whether it expires before the caller starts or during the call. The test must not assume that Postgres completes within the request's 10ms budget.
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

### TEST-057: canonical execution identity and result source

- Target: root client, `internal/runtime/`, providers, traces, and telemetry.
- Command: `go test ./... -run 'Test(ExecutionIdentity|ResultSource|GlobalAttemptBudget)' -count=1`
- Assertions:
  - Selected target and immutable prepared target remain distinct.
  - Provider result source references exactly one successful call-global attempt;
    cache source retains producer identity without a current provider attempt;
    failed/pre-provider calls use none.
  - Initial, retry and repair attempts on the selected target share one global budget and
    sequence; `providerUsed` is set only at the execution boundary.
  - Public result, domain trace, artifact projection, and telemetry derive from
    the same canonical record.
- Expected runtime: 15 seconds.

### TEST-058: canonical accounting and cache v2

- Target: `internal/accounting/`, runtime, providers, cache, pricing, telemetry.
- Command: `go test ./internal/accounting/... ./internal/providers/... ./internal/runtime/... ./... -run 'Test(Accounting|CacheV2)' -count=1`
- Assertions:
  - Five exclusive components derive prompt/completion/total with checked
    arithmetic and explicit completeness.
  - Result and current-provider accounting remain distinct through retries and
    cache hits.
  - Exact, partial, unknown, and unavailable cost preserve known subtotal;
    tiny positive cost never becomes exact zero.
  - Cache v2 retains producer identity/result accounting and cache v1 is not
    accepted after the version cut.
- Expected runtime: 20 seconds.

### TEST-059: execution aggregate, OpenAPI, stats, and mixed versions

- Target: gateway, Postgres, migrations, `api/openapi.yaml`.
- Command: `make test-api && make test-integration`
- Assertions:
  - New execution facts persist once under the run aggregate; history and trace
    APIs agree without a second trace document.
  - Stats use typed execution fields and preserve result/provider accounting,
    usage completeness, and overall/cached cost coverage.
  - Retained v1 documents render only immutable captured facts and explicitly
    mark missing facts; current profiles and telemetry are never consulted.
  - OpenAPI examples satisfy semantic equations, not only JSON shape.
- Expected runtime: integration tier.

### TEST-060: artifact operation journal and crash convergence

- Target: gateway artifact coordinator, Postgres journal, Garage integration.
- Command: `make test-integration`
- Assertions:
  - Publish/delete intent precedes object mutation; identical retries are
    idempotent and integrity conflicts fail closed.
  - New and legacy immediately eligible publication rows survive reconciliation
    until their execution metadata commits; abandoned publications still
    converge after the bounded grace.
  - Ambiguous PUT, partial multi-delete, process-boundary failpoints, competing
    reconcilers, and restart converge; the second reconciliation is a no-op.
  - Shared save/exclusive clear/per-execution delete lock ordering preserves
    concurrency and owner isolation.
  - Only available artifacts presign; unavailable/deleting artifacts never do.
  - A bounded reverse inventory identifies missing available bodies and aged
    unreferenced objects without emitting object keys or deleting data.
- Expected runtime: T3 with real PostgreSQL and Garage.

### TEST-061: current execution contract and structural ownership

- Target: v2 execution reads, retired command removal, run-to-trace ownership.
- Command: targeted command/OpenAPI tests plus `make test-integration`.
- Assertions:
  - History and trace use the canonical RunResult schema, without retained-v1
    alternatives; the retired `reconcile-history` command is rejected.
  - Every trace has one owner/run binding; relational cascade prevents
    independent trace subtrees or writers. Deletion still uses the artifact
    coordinator and journal, including idempotency and crash convergence.
  - Forward-only migrations remain unchanged; new installations reach the
    current schema. The bounded pre-v2 reconciliation tool was removed after
    the explicitly authorized 2026-09-10 data purge, not replaced with a fallback.
- Expected runtime: T0/T1 plus PostgreSQL/Garage T3 certification.

### TEST-062: browser-free branch preview lifecycle

- Canonical specification: `SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001`.
- T0/T1: branch identities are stable and collision-resistant; production/dev
  cleanup boundaries are enforced; current same-repository passing revisions
  alone qualify; changed paths select only affected images; automatic fast,
  baseline, and release selectors cannot launch browsers. Browser assertions
  remain explicit opt-ins (amends TEST-055 selection, not its assertion oracles).
- T5, browser-free: dedicated preview tunnel/router, isolated application/data
  services, healthy exact image identity, public HTTP readiness and initial
  authenticated API checks; feature cleanup leaves dev/production untouched.
- Implementation: `scripts/test/preview_policy_test.mjs`,
  `scripts/preview-policy.mjs`, `scripts/preview-event.mjs`, and
  `scripts/preview-environment.mjs`.
- Operations: `docs/preview-environments.md`. No provider run is a deployment
  prerequisite; real browser certification requires explicit user selection.

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
| P08 | TEST-057 through TEST-061 |

## 16. Completion criteria

The backend program is complete when TEST-001 through TEST-036, TEST-039,
TEST-040, and TEST-057 through TEST-061 pass; TEST-037 and TEST-038 pass when
explicit live certification is required; all backend target test files use the
single `TEST-###` namespace; OpenAPI and router behavior conform; backend-owned
paths have no Firebase or frontend implementation surface; backend gates do not
invoke `frontend/`; Collector fanout is the only Langfuse export path; Garage is
the only Harden-LLM artifact store; Langfuse retains its pinned upstream MinIO
dependency; and the full Compose smoke proves correlated application and
diagnostic behavior.

## 17. Recovery architecture acceptance controls

ADR-HLLM-020 and PLAN-HARDEN-LLM-RECOVERY-001 define the current recovery contract. These cases supplement retained assertions at the same production boundaries.

### TEST-201: Existing specification and static gate

- Type / verifies: static; REQ-211.
- Location: `internal/testkit/static_traceability_test.go`.
- Command: `make test-static`
- Fixtures/data: Canonical catalogs, parity manifest and existing static checks; add only traceability tags where needed.
- Deterministic controls: Existing runner; no new plan linter or fixture framework.
- Pass criteria: Catalog links and existing static assertions pass; no unrecorded parity deviation.
- Expected runtime: Existing local gate; record wall time, without introducing a timing threshold.

### TEST-202: Complete public policy

- Type / verifies: unit; REQ-201, REQ-206.
- Location: `client_test.go`.
- Command: `go test . -run '^TestRecovery' -count=1 -timeout=60s -v`
- Fixtures/data: Add TestRecoveryPolicy: complete defaults, omitted/partial/null policy, unknown categories, duplicate categories, empty retryOn, explicit false, zero delays, maxAttempts 1 and 10, and invalid limits.
- Deterministic controls: Local client/provider fixtures; no credentials or public network.
- Pass criteria: One default constructor and validator serve current callers; explicit values survive; invalid input makes zero provider calls.
- Expected runtime: 60-second package timeout; report observed duration.

### TEST-203: Strict value-preserving structured output

- Type / verifies: unit; REQ-203.
- Location: `internal/schema/schema_test.go`.
- Command: `go test ./internal/schema -run '^TestRecovery' -count=1 -timeout=60s -v`
- Fixtures/data: Add TestRecoveryValues: postal codes, numeric string enums, large integers, decimals, large positive/negative exponents, nested arrays, null, trailing data, fenced JSON, malformed JSON and schema mismatches.
- Deterministic controls: Fixed inline JSON and schemas; existing decoder and validator boundary.
- Pass criteria: Valid JSON values retain their types and precision; invalid syntax/schema fails without coercion or heuristic salvage.
- Expected runtime: 60-second package timeout; report observed duration.

### TEST-204: Original-schema repair across supported protocols

- Type / verifies: unit; REQ-204, REQ-210.
- Location: `client_test.go`.
- Command: `go test . -run '^TestRecovery' -count=1 -timeout=60s -v`
- Fixtures/data: Add TestRecoveryRepairPayload using existing local HTTP/TLS fixtures for chat-completions, responses, gemini-generate-content and anthropic-messages; capture initial/repair requests and direct schema-valid responses.
- Deterministic controls: Test-owned servers, fixed schema/output, no live keys; use the real provider serialization path.
- Pass criteria: Repair preserves the selected target/options, requests the original schema, treats prior output as data and returns the direct validated value without a repair metadata envelope.
- Expected runtime: 60-second package timeout; report observed duration.

### TEST-205: Bounded execution and canonical facts

- Type / verifies: unit; REQ-202, REQ-204, REQ-209, REQ-212.
- Location: `internal/runtime/repair_test.go`.
- Command: `go test ./internal/runtime -run '^TestRecovery' -count=1 -timeout=60s -v`
- Fixtures/data: Rename existing repair_backup_test.go in P01.S03 and add TestRecoveryExecution: initial success; invalid output -> repair 503 -> repair success; repeated invalid repairs; exhausted budget; cancellation; prerequisite failure; cache hit; changed structured projection.
- Deterministic controls: Existing runtime stubs, injected waits/randomness, fixed cache producers and counting dispatchers; no wall-clock sleeps.
- Pass criteria: No more than maxAttempts slots or model invocations; all calls use the selected target; repair identity survives transport retries; records match dispatched work; cache hit invokes neither search nor model; result/provider accounting remain distinct.
- Expected runtime: 60-second package timeout; report observed duration.

### TEST-206: Explicit retry categories and waiting

- Type / verifies: unit; REQ-201, REQ-205.
- Location: `internal/retry/retry_test.go`; `internal/providers/requests_test.go`.
- Command: `go test ./internal/retry ./internal/providers -run '^TestRecovery' -count=1 -timeout=60s -v`
- Fixtures/data: Add TestRecoveryBackoff: every enabled/disabled category, all disabled, zero delays, cap/jitter boundaries, Retry-After on 429/503, oversized numeric delay, signed/malformed/past header, deadline before dispatch and cancellation during wait.
- Deterministic controls: Injected clock, random source and waiter; local header parsing fixtures.
- Pass criteria: Only listed transient categories repeat; valid server delay is never capped below its minimum; no wait or dispatch escapes context cancellation/deadline.
- Expected runtime: 60-second package timeout; report observed duration.

### TEST-207: Current REST and persisted-document contract

- Type / verifies: unit; REQ-201, REQ-206, REQ-208, REQ-209, REQ-210.
- Location: `internal/gateway/run_validation_test.go`; `internal/gateway/openapi_contract_test.go`.
- Command: `go test ./internal/gateway -run '^TestRecoveryContract' -count=1 -timeout=60s -v`
- Administrative boundary: `cmd/harden-llm-gateway/shared_profiles_test.go`; `go test ./cmd/harden-llm-gateway -run '^TestSyncProfilesRejectsOldCatalog' -count=1 -timeout=60s -v` rejects an old catalog before environment/database access.
- Fixtures/data: Add TestRecoveryContractInput and TestRecoveryContractOpenAPI: required policy, profiles response defaults, current profile/state/bundle versions, result v3, old input rejection and examples shared with frontend tests.
- Deterministic controls: Existing validators, strict decoders and OpenAPI example validation; no database for shape permutations.
- Pass criteria: One current wire shape; exact required fields; no recovery aliases, retired routing controls, alternate history result schema or silently accepted old request. Integration handlers remain covered by TEST-208/TEST-211.
- Expected runtime: 60-second package timeout; report observed duration.

### TEST-208: Ordinary migration preserves execution facts and ownership

- Type / verifies: integration; REQ-206, REQ-208, REQ-209, REQ-210.
- Location: `internal/postgres/repository_test.go`; `internal/gateway/resource_routes_test.go`.
- Command: `make test-integration`
- Fixtures/data: Extend existing repository migration cases: migrate a database through version 5, seed two owners' profiles/state/results, credentials and unrelated data, then invoke Store.Migrate; include conflicting/invalid documents, absent fields, false/zero values, old request JSON, null attempts and real concurrent Migrate calls. Existing gateway resource-route cases assert the real profiles defaults response and canonical result read-back.
- Deterministic controls: Existing PostgresLease and integration runner; synthetic data only; assert version 6 with existing migration history checks.
- Pass criteria: Only Section 8 transformations occur; all other values/rows and credential ciphertext remain equal; invalid rows abort the entire migration; repeated/concurrent Migrate is safe; current history/trace contracts accept migrated results.
- Expected runtime: Existing integration task timeout; hosted integration job envelope is 90 minutes, not a new performance target.

### TEST-209: Shared editor and strict frontend boundary

- Type / verifies: unit; REQ-201, REQ-206, REQ-207, REQ-208, REQ-209.
- Location: `frontend/test/harden_llm_web/live/profile_widget_state_test.exs`; `frontend/test/harden_llm_web/live/profile_widget_component_test.exs`; `frontend/test/harden_llm_web/live/profiles_live_test.exs`; `frontend/test/harden_llm_web/live/workspace_live_test.exs`; `frontend/test/harden_llm_web/harden_api_test.exs`; `frontend/test/harden_llm_web/profile_widget_style_test.exs`.
- Command: `(cd frontend && mix test --only recovery --seed 104729)`
- Fixtures/data: Tag new/changed recovery cases with :recovery and register WEB-TEST-071, WEB-TEST-072, WEB-TEST-073. Use backend-validated response examples for new profile, existing profile, edited draft, save/reload/run/cURL, backend errors, current history and rejected old rerun.
- Deterministic controls: Private Req.Test ownership, async cases where supported, test-owned component IDs and element-driven LiveView events; existing clickable help mechanism.
- Pass criteria: Both editors use the same controls/help/styles and serializer; drafts preserve explicit values; no Phoenix semantic defaulting; error paths are visible; help trigger/binding remains present; strict decoder accepts the current contract. This does not certify browser execution or layout.
- Expected runtime: Existing Mix timeouts; within the existing fast-job envelope of 20 minutes.

### TEST-210: Broad deterministic development gate

- Type / verifies: static; REQ-211, REQ-212.
- Location: `internal/testkit/test_tier_policy_test.go`.
- Command: `make test-fast`
- Fixtures/data: Existing Go, static/parity, Phoenix and dependency-free Node tasks including the new cases above.
- Deterministic controls: Pinned tools; existing worker/resource limits; no new runner task, Docker, browser or public provider.
- Pass criteria: Every selected task passes; new focused cases are discovered by normal suites; no weakened assertions or excluded required cases.
- Expected runtime: Existing fast-job envelope of 20 minutes; record actual duration.

### TEST-211: Cross-system browser-free certification

- Type / verifies: integration; REQ-202, REQ-206, REQ-208, REQ-209, REQ-210, REQ-211, REQ-212.
- Location: `internal/testkit/release_gate_test.go`.
- Command: `make test-release`
- Fixtures/data: Existing release tasks with current fixtures, including real storage/API/lifecycle boundaries.
- Deterministic controls: Existing Docker services, lease/resource ownership and release runner; no live-provider or browser path.
- Pass criteria: All selected release tasks pass against the final source/configuration identity; local evidence is distinguished from hosted CI and deployment.
- Expected runtime: Existing release-job envelope of 180 minutes; record actual duration.

## 18. Recovery boundary consolidation

The following cases are the canonical additions for
`PLAN-HLLM-RECOVERY-BOUNDARIES-001`. They extend existing files and retain the
same T0-T5 hierarchy; no new runner or dependency is introduced. Go cases use
the backend specification identifier in their source comments. Frontend cases
are registered in the Phoenix specification with the separate WEB-TEST
namespace.

### TEST-212: Canonical registration and repository policy

- Type / verifies: static; REQ-223.
- Location: `internal/testkit/static_traceability_test.go`.
- Command: `make test-static`.
- Acceptance: TEST-212 through TEST-228 and WEB-TEST-074/076 have one canonical
  definition and source traceability; existing parity provenance and policy
  checks remain intact.

### TEST-213: Failure classification at the provider boundary

- Type / verifies: unit; REQ-213, REQ-215, REQ-224.
- Location: `internal/retry/retry_test.go`, provider normalization, runtime
  repair/telemetry and trace parity tests.
- Command: `go test ./internal/retry ./internal/providers ./internal/runtime ./internal/traces -run '^TestRecoveryBoundaryClassification' -count=1 -timeout=60s -v`.
- Acceptance: schema field names and diagnostic text cannot create refusal or
  network failures; HTTP 400/401/403 are terminal while 429/5xx retain their
  categories and Retry-After; malformed envelopes cannot trigger semantic
  repair; documented provider directives and parent cancellation/deadline keep
  their exact categories and bounded metadata.

### TEST-214: Bounded transport recovery and observed dispatch

- Type / verifies: unit; REQ-214, REQ-215, REQ-224.
- Location: provider request, endpoint-policy and web-search tests plus runtime
  repair/telemetry tests.
- Command: `go test ./internal/providers ./internal/runtime -run '^TestRecoveryBoundaryTransport' -count=1 -timeout=60s -v`.
- Acceptance: preparation/cache hits do no DNS; guarded resolution and transient
  transport failures consume the single attempt budget; security and redirect
  checks remain active; model dispatch is false before WroteHeaders and true
  after it; model/Jina status normalization and partial result facts agree.

### TEST-215: Full jitter, server minimum and parent deadline

- Type / verifies: unit; REQ-215, REQ-222.
- Location: retry, provider request and runtime repair tests.
- Command: `go test ./internal/retry ./internal/providers ./internal/runtime -run '^TestRecoveryBoundaryTiming' -count=1 -timeout=60s -v`.
- Acceptance: integer full-jitter values follow the one capped-window formula,
  remain distinct at the cap, honor valid Retry-After as a lower bound and
  stop before the next dispatch when the parent context is canceled or expired.

### TEST-216: Provider completion precedes output acceptance

- Type / verifies: unit; REQ-213, REQ-216.
- Location: provider normalization and request tests.
- Command: `go test ./internal/providers -run '^TestRecoveryBoundaryCompletion' -count=1 -timeout=60s -v`.
- Acceptance: Responses accepts only a completed terminal response object;
  delta/done-only streams, malformed envelopes, explicit incomplete/limit/
  refusal states and unsupported terminal markers cannot become output or
  repair input. Chat, Gemini and Anthropic require their documented successful
  completion markers and strict output shapes.

### TEST-217: Failed-attempt accounting and cache admission

- Type / verifies: unit; REQ-217, REQ-218, REQ-224.
- Location: provider normalization/request, runtime repair, accounting,
  cache-key and trace parity tests.
- Command: `go test ./internal/providers ./internal/runtime ./internal/accounting ./internal/cachekey ./internal/traces -run '^TestRecoveryBoundaryAccountingCache' -count=1 -timeout=60s -v`.
- Acceptance: Valid failed-attempt usage/cost survives output/completion errors;
  known totals accumulate without upgrading uncertainty; invalid token,
  component or cost data is bounded and terminal; only completed accepted output
  writes projection v3; old projection keys are never read and policy-only cache
  changes preserve semantic identity.

### TEST-218: One active recovery policy across editor contexts

- Type / verifies: unit; REQ-219, REQ-221.
- Location: `frontend/test/harden_llm_web/live/profile_widget_state_test.exs`,
  component, ProfilesLive, WorkspaceLive and EmbeddingLive tests.
- Command: `(cd frontend && mix test --only recovery_boundary_owner --seed 104729)`.
- Acceptance: The host owns one active policy; widget updates are intents,
  selection is one complete snapshot, restoration preserves captured policy,
  and run/profile-save/export/cURL actions read the source defined by the
  frontend contract. Independent widget instances remain isolated.

### TEST-219: One ordered workspace state writer

- Type / verifies: unit; REQ-219, REQ-220.
- Location: `frontend/test/harden_llm_web/live/workspace_live_test.exs`.
- Command: `(cd frontend && mix test --only recovery_boundary_persistence --seed 104729)`.
- Acceptance: Each LiveView has at most one in-flight complete-state write and
  one latest pending snapshot. Success drains only the newest snapshot; errors,
  task exits and auth expiry retain the draft without same-snapshot retries;
  stored read-back and reload equal the last successful visible state.

### TEST-220: Current public and REST contract holdout

- Type / verifies: unit; REQ-215, REQ-221.
- Location: `client_test.go`, `internal/gateway/run_validation_test.go`,
  `internal/gateway/openapi_contract_test.go`.
- Command: `go test . ./internal/gateway -run '^TestRecovery' -count=1 -timeout=60s -v`.
- Acceptance: Current policy presence/ranges, explicit false/zero/empty values,
  original-schema repair and current OpenAPI/storage versions retain their
  existing assertions; retired inputs remain rejected.

### TEST-221: Broad deterministic development gate

- Type / verifies: static; REQ-223.
- Location: `internal/testkit/test_tier_policy_test.go`.
- Command: `make test-fast`.
- Acceptance: Every manifest-selected offline Go, parity, Phoenix and Node task
  passes with the new focused cases discovered; no required assertion or task
  is weakened or excluded.

### TEST-222: Final cross-system certification

- Type / verifies: integration; REQ-214, REQ-215, REQ-217, REQ-218, REQ-220,
  REQ-221, REQ-223, REQ-224.
- Location: `internal/testkit/release_gate_test.go`.
- Command: `make test-release`.
- Acceptance: Existing browser-free release composition passes against the
  final source/configuration identity, including real storage, API lifecycle,
  concurrency/race and deterministic frontend boundaries. Browser and live
  provider tasks are not part of this case.

## 19. Recovery integrity follow-up

These cases implement `PLAN-HLLM-RECOVERY-BOUNDARIES-002` inside the existing
provider, runtime, accounting, cache, persistence and contract owners. They
reuse the repository's T0-T3 hierarchy and service-pool runner. They do not
add a retry service, provider fallback, compatibility reader or browser path.
Every new Go test file or test group carries this specification ID and its
canonical `TEST-###` comment; the Phoenix cases use the separate
`SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001` and `WEB-TEST-076`.

### TEST-223: Recovery transport precedence

- Type / verifies: unit; REQ-213, REQ-214, REQ-224.
- Location: `internal/providers/requests_test.go`, `internal/providers/normalization_test.go`, `internal/providers/web_search_test.go` and focused runtime transport tests.
- Command: `go test ./internal/providers ./internal/runtime -run '^TestRecoveryIntegrityTransport' -count=1 -timeout=60s -v`.
- Fixtures/data: Per-test local HTTP handlers for model and Jina redirects, 429/503 responses, oversized bodies, simultaneous body bytes/read errors and permanent/transient DNS/TLS/EOF failures. Use owned request counters, cancellation channels and the existing endpoint-policy/TLS helpers.
- Deterministic controls: `MaxAttempts=2`, zero backoff and injected no-sleep wait for retry cases; no arbitrary sleeps or public network.
- Pass criteria: Redirects and endpoint/size policy failures are terminal after one request; size wins over status/read diagnostics; status and valid `Retry-After` survive an interrupted body; permanent transport failures stop and documented transient failures retain policy categories; model dispatch is reported only at the existing transport event and Jina never sets it.
- Expected runtime: less than 60 seconds.

### TEST-224: Lossless cache admission

- Type / verifies: unit; REQ-217, REQ-218, REQ-221.
- Location: `client_cache_test.go`, `internal/runtime/repair_test.go`, `internal/runtime/search_test.go` (or the existing runtime search test file) and cache-key tests.
- Command: `go test . ./internal/runtime ./internal/cachekey -run '^TestRecoveryIntegrityCacheAdmission' -count=1 -timeout=60s -v`.
- Fixtures/data: Valid response-projection `v3` records mutated in memory for whitespace/null output, original-schema violations, a JSON integer `9007199254740993`, precise decimal `0.12345678901234567890123456789`, invalid accounting/search metadata, trailing JSON, producer mismatch and profile aliases.
- Deterministic controls: `json.Decoder.UseNumber`, an EOF check and process-owned provider/search counters. No conversion to float or cache-as-miss fallback.
- Pass criteria: Exact JSON values and types survive write/read; valid hits preserve producer attribution and perform zero DNS/search/model work; malformed identity, projection, accounting, search or schema data returns bounded `CACHE_INTEGRITY` with no repair, retry or write; equivalent profile aliases reuse a valid entry.
- Expected runtime: less than 60 seconds.

### TEST-225: Nested timeout ownership

- Type / verifies: unit; REQ-214, REQ-215, REQ-222.
- Location: `internal/providers/requests_test.go`, `internal/providers/web_search_test.go` and runtime timeout tests.
- Command: `go test ./internal/providers ./internal/runtime -run '^TestRecoveryIntegrityTimeout' -count=1 -timeout=60s -v`.
- Fixtures/data: A real Router preparation path with a local Jina transport that waits for its request context, a shorter attempt timeout, a live overall parent, explicit parent cancellation/deadline, Jina-local timeout, search memoization and disabled retry controls.
- Deterministic controls: Two-attempt policy with zero backoff; channel synchronization for request entry and cancellation; test deadlines bound only stuck tests.
- Pass criteria: An attempt-local or Jina-local timeout while the parent is live is network and may consume the second slot; parent cancellation/deadline stops immediately; search succeeds once per logical call; disabled retry and `maxAttempts=1` remain single-attempt controls; search never marks model dispatch.
- Expected runtime: less than 60 seconds.

### TEST-226: Provider accounting coverage

- Type / verifies: unit; REQ-217, REQ-218, REQ-224.
- Location: `internal/accounting/accounting_test.go`, `internal/providers/normalization_test.go`, `internal/providers/requests_test.go` and `internal/runtime/repair_test.go`.
- Command: `go test ./internal/providers ./internal/runtime ./internal/accounting -run '^TestRecoveryIntegrityAccounting' -count=1 -timeout=60s -v`.
- Fixtures/data: Sequences of dispatched/unmeasured, measured `CompleteUsage(2,0,0,1,0)` plus `ExactCost(0.01,"reported")`, pre-dispatch failures, measured zero, independent usage/cost availability, complete error JSON, interrupted complete JSON, truncated JSON, invalid components and checked-addition overflow.
- Deterministic controls: A single `ProviderAccumulator` per call; independently asserted usage/cost fields and known/unknown observation counts; local provider bodies only.
- Pass criteria: Unknown dispatched work remains uncertain; known subtotals are retained; observation order is commutative; measured zero is known; invalid accounting is terminal while independent valid dimensions remain; complete facts in rejected/interrupted responses survive; truncated data is not guessed; accepted result accounting remains separate from provider totals; cache hits add no provider observation.
- Expected runtime: less than 60 seconds.

### TEST-227: Forward cache migration and persistence

- Type / verifies: integration; REQ-217, REQ-218, REQ-221, REQ-223.
- Location: `internal/postgres/cache_test.go`, `internal/postgres/repository_test.go`, new migration test coverage and `internal/gateway/run_test.go`.
- Command: `make test-integration`.
- Fixtures/data: Runner-owned `PostgresLease`; historical schema version 0006 rows seeded with SQL in a test-only fixture, two owners, retained result JSON with exact large-number/decimal values, upsert timestamps, owner isolation and concurrent migration/read/write cases.
- Deterministic controls: Existing service-pool runner and lease cleanup; real `Store.Migrate`; no application database, per-test Compose fallback or manually executed migration.
- Pass criteria: Migration 0007 drops only `operation`, `provider_envelope`, `usage` and `cost`; six retained cache columns, keys, timestamps and rows remain byte/value equivalent; readiness and idempotency pass; current root client replays the migrated row with zero provider work; owner isolation/upsert/concurrency and the pre-existing rollback/locking assertions remain intact. The report contains executed `TestRecoveryIntegrityStorage*` cases.
- Expected runtime: within the existing integration task envelope.

### TEST-228: Cache-write failure success boundary

- Type / verifies: unit and integration; REQ-217, REQ-218, REQ-220, REQ-221, REQ-223.
- Location: `client_test.go`, `internal/runtime/telemetry_test.go`, `internal/runtime/repair_test.go`, `internal/traces/parity_test.go`, `internal/gateway/run_test.go` and shared API contract tests.
- Command: Cheap cases: `go test . ./internal/runtime ./internal/gateway ./internal/traces -run '^TestRecoveryIntegrityCacheWrite' -count=1 -timeout=60s -v`. Stored-run case: the same `make test-integration` execution as TEST-227.
- Fixtures/data: A process-local cache store whose `Set` returns a sentinel error after accepted output; cache miss, refresh, cache-off, hit, read/integrity/provider failure and accepted-output deadline cases; root traces, history/read-back, OTel span/metric exporter and real RunService/Postgres/local-provider fixtures.
- Deterministic controls: Exactly one provider request and one attempted write for accepted miss/refresh; no write for rejected output or read/integrity failure; no raw sentinel text in public errors, labels or UI.
- Pass criteria: Accepted inference returns success with output, both ledgers and result source, `write_failed` and `Written=false`; it is not retried or redispatched. Reads/integrity failures remain errors. Trace lookup/write observations, write-span error, bounded telemetry, REST/history payloads and Phoenix projection agree. The integration report contains `TestRecoveryIntegrityCacheWriteStoredRun`.
- Expected runtime: cheap cases less than 60 seconds; integration within the existing task envelope.

## 21. Reusable numbered pagination

These cases cover the reusable numbered-pagination contract introduced for the
workspace History list. Cursor pagination remains a supported legacy REST
mode and is tested separately; it is not silently converted to numbered mode.
The frontend cases are registered in the separate Phoenix specification.

### TEST-229: Numbered history request and arithmetic contract

- Type / verifies: unit and HTTP contract; REQ-207, REQ-221, REQ-223.
- Location: `internal/gateway/http_contract_test.go`, `internal/gateway/httpapi/resources.go`, `internal/gateway/resources.go`, and `internal/postgres/resources.go`.
- Command: `go test ./internal/gateway -run 'TestHTTPContract|TestOpenAPIContract|TestRecoveryContractOpenAPI' -count=1`.
- Assertions: positive signed-64-bit pages, limits from 1 through 100, mixed page/cursor rejection including an empty cursor, overflow/zero/empty rejection, one-based empty/exact-multiple arithmetic, above-range clamping, and unchanged cursor responses.
- Pass criteria: invalid input has the existing `400 invalid_request` envelope; valid numbered requests select the numbered envelope and never expose cursor metadata; legacy requests retain their original shape and default behavior.

### TEST-230: PostgreSQL numbered reads and snapshot ownership

- Type / verifies: integration; REQ-207, REQ-208, REQ-220, REQ-223.
- Location: `internal/postgres/pagination_integration_test.go` and `internal/gateway/resource_routes_test.go`.
- Command: `make test-integration` through the canonical service-pool runner.
- Fixtures/data: isolated real PostgreSQL, owner-scoped histories with identical timestamps, unseen middle/last pages, an owner with no rows, concurrent insert/delete mutations and a canceled read.
- Assertions: `COUNT(*)` and ordered page rows use one owner predicate and one `REPEATABLE READ READ ONLY` transaction; `started_at DESC, run_id DESC` is stable; effective-page clamping, owner isolation, cancellation and pool cleanup are observable; a concurrent writer cannot produce a count/cardinality mismatch.
- Pass criteria: real HTTP/API and direct store cases pass without a preceding cursor walk, cross-owner rows, leaked connections or a fake database boundary.

### TEST-231: OpenAPI and strict numbered wire shapes

- Type / verifies: unit and API client boundary; REQ-207, REQ-221.
- Location: `api/openapi.yaml`, `internal/gateway/openapi_contract_test.go`, `frontend/lib/harden_llm/llm_diagnostics_wire.ex`, `frontend/lib/harden_llm_web/harden_api.ex`, and `frontend/test/harden_llm_web/harden_api_test.exs`.
- Command: `go test ./internal/gateway -run 'TestOpenAPIContract|TestRecoveryContractOpenAPI' -count=1` plus `(cd frontend && mix test test/harden_llm_web/harden_api_test.exs)` with the pinned Elixir/OTP PATH.
- Assertions: legacy and numbered result alternatives are exact and disjoint; mode-specific requests require the matching response; pagination bounds/cardinality and canonical HistoryItem/RunResult data are strict; malformed numbered metadata cannot fall back to cursor traversal.
- Pass criteria: published examples and deterministic backend-validated fixtures decode successfully, while missing, extra, malformed or legacy-only numbered responses fail closed.

### TEST-232: Bounded numbered-pagination measurement

- Type / verifies: integration measurement; REQ-207, REQ-208, REQ-223.
- Location: `internal/postgres/pagination_integration_test.go`; ignored evidence is written to `plans/evidence/harden-llm/reusable-pagination-test-232.json`.
- Command: `make test-integration` through the canonical service-pool runner.
- Fixtures/data: real PostgreSQL owner datasets of 1,000, 10,000 and 100,000 rows; page sizes 10, 25, 50 and 100; first, middle and last pages; response byte counts; `EXPLAIN (ANALYZE, BUFFERS)` for count and page queries.
- Assertions: the report records source SHA, host/toolchain/PostgreSQL version, first-call and three warm-call microsecond timings, plans, buffer summaries, applied metadata and response sizes. It records that pooled execution could not provide a true shared-buffer eviction/cold-cache run rather than labeling the first call cold.
- Pass criteria: all requested cardinalities and positions pass exact count/cardinality assertions, the existing owner-history index is used for page reads, the snapshot/concurrency and cancellation checks pass, and no latency threshold is invented from this single host observation.

## 22. Production configuration reproducibility

These cases cover the reusable production configuration boundary introduced by
`PLAN-HLLM-PRODUCTION-CONFIG-001`. The command resolves approved dotenv files
and explicit host metadata without sourcing them as shell code or recovering
missing values from running containers. The native Compose case uses temporary
synthetic files and `config` only; it does not start containers, pull images,
or contact a provider.

### TEST-233: Approved source ownership and resolution policy

- Type / verifies: pure Node policy; REQ-019 and the source-ownership controls in `PLAN-HLLM-PRODUCTION-CONFIG-001`.
- Location: `scripts/production-config.mjs` and `scripts/test/production_config_test.mjs`.
- Command: `make test-static` through the canonical `go-static` task.
- Fixtures/data: mode-0600 synthetic observability, production and shared-application files containing literal dollar signs, intentional empty values, duplicate/malformed assignments, and ambient application variables.
- Assertions: only the six PRLS inputs are owned by the observability source; production wins permitted overlap; PRLS conflicts and invalid sources fail; shared application variables preserve intentional empties; arbitrary ambient application and Compose overrides do not enter the child environment; descriptor metadata rejects credential-shaped values.
- Pass criteria: all values remain in process memory or the approved child environment, no supplied synthetic secret appears in diagnostics, and existing `TEST-062` preview/profile behavior remains unchanged.

### TEST-234: Semantic comparison and scoped no-op/application policy

- Type / verifies: pure Node comparison and recorded subprocess boundary; REQ-017 and REQ-019.
- Location: `scripts/production-config.mjs` and `scripts/test/production_config_test.mjs`.
- Command: `make test-static` through the canonical `go-static` task.
- Fixtures/data: synthetic Compose models and runtime inspection objects with image IDs, release identity, command, health, network, mount path/content and environment fields.
- Assertions: Compose serialization normalization does not hide real drift; release identity may be explicit and service-specific; equivalent configuration never invokes `up`; unapproved differences block selected application; command arguments and diagnostics contain no resolved values.
- Pass criteria: runtime comparison is scoped to selected descriptor services, affected fields are named without values, and the same source snapshot is required immediately before application.

### TEST-235: Native Compose interpolation and serialization boundary

- Type / verifies: focused native Docker Compose CLI conformance; REQ-019.
- Location: `scripts/test/production_config_compose_test.mjs` and `scripts/production-config.mjs`.
- Command: `make test-production-config`.
- Fixtures/data: temporary four-file Compose graph, two ordered env files, single-quoted bcrypt-like dollar text, JSON, an intentional empty, a precedence collision and a defaulted variable.
- Assertions: native `docker compose config --format json` resolves the approved file order; production precedence wins the permitted collision; empty/default semantics remain distinct; serialized dollar escaping is normalized before comparison; no daemon operation is issued.
- Pass criteria: the test passes with the installed Compose version and cannot pass by replacing native interpolation with a hand-written parser or by starting a container.

## 23. Recovery production closeout

These cases close the recovery stream, inherited-policy presentation, and
two-application release evidence gaps recorded in
`PLAN-HLLM-RECOVERY-CLOSEOUT-001`. They extend existing lanes and do not add a
runner, provider, database, browser, or timeout budget.

### TEST-260: Candidate identity is independent of descriptor consistency

- Type / verifies: unit; REQ-331, REQ-339, REQ-340.
- Location: `scripts/production-config.mjs` and `scripts/test/production_config_test.mjs`.
- Command: `node --test scripts/test/production_config_test.mjs`.
- Acceptance: an explicit 40-hex candidate rejects stale desired/runtime identity, missing labels, unhealthy candidates, invalid scope, and resolve-only use; a valid old runtime may transition to matching candidate images; no mutating subprocess runs on rejection and no diagnostic contains fixture secrets.

### TEST-261: Pending and later outcomes each terminate once

- Type / verifies: unit; REQ-332, REQ-334, REQ-335, REQ-340.
- Location: `internal/gateway/httpapi/sse_test.go`.
- Command: `go test ./internal/gateway/httpapi -run '^TestSSE' -count=1 -timeout=60s`.
- Acceptance: a pre-consumed completion and a later completion each produce exactly one terminal envelope, preserve IDs/result/error data, drain bounded progress before terminal output, and return without waiting for another outcome.

### TEST-262: Deadline and cancellation release the handler

- Type / verifies: unit; REQ-333, REQ-334, REQ-335, REQ-340.
- Location: `internal/gateway/httpapi/sse_test.go`.
- Command: `go test ./internal/gateway/httpapi -run '^TestSSE' -count=1 -timeout=60s`.
- Acceptance: pre-admission expiry retains JSON 504, post-admission expiry emits one `run_timeout` failure, client cancellation/write failure cancels execution, and cooperative workers are joined during bounded cleanup without fabricated accounting.

### TEST-263: Channel and writer ownership survive termination

- Type / verifies: unit; REQ-332, REQ-333, REQ-335, REQ-340.
- Location: `internal/gateway/httpapi/sse_test.go`.
- Command: `go test ./internal/gateway/httpapi -run '^TestSSE' -count=1 -timeout=60s`.
- Acceptance: producer-owned channels have one close owner, late buffered results do not panic or block, no write occurs after handler completion, and all cooperative fixture workers exit.

### TEST-267: Actual RunService preserves the SSE and persistence contract

- Type / verifies: integration; REQ-332, REQ-333, REQ-334, REQ-335.
- Location: `internal/gateway/run_test.go`.
- Command: `make test-integration`.
- Acceptance: authenticated progress, one terminal event, single execution, persistence, owner isolation, resume rejection, and cancellation behavior remain intact with bounded request contexts and closed response bodies.

### TEST-268: Closeout verification remains discoverable and bounded

- Type / verifies: static; REQ-335, REQ-340.
- Location: `scripts/verify-test-tiers.mjs`.
- Command: `node scripts/verify-test-tiers.mjs`.
- Acceptance: TEST-260 through TEST-268 are registered exactly once in their existing lanes, TEST-269 and TEST-270 are documented operational exceptions, frontend companion IDs are present, and no fast/release/browser policy or timeout changed.

### TEST-269: Both running applications match the intended candidate

- Type / verifies: explicit operator acceptance; REQ-331, REQ-339, REQ-340.
- Location: `scripts/production-config.mjs` CLI.
- Command: `node scripts/production-config.mjs check --descriptor /home/kirill/.config/harden-llm/production.json --services harden-llm-gateway,harden-llm-web --expected-release "$HLLM_RELEASE_SHA"`.
- Acceptance: descriptor, desired image, OCI version, release environment, running image identity, and health match the same candidate for both application services; this is read-only and does not claim browser or provider success.

### TEST-270: Complete candidate passes the browser-free release graph

- Type / verifies: integration; REQ-332 through REQ-340.
- Location: `test/test-tiers.json` and the existing release runner.
- Command: `make test-release`.
- Acceptance: every existing browser-free release task passes with zero failed tasks or cleanup errors and the unchanged manifest budgets; browser and paid-provider tasks remain opt-in.

## 24. Resource lifecycle and measured capacity

These deterministic and opt-in tests implement the test-resource and capacity
requirements REQ-341 through REQ-352 in the backend implementation plan.
Lifecycle receipts are test-only; no REST schema or production database is
added. The test runner owns disposable Docker resources. Capacity tests use
synthetic credentials, isolated stores, and a local scripted provider.

### TEST-271: Ownership precedes mutation

- Type / verifies: unit; REQ-341, REQ-345.
- Location: `scripts/test/test_resource_lifecycle_test.mjs`.
- Command: `node --test --test-name-pattern=TEST-271 scripts/test/test_resource_lifecycle_test.mjs`.
- Fixtures/data: Fake Docker command recorder, private temporary ledger, invalid receipt, and atomic-write failure.
- Deterministic controls: Injected clock and run IDs; no daemon/socket; 5-second per-case deadline.
- Pass criteria: No create before valid durable registration; invalid registration prevents dispatch; private records survive scratch removal; secrets never appear.
- Expected runtime: 10 seconds.

### TEST-272: Parent teardown controls acceptance

- Type / verifies: unit; REQ-342, REQ-345.
- Location: `scripts/test/test_resource_lifecycle_test.mjs`.
- Command: `node --test --test-name-pattern=TEST-272 scripts/test/test_resource_lifecycle_test.mjs`.
- Fixtures/data: Fake child/process-group controller with partial creation, TERM, crash, hung inventory, and foreign-attachment cases.
- Deterministic controls: Injected monotonic clock and fake Docker; 5-second per-case deadline; bounded output.
- Pass criteria: Preserve the first failure; reap owned child before deletion; unknown inventory, failed exact removal, foreign attachment, non-empty final inventory, or receipt persistence failure fail acceptance; delete exact owned IDs only; retain pending evidence. A failed best-effort Compose `down` is reported in `cleanupWarnings` and is non-fatal only when exact fallback cleanup completes, final inventory is empty, and the receipt is durably `cleaned`.
- Expected runtime: 15 seconds.

### TEST-273: Daemon guard and dead-owner recovery

- Type / verifies: unit; REQ-343, REQ-344.
- Location: `scripts/test/test_resource_lifecycle_test.mjs`.
- Command: `node --test --test-name-pattern=TEST-273 scripts/test/test_resource_lifecycle_test.mjs`.
- Fixtures/data: Two local processes and temporary flock directory; synthetic daemon, boot, active/dead/reused PID identities; corrupt receipts, remote endpoints, pure tasks, bounded lock wait, and nested lease reuse.
- Deterministic controls: No Docker; ready/release IPC instead of sleeps; 5-second subprocess deadline.
- Pass criteria: Same-daemon invocations exclude each other; other daemon identities do not collide; dead proof is required; active/corrupt/sentinel records survive; remote Docker is rejected before mutation; lock wait is bounded; pure tasks avoid Docker; nested runs reuse the lease without deadlock.
- Expected runtime: Up to 40 seconds on the reference host.

### TEST-274: Actual disposable Docker cancellation boundary

- Type / verifies: integration; REQ-341, REQ-342, REQ-343, REQ-345.
- Location: `scripts/test/test_resource_lifecycle_docker_test.mjs`.
- Command: `node scripts/run-test-tier.mjs --task test-resource-lifecycle-docker`.
- Fixtures/data: Tiny task-owned container/network/volume using the cached `alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc` image with pulls disabled, plus an independently receipted sentinel Compose project.
- Deterministic controls: One inherited local-daemon guard, synthetic run/project IDs, controlled IPC, 300-second test-task deadline. After a stopped/failed task, the existing shared 120-second cleanup tail is bounded recovery work; it never re-runs or extends the test.
- Pass criteria: Success, partial create, child SIGTERM, and child-supervisor SIGKILL followed by next-owner recovery leave no lifecycle-test project resources; sentinel resources survive each target recovery; receipts account for outcomes and finish cleaned.
- Expected runtime: At most the 300-second task deadline plus only the existing bounded termination/cleanup tail when failure requires reconciliation.

### TEST-275: Resource units and attribution

- Type / verifies: unit; REQ-346.
- Location: `scripts/test/test_resource_measurement_test.mjs`.
- Command: `node --test scripts/test/test_resource_measurement_test.mjs`.
- Fixtures/data: Numeric Docker API records; B/kB/MB/GB and KiB/MiB/GiB text; missing/malformed units; CPU; duplicate/project labels; host memory and Docker data-root headroom thresholds.
- Deterministic controls: Frozen inputs, no daemon, integer-byte expected values, seed 104729.
- Pass criteria: Convert recognized units correctly; unknown units are errors/unknown, never zero; project attribution is exact; Docker memory, RSS, sampled peak, host memory, and Docker data-root disk remain distinct; fractional CPU percentages aggregate as finite percentage values rather than integer bytes; safety thresholds fail closed. Missing or malformed Docker memory/CPU values preserve a precise bounded parser reason; RSS and volume-used-byte metrics explicitly state when they are not collected. Unavailable exact container measurements identify the bounded failing stage, exit status, timeout flag, and at most 256 characters of caller-redacted stderr; raw Docker output is not retained.
- Expected runtime: 5 seconds.

### TEST-276: Bounded load generation and streaming accounting

- Type / verifies: unit; REQ-347, REQ-348, REQ-349.
- Location: `internal/capacity/driver_test.go`.
- Command: `go test ./internal/capacity -run '^TestCapacityDriver' -count=1`.
- Fixtures/data: Fake clock/transport; scripted delay, 429, invalid JSON, recovery, cache hit, terminal stream success, and truncated EOF.
- Deterministic controls: Seed 104729; finite queues; no Docker/internet; test contexts at most 5 seconds.
- Pass criteria: Open-loop arrivals remain independent; accounting reconciles; concurrency/request bounds hold; EOF without terminal success fails; recovery dispatch equals script; production endpoints reject before dialing.
- Expected runtime: 10 seconds.

### TEST-277: Real application capacity boundary

- Type / verifies: perf; REQ-347, REQ-348, REQ-349.
- Location: `cmd/harden-llm-gateway/capacity_test.go`.
- Command: `node scripts/run-test-tier.mjs --task capacity-baseline --output tmp/test-feedback/capacity-baseline.json`.
- Fixtures/data: Real command server assembly, REST/auth/client, disposable Postgres/Garage, local TLS scripted provider and export sink. Bootstrap the configured static-token owner in the fresh Postgres lease through the existing `bootstrap-user` command path before starting the gateway; do not bypass auth or insert a partial user. Keep provider/export dependencies alive until the gateway has completed shutdown. The existing separately-owned Compose smoke remains its own boundary; TEST-277 does not start another application stack.
- Deterministic controls: Explicit `integration,capacity` tags; seed 104729; synthetic credentials; Section 6 bounds; no live/browser selector. Capacity runs are explicit-only through workflow dispatch and accept only `correctness`, `exploration`, or `holdout`.
- Pass criteria: Static-token profile setup succeeds only for the bootstrapped owner; persisted history/artifacts agree with terminal outcomes; provider receive counts match runtime attempts; SSE terminal oracle holds; report is bounded and owned fixtures are cleaned; gateway telemetry flush completes before its local export sink stops.
- Expected runtime: Correctness up to 5 minutes; exploration up to 15 minutes; holdout up to 5 minutes, within the registered 40-minute task deadline.

### TEST-278: Comparable cost and decision reports

- Type / verifies: unit; REQ-346, REQ-350, REQ-351.
- Location: `internal/capacity/report_test.go`.
- Command: `go test ./internal/capacity -run '^TestCapacityReport' -count=1`.
- Fixtures/data: Byte/token/price fixtures, mismatched fingerprints, unknown CPA actual price, missing SLO, host resource and exporter-drop records, short/large latency populations, stream event/byte counts, a maximum 2,000-request scenario, and a maximum four-case artifact-report population.
- Deterministic controls: Fixed decimal inputs and seed 104729; no external price lookup or real provider.
- Pass criteria: Denominators/unit math are exact; incomparable samples reject; unknown price/metrics remain null with reasons; p99 is absent below 1,000 samples; offered/launched/succeeded rates and SSE event/byte growth are summarized; stage/model token counts are aggregated; at most 96 failure/outlier request diagnostics retain trace provenance for first-event latency, full latency, launch lag, stream bytes, and event counts plus omitted-count evidence; per-scenario artifact metadata is sampled at most 96 with exact artifact-count/byte aggregates and omitted-count evidence; no unbounded raw request or artifact arrays; maximum supported reports publish privately as `harden-llm-capacity.v2` within 1 MiB; no unsupported savings/SLO claims; only the four specified dispositions occur.
- Expected runtime: 5 seconds.

### TEST-279: Task policy and registration integrity

- Type / verifies: static; REQ-344, REQ-352.
- Location: `scripts/verify-test-tiers.mjs`.
- Command: `node scripts/verify-test-tiers.mjs`.
- Fixtures/data: Current Makefile, task manifest, canonical catalog, traceability and workflow source; existing timeout baseline.
- Deterministic controls: Offline source reads; no Docker; existing tier/budget policy.
- Pass criteria: Executable test registration is discoverable; cheap task is offline/container-free; Make and manifest do not recurse; capacity is opt-in; release is browser-free; bounded private per-run reports are written by default and fast/integration/release jobs upload them with `always()`; candidate identity policy is present.
- Expected runtime: 5 seconds.

### TEST-280: Cross-language receipt contract

- Type / verifies: unit; REQ-341, REQ-345.
- Location: `internal/integrationtest/resource_receipt_test.go`.
- Command: `go test ./internal/integrationtest -run '^TestResourceReceipt' -count=1`.
- Fixtures/data: Temporary private receipt path, shared JSON vectors, invalid run/project/daemon/permissions, atomic-write failures.
- Deterministic controls: Untagged standard-library-only test/helper; no Docker; fixed IDs/clock; 5-second test deadline.
- Pass criteria: Go and Node fields/transitions agree; invalid ownership prevents fixture dispatch; receipts contain no credentials; writes are private and atomic.
- Expected runtime: 5 seconds.

### TEST-281: Cleanup deadline scoping

- Type / verifies: unit; REQ-342, REQ-345.
- Location: `scripts/test/run_test_tier_test.mjs`.
- Command: `node --test --test-name-pattern='TEST-281 cleanup budgets' scripts/test/run_test_tier_test.mjs`.
- Fixtures/data: Synthetic invocation/task cleanup state and monotonic timestamps; no Docker or child process.
- Deterministic controls: Injected clock values; existing per-task and invocation cancellation budgets; no sleeps or environmental timing dependency.
- Pass criteria: A successful task's cleanup does not age later tasks' cleanup allowance; setup/final cleanup for a task share one bounded deadline; first failure or external cancellation establishes one bounded deadline for remaining cleanup.
- Expected runtime: Under 1 second.

### TEST-282: Capacity history cursor pagination

- Type / verifies: unit; REQ-349.
- Location: `cmd/harden-llm-gateway/capacity_history_test.go`.
- Command: `go test ./cmd/harden-llm-gateway -run '^TestCapacityHistoryPagination' -count=1`.
- Fixtures/data: Local HTTP history pages with an opaque cursor, expected run/trace pairs on separate pages, and repeated-cursor input.
- Deterministic controls: `httptest` only; no gateway process, Docker, database, credentials, or timing sleeps.
- Pass criteria: Verification follows the existing limit-100 REST cursor contract until all expected pairs are found; URL-encodes opaque cursors, rejects repeated/oversized cursors and oversized pages, and reports a missing pair only after the final page or bounded page limit.
- Expected runtime: Under 1 second.

### TEST-283: Retired private gateway-image publication contract

- Status: Retired 2026-09-22 by ADR-HLLM-028; not registered in any active test tier or release gate. The identifier is retained and must not be reused.
- Historical verification: REQ-353, the manual private-GHCR publisher contract implemented at source commit `6887fcd8146961dc64598dd7a236e7a9fc522c9c`.
- Historical source: `docs/archive/harden-llm-ghcr-publisher-reference-6887fcd.tar.gz`, containing the workflow, publisher contract test, and exact Dockerfile from that commit.
- Retirement rationale: active production uses an exact-SHA local image on one existing Docker host; there is no current distribution need justifying publisher-specific workflow permissions and tests. Current build/deployment acceptance is defined by `SPEC-HLLM-IMAGE-DEPLOYMENT-001` and KER-IBD-001 through KER-IBD-010.
- Restoration: review the archived source against the current release, security, retention, and restore requirements; do not extract/run it automatically. Reusing TEST-283's source requires reactivating an explicitly approved publication design.
