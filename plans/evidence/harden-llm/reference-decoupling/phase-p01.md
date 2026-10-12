# 1. P01 stateless core checkpoint

- Plan: PLAN-HLLM-REFERENCE-DECOUPLING-002 v5.3.
- Branch: `feat/reference-decoupling`; input source: `711fe9a`.
- Status: P01.S01–S10 done; next step P02.S01.
- Toolchain: Go 1.26.6 linux/amd64, Node 22.22.1, pinned Elixir 1.20.2/OTP 28.4.3.
- Dependency locks unchanged. No production, Docker, browser or public-provider call ran.

## 2. Ordered evidence

| Steps | Result |
| --- | --- |
| S01/S02 | TEST-421 intended missing-executor RED, then same command GREEN. `p01-s01-red.log`, `p01-s02-green.log`. |
| S03/S04 | TEST-437 wrong repair binding/pricing RED, then same command GREEN. `p01-s03-red.log`, `p01-s04-green.log`. |
| S05/S06 | TEST-445 missing stateful composition RED, then same command GREEN. `p01-s05-red.log`, `p01-s06-green.log`. |
| S07/S08 | TEST-422 executable composition ownership RED, then same command GREEN. `p01-s07-red.log`, `p01-s08-green.log`. |
| S09 | Concrete OTEL implementation and original tests moved once; original retry/cache/accounting assertions retained. AST boundary test forbids concrete effects in the core. |
| S10 | All four exact phase commands and race checks exit 0 in `p01-final-focus.log`; final `make test-fast` exit 0 in `p01-fast.log`. |

## 3. EVAL-421 and review

Fixed adversarial oracles observed zero unexpected dispatches, zero cross-call
mutations and zero plain-client contract failures. Unknown upstreams and canceled
requests dispatch zero providers; a cache miss/hit pair dispatches exactly once;
12 detached concurrent calls dispatch 12 times; 10 concurrent two-stage repairs
dispatch 20 times. Full five-leaf recovery uses six frozen fixture targets and
credentials and totals USD 30; two-stage recovery totals provider USD 9/result
USD 6. Reported, zero and absent prices retain distinct accounting semantics.
Categorical checks are exact assertions, not latency estimates or confidence intervals.

The review added two cheap root regressions before closing the phase:
`p01-policy-red.log` shows a caller changing an in-flight repair binding; the
per-call deep copy fixes it while preserving explicit empty retry lists.
`p01-capture-red.log` shows a failed projection silently omitting the callback;
a bounded `diagnostic_projection_failed` code now arrives once, without changing
inference or exposing an unredacted trace. The plan names that existing diagnostic
requirement's concrete field. Both regressions pass in the final focused log.
Model discovery also receives detached connection metadata.

## 4. Failed preparations

A temporary test used an unavailable ProviderError field; preparation was fixed
before intentional RED. Moving OTEL tests exposed a runtime test helper dependency;
it now uses the existing progress cache fixture, preserving its assertions.
The initial broad fast attempt lacked fresh-worktree Mix dependencies; pinned
`mix deps.get` ran separately. A subsequent invocation omitted the pinned PATH;
that infrastructure failure is retained as `p01-fast-missing-path.log`. No failed
application assertion was skipped. Clone review initially changed [] to nil;
existing validation caught it and `slices.Clone` preserves both meanings.

Text evidence normalizes trailing whitespace only. Full focused suites and race
checks passed; the final fast runner accepted all nine tasks. The first successful
fast result is retained separately from the final boundary-fix run. Hosted CI and
production acceptance remain separate gates, and CHECK-422 is still pending.
