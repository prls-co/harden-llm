# Parallel Test Feedback Hierarchy Decisions

## Resolution

On 2026-08-23, the user accepted the recommendations for all nine questions:
`1A`, `2A`, `3A`, `4A`, `5B`, `6A` conditional on isolation proof, `7A`,
`8A`, and `9A` with explicit approximations allowed only under the stated
higher-tier evidence rule. The follow-up clarified that Happy DOM was an
example rather than a required dependency. The approved implementation is
defined in
`plans/from_utility-llm/harden-llm-parallel-test-feedback-plan.md`.

The original questionnaire is retained below as decision rationale. These
resolved choices govern the planned repository test methodology, executable
test targets, parallelism model, and agent behavior.

## Verified current context

- The root Makefile defines test-unit, test-parity, test-integration,
  test-integration-race, test-compose, test-race, test-vulnerability, and the
  aggregate verify target.
- Makefile currently fixes integration and race package parallelism at 1.
- frontend/test/test_helper.exs excludes browser and compose tests from the
  default mix test run and starts Wallaby/assets only when those tags are
  explicitly selected.
- Most frontend LiveView and ConnCase test modules currently declare
  async: false.
- ProfileWidgetComponent.handle_event/3 owns widget transitions such as
  toggle-fold, while WorkspaceLive.handle_info/2 and
  EmbeddingLive.handle_info/2 receive namespaced component messages.
- integrationtest.StartPostgres and integrationtest.StartGarage currently
  start isolated randomized Compose projects and clean them up per test.

## Terminology

- **Fast loop**: The edit-test cycle Codex should run repeatedly while coding.
- **Warm run**: A run after dependencies and compiler caches already exist.
- **Cold run**: A run that must compile or initialize missing test artifacts.
- **Cheap test**: A deterministic, parallel-safe test without a real browser,
  container, public network call, fixed port, or process-global mutable state.
- **Test fidelity**: How closely the test environment resembles production.
  Lower fidelity may replace Chromium, Postgres, Garage, or a provider with an
  in-process model.
- **Oracle correctness**: Whether the assertion precisely decides the claim the
  test says it covers. Lower fidelity must not imply a vague or incorrect
  oracle.
- **Resource class**: A declared concurrency category such as pure,
  in-process, service, browser, or full-system.
- **Browser canary**: A small representative Chromium test that verifies a
  browser-only boundary without multiplying the full state matrix.
- **Shared infrastructure, isolated state**: Reuse one service process or
  container while assigning each test a unique database, schema, bucket,
  owner, prefix, or transaction.
- **T0-T5**: Proposed tiers: T0 pure, T1 in-process LiveView/HTTP, T2
  lightweight JavaScript DOM, T3 service integration, T4 real browser, and T5
  full Compose/deployed/live-provider certification.

### 1.1 Question

What runtime budget should define the default fast loop?

### 1.2 Context & clarification

Development iteration speed is an explicit requirement, but current warm time,
cold time, peak memory, and CPU saturation have not been benchmarked as
separate test tiers. A budget is needed so agents can fail fast when a cheap
suite gradually becomes expensive. The decision affects future test-fast
targets and CI performance assertions.

### 1.3 Options

- **Option A: Benchmark-driven enforced service-level objective**
  - **Rubrics**: `Conf:80% | Invest:i | Blast:i | Reversal:i | Fit:i | Reuse:i | Obs:i | Surface:iii | Perf:i`
  - **Approach**: Measure warm/cold duration and peak resources on this machine, then set and enforce budgets from the measured baseline.
  - **Example**: Record five warm and three cold runs, then fail test-fast when its rolling budget regresses beyond an agreed threshold.
  - **Architecture**: Adds one canonical performance contract around existing Make and Mix targets without changing production code.
  - **SSoT**: A checked-in test-budget configuration and one test runner own the measurements and thresholds.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Most defensible and observable, but requires a benchmark harness and periodic recalibration.

