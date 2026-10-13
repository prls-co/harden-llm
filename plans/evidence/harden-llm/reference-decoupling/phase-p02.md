# 1. P02 stateful storage checkpoint

- Plan: PLAN-HLLM-REFERENCE-DECOUPLING-002 v5.3.
- Branch: `feat/reference-decoupling`; input source: `20ab001`.
- Status: P02.S01–S18 complete; next step P03.S01.
- Toolchain unchanged: Go 1.26.6 linux/amd64, Node 22.22.1, pinned Elixir 1.20.2/OTP 28.4.3.
- Dependency locks unchanged. Tests used local TLS providers and runner-owned PostgreSQL/Garage; no browser or paid provider ran.

## 2. Ordered RED and GREEN evidence

| Steps | Test and evidence |
| --- | --- |
| S01/S02 | TEST-423 complete profile values; `p02-s01-red.log`, `p02-s02-green.log`. |
| S03/S04 | TEST-444 baseline AES-GCM/payload/origin oracles plus independently generated ciphertext; `p02-s03-red.log`, `p02-s04-green.log`. |
| S05/S06 | TEST-424 fresh/idempotent schema, mismatch, rollback, exact constraints and canceled acquisition; corresponding RED/GREEN JSON and text reports. |
| S07/S08 | TEST-438 real atomic immutable saves, shared scope, foreign denial, CAS, restart and no held probe connection; corresponding RED/GREEN reports. |
| S09/S10 | TEST-439 key/credential cache isolation, expiry/cleanup, core integrity rejection and CAS drafts; corresponding RED/GREEN reports. |
| S11/S12 | TEST-440 one bounded probe, no saved mutation on refresh and atomic encrypted v4 bundles; corresponding RED/GREEN reports. |
| S13/S14 | TEST-425 missing/invalid/duplicate/fingerprint bearer denial, independent fixed hashes and scope-selector rejection; `p02-s13-red.log`, `p02-s14-green.log`. |
| S15/S16 | TEST-449 required key metadata, typed settings, schema command and preview secret/schema composition; Go and Node RED/GREEN logs. |
| S17/S18 | `p02-final-storage.json`, `p02-integration.json`, `p02-final-fast.log` and `p02-race.log` all pass, with no cleanup errors/warnings. |

Focused storage reports use the existing `runTasks`/`go-integration` owner with the
exact phase Go selection. The disposable orchestration script changes only that
command; it does not create its own services or substitute a store. The final
aggregate selection runs the canonical full Go integration command.

## 3. EVAL-422 and ownership

Exact categorical assertions observed zero cross-key leaks, zero overwritten
snapshots, zero credential substitutions and zero stale cache hits. Two identical
concurrent saves create two UUIDs. Same-key clients/services share state; different
host draft keys remain separate. The cache write removes exactly 100 of 205 expired
same-key rows while preserving live and foreign rows. Both probes and blocked
cache-on providers hold zero SQL connections, and cache-off ordinary inference
adds zero acquisitions. Invalid authorization and forged selectors reach zero
handlers/client constructions; the Control Plane counter remains zero.

The moved baseline definition validator and AES-GCM implementation have one owner
in `stateful`. Retry/option semantics continue using core validation. One short
transaction helper owns SQL budgets; profiles, credentials, cache and drafts use
direct pgx queries. Bundles and Save reuse inserts rather than separate stores.
`Connection.Clone` centralizes detachment for core and wrapper composition.
`Config.CacheTTL` names the accepted TTL composition setting; the plan records it.
No profile update/seed/inheritance, user/session/key registry, repository interface,
legacy reader or environment credential fallback was added.

P02 introduces the one-shot schema CLI and preview dependency but serving never
initializes or resets a schema. Preview source descriptors now pass Go storage,
history and a preserved 32-byte encryption key; artifacts remain unset. Frontend
Ecto drafts/history remain until P03.S13. Runtime reference routes are a P03 gate.

## 4. Failed attempts and corrections

- The new corrupt-cache test initially expected another successful provider call.
  Retained `client_cache_test.go` requires CACHE_INTEGRITY, no output and no extra
  dispatch. The new test now checks that stronger existing oracle against real
  PostgreSQL; production cache semantics were unchanged. Its initial mismatch is
  retained in `p02-s10-cache-oracle-mismatch` reports.
- The timeout provider fixture waited for cancellation without draining its request
  body, preventing the HTTP test server from detecting disconnect. The owned test
  process was terminated with SIGQUIT, stack evidence retained, and the fixture now
  drains/closes the body before waiting. The subsequent intended RED and GREEN are
  separate; `p02-s11-timeout-fixture-deadlock` is not counted as intended RED.
- Temporary scripted constructor editing added extra braces to earlier test calls;
  the file was restored from HEAD and only its constructor block changed. No
  existing protocol assertion changed.
- The fast gate found the retired two-field configuration metadata oracle. It now
  strictly requires the third `api_key_envs` field and its environment name, retaining
  exact upstream metadata and plaintext-credential exclusion assertions. The failed
  run is retained in `p02-fast-old-metadata-oracle.log`.
- Typed-nil optional pool errors were corrected to an explicit nil return during
  review. Negative entered-credential revisions are rejected before probing.

## 5. Deployment boundaries

No main/production promotion ran. P02 feature tests and source descriptors are
verified. A dev promotion must also use the updated preview control assets and
secret generator: the current workflow checks out main for its deployer, so source
changes alone do not prove those assets active. Preserve existing preview state
until that owner path is reviewed; never claim this checkpoint is deployed.
CHECK-422 remains pending. Release and real fresh-image schema convergence remain
P05/P06 gates. Timing samples are single categorical checks, not capacity evidence.
Text logs normalize trailing whitespace only; runner reports retain actual results.
