# Timeout Change Policy

[`baseline.json`](baseline.json) is the certified v1 timeout baseline. TEST-039
reads each value from its owning source file. Unchanged or reduced values pass.
An increase fails unless `ker/timeouts/rca/` contains a matching JSON record.

Before increasing a timeout, prove that the affected phase started and capture
failed timings plus comparable successful timings. Record p95 and maximum
duration, the previous and proposed timeout, explicit headroom, the root cause,
and why an increase is safer than fixing readiness, routing, or resource limits.
Never use a timeout increase to hide a defect.

The committed telemetry race-test RCA is the reference example of a supported
increase: it records repeated race/non-race timings and changes only a test
assertion, not a production queue, exporter, or request timeout.

The 300-second Compose readiness value is a legacy cap from the former stack.
Langfuse was part of its historical startup basis but is no longer started. Keep
the cap until a new observed baseline is recorded; it does not describe a
current service dependency or justify increasing another timeout. Backend
certification does not derive a frontend timeout; the browser-facing client
owns its separate transport margin.

Start from [`rca/TEMPLATE.md`](rca/TEMPLATE.md), then commit a redacted JSON
record whose fields exactly match `requiredRCAFields` in the baseline manifest.
