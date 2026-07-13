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
- add DNS/IP/redirect/TLS/header/origin checks before outbound provider calls;
- replace the source GCM envelope with a versioned key ID and owner, credential,
  and origin AAD while preserving AES-256-GCM round-trip and tamper behavior;
- replace Firebase/GCS attachment locations with owner-scoped Garage references;
- omit unredacted raw provider payloads from persisted trace and diagnostic
  projections.

Every affected fixture uses projection or intentional-difference parity and
names this ADR. No other mismatch is permitted by this decision.

## Consequences

Ciphertext and storage URLs are not cross-runtime byte compatible. Migration
must decrypt through an authorized source process and re-encrypt/write through
the target adapters. TEST-006, TEST-014, TEST-018, TEST-019, and TEST-040 verify
the allowed differences and their security properties.
