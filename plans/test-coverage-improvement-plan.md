# 1. Workflow test coverage improvement

- ID: `PLAN-HLLM-WORKFLOW-COVERAGE-001`.
- Approved: 2026-10-13, following the four standards-informed audits.
- Baseline: `821116a1c6ae18f678b040172fce89c2da39ebe3`.
- Scope: existing Go, LiveView, Node, local HTTP and PostgreSQL harnesses.
- Browser checks, additional paid calls and off-host restore drills remain separate.

## 2. Implementation and independent oracles

| Phase | Step / test IDs | Required evidence |
| --- | --- | --- |
| P00 | Registration and matrix completeness, TEST-460 | Removing a required registration/consumer or interaction row fails. Existing broad tasks still execute cases; no second registry or scheduler. |
| P01 | Strict native fixture, TEST-458; per-preset reload, TEST-454 / WEB-TEST-126 | Literal native protocol expectations reject malformed/wrong requests; every preset's existing two calls execute after reload, retaining 80 matrix calls and exact six-stage routing. |
| P02 | Run admission / ordered async events, TEST-459 / WEB-TEST-128 | Rejected direct events make zero POSTs; a blocked duplicate makes exactly one; late selection/completion never overwrites current state; pending autosaves preserve the latest full payload. |
| P03 | Capability partitions and recovery witnesses, TEST-461/462 / WEB-TEST-129 | Explicit expected maps and field paths; partial/absent maps, inherited targets, unknown references and native-option conflicts; exact early-stop preparation/dispatch/wait and budget outcomes. Small cheap domains are exhaustive. Cross-protocol cases use native parsers and local effects. |
| P04 | Concurrent CAS and interrupted transport, TEST-463/464/465 / WEB-TEST-130 | Process-owned barriers, one CAS winner, complete winning state and losing transaction rollback; actual client/gateway interruption preserves known output and never replays inference. TEST-465 adds the cheap flush-error invariant found by T3. |
| P05 | Verification and closeout | Focused checks, fast gate, affected T3/race and browser-free release selection; updated catalog, ADR-HLLM-015, evidence and issue. Commit/push/merge; rebuild/deploy only if a production defect requires an application change. |

Each step adds regression assertions before changing production behavior. Keep
intentional RED evidence when a missing guard or defect is found. Cases that
exercise already-correct behavior need no artificial failing implementation.
Use process-owned acknowledgements rather than sleeps. Do not weaken an oracle,
retry an ambiguous run or use production conversion to calculate expectations.

## 3. Coverage and standards boundaries

Use 29119's explicit test bases/partitions/transitions, selected MC/DC-style
independence witnesses, 1012's contract-versus-workflow distinction, and NIST's
measured feasible interaction coverage. These are engineering techniques;
formal conformance and organizational IV&V independence are not claimed.

Registration stays in `test/test-tiers.json`; specifications and executable
markers define its closure. Matrix definitions stay with their existing fixture
owners; an independent enumerator checks required interactions. Keep named
production incidents in addition to systematic rows. Invalid API values remain
negative tests rather than being excluded as inconvenient combinations.

## 4. Progress

- P00: complete. Real registration RED identified TEST-452 missing from the manifest; the reviewed closure now registers the current cases, rejects missing consumer/gate bindings and validates the retry model independently. Focused Node cases and existing Go tier-policy test pass.
- P01: complete. Native malformed-JSON RED is retained; the per-preset reload test initially retained the old view revision and correctly hit CAS. Driving the reloaded view fixes the test lifecycle without changing the application. All 80 runs and the six-stage chain pass strict native oracles; cleanup is clean.
- P02: complete. Fourteen focused LiveView cases pass, including controlled duplicate/late responses/autosaves and endpoint Save admission.
- P03: complete. Capability projections/pickers, independent option/guard witnesses, early exits/deadline boundaries and all 500 native cross-protocol/position/failure/budget cases pass. The first native test incorrectly conflated selected target with result producer; its corrected oracle separately asserts both canonical fields. No production behavior changed.
- P04: complete. Concurrent SQL CAS, actual transport/record readback and cheap flush/error-message regressions pass; final race certification is in P05.
- P05: in progress. Final fast, T3 and integration-race gates pass with no cleanup issues; merge, exact-source release and scoped application deployment remain.
- Evidence: `plans/evidence/harden-llm/workflow-test-coverage/`.
- Protected operator scratch: outside Git; no production credentials or user
  records are needed by these tests.

## 5. Findings and justified scope changes

T3 found the logging writer concealing flush errors; TEST-465 reproduces the root
invariant at T1 before its direct `ResponseController` forwarding fix. The fast
terminal test found a misleading post-terminal error; read-only review added a
full failed-stream/saved-recording regression before narrowing its correction.
These two defects require gateway and frontend images after exact-source release
certification. They change no UI controls, layout, storage model or retry policy.

The transport cases share the existing serial workflow module and tagged Go
launchers; adding a third serial module was rejected by the unchanged policy.
Test setup accounts for persisted fold state. Source closure explicitly verifies
selection/ownership rather than runtime execution. Reports and independent
dispatch/history assertions establish the latter. No extra service/framework,
browser, paid call, timeout increase or relaxed concurrency rule was introduced.

The first race gate exposed PostgreSQL's transaction-local activity snapshot in
the overlap observer. Clear that snapshot before each observation on the same
lock-owning connection; retain both real waits and the unchanged CAS oracles.
The failed race report and corrected report belong in P05 evidence.

The first exact-source release failed at existing `referencePool` setup, before
three assertion bodies. Add one bounded readiness `Ping` for the fixture's lazy
pool before real schema application. Retain the failed report and require a new
merged-SHA release; do not retry that failed candidate, raise production budgets,
serialize assertions or claim cold production startup from warm fixtures.
