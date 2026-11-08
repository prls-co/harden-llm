# Responses Client Production Closeout Plan

## 1. Objective and authorization

Address the three review findings on PR #148, merge the verified implementation
to `main`, deploy the affected HLLM application components, update operational
documentation, and prove that production matches its protected configuration.
The user's 2026-11-07 instruction authorizes implementation, push/merge and
production deployment. Browser testing and paid provider inference are excluded.

Baseline: PR #148 at `c5539844851b3cf6cc6020dc1fbfeb855bd59a69`, targeting
`main` at `8b62f4e59a7bef10b07cb6932ee08a75d7bae448`. Initial production
gateway, web and batch-worker images report application source
`f1ebdc41e4ea7c38b65f256b346b1f2cad3dbfb4`. Revalidate each identity before
promotion; these baseline observations are not completion evidence.

## 2. Required behavior and ownership

1. `httpclient.Responses` sends one inference POST. Explicit gateway uncertainty
   overrides an execution ID and returns `UnknownOutcomeError`. Diagnostics
   identify execution; they do not prove its outcome. Preserve bounded response
   facts without making `errors.As` also identify a definitive terminal error.
2. Known authentication, validation and tracking-ID-conflict rejections remain
   definitive. `x-should-retry: false` on `tracking_id_conflict` suppresses SDK
   retry but does not mean the rejected request executed. Malformed/ambiguous
   wire outcomes remain unknown; the client never automatically replays them.
3. Ordinary Responses and individual batch requests retain their existing
   256 KiB encoded bounds. Complete batch manifests retain their existing
   32 MiB bound. Correct both OpenAPI copies without changing these limits.
4. The reference-gateway contract test preserves its operation IDs, real
   gateway/provider boundary, exact provider counts and rejection assertions.
   Reusing a fixture operation waits for explicit handler completion. Do not
   weaken overlap rejection, add sleeps/retries, or serialize unrelated tests.
5. Root Go, public stateful composition, gateway and Phoenix ownership stay
   unchanged. Provider prompts, extraction schemas and recovery semantics stay
   unchanged apart from correcting the HTTP client's uncertainty projection.
6. Production promotion preserves accepted jobs, HLLM data and credentials,
   shared infrastructure, networks, identities and other products. No reset,
   provider request, or volume/image pruning is part of this closeout.

## 3. P01: Reproduce the defects before implementation

- Add TEST-422 client cases for unconfirmed 502/504 and partial-output 429,
  with/without diagnostics, explicit error-code uncertainty, definitive
  rejections, preserved facts and exactly one POST. Show the new cases fail
  against the current client before changing its implementation.
- Use a process-owned, channel-controlled HTTP fixture to hold handler teardown
  after its response is observable. Prove the reference fixture still rejects
  overlapping reuse until the outer handler has returned; retain this boundary
  regression under TEST-485.
- Extend the real gateway contract test with a deterministic teardown schedule
  and explicit completion synchronization. Establish the retained failure's
  cause from the controlled schedule and exact counters, rather than a retry.
- Preserve the original failed runner report and record its path, failure and
  relationship to the deterministic reproduction in the closeout evidence.

Acceptance: a recorded failing uncertainty regression and a demonstrated fixture
completion race, with no public provider, browser or application data involved.

## 4. P02: Implement the fixes and update the contract documentation

- Classify explicit uncertainty before the existing terminal-error projection.
  Keep the documented pre-execution tracking conflict as a definitive rejection.
  Expose response status/code/diagnostics through a nonterminal typed cause;
  remote content must not enter error strings.
- Add channel synchronization around the complete reference fixture handler,
  preserving all existing operation-accounting assertions and parallel tests.
- Restore the batch manifest description to 32 MiB in `api/openapi.yaml` and
  its frontend copy. Update `httpclient/README.md`, the canonical TEST-422 and
  TEST-485 catalog entries, and the failure/RCA record.
- Review the resulting diff for accidental request-limit, prompt/schema,
  authentication, retry-policy, persistence or infrastructure changes.

Acceptance: all new regressions pass, terminal and unknown error types are
disjoint, and canonical/vendored OpenAPI content is identical.

## 5. P03: Verify and deliver the source

1. Run focused deterministic cases, then the complete HTTP client, gateway and
   reference-fixture race suites with `-count=1`.
