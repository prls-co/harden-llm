# Preset live qualification

## 1. Scope and authorization

TEST-455 runs the existing stateful service and stateless engine against the
actual shipped catalog. It is an explicitly authorized live-provider canary,
outside automatic fast/release/deployment checks. It starts no browser, gateway
server, database or history storage. Each configured preset executes once per
selected mode, with cache off and no Harden retry, repair or rerun. Upstream
internal retries are outside this canary's control.

Use the approved protected environment and metadata file named by
`HARDEN_LLM_CONFIG_FILE`. Provider keys remain in their configured environment
variables. Search additionally uses the existing `JINA_API_KEY` for fallback
routes. Missing credentials are reported as `not_configured`, never as passes.

## 2. Select a campaign

With the protected startup environment already loaded, choose a new private
report path in an existing private directory:

```sh
HARDEN_LLM_LIVE_PRESETS=1 \
HARDEN_LLM_LIVE_PRESET_SEARCH=1 \
HARDEN_LLM_LIVE_PRESET_REPORT=/private/new-search-report.json \
go test -tags=live ./cmd/harden-llm-gateway \
  -run '^TestLivePresetMatrix$' -count=1 -v -timeout=90m
```

Omit `HARDEN_LLM_LIVE_PRESET_SEARCH` for the existing basic-generation campaign.
Both text and structured modes run by default. `HARDEN_LLM_LIVE_PRESET_MODE=text`
or `structured` explicitly selects one mode; it is not a retry mechanism.
The Go test timeout covers the whole sequential catalog; every individual run
retains its existing 60-second deadline.

Search changes only the prompt and search intent. Preset model IDs, reasoning,
provider options and structured schemas stay unchanged. Native support follows
the profile capability and protocol; Claude structured output and unsupported
native routes use the existing Jina fallback. Direct Anthropic and Claude via
CPA are separate routes and require separate evidence.

## 3. Read the evidence

Every selected preset/mode receives a row. Atomic mode-0600 snapshots record the
whole plan, then `started` before dispatch and the final result afterward.
Reports refuse an existing destination. Each row records the Git checkout revision in
`sourceRevision` and hashes the effective prompt, schema (when applicable),
provider options, preset definition and recovery policy in `configIdentity`.
`sourceDirty` records whether Git found tracked changes or untracked files before
the first dispatch. Preserve those changes alongside a dirty checkout's report.

Successful search cases record expected/observed mode, execution, call/source/
citation counts, omission warnings and completeness. `searchObservation` is
`observed` or `not_observed`. Optional native search can remain unobserved on a
successful answer; that row does not certify retrieval. An observed search can
also return zero results. Neither outcome proves fact-level grounding.

Text citations are verified against the exact returned text hash and Unicode
code-point bounds. Structured reference results do not expose original
assistant text; `original_text_unavailable` reports this verification limit.
Reports retain counts and fixed diagnostics, not answer bodies, source URLs,
credentials or raw provider error output.

Read three fields together in reports with `qualificationStatus`.

| Field | Meaning |
| --- | --- |
| `status` | Execution outcome. A dispatched request with an unknown outcome is `unconfirmed`. |
| `diagnosticCaptureStatus` | `complete` requires the expected operations, in order, on the actual attempt. `missing`, `incomplete`, `clipped` and `publication_failed` explain evidence gaps. `not_applicable` covers a run with no operation to observe. |
| `qualificationStatus` | `passed`, `failed`, `invalid_execution`, `invalid_evidence` or `invalid_diagnostics`. Planned, started and unconfigured rows have no verdict. |

For example, a successful Jina fallback needs a successful search record
followed by a model record. If the latter is missing, the answer remains
`succeeded` and qualification is `invalid_diagnostics`. A Jina failure before
model dispatch can have complete diagnostics with just the failed search.
Use `runId`, `callId` and `traceId` to join the row to its evidence.

Count passes in a new report with the following command.

```sh
jq '[.[] | select(.qualificationStatus == "passed")] | length' report.json
```

Archived reports without the verdict field used `status` for both execution and
qualification. Read their counts under that original convention. Their rows
cannot establish that the newly required operation sequence was captured.

Missing credentials remain `not_configured`. Provider failures, malformed
evidence and invalid execution remain failures. A leftover `started` row or an
`unconfirmed` transport outcome is never a successful or safely repeatable
call. Do not rerun the campaign to obtain a green result or automatically replay
ambiguous inference. Investigate the retained failure and separately authorize
any new experiment. TEST-548 exercises the campaign's cheap invariants through
the default-tag Go tests.
