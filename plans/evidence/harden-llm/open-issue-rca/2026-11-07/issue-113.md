# Issue #113: RCA and solution options

## 1. Scope and evidence

Analysis on 2026-11-07 against Harden `3f8a5173086ea245841551fe988f6a3b03cff7f8`. This is analysis only. No application fix, main-branch change, deployment, browser test or paid inference is claimed. Retained incident evidence and fresh observations are identified separately.

## 2. RCA boundary and fresh platform readback

The failure occurred before CodeQL received a runner, so no repository application line or executed analysis step can be identified as the cause.

- Incident 1: [run 38442596076](https://github.com/prls-co/harden-llm/actions/runs/38442596076), source `b3a4f52a913c531cc20c19615a5b0eee97cbb7cc`; JavaScript job `115452708168` remains queued, conclusion null, runner null and zero steps on fresh API readback. Actions/Go succeeded.
- Incident 2: [run 38459380313](https://github.com/prls-co/harden-llm/actions/runs/38459380313), source `1a6ed34e8a674640f05b097c7dff34f5d75fe655`; Actions job `115510280938` likewise remains queued, conclusion null, runner null and zero steps; Go/JavaScript succeeded. A synthetic `started_at` value does not prove runner execution when runner/steps/completion are absent.
- The committed [first incident record](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/plans/evidence/harden-llm/reference-ui-regression/codeql-prestart-incident.json) and [second incident record](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/plans/evidence/harden-llm/preset-workflow-qualification/codeql-prestart-incident.json) retain rejected rerun operations, identical passing PR/merge trees and unchanged settings during those incident investigations. The old rejected mutations were not repeated today.
- **Current source is independently green:** [run 42435416572](https://github.com/prls-co/harden-llm/actions/runs/42435416572) on `3f8a5173086ea245841551fe988f6a3b03cff7f8` completed successfully; fresh job readback shows Python, Go, Actions and JavaScript-TypeScript all succeeded. Eight latest dynamic runs retrieved also succeeded; this is a bounded recent sample, not proof of no other past recurrence.
- Current default setup API reports configured, standard runner, weekly/default suite, remote threat model, last updated 2026-11-04. Current settings have advanced since the October snapshot; this analysis made no setting changes and does not claim they were unchanged for the entire intervening period.

**Root-cause confidence:** verified failure boundary is GitHub-generated `dynamic/github-code-scanning/codeql` scheduling/orchestration before runner assignment. The underlying GitHub service defect/reason is **unknown**. A repository workflow change or JavaScript patch cannot be inferred from a job that executed zero steps. Different languages failing on equivalent passing trees further weakens a source-specific explanation, but does not identify GitHub's internal cause.

## 3. Options and recommendation

1. **Recommended:** preserve both historical failures and close this repository investigation as a recovered external-platform incident with unknown deeper cause, or transfer it to a GitHub support record if deeper RCA is required. Keep current revision scans as the release gate. Do not relabel the old jobs as passes.
2. Ask the GitHub service owner/support to correlate these run/job/check-suite IDs and explain the inconsistent terminal-run/queued-job state. This is the only available route to a platform-internal RCA. Link the support case here if created.
3. Switch from default to explicit advanced CodeQL setup only if recurring operational failures justify owning that configuration. It is a larger change and is not proven to fix runner orchestration. Weakening languages/checks, empty commits or repeated reconfiguration solely to hide the incident are not solutions.

## 4. Verification and blockers

Fresh commands were read-only: default-setup API, dynamic run list, original jobs and current-source jobs. No rerun, scan-language change, app rebuild or application test was necessary for this platform investigation.

**Blocker to deeper RCA:** GitHub internal scheduler/control-plane logs or a support explanation, unavailable through these repository APIs. No code-access or credential request is needed. **User decision only if desired:** whether to pursue support attribution rather than accept a documented unknown-cause recovered incident. This no longer blocks current main security checks; administrative closure is reasonable, but is not a claimed platform fix.
