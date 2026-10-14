# Harden-LLM Backend and REST API Test Specification

## 1. Title and metadata

- Project name: `harden-llm`
- Target repository: `/home/kirill/harden-llm`
- Contract source repository: `/home/kirill/utility-llm`
- Version: `1.5.0-openai-proxy-reference`
- Owners: package maintainers and self-hosted runtime implementers
- Date: 2026-10-06
- Document ID: `SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001`
- Related stack specification: `plans/from_utility-llm/self-hosted-go-stack-spec.md`
- Summary: This document is the canonical backend test catalog. Its active 2026-10-12 amendment follows ADR-HLLM-033 and PLAN-HLLM-REFERENCE-DECOUPLING-002 v5.3: stateless core, stateful reference library, one API-key scope and create-on-save snapshots. The 2026-10-06 transition remains historical. Earlier catalog entries remain historical where they describe removed profiles, per-login product data, custom run routes, or proxy-owned history. Phoenix tests use the separate `WEB-TEST-###` namespace; the current proxy/reference cases are TEST-400–410 and WEB-TEST-114–116.

> **Current contract (2026-10-12):** Section 26 and ADR-HLLM-033/034 supersede conflicting historical ownership/route declarations. Direct `/v1` is ordinary OpenAI inference; reference `/api/v1` uses the same bearer and `api_key_hash`. Only reference runs enter PostgreSQL history. Execution status is in the reference-decoupling evidence ledger, not the old implementation-status file.
- Resource-efficiency addendum date: `2026-09-21`; plan: `plans/production-scale-efficiency-plan.md`.

## 2. Test strategy

- Test execution occurs in `/home/kirill/harden-llm` unless a command explicitly names the source repository.
- `/home/kirill/utility-llm` is read only during fixture capture and JS contract verification.
- P00 creates `go.mod`, the test harness, fixture verification scripts, `api/openapi.yaml`, and canonical `Makefile` targets before later phases use those commands.
- Every behavior slice starts with failing target-repository coverage and ends with the same command passing.
- JS-to-Go parity is tested in the phase that ports each behavior. Final parity is an aggregate gate, not the first parity check.
- Provider tests use local `httptest` servers. Public internet and provider credentials are allowed only under the `live` build tag.
- Postgres and Garage tests use isolated projects and never reuse developer data.
- Compose tests use pinned Harden-LLM images and explicit loopback-bound test Caddy; production HLLM services publish no host ports.
- The Compose smoke uses a test-only OTLP sink named `laminar` for HLLM traces. It is absent from production Compose.
- Garage is the sole Harden-LLM artifact store for trace artifacts and diagnostic attachments migrated from Firebase Storage. No MinIO or Langfuse service is part of the current deployment.
- The HLLM Collector exports gateway traces to Laminar and diagnostic traces to Tempo; the retired Langfuse pipeline is forbidden.
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
  - Current foundation paths exist: `cmd/harden-llm-gateway/main.go`, `api/openapi.yaml`, `internal/testkit`, `internal/gateway/httpapi`, `scripts`, `fixtures/parity`, and `plans/implementation-status.json`.
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
- Setup: backend-owned source and test paths, `go.mod`, backend build manifests, and base Compose config. Shared public route policy is owned by `prls-co/caddy-shared`. `frontend/` and `deploy/frontend/` are owned by the separate frontend specification and are excluded from content scans.
- Assertions:
  - Backend production/test/deploy code contains no Firebase, Firestore, Firebase Auth, Functions, Hosting, or Storage dependency.
  - Backend code contains no Phoenix, LiveView, React, Vite, HTML-template, browser-session, or frontend-asset implementation.
  - Go production dependencies contain no application SQLite, Temporal, Sentry, or Langfuse SDK/client.
  - Collector configuration contains no Langfuse exporter, authenticator, credential, or dependency and exactly one dedicated HLLM Laminar exporter.
  - Application code has no direct Langfuse host/key configuration or ingestion request.
  - Active deployment manifests, descriptors, and image locks contain no Langfuse service, route, image, secret, or persistent volume.
- Pass criteria: the forbidden-dependency scan passes, retired Langfuse deployment surfaces are absent, and the Collector retains one dedicated HLLM Laminar exporter.
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

- Target: `internal/providers/requests_test.go`, `internal/providers/optional_system_prompt_test.go`, `internal/gateway/run_validation_test.go`
- Command: `go test ./internal/providers/... -run 'TestProviderRequestParity|TestOptionalSystemPrompt|TestPerplexityUsesAgentEndpoint' -count=1`; `go test ./internal/gateway/... -run 'TestValidateRunInput' -count=1`
- Setup: local HTTP servers and request goldens for OpenAI-compatible Chat, OpenAI Responses, Gemini GenerateContent, Anthropic Messages, and generic OpenAI-compatible endpoints.
- Assertions:
  - Paths, methods, headers, model IDs, prompts, schemas, tools, reasoning options, token limits, and native options match source fixtures.
  - Gemini bare model IDs and Google resource names both construct the exact
    `/v1beta/models/{id}:generateContent` path; the captured source oracle remains unchanged.
  - Chat and Responses requests omit an absent system prompt for both text and
    structured calls; nonempty prompt contents and message order are preserved.
  - Perplexity Responses requests use the canonical `/v1/agent` endpoint and
    reject a legacy chat override. Native search uses the shared `web_search`
    tool; Agent `search_results` retain execution and safe source evidence,
    including executed searches with no results.
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
  - Scalar reported costs and USD objects containing `total_cost` normalize
    as provider-reported exact totals. Missing/invalid totals and non-USD
    currency remain `ACCOUNTING_INVALID`; valid usage remains available.
  - Null optional prompt/completion detail containers are absent breakdowns;
    valid aggregate counts and remaining reported details are preserved. Actual
    null/negative/fractional counts and malformed containers remain invalid.
  - Reasoning tokens exceeding the completion total remain `ACCOUNTING_INVALID`;
    the valid provider-reported cost survives that failure and output is withheld.
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

### TEST-017: Retired profile catalog contract

- Status: Retired by `PLAN-HLLM-PROXY-REFERENCE-001`; profile catalogs,
  provisioning and profile-owned credentials are removed from the product.
  This ID is retained for historical traceability and must not be reused.

### TEST-018: Retired credential bundle contract

- Status: Retired with `TEST-017`. Credential bundles and profile-bound
  encrypted credentials are removed; provider credentials now come from the
  static upstream configuration. This ID is retained and must not be reused.
- Setup: deterministic test key IDs, fixed nonces through injected random reader, fake credentials, and source bundle fixtures.
- Assertions:
  - AES-256-GCM uses random production nonces, key IDs, and owner/credential/origin AAD.
  - Wrong key, wrong AAD, or modified ciphertext fails.
  - API state never exposes raw keys or ciphertext internals.
  - Stored credential metadata with empty or null inference types remains
    invalid; storage cleanup must remove unused records rather than relaxing
    validation or skipping corrupt metadata.
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
  - Application migrations and credentials name only the Harden-LLM Postgres service; no second Langfuse Postgres service or endpoint is configured.
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

### TEST-022: Current identity auth, isolation and transactional profile save

- Targets: `internal/gateway/auth/service_test.go`, `internal/gateway/resource_routes_test.go` and real Postgres profile/storage integration tests.
- Commands: `go test ./internal/gateway/auth -count=1`; `make test-integration`.
- Setup: private Control Plane HTTP fixture, two non-UUID user IDs, two sessions for one user, both users sharing a company and account-less contexts, explicit token scope for the second user, isolated Postgres and local provider fixture.
- Assertions:
  - Exactly one valid service bearer is required; malformed/duplicate auth, session references and unexpected cookies are rejected.
  - Human requests resolve current enabled Control Plane identity. Company selection and company product grants do not gate HLLM. Revocation, malformed identity and unavailable authority fail closed without a machine-user fallback.
  - Different users are different owners even in the same company; two sessions for one user share an owner; the scoped token shares only its configured user's data. Owners remain isolated across profiles/history/stats/traces/artifacts/state/cache/bundles/deletion.
  - HLLM stores no local credentials or human sessions. Phoenix sign-in/out and secure host-only cookies use the shared identity components.
  - Profile probing precedes the short database commit; failed probes preserve prior state. Credential-free edits preserve owner-bound credentials. Foreign-owner bundles fail before probing/writes. Probe/model refresh use TEST-014 endpoint policy.
- Decision change: PLAN-HLLM-LOGIN-OWNERS-001 explicitly supersedes the previous assertion that different logins share a company's data; authentication and isolation oracles remain mandatory.
- Pass criteria: identity/isolation and profile transaction assertions pass.
- Expected runtime: within the existing integration task envelope.

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
  - Provider model discovery returns Gemini bare IDs that prepare the exact
    `models/{id}:generateContent` resource path (`internal/providers/models_test.go`).
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
- Setup: root Go files, `cmd/`, `internal/`, `api/`, backend fixture/scripts, `go.mod`, the backend `Makefile` gates, base deployment files, and base Compose manifests. Planning documents, `frontend/`, and `deploy/frontend/` are excluded from literal-name scans.
- Assertions:
  - No backend dependency, import, environment name, configuration, deploy script, server, emulator, or production code calls Firebase Auth, Firestore, Functions, Hosting, or Storage.
  - No backend package contains Phoenix, LiveView, React, Vite, JSX, HEEx, HTML-template, frontend asset, browser-cookie, or browser-CSRF implementation code.
  - Backend tests and release commands do not build or test any frontend application.
  - The seven-service production Compose topology contains no Caddy and does not depend on or route a frontend service; the optional frontend overlay is tested under the frontend specification, with explicit test-only Caddy fixtures.
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

