# 1. P03 reference execution and history checkpoint

- Plan: `PLAN-HLLM-REFERENCE-DECOUPLING-002` v5.3.
- Branch: `feat/reference-decoupling`; input committed source `2a8aede9096996963f74107fded8218aa15a9011` after P02 implementation `8b02fe71b85e50d8253ee094f916efe85a587530`.
- P03.S01–S14 complete. Next: P04.S01. No dev/main promotion or production change.
- Toolchain: Go 1.26.6, Node 22.22.1, Elixir 1.20.2/OTP 28.4.3. No dependency version was added or upgraded; unused Go and Phoenix database dependencies were removed.
- Real PostgreSQL/Garage leases and local TLS providers only. Browser layout was not checked; no paid provider ran.

## 2. Ordered verification

| Steps | Original oracle and retained evidence |
| --- | --- |
| S01/S02 | TEST-442/451 scoped full records, numbered windows, usage, exact 17 MiB bound, one insert and one readback, real delayed COMMIT; `p03-s01-store-red`, `p03-s02-outcome-green`, `p03-canonical-boundary-green`. |
| S03/S04 | TEST-443 real Garage publication, independent download, integrity and prefix deletion; `p03-s03-red`, `p03-s04-green`. |
| S05/S06 | TEST-427 core accounting, common key/window filter, prefix-before-row deletion, clear cutoff/batches/failure; `p03-s05-red`, `p03-s06-green`, `p03-history-query-refactor`. |
| S07/S08 | TEST-426 complete binding resolution and one core execution, terminal before recording, all failure outcomes without replay; `p03-s07-red`, `p03-s08-record-green` and value/storage review logs. |
| S09/S10 | TEST-441 all 18 REST operations, bearer-derived scope, same-key sharing/foreign denial, canonical wire and SSE lifecycle; `p03-s09-red`, `p03-s10-json-values`. |
| S11/S12 | TEST-428/WEB-TEST-115 preserved baseline workspace controls/fields, immutable Save, exact restore, Portal draft keys, repaired output/progress and recording warnings; `p03-s11-all-red`, later complete-input/template RED logs, `p03-s12-token-boundary-green.log`. |
| S13 | Retired Phoenix SQL assertions moved to real Go ownership before removing Ecto/Repo/migrations/second pool; `p03-s13-retained-storage-oracles.json`, `p03-s13-final-frontend.log`, `p03-s13-static-owners.log`. |
| S14 | Current real-owner measure at all three EVAL seeds, full integration/race and nine-task fast gate; `p03-final-measure-json-tree.json`, `p03-final-integration-json-tree.log`, `p03-final-race-json-tree.log`, `p03-final-fast-json-tree.log`. All pass; cleanup errors/warnings zero. |

The focused runner clones an existing canonical task and changes only its test
command. It does not own an alternate service pool or substitute storage.
The measurement command uses `-shuffle=104729`, `130363`, `155921`, `-p=2`,
`-count=1` for the six named suites on one runner-owned real service lease.

## 3. EVAL-423 observations

Every seeded execution of the named owner tests proves one committed healthy
reference record per execution, zero ordinary-call records, zero disabled-history
acquisitions, zero false saved claims, zero history-blocked valid inferences, zero
secret diagnostic leaks, zero full-payload mismatches, zero inference replays and
zero cross-key artifact reads. These are exact categorical assertions, not a
capacity estimate. The current logged full-boundary sample is 216.166344 ms insert
and 85.124954 ms detail; the full race suite also passes both unchanged two-second
assertions. Oversize records are rejected, never truncated.

## 4. Single ownership and retirement

The wrapper has one full-record builder, one safe error projection, one
parameterized history filter, one cache/profile/credential resolution path and
one bounded Garage client. Statistics sum the existing core ledgers and never
reprice. A complete terminal result is delivered before separate history status;
unknown COMMIT receives exactly one readback and remains unconfirmed when absent
or failed. Artifacts remain readable independently of a history row.

Phoenix now calls REST for drafts/history and keeps only Portal admission and
async lifecycle. Its per-user draft selector stays inside the host key scope.
The restored workspace loads complete templates/snapshots and restores captured
settings without matching names, saving or rerunning. The existing preview
launcher invokes the gateway image's schema command. Package extraction and
remaining profile/history/trace/embedding workflows belong to P04.

Opaque request/result payloads use PostgreSQL `json`; queried summaries use JSONB.
This preserves encoding directly and removes normalization/redundant scans. One
canonical OpenAPI validator consumes an exact-number JSON tree from the already
pinned GJSON parser. The generic conversion and cheap syntax/value regression
retain schema validation, number fidelity and Unicode semantics without a parser
fallback. No size, timing or accounting assertion was relaxed.

## 5. Retained failures and deployment boundary

[The implementation log](phase-p03-in-progress.md) records fixture/preparation,
retired-policy, nanosecond/microsecond, pointer-alias race and large-body timing
failures and their corrections. The race has a cheap mutation regression which
failed before detachment and passes under race afterward. Full-body validation
was previously repeated; the final path validates once and reuses its correlation
and ledger values. Intermediate nominally named GREEN files are explicitly
identified as failed in that log.

CHECK-422 remains pending. The existing trusted preview control path still loads
main-owned assets; source checkpoints alone do not activate their replacements.
Preserve existing previews until that gate is satisfied. P04–P06, production
schema ordering/topology, main promotion and deployed acceptance remain pending.