- **Option B: Fixed initial budget**
  - **Rubrics**: `Conf:70% | Invest:ii | Blast:ii | Reversal:ii | Fit:ii | Reuse:ii | Obs:ii | Surface:ii | Perf:ii`
  - **Approach**: Adopt a fixed target immediately, such as 10 seconds warm and 30 seconds cold, then measure only when the target fails.
  - **Example**: test-fast exits nonzero when elapsed wall time exceeds the fixed warm budget in CI.
  - **Architecture**: Wraps existing commands with a simple elapsed-time check.
  - **SSoT**: One Make variable or checked-in script owns the fixed limits.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Fast to establish, but the values may be unrealistic or too generous for the current host.

- **Option C: Observe without enforcing**
  - **Rubrics**: `Conf:90% | Invest:iii | Blast:iii | Reversal:iii | Fit:iii | Reuse:iii | Obs:iii | Surface:i | Perf:iii`
  - **Approach**: Print or record durations but do not fail on regressions.
  - **Example**: CI publishes timing summaries while agents decide manually whether the loop is too slow.
  - **Architecture**: Leaves existing targets unchanged and adds only reporting.
  - **SSoT**: CI logs are the only timing record.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Lowest investment and easiest rollback, but test cost can drift without an enforceable invariant.

### 1.4 Recommendation

Recommend **Option A**. Performance is explicitly important, so the decision
should be measured rather than guessed. Establishing a local baseline before
setting limits follows correctness and observability precedence while still
making test speed enforceable.

### 2.1 Question

What should “all tests run in parallel” mean operationally?

### 2.2 Context & clarification

Unlimited concurrency is safe for pure and isolated in-process tests but can
make Chromium, Docker, race-instrumented binaries, and fixed-capacity services
slower or unstable. A resource class is the concurrency contract used by the
test scheduler. The current Makefile already serializes integration and race
packages at 1, but there is no repository-wide resource scheduler.

### 2.3 Options

- **Option A: Resource-class-aware parallel scheduler**
  - **Rubrics**: `Conf:90% | Invest:i | Blast:i | Reversal:i | Fit:i | Reuse:i | Obs:i | Surface:iii | Perf:i`
  - **Approach**: Run T0-T2 freely in parallel, assign measured worker limits to T3-T4, and run T5 exclusively.
  - **Example**: Pure and LiveView pools use all available cores, service tests use two slots, browser tests use one slot, and full Compose acquires an exclusive lock.
  - **Architecture**: Extends existing tags/build tags into an explicit repository scheduler and keeps resource policy out of individual test logic.
  - **SSoT**: One runner configuration maps test tiers to concurrency limits.
  - **System limits**: Current integration and race package caps are 1; safe machine-wide limits are unknown. Unknown - not available in local context.
  - **Trade-offs**: Best throughput and stability, but requires classification, scheduling, and measured limits.

- **Option B: One global concurrency cap**
  - **Rubrics**: `Conf:70% | Invest:ii | Blast:ii | Reversal:ii | Fit:iii | Reuse:ii | Obs:ii | Surface:ii | Perf:iii`
  - **Approach**: Run every selected test concurrently under one maximum worker count regardless of resource type.
  - **Example**: All Go, ExUnit, browser, and integration jobs share a maximum of four workers.
  - **Architecture**: Adds a simple outer limit but ignores framework-specific and service-specific cost differences.
  - **SSoT**: One global worker-count variable controls concurrency.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Simpler than resource classes, but cheap tests can be delayed by expensive workers and the optimal cap varies by workload.

- **Option C: Parallel cheap tiers and serialize every expensive tier**
  - **Rubrics**: `Conf:90% | Invest:iii | Blast:iii | Reversal:iii | Fit:ii | Reuse:iii | Obs:iii | Surface:i | Perf:ii`
  - **Approach**: Run T0-T2 in parallel and keep T3-T5 sequential without introducing measured worker pools.
  - **Example**: mix test and unit Go tests parallelize internally; Postgres, Garage, Chromium, and Compose gates run one after another.
  - **Architecture**: Closely follows the current Makefile’s conservative integration/race behavior.
  - **SSoT**: Existing test tags and sequential release scripts define the split.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Stable and easy to implement, but leaves available capacity unused for service tests that could safely share infrastructure.

### 2.4 Recommendation

Recommend **Option A**. It interprets “all tests parallel” as “all tests run at
the maximum safe parallelism for their resource class.” This preserves rapid
cheap feedback without allowing expensive tests to starve or destabilize it.

### 3.1 Question

