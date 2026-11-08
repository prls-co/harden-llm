# Issue #142: RCA and solution options

## 1. Scope and evidence

Analysis on 2026-11-07 against Harden `3f8a5173086ea245841551fe988f6a3b03cff7f8`. This is analysis only. No application fix, main-branch change, deployment, browser test or paid inference is claimed. Retained incident evidence and fresh observations are identified separately.

## 2. Finding: no demonstrated analytics defect

This is an evaluate-before-building issue, not evidence that missing dashboards caused an incident. The current code already answers the main operational distinctions:

- **Was the provider entered, stopped and settled?** [stateful/cpa_admission.go:289–300](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/stateful/cpa_admission.go#L289-L300) logs ordinary dispatch identity, admission/outcome, local stop, settlement acknowledgement, wait/generation timings and available usage. [stateful/batch/attempt_execution.go:90–96](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/stateful/batch/attempt_execution.go#L90-L96) records the corresponding durable observation and safe error code.
- **Why is the pool waiting?** [stateful/batch/pool_progress.go:126–161](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/stateful/batch/pool_progress.go#L126-L161) explicitly distinguishes unresolved execution, persistence, rollover, account restriction, worker availability, provider throttling, local admission window, dispatch pending and concurrency capacity. [stateful/batch/pool_workflow.go:347–362](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/stateful/batch/pool_workflow.go#L347-L362) logs changed waiting states with eligibility/window/rate/permit context.
- **Is an ordinary request stale or unconfirmed?** [stateful/request_tracking.go:11–30](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/stateful/request_tracking.go#L11-L30) exposes stage/reason/deadline/attempts/error; lines 49–66 compute freshness and mark an unobserved expired execution unconfirmed. This is observation, not an inference replay trigger.
- **Where are classified status/code retained?** [stateful/batch/types.go:97–123](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/stateful/batch/types.go#L97-L123) carries safe HTTP status/code, timings and usage in outcomes. [stateful/batch/activities.go:215–260](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/stateful/batch/activities.go#L215-L260) distinguishes throttling, account restrictions and partial-output ambiguity. [stateful/batch/provider_observation.go:31–48](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/stateful/batch/provider_observation.go#L31-L48) prevents ambiguous ordinary outcomes authorizing retry.

**Concrete logging gap, not yet a justified product change:** the legacy event at [stateful/batch/activities.go:93–105](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/stateful/batch/activities.go#L93-L105) emits both code and HTTP status; the independent durable event at attempt_execution.go:96 emits code but not HTTP status; ordinary cpa_admission.go:300 emits neither classified code nor HTTP status. The facts exist in retained outcomes but not uniformly in every log line. The gap is event projection, not absent classification. Adding an analytics subsystem would not itself repair a missing event field or reveal unknown CPA internal attempts.

Fresh focused controls passed: `TestAttemptClassificationSeparatesQuotaAccountAndOrdinaryFailures` and `TestPoolWaitingProjectionUsesActualGateAndNoInventedETA`. They prove deterministic classification/projection, not production log retention or a measured capacity ceiling. No fresh incident, unavailable historical query or evidence-loss case was supplied that requires broader analytics. The older capacity campaign's unconfirmed outcomes do not become recoverable merely by adding dashboards.

## 3. Options and recommendation

1. **Recommended disposition on present evidence:** existing logs/status/attempt evidence cover the identified waiting/outcome questions; do not build additional capacity analytics. Record that bounded conclusion and close/defer this speculative enhancement through normal triage. This does not claim every future historical analytics question is answered.
2. If a real investigation cannot join retained outcomes to logs, add only the already safe classified `http_status`/`error_code` to the existing ordinary/independent events, with redaction and event-assertion tests. First retain the exact incident/time/request/permit and missing question that justifies it.
3. Central retention, dashboards, metrics, alerts or auto-tuning require a separate demonstrated need and cost/retention decision. They are not prerequisites for normal CPA execution or this issue's conclusion.

## 4. Verification and blockers

For a concrete incident, join request/job/custom item/permit identities to retained outcomes and changed pool states. State “not observed” for missing provider-internal attempts/quota; local configured concurrency or admission windows do not establish the upstream ceiling. Existing bounded Docker-log retention may not answer older questions; current live retention/export was not audited here.

No implementation blocker or user input is required for the present no-build recommendation. **Optional input:** a specific recurring question or incident (time range plus safe IDs) that current logs/status cannot answer would change the assessment. This analysis does not invent one to justify work. Potential quick administrative closure; not an application/security blocker.
