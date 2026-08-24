# ADR-HLLM-015: Resource-aware parallel test-feedback hierarchy

- Status: Accepted for implementation
- Date: 2026-08-23
- Scope: `PLAN-HARDEN-LLM-TEST-FEEDBACK-002`
- Requirements: REQ-001 through REQ-018
- Initial verification: TEST-048
- Evidence record: `KER-HLLM-TEST-FEEDBACK-001`

## Context

Harden-LLM combines default-tag Go tests, real service integration, Phoenix
LiveView tests, browser sessions, Compose certification, and live-provider
checks. They do not have the same fidelity or resource cost. The coding loop
needs broad cheap feedback, while release confidence still needs the existing
high-fidelity boundaries.

## Decision

Accept one machine-readable task manifest at `test/test-tiers.json` and one
dependency-free benchmark harness at `scripts/benchmark-test-feedback.mjs`.
Tasks are classified T0 through T5 and carry one command, resource class,
timeout, cleanup owner, network policy, credential-key declaration, and
canonical test identifier. The canonical Make targets remain unchanged; new
hierarchy targets are additive.

T0-T2 work is offline and credential-free. CPU work may overlap within the
measured slot cap. Postgres/Garage integration uses pooled services only after
unique namespace, cleanup, and contamination tests pass. Destructive Garage
restart behavior remains exclusive. Chromium, Compose, release, and live
provider work remain bounded higher-fidelity lanes rather than part of the
fast loop.

Server-owned LiveView behavior is tested through public LiveView events and
rendered diffs. Pure client decisions are extracted into a dependency-free
JavaScript module and tested by Node's built-in runner. No initial synthetic DOM dependency such as happy-dom or jsdom is accepted: the thin hook adapters,
native events, CSS, focus, and LiveSocket patching remain browser-owned facts.

Every higher-tier defect is evaluated for a lower-tier root-invariant
regression. Lower fidelity may replace an environmental boundary, never the
assertion oracle. A failed or leaked benchmark sample is not a timing datum.
Thresholds are derived from the fingerprinted KER samples and may change only
with new evidence and an ADR amendment.

## Consequences

The repository gains one policy source, one benchmark/evidence path, explicit
resource ownership, and a repeatable T0-T2 loop. Browser and service startup
cost is isolated from ordinary coding feedback. The runner and KER must keep
bounded logs and cleanup evidence, and unlike hosts must report rather than
silently enforce reference-host budgets.

The two named frontend serial exceptions are the SessionVault lifecycle/clock
case and global observability application configuration. A third exception,
additional browser feature, DOM emulator, threshold relaxation, or pooling
fidelity change requires a new decision record or an amendment before merge.

## Alternatives rejected

- Keep all tests serial: preserves cost and hides safe concurrency capacity.
- Put all tests in one parallel pool: permits resource contention and makes
  failures difficult to attribute.
- Add happy-dom or jsdom immediately: adds runtime/dependency cost without
  proving the browser-owned boundaries the hooks depend on.
- Reduce assertions or retry failed samples: would change test purpose and
  invalidate performance evidence.

## Rollback

Remove or disable only the additive hierarchy targets and runner selection;
the existing Make targets, test files, service fixtures, and release gates
remain the rollback path. A future incompatible manifest/KER schema requires
an explicit ADR and schema version change.
