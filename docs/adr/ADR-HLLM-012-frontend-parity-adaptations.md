# ADR-HLLM-012: Self-Hosted Frontend Parity Adaptations

- Status: Accepted
- Date: 2026-08-18
- Requirements: REQ-007, REQ-011, REQ-012, REQ-018, REQ-019 and `SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001`
- Verification: WEB-TEST-031 through WEB-TEST-037, `make verify`, `make test-compose`, and the pinned Phoenix/browser gates

## Context

The post-certification `utility-llm` frontend audit at source revision
`5c0309e` (`0.15.0`) found missing behavior in the initial Phoenix console:
advanced profile options, write-only credential staging, custom profile values,
schema and retry controls, actionable workspace history, richer trace/resource
controls, and persisted UI state. The source also uses Firebase/Firestore,
browser provider calls, a Downshift combobox, and offset/page-number history
pagination. Those implementation details are not valid for Harden-LLM's
self-hosted architecture.

## Decision

- Keep `ProfilesLive` as the single owner of the full profile editor. Workspace
  model rows use a native editable datalist and deep links rather than copying
  a second nested editor into every workspace fold.
- Keep profile editing and profile-delete confirmation in the normal document
  flow. `New profile` expands an inline editor while the profile cards remain
  visible; credential, options, retry/repair, and pricing controls are nested
  folds. A viewport overlay is not used for these information-dense actions.
- Use a compact vertical studio stack and profile cards rather than a wide
  profile table or a workspace side rail. Emoji labels are presentation
  affordances only; every action retains an accessible name and stable test
  selector.
- Use a non-reserved LiveView payload key such as `phx-value-open` for browser
  fold state. `phx-value-value` collides with a button's native empty `value`
  property in the LiveView browser serializer, so server-rendered event tests
  alone are not sufficient for this interaction contract.
- Keep profile defaults, retry policy, repair escalation, pricing, credentials,
  and provider execution backend-owned. The browser never sends a raw stored
  credential or creates a second provider execution path.
- Use the existing cursor/limit history contract with page-size selection and
  load-more. Do not add offset or arbitrary page-number navigation only to
  reproduce the source quick-jump control.
- Represent source trace/stat widgets through the composed Workspace and
  History LiveViews plus authenticated same-origin artifact/trace routes.
  Firebase auth, Firestore, signed Firebase URLs, Blob-download code, and
  browser provider calls remain excluded.
- Treat these as semantic UI adaptations, not compatibility shims. A future
  REST contract change that requires offset pagination or another profile owner
  requires a new review and ADR.

## Consequences

The translated controls are functional through one Phoenix/Go/OpenAPI path and
retain write-only credentials, redaction, owner authorization, and Postgres /
Garage ownership. The visual structure is not byte-identical to React, and the
utility quick-jump interaction is intentionally absent. The 2026-08-22
presentation amendment brings the layout topology closer to utility-llm:
information-dense controls stay in one page and expand in flow instead of
covering the surrounding content. The parity inventory is the
requirement-level record: `docs/utility-llm-frontend-parity-inventory.md`.

The Tempo smoke harness also normalizes a one-nibble leading-zero omission in
Tempo's returned trace ID. This is a test-observation normalization only; it
does not change production trace IDs, timeouts, or telemetry routing.

The 2026-08-22 fold-event correction adds a real browser assertion to the
parity gate. The deterministic LiveView test remains useful for server
behavior, while the browser workflow verifies that the serialized event
actually carries the requested open/closed state.