### TEST-030: Collector pipelines and Laminar-only HLLM trace export

- Target: `internal/deploytest/collector_test.go`
- Command: `go test ./internal/deploytest/... -run TestCollectorPipelines -count=1`
- Setup: parsed `deploy/otel/collector.yaml` and fake OTLP endpoints for traces, metrics, and logs.
- Assertions:
  - Traces export to Tempo and complete `service.name=harden-llm-gateway` traces export once to the dedicated HLLM Laminar project with root and children preserved.
  - The HLLM trace pipeline contains no Langfuse exporter and the Collector has no Langfuse authentication extension.
  - Metrics expose one Prometheus scrape endpoint.
  - OTel log records mirrored from the composed `slog` handler export to Loki OTLP/HTTP.
  - Memory limiter, batch, bounded queues, retry limits, and redaction processors are present.
  - No Langfuse service, trace pipeline, exporter, or authentication extension is configured.
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

### TEST-033: Production Compose service and storage ownership

- Target: `internal/deploytest/compose_caddy_test.go`
- Command: `go test -tags=compose ./internal/deploytest/... -run '^TestComposeDeploymentContract$' -count=1`
- Setup: effective production `docker compose config` and the Harden-LLM image manifest. Shared ingress routes are owned by `prls-co/caddy-shared`; HLLM production Compose must not define Caddy.
- Assertions:
  - The seven HLLM backend services exist: gateway, Harden-LLM Postgres, Collector, Prometheus, Loki, Tempo, and Grafana. Optional Phoenix web is tested through its dedicated overlay; in the merged topology it depends on the healthy gateway only and does not gate startup on Postgres health. Caddy and Garage remain separate shared service owners.
  - Production topology, descriptors, and image locks contain no Langfuse service, ClickHouse, Redis, MinIO, retired image, or Langfuse-owned persistent volume.
  - Named HLLM volumes and health checks exist, and all HLLM-owned image tags/digests are pinned.
  - Production Compose has no Caddy service, Caddy volumes, Garage service, Garage-owned volume, RPC secret, bootstrap command, or dependency edge. The gateway and Loki join the existing external `prls-observability` network; the gateway remains reachable through the shared edge, and Loki uses `garage-shared:3900` for observability storage.
  - No HLLM-owned service publishes a host port in the effective production topology.
  - Caddy route/TLS/auth/body-limit policy is tested by the shared owner; this test does not duplicate those route assertions.
  - The seven-service backend model does not include Phoenix/LiveView; the optional web service stays in its dedicated overlay and explicit frontend smoke fixture.
  - Production Compose owns no Harden-LLM artifact store or artifact bucket; shared Garage remains external infrastructure used by Loki.
  - No Firebase, application SQLite, Sentry, Temporal, or retired Langfuse deployment dependency exists.
- Pass criteria: parser tests and `docker compose config --quiet` pass with seven backend services and no edge owner in HLLM production.
- Expected runtime: 20 seconds.

### TEST-289: HLLM and shared-ingress ownership boundaries

- Type / verifies: static source ownership, isolated fixture retention, and cleanup ownership for the one-shot frontend Caddy validation container; REQ-017 and REQ-019.
- Location: `internal/deploytest/shared_caddy_test.go`.
- Command: `go test ./internal/deploytest/... -run '^TestSharedIngressOwnership$' -count=1`.
- Fixtures/data: HLLM production Compose, image lock, example production descriptor, smoke/preview Compose sources.
- Deterministic controls: standard Go test tools; no Docker daemon, provider credentials, network or deployed resources.
- Assertions: production service/volume/image/descriptor selections omit Caddy; shared-edge environment variables are not mandatory HLLM inputs; backend/frontend smoke owns explicit test Caddy and reserved isolated subnets; the disposable frontend-config validation container carries the exact smoke project label so the existing resource receipt can clean it if interrupted; preview retains its separate Caddy.
- Pass criteria: ownership separation holds, each retained isolated edge fixture has its own reviewed source, and the one-shot validation container is discoverable by exact project cleanup.
- Expected runtime: 1–15 seconds.

### TEST-034: full Compose signal and application smoke

- Target: `internal/smoke/compose_smoke_test.go`
- Command: `go test ./internal/smoke/... -tags=compose -run TestComposeSmoke -count=1`
- Setup: clean named test project, production Compose, private integration overlay, and `deploy/test/compose.smoke.yml`; reference hardware; images already available; generated non-production secrets. The smoke overlay owns an isolated Garage fixture with fresh project-scoped volumes, unique non-external networks, and reserved test-only CIDRs so tests do not consume the host default address pools.
- Assertions:
  - The effective backend smoke stack has twelve services: seven production backend services plus test-only Caddy, Garage, fake provider, Collector state initializer, and Laminar OTLP sink. Ten long-running services become healthy within the 300-second legacy readiness cap, the fake provider is healthy, and the state initializer exits successfully. No Langfuse service starts.
  - The test-only private `fake-provider` service is reachable only by the gateway and publishes no host port.
  - API routes through the explicit test-only Caddy fixture; gateway readiness reaches Harden-LLM Postgres and the isolated Garage fixture.
  - Login returns an opaque bearer token that authenticates the smoke lifecycle without a browser cookie or CSRF path.
  - One fake-provider run creates application state and an available artifact index in Harden-LLM Postgres.
  - The linked canonical redacted trace artifact is fetched from the isolated Garage fixture through an authenticated gateway route and short-lived test-Caddy artifact-host URL; its SHA-256 and byte length match Postgres.
  - Its trace reaches Tempo and the authenticated test-only Laminar OTLP sink; both the OTel trace ID and application trace ID are read from the sink output. Its metric reaches Prometheus and correlated log reaches Loki.
  - The Collector exports gateway traces to the test Laminar sink and Tempo; no retired Langfuse endpoint or credential is configured.
  - Grafana datasources are healthy.
  - No MinIO service or volume starts, and Harden-LLM artifact traffic resolves to the isolated smoke Garage fixture through the same `garage-shared` service name used in production.
- Pass criteria: end-to-end correlation IDs are found in Tempo, the test Laminar sink, and application trace artifacts; metric/log correlation passes; only the loopback-bound test Caddy ports are published.
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

- Target: all backend-owned paths and the base seven-service Compose topology without Caddy; `frontend/` and `deploy/frontend/` are excluded
- Command: `make verify`
- Setup: Go and Node dependencies installed, isolated Harden-LLM Postgres and Garage, and pinned Harden-LLM images. The base production topology has seven backend services and no Langfuse deployment dependency.
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
  For one shared provider account with a request-rate budget, set
  `HARDEN_LLM_LIVE_PROVIDER_INTERVAL` to its minimum request-start interval
  (Perplexity Tier 0: `1200ms`). One process-owned gate spaces credential
  resolution across the configured profiles and both call types. This does not
  add retries, change prompts, or relax single-attempt and accounting assertions.
- Compile coverage: `make test-static` compiles TEST-037/038 with the `live` tag
  and `-run '^$'`, without running tests or making provider calls.
- Assertions:
  - Configured providers return tiny text and supported structured output.
  - Result and provider accounting ledgers have nonnegative usage and known
    cost subtotals; observed known cost retains its source.
  - No live output enters committed fixtures or evidence without redaction.
- Pass criteria: every explicitly configured provider passes.
- Expected runtime: 240 seconds.

### TEST-038: live self-hosted gateway lifecycle

- Target: `internal/smoke/live_gateway_test.go`
- Command: `go test ./internal/smoke/... -tags=live -run TestLiveGatewayLifecycle -count=1`
- Setup: running full stack, explicit user-scoped service token and provider credential.
- Assertions:
  - Authenticated profile save/probe, model refresh, run, trace retrieval, authenticated Garage artifact retrieval, bundle export, profile deletion, and test-data cleanup pass.
  - The live trace ID appears in Tempo; Prometheus confirms the HLLM Laminar exporter span counter increased after the run; correlated metrics and logs appear in Grafana without secret leakage. No observability query credential is required.
- Pass criteria: lifecycle completes and cleanup removes test application records.
- Expected runtime: 360 seconds.

### TEST-039: timeout RCA policy guard

- Target: `internal/testkit/timeout_policy_test.go`
- Command: `go test ./internal/testkit/... -run TestTimeoutPolicy -count=1`
- Setup: target diff, timeout baseline manifest, `ker/` and evidence metadata.
- Assertions:
  - A timeout increase requires an RCA recording exact phase, start proof, failed timings, comparable successes, p95/max, configured timeout, headroom, root cause, and rationale.
  - The unchanged baseline records the 60-second gateway maximum run duration and the frontend-independent backend gate does not infer or pad a client timeout.
  - The unchanged 300-second Compose readiness budget is a legacy cap from the former topology, not a requirement to start retired services or evidence for increasing a timeout.
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
  baseline, and release selectors cannot launch browsers. Initial preview-host
  setup requires the explicit preview Cloudflare token, trims it, and rejects
  missing/blank values even when a generic Cloudflare variable is present; the
  bootstrap has no Shaman SSH credential-file dependency. Browser assertions
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
- current HLLM image digests and source identity; retired Langfuse release details remain only in historical release evidence;
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
invoke `frontend/`; the HLLM Collector has no Langfuse export path and sends
gateway traces only to the dedicated Laminar project; Garage is the only
Harden-LLM artifact store; and the full Compose smoke proves correlated
application and diagnostic behavior.

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

