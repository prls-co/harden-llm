# Issue 107: owned process interruption

## 1. Cause and change

The importing Node test harness did not own SIGTERM/SIGINT cancellation. Its
detached GNU time/worker groups survived when the test process exited, taking
their task-timeout timers with it. The retained initial reproduction is on
`research/open-issue-rca-20261107`, commit `43cb25d`.

The CLI and importing harness now explicitly own the same signal-to-abort
helper. The harness supplies that signal to every fixture invocation, joined
with any test-specific AbortSignal. Task and auxiliary command cancellation
use the existing two-second TERM-to-KILL budget and handle a signal already
aborted during setup. Disposal occurs after owned work is joined. No global
sweeper, name-based kill or detached supervisor was added.

## 2. Verification

- Three subprocess regressions pass: SIGTERM, SIGINT and TERM-resistant workers.
  Each starts three Node workers plus three GNU time wrappers, records PID/start
  identities, and requires the importing owner to join all six before exiting
  within five seconds, well before its 30-second task timeout. The outer test
  cleans only identity-matched resources even if the assertion fails.
- The complete manifest runner-contract command passed 60 tests with zero
  failures, skips or cancellations. It includes unchanged overlap/resource-slot,
  first-failure, timeout, report, resource-lifecycle and coverage oracles.
- `git diff --check` passed. No application build, browser, real Docker service
  lifecycle, provider call or production deployment was performed. The runner
  lifecycle tests use their existing fake Docker executables.
- SIGKILL of the owner cannot run JavaScript cleanup and is explicitly outside
  this graceful-interruption contract. The original incident's exact signal is
  still unknown; the reproduced failure mechanism is fixed by this change.

## 3. Publication gate

This change is prepared on `fix/tier-runner-interruption-107`. Main remains
unchanged per the user's earlier constraint. Issue closure waits for the
reviewed change to be merged; source preparation and local passing tests do
not establish merged-main adoption.
