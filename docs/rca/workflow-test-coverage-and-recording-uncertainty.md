# 1. Workflow coverage and recording uncertainty

Issue #122 implements the approved standards-informed audit, recorded in
[the phase plan](../../plans/test-coverage-improvement-plan.md). The source
baseline is `821116a1c6ae18f678b040172fce89c2da39ebe3`; the running application
at the start was `0773784111baf5858fb799a730e169dfb052a3c1`. These identities
are deliberately separate.

## 2. Gaps and root causes

The existing harness already covered native request preparation, deterministic
LiveView behavior, PostgreSQL and real provider qualification. It missed several
compositions: each preset's execution after its own reload, native recovery
across protocols, simultaneous conflicting saves, and connection loss before
versus after a terminal result. The old matrix reloaded only its final preset;
the native success fixture also ignored malformed request JSON. Some newer
canonical case IDs were absent from task registration even though broad commands
ran their source files. None of those assertions alone established complete
workflow coverage.

The new terminal-versus-recording regression reproduced a frontend error:
`WorkspaceLive.handle_async/3` selected its message from `APIError.ambiguous?`
without consulting the terminal result already received through the stream.
It could show unknown inference or malformed response after a known result,
when the remaining uncertainty concerned history confirmation. The result was
already preserved correctly; the defect was its explanation.

The actual transport test then exposed `statusWriter.Flush` discarding flush
errors (and reporting success for unsupported flushing). The stream controller
could not observe the failed flush and cancel a pending call. TEST-465 reproduced
that root invariant in the fast lane before changing the wrapper to directly
return `ResponseController.Flush()` errors.

## 3. Corrections and independent oracles

TEST-458/460 reject malformed native requests and missing source registration or
feasible interactions. The native oracle reads provider user-input fields, not
arbitrary metadata, and checks submitted starting inputs against Git presets.
Static closure verifies selection/ownership; runner reports prove execution.
TEST-454 now performs its existing 80 preset calls after each preset's reload,
retaining the exact six-stage routing assertion.

TEST-459/461/462 cover public admission, duplicate/stale events, durable latest
selection, autosave coalescence, capability partitions and recovery witnesses.
All 500 native source/target/position/failure/budget combinations execute locally
with distinct selected-target and result-producer assertions. TEST-463 holds
actual concurrent SQL writers and provider probes at owned barriers, requiring
one complete CAS winner and rollback of the losing credential save.

TEST-464 closes actual owned HTTP connections during inference and after terminal
delivery, retains the real client/decoder and reads back exact PostgreSQL records.
The fast companion independently checks a missing recording confirmation. The
frontend production edit consults the current terminal result and recording
acknowledgement before choosing the error message. A full failed stream with
confirmed recording retains its ordinary failure message. It changes no controls,
layout, inference, retry or storage policy.
The gateway production edit propagates the existing flush failure; cancellation
and detached recording retain their existing ownership and budgets.

## 4. Test-development failures retained

The malformed-request fixture and missing registration produced deliberate RED
evidence. The first reload test incorrectly drove the stopped original view and
hit a real revision conflict; it now drives each reloaded view. The initial
native recovery assertion conflated original selected target with actual output
producer; both canonical fields are now independently asserted. The first
starting-options check expected only per-call overrides rather than the declared
default options plus overrides. The initial transport provider fixture returned
plain JSON despite a streaming request; it now uses the existing native formatter.
These were test assumptions, not application regressions or weakened assertions.
The unchanged serial policy also rejected a third module; the new transport cases
now share the existing workflow module, and account for persisted fold state.

Read-only review also caught the first message fix being too broad: a normal
failed terminal plus saved history must not claim recording uncertainty. A new
cheap RED reproduced that unreleased regression; the warning now requires both
a known terminal and an absent recording acknowledgement.

Race certification exposed a test observation error: the lock-owning transaction
cached its first `pg_stat_activity` snapshot. The non-race run happened to see both
writers initially, while the race run repeatedly saw the earlier state. The test
now calls `pg_stat_clear_snapshot()` before observing overlap, using the same owned
connection and deadline. This follows [PostgreSQL 17's documented statistics
snapshot behavior](https://www.postgresql.org/docs/17/monitoring-stats.html#MONITORING-STATS-VIEWS).
The actual overlap, one-winner/conflict, full payload and foreign-scope oracles
remain unchanged; no storage behavior or timeout is changed.

The first exact merged-source release, run 38568845136 at
`b361d2344ea2b47797b3d05b0432d62e62efaeaf`, failed in three existing stateful
cases at `referencePool` setup before their assertions. The bounded fixture Ping
in PR #124 passed local integration/race, but exact-main release 38572926810 at
`2e65c8094aaa9289c5d97356ec8a0ca0c3889ea6` still failed inside schema application
for two existing cases. Both failed reports are retained; neither was retried or
deployed. The second run falsifies connection warm-up as a sufficient correction.

`ApplySchema` incorrectly reused the short request transaction helper: its 600ms
parent budget included acquiring a connection, inspecting the version, creating
all tables/indexes and committing. The accepted short limits protect ordinary
profile/draft/cache/list/status requests; administrative initial DDL is a separate
lifecycle. TEST-466 reproduces the wrong deadline offline through the actual
pgx pool acquisition tracer, canceling before network access. It fails at the
600ms schema deadline and passes with a fixed 30s administrative budget. Earlier
caller deadlines still win; short requests retain 600/500ms and inference/history
budgets do not change. Both policies share one transaction implementation and
parameterized transaction-local SQL settings. No retry or serialization is added.

This is an explicit production schema-lifecycle correction beyond the original
two stream fixes, not just a test warm-up. Remove the unsuccessful Ping. TEST-424
retains fresh/idempotent/mismatch/partial-failure assertions and adds real SQL
setting/reset checks plus caller-canceled partial DDL rollback. No table, index,
data reset or schema version changes. The corrected exact-source release 38575382378 passed; its evidence and actual
production application are recorded below. Local success alone was not used to
certify the merged candidate or production startup.

Private Mix compilation chatter initially displaced useful failures from bounded
runner previews. The launchers now log the ExUnit section; compile-only failures
retain their full diagnostic output. This does not alter test selection or results.

## 5. Verification and rollout

The [acceptance ledger](../../plans/evidence/harden-llm/workflow-test-coverage/acceptance.md)
owns final commands, reports, merged source, exact release and deployed identity.
PRs #123–#125 are merged. Application `4ad4072425a0e67ad48b3c5170008a1a362004e6` passed
[release 38575382378, attempt 1](https://github.com/prls-co/harden-llm/actions/runs/38575382378/attempts/1) with all 29 tasks
and no cleanup issues, plus exact-main fast and CodeQL. Both locally built images
are deployed at `https://harden-llm.prls.co/`, healthy with zero restarts; the ledger
records their immutable IDs. Schema-before-apps, scoped/full equivalence, public
HTTP/auth, Portal login and all 40 preset definition/input comparisons passed.
Thirty presets are credential-configured; no additional paid qualification ran.
Shared services, credentials, upstream metadata and routing are unchanged. No
production data correction, provider replay or browser launch occurred. The
operator preset-readback script's initial incorrect GET envelope assumption was
corrected to the canonical direct `ProfileDraft`, preserving its value oracle.

## 6. Limits and follow-up

The standards supply test-design techniques, not formal conformance or
organizational IV&V. The static checker is intentionally a source-selection
policy, not a language parser or runtime per-case instrumentation system.
Current finite domains and incidents are covered; future provider/API changes
still require explicit qualification. Browser layout/native events, additional
paid canaries and off-host restoration remain separate work.