### TEST-227: Current cache persistence and schema integrity

- Type / verifies: integration; REQ-217, REQ-218, REQ-221, REQ-223.
- Location: `internal/postgres/cache_test.go`, `internal/postgres/repository_test.go` and `internal/gateway/run_test.go`.
- Command: `make test-integration`.
- Fixtures/data: Runner-owned `PostgresLease`, fresh schema version 11, user-scoped rows, exact large-number/decimal JSON, upsert timestamps and independent owners.
- Deterministic controls: Existing service-pool runner and lease cleanup; real `Store.Migrate`; no application database or per-test Compose fallback.
- Pass criteria: The six cache projection columns, exact values and producer identity survive repeat migration; readiness/idempotency/concurrent initialization pass; retired schema ledgers fail before product writes; owner isolation/upsert/concurrency and rollback/locking checks remain intact. The report contains executed `TestRecoveryIntegrityStorage` cases. Historical document-conversion tests retire with migrations 1–10 after the authorized clean cut; no legacy-data conversion is supported.
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
- Assertions: Compose serialization normalization does not hide real drift; logical network keys resolve through `model.networks[*].name` and wrong/extra/missing physical memberships remain drift; release identity may be explicit and service-specific; equivalent configuration never invokes `up`; unapproved differences block selected application; command arguments and diagnostics contain no resolved values.
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
added. The test runner owns disposable Docker resources. The standalone load
driver uses a local scripted provider; it does not configure or call a deployed
Harden-LLM gateway.

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
- Fixtures/data: Fake child/process-group controller with partial creation, TERM, crash, hung inventory, foreign-attachment, and asynchronous container-removal cases.
- Deterministic controls: Scripted fake Docker state transitions, file gates for blocking child cases, bounded cleanup deadline, 5-second process-readiness deadline, and bounded output; no daemon/socket.
- Pass criteria: Preserve the first failure; reap owned child before deletion; unknown inventory, failed exact removal, foreign attachment, non-empty final inventory, or receipt persistence failure fail acceptance; delete exact owned IDs only; retain pending evidence. When Docker reports that an exact container removal is already in progress, wait within the cleanup deadline and accept only after the exact project inventory confirms that container is gone; otherwise retain pending evidence and fail. A failed best-effort Compose `down` is reported in `cleanupWarnings` and is non-fatal only when exact fallback cleanup completes, final inventory is empty, and the receipt is durably `cleaned`.
- Expected runtime: 45 seconds.

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
- Fixtures/data: Numeric Docker API records; B/kB/MB/GB and KiB/MiB/GiB text; missing/malformed units; CPU; duplicate/project labels; host memory and Docker data-root headroom metrics.
- Deterministic controls: Frozen inputs, no daemon, integer-byte expected values, seed 104729.
- Pass criteria: Convert recognized units correctly; unknown units are errors/unknown, never zero; project attribution is exact; Docker memory, RSS, sampled peak, host memory, and Docker data-root disk remain distinct; fractional CPU percentages aggregate as finite percentage values rather than integer bytes. Missing or malformed Docker memory/CPU values preserve a precise bounded parser reason; RSS and volume-used-byte metrics explicitly state when they are not collected. Unavailable exact container measurements identify the bounded failing stage, exit status, timeout flag, and at most 256 characters of caller-redacted stderr; raw Docker output is not retained.
- Expected runtime: 5 seconds.

### TEST-276: Bounded load generation and streaming accounting

- Type / verifies: unit; REQ-347, REQ-348, REQ-349.
- Location: `internal/capacity/driver_test.go`.
- Command: `go test ./internal/capacity -run '^TestCapacityDriver' -count=1`.
- Fixtures/data: Fake clock/transport; scripted delay, 429, invalid JSON, recovery, cache hit, terminal stream success, and truncated EOF.
- Deterministic controls: Seed 104729; finite queues; no Docker/internet; test contexts at most 5 seconds.
- Pass criteria: Open-loop arrivals remain independent; accounting reconciles; concurrency/request bounds hold; EOF without terminal success fails; recovery dispatch equals script; production endpoints reject before dialing.
- Expected runtime: 10 seconds.

### TEST-277: Retired real-gateway capacity boundary

- Status: Retired 2026-10-06 by the OpenAI proxy/reference-history cutover; the identifier is retained and must not be reused.
- Historical scope: Exercised the former custom `/api/v1/run` endpoint, profile selection, and proxy-owned persisted call history/artifacts.
- Retirement rationale: The supported contract is now stateless OpenAI-compatible `/v1` proxying; profile configuration and proxy-owned history/artifact persistence have been removed. The retired end-to-end case depended on those removed interfaces and stores. `TEST-409` exercises the current gateway plus Phoenix reference app boundary, but it does not claim load or capacity results.

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
- Pass criteria: Executable test registration is discoverable; cheap task is offline/container-free; Make and manifest do not recurse; the retired capacity workflow is absent; release is browser-free; bounded private per-run reports are written by default and fast/integration/release jobs upload them with `always()`; candidate identity policy is present.
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

### TEST-282: Retired capacity history API pagination

- Status: Retired 2026-10-06 by the OpenAI proxy/reference-history cutover; the identifier is retained and must not be reused.
- Historical scope: Followed pages from the former proxy-owned call-history REST endpoint.
- Retirement rationale: History is recorded and displayed by the Phoenix reference implementation. It is outside the supported OpenAI-compatible proxy REST API, so no gateway history endpoint remains.

### TEST-283: Retired private gateway-image publication contract

- Status: Retired 2026-09-22 by ADR-HLLM-028; not registered in any active test tier or release gate. The identifier is retained and must not be reused.
- Historical verification: REQ-353, the manual private-GHCR publisher contract implemented at source commit `6887fcd8146961dc64598dd7a236e7a9fc522c9c`.
- Historical source: `docs/archive/harden-llm-ghcr-publisher-reference-6887fcd.tar.gz`, containing the workflow, publisher contract test, and exact Dockerfile from that commit.
- Retirement rationale: active production uses an exact-SHA local image on one existing Docker host; there is no current distribution need justifying publisher-specific workflow permissions and tests. Current build/deployment acceptance is defined by `SPEC-HLLM-IMAGE-DEPLOYMENT-001` and KER-IBD-001 through KER-IBD-010.
- Restoration: review the archived source against the current release, security, retention, and restore requirements; do not extract/run it automatically. Reusing TEST-283's source requires reactivating an explicitly approved publication design.

### TEST-284: Runtime progress snapshot callbacks

- Type / verifies: unit; runtime `ProgressSnapshot` callback compatibility.
- Location: `internal/runtime/repair_test.go`.
- Command: `go test ./internal/runtime -run '^TestProgressSnapshotContracts$' -count=1`.
- Fixtures/data: Synthetic executor results, stream counters, cache, origin, run IDs, and explicit repair work; no provider or persistence dependency.
- Deterministic controls: Local executor and cache fakes, fixed advancing clock, bounded deadline, no sleeps or network.
- Pass criteria: Public `Execute` callback observations preserve simple and explicit path event order, preflight and nil-work differences, active stream plus completed-attempt counters, recovery stage/profile/reasoning identity, cache-hit events, deadline facts, per-call sequencing, copied attempts/accounting/timeouts, and no-callback behavior. Intentional path differences remain explicit.
- Expected runtime: Under 1 second.

## 25. OpenAI proxy and reference history transition

REQ-400–415 are defined in the implementation-plan amendment. TEST-400–411 are active IDs; each test file uses this specification ID and its TEST ID. Frontend cases also use WEB-TEST-114–117.

| ID | Type / owner | Command | Acceptance oracle |
| --- | --- | --- | --- |
| TEST-400 | Static; traceability | `node --test scripts/test/proxy_reference_contract_test.mjs` | Paths, extension, fixtures, requirements, tests and one task owner agree; SQL task selects only PostgreSQL. |
| TEST-401 | Unit; root/provider | `go test . -run '^TestProxyRequestContract$' -count=1` | Ordered messages/tool results/native reasoning survive; recovery/accounting exact; no profile input or implicit model fallback. |
| TEST-402 | Unit; gateway runtime/cache | `go test ./internal/gateway -run '^TestProfileFreeRuntime$' -count=1` | One startup client, scoped cache, no owner factory, disabled cache has no store access. |
| TEST-403 | Unit; HTTP API | `go test ./internal/gateway/httpapi -run '^TestOpenAIContract$' -count=1` | Official SDK consumes local handlers/models/errors/final SSE; retries disabled; unsupported fields do not dispatch. |
| TEST-404 | Unit; auth/startup | `go test ./cmd/harden-llm-gateway -run '^TestOpenAIAuthAndStartup$' -count=1` | Only HARDEN_LLM_TOKEN works; aliases/owner are gone; cache-disabled startup needs no DB. |
| TEST-405 | Unit; Phoenix workflow | `cd frontend && mix test test/harden_llm_web/live/shared_workspace_test.exs` | Logins share new history; only frontend records after publish; access enforced; direct calls absent. |
| TEST-406 | Superseded by TEST-428/439 | Retired Phoenix context; see ADR-HLLM-033. | REST workflow, canonical record fidelity and draft concurrency now run at their UI/Go owners. |
| TEST-407 | Superseded by TEST-424/426/427/439/442 | Retired Phoenix SQL task; see ADR-HLLM-033. | Real Go PostgreSQL/Garage integration retains schema, constraints, timeout, scope, deletion and no-replay assertions. |
| TEST-408 | Static; config/cutover | `node --test scripts/test/proxy_reference_cutover_test.mjs` | Synthetic one-token/connection config; old routes/profile keys absent; clean-cut excludes unrelated sentinel. |
| TEST-409 | Integration; Compose smoke | `node scripts/run-test-tier.mjs --task go-compose` | Gateway/Phoenix work with synthetic upstream/real SQL; direct calls leave history empty; cleanup succeeds; no browser/provider. |
| TEST-410 | Unit; Compose log-query contract | `go test ./internal/smoke -run '^TestLokiCorrelatedResponseLog$' -count=1` | A log is accepted only when the same Loki stream carries the expected OpenTelemetry trace ID and `/v1/responses` route with a completed-request message; malformed and split-stream results reject. |
| TEST-411 | Superseded by TEST-424/433/450 | Retired Phoenix release migration; see ADR-HLLM-033. | One-shot Go schema setup is repeatable, executes before application startup and fails explicitly on incompatibility. |

