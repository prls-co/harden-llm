# 1. Reference decoupling conformance audit

- Audited specification: v5.3 at `ce09df303ba9c04f489563cf68fcdec25325ee36`, before implementation.
- Implemented application: `bbdb5d77bc76424804427ca30a7bf9ddc453d0cf`.
- First release closeout: `01f255a3cce0012e5fb2291e0cf1d7a3a82a7668`.
- Audit date: 2026-10-13; issue #100.
- Result: all seven phases/72 steps are implemented and deployed. The audit corrects documentation and test traceability; it does not change application behavior or require new images.

This comparison is against the accepted pre-implementation v5.3 plan, not the
rejected PR #97 UI or historical v5.0 proposal. PostgreSQL history, key-scoped
saved data, per-Portal-user drafts, complete create-on-save snapshots and the
single-origin API were already settled before this implementation. They are
not newly introduced deviations. The baseline remains PR #95 at
`754690c8d0890c664d69fbdf526b16b3e5601060` with its 32 frozen capabilities.

## 2. Exact departures and refinements

| ID / location | Planned → actual; why | Change and evidence |
| --- | --- | --- |
| D01 — P01.S02/S09, section 4.2 | `DiagnosticCapture` gained `ErrorCode`. A failed projection otherwise silently omitted the required terminal callback. It now delivers `diagnostic_projection_failed` once with no unsafe values and preserves inference. | `20ab001`; P01 policy/capture RED and final focused/race tests, TEST-421/445; ADR-HLLM-033 §4. |
| D02 — P02.S10, section 4.3 | Added explicit `Config.CacheTTL` to wire the already specified 86,400-second default into the wrapper. This names a missing composition field; it adds no setting semantics or second cache owner. | `8b02fe7`; TEST-439 real expiry/CAS/isolation; ADR-HLLM-033 §4. |
| D03 — P03.S02/S14, section 8.2 schema | Opaque `request`/`result` changed from JSONB to PostgreSQL `json`; queryable `summary` stays JSONB. Preserving the validated encoding avoids normalization and repeated full-body scans. One canonical OpenAPI validator consumes an exact-number tree through the existing GJSON dependency. SQL timestamps/JSON summaries use microseconds; unresolved application errors have null target/output and local correlation. | `a49131d`; TEST-442/451 full 17 MiB equality, oversize rejection, delayed COMMIT and unchanged two-second limits; P03 logs retain earlier failed timings. ADR-HLLM-034 §5. |
| D04 — P03.S08/S14 correctness | Diagnostic timestamps and call inputs needed detachment; the existing pointers could alias progress/runtime state and race. One generic copy helper replaces shared references without changing accounting or output semantics. | `a49131d`; cheap mutation RED/GREEN and full race acceptance in P03. This fixes the planned immutability requirement, not a new feature. |
| D05 — P04.S02 before S03/S04 | Client/error declarations moved with component dependencies so the standalone package could compile without importing host modules. Behavior was revised in S03/S04 as planned. Local widget Save/refresh pending state stays in the widget; workspace async orchestration stays in the host. | `335f29b`; independent package host and component isolation tests, EVAL-424; ADR-HLLM-033 §4. |
| D06 — P04.S08 | Keep summary pages in memory and fetch the full record on its existing details expansion, rather than eagerly fetching every record. Kirill explicitly approved this for records up to 17 MiB. Existing controls and complete stored data remain. | `335f29b`; TEST-448 and P04 history/trace/restore cases; plan action updated when approved. |
| D07 — P05.S02/CHECK-422 | The planned Agent Platform standalone PostgreSQL/network was absent. Its live service lock points to Temporal's existing `compose-postgres-1` on `compose_default`, physically owned by `self-imp-bin-eval`. Kirill approved the isolated `harden_llm` DB/role there on 2026-10-13. No second database daemon was started. | `b4d270d`; P05 owner inspection, P06 restricted-role/sentinel and Garage lifecycle proofs; ADR-HLLM-034 §5 and cutover runbook. |
| D08 — branch/preview execution | Completed the one approved dev reset, then pushed phase checkpoints on the feature branch and promoted to main for the authorized production release. Did not deploy phase previews: trusted assets were main-owned and the preview runner/dev apps were intentionally off under PR #79's RAM policy. No dev acceptance is claimed. | P00/P02/P06 logs; `docs/preview-environments.md`. The plan allowed dev merges only at deployable boundaries; it did not require waking an on-demand preview. |
| D09 — P05.S04–S06 harness/telemetry | The composed gate exposed lost matched-route metadata in HTTP telemetry. Passing the context-bearing request to the router restores its bounded route label. The harness shares one application-image preparation helper, separates bounded build/startup time, and retains verbose composed evidence. S05's final unchanged run also supplies S06 measurements. | `b4d270d`; `TestHTTPObservationUsesMatchedRoute` RED/GREEN, TEST-433 actual composed acceptance with 4/4 correlations; no second runner or duplicate application engine. |
| D10 — P06.S01 security | Removed optional capacity arithmetic flagged by CodeQL without changing append output. Specific digest-hashing alert dismissed only after proving raw bearer validation precedes non-authenticating scope derivation. Go 1.26.6 → 1.26.9 and x/net 0.58.0 → 0.60.0 plus required dependencies address reachable vulnerabilities. | `b01e3c3`, `265e241`; TEST-425 digest denial, exact-source release vulnerability gate and all 28 tasks; ADR-HLLM-011. No scanner query or security assertion disabled. |
| D11 — P06.S01 test execution | Cold build/static command budgets changed 180/120 s → 600 s, matching existing lint, after a measured cold timeout. Integration-race moved from shared `service` to existing exclusive `release` resource to remove competing compiler load from SQL timing. Command, race flag, two package slots, SQL bounds and all assertions remain. Standalone tier validator now matches the actual verbose Compose command. | `f212610`, `5267c61`, `11af8a9`; cold measurements, isolated real-PG race and full hosted release; ADR-HLLM-015. Failed attempts remain failed. |
| D12 — P06.S02–S04 deployment verification | Fixed the operator helper/runbook to expect `gh --attempt`'s attempt URL. Fixed the checker to resolve Compose aliases to actual network names; the first apply had schema/app success but failed its postcheck. Corrected check is equivalent and repeated apply is a no-op. Caddy merged into its already-deployed Portal branch, not its older main; retired only the exact API CNAME after main-origin acceptance. | `358361a` / PR #105; TEST-234 wrong/extra/missing network cases, TEST-450 schema ordering, TEST-434/435 deployed proof. Caddy PR #9 at `0afe6c6`; ADR-HLLM-028/KER-IBD-004/005. |

