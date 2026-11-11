# Transport and admission diagnostics for issue #162

## 1. Scope and design

Continue [issue #162](https://github.com/prls-co/harden-llm/issues/162) from
`d91ff3755f6a93f0d7b39763c2c2d59d6f8f352e`. The header cutoff and reference
uncertainty projection are already corrected. The original interrupted network
hop remains unproven; this work improves retained evidence for future failures.

Use the existing provider router, provider-effect `StreamDiagnostics`, ordinary
CPA dispatch event, ledger error mapper and redacted structured logger. Capture
finite transport cause/phase labels before normalization discards the raw error.
Carry them as non-JSON diagnostic fields and consume them in the existing event.
Reuse the existing stream counters, timestamps, safe outcome error and accepted
generation budget/deadline. Existing logger ownership supplies trace correlation
and equivalent JSON/OTel records; no observer interface or logging sink is added.

The shared ledger mapper retains a safe error class, including a validated
five-character PostgreSQL SQLSTATE, while continuing to unwrap to
`ErrUnavailable`. It must discard the original error message, SQL, arguments,
hostnames and connection details. Both waiter recovery and terminal admission
failure consume this same classification.

## 2. Implementation sequence

1. Add failing T0/T1 regressions to the existing actual TLS/router/core/admission
   fixture and shared ledger mapper. Cover header timeout, partial disconnect,
   cancellation/deadline, success/cache and unavailable waiter reads. Preserve
   exact dispatch counts, positive local stop, unknown usage and deadline bounds.
2. Extend the existing provider diagnostics at the raw HTTP boundary; keep
   `normalizeTransportError` and retry classification authoritative and unchanged.
3. Extend the existing dispatch/recovery/failure records. Do not introduce a
   second event, mutable registry, dashboard or persistence model.
4. Retain SQL/error classes in the one shared ledger mapper, with the existing
   known sentinel mappings and metadata read-recovery limits intact. Add one
   real PostgreSQL waiter-read test for the actual driver/mapping boundary.
5. Run focused race checks, `make test-fast`, the affected PostgreSQL check and
   `git diff HEAD --check`. Commit and push a reviewable feature branch/PR from
   `dev`; production promotion is outside this routine change.

## 3. Compatibility and conventions

- No HTTP/OpenAPI field, status, endpoint or required client upgrade. The
  canonical diagnostics schema rejects unknown properties; adding JSON fields
  would break older strict clients. New transport labels use `json:"-"` and
  never change cache bytes, saved history, artifacts or wire diagnostics.
- The provider-effect diagnostics struct gains additive internal fields. Public
  result/attempt DTOs and observer interfaces retain their shapes. External Go
  code using unkeyed literals of an inferred internal effect type could need
  adjustment; keyed literals remain compatible.
- Unavailable ledger errors may become safe wrappers. Use the established
  `errors.Is(err, batch.ErrUnavailable)` convention; direct equality would differ.
  Repository inspection found no direct sentinel equality to migrate.
- Existing log event names and fields remain; new diagnostic attributes are
  additive. Optional phases/timings remain absent when unobserved. A cache hit
  produces no native dispatch record and must not inherit provider latency.
- No new dependency, configuration, database migration, data reset, telemetry
  SDK in the core, parallel error/retry policy or provider replay. Prompts,
  schemas, models, reasoning, total generation limits and accounting are unchanged.

## 4. Acceptance and limits

TEST-542 owns safe transport/event evidence and wire controls. TEST-543 owns
ledger classification, safe wrapping and unchanged recovery. TEST-544 owns the
real SQL and cache-hit boundaries. Failed and accepted checks are retained separately under
`plans/evidence/harden-llm/issue-162/diagnostics/`.

No browser or paid-provider execution is selected. A finite deterministic pass
does not prove the original incident's network root cause. Implementation,
hosted checks and any later deployment remain distinct evidence.

## 5. Discovered catalog correction

Unmodified `HEAD` fails coverage verification because TEST-539 has no task
registration. Register its existing executable consumer under `go-unit`, which
already runs that default-tag Go test. Keep its assertions and commands intact;
no additional execution path or verifier exception is introduced. The baseline
failure and interrupted early aggregate run remain retained separately.
