# ADR-HLLM-015: Resource-aware parallel test-feedback hierarchy

- Status: Accepted for implementation
- Date: 2026-08-23
- Scope: `PLAN-HARDEN-LLM-TEST-FEEDBACK-002`
- Requirements: REQ-001 through REQ-018
- Initial verification: TEST-041, TEST-042, TEST-043, TEST-044, TEST-045, TEST-046, TEST-047, TEST-048, TEST-049, TEST-050, TEST-051, TEST-052, TEST-053, EVAL-002, EVAL-003, EVAL-004, EVAL-005, EVAL-006
- Evidence record: `KER-HLLM-TEST-FEEDBACK-001`

## Context

Harden-LLM combines default-tag Go tests, real service integration, Phoenix
LiveView tests, browser sessions, Compose certification, and live-provider
checks. They do not have the same fidelity or resource cost. The coding loop
needs broad cheap feedback, while release confidence still needs the existing
high-fidelity boundaries.

## Decision

Accept one machine-readable task manifest at `test/test-tiers.json` and one
dependency-free execution runner at `scripts/run-test-tier.mjs`. The benchmark
at `scripts/benchmark-test-feedback.mjs` is an evidence adapter over that
runner, and Make targets are thin delegates rather than a second task list.
Tasks are classified T0 through T5 and carry one command, resource class,
timeout, cleanup owner, network policy, credential-key declaration, and
canonical test identifier. The canonical Make targets remain unchanged; new
hierarchy targets are additive.

T0-T2 work is offline and credential-free. On the reference host, fast CPU
work overlaps within a measured four-slot cap; EVAL-002 accepted that cap with
warm p95 wall time 8078 ms versus a 20495.2 ms budget and zero failures/leaks.
The KER's accepted RSS budget, including its documented 25% headroom, is the
operational pressure boundary. Postgres/Garage integration uses pooled services only after
unique namespace, cleanup, and contamination tests pass. Destructive Garage
restart behavior remains exclusive. Chromium, Compose, release, and live
provider work remain bounded higher-fidelity lanes rather than part of the
fast loop.

Server-owned LiveView behavior is tested through public LiveView events and
rendered diffs. Pure client decisions are extracted into a dependency-free
JavaScript module and tested by Node's built-in runner. No initial synthetic DOM dependency such as happy-dom or jsdom is accepted: the thin hook adapters,
native events, CSS, focus, and LiveSocket patching remain browser-owned facts.

Deterministic LiveView tests use private Req ownership by default. The shared
test boundary explicitly allows each spawned LiveView process to access its
test stub and stops the LiveView/proxy during teardown, so async work does not
outlive the Req owner. Exactly two deterministic modules remain serial: the
SessionVault lifecycle/clock case and global observability application
configuration. EVAL-003 passed all ten fixed seeds with zero ownership or
cleanup failures and warm p95 of 4125 ms against the 10033 ms sequential
frontend reference.

The extracted client decision core is measured as an independent T0 task.
EVAL-004 passed 30 warm samples at 495 ms p95 and 120.3984375 MiB maximum
RSS, with no package installation or network attempt. Its production import
is the only decision path; hooks retain browser effects and listener teardown.

Ordinary real-browser coverage is exactly two named serialized canaries:
`widget_canary_test.exs` and `authenticated_workflow_canary_test.exs`. The
old monolithic ordinary workflow is removed, while the Compose feature remains
an explicitly separate higher-cost feature and is not counted as an ordinary
canary. The browser task uses the pinned
`harden-llm-browser-test:local` image, host networking, the Docker socket, and
2 GiB shared memory. EVAL-005 passed five warm samples with two Chromium
sessions per sample, p95 wall time `39621 ms`, peak RSS
`403.08203125 MiB`, and zero failures, leaks, screenshots, or remaining
browser containers. These results are below the unchanged P00 browser budgets.

The canary's public LiveView event path found and corrected a production
parameter spelling mismatch: a `phx-value-api-key` attribute arrives as
`api-key`, so the SecretStager handler accepts that actual event key while
retaining write-only staging and redaction. Browser-specific evidence checks
the exact feature inventory, session count, successful completion, screenshot
absence, and cleanup rather than treating a zero exit code as sufficient.

P05 accepted ordinary service pooling after TEST-042 proved concurrent
Postgres database and Garage key-prefix isolation, cross-namespace read/write/
delete/list rejection, release-one/retain-one behavior, injected child failure,
and exact cleanup. The canonical runner owns one randomized
`harden-llm-test-<12-hex>` Compose project per ordinary task invocation and
passes dynamic loopback endpoints to unique database/prefix leases. The
`garage-restart-exclusive` task is the only `service_garage_exclusive` consumer;
TEST-043 prevents it from overlapping the ordinary pool and preserves the
restart assertion. TEST-053 covers the migrated consumers and normal/race
execution. EVAL-006 selected three normal package slots and two race package
slots from six measured candidates with 48 successful samples, zero leaks, and
zero exclusive overlap. Because the P00 integration reference was not
race-instrumented, its race comparator uses the accepted full-system warm
wall/RSS ceilings as a conservative safety ceiling; this is an explicit
measurement-fidelity adaptation, not a threshold relaxation.

Every higher-tier defect is evaluated for a lower-tier root-invariant
regression. Lower fidelity may replace an environmental boundary, never the
assertion oracle. A failed or leaked benchmark sample is not a timing datum.
Thresholds are derived from the fingerprinted KER samples and its accepted
budgets, and may change only with new evidence and an ADR amendment.

## Consequences

The repository gains one policy source, one execution path, one
benchmark/evidence adapter, explicit resource ownership, and a repeatable
T0-T2 loop. Browser and service startup
cost is isolated from ordinary coding feedback. The runner and KER must keep
bounded logs and cleanup evidence, and unlike hosts must report rather than
silently enforce reference-host budgets.

The two named frontend serial exceptions are the SessionVault lifecycle/clock
case and global observability application configuration. A third exception,
additional browser feature, changing the separate Compose ownership, DOM
emulator, threshold relaxation, or pooling fidelity change requires a new
decision record or an amendment before merge.

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
