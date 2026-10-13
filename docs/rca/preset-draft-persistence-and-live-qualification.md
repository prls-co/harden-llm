# 1. Preset draft persistence and live qualification

Issue [#115](https://github.com/prls-co/harden-llm/issues/115), 2026-10-13.
Affected deployed source: `739cfda1ea6a67fb7a2ee737e31d8342c58f36d0`.

## 2. Sol selection failure and missed coverage

Production rejected `POST /api/v1/state` with HTTP 400 / `draft_invalid` before
inference. `state_from_profile_draft` copied the editable form into persistence,
including its blank `apiKey`. The backend correctly forbids credential fields
even when empty. The profile widget's normal notifications already removed the
field, but hydration/selection used another conversion path. One shared
`ProfileForm.draft_snapshot` now owns the credential-free editor projection for
hydration, selection and root/leaf notifications. Draft security is unchanged.

The previous all-preset LiveView test returned success for every draft POST.
OpenAPI describes a generic payload object and cannot establish the backend's
secret/storage rules. The Go matrix called resolve/Prepare rather than the full
Run method, provider parser or persistence. Those checks were useful within
their limits; they were insufficient evidence of working preset workflows.
The new cheap credential-field assertion fails on the affected implementation.

## 3. Coverage at the actual boundaries

TEST-453 strengthens the shipped-catalog UI test: selection/save has no draft
error or credential field; fresh Run and all text/structured submissions remain
covered. Unconfigured templates remain editable and disable Run until a key is
saved. Negative credential/scope guards remain in the Go storage owner.

TEST-454 / WEB-TEST-126 is the explicit `frontend-gateway-workflows` T3 task.
Real LiveView events use the actual UI HTTP client, Go gateway, PostgreSQL and
native provider wire fixtures. It proves selection, CAS save/readback, reload,
both execution modes, repair/escalation configuration and the finite recovery
chain. The Go fixture additionally asserts the exact observed provider model
sequence. It is separate from Go-only integration and the offline fast loop;
no browser or public provider participates. A process-owned Mix build path and
ephemeral HTTP port prevent compilation/listener collisions. The host API
application settings are a machine-checked serial resource; Req ownership
allowance is only used when the selected transport is actually Req.Test.

TEST-455 is an explicitly authorized live canary, outside release/fast gates.
It uses deployment connection metadata and the same stateful engine, with
cache/history disabled and exactly one generation attempt per mode. No real
database, user history, session or browser is used. Outcomes are written to a
private report and failures are retained; provider dispatch is never replayed
automatically.

## 4. Current CPA discovery and actual qualification

Authenticated CPA discovery through `/v1/models` returned 33 entries. Five
image-generation entries are outside the text/structured proxy contract.
The 28 text candidates were checked alongside the other 12 configured presets.
The live matrix executed 80 provider calls, one per mode per candidate.
Thirty configured presets passed both modes, including 19 CPA entries.
Ten candidates failed at least one mode and are removed from active Git presets,
with original outcomes retained rather than marked passed. The catalog now
contains 40 templates: 30 configured/qualified and ten awaiting OpenAI/Anthropic
credentials. This is a dated qualification, not a guarantee against future
provider outages or credential changes.

Failures include CPA 503s for older aliases, `model_cooldown` 429s, 400/500
provider failures, and Novita Flash 404. The deeper CPA cause belongs to its
owner; the matrix does not invent a source repair or retry those calls.
Prices are copied only where the read-only utility registry provides them;
unknown prices remain absent, rather than falsely zero. Discovery and registry
provenance are retained beside the full success/failure summary.

The first canary invocation mistakenly attached the structured schema to text
requests. The backend rejected all 40 text cases before provider dispatch.
That harness error is preserved privately. After correcting the input, only
those unexecuted text cases ran; already-dispatched structured cases were not
repeated. This did not weaken text validation or conceal a provider failure.

## 5. Acceptance and rollout

Evidence: `plans/evidence/harden-llm/preset-workflow-qualification/`.
Cheap RED/GREEN, the fast report and live qualification are retained. The final T3 task passed with all 40 templates/80 mode executions and exact six-model recovery routing. Release/source/image/public acceptance is recorded in #115 after completion.
Promote exact certified gateway/frontend images through the existing scoped
operator; keep credentials, shared owners, data and routing stable. Browser
layout remains outside this browser-free scope.