`test/fixtures/proxy-reference-contract.json` is sanitized from checked-in CPA Chat/Responses fixtures; it contains no credentials/user data/live responses.

## 26. Reference decoupling and API-key-scoped state (2026-10-12)

ADR-HLLM-033 is the accepted boundary; the plan's sections 4 and 8 are the
canonical API/type/configuration/data contracts. `api_key_hash` is derived from
the validated incoming key, never a human ID or request selector. Git presets
are copied starting values, `.env` holds deployment secrets, and PostgreSQL
holds complete immutable create-on-save snapshots, encrypted entered keys,
drafts/cache/history. `draftKey` is host-supplied inside the key scope.

Only reference calls record terminal history. Recording failure preserves the
result. Unknown COMMIT with empty/failed readback remains unconfirmed; no replay
or polling. Garage objects use the authenticated hash/run prefix. Laminar is
only diagnostic tracing. Tests below are allocated, not all implemented or
passed: the phase ledger supplies actual evidence. Historical IDs stay assigned;
old assertions move to their new owner without weakening their purpose.

### TEST-420: Architecture and baseline contract

- Type: static; verifies: REQ-420, REQ-425, REQ-432, REQ-433, REQ-435.
  - Location: `internal/testkit/reference_decoupling_contract_test.go`.
  - Command: `go test ./internal/testkit -run '^TestReferenceDecouplingContract$' -count=1`.
  - Fixtures/mocks/data: Pinned baseline manifest, canonical documentation and OpenAPI ownership declarations.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: Every baseline capability retains an owner with the two accepted semantic changes recorded. Canonical policy specifies one bearer/key-hash scope, full create-on-save snapshots, Git/`.env`/PostgreSQL authority, frontend-only Portal admission and Model Recall interface review. No Go user/session ownership or conflicting store declaration. These source checks do not substitute for authentication/storage behavioral tests.
  - Expected runtime: 10–30s.

### TEST-421: Stateless execution and effect boundaries

- Type: unit; verifies: REQ-421, REQ-424, REQ-431, REQ-432.
  - Location: `stateless_contract_test.go`.
  - Command: `go test . -run '^TestStatelessContract$' -count=1`.
  - Fixtures/mocks/data: Local provider, recording cache/event ports, injected clock/wait, concurrent distinct profile snapshots.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: Retry/repair/budgets/cache/accounting and optional trace/artifact capture retain their assertions; nil capture does no artifact work, and capture never leaks fixture credentials; no cross-call mutation, infrastructure SDK ownership or repeated inference after sink failure.
  - Expected runtime: 10–30s.

### TEST-422: Ordinary OpenAI client contract

- Type: unit; verifies: REQ-422, REQ-431, REQ-432.
  - Location: `internal/gateway/httpapi/openai_contract_test.go`.
  - Command: `go test ./internal/gateway/httpapi -run '^TestOpenAIContract' -count=1`.
  - Fixtures/mocks/data: Existing official-SDK harness and selected deployment-key fixture, local provider, missing/invalid bearers, unavailable database/Control Plane and supported/unsupported requests. This existing protocol oracle stays runnable in P01 before P02 adds multi-key composition.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: Models/chat/responses accept the selected deployment bearer without login/profile/custom identity header. Errors/tools/final-only SSE match OpenAPI; cache-off calls succeed during storage/identity outage and direct calls create zero history entries. No incoming key/hash enters root Request or diagnostic payload. P02's TEST-425/439 add two-key authentication/cache isolation without changing this existing protocol oracle; rerun this gate after P02 composition changes.
  - Expected runtime: 10–45s.

### TEST-423: Complete saved profiles and credential bindings

- Type: unit; verifies: REQ-423, REQ-427, REQ-432.
  - Location: `stateful/profile_test.go`.
  - Command: `go test ./stateful -run '^TestProfileContract$' -count=1`.
  - Fixtures/mocks/data: Value-only complete snapshot/template fixtures for two API-key hashes, all run fields and explicit false/zero/empty values; changed/deleted Git templates, hidden choices, credential bindings and five recovery leaves. No fake SQL repository.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: One snapshot validator preserves all nonsecret setup/input values. Template selection fills a draft; Save freezes it with a new UUID/time/suffix, never a live override or update. Changed/deleted Git templates leave saved snapshots unchanged. Invalid origin/binding and foreign-key records fail without substitution. Picker/detail DTOs omit secrets, and core retry validation owns policy semantics. Real CRUD/restart/refresh/bundles remain TEST-438/440; encryption remains TEST-444.
  - Expected runtime: 10–30s.

### TEST-424: PostgreSQL storage and concurrency

- Type: integration; verifies: REQ-423, REQ-424, REQ-427, REQ-431, REQ-432.
  - Location: `stateful/storage_integration_test.go`.
  - Command: `go test -tags=integration ./stateful -run '^TestReferenceStorage$' -count=1`.
  - Fixtures/mocks/data: Real PostgreSQL lease; two key hashes; snapshot, hidden-preset, entered-credential, cache, mutable-draft and history rows; cross-key FKs, invalid binding/hash/null cases, credential/draft positive revisions and incompatible schema version.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: Fresh/versioned ApplySchema is transactional and idempotent; mismatch fails without reset. Composite key-hash predicates/FKs isolate every row and forbid cross-key credential binding. Complete snapshot rows and hidden-preset preferences satisfy their separate constraints. History rows carry the key hash and a page index; no incoming-key/user/session/outbox registry tables. Only leased data is cleaned; cancellation bounds connection checkout. Schema initialization uses one 30 s administrative budget; short request SQL keeps 600/500 ms. Real SQL settings are transaction-local, and caller cancellation rolls back partial administrative DDL. Full transaction/restart/cache behavior remains TEST-438/439/440.
  - Expected runtime: 30–120s with warm service pool; existing aggregate integration gate remains required.

### TEST-425: API-key REST access

- Type: unit; verifies: REQ-425, REQ-427, REQ-432.
  - Location: `internal/gateway/httpapi/reference_access_test.go`.
  - Command: `go test ./internal/gateway/httpapi -run '^TestReferenceAccess' -count=1`.
  - Fixtures/mocks/data: One httptest API with two accepted synthetic HLLM keys from `access-keys.json`, a recording handler that captures request context, invalid/duplicate/hash-only bearers, caller owner/user/hash/session selectors and a Control Plane request counter. No database and no fake store; HTTP-level storage isolation is TEST-441.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: A valid key alone authorizes both REST surfaces and derives its hash server-side; missing/invalid bearer fails before storage/provider work. Scope/session selectors return 400 `scope_selector_rejected`, and fingerprints cannot authenticate. The handler receives exactly the fixed expected hash for each key, and `ClientForKey` does no storage work. Go performs zero identity-authority requests, including during outage. Portal login/admission remains a separate Phoenix host assertion; ordinary `/v1` is independent.
  - Expected runtime: 10–30s.

### TEST-426: Reference history semantics

- Type: integration; verifies: REQ-426, REQ-429, REQ-431, REQ-432.
  - Location: `stateful/history_integration_test.go`.
  - Command: `go test -tags=integration ./stateful -run '^TestReferenceHistory$' -count=1`.
  - Fixtures/mocks/data: Real PostgreSQL and Garage leases; local provider; repaired success, cache hit, validation failure, canceled request, explicit disablement, enabled-with-nil-pool configuration error, closed pool, oversize record, injected commit hook that drops the outcome after COMMIT, failed readback, and synthetic secrets inside driver-error fixtures.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: Healthy enabled recording inserts one complete record with the authenticated hash; direct/disabled calls do no history I/O. Every detail/list/stats/restore/delete/clear is key-scoped. History outage/configuration failure preserves otherwise authorized inference; invalid profiles/credentials prevent dispatch. Saved, not saved (definite), unconfirmed (absent or failed readback after unknown commit), empty and read failure differ. Exactly one insert attempt and at most one readback per run; no inference retry, polling or secret diagnostic leak.
  - Expected runtime: 30–120s with warm service pool.

### TEST-427: History statistics, delete and clear

- Type: integration; verifies: REQ-426, REQ-427, REQ-429, REQ-431, REQ-432.
  - Location: `stateful/history_queries_integration_test.go`.
  - Command: `go test -tags=integration ./stateful -run '^TestHistoryQueries$' -count=1`.
  - Fixtures/mocks/data: Real PostgreSQL and Garage leases; history rows for key A (two client contexts) and key B with known/unknown/zero costs and cache hits; artifact objects under each run prefix; one missing object; one run started after the clear cutoff.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: Statistics use the same key/window filter as list, sum known costs, count unknown observations and never treat missing records as zero-cost. Delete removes the run's Garage prefix then its row; a missing object counts as removed and a failed object delete keeps the row and reports `artifact_cleanup_failed`. Clear removes only key A rows before the cutoff in batches, leaves key B and the later run intact and returns deleted count, `complete` and the first failed run ID.
  - Expected runtime: 30–120s with warm service pool.

