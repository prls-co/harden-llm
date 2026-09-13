# Testing guidelines for LiveView frontends and Go backends

This is a standalone, portable guideline. Copy the whole file to
`docs/liveview-go-testing-guidelines.md` in another repository, then paste the
`AGENTS.md` text from section 8. No files from the source repository are needed.

## 1. Purpose and governing rule

Use this standard in repositories with a Phoenix LiveView frontend and a Go
backend. It is a development policy, not a requirement to install three test
frameworks or run every level for every change.

**Use the smallest environment that can exercise the production behavior and
prove the assertion.** Most development should require no browser, external
service, public network, or paid credentials. Test cost depends on setup and
dependencies, not simply on whether the test uses Go, Elixir, or JavaScript.

Keep application ownership intact: Go tests exercise backend rules and API
behavior; Elixir tests exercise server-owned UI behavior; JavaScript tests
exercise actual client code. Do not duplicate JavaScript logic in Elixir just
to avoid running Node.

## 2. Three practical levels

| Level | Environment and purpose | When to run |
| --- | --- | --- |
| **1. Fast, browser-free** | Pure Go/Elixir functions, Go HTTP handlers with local test doubles, Phoenix components/ConnCase/LiveViewTest, and dependency-free Node tests of production JavaScript. | Default coding loop and push/PR checks. |
| **2. DOM adapter tests, optional** | Node with one justified DOM emulator, such as Happy DOM or jsdom, exercising real hook code against simulated elements and events. | Targeted adapter changes, after the repository explicitly adopts this level. |
| **3. Real browser, opt-in** | A small set of targeted checks for actual rendering, browser events/APIs, and LiveSocket integration. | Only when explicitly requested and the assertion requires a browser. |

These are selection levels, not mandatory sequential gates. Level 2 may be
absent indefinitely. A layout defect can need Level 3 directly; adding a DOM
emulator would not make that assertion cheaper to prove.

Real database/storage integration, migrations, race checks, full-system
deployment checks, and live-provider calls are **separate verification tracks**.
This frontend hierarchy does not replace them or authorize them automatically.
Select those checks when the changed backend or deployment boundary needs them.

## 3. Put assertions at their owner

| Behavior | Primary check | What it does not establish |
| --- | --- | --- |
| Validation, authorization, cache identity, retries, provider routing | Go unit/handler tests; deterministic provider stubs | Real database semantics or live-provider compatibility |
| REST request/response shape, optional fields, error decoding | Go contract tests plus Elixir API-client tests using the agreed wire contract | A full deployed network path |
| Server-owned forms, folds, loading/error states, pagination, component messages | Elixir component and LiveView tests | Execution of browser JavaScript or visual layout |
| Filtering, selection arithmetic, shortcut decisions, client payload construction | Plain Node tests importing production functions | Native event delivery or hook lifecycle integration |
| Listener registration/removal and element mutation in a hook | Optional DOM tests importing the actual hook | Real LiveSocket patching, layout, or browser permissions |
| Overflow, focus behavior, native uploads, clipboard permissions, reconnect/patch behavior | A narrowly scoped real-browser check | Correctness of every backend or input permutation |

Shared API fixtures must remain checked against the backend/OpenAPI contract.
A frontend stub and a decoder that agree on an invented response are not
end-to-end contract coverage. Keep credentials and live user data out of fixtures.

## 4. Level 1: the default development loop

### Backend and LiveView

- Add a deterministic regression before fixing a defect where practical.
- Exercise public handlers/events and observable output, not private assigns
  or implementation details unnecessarily.