How should Make target semantics distinguish the coding loop from release
certification?

### 3.2 Context & clarification

The current make verify target includes Docker integration, integration under
the race detector, another full race pass, and vulnerability scanning. It is a
strong release gate but a poor default after every edit. Agents need one
unambiguous fast command and one unambiguous comprehensive command.

### 3.3 Options

- **Option A: Add tiered targets and preserve verify as comprehensive**
  - **Rubrics**: `Conf:90% | Invest:i | Blast:ii | Reversal:ii | Fit:i | Reuse:i | Obs:i | Surface:iii | Perf:i`
  - **Approach**: Add test-fast, test-integration, test-browser, and test-release while retaining make verify as the existing comprehensive backend contract.
  - **Example**: Codex runs make test-fast repeatedly; CI runs integration/browser conditionally; release certification runs make test-release and make verify.
  - **Architecture**: Extends existing Make conventions without silently changing a published command’s meaning.
  - **SSoT**: The Makefile owns tier composition; AGENTS.md only explains when to invoke each target.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Clear and backward-compatible, but introduces several explicit targets.

- **Option B: Redefine verify as fast and add verify-release**
  - **Rubrics**: `Conf:80% | Invest:ii | Blast:i | Reversal:i | Fit:ii | Reuse:ii | Obs:ii | Surface:ii | Perf:ii`
  - **Approach**: Make the shortest command the default fast loop and move the current aggregate behavior to a new release target.
  - **Example**: make verify becomes browser/Docker-free; make verify-release invokes all expensive gates.
  - **Architecture**: Produces intuitive inner-loop naming but changes the existing verify contract referenced by documentation and automation.
  - **SSoT**: The Makefile still owns composition, but all callers must migrate atomically.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Convenient after migration, but has the highest compatibility and certification risk.

- **Option C: Keep targets unchanged and document manual subsets**
  - **Rubrics**: `Conf:90% | Invest:iii | Blast:iii | Reversal:iii | Fit:iii | Reuse:iii | Obs:iii | Surface:i | Perf:iii`
  - **Approach**: Tell agents to combine existing focused commands instead of adding an aggregate fast target.
  - **Example**: Agents separately invoke make test-unit, make test-parity, and mix test.
  - **Architecture**: Requires no Makefile change.
  - **SSoT**: AGENTS.md becomes responsible for command composition in addition to the Makefile.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Minimal code change, but duplicates orchestration knowledge and makes omissions likely.

### 3.4 Recommendation

Recommend **Option A**. It creates an explicit fast path without weakening or
silently redefining retained certification semantics.

### 4.1 Question

How much real-Chromium coverage should remain after cheap coverage is expanded?

### 4.2 Context & clarification

Current browser coverage has desktop and mobile full workflows, a two-instance
embedding feature, and a release-only Compose feature. Chromium is required
only for LiveSocket/DOM patching, actual JavaScript hooks, native input
semantics, browser event ordering, focus, CSS layout, and viewport behavior.
Profile and fold permutations can be covered below the browser tier.

### 4.3 Options

- **Option A: Two risk-based browser canaries plus release Compose**
  - **Rubrics**: `Conf:80% | Invest:i | Blast:i | Reversal:i | Fit:i | Reuse:i | Obs:i | Surface:ii | Perf:ii`
  - **Approach**: Keep one widget/client-hook canary and one authenticated workflow canary; reserve full topology behavior for the Compose release test.
  - **Example**: Canary one exercises searchable selection, nested folding, and two-instance isolation; canary two performs login, run, result, reconnect, and logout.
  - **Architecture**: Mirrors the actual client/server boundaries while moving state matrices to LiveViewTest.
  - **SSoT**: Browser files own only browser-specific contracts; LiveView tests own server-state permutations.
  - **System limits**: Safe Chromium concurrency on this machine is unknown. Unknown - not available in local context.
  - **Trade-offs**: Strong targeted confidence with low browser count, but requires carefully defining the browser-only invariant list.