### TEST-428: Restored workspace vertical workflow

- Type: unit; verifies: REQ-420, REQ-426, REQ-427, REQ-429.
  - Location: `frontend/test/harden_llm_web/live/restored_workflow_test.exs`.
  - Command: `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_workflow_test.exs`.
  - Fixtures/mocks/data: WEB-TEST-118; OpenAPI-valid private Req fixtures, full saved-input snapshots and baseline selectors/forms; two Portal identities sharing one HLLM key with distinct draft keys, plus a host context using the second key.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: Selection → complete draft/new profile Save → repair/progress/result/stats → same-key history restore works. Portal identities share profiles/history but keep separate drafts; a different key partitions everything. Restore copies prompts/settings without name lookup or implicit Save/Run. All recording warning states preserve output and forbid resubmission. Baseline markup/style remains, with only accepted snapshot naming/save semantics.
  - Expected runtime: 15–60s.

### TEST-429: Reusable component library

- Type: unit; verifies: REQ-420, REQ-428.
  - Location: `frontend/packages/harden_llm_ui/test/harden_llm_ui/components_test.exs`.
  - Command: `cd frontend/packages/harden_llm_ui && mix test --seed 104729 test/harden_llm_ui/components_test.exs`.
  - Fixtures/mocks/data: WEB-TEST-119; independent test host informed by read-only Model Recall interface review, schema-valid API fixtures and two component instances with host-supplied API-key contexts.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: Picker/input/result/stats/history/pagination components render and dispatch independently; package imports no host/Portal/Ecto module, stores no incoming keys and owns no login. API-key context/instance state do not leak; explicit save/detail and baseline visual bindings work. Assets/decoders/styles have one source; Model Recall migration is not required.
  - Expected runtime: 15–60s.

### TEST-430: Progress and client decisions

- Type: unit; verifies: REQ-428, REQ-429.
  - Location: `scripts/test/run_progress_test.mjs`.
  - Command: `node --test scripts/test/run_progress_test.mjs`.
  - Fixtures/mocks/data: Existing production decision functions, fixed event sequences including duplicate/out-of-order/disconnect cases.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: Progress remains monotonic and terminal outcomes stable; multiple widgets do not leak state; tests import production functions without a DOM emulator.
  - Expected runtime: 1–5s.

### TEST-431: Complete restored reference workflows

- Type: unit; verifies: REQ-420, REQ-423, REQ-425, REQ-426, REQ-429.
  - Location: `frontend/test/harden_llm_web/live/restored_workflows_test.exs`.
  - Command: `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_workflows_test.exs`.
  - Fixtures/mocks/data: WEB-TEST-120; baseline profile/editor/bundle/history/trace/artifact/embed cases; private Req ownership and shared OpenAPI fixtures.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: Every baseline manifest capability passes success/error interactions; presets and persisted private customizations/credential replacement remain usable; history messages use the common error contract. No removed controls, replacement layout or second logic/style implementation; shared login/logout and artifact authorization remain correct without an HLLM registration/password flow.
  - Expected runtime: 20–90s.

### TEST-432: Shared deployment topology

- Type: static; verifies: REQ-425, REQ-430, REQ-435.
  - Location: `internal/deploytest/reference_topology_test.go`.
  - Command: `go test ./internal/deploytest -run '^TestReferenceTopology$' -count=1`.
  - Fixtures/mocks/data: Compose/Caddy/env/descriptor fixtures; approved shared-service attachments; HLLM-only reset allowlist.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: One Go and one Phoenix service share UI/`/v1`/`/api/v1` origin. Go validates one configured key-array source for all REST; Phoenix uses its server-held selected key and Portal admission. No Go Control Plane/session requirement, private-only API route, second hostname, dedicated production PostgreSQL, Phoenix DB credentials or broad reset. Production `gateway-schema` is a one-shot command with no build stanza, using the accepted gateway image and the same protected database configuration; it is not a third long-lived application. Preview descriptors may keep their per-environment `postgres`, attached only to `gateway`, and set no `HARDEN_LLM_ARTIFACT_*` values. No key issuance, secret echo or per-user key synchronization.
  - Expected runtime: 10–30s.

### TEST-433: Browser-free composed workflow

- Type: e2e; verifies: REQ-422, REQ-425, REQ-426, REQ-430, REQ-431.
  - Location: `internal/smoke/compose_smoke_test.go`.
  - Command: `make test-compose`.
  - Fixtures/mocks/data: Existing fake provider and frontend Control Plane admission fixture, two synthetic HLLM access keys, local Caddy, isolated real PostgreSQL/Garage, HTTP/LiveView harness.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: Actual one-host boundaries preserve restored workflow and frontend Portal admission without company/product grant. A fresh leased database receives schema version 1 from the gateway's schema command before serving; a real reference write succeeds afterward. Valid bearer alone reaches reference REST; key A shares data across contexts and key B cannot access it. PostgreSQL/Control Plane outage leaves cache-off REST inference and local key authentication independent; history outage preserves resolved output. No browser or real provider.
  - Expected runtime: 3–15min within existing 30min runner ceiling.

### TEST-434: Deployed configuration identity

- Type: integration; verifies: REQ-430, REQ-434.
  - Location: `scripts/production-config.mjs`.
  - Command: `node scripts/production-config.mjs check --services harden-llm-gateway,harden-llm-web --expected-release "$HLLM_RELEASE_SHA"`.
  - Fixtures/mocks/data: Protected production descriptor and running Docker metadata; exact successful release-attempt SHA.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: Both selected services match intended source/image/config/network identity; checker exits nonzero for mismatch and emits no secrets.
  - Expected runtime: 5–30s.

### TEST-435: Deployed HTTP access and contract

- Type: e2e; verifies: REQ-422, REQ-425, REQ-434.
  - Location: `internal/smoke/deployed_reference_test.go`.
  - Command: `go test -tags=live ./internal/smoke -run '^TestDeployedReferenceHTTP$' -count=1`.
  - Fixtures/mocks/data: Deployment URL/release SHA plus protected process-env bearer; no browser session, stored user data or provider execution.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks. Required deployment variables: `HARDEN_LLM_ACCEPTANCE_URL`, `HLLM_RELEASE_SHA`, `HARDEN_LLM_TOKEN`; fail explicitly when absent.
  - Pass criteria: Health/readiness, Portal entry, unauthenticated denial, models, local request-validation errors and bearer-only reference status match contract. Forged ownership/hash/session inputs cannot select another scope. Read only the status figures (`history.rows`, `history.tableBytes`); read no stored records and make no paid inference or mutation. These checks do not claim a signed-in interactive workflow or exhaustive deployed cross-key isolation.
  - Expected runtime: 5–30s; each HTTP request bounded to 5s.

### TEST-436: History and artifact contract fixtures

- Type: static; verifies: REQ-426, REQ-432.
  - Location: `internal/testkit/reference_history_contract_test.go`.
  - Command: `go test ./internal/testkit -run '^TestReferenceHistoryContract$' -count=1`.
  - Fixtures/mocks/data: `test/fixtures/reference/{history-outcomes,history-pages,artifact-manifest}.json` and `api/openapi.yaml`.
  - Deterministic controls: section 7.3 defaults.
  - Pass criteria: Every fixture validates against OpenAPI; every history row carries a 64-character key hash; pages carry `page`, `pageSize`, `totalCount`; `RecordingOutcome.status` uses only the five section 8.4 values; every artifact object key matches `artifacts/<api_key_hash>/<run_id>/<artifact_id>` with the row's own hash; no fixture contains a caller-supplied object key, raw key or leak sentinel.
  - Expected runtime: 1–10s.

### TEST-437: Resolved recovery targets and attempt pricing

- Type: unit; verifies: REQ-421, REQ-423, REQ-429, REQ-432.
  - Location: `recovery_snapshot_test.go`.
  - Command: `go test . -run '^TestRecoverySnapshot$' -count=1`.
  - Fixtures/mocks/data: recovery-cross-profile.json; two local providers with independent credentials/rates; fixed clock and attempt budget.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: Cross-profile repair/escalation/rerun invokes the exact resolved targets; invalid targets dispatch zero calls; concurrent snapshots stay isolated; reported/unknown/zero/mixed pricing and cache ledgers remain correct.
  - Expected runtime: 10–30s.

### TEST-438: Atomic API-key profile snapshots

- Type: integration; verifies: REQ-423, REQ-427, REQ-432.
  - Location: `stateful/profile_integration_test.go`.
  - Command: `go test -tags=integration ./stateful -run '^TestProfilePersistence$' -count=1`.
  - Fixtures/mocks/data: Real PostgreSQL lease; key A/two client contexts and key B; full prompt/schema/repair snapshots, hidden templates, changed/deleted Git templates, repeated saves, credential replacement/CAS and concurrent draft writes.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: Every explicit Save inserts a distinct UUID/time/suffix with exact normalized inputs/settings and atomically saves any entered credential. No old profile update exists. List/detail/delete/hide are key-scoped; same-key contexts share rows and foreign keys cannot access/bind them. Git changes/reconstruction leave snapshots fixed. Credential/draft conflicts are explicit and failed transactions/probes preserve prior data. No connection remains held during probes.
  - Expected runtime: 30–120s with warm service pool.

