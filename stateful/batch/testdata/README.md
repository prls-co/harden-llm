# Temporal replay fixture

## 1. Provenance

`cooldown-restart-v1.json` was captured on 2026-10-16 by
`TestTemporalBatchWorkerRestartDuringCooldown` (TEST-480) against the canonical
runner's isolated Temporal 1.28.4, PostgreSQL and Garage stack. The provider was
a process-owned TLS/SSE server, with a synthetic key, 24 independent synthetic
nonce items, one HTTP 429, a seven-second cooldown and a stopped/restarted worker.
All 24 results qualified after exactly 25 provider requests. Worker identities
were replaced with `synthetic-fixture-worker`.

This history contains workflow references and diagnostic activity results. It
contains no provider credentials, user prompts, generated text or live-account
data. The default-tag replay test runs offline without a Temporal server or
activities, protecting compatibility of the versioned V1 dispatcher.

## 2. Refresh

Retain this fixture when changing workflows. Use Temporal workflow versioning
or introduce a new workflow name when a change cannot replay it. For an explicit
new synthetic capture, set `HARDEN_LLM_TEST_BATCH_HISTORY_OUTPUT` to a fresh
absolute path and run the canonical `go-temporal-integration` task. Review the
metadata and replace worker identities before adding another fixture. Default
verification never rewrites fixtures.