- **Option B: Retain the current three browser features**
  - **Rubrics**: `Conf:90% | Invest:ii | Blast:iii | Reversal:iii | Fit:ii | Reuse:ii | Obs:ii | Surface:iii | Perf:iii`
  - **Approach**: Keep desktop, mobile, and embedding features unchanged while running them in a separate lane.
  - **Example**: Continue executing the current full_workflow_test.exs only for UI-relevant changes and releases.
  - **Architecture**: Uses the existing Wallaby suite without test restructuring.
  - **SSoT**: Existing browser feature modules remain the browser acceptance source.
  - **System limits**: Safe Chromium concurrency on this machine is unknown. Unknown - not available in local context.
  - **Trade-offs**: Highest retained evidence and lowest migration risk, but repeats the large workflow and consumes more browser time.

- **Option C: One consolidated browser workflow plus release Compose**
  - **Rubrics**: `Conf:70% | Invest:iii | Blast:ii | Reversal:ii | Fit:iii | Reuse:iii | Obs:iii | Surface:i | Perf:i`
  - **Approach**: Merge all representative browser assertions into one sequential Wallaby feature and rely on cheaper tiers for isolation.
  - **Example**: One feature checks desktop widget interaction, resizes to mobile, completes one run, then logs out.
  - **Architecture**: Minimizes browser session startup but couples several contracts into one long scenario.
  - **SSoT**: One feature becomes the sole browser acceptance path.
  - **System limits**: Safe maximum scenario duration is unknown. Unknown - not available in local context.
  - **Trade-offs**: Cheapest browser execution, but failures are less isolated and one early failure hides later assertions.

### 4.4 Recommendation

Recommend **Option A**. Two independently diagnosable canaries retain browser
boundary confidence without turning data permutations into browser sessions.

### 5.1 Question

Should the repository introduce Vitest/Happy DOM now for custom JavaScript
hooks?

### 5.2 Context & clarification

frontend/assets/js/app.js currently contains client-owned behavior such as the
SearchableCombobox, SecretStager, PromptShortcut, SchemaPending, and Clipboard
hooks. Happy DOM can cheaply model many DOM events but cannot prove CSS layout,
native-browser fidelity, or the complete Phoenix LiveSocket lifecycle. The
frontend currently has no dedicated JavaScript test stack.

### 5.3 Options

- **Option A: Add Vitest and Happy DOM as a canonical T2 tier**
  - **Rubrics**: `Conf:70% | Invest:i | Blast:i | Reversal:i | Fit:ii | Reuse:i | Obs:i | Surface:iii | Perf:ii`
  - **Approach**: Extract hook modules, run them under Happy DOM, and make the suite part of test-fast.
  - **Example**: Test combobox filtering, arrow navigation, custom-value commits, blur restoration, and secret attribute removal without Chromium.
  - **Architecture**: Adds a narrow client-test boundary beside the existing esbuild assets and retains LiveView as the state owner.
  - **SSoT**: Exported hook modules remain the production implementation imported by app.js and the tests.
  - **System limits**: Happy DOM compatibility with every current LiveView hook lifecycle is unverified. Unknown - not available in local context.
  - **Trade-offs**: Broad cheap client coverage, but adds Node dependencies, configuration, and a second frontend test runtime.

- **Option B: Extract a pure JavaScript core and use Node’s built-in test runner**
  - **Rubrics**: `Conf:80% | Invest:ii | Blast:ii | Reversal:ii | Fit:i | Reuse:ii | Obs:ii | Surface:ii | Perf:i`
  - **Approach**: Move filtering and state-transition logic into pure functions and test those without a synthetic DOM.
  - **Example**: Test visible-option calculation and keyboard transition results as arrays and explicit state values.
  - **Architecture**: Applies the functional-core/imperative-shell pattern while preserving current hooks as thin DOM adapters.
  - **SSoT**: Pure exported functions own client state rules; hooks only translate DOM events and effects.
  - **System limits**: DOM-adapter behavior would remain covered only by browser canaries. Unknown - not available in local context.
  - **Trade-offs**: Minimal dependency cost and fastest execution, but cannot cheaply verify listener lifecycle or actual DOM mutation.