### TEST-439: Cache and draft ownership

- Type: integration; verifies: REQ-424, REQ-427, REQ-431.
  - Location: `stateful/cache_draft_integration_test.go`.
  - Command: `go test -tags=integration ./stateful -run '^TestCacheDraftContract$' -count=1`.
  - Fixtures/mocks/data: Real PostgreSQL lease; two accepted key hashes, same-key concurrent clients with the same and different draft keys, fixed TTL clock, expired/corrupt cache, entered/deployment key changes, recovery-target credential changes, draft conflicts, pool size one and blocked provider.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: Cache/draft ownership is key-scoped; same-key clients share cache, share a draft only under the same draft key, and different keys share nothing. Changed provider credentials or recovery credential snapshots cannot hit prior credential-scoped entries. Expired cache cannot be served; at most 100 expired rows are physically deleted per owning write, preserving live/foreign rows. Corrupt records follow core validation. Draft CAS/outage differs from absence. No checked-out connection during provider waits; cache-off calls never acquire pool.
  - Expected runtime: 30–120s with warm service pool.

### TEST-440: Profile probes models and bundle replacement

- Type: integration; verifies: REQ-420, REQ-423, REQ-427, REQ-432.
  - Location: `stateful/profile_resources_integration_test.go`.
  - Command: `go test -tags=integration ./stateful -run '^TestProfileResources$' -count=1`.
  - Fixtures/mocks/data: Real PostgreSQL lease; local provider model discovery and explicit Save/import probes, old/new complete snapshots and full draft values, current encrypted same-hash bundle and wrong-hash/encryption-key/version variants.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: Explicit Save probes once/one attempt; failed/unknown probe never saves/retries. Model refresh returns a changed draft, performs no generation and leaves old snapshot intact. Bundle import is atomic for its matching API-key hash/encryption key and retains candidate probes before the transaction. Complete saved inputs/provenance and hidden preferences roundtrip; no incoming/environment/plaintext key or legacy schema export/import.
  - Expected runtime: 30–120s with warm service pool.

### TEST-441: Reference HTTP wire, streaming and storage isolation

- Type: integration; verifies: REQ-422, REQ-425, REQ-426, REQ-429, REQ-432.
  - Location: `internal/gateway/httpapi/reference_contract_test.go`.
  - Command: `go test -tags=integration ./internal/gateway/httpapi -run '^TestReferenceContract$' -count=1`.
  - Fixtures/mocks/data: OpenAPI-valid shared fixtures, httptest server over the real `stateful.Service`, real PostgreSQL and Garage leases, two synthetic bearer keys, local provider and SSE recorder; no reference session/Control Plane stub and no fake store.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: Every section 8.4 route validates one bearer/hash scope and its wire/error schema, including POST snapshot creation, GET full setup and status `history:{rows,tableBytes}` (present only when history is enabled; unavailable rather than zero on read failure). Missing/forged scope authentication dispatches zero providers. Key B gets 404 for key A's profiles, history and artifacts; the same key shares data across two clients; drafts are shared only under the same draft key. SSE emits one inference terminal followed by bounded recording outcome. Ordinary OpenAI contracts remain supported without login, scope headers or root identity fields.
  - Expected runtime: 10–30s.

### TEST-442: PostgreSQL history store

- Type: integration; verifies: REQ-426, REQ-427, REQ-429, REQ-431, REQ-432.
  - Location: `stateful/history_store_integration_test.go`.
  - Command: `go test -tags=integration ./stateful -run '^TestHistoryStore$' -count=1`.
  - Fixtures/mocks/data: Real PostgreSQL lease; history-pages.json rows for two hashes with equal start timestamps; 17 MiB boundary record; injected clock and commit hook.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: One insert per run records the server key hash; callers cannot choose hash or SQL. One fixed parameterized filter drives list/count/detail; numbered pagination/total counts preserve baseline navigation, tie order and the fixed `before` cutoff. Foreign-key run IDs return not found. Oversize is rejected as not saved without truncation. The usage query returns the key's own row count and a positive table size, unaffected by another key's rows. Unknown commit gets exactly one readback: found means saved; absent or failed means unconfirmed. A delayed real commit becomes visible after an empty readback without another insert; definite pre-COMMIT rejection alone means not saved. No polling, insert retry or secret-bearing error. The 17 MiB boundary record inserts and loads within the named history budgets (2 s insert, 2 s detail) on the real lease, and its measured durations are recorded.
  - Expected runtime: 30–120s with warm service pool.

### TEST-443: Artifact streaming and cleanup

- Type: integration; verifies: REQ-426, REQ-429, REQ-432.
  - Location: `stateful/artifact_integration_test.go`.
  - Command: `go test -tags=integration ./stateful -run '^TestReferenceArtifacts$' -count=1`.
  - Fixtures/mocks/data: Real Garage lease; key A/two clients and key B, baseline trace/parse-failure bytes/support bundle/media/digest, missing object/digest mismatch, history disabled/not saved and interrupted prefix delete.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: Bounded publication preserves baseline bytes/redaction. Same-key authenticated downloads return exact media/bytes even with history disabled or not saved; with all artifact settings absent, publication is skipped, status reports disabled and downloads return `artifact_not_found`; other keys and guessed foreign IDs cannot read/delete because the object key is built from the authenticated hash. No arbitrary Garage key or external redirect. Delete cleans only selected-scope objects and reports partial/unknown outcomes without replaying inference.
  - Expected runtime: 30–120s with warm service pool.

### TEST-444: Credential encryption and binding

- Type: unit; verifies: REQ-423, REQ-427, REQ-432.
  - Location: `stateful/credential_test.go`.
  - Command: `go test ./stateful -run '^TestCredentialContract$' -count=1`.
  - Fixtures/mocks/data: Fixed 32-byte encryption key/test nonce reader, baseline associated-data encoder using key hash/credential ID/origin, same-key clients, tampered hash/ID/origin/encryption-key/ciphertext and synthetic raw-key leak sentinels.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: One encrypted current format rejects associated-data mismatch and foreign-key decrypt. Plaintext HLLM/provider secrets are absent from read DTOs/bundles/diagnostics/metadata. Credential source is explicit with no environment substitution. Production uses crypto randomness; no legacy decryptor or raw incoming-key registry.
  - Expected runtime: 1–10s.

### TEST-445: Concrete effects composition

- Type: unit; verifies: REQ-421, REQ-431, REQ-432, REQ-435.
  - Location: `stateful/client_test.go`.
  - Command: `go test ./stateful -run '^TestClientComposition$' -count=1`.
  - Fixtures/mocks/data: Existing local provider fixtures; in-memory OTEL exporter; no database config; disabled/failed observer.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: NewClient composes one core engine without database/identity/history requirements; preserved span/metric names and safe attributes; provider/cancellation/retry behavior independent of telemetry outcome; no duplicate transport or root concrete SDK dependency.
  - Expected runtime: 10–30s.

### TEST-446: Reusable REST client contract

- Type: unit; verifies: REQ-425, REQ-428, REQ-429, REQ-432.
  - Location: `frontend/packages/harden_llm_ui/test/harden_llm_ui/client_test.exs`.
  - Command: `cd frontend/packages/harden_llm_ui && mix test --seed 104729 test/harden_llm_ui/client_test.exs`.
  - Fixtures/mocks/data: WEB-TEST-121; OpenAPI fixtures, private Req ownership, two host API-key contexts, same-key client sharing, caller scope-selector rejection, unknown errors and SSE terminal/recording/disconnect sequences.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: One client/decoder uses API key in Authorization only; no session/user/hash authority header. Complete snapshot detail/save fields and pagination decode to canonical schemas. Key A/B context secrets cannot leak across requests or to the browser; errors remain safe. Inference completion differs from recording completion and disconnect never retries work.
  - Expected runtime: 15–60s.

### TEST-447: Restored profile editor interactions

- Type: unit; verifies: REQ-420, REQ-423, REQ-428, REQ-432.
  - Location: `frontend/test/harden_llm_web/live/restored_profiles_test.exs`.
  - Command: `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_profiles_test.exs`.
  - Fixtures/mocks/data: WEB-TEST-122; baseline picker/editor/refresh/bundle/credential/repair events, complete snapshot and draft fixtures, two component instances with explicit IDs.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: Existing visual controls select full setups, edit the current draft and save each explicit action as a new UUID/date/suffix profile. Refresh updates draft only; failed Save retains inputs. Delete/hide/recreate/bundles/credential replacement and repair/rerun remain usable. Loading/restoring never saves or executes; secrets are not echoed and form logic has one source.
  - Expected runtime: 15–60s.

### TEST-448: Restored history trace and artifact interactions

- Type: unit; verifies: REQ-420, REQ-426, REQ-428, REQ-429.
  - Location: `frontend/test/harden_llm_web/live/restored_history_test.exs`.
  - Command: `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_history_test.exs`.
  - Fixtures/mocks/data: WEB-TEST-123; baseline numbered pages/counts and history/stat/trace/download bindings, key A shared by two contexts, key B and every history/error/cleanup state.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: Numbered pagination/counts and restore/delete/clear/stats/download controls retain baseline bindings. Reads obey key scope; same key shares history and foreign keys cannot access it. Restore fills complete recorded inputs/settings in the draft, without display-name lookup, foreign credentials or automatic Save/Run. Failed reads are not empty; recording preserves output and partial cleanup/unknown costs are explicit.
  - Expected runtime: 15–60s.

### TEST-449: Reference startup configuration and schema command

