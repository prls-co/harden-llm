# Reusable Result and Workspace History

## 1. Scope and interaction contract

The workspace Output panel becomes Result. Current Result and each workspace
History item use one presentation-only `LlmResultComponents.llm_result/1`.
The separate `/history` audit page retains its pagination, inspection, restore,
and download workflows.

Each card has three rows: recorded user input, output, and LLM stats. Input
and output each have an accessible `📋` copy command. One `↕️` command expands
or collapses both text rows without truncating the underlying copy values.
Expansion is card-local and retained across unrelated LiveView patches.
The full request (including system prompt/options) remains in Request.

The stats row retains the existing Overview-on-open behavior. Its controls are
Overview, Details, Request, Response, cURL, then `🔁` rerun. History cards start
with stats details closed and maintain independent selections. Existing
restore, delete, inspection, and artifact downloads remain available.

## 2. Ownership and data flow

The reusable card owns presentation and local text expansion only. It receives
input/output values and slots for stats and optional collection actions. It
does not know about authentication, storage, API clients, or run execution.
`LlmTraceComponents` remains the common stats/details renderer. Existing JSON
folding and Clipboard behavior are reused; emoji copy feedback is opt-in.

WorkspaceLive owns the current result and the existing owner-scoped History
array. It supplies recorded values through the existing projections. Per-card
trace state is transient and pruned when History changes. Full trace requests
start only on Details, avoid duplicate in-flight loads, and ignore completions
for removed or superseded records. No backend/OpenAPI or storage change is
needed.

Rerun resolves the request by ID from server-owned current result/History data,
never from client-submitted payloads or the edited form. It reuses the same
execution startup/completion/error path as Run Prompt. It preserves the
recorded cache mode; backend resolution uses currently saved profiles and
credentials. Missing requests and dirty unsaved profiles disable rerun, and
the existing active-run guard prevents duplicate submissions. The editor
draft stays unchanged.

## 3. Verification and deployment

WEB-TEST-066/067 cover the result contract, isolated trace state, lazy loads,
exact rerun payloads, duplicate prevention, full text copy, and browser folding.
Run focused deterministic tests, `make test-fast`, native browser checks, and
the required release gate before promoting the exact pushed frontend image.
Use the existing production path and retain the previous image for rollback.
Record hosted identity, health, and authenticated results accurately.

Deletion clears a removed card's transient trace state immediately. A cheap
regression proves that a trace completing during an optimistic delete cannot
leave a restored card stuck loading when the delete fails. The regression
failed before this fix and passed afterward.

The native browser canary centers the specific control before clicking after
large trace panels have scrolled it offscreen. Centering an entire tall card
can leave its top-row controls under the sticky header. Collapse, patch
retention, and clipboard assertions remain unchanged; an explicit hit-test
checks that the expansion control is unobstructed.

The earlier cURL-only checkpoint `a3d65e2` passed the fast gate and was pushed.
Its release run was deliberately cancelled through the runner's signal handler
when this larger request arrived; cleanup reported no errors. It was not
promoted or treated as a successful release certification. The combined
Result/History change includes that cURL move and is certified separately.
