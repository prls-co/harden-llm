# ADR-HLLM-026: Recovery Closeout Verification and Release Intent

- Status: Accepted and implemented with the recovery closeout release.
- Date: 2026-09-21.
- Requirements: REQ-331 through REQ-340 in `plans/recovery-production-closeout-plan.md`.
- Verification: TEST-260 through TEST-270 and WEB-TEST-100 through WEB-TEST-102.
- Related: ADR-HLLM-024 and ADR-HLLM-025.

## Context

The previous production receipt described a web image at a newer revision while
the gateway remained on an older revision. The production descriptor was
internally equivalent, so an ordinary configuration check could not detect the
omitted gateway deployment. Separately, the request-bound SSE admission path
could consume a completed outcome while selecting a ready signal, then wait for
that outcome a second time. Existing repeated requests did not force this
ordering. Nested recovery panels displayed inheritance text without the
effective shared retry values.

## Decisions

1. `production-config` accepts an optional, explicit, full 40-hex
   `--expected-release` for selected application services. Candidate checks
   compare the descriptor, desired image digest, OCI release label, release
   environment, running image identity, and health. The option is independent
   of descriptor contents and cannot be combined with `--resolve-only`.
2. SSE admission carries an optional pending outcome into one terminal
   finalizer. Completion is authoritative over a simultaneous expiry; without
   an outcome, the existing execution deadline produces the existing timeout
   shape. Request cancellation and write failure cancel execution. Producer
   channels retain one close owner and no new public API is introduced.
3. The complete profile widget renders retry controls in editable root mode or
   inherited disabled mode. Nested values are the current root draft, emit no
   form names/events, and offer an instance-scoped navigation action to the
   owning root editor. Retry budget ownership remains root-only.
4. TEST-269 is an explicit operator acceptance check and TEST-270 is the
   existing release graph; neither is silently added to a normal fast lane or
   an automatic browser/provider check.

## Threshold and evidence policy

The focused lifecycle watchdog is two seconds per controlled case and the
existing command, integration, and release budgets remain unchanged. A
watchdog failure is evidence of a lifecycle defect, not permission to increase
a timeout. Reports contain bounded IDs, durations, statuses, counters, image
identities, and health states; prompts, outputs, credentials, bearer/session
data, and full environments are excluded.

## Consequences and rollback

The release gate can reject a stale but self-consistent descriptor before any
Compose mutation, and a successful production receipt names both application
services. Local channel tests prove forced event order while integration tests
retain the real RunService/Postgres boundary. No polling job, database/schema
migration, provider policy, browser dependency, or per-target retry budget is
added.

Rollback is service-scoped: restore the recorded descriptor/image identity for
the affected gateway or web service, apply with the same explicit expected
release, and rerun the candidate check and health/auth probes. If a migration
appears in the candidate diff, stop and use the matching database/configuration
checkpoint rather than an image-only rollback.