- Type: unit; verifies: REQ-424, REQ-430, REQ-431, REQ-432.
  - Location: `cmd/harden-llm-gateway/reference_config_test.go`; one Node case in `scripts/test/preview_policy_test.mjs`.
  - Command: `go test ./cmd/harden-llm-gateway -run '^TestReferenceConfig$' -count=1` ; `node --test scripts/test/preview_policy_test.mjs`.
  - Fixtures/mocks/data: Synthetic required api_key_envs metadata arrays plus environment maps and selected frontend key, missing/duplicate/empty names/values, forbidden literal secrets in JSON, storage/encryption/feature settings, absent Control Plane settings in Go, schema-command inputs, `deploy/preview/compose.yml` and the `previewSecrets()` output.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: One gateway metadata/env path validates without printing secrets or using scalar/session fallback. JSON contains only key environment names; frontend and gateway use the approved shared HARDEN_LLM_TOKEN value. No DB ping/key generation/identity request at construction. Disabled history differs from bad enabled config; one pool/independent status; schema command cannot reset broad data. The preview descriptor wires the gateway database, history flag and credential key, runs the one-shot `gateway-schema` service before `gateway`, and sets no artifact settings; `previewSecrets()` yields a standard-base64 32-byte credential key and existing values are kept.
  - Expected runtime: 10–30s.

### TEST-450: Production schema invocation precedes application startup

- Type: unit; verifies: REQ-430, REQ-432, REQ-435.
  - Location: `scripts/test/production_config_test.mjs`.
  - Command: `node --test scripts/test/production_config_test.mjs`.
  - Fixtures/mocks/data: Existing protected-descriptor fixtures and injected process runner; accepted gateway image/SHA, one-shot `gateway-schema` service, synthetic database credential sentinel, successful/failed schema exits, missing service and mismatched image/command, frontend-only and no-change apply cases. Extend the existing test file in P05.S01; do not create another deployer or runner.
  - Deterministic controls: Node only, process-owned fixtures, fixed command results; no Docker daemon, database, provider or browser. Assertions inspect calls to the production `runApply`, not a test-only implementation.
  - Pass criteria: For an apply selecting the gateway that needs changes, validated schema invocation runs exactly once before any application `up`, with the accepted gateway image/config/context; application startup retains `--no-deps`. Nonzero schema exit, missing service or mismatched image/command prevents application startup. Frontend-only and no-change apply do not start the schema service. No credential appears in command arguments or diagnostics.
  - Expected runtime: 1–10s.

### TEST-451: History recording outcome classification

- Type: unit; verifies: REQ-426, REQ-429, REQ-431.
  - Location: `stateful/history_test.go`.
  - Command: `go test ./stateful -run '^TestHistoryRecordingOutcome$' -count=1`.
  - Fixtures/mocks/data: Table-driven committed, definitely rejected and unknown commit outcomes with found/absent/failed readback. Created in P03.S01; exercise the one production classifier used by `insertHistory`.
  - Deterministic controls: Default-tag Go, no storage/provider or fake store; synthetic outcomes and fixed expected status strings.
  - Pass criteria: Confirmed commit or positive readback after unknown commit returns saved; definite pre-COMMIT rejection returns not_saved; unknown commit with absent or failed readback returns unconfirmed. Empty readback never proves rejection. TEST-442 separately proves the real delayed-commit boundary.
  - Expected runtime: 1–10s.

## 27. Reference closeout traceability corrections (2026-10-13)

TEST-420 additionally checks the current ownership header in
`self-hosted-go-stack-spec.md`; the audit retained its RED result before replacing
stale Phoenix-history guidance with ADR-HLLM-033/034. This adds the omitted
canonical document to the existing contract test, without changing its purpose.

TEST-048 includes `internal/testkit/integration_race_resources_test.go`,
`TestIntegrationRaceOwnsTimingResources`, a default-tag manifest assertion for
exclusive race-timing resources under ADR-HLLM-015. Its original TEST-008 comment
was a traceability error; TEST-008 remains retry classification/budget/backoff/
cancellation. No assertion, runtime task or deadline changes in this correction.
TEST-234's physical-network cases and TEST-450 schema ordering already pass;
TEST-434/435 remain explicit deployed identity/HTTP checks.


## 28. Shipped reference preset acceptance (2026-10-13)

### TEST-452: All shipped presets remain usable through reference Run

- Coverage gate: `fast`.
- Frontend cases: WEB-TEST-124, WEB-TEST-125.

- Requirements: the corrected REQ-422/425/428/429 workflows from the reference decoupling; issue #109.
- Tier: T0/T1/T2; no browser/provider execution, credentials or public network.
- Locations: `stateful/default_catalog_test.go`, `frontend/test/harden_llm_web/live/shipped_presets_test.exs`, `frontend/test/harden_llm_web/live/restored_profiles_test.exs`, `frontend/assets/test/client_core.test.mjs`.
- Commands: `go test ./stateful -run '^TestDefaultProfileCatalogParity$' -count=1`; `cd frontend && mix test test/harden_llm_web/live/shipped_presets_test.exs test/harden_llm_web/live/restored_profiles_test.exs`; `node --test frontend/assets/test/client_core.test.mjs`.
- Fixtures: the actual `stateful/presets.json`, unique private Req owners, existing validated terminal-event fixture and synthetic credentials/resolver. No copied production records.
- Assertions: fresh load has valid shipped schema and enabled Run; every preset is rendered/selectable and submits in both modes; invalid user schema remains disabled; focus shows all choices and typing filters; actual wrapper/preparer retain protocol/path/Agent/DeepInfra/redaction behavior; five finite leaf editors keep credentials, options, refresh, complete save and rebinding local.
- Pass criteria: every shipped preset submits in both modes (currently 40 presets/80 submissions), no unsupported shipped schema or leaked credential, no additional recovery recursion/engine/service, no weakened existing oracle.
- Expected runtime: 1–10 seconds for focused suites.
- Historical relationship: TEST-017 stays retired; its all-profile behavioral oracle is restored at this new owner/ID. See `docs/rca/reference-preset-schema-and-recovery-ui.md` for the coverage failure and correction.


### Composed port ownership regression (TEST-034/433)

- Locations: `internal/smoke/ports{,_test}.go`, `internal/smoke/harness_compose.go`, `internal/smoke/frontend_fixture_test.go`.
- Cheap command: `go test ./internal/smoke -run '^TestSmokeHTTPSPort$' -count=1`.
- Assertions: accept one assigned loopback port; reject zero/out-of-range/non-loopback/malformed/multiple publications. Both fixtures delegate binding/allocation to Docker; no released host-port probing.
- Distinct real boundary: canonical `go-compose` task starts the owned ingress, observes its assigned port and executes unchanged full-system assertions; cleanup verifies no owned resources remain. The opt-in browser fixture shares setup but is not launched by this correction.

### Corrective acceptance evidence

