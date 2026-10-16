# Native CPA throughput search

## 1. Scope

This explicitly authorized live benchmark measures native CPA streaming separately
from HLLM's 60 s execution deadline and 128-connection transport cap. It uses the
same synthetic nonce/model/low-effort workload and existing CPA credential. It
returns on validated `response.completed`, without waiting for connection EOF.
No production transport, shared CPA configuration or official API route changes.

## 2. Search and observation

```sh
go run -tags=live ./cmd/harden-llm-cpabench --live --output /private/new-run --action search --maximum 1000 --granularity 8 --quiet 90s
```

Supply only `CPA_API_KEY` through the protected environment source. The endpoint
is restricted to the same CPA at loopback port 8317 or `--endpoint public`.
Double candidate concurrency from one until throttling or a significant failure
ratio appears, then bisect the interval. Candidates at least 64 are separated
by a recorded quiet interval to reduce quota carryover. A passing search point
means no explicit throttle; wrong results invalidate calibration and unknown
outliers are retained separately. This is an empirical workload-dependent burst bracket,
not a universal provider entitlement or proof of monotonic limits.

A final 1000-item drain fills the selected pool immediately. Explicit rejections
may retry at most 12 times, honoring Retry-After, shared cooldown, and a single
recovery probe before reopening the pool. Unknown outcomes are terminal and are
never replayed; they do not abort independent items. Complete drain acceptance
requires 1000 validated distinct items. The native-only per-call budget defaults
to 180 s, at most 5 m; the hard campaign deadline is at most two hours. Native
benchmark timings do not qualify HLLM's unchanged ordinary request deadline.

Five-second heartbeat events and atomic `status.json` report accepted, dispatched,
completed, unknown, rejected, active, peak, cooldown and phase timing. Raw output
and credentials are not logged. Report useful completions/second and whole-batch
wall time, including cooldowns and tails. Separate cold burst performance from
sustained throughput; bisection can be noisy because other clients share quota.

Use `--action wave --concurrency 256` for one independent burst or
`--action drain --concurrency 128 --calls 1000 --interval 0s` for a backlog.
Every run needs a fresh output directory and explicit live authorization.