- Use local HTTP test servers or recorders for Go handlers/provider adapters.
  Go supplies these in [`net/http/httptest`](https://pkg.go.dev/net/http/httptest).
- For LiveView, prefer element/form-driven events so the rendered binding and
  handler are checked together. LiveViewTest communicates with LiveView
  processes instead of driving a browser. [Phoenix documentation](https://hexdocs.pm/phoenix_live_view/Phoenix.LiveViewTest.html#testing-events)
- Cover success, empty/loading/error states, malformed responses, repeated
  events, and independent component instances where relevant. Use table-driven
  cases at this level instead of repeating the same matrix in a browser.
- Keep API stubs private to each test and explicitly allow owned child
  processes. Use unique fixtures, supervised processes, bounded waits, and
  teardown. Prefer `async: true` and parallel execution when ownership permits.
- Name the shared resource when a test must be serial. Do not serialize a
  suite to conceal a race or use arbitrary sleeps to conceal missing signals.

### Client JavaScript

Keep pure decisions separate from thin hooks that perform DOM effects. Import
the same functions in production and tests; avoid a test-only reimplementation.
Use Node's built-in [`node:test`](https://nodejs.org/api/test.html) and assertions
when sufficient. No DOM emulator or additional test framework is required for
pure functions. A repository without custom JavaScript need not add Node tests.

An HTML assertion, a serialized `Phoenix.LiveView.JS` command assertion, or a
CSS source check can protect a useful contract. None is evidence that the
browser executed the command or rendered the expected geometry. State that
limit; do not replace a visual assertion with an HTML-presence assertion and
claim equivalent coverage.

## 5. Level 2: adopt a DOM emulator only for a demonstrated gap

Before adding Happy DOM or jsdom, record:

1. The concrete adapter failure and why pure functions/LiveViewTest cannot
   exercise its cause.
2. The DOM APIs and event behavior the production hook needs.
3. A focused comparison of the candidates against those APIs, correctness,
   installation/maintenance cost, and measured test time/memory.
4. The cases moving out of the expensive suite and the real-browser check
   retained for the simulated-to-real integration boundary.

Choose one emulator; do not install both by default. Use a short repository
decision record and an explicit command/path selection. Once adopted, run the
relevant DOM tests for adapter changes without silently expanding every fast
invocation. Preserve any stricter repository adoption requirements.

Do not assume simulated DOM success proves visual correctness. For example,
jsdom explicitly does not perform layout or rendering, even in its visual
mode. [jsdom documentation](https://github.com/jsdom/jsdom#pretending-to-be-a-visual-browser)
Supplying fake element dimensions can test a calculation, not the browser's
actual dimensions. Do not stub the behavior under test just to make it pass.

## 6. Level 3: explicit, bounded browser use

Browser use requires an explicit request. UI implementation, debugging,
deployment, "test", "verify", or "production-ready" alone does not authorize
launching Chromium, Playwright, Wallaby, or a browser-containing container.

- State the browser-only assertion before launch. Keep just enough
  representative cases to prove that boundary; keep permutations in Level 1
  or an adopted Level 2.
- Use one coordinated browser-test workflow at a time per shared development
  host. Do not launch competing browser runners from separate tools or agents.
- Reuse one test-owned browser with isolated, sequential contexts when the
  harness supports it; otherwise close each session before starting the next.
  Do not reuse a person's browsing profile or carry state between tests.
- Coordinate ownership before launch. A worker count of one only limits that
  invocation; it does not prevent another process or agent starting Chrome.
  Shared runners need a shared serialization mechanism, such as a common lock
  or queue, when independent launchers can overlap. Do not claim this is
  enforced until it is implemented and tested.
- Close owned sessions, contexts, drivers, and containers on success, failure,
  and cancellation. Verify cleanup. Never kill unrelated browser processes.
  Multiple renderer/utility processes can belong to one browser instance.
- Prefer deterministic fixtures and a local backend. Browser authorization
  does not authorize paid inference, production mutations, or copying sessions.
- Keep failure artifacts bounded and redacted. Do not automatically retry an
  ambiguous operation that could repeat a real mutation or provider charge.

Automatic development CI and deployment checks remain browser-free. Browser
checks use a separate, explicit opt-in entrypoint, not a dependency hidden
inside a normal test or release command.

## 7. Daily workflow and evidence

1. Identify the behavior, its production owner, and the lowest sufficient level.
2. Add or identify its cheap regression; implement the change and run focused
   tests, then the repository's broad fast gate for application changes.
3. Run additional tracks only for their distinct changed boundaries. Stop and
   ask if a necessary check requires browser or live-provider authorization.
4. When an expensive test reveals a defect, add a cheap root-cause regression
   where possible. Keep the expensive case only for what the cheap test cannot
   observe; explain when no lower-level equivalent exists.
5. Report exact commands/results and distinguish local tests, hosted CI,
   deployed HTTP checks, browser verification, and live-provider checks. If no
   browser ran, say "browser layout not checked" rather than implying it passed.

Measure whole-command wall time, setup/cold-start cost, and peak memory before
optimizing a gate. Compare equivalent assertions and conditions; do not infer
a language ranking from differently sized suites. Prefer removing duplicated
expensive scenarios over adding test infrastructure. Do not target arbitrary
test counts, coverage percentages, or universal timing budgets.

Docs-only changes need proportionate Markdown/link/policy checks, not application
builds, browsers, provider calls, or a full release suite. Test-code changes
need their affected checks even when no application rebuild is necessary.

## 8. Adoption in another repository

### Copy and activate

1. Copy this **entire file, unchanged**, to
   `docs/liveview-go-testing-guidelines.md` in the destination repository.
2. Add the following block to that repository's root `AGENTS.md`. If a testing
   section already exists, merge the block into it and preserve local commands
   and stricter requirements.
3. Optionally link the guideline from the development README. No other document,
   dependency, scheduler, or CI workflow needs to be copied to use the policy.

### Ready-to-paste `AGENTS.md` text

```markdown
## Test Feedback Methodology

Read and follow [LiveView and Go testing guidelines](docs/liveview-go-testing-guidelines.md)
in full before planning changes, writing tests, or running verification.
Use its three-level policy: fast Go/Elixir/LiveView and plain Node by default,
optional justified DOM tests, and real browsers only on explicit user request.
Keep repository-specific commands and stricter requirements in this file.
```

The link in this snippet is relative to the repository-root `AGENTS.md`. If you
choose another guideline location or a nested `AGENTS.md`, adjust the link.
Copying the policy does not automatically enforce it in CI; use existing local
gates and report any enforcement gaps rather than claiming they are implemented.

### Repository-specific details

Keep local details in `AGENTS.md` or the development README, not in this portable
file. Record the actual commands and paths; do not assume another repository has
the same Make targets, directory layout, test IDs, or toolchain versions:

- Pinned Go, Elixir/OTP, and Node toolchains and the broad fast command.
- Backend/frontend contract location and fixture ownership.
- Optional DOM adoption status and command; do not create an empty framework.
- Browser opt-in command, owner, serialization scope, and cleanup mechanism.
- Separate service-integration, deployment, and live-provider commands.

Use existing Make/Mix/CI entrypoints where possible. Keep one authoritative
task selection policy instead of introducing a second scheduler. Adopting this
document does not itself install dependencies or implement process locks.