- **Option C: Defer lightweight JavaScript tests**
  - **Rubrics**: `Conf:90% | Invest:iii | Blast:iii | Reversal:iii | Fit:iii | Reuse:iii | Obs:iii | Surface:i | Perf:iii`
  - **Approach**: Keep client behavior covered by targeted Chromium canaries until hook complexity materially grows.
  - **Example**: Continue testing combobox and secret staging only in the browser workflow.
  - **Architecture**: Leaves the current Phoenix asset architecture unchanged.
  - **SSoT**: app.js remains the only implementation, with no duplicated test model.
  - **System limits**: Browser test capacity is unknown. Unknown - not available in local context.
  - **Trade-offs**: Strict YAGNI and no new tooling, but preserves an expensive feedback gap for client-hook changes.

### 5.4 Recommendation

Recommend **Option B** initially. It creates the cheapest reusable state model
with no synthetic-browser dependency. Add Happy DOM later only if measured
failures show that thin DOM adapters need broader lifecycle coverage.

### 6.1 Question

What isolation model should Postgres, Garage, and similar integration tests use?

### 6.2 Context & clarification

integrationtest.StartPostgres and StartGarage currently create an isolated
Compose project per test. That is robust but repeats container startup and
prevents cheap high-concurrency integration feedback. Shared infrastructure
means reusing service processes while retaining unique test state and
fail-fast leak checks.

### 6.3 Options

- **Option A: One service pool per test runner with per-test namespaces**
  - **Rubrics**: `Conf:70% | Invest:i | Blast:i | Reversal:i | Fit:iii | Reuse:i | Obs:i | Surface:iii | Perf:i`
  - **Approach**: Start Postgres/Garage once per runner, allocate unique databases/schemas and buckets/prefixes, and prove cleanup/isolation.
  - **Example**: Each Go test receives a generated database name and Garage bucket prefix while migrations and operations run concurrently.
  - **Architecture**: Centralizes external-boundary lifecycle in integrationtest while changing its current per-test process-isolation contract.
  - **SSoT**: An integration fixture manager owns service lifecycle and namespace allocation.
  - **System limits**: Maximum safe database connections, Garage concurrency, and host container capacity are unknown. Unknown - not available in local context.
  - **Trade-offs**: Best startup amortization and parallel throughput, but requires strong namespace invariants and leak detection.

- **Option B: One service pool per package or suite**
  - **Rubrics**: `Conf:80% | Invest:ii | Blast:ii | Reversal:ii | Fit:ii | Reuse:ii | Obs:iii | Surface:ii | Perf:ii`
  - **Approach**: Share services only among tests in one package/suite and destroy them at package completion.
  - **Example**: Gateway integration tests share one Postgres instance while artifact tests share one Garage instance.
  - **Architecture**: Extends current package boundaries and limits cross-package coordination.
  - **SSoT**: Each package-level fixture owns its service and namespace allocator.
  - **System limits**: Go package lifecycle hooks and safe package concurrency need a local prototype. Unknown - not available in local context.
  - **Trade-offs**: Easier isolation than a job-wide pool, but starts duplicate services and duplicates fixture lifecycle logic.

- **Option C: Preserve one Compose project per test**
  - **Rubrics**: `Conf:90% | Invest:iii | Blast:iii | Reversal:iii | Fit:i | Reuse:iii | Obs:ii | Surface:i | Perf:iii`
  - **Approach**: Keep StartPostgres and StartGarage semantics unchanged and control cost only through test selection.
  - **Example**: T3 remains opt-in and serial while T0-T2 provide the rapid coding loop.
  - **Architecture**: Exactly preserves the current integrationtest implementation.
  - **SSoT**: Each test owns and cleans up its isolated Compose project.
  - **System limits**: Concurrent container capacity is unknown. Unknown - not available in local context.
  - **Trade-offs**: Strongest process isolation and simplest diagnosis, but highest startup and disk/network cost.

### 6.4 Recommendation

Recommend **Option A**, conditional on first proving unique-state allocation,
cleanup, and contamination detection. The user’s explicit high-parallelism
requirement justifies changing the current fixture lifecycle, but data integrity
must remain the higher-precedence constraint.

### 7.1 Question

How aggressively should existing async: false frontend tests be refactored?

### 7.2 Context & clarification

Most current LiveView and ConnCase modules are serial. Some may require serial
execution because they mutate Application configuration, use globally
registered processes, or depend on shared endpoint state; others may simply
retain conservative defaults. The exact reason for every module has not yet
been audited.

### 7.3 Options

