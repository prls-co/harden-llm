# Responses Client Uncertainty and Reference Fixture Completion

## 1. Findings and retained failure

PR #148 originally classified a received HTTP error as definitive when a
5xx response contained `harden.execution_id`. It also classified a partial
429 refusal as definitive without checking the gateway uncertainty signal.
The real gateway already suppresses replay of unconfirmed execution through
`x-should-retry: false`. An execution identifier cannot override that signal.

The first fast run at the original implementation checkpoint also failed
`TestHTTPClientUsesActualReferenceGatewayAndRecovery`:

```text
owner rejection misclassified or replayed: posts=1 calls=2
err=harden inference outcome is unknown; do not replay inference
```

Its original report is retained at
`tmp/test-feedback/runner-1794059625515-3508864-9de1f7260f528b1c.json`, generated
at `2026-11-07T13:53:45.515Z`. Later unchanged passing runs did not explain
that failure. The new channel-controlled reproduction establishes the missing
ordering described below; it does not attribute unrelated TLS diagnostic lines
in the original run to this cause.

## 2. Deterministic reproduction and cause

`reference.Scenario.Gateway` marks the logical operation active before entering
the real owner handler and clears it in a deferred function after that handler
returns. The client returns after consuming the final `history.recorded` SSE
event. That event is flushed before the handler and wrapper finish returning.
The test immediately reused the same operation ID for its missing-profile case.

The controlled reproduction holds the first owner handler after flushing its
complete response, keeping the fixture's logical operation active. The next
request is rejected by the fixture with HTTP 409 and a fixture text body before
reaching the inner real-handler counter. The strict client cannot decode that
body as the reference error contract and correctly returns an unknown outcome.
This deterministically reproduces the original `posts=1`, `calls=2` assertion
failure. No provider execution was replayed.

`TestScenarioResponsePrecedesOperationCleanup` separately exercises the fixture
over an owned TLS listener. It requires overlap rejection while teardown is
held, successful reuse after the outer handler completion signal, three
observed Run POSTs, one unmatched overlap, two owner admissions and zero
provider requests. The original actual gateway/client case now waits for
outer fixture completion before reusing the same operation ID. Its operation
identity, repair, rejection, grounding and exact call assertions are retained.
There is no sleep, retry, weakened overlap policy or global serialization.

## 3. Uncertainty projection correction

The typed Responses client gives explicit `execution_unconfirmed` and gateway
uncertainty precedence over execution metadata. A valid HTTP 409
`tracking_id_conflict` remains a documented pre-execution rejection despite its
no-retry header. Known auth/validation and definitive terminal errors retain
their existing classification.

Received uncertain error facts are available as a `ResponsesUnconfirmed` cause
of `UnknownOutcomeError`: status, code, partial output and optional diagnostics.
This cause is independent of `ResponsesFailure`, so `errors.As` cannot also
identify the unknown result as definitive. Both error strings omit remote
content. Malformed or incomplete wire data stays unknown. No automatic POST
retry is introduced.

TEST-422 covers uncertainty with and without diagnostics and the pre-execution
exception. A gateway projection regression uses the real admission uncertainty
wrapper and error writer for network, partial refusal and deadline outcomes.
Every client case requires exactly one POST.

## 4. Documentation correction and release scope

The previous limit rollback accidentally changed the complete batch manifest
description in both OpenAPI copies. Restore its existing 32 MiB description;
ordinary Responses and individual batch requests remain limited to 256 KiB.
The batch handler and authorization middleware already enforce the 32 MiB
manifest bound. No request limit, provider prompt, extraction schema, core
retry policy, data ownership or persistence behavior changes in this correction.

Execution and release evidence belong in the
[closeout plan](../../plans/responses-client-production-closeout-plan.md) and
[release certification](../release-certification.md). Local synthetic evidence
does not certify live-provider behavior. Future limit research remains #149.

## 5. Verified closeout

PR #148 merged as `c884eb8f7b6d0c48282ee8e9f7077abb5b0d96fd` and the
exact-source [browser-free release](https://github.com/prls-co/harden-llm/actions/runs/42430881773)
passed all 30 tasks with no cleanup errors/warnings. Complete local package race
suites, all ten fast tasks and hosted default fast/CodeQL checks also pass.
The [production receipt](../../plans/evidence/harden-llm/responses-client/2026-11-07-production-closeout.json)
records both deployed image identities, controller equivalence across eight
managed services, twelve passing HTTP checks, new-worker Temporal readiness and
unchanged retained data. No browser or paid provider inference was run.
