# Reusable Result and Workspace History

## 1. Scope and interaction contract

The workspace Output panel becomes Result. Current Result and each workspace
History item use one presentation-only `LlmResultComponents.llm_result/1`.
History is one cursor-paginated array of these cards inside the workspace.
The duplicate audit page and Domain trace dialog are retired; old `/history`
bookmarks redirect to `/workspace`, preserving an optional trace selection.

Each card has three rows: recorded user input, output, and LLM stats. Input
and output each have an accessible `📋` copy command. One `↕️` command expands
or collapses both text rows without truncating the underlying copy values.
Expansion is card-local and retained across unrelated LiveView patches.
The full request (including system prompt/options) remains in Request.

The stats row retains the existing Overview-on-open behavior. Its controls are
Overview, Details, Request, Response, cURL, then `🔁` rerun. History cards start
with stats details closed and maintain independent selections. Existing
restore, delete, and artifact downloads remain available. The per-card audit
shortcut is omitted because inline trace details provide inspection in place.
There is no separate aggregate stats panel or aggregate polling UI. Backend
aggregate accounting, trace storage, OpenAPI, and telemetry remain unchanged.
Available artifact downloads use the shared resource projection and inline
controls, including artifacts that become available when Details loads.

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

History loads ten cards per REST page and offers Load more only when a server
cursor exists. One in-flight history read is allowed; pagination appends by
unique run ID and preserves existing card disclosure state. Refresh after a
run/deletion restarts from the first page; older pages can be loaded again.
Failed page reads retain loaded cards and the cursor for an explicit retry.
Clear-all invalidates the active read reference so late pages cannot resurrect
deleted cards. WEB-TEST-069 covers this lifecycle and retired-page redirects.

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

## 4. Stats-row wrapping investigation (proposal only)

The shared summary uses `font: inherit`. Current Result is hosted under
`.ullm-output-widget` (14px, line-height 1.45); History lacks that typography
and inherits the application's 16px default. The rendered fixture cards show
this mismatch. Both immediate summary groups explicitly use `flex-wrap: wrap`
with 12px gaps. Full trace IDs, model names, error categories, and wider metric
values consume the available width; even an individual metric can break at a
space. This is a layout issue, not duplicated or disconnected stats data.

Recommended next change: give the stats component consistent 14px typography,
make the identity group shrink with ellipsis on ID/model (retain full accessible
text and add full-value titles), and keep individual metrics unbroken. Target a
single desktop row with deliberate narrow-screen wrapping. Forcing one row at
every width would require horizontal scrolling or moving lower-priority metrics
into Overview; that is a separate product choice. No wrapping CSS is changed
in this cleanup.

Local Chromium screenshots cover rendered Result and History cards; the pinned
test image lacks emoji glyphs, so those screenshots are not proof of emoji
appearance or universal line-break thresholds. Existing DOM/ARIA checks cover
the emoji controls. The temporary layout probe was removed before the checkpoint.

## 5. Retained implementation notes

Deletion clears a removed card's transient trace state immediately. A cheap
regression proves that a trace completing during an optimistic delete cannot
leave a restored card stuck loading when the delete fails. The regression
failed before this fix and passed afterward.

The native browser canary centers the specific control before clicking after
large trace panels have scrolled it offscreen. Centering an entire tall card
can leave its top-row controls under the sticky header. Collapse, patch
retention, and clipboard assertions remain unchanged; an explicit hit-test
checks that the expansion control is unobstructed.

Hosted cleanup exposed a separate canary assertion error: Wallaby `refute_has`
queries presence and fails immediately when the pre-patch row is found. It does
not wait for disappearance. The frontend logged a successful delete and a
subsequent owner-scoped API read confirmed the exact smoke record was absent.
Both local and hosted canaries now assert `count: 0, visible: :any`, waiting
within the existing timeout for actual DOM removal, not merely hiding. Existing
LiveView tests cover optimistic removal, successful refresh, and failed-delete
rollback; the browser case covers native click and patch delivery. No deletion
implementation change is needed for this test defect.

The earlier cURL-only checkpoint `a3d65e2` passed the fast gate and was pushed.
Its release run was deliberately cancelled through the runner's signal handler
when this larger request arrived; cleanup reported no errors. It was not
promoted or treated as a successful release certification. The combined
Result/History change includes that cURL move and is certified separately.
