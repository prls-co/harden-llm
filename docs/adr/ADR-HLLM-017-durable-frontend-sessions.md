# ADR-HLLM-017: Durable Single-Replica Frontend Sessions

- Status: Accepted
- Date: 2026-08-28
- Requirements: REQ-010, `SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001`
- Verification: WEB-TEST-004, WEB-TEST-005, Compose configuration, and hosted health probes

## Context

The Phoenix browser cookie intentionally carries only a random frontend session
handle. The corresponding Go bearer token was previously held in a process-local
ETS table. Replacing the frontend container therefore erased the handle-to-token
mapping even when the signed and encrypted browser cookie remained valid. Users
had to sign in after every frontend release.

The single-host Compose deployment has one Phoenix replica and already retains
named volumes across normal container replacement. The token must remain out of
the browser cookie, rendered HTML, LiveView session payload, logs, and telemetry.

## Decision

- `HardenLlmWeb.SessionVault` uses one supervised DETS table keyed by SHA-256
  digests of random frontend handles.
- DETS stores the backend bearer token only as authenticated encrypted ciphertext
  using a key derived from the stable Phoenix `secret_key_base`. Expiry remains
  an absolute timestamp and is checked on every lookup.
- Inserts, revocations, expiry cleanup, and graceful shutdown synchronize the
  DETS file before returning or closing it. Invalid or undecryptable entries are
  removed and treated as expired sessions.
- Production mounts the table at
  `/var/lib/harden-llm-web/session-vault.dets` on the retained
  `harden-llm-web-sessions` named volume. The service remains one replica; this
  volume is not a multi-node coordination mechanism.
- The browser session contract remains handle-only. The frontend still adds the
  bearer header only inside `HardenAPI` while constructing a server-to-server
  request.

## Security and operational consequences

This preserves the existing browser token-confinement boundary while allowing a
normal image rebuild/recreate to retain valid sessions. The session volume is
encrypted at the application layer and must be protected like other production
application data. It contains no user prompts, provider credentials, or domain
records. Losing the volume, rotating `HARDEN_LLM_WEB_SECRET_KEY_BASE`, or
intentionally running `docker compose down --volumes` invalidates frontend
sessions and requires a new login; durable gateway sessions and user data remain
independent.

The named volume must not be removed during a routine release. A future
multi-replica deployment needs a shared encrypted store with explicit locking,
backup, and failure semantics; it cannot mount this single-host DETS file from
multiple Phoenix nodes.

## Rollback and verification

Rollback restores the previous application image while retaining the session
volume. The previous ETS implementation cannot read the DETS records, so a
rollback to a pre-ADR-HLLM-017 image requires users to sign in again; it does
not delete the volume or affect gateway sessions.

`WEB-TEST-004` proves handle generation, encrypted-at-rest storage, expiry,
revocation, and valid-session recovery after a supervised vault restart.
`WEB-TEST-005` continues to prove LiveView authorization, backend-session
validation, redirect-on-expiry, and token non-disclosure. No browser test is
needed for the restart invariant because it is entirely server-owned.
