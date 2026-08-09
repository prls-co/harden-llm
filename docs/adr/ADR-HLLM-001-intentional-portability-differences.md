# ADR-HLLM-001: Intentional Portability Differences

- Status: Accepted
- Date: 2026-07-13
- Requirements: REQ-002, REQ-005, REQ-008, REQ-018, REQ-020

## Context

The JavaScript source combines portable LLM semantics with Firebase-specific
storage and a less restrictive endpoint/credential model. Exact byte parity for
those infrastructure details would violate the target security specification.

## Decision

Preserve exact parity for request semantics, provider normalization, retry,
schema, cache identity, usage, pricing, profile graphs, and diagnostic fields.
Accept only these explicit projections:

- compare the JavaScript direct return to `Result.Output`; Go adds normalized
  call metadata from the same call record;
- require HTTPS profiles, send every provider credential in an origin-bound
  header (including Gemini), validate every DNS answer on every request, pin
  dials to those answers, reject redirects, and strip unsafe headers;
- permit private endpoints only through exact host/CIDR exceptions; this does
  not permit redirects, mixed safe/unsafe DNS answers, TLS bypass, or proxy
  inheritance;
- replace the source GCM envelope with a versioned key ID and authenticate the
  schema version, algorithm, key ID, owner, credential, and normalized origin
  as AAD while preserving AES-256-GCM round-trip and tamper behavior;
- replace Firebase/GCS attachment locations with owner-scoped Garage references;
  generated unique artifact IDs and Postgres object-key uniqueness are the
  cross-replica immutability boundary, while the Garage adapter additionally
  preflights writes and serializes same-key writes within one process because
  Garage v2.3 does not enforce S3 `If-None-Match` on `PutObject`;
- omit unredacted raw provider payloads from persisted trace and diagnostic
  projections;
- replace the package-global source model registry and pricing overrides with
  owner-managed profiles, model discovery, and profile pricing snapshots;
- replace Firebase query helpers with owner-scoped PostgreSQL repositories and
  the versioned REST/OpenAPI resource contract;
- replace Firebase/process-log telemetry documents with one OpenTelemetry
  pipeline plus redacted domain traces, preserving portable usage, attempt,
  cache, status, and correlation semantics;
- preserve the source retry identity for coded `empty_response` failures and
  the proven OpenAI Responses `provider_retry` directive, exposing only bounded
  code/type/request-ID metadata in Go attempts and traces; raw stream events
  remain omitted.

Every affected fixture uses projection or intentional-difference parity and
names this ADR. No other mismatch is permitted by this decision.

## Consequences

Ciphertext and storage URLs are not cross-runtime byte compatible. Provider
profiles using HTTP, URL credentials, or query/fragment configuration must be
normalized before import. Migration must decrypt through an authorized source
process and re-encrypt/write through the target adapters. TEST-006, TEST-014,
TEST-018, TEST-019, and TEST-040 verify the allowed differences and their
security properties. TEST-015, TEST-016, TEST-017, TEST-020, TEST-024,
TEST-028, and TEST-031 verify the profile, pricing, query, and telemetry
projections. The in-process Garage key lock is not a distributed lock; callers
must continue to generate single-use artifact IDs and commit the unique object
key only after upload.
