# CPA repository closeout

## 1. Published scope

The production feature is complete under issues #134 and #136. This closeout
reconciles the older checkout and remaining CPA branches with that release.
The original `feat/cpa-capacity-campaign` checkout was at `6fdbf4f` with ten
modified files and seven untracked fixture files. Those changes were preserved
in `983b13f`, then merged with the released `main` history.

The final history also includes planning PR #135 at `035b6f1` and research
PR #133 at `75909d9`. Conflict resolution retains the production-qualified
architecture, completed status plan, current generation budgets, security
patches, shared admission implementation and all existing test registrations.
The historical CPA feature audit and final research evidence are published
without changing their measurements or claiming a new provider quota.

The consumer fixture runs the actual gateway, stateful service and provider
parsers against synthetic native responses. TEST-485 covers strict scenarios
and independent operation/provider counts; TEST-486 covers the real executable,
SQL cache/history, isolation and owned-resource cleanup. Existing cache/model
regressions and original gateway assertions are preserved. The README links
batch monitoring and distinguishes it from ordinary request observation.

## 2. Verification

Code checkpoint: `53f342ba42857bcdcab2f0e9bbd73482e0033b87`.
The [machine-readable closeout receipt](../evidence/harden-llm/cpa-admission/2026-11-04-repository-closeout.json)
records commands, all ten successful fast tasks and retained failure evidence.

- `make test-fast`: all ten tasks pass, with no cleanup warnings or errors.
- `go test -race ./internal/testfixture/... ./internal/gateway/httpapi ./internal/livecapacity ./stateful/batch -count=1`: passes.
- `go test -race ./httpclient -count=1`: passes after the fixture correction below.
- `node scripts/run-test-tier.mjs --task reference-fixture-integration`: passes
  against the existing approved loopback CI database, using only unique owned
  schemas; no cleanup warnings or errors.
- The frozen research manifest verifies all 42 original file sizes and hashes.
  Research JSON, Python syntax, whitespace and protected-value checks pass.

The first fast run failed because plaintext client fixtures observed extra
unauthenticated `GET /` traffic. A standalone idle Go `httptest` server
independently reproduced two requests, with `127.0.0.1` and `localhost` Host
values. The source of those probes was not established. Per-test TLS listeners
and their trusted clients isolate the handlers from plaintext traffic. Exact
request counts, bearer/path checks, progress, cancellation and no-replay
assertions remain unchanged. Production client code is unchanged. Interrupted
runner/frontend siblings in the failed report are not counted as passing.

Hosted verification and merge identities are recorded on
[PR #133](https://github.com/prls-co/harden-llm/pull/133). Local results do not
stand in for hosted checks or deployment evidence.

## 3. Production identity and checks

Production remains the application release
`f1ebdc41e4ea7c38b65f256b346b1f2cad3dbfb4`, qualified by the
[production receipt](../evidence/harden-llm/cpa-admission/2026-11-04-production-cpa.json).
Gateway/worker image ID is
`sha256:fc338caf13a639864240ea976b591fe3cce62ee7d11fadb3c67e3b617ba803d2`;
web image ID is
`sha256:887c52cd8859d892a116fb3226c9caee2878cfddeb8f155369c48c6b1ebc5f8d`.
The production URL is `https://harden-llm.prls.co/`, with API base
`https://harden-llm.prls.co/v1`.

The production controller independently verifies equivalent configuration and
the gateway, web and worker runtime identities. All seven provider-free HTTP
health/readiness/authentication checks pass. This closeout changes no production
runtime source or configuration; the gateway dependency graph excludes the
research and fixture packages. The already-qualified immutable images require
no rebuild or recreation. No new provider inference or browser was run; browser
layout remains unchecked. The retained production campaign completed 1,000
separate requests with 1,000 validated results and zero failures in 313.392
seconds.

## 4. Final checkout and remaining scope

Final repository acceptance requires this checkout on clean `main`, with local,
tracking and remote refs identical, all closeout commits reachable from that
ref, and the production source checkout synchronized without recreating its
unchanged application images. Preserve unrelated historical worktrees and
accepted data/jobs; the former `main` worktree can remain detached at its saved
commit.

Application callers can adopt `POST /v1/jobs`, persist the returned job ID, poll
its status URL and download results by `custom_id`. That consumer integration
is separate from the completed gateway delivery. Issue #142 intentionally
retains evaluation of future analytics before implementation; it is not a
release blocker.