- **Option A: Perform a complete isolation audit and parallelization refactor now**
  - **Rubrics**: `Conf:70% | Invest:i | Blast:i | Reversal:i | Fit:i | Reuse:i | Obs:i | Surface:iii | Perf:i`
  - **Approach**: Classify every serial module, remove avoidable global state, convert safe modules to async: true, and enforce documented exceptions.
  - **Example**: Replace global Req/Application configuration with dependency injection and uniquely supervised per-test processes.
  - **Architecture**: Aligns Phoenix tests with process isolation and makes parallel safety an explicit contract.
  - **SSoT**: ConnCase/test-support helpers own isolated dependency setup; a guard owns the serial-exception list.
  - **System limits**: Safe ExUnit maximum cases and endpoint capacity are unknown. Unknown - not available in local context.
  - **Trade-offs**: Delivers the desired feedback model quickly, but touches many test-support boundaries and may reveal latent coupling.

- **Option B: Convert incrementally and forbid new unexplained serial tests**
  - **Rubrics**: `Conf:90% | Invest:ii | Blast:ii | Reversal:ii | Fit:ii | Reuse:ii | Obs:ii | Surface:ii | Perf:ii`
  - **Approach**: Add the policy now, convert modules when their production area is touched, and require a reason for new async: false usage.
  - **Example**: Widget work converts embedding/workspace tests first; history and profile tests remain serial until changed.
  - **Architecture**: Preserves current behavior while steadily moving toward process isolation.
  - **SSoT**: AGENTS.md and a lint/guard test define permitted serial exceptions.
  - **System limits**: Completion time for the migration is unknown. Unknown - not available in local context.
  - **Trade-offs**: Lower immediate risk, but leaves the fast suite partially serialized and creates a prolonged mixed model.

- **Option C: Establish methodology only**
  - **Rubrics**: `Conf:100% | Invest:iii | Blast:iii | Reversal:iii | Fit:iii | Reuse:iii | Obs:iii | Surface:i | Perf:iii`
  - **Approach**: Document the desired hierarchy without changing existing serial declarations.
  - **Example**: Only future test files are expected to prefer async: true.
  - **Architecture**: Makes no code or test-support changes.
  - **SSoT**: AGENTS.md carries a non-enforced convention.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Minimal immediate risk but does not materially improve current iteration speed.

### 7.4 Recommendation

Recommend **Option A**, implemented in small verified commits. The repository’s
development model depends on cheap parallel tests, so leaving most LiveView
coverage serial would undermine the central goal.

### 8.1 Question

Where should the test hierarchy and resource policy be documented?

### 8.2 Context & clarification

AGENTS.md guides Codex’s operational behavior, the canonical test
specifications define test obligations and identifiers, and an ADR records
durable architectural choices such as shared service infrastructure and
resource classes. Using only one document risks either insufficient agent
guidance or missing architectural rationale.

### 8.3 Options

- **Option A: AGENTS.md, test specifications, and an ADR**
  - **Rubrics**: `Conf:90% | Invest:i | Blast:i | Reversal:i | Fit:i | Reuse:i | Obs:i | Surface:iii | Perf:na`
  - **Approach**: Put concise commands/selection rules in AGENTS.md, detailed tier contracts in test specs, and durable architecture/rationale in one ADR.
  - **Example**: AGENTS.md says when to run test-fast; the frontend/backend specs map WEB-TEST/TEST cases to tiers; the ADR defines shared-infrastructure isolation.
  - **Architecture**: Uses each existing documentation type for its established responsibility.
  - **SSoT**: The ADR owns the decision, specs own test obligations, and AGENTS.md references rather than duplicates them.
  - **System limits**: Not applicable; no runtime API or concurrency limit is introduced.
  - **Trade-offs**: Best traceability and agent usability, but requires synchronized edits across three documentation surfaces.

- **Option B: AGENTS.md and test specifications**
  - **Rubrics**: `Conf:90% | Invest:ii | Blast:ii | Reversal:ii | Fit:ii | Reuse:ii | Obs:ii | Surface:ii | Perf:na`
  - **Approach**: Treat the hierarchy as test methodology rather than a distinct architecture decision.
  - **Example**: AGENTS.md contains operational rules and the canonical test specs define tiers/resource requirements.
  - **Architecture**: Fits existing specification ownership but omits a dedicated rationale record for fixture pooling.
  - **SSoT**: Test specs own definitions; AGENTS.md contains only agent-facing summaries.
  - **System limits**: Not applicable; no runtime API or concurrency limit is introduced.
  - **Trade-offs**: Less documentation overhead, but future fixture architecture changes may lack a durable decision record.

