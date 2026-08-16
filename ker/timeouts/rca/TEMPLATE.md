# Timeout Increase RCA Template

Create `<date>-<policy-id>.json` in this directory:

```json
{
  "policyId": "compose.readiness-budget",
  "phase": "P07 / TEST-034",
  "startProof": "Redacted evidence path or CI job URL proving the phase started",
  "failedTimingsMilliseconds": [301000],
  "comparableSuccessesMilliseconds": [180000, 205000, 220000],
  "p95Milliseconds": 220000,
  "maximumMilliseconds": 220000,
  "previousTimeoutMilliseconds": 300000,
  "configuredTimeoutMilliseconds": 330000,
  "headroomMilliseconds": 110000,
  "rootCause": "Measured, actionable cause",
  "rationale": "Why increasing the bound is safer than correcting the cause"
}
```

Use redacted evidence references; never place credentials or provider output in
the record. TEST-039 verifies completeness and exact timeout coverage.