These changes did not introduce another service, compatibility adapter, storage
owner or inference path. Original assertion failures, fixture mistakes and
command-preparation mistakes remain in the chronological P01–P06 logs; those
are not alternative passing evidence. The [P06 checkpoint](phase-p06.md) links
the accepted exact-source release and application/owner identities.

## 3. Closeout omissions corrected by this audit

1. ADR-HLLM-033/034 still described implementation/deployment as pending. Their
   status and implementation refinements are now recorded. Older ADR-013/016/018
   have explicit supersession boundaries instead of appearing to authorize
   seeding, model fallbacks or an artifact journal.
2. The backend stack specification still called the removed Phoenix-owned history
   architecture current. Its current header now points to ADR-HLLM-033/034 and
   labels conflicting historical sections. TEST-420 now includes that omitted
   canonical document; `audit-stack-contract-red.log` retains the actual failure.
3. The race resource guard used TEST-008, which belongs to retry behavior. Its
   annotation/ADR now use existing TEST-048 (manifest contract). No function,
   assertion, deadline or manifest selection changes; old evidence labels stay.
4. The image KERs/runbook did not spell out the approved scope expansion or the
   destructive clean cut's recovery limit. Prior images/checkpoints are retained,
   but the old app cannot recover its discarded database through image rollback.
   Only schema-compatible rollback or a newly certified forward fix is supported.
5. Issue #100's body and a plan branch-state paragraph still said implementation
   was pending. Updated current pointers without rewriting historical phase logs.
6. Disposable operator helpers, profile/test binaries, private scratch/build logs
   and owned test artifacts were left behind. The cleanup inventory distinguishes
   removed temporary files from retained Git evidence, operational checkpoints,
   active settings, shared data, release images and reusable dependency caches.
   Three synthetic workers and their time wrappers had outlived their absent
   parent; the existing release barrier stopped all six. The interruption cause
   is unverified and tracked separately in [issue #107](https://github.com/prls-co/harden-llm/issues/107).

## 4. Verification and remaining work

The original application remains certified by run 38377471293 attempt 1 at
`bbdb5d77bc76424804427ca30a7bf9ddc453d0cf`: all 28 tasks and nested `make verify`
passed. Audit verification reruns the affected contract/manifest/retry/Node
checks, validates plan references, and checks actual deployed identity and public
HTTP using the existing TEST-434/435 paths. It does not rebuild unchanged apps
or repeat database provisioning, schema reset, provider inference or browsers.
Final audit source/PR/main/check identities are recorded in the issue closeout;
`audit-verification.json` and `audit-cleanup.json` retain local evidence.

No implementation step remains inside this plan. Browser layout, interactive
Portal login and live-provider inference remain explicitly unperformed under
the accepted opt-in boundary. No high-availability, production latency SLO,
off-host restore or downstream-consumer migration is claimed.

## 5. Next concrete steps beyond this plan

1. Investigate the synthetic-runner interruption leak in issue #107 with a
   bounded process-local reproduction; preserve the resource/overlap assertions.
2. Run an explicitly authorized browser acceptance of Portal entry, the profile
   editor, JSON repair controls, history expansion/restore and embedding against
   the frozen PR #95 baseline. Use local scripted providers for UI cases.
3. Authorize a bounded real OpenAI-client check using the production `/v1` base
   URL and existing deployment token: one text and one structured-output call,
   with attempts and cost recorded. This proves the live provider path separately
   from deterministic compatibility and HTTP auth/model discovery.
4. Exercise a private restore drill for the HLLM database, stable credential key
   and Garage objects. Retain one complete provider-credential roundtrip and one
   history/artifact read in an isolated restore; never test restore over live data.
5. Pilot one `HardenLlmUI` component in Model Recall with an explicit host context.
   Keep its admission/lifecycle in Model Recall. Split services only if that real
   consumer demonstrates a need for independent deployment or scaling.

These are recommendations, not started migrations, enabled preview services or
newly required gates for the completed plan.
