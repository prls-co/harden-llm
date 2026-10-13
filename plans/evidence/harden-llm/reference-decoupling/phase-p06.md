# P06 release and production acceptance

## 1. Accepted identities

- Plan: `PLAN-HLLM-REFERENCE-DECOUPLING-002`, v5.3, issue #100.
- Application source: `main` merge `bbdb5d77bc76424804427ca30a7bf9ddc453d0cf`.
- Exact hosted release: [38377471293, attempt 1](https://github.com/prls-co/harden-llm/actions/runs/38377471293/attempts/1).
- Gateway image: `sha256:1ca34c3a1bddc6049c46ac1fa5f898d954c2eb0b325170f9480a58e5349edf86`.
- Frontend image: `sha256:c673c3fc7b5279a7f5e0a86ea67985e8bc82d4f832c44ecb5c61b031eb71294d`.
- Both never-reused `release-<full-source-SHA>` tags were built from a clean
  detached checkout of the accepted source. Gateway binary, OCI identity and
  runtime identity match; frontend OCI/runtime release matches.
- Caddy owner: [PR #9](https://github.com/prls-co/caddy-shared/pull/9), deployed
  `codex/shared-portal-login` merge `0afe6c6677c1666608326316d560dd6e3d17ce75`.
- Public UI: <https://harden-llm.prls.co/>; OpenAI base URL:
  <https://harden-llm.prls.co/v1>. Existing `HARDEN_LLM_TOKEN` is preserved.

Deployment checker PR #105 merged at `8bee48dfdbf3d1568eb103ab061fc7167fc5d8e9`;
its hosted fast checks and CodeQL passed. The production operator checkout
was advanced to that revision and its canonical check passed.

Later deployment-checker/documentation commits do not change these application
image identities. The production checkout may advance for those operator files;
its protected descriptor continues to pin the certified component images.

## 2. Phase steps and verification

| Step | Accepted result |
| --- | --- |
| P06.S01 | All 28 hosted release tasks pass, including `make verify`, real database integration/race, composed reference contract, vulnerability scan, Phoenix/package/Node tests and release build. All three uploaded runner reports are accepted; zero cleanup errors/warnings. |
| P06.S02 | CHECK-422 completed; shared owner provisioning/permission proof passed. Exact gateway-image `gateway-schema` exited 0 before both application services converged. Corrected physical-network comparison confirms equivalent configuration; subsequent apply performs no work. |
| P06.S03 | TEST-435 passes after ingress cutover and again after exact retired-resource cleanup. Application and complete seven-service configuration checks are equivalent. |
| P06.S04 | This checkpoint, release summary, requirements, runbook and plan ledger record actual identities/results, earlier failures and unperformed checks. Final docs/checker changes follow normal PR checks and main merge. |

The release task took 13m53s between GitHub step timestamps. Task-level wall
measurements are in `p06-release-summary.json`; these are single-run observations,
not production capacity/SLO measurements. No confidence interval is applicable.

## 3. Shared services and clean cut

The existing `compose-postgres-1` (`compose_default`) now contains one isolated
`harden_llm` database and restricted login role. The role authenticated, wrote
and read its own temporary table, and was denied access to an administrator-owned
sentinel. The sentinel was removed. No other product database was reset.

Shared Garage now contains only the new HLLM-owned `harden-llm-artifacts` bucket
and `harden-llm-reference` read/write key for this application. A unique synthetic
`artifacts/<key-hash>/<run-id>/` passed put/list/get/delete; its final listing was
empty. New database/artifact secrets and the stable 32-byte encryption key remain
in protected production settings/backups. The nonsecret upstream metadata file
retains readable permissions for the unprivileged gateway and now names
`api_key_envs: ["HARDEN_LLM_TOKEN"]`.

Both shared-owner container IDs/images remain unchanged. Removed only
`harden-llm-harden-postgres-1` and its sole-consumer
`harden-llm_harden-postgres-data` volume, after immediate ownership/mount checks
and successful public acceptance. The production application daemon count stays
two; one dedicated database daemon is retired and no infrastructure daemon added.

Caddy native validation and scoped apply passed. Tunnel version 16 → 17 removes
only the old API rule (14 → 13 rules); exact readback preserves all others.
The local mirror validated and its connector was restarted. After acceptance,
only the reviewed explicit `harden-llm-api.prls.co` CNAME was deleted; exact-name
lookup is empty. The artifact hostname remains owned by shared ingress for
other consumers; HLLM artifact downloads use its authenticated same-origin API.

## 4. Public acceptance and limits

TEST-435 verifies health/readiness, Portal entry, metrics 404, absent/unknown/
digest bearer denial, authenticated model discovery (33 models), local malformed
request rejection, forged scope-selector rejection, and independent configured
storage/history/artifact/credential states. History growth baseline is **0 rows,
24,576 bytes**; **5 GiB** remains the retention-review trigger, not an enforced
quota. No stored prompt/result was read and no inference was dispatched.

Browser layout, interactive Portal login and live-provider inference were not
checked. Deterministic restored-component tests and the exact baseline CSS
comparisons are recorded in P04; these do not claim browser rendering acceptance.
The on-demand preview runner and stopped dev applications remain stopped;
production deployment does not certify the dev URL. No architectural refactor
was needed in this release phase; the actual test/security/deployment-checker
corrections are recorded below without hiding them.

## 5. Retained failures and evidence

See [the chronological log](phase-p06-in-progress.md) for three failed release
attempts (cold compilation budget, reachable vulnerabilities, CPU contention in
race-instrumented large-record timing), the build helper's incorrect attempt-URL
comparison and the first production postcheck's logical/physical-network mismatch.
The accepted release follows those fixes; the failures are not counted as passes.
TEST-234 fails before the network fix and passes afterward, including wrong,
extra and missing network membership. All 29 focused Node deployment tests pass.

Key records: `p06-release-summary.json`, `p06-release-final-reports/`,
`p06-shared-owner-probe.log`, `p06-initial-apply.json`,
`p06-production-before.json`, `p06-production-after.json`,
`p06-ingress-apply.log`, `p06-retired-api-dns.log`,
`p06-retired-postgres-removal.log`, `p06-http-acceptance.json`,
`p06-final-application-check.log`, `p06-final-full-check.log`, and
`p06-network-check-red.log` / `p06-network-check-green.log`.
Raw build logs, protected descriptors/settings and infrastructure backups are
operator-local, not Git evidence. No secret or customer payload is published.
