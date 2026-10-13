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
PR [#116](https://github.com/prls-co/harden-llm/pull/116) merged application source
`ff5c968bc3c2263956fb1f946935395506347bfc`. Main fast checks and all three CodeQL
analyses passed. Exact-source [release 38455292756, attempt 1](https://github.com/prls-co/harden-llm/actions/runs/38455292756/attempts/1)
passed all 29 tasks with no failure, timeout or cleanup issue. This includes
TEST-454 with all 40 templates/80 mode executions and exact six-model routing;
its hosted runtime was 256,251 ms. Cheap RED/GREEN and the full authorized live
success/failure matrix remain separate evidence.

Both images were built from a clean detached checkout of the certified source;
the checkout was removed. Immutable tags use `release-<full-source-sha>`.

| Component | Actual image ID | Final state |
| --- | --- | --- |
| Gateway | `sha256:97c134af86b9cceb91fde36eb666280c964de8d59855ffea5a5aa33e4bfdbedb` | Healthy, zero restarts; OCI version/revision and isolated version match source. |
| Frontend | `sha256:b3902de4b91baf3ea733fb9a7601bb8d874316a22d0de739ded1032b4f4f7f64` | Healthy, zero restarts; OCI version and both runtime release fields match source. |

The existing operator applied schema with exit 0 before starting only the two
apps. Scoped expected-release and full seven-service checks are equivalent.
Only application image/release identity changed in the descriptor. Protected
environment files, upstream metadata, deployment bearer, PostgreSQL/Garage and
telemetry containers/images, Caddy source/routing and user data are unchanged.
No saved-draft correction is needed: the rejected credential field was never
stored. Previous schema-compatible images and mode-0600 private checkpoints
remain available.

TEST-435 passed at `https://harden-llm.prls.co/`: Portal entry, health/readiness,
bearer enforcement, model discovery and reference status/scope guards. All 40
deployed template drafts match the certified definitions, inputs and bindings.
Configured counts are CPA 19, Google 5, Novita 1, OpenRouter 4 and Perplexity 1;
all 30 are among the live-qualified candidates. Ten need OpenAI/Anthropic keys.
Final discovery now returns 28 entries, with all 19 active CPA models present;
five previously failing old aliases are no longer advertised. Their deeper
upstream change is not attributed to this HLLM release. Four failed CPA text
candidates remain advertised and are deliberately absent from presets.

The [release report](../../plans/evidence/harden-llm/preset-workflow-qualification/release-accepted-report.json)
and [deployment record](../../plans/evidence/harden-llm/preset-workflow-qualification/deployment-acceptance.json)
retain final acceptance. Deployment made no paid inference call. Browser layout
and interactive Portal behavior were not checked. Later docs-only revisions
do not rebuild or relabel these images.

## 6. Scope changes, limitations and next work

The added T3 release task closes the untested UI/HTTP/storage/execution boundary;
it does not replace cheap tests or introduce another runner. The shared snapshot
removes duplicate credential projection logic. Disabling Run for unconfigured
templates makes the existing credential requirement explicit, without changing
markup or styles. No recovery engine, database, identity model, compatibility
reader, fallback or data reset was added.

The user requested current CPA presets and authorized live qualification. The
catalog therefore includes newly qualified CPA models and removes ten observed
failures rather than presenting unverified availability. Original failures and
the pre-dispatch canary input mistake are retained, not marked green. This is a
dated test of one short input per mode, not proof of all options, prompts or
future availability.

The HLLM corrective scope is complete. Investigate remaining upstream causes in
[CPA #5](https://github.com/prls-co/CLIProxyAPI-setup/issues/5), then explicitly
qualify repaired models before restoring presets. Supply OpenAI/Anthropic keys
if those templates are needed. Browser checks remain an optional separate opt-in.