The matrix and port boundary passed in exact-source release 38439024276 attempt
1 (`739cfda1ea6a67fb7a2ee737e31d8342c58f36d0`), all 28 tasks exit 0 with no
cleanup issues. [The RCA](../../docs/rca/reference-preset-schema-and-recovery-ui.md#7-merged-and-deployed-acceptance)
links the original failed gate, full accepted report and production identity/
HTTP verification. Deployed reads confirm all 24 preset drafts match that tested
source; they do not substitute for the 48 enabled-submit/provider-prepare cases
or claim interactive browser/live-provider execution.

## 29. Preset selection and real gateway workflow qualification (issue #115)

### TEST-453: Workspace credential-free snapshot

- Coverage gate: `fast`.
- Locations: `frontend/test/harden_llm_web/live/shipped_presets_test.exs`.

Root hydration, preset selection and leaf notifications use one editor snapshot constructor. All shipped presets persist without an `apiKey` field, including empty keys; actual credential and scope guards remain enforced. Fast LiveView coverage asserts absence of draft errors and enabled runs.

### TEST-454: Real LiveView/gateway/PostgreSQL preset and recovery workflows

- Coverage gate: `release`.
- Frontend cases: WEB-TEST-126.
- Locations: `internal/gateway/httpapi/frontend_workflows_integration_test.go`, `frontend/test/harden_llm_web/live/gateway_workflows_test.exs`.

Explicit T3 task `frontend-gateway-workflows` starts the actual Go gateway with actual shipped metadata, isolated PostgreSQL and native protocol HTTP fixtures. It runs real LiveView events and the UI REST client without a successful-response stub. Every preset selects, saves, reads back and executes text/structured requests through actual provider parsers; page reload also verifies persisted state. Repair and escalation configuration must persist, resolve and execute finite targets under the global budget. The separate Mix build path prevents concurrent frontend compilation interference; application gateway configuration is the documented serial resource. Browser and public provider execution are excluded.

### TEST-455: Explicit live preset qualification

- Coverage gate: `manual`.

`go test -tags=live ./cmd/harden-llm-gateway -run ^TestLivePresetMatrix$` requires explicit authorization, protected startup configuration and a private report path. Each configured preset executes one text and one structured request with cache/history off and a one-attempt/no-repair policy. Retain failures and missing credentials as failures/unconfigured, not passing skips. The matrix proves real provider compatibility, while TEST-454 proves UI/HTTP/persistence/recovery behavior. Canary harness preflight mistakes are retained separately and never justify repeating already-dispatched calls.

Selection requires `HARDEN_LLM_LIVE_PRESETS=1` and a mode-0600 report named by
`HARDEN_LLM_LIVE_PRESET_REPORT`, with the deployment's protected startup sources.
`HARDEN_LLM_LIVE_PRESET_MODE=text` selects only text cases when those cases were
proven unexecuted; it never authorizes replaying a dispatched/ambiguous call.
No live canary is part of `make test-fast`, `make verify` or deployment smoke.

### Exact-source corrective acceptance

Application `ff5c968bc3c2263956fb1f946935395506347bfc` passed release 38455292756
attempt 1, all 29 tasks and no cleanup issues. TEST-454 covers every final
template's selection/persistence/readback and both modes, page reload and exact
six-model routing. The 80 real calls separately qualified all 30 configured
final templates while preserving ten failed candidates. TEST-435 and certified
catalog reads passed after deployment; the
[RCA](../../docs/rca/preset-draft-persistence-and-live-qualification.md#5-acceptance-and-rollout)
links source/images/reports and limitations.

## 30. Mixed recovery profile capabilities (#119)

### TEST-456: Shared UI reasoning and finite recovery conversion

- Coverage gate: `fast`.
- Frontend cases: WEB-TEST-127.

Actual rendered target selections switch mapped/unmapped presets at all five
positions and submit only supported reasoning. Pure package cases preserve
explicit supported selections and options, omit unsupported reasoning, normalize
restored drafts idempotently and resolve a fresh rerun from the generation setup.
Locations: `frontend/test/harden_llm_web/live/shipped_presets_test.exs` and
`frontend/packages/harden_llm_ui/test/harden_llm_ui/profile_reasoning_test.exs`.
Command: `make test-fast`. No provider, browser or database participates.

### TEST-457: Rejection field paths and safe diagnostic correlation

- Coverage gate: `fast`.

`stateful/recovery_reasoning_test.go` requires strict invalid-reasoning rejection
at all five paths and valid preparation when the unsupported field is omitted.
`internal/gateway/httpapi/reference_diagnostic_test.go` requires correlation IDs
and code in logs with no raw validation message. The independent client case
requires code/path/run ID in the visible validation message, without reflecting
the raw server message. Command: `make test-fast`.

TEST-454 additionally selects different capability profiles, reads saved state,
reloads and executes from that reloaded view, retaining the exact six-model
routing assertion. Command: `node scripts/run-test-tier.mjs --task frontend-gateway-workflows`.
The [RCA](../../docs/rca/mixed-recovery-profile-reasoning.md) records the original
production rejection, cheap RED/GREEN and the retained async-acknowledgement miss.

## 31. Workflow coverage assurance (#122)

### TEST-460: Runnable case closure and interaction completeness

- Coverage gate: `fast`.
- Location: `scripts/test/test_coverage_test.mjs`.
- Command: `node --test scripts/test/test_coverage_test.mjs`; `node scripts/verify-test-tiers.mjs`.
- Oracle: reviewed case headings declare their required gate; the existing manifest owns task selection. Every declared case has a selected source consumer with test declarations and every frontend case has its canonical description. Comment-only consumers, module-excluded Mix cases, removed registration/consumer/gate and excluded Go names fail. Manual paid cases remain explicit exceptions. Static ownership is not proof of runtime execution; retained task reports establish observed results.
- Matrix oracle: independently enumerate feasible domain assignments and required pairs/triples; missing rows, invalid values/constraints and missing permanent regression IDs fail. The current retry model retains all 36 required triples.
- Reviewed closure begins at TEST-452 and applies to all later case headings; historical cases retain their existing traceability gates. No second registry/scheduler or runtime combinatorial dependency.

### TEST-458: Strict native request oracle

- Coverage gate: `fast`.
- Locations: `internal/gateway/httpapi/preset_native_oracle_test.go`, `internal/gateway/httpapi/preset_native_fixture_test.go`, `internal/gateway/httpapi/preset_native_expectation_test.go`.
- Fast command: `go test ./internal/gateway/httpapi -run '^TestPresetNativeFixture' -count=1`.
- Independent assertions: malformed/trailing JSON, wrong method/path/query credential/header/model/reasoning/schema/options and unexpected fields are rejected. Reviewed native paths and declared setup values define expected requests; no production preparation function calculates expectations.
- Composition: TEST-454 applies this oracle to all 40 presets in both modes after each preset reload, then the exact six-stage recovery sequence. The matrix retains 80 generation calls, not a duplicate execution before/after reload.

### TEST-459: Public Run admission and owned async ordering

- Coverage gate: `fast`.
- Frontend cases: WEB-TEST-128.
- Location: `frontend/test/harden_llm_web/live/restored_workflow_test.exs`.
- Command: `cd frontend && mix test test/harden_llm_web/live/restored_workflow_test.exs`.
- Oracle: zero POSTs for blank profile/prompt, invalid structured schema, loading selection and unsaved endpoint changes; clearing each blocker permits one POST. A pending run rejects duplicate events and stale stream references. Out-of-order profile responses preserve the newest complete selection. Queued edits coalesce and reload the latest payload. Private Req owners and acknowledged barriers replace sleeps. Disabled HTML is not the admission oracle.

### TEST-461: Profile option and recovery capability partitions

- Coverage gate: `fast`.
- Frontend cases: WEB-TEST-129.
- Locations: `stateful/profile_options_test.go`, `frontend/packages/harden_llm_ui/test/harden_llm_ui/profile_reasoning_test.exs`, `frontend/test/harden_llm_web/live/shipped_presets_test.exs`, `internal/schema/schema_test.go`.
- Oracle: portable/native reasoning conflict has explicit independent witnesses; temperature nil/false/true and token aliases preserve the declared semantics, explicit false/zero/null and immutable defaults. All five positions preserve full/partial/absent map selections and unrelated options; inheritance compiles only fresh generation; unknown references remain invalid. The actual picker shows partial levels and submits explicit highest. Schema guards reject true and nonboolean additionalProperties with exact path/keyword.
- Commands: `go test ./stateful -run '^TestProfileOptionDecisionWitnesses$'`; existing package/LiveView tasks.

### TEST-462: Recovery decisions and cross-protocol finite interactions

- Coverage gate: `fast`.
- Locations: `internal/runtime/recovery_decisions_test.go`, `internal/gateway/httpapi/native_recovery_test.go`.
- Oracle: every stage stops immediately on success; independently disabled branches retain exact preparation/dispatch order and no semantic-repair wait. Injected time exercises just-before/equal/after retry deadline without sleeping and verifies prepared-operation reuse.
- Native domain: five source endpoint families × five target families × five recovery positions × syntax/schema failure × zero/one remaining target budget = 500 cases. Every feasible combination executes against native local TLS provider parsers with no database/public network; zero remaining budget means exhaustion before target, never invalid maxAttempts=0. Exact model order, credentials/options/schema, attempts, original selected target and actual result producer are distinct oracles. Small finite domains are exhaustive, avoiding a combinatorial runtime/generator dependency.
- Commands: `go test ./internal/runtime -run 'TestRecoveryDecisionWitnesses|TestRecoveryDeadlineIndependence'`; `go test ./internal/gateway/httpapi -run '^TestNativeRecoveryInteractions$'`.

### TEST-463: Concurrent SQL revision writers and probe window

- Coverage gate: `release`.
- Location: `stateful/concurrent_cas_integration_test.go`.
- Command: existing `go-integration` and `go-integration-race` tasks.
- Draft oracle: a test-owned PostgreSQL row lock holds two actual pool writers; observed SQL lock waits acknowledge overlap. Release admits exactly one complete winner, one revision conflict and one increment; foreign key scope is unchanged.
- Credential oracle: two candidates finish their revision reads and block in local provider probes with zero held SQL connections. Releasing both yields one winning ciphertext/revision, one conflict with losing snapshot rollback, and unchanged foreign credentials/profile count. No production data, arbitrary sleeps, success retries or new database service.

### TEST-464: Real client/gateway stream interruption

- Coverage gate: `release`.
- Frontend cases: WEB-TEST-130.
- Locations: `internal/gateway/httpapi/frontend_transport_integration_test.go`, `frontend/test/harden_llm_web/live/gateway_workflows_test.exs`.
- Command: existing `frontend-gateway-workflows` task.
- Oracle: close the actual owned HTTP connection during provider execution and after a complete terminal event, before recording confirmation. Provider-start and handler-exit acknowledgements bound cancellation. The real client/LiveView reports unknown inference only before terminal, retains terminal output afterwards and reports unconfirmed history. Read both exact records through the API and assert one actual provider dispatch and one PostgreSQL record per case; never synthesize recording success or replay inference.

### TEST-465: Logging writer flush-error propagation

- Coverage gate: `fast`.
- Location: `internal/gateway/httpapi/response_writer_test.go`.
- Command: `go test ./internal/gateway/httpapi -run '^TestStatusWriterFlush' -count=1`.
- Oracle: the actual logging writer forwards a closed-connection error through `http.ResponseController`, flushes a supported writer without changing an explicit status, and preserves `http.ErrNotSupported` for unsupported flushing. TEST-464 retains the actual cancellation/history boundary; this cheap root invariant cannot be replaced with a successful flush fake.

### TEST-466: Schema and request acquisition budgets

- Coverage gate: `fast`.
- Location: `stateful/schema_budget_test.go`.
- Command: `go test ./stateful -run '^TestSchemaAcquisitionBudget$' -count=1`.
- Oracle: the actual pool acquisition context uses 30 s for administrative schema initialization and 600 ms for request storage; an earlier caller deadline wins for both. A private pgx acquisition tracer captures the deadline and cancels before network I/O. No clock sleep, fake SQL success or database is involved. TEST-424 retains actual DDL, transaction-local SQL timeout, rollback and schema-version boundaries.