2. Run `make test-fast` with the pinned Elixir/OTP PATH and an owned temporary
   build directory outside the quota-limited `/tmp`. Retain the runner JSON;
   diagnose any failure before another run. Run formatting and whitespace
   checks and review the final diff.
3. Commit and push the verified PR checkpoint. Update the PR description with
   final behavior, the RCA and actual validation, preserving the original
   failure rather than presenting its earlier passing retry as an explanation.
4. Verify hosted fast checks and CodeQL on the exact PR head, then merge #148
   with an expected-head guard. Fetch and verify the resulting `main` SHA.

Acceptance: PR merged, local and remote source identities reconciled, and exact
head CI evidence retained. This does not yet establish production deployment.

## 6. P04: Certify the exact production candidate

Follow `plans/gateway-image-build-deployment-spec.md` and ADR-HLLM-028.

1. Dispatch `test-hierarchy.yml` on `main` with `suite=release`; record the
   pre-dispatch SHA and exact workflow run/attempt. Verify its SHA and the
   successful `browser-free release` job and `Run make test-release` step.
   This suite must not invoke browser or live-provider tracks.
2. Inspect the current protected production descriptor and runtime in memory;
   print only redacted identities. Capture a mode-0600 descriptor checkpoint
   in a mode-0700 private directory, with a before hash and rollback images.
3. Check that accepted durable jobs and active execution can survive the
   affected worker rollout. Use the existing lifecycle and graceful-stop
   behavior; never replay inference or delete accepted work.
4. Build the gateway/worker binary image and affected web image from a clean
   checkout of the accepted release SHA. Use fresh full-SHA local tags, retain
   the previous images, and verify OCI source/revision/version, image IDs and
   gateway `version` output. Keep private build credentials out of logs.

Acceptance: exact merged source certified and images independently identified;
previous production images/configuration remain available for rollback.

## 7. P05: Deploy, verify and record production acceptance

1. Update only the affected image/release identity fields in the protected
   descriptor, with a freshness check against the recorded before hash.
2. Run candidate-scoped `production-config check`; inspect every difference.
   Apply only gateway, batch-worker and web as required by their changed
   artifacts, with the documented production-config lifecycle.
3. Verify the affected containers' image IDs, OCI revisions, release variables,
   binary version, health and restart state. Require the scoped candidate
   checks and full descriptor check to report `equivalent`.
4. Verify public gateway/web health and readiness, Portal login routing,
   missing/invalid bearer rejection, and an authenticated non-inference API
   operation using the server-held key without printing it. Where practical,
   use authenticated invalid/oversized requests that cannot admit inference to
   verify deployed request validation and limits.
5. Verify support services retain their original container/image identities;
   retain evidence of durable-job/worker health and no deployment-related
   failure. Inspect bounded, redacted diagnostics only if a check fails.
6. Append a dated release-certification entry and a machine-readable redacted
   receipt with branch, deployed application SHA, component image IDs, URL,
   release/CI runs, tests, configuration hashes and HTTP results. Complete the
   plan's execution table. Commit/push the documentation-only closeout and
   reconcile both source worktrees without relabeling application artifacts.

Acceptance: the production checks above pass, the final repository is clean,
the verified application artifact remains identifiable separately from its
documentation-only successor, and all remaining follow-ups are stated exactly.

## 8. Execution and completion audit

| Phase | State | Evidence |
| --- | --- | --- |
| P01 reproduction | complete | New client regression failed in five uncertainty cases; held handler deterministically reproduced the retained `posts=1 calls=2` failure. See the [RCA](../docs/rca/responses-client-and-fixture-completion.md). |
| P02 fixes/docs | complete | Typed uncertainty cause, real gateway projection cases, explicit fixture completion and restored OpenAPI manifest docs. Complete client/gateway/fixture race suites pass; OpenAPI copies match. |
| P03 verification/merge | in progress | Local race log `/var/tmp/harden-pr148-race-corrected.log` passes. Default fast report `runner-1794097903304-3508909-effef4c2722ee151.json` retains runner startup/timeouts during measured host pressure; unchanged fast selection with supported `--candidate-slots 2` is running. Hosted default fast and merge remain pending. |
| P04 release/images | pending | Required exact-source release run and image identities |
| P05 production/closeout | pending | Required configuration equivalence, HTTP acceptance and durable receipt |

Completion requires all five phases, not merely passing unit tests or a merged
PR. Browser layout and live provider performance are not certified by this work.
Issue #149's future size-limit research remains a separate follow-up.
