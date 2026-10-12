# 1. P00 contract checkpoint

- Plan: PLAN-HLLM-REFERENCE-DECOUPLING-002 v5.3.
- Branch: `feat/reference-decoupling`; phase input source: `ce09df3`.
- Status: P00.S01–S06 done; next step P01.S01.
- Application runtime, production and paid-provider behavior are not implemented
  or certified by this phase. No browser ran.

## 2. Step evidence

| Step | Mode | Actual result |
| --- | --- | --- |
| P00.S01 | RED | TEST-420 exit 1 on old ownership policy; baseline source checks passed. `p00-s01-red.log` preserves the observed excerpt. |
| P00.S02 | GREEN | Same command exit 0 after accepted contracts, OpenAPI and ADR-HLLM-033 were updated. `p00-s02-green.log`. |
| P00.S03 | RED | TEST-436 exit 1 because rejecting placeholder schemas do not describe fixture fields. Full output in `p00-s03-red.log`. |
| P00.S04 | GREEN | Same command exit 0 with complete history/artifact schemas. `p00-s04-green.log`. |
| P00.S05 | MEASURE | ADR-HLLM-034 and historical decision headers recorded; one current history owner (PostgreSQL), no current Laminar history owner, zero artifact paths outside key scope. TEST-420/436 pass. |
| P00.S06 | VERIFY | Final contract/history/OpenAPI checks and `make test-static` exit 0; `p00-s06-history.log`, `p00-openapi.log`, `p00-static.log`. |

## 3. Frozen behavior and review

Pinned baseline is `754690c8d0890c664d69fbdf526b16b3e5601060`.
The immutable manifest contains 12 source digests and 32 capabilities; selectors,
events and CSS provenance are retained. Only key-scoped state (with per-Portal
user drafts) and complete create-on-save profiles are accepted product deltas.
Model Recall host responsibilities were inspected read-only and recorded in
`docs/architecture.md`. No other repository was changed.

Contract review fixed current ownership rather than editing historical evidence.
Post-green review removed duplicate artifact metadata schemas and shared result
constraints through schema references. Existing ordinary protocol, exact-route
and runner assertions remain; reference paths and the new storage owner replace
superseded boundary assertions. Hosted tests check out complete Git history so
baseline provenance remains offline. Browser opt-in policy is unchanged.

The approved `dev` reset was applied after verifying 1 unique/233 missing commits
and patch equivalence (`git cherry` reported the unique commit with `-`).
Remote `dev` then matched `origin/main` `879106f`. No main/production mutation
was performed. The active implementation branch was already created.

## 4. Failed preparation attempts retained

- Separate YAML fragments initially reused anchor IDs. Complete document parsing
  caught this; serialization now emits no cross-fragment aliases.
- A synthetic fixture used guessed backoff names. Schema validation caught it;
  the fixture was corrected to baseline `baseDelayMs`/`maxDelayMs`. Neither schema
  nor assertion was weakened. `p00-s04-fixture-validation-failure.log` is retained.
- The first broad static run found old documentation ownership expectations and
  a forbidden frontend term in the backend contract description. Canonical
  contracts and their current ownership assertions were reconciled; the backend
  dependency prohibition remains intact. `p00-static-policy-mismatch.log`.

No retry hides ambiguous storage/provider work: all failures were offline
contract/fixture checks. Test timing samples are single runs, not performance
certification or confidence estimates. CHECK-422 remains pending for P05/P06.

Stored text logs normalize trailing whitespace only; statuses and assertions are
unchanged. The initial TEST-420 log is an explicitly labeled observed excerpt.
