# Real provider qualification and fixture retirement

## 1. Accepted scope

On 2026-11-08 the user explicitly rejected extending issue #151's scripted
provider fixture and authorized actual Query Analytics → NER/Clustering → Harden
→ provider qualification, existing cache reuse, and retirement of the simulator.
This supersedes the issue's original request for Responses scenario activation.
Small deterministic tests protecting concrete production boundaries remain.
The later user instruction to finish production readiness authorized main promotion and the scoped gateway/worker rollout. Browser testing remains excluded.

## 2. Actual defect found

The initial normal-gateway/CPA run completed generation and NER but failed
Clustering. Its original public parent failure was `child_operation_failed`;
Temporal retained Clustering's definitive `harden_operation_failed`. No
ambiguous inference was replayed. A separate diagnostic run added error capture;
the initial capture query used the wrong history timestamp column, which was
corrected to `started_at` before further instrumentation.

A focused real request with the Clustering schema reproduced HTTP 400. A
temporary external Go overlay captured only the synthetic request body and
upstream error body privately, without authorization headers. The payload had
both `max_output_tokens: 4096` and profile default `max_tokens: 16000`. CPA's
body was `{"detail":"Unsupported parameter: max_tokens"}`. A direct native
request without the legacy alias succeeded. The overlay is not shipped.

`normalizeResponsesTokenOptions` removed an alias only when it also populated
the native field. With an explicit native override, it leaked the profile's
unsupported legacy field. TEST-533 failed before the fix for native-plus-alias
and two-alias inputs. The fix always removes legacy aliases after selecting
the native limit. It preserves native precedence, legacy-only conversion and
the previous alias precedence, without changing caller-owned options.

The old edge-parity assertion deliberately preserved this inherited source
behavior. It is corrected because the real provider rejects it. Production
prompts, extraction schemas, recovery policy and accounting are unchanged.

## 3. Live acceptance

The consumer's explicit `integration && live` gate builds the normal gateway
command with the existing connection example, a synthetic incoming key and
privately supplied CPA credential. It uses approved existing CI PostgreSQL,
Temporal and Garage with unique schemas, task queues, child processes and
owned object cleanup. NER remains cache-off; QA retains its production cache.
No application dataset, production bearer or browser session is copied.

After the adapter fix, the whole workflow completed. The first acceptance
input mixed GitHub's platform/company meanings and failed the newly authored
exact target-name assertion. That failed run is retained. The final input
explicitly asks for the two companies Microsoft and Alphabet, preserving the
same target-inclusion, text-support and lineage assertions without changing
owner prompts or policy.

The completed company run retained two entities/two clusters, actual NER
execution evidence, persisted report bytes and exact exported observation
lineage. Duplicate admission and repeated export reused completed work. An
independent identical request proved SQL cache miss then hit, with provider
attempt counts one then zero. See the dated evidence directory and receipt.
Reported diagnostic usage/cost is not a provider invoice; missing NER usage
must remain unavailable.

## 4. Code retirement and retained coverage

Remove `internal/testfixture/reference-gateway`, the reference scenario/stats
package and its standalone SQL test tier. TEST-485/TEST-486 become historical
records in Git, not current acceptance. TEST-482 retains the actual
gateway/client recovery, rejection, model identity, no replay, native search
and exact POST/provider assertions through a small local HTTP boundary and
the existing native writer. The fixture's active-operation teardown race
ceases to exist with its matcher removed.

The consumer removes its simulator-dependent multi-service fault campaign.
Its assertion-disposition table in `docs/NER-V4-MIGRATION.md` names retained
auth/revocation/cancellation, unknown-commit/no-replay, checkpoint, real SQL,
real Temporal and native process tests. This retirement is explicit: a live
happy path does not claim native worker-death or every failure-boundary proof.
The old scenario JSON remains where independent pipeline tests still use its
authored domain values; there is no executable provider scenario interpreter.

## 5. Delivery boundary

Record exact source commits, binary identities, live and deterministic results
in `plans/evidence/harden-llm/real-provider-151/`. The live chain uses candidate
local processes and a real upstream; it is not evidence of a promoted or
deployed Query Analytics release. Existing unrelated dirty owner checkouts are
reported as such and left untouched. Deploying the adapter fix remains a
separate explicit production promotion.

## 6. Production closeout

PR #152 is merged and source `94c7f7562c84b55d681cf4d11ee676ee59dd0bd5`
is deployed to the gateway and worker after all 30 exact-source release tasks
passed. The targeted deployed token-limit request succeeded, and the repeated
request was served from SQL cache without provider dispatch. Twelve HTTP
checks, all eight controller scopes, new worker pollers, original-data
fingerprints and unchanged shared owners pass. See the
[production receipt](../../plans/evidence/harden-llm/real-provider-151/2026-11-09-production.json).
Query Analytics remains a separately blocked draft; Agent CI #24 records its
runner allowlist activation prerequisite. No browser or consumer deployment is claimed.
