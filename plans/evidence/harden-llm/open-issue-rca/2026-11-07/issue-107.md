# Issue #107: RCA and solution options

## 1. Scope and evidence

Analysis on 2026-11-07 against Harden `3f8a5173086ea245841551fe988f6a3b03cff7f8`. This is analysis only. No application fix, main-branch change, deployment, browser test or paid inference is claimed. Local witnesses assert the observed current behavior, not that the requested feature is fixed.

## 2. Causal chain

- The overlap fixture creates four barrier tasks with 30-second task deadlines at [scripts/test/run_test_tier_test.mjs:430–439](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/scripts/test/run_test_tier_test.mjs#L430-L439). Resource limits start two CPU workers and one service worker before release.
- Each synthetic worker polls the release file indefinitely at [scripts/test/run_test_tier_test.mjs:39–42](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/scripts/test/run_test_tier_test.mjs#L39-L42). The supplied duration is ignored in this mode. Its SIGTERM/SIGINT handlers run only if those workers themselves receive the signal (lines 27–28).
- [scripts/run-test-tier.mjs:1180–1188](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/scripts/run-test-tier.mjs#L1180-L1188) wraps each command in GNU time and creates a detached process group. The parent holds the timeout and abort handlers at [scripts/run-test-tier.mjs:1200–1217](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/scripts/run-test-tier.mjs#L1200-L1217). Killing that parent removes the timers; detached descendants receive no automatic parent-death signal.
- The fixture's `finally` at [scripts/test/run_test_tier_test.mjs:458–461](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/scripts/test/run_test_tier_test.mjs#L458-L461) releases workers on ordinary assertion/error unwinding. Process termination does not unwind it.
- Signal-to-AbortController wiring exists only in executable `main()` at [scripts/run-test-tier.mjs:1601–1622](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/scripts/run-test-tier.mjs#L1601-L1622). The test imports `runTasks` directly through `runFixture` (test lines 130–138), supplies no AbortSignal, and never enters that CLI owner. `runTasks` only forwards an optional supplied signal at [scripts/run-test-tier.mjs:1402–1409](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/scripts/run-test-tier.mjs#L1402-L1409).

**Reproduced mechanism:** an importing parent launched three barrier workers through the real `runTasks`. SIGTERM of that parent exited it with signal 15. After 3.5 seconds, beyond the experiment's 3-second task deadline, all three Node workers and all three GNU time wrappers were alive; wrappers had PPID 1. PID plus /proc start identities were recorded. Writing the owned release barrier left **zero live owned descendants**. Existing overlap, AbortSignal cancellation and timeout controls also passed (3 Node tests). No unrelated process was killed.

**Limit:** this proves a sufficient failure path matching the observed six-process shape. The October incident's exact original signal/outer supervisor behavior remains unproven; SIGKILL would also bypass JavaScript cleanup.

## 3. Options and recommendation

1. **Recommended for graceful interruption:** give the test process one signal/cancellation owner and pass its AbortSignal through `runFixture` to every `runTasks` call. On cancel, terminate and join owned groups using the runner's existing process-group path; release the fixture barrier as teardown fallback. Keep CLI/library ownership explicit rather than adding independent signal listeners to every runner invocation. Preserve the overlap/resource assertions.
2. Add an owned, finite fail-safe deadline to the barrier fixture if abrupt parent death must also be contained. This is fixture-specific protection; it must fail loudly and stay longer than the assertion/cancellation budget. It cannot replace testing graceful cleanup.
3. An external process/container supervisor could contain SIGKILL, but is broader than this harness issue. Do not add a daemon, global sweeper or name-based kill.

Cancellation currently sends SIGTERM only, while timeout adds SIGKILL after 2 seconds (runner lines 1202–1209). If supporting TERM-resistant descendants is required, unify bounded TERM→KILL escalation under that same owner; do not claim the existing abort path already provides it.

## 4. Verification and blockers

Add subprocess regressions for graceful parent SIGTERM/SIGINT, explicit AbortSignal, ordinary failure and timeout. Assert PID/start identity disappearance within the existing cancellation budget and no owned files/processes left; retain resource-overlap oracle. Test SIGKILL separately only if that contract is adopted. No browser/provider/Docker is needed.

No user input is needed for the ordinary signal-ownership fix. Exact historical interruption attribution needs the original outer-runner logs, if available; it does not block fixing the demonstrated path. Effort: bounded harness change, medium confidence until cancellation regressions pass.
