# Request status lookup

## 1. Submit normally and observe separately

Add one optional `X-HLLM-Tracking-ID` header with a fresh, canonical lowercase,
hyphenated, nonnil UUID to `POST /v1/responses` or
`POST /v1/chat/completions`. Normal request bodies, completion JSON, final-only
SSE and cache identity stay unchanged. Status lookup itself does not select or
restart inference/recovery.
Requests without this header allocate no tracking collector and issue no
tracking SQL. The header is never forwarded to the provider or written to diagnostic logs;
logs use server-generated call/trace correlation.

Keep the original POST connection open to receive its result. On another
connection, send `GET /v1/requests/{trackingID}` with the same HLLM bearer key.
The ID is a correlation identifier, not authentication or a replayable
idempotency key. Clients sharing that key can observe each other's IDs.
Different configured keys remain isolated.

For example, with a locally configured HTTP client holding the bearer:

```http
POST /v1/responses
Authorization: Bearer <HLLM key>
Content-Type: application/json
X-HLLM-Tracking-ID: a60f21da-604f-4fe3-a039-23382ec6b313

{"model":"<configured model>","input":"hello"}
```

While that POST is outstanding, use a second connection:

```http
GET /v1/requests/a60f21da-604f-4fe3-a039-23382ec6b313
Authorization: Bearer <same HLLM key>
```

A lookup can initially race body validation/registration and return `404`.
Retry GET while the original POST is outstanding; an absent record does not
prove that inference was never dispatched. Never automatically resubmit POST
based on a missing or stale observation.

## 2. Observation meanings

The canonical fields and enums are in `api/openapi.yaml` (`RequestStatus`).

- `running`: execution is being observed; `stage` is a bounded local phase.
- `waiting`: `retry_wait` means the runtime scheduled a retry; `admission_wait`
  means the configured CPA pool has not granted this live dispatch. Reasons
  distinguish throttling/window/capacity, account restriction, worker readiness,
  rollover, persistence and unresolved execution. `next_attempt_at` is a known
  eligibility floor, never a completion promise, and is null when unknown. A
  terminal refusal ends the POST rather than leaving it waiting; partial-output
  or post-entry transport uncertainty can be unconfirmed.
- `completed`: a valid, size-checked normal response was prepared. This does
  not prove the client received every byte.
- `failed` / `cancelled`: a known terminal failure or positively never-started
  cancellation. `outcome_confirmed` distinguishes certainty from success.
- `unconfirmed`: execution may have begun and its outcome is uncertain. Lookup
  does not restart it or assume the provider stopped.

`last_worker_heartbeat_at`, `last_progress_at` and `observed_at` have different
meanings. Heartbeats do not invent progress; polling does not advance either.
Snapshots coalesce at five-second intervals plus registration/finalization, so
short phases can pass between observations. A pending heartbeat older than
15 seconds is stale. After the execution deadline plus 30 seconds, missing
owner completion derives an unconfirmed observation without changing the row.
An eventual authoritative completion may supersede that derived observation.

All lookup responses, including errors, have `Cache-Control: no-store`.
Running/waiting observations include `Retry-After: 2` as polling guidance.
Unknown, expired and other-key IDs return the same `404 request_not_found`.
Malformed IDs/unsupported queries return 400; unavailable storage returns
503 `request_status_unavailable`.

## 3. Registration, retention and failures

A tracked request registers before invoking the core. A same-key retained ID,
including reuse on the other POST endpoint, returns 409 `tracking_id_conflict`
with `x-should-retry: false`, preventing the pinned SDK from automatically
retrying the conflict. Generate a fresh ID for independent work. Ambiguous
registration COMMIT requires positive readback of the exact invocation nonce;
failed/absent readback returns 503 without starting inference.

After dispatch, metadata write failure cannot replace a known response or
introduce inference retry. Lookup may subsequently show stale/unknown metadata.
When CPA admission is enabled, each actual provider retry/repair obtains its own
shared-pool permit. Unconfirmed managed dispatches send `x-should-retry: false`
so compatible SDKs do not silently repeat uncertain inference. Queued cancellation
before native entry is confirmed; canceling an entered provider path is uncertain.

Only bounded status facts are stored, never prompts, schemas, output, provider
credentials, arbitrary errors or reference history. No Garage artifact is made.

Metadata expires at the original execution deadline plus 24 hours. Each new
registration removes at most 100 expired rows in its own key scope, prioritizing
its requested ID. Idle-key rows may remain physically present until scoped
cleanup. An old invocation cannot mutate a later reuse of the ID.

Status lookup preserves the ordinary 60-second maximum and original disconnect
cancellation. It does not supply background execution, result retrieval,
shared admission, longer proxy/client timeouts or durable waiting. Durable jobs
and CPA admission remain a separate feature; polling cannot extend execution.

## 4. Operator rollout

Use the matching gateway build and existing HLLM database/pool. Run its explicit
`harden-llm-gateway schema` command with the normal protected database
configuration before enabling tracked callers. This adds one table/index,
preserves reference schema version 1 and rejects incompatible existing shapes.
Serving requests never perform DDL. No CPA upgrade/configuration change, new
service, database, worker or UI is required.

Drain active ordinary requests before rollback. An older binary ignores the
new table but does not serve the status contract. Rollout evidence must record
source SHA, API image identity, environment URL and authenticated HTTP checks;
a local test or PR is not deployment evidence.
