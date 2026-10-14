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
| P04 | Concurrent CAS and interrupted transport, TEST-463/464 / WEB-TEST-130 | Process-owned barriers, one CAS winner, complete winning state and losing transaction rollback; actual client/gateway interruption preserves known output and never replays inference. |
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
- P01–P05: pending.
- Evidence: `plans/evidence/harden-llm/workflow-test-coverage/`.
- Protected operator scratch: outside Git; no production credentials or user
  records are needed by these tests.