- **Option C: AGENTS.md only**
  - **Rubrics**: `Conf:100% | Invest:iii | Blast:iii | Reversal:iii | Fit:iii | Reuse:iii | Obs:iii | Surface:i | Perf:na`
  - **Approach**: Add a concise methodology section only to the agent instructions.
  - **Example**: AGENTS.md defines cheap, integration, browser, and release gates.
  - **Architecture**: Keeps guidance local but does not update canonical test specifications or architecture records.
  - **SSoT**: AGENTS.md becomes the sole policy source.
  - **System limits**: Not applicable; no runtime API or concurrency limit is introduced.
  - **Trade-offs**: Fastest documentation change, but human and agent test contracts can drift from plans/specifications.

### 8.4 Recommendation

Recommend **Option A**. The decision spans agent behavior, canonical test
coverage, and durable service/test architecture, so each document has a
distinct non-duplicative role.

### 9.1 Question

What accuracy policy should govern cheap tests?

### 9.2 Context & clarification

The stated goal permits sacrificing accuracy to gain speed. The safer precise
interpretation is to sacrifice environmental fidelity while keeping assertions
correct for a narrowly stated claim. For example, LiveViewTest does not prove
CSS layout, but it can precisely prove that toggle-fold updates server state
and renders the expected element.

### 9.3 Options

- **Option A: Preserve oracle correctness and explicitly scope fidelity**
  - **Rubrics**: `Conf:90% | Invest:i | Blast:i | Reversal:i | Fit:i | Reuse:i | Obs:i | Surface:iii | Perf:ii`
  - **Approach**: Every test states the invariant and tier; lower tiers may omit production boundaries but cannot use vague assertions for deterministic behavior.
  - **Example**: A LiveView test proves a fold appears after render_click and records that CSS layout and browser focus remain T4 concerns.
  - **Architecture**: Matches design-by-contract and keeps each boundary’s evidence honest.
  - **SSoT**: Canonical test specs map each invariant to the lowest sufficient tier and any required higher-tier canary.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Strong confidence and diagnosability, but requires disciplined claim wording and tier mapping.

- **Option B: Permit explicit approximate models backed by higher tiers**
  - **Rubrics**: `Conf:70% | Invest:ii | Blast:ii | Reversal:ii | Fit:ii | Reuse:ii | Obs:ii | Surface:ii | Perf:i`
  - **Approach**: Cheap tests may use approximations or tolerances when exact fidelity is expensive, provided a targeted higher-tier test validates the approximation boundary.
  - **Example**: Happy DOM event behavior is accepted as a fast model while one Chromium canary checks the production browser path.
  - **Architecture**: Introduces an explicit model-versus-contract distinction across tiers.
  - **SSoT**: Test specs record each approximation and its corresponding certification test.
  - **System limits**: The acceptable divergence of synthetic DOM behavior is unknown. Unknown - not available in local context.
  - **Trade-offs**: Maximizes cheap coverage, but approximation drift can create false confidence unless mappings remain current.

- **Option C: Decide accuracy requirements independently per test**
  - **Rubrics**: `Conf:90% | Invest:iii | Blast:iii | Reversal:iii | Fit:iii | Reuse:iii | Obs:iii | Surface:i | Perf:iii`
  - **Approach**: Avoid a repository-wide accuracy rule and let each test author choose its fidelity and assertion style.
  - **Example**: Test comments explain locally whether snapshots, partial matches, or exact assertions are sufficient.
  - **Architecture**: Adds no central contract and relies on code review.
  - **SSoT**: Each test file owns its own interpretation.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Flexible and low-overhead, but inconsistent evidence is particularly difficult for coding agents to interpret.

### 9.4 Recommendation

Recommend **Option A**, with Option B’s tolerances allowed only when the
underlying domain is genuinely approximate and the approximation is recorded
against a higher-tier certification test. This sacrifices fidelity while
preserving clear, correct evidence.
