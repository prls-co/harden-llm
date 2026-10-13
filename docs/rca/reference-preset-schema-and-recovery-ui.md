# 1. Reference preset, schema and recovery UI regression RCA

- Date: 2026-10-13; production follow-up [issue #109](https://github.com/prls-co/harden-llm/issues/109) to issue #100.
- Affected application: `bbdb5d77bc76424804427ca30a7bf9ddc453d0cf`; operator/main audit revision `3b7ec9367d7c0148eb1ff7ea95181299d359ed3a`.
- Preset/UI baseline: PR #95, `754690c8d0890c664d69fbdf526b16b3e5601060`.
- Corrective implementation/release: tracked by #109; acceptance is recorded below and in its final issue comment.

## 2. Causes and observable failures

### 2.1. Structured Run on fresh load

P02 converted provider definitions into complete preset snapshots. All 24
`stateful/presets.json` templates supplied `inputs.schema: {"type":"object"}`.
`WorkspaceLive.hydrate/1` selects the first preset and merges its complete inputs
over the valid workspace example. The supplied object replaces the schema with
`properties`, `required` and `additionalProperties`; it is an invalid contracted
schema. `WorkspaceSchema.check/1` correctly reports the missing properties, and
`run_disabled?/3` correctly disables structured Run. Turning structured output
on therefore exposes invalid shipped data, not a broken retry/repair engine.

The shipped schema, shorthand and example prompts now match the restored PR #95
joke/explanation example. Presets omit portable reasoning when their definition
has no reasoning map. `ValidateCatalog` uses the existing Go schema normalizer
for structured template inputs; it adds no second schema validator. User drafts
may still contain incomplete/invalid work, and actual invalid schema continues
to disable Run and fail core validation.

### 2.2. Presets appear to contain only Luna

The live authenticated API returns all 24 preset IDs. They match the last
pre-rewrite catalog at PR #95. The preceding October 4 catalog has 29 entries,
including Perplexity definitions later deliberately retired by `f094bdc`; it is
not a better restoration source. Earlier CPA GPT-5.4 definitions were retired
by `38b56fd`. No catalog model/pricing/options definition is replaced with an
older or guessed value in this fix.

`SearchableCombobox.onFocus` filtered by its committed value, so opening the
picker hid every other preset. Focus now selects the current value and opens
an unfiltered list; subsequent typing performs the existing search. Saved
profile names participate in search as well as their IDs/provider/model fields.

Production additionally retained only the CPA startup connection. The approved
shared `.env` still contains Gemini, Novita, OpenRouter and Perplexity keys, but
the deployment environment allowlist and Compose graph passed only CPA. The
allowlist/Compose boundary now passes the documented optional provider keys to
the gateway. The operator explicitly adds matching connection metadata for
existing credentials. A missing provider credential remains unconfigured until
explicitly supplied; no key is guessed, copied from another provider or embedded
in Git. The minimal CPA example remains a valid basic deployment.

### 2.3. Disabled and difficult recovery profile editing

The baseline renderer disabled model/reasoning/config folds when a recovery
target inherited generation, and hid its drawer even when open. Its shared
`profile_editor` additionally disabled API/base URL, key, refresh, pricing,
New/Delete controls for every target. Thus sharing the renderer did not provide
a usable complete editor. Save built a partial setup from list metadata and
did not rebind the leaf to the newly created immutable snapshot. Host echoes
could also reset folds after a field edit.

The root and all five leaves now reuse the same editor, form conversion,
options normalization and existing REST client. Each leaf keeps its own complete
loaded draft, staged key, folds and async snapshot. Opening an inherited editor
preserves inheritance; an explicit invocation edit creates a profile target.
Definition/credential edits require explicit Save before execution. Save creates
a complete new snapshot and rebinds only that leaf. Late async responses preserve
newer edits. Refresh/New/Delete/credential events carry the fixed node path;
root/catalog actions retain their existing ownership. Bundle operations remain
host-owned. The UI retains existing layout/classes; no second profile service,
HTTP client or provider execution engine is introduced.

The finite role matrix remains: original generation may repair/rerun, rerun may
repair but cannot rerun again, and repair stages cannot create more recovery.
Retry/backoff/deadline remain shared. The Go wrapper resolves the same five
references once and never follows a referenced profile's recovery policy.

## 3. Why the tests missed it

The P04 restored workflow tests used `test/fixtures/reference/workspace.json`,
whose schema was valid, rather than the embedded production catalog. They
submitted the form without asserting that its rendered submit button was
enabled. Programmatic `render_submit` can bypass a disabled button, so successful
mock execution did not establish usable fresh-load structured Run.

The historical `TestDefaultProfileCatalogParity` (TEST-017) exercised every
preset and both request modes, but was retired during the profile-free rewrite.
Its behavioral oracle was not moved back when the stateful wrapper returned.
Catalog validation checked JSON-object shape, not the contracted schema. The
positive two-profile fixture also contained the same invalid placeholder.

The repair test explicitly selected an override before opening its drawer;
it did not exercise inherited editing or complete snapshot/save/key/refresh
behavior. Pure combobox tests covered matching/selection, but did not prove
that focus opens all choices. These were coverage gaps; passing CI and HTTP
health/auth did not establish the missing interactions. The earlier release
remains historical evidence within its recorded boundaries.

## 4. Corrective verification

- TEST-452 / WEB-TEST-124: load the actual shipped JSON; fresh load and all 24
  presets submit in text and structured modes (48 submissions), after checking
  the rendered button is enabled and bound to the form. Validate wire payloads
  against OpenAPI and schemas against the existing UI checker. Finally enter
  the invalid bare-object schema and assert Run is disabled and no call occurs.
- TEST-452: successor to the historical all-profile prepare matrix at `stateful/`.
  Use shipped inputs, actual wrapper resolution and the real provider preparer
  with synthetic credentials/resolver. Preserve protocol/path, Perplexity Agent,
  DeepInfra routing/no-fallback and credential-redaction assertions. No provider
  execution or public network is used. TEST-017 remains historically retired.
- WEB-TEST-122/125: inherited full editing, option persistence/fold stability,
  complete create-on-save, leaf-only rebinding, staged-key non-echo, exact leaf
  refresh, all five editor positions and finite role restrictions.
- WEB-TEST-046: focus opens all presets; typing still filters; selection,
  keyboard, escape and blur assertions remain.
- TEST-408: optional documented credentials reach only the approved deployment
  environment; unrelated/retired secrets remain excluded. Native Compose
  serialization tests exercise the changed boundary without starting services.

The positive fixture's bare object is replaced with a valid `ok:boolean` schema;
its existing ownership, identity, isolation and snapshot assertions stay intact.
Actual RED evidence reproduces startup/24-schema, focus, inherited-config and
environment failures. Early compilation/selector preparation mistakes are not
counted as behavioral RED. Local fast/focused checks, exact-source hosted release,
merged CI and final deployment identities are recorded in #109 and accompanying
evidence. Browser layout, interactive Portal and live-provider inference require
the existing explicit opt-in; none is implied by these checks.

## 5. Existing draft correction and rollout

One production draft was found containing the exact shipped bare-object schema
(out of one total draft). Updating Git templates does not overwrite saved drafts.
After the new application is certified, correct only rows whose selected ID is
a shipped preset, schema equals that placeholder and shorthand still equals the
original joke/explanation example. Replace the schema and matching editor-input
schema with the canonical preset schema; increment draft revision and update
its timestamp. Preserve all other fields. Recheck the affected count in the
transaction and record changed-row count only; do not export user prompts.
This is a one-time operator correction, not a runtime compatibility reader or
an automatic reset of all state/history/settings.

Deploy the exact certified gateway/frontend images through the existing scoped
operator. Schema version remains 1; no new database or service is created. The
existing deployment bearer, encryption key, shared owners, Caddy routing and
other products' resources are preserved. Verify actual image/runtime identities,
full configuration equivalence, public HTTP/model discovery, preset count and
configured credential statuses. Do not dispatch paid calls as deployment smoke.


## 6. Release harness failure and correction

Exact-source release `38435523111` attempt 1 (`d79116c`) failed during Compose
startup: Caddy could not bind the previously selected host port. Twenty-five
release tasks passed; frontend release and nested backend verification were
cancelled. Cleanup reports have no errors/warnings; the failed report is retained.
The application UI regressions were not the failing boundary.

The harness used listen-on-zero then closed the listener before Docker bound
it, leaving a real allocation race. Both composed fixtures now request Docker
ports zero and start their owned ingress first. The harness reads the actual
HTTPS publication, configures identity/public URLs with it, then starts the
remaining services. The same Caddy stays running; Docker owns both ports from
allocation through teardown. Readiness timing includes ingress allocation;
commands/oracles, budgets and cleanup ownership remain. The cheap port parser
checks publication shape/range; actual Compose proves port ownership/startup.
No retry loop, port-range reservation service or browser launch is introduced.

## 7. Merged and deployed acceptance

PRs [#110](https://github.com/prls-co/harden-llm/pull/110) and
[#111](https://github.com/prls-co/harden-llm/pull/111) produced main application
source `739cfda1ea6a67fb7a2ee737e31d8342c58f36d0`. Its
[release run 38439024276, attempt 1](https://github.com/prls-co/harden-llm/actions/runs/38439024276/attempts/1)
passed all 28 tasks with no preflight/task failure or cleanup error/warning.
Main fast CI and CodeQL passed. Both production images were built from a clean
detached checkout of that exact certified source; the build checkout was removed.

| Component | Actual image ID | Deployed state |
| --- | --- | --- |
| Gateway | `sha256:4635359331af363b785a3d8d56921353a72bd7e1f9709f3ee26274cd40ee7039` | Healthy, zero restarts, version/OCI release equals source SHA |
| Frontend | `sha256:89d57142bc424e2b8f08180add98676cbe8aba688b581894ecc318129c135ac4` | Healthy, zero restarts, OCI/environment release equals source SHA |

Scoped application and full seven-service checks are equivalent. The existing
operator applied schema before the two apps with exit 0; no schema change was
introduced. The descriptor changed only application image/release identity.
Nonsecret upstream metadata now binds the four existing optional credentials.
Protected environment files and the incoming deployment bearer are unchanged;
provider keys match approved settings and are absent from the frontend. Shared
PostgreSQL/Garage container/image identities and Caddy source/routing are unchanged.

TEST-435 passed against `https://harden-llm.prls.co`: health/readiness, Portal
entry, bearer rejection, model discovery and reference status/scope guards.
Authenticated reads of all 24 preset drafts match the certified catalog's full
definitions, inputs, schemas and credential bindings. Fourteen presets are
configured: CPA 2, Google 5, Novita 2, OpenRouter 4 and Perplexity 1. Five OpenAI
and five Anthropic presets remain visible and unconfigured because their keys
were not supplied. Configured status proves binding, not provider inference.

The precise one-time correction changed one known-default draft, incremented
its revision and preserved all other fields; no matching faulty draft remains.
Five real PostgreSQL fixture cases passed before mutation. There were zero
saved profiles, so no immutable saved snapshot required correction. Only counts
and preservation results are exported; no user prompt or stored record is logged.

The [verification record](../../plans/evidence/harden-llm/reference-ui-regression/verification.json),
[release report](../../plans/evidence/harden-llm/reference-ui-regression/release-accepted-report.json)
and [deployment record](../../plans/evidence/harden-llm/reference-ui-regression/deployment-acceptance.json)
retain the result and original failed gate separately. This completes #109's
browser-free corrective scope. Browser layout, interactive Portal and real
provider inference were not tested. Optional next work is supplying the missing
provider credentials and explicitly commissioning those acceptance checks;
none requires another architecture or API change.

## 8. Documentation closeout: unstarted GitHub scan

PR #112 merged the release record at `b3a4f52a913c531cc20c19615a5b0eee97cbb7cc`.
Its tree equals the PR tree that passed all three CodeQL analyses. The subsequent
[merged-main CodeQL run 38442596076](https://github.com/prls-co/harden-llm/actions/runs/38442596076)
reported completed/failure while its JavaScript job remained queued with no
runner, steps, annotations, completion or conclusion. Go and Actions passed;
there is no failed JavaScript execution log. This is an unstarted scan, not an
observed application test/security finding. Its deeper GitHub cause is unknown.

GitHub rejected job/run restarts and reports the suite/runs as non-rerequestable.
Reapplying `state=configured` returned the old August setup run, with the full
configuration unchanged. No successful scan is inferred from those rejected/
idempotent requests, and the old setup result is not current-source evidence.
The [redacted incident record](../../plans/evidence/harden-llm/reference-ui-regression/codeql-prestart-incident.json)
and [issue #113](https://github.com/prls-co/harden-llm/issues/113) preserve this
independent CI incident. This documentation update records it without changing
the application, scan settings or test oracle. A later successful scan on a new
documentation revision does not prove the failed run was repaired. The exact
28-task application release and production checks above remain valid within
their recorded scope; an unstarted scan is never recorded as green.
