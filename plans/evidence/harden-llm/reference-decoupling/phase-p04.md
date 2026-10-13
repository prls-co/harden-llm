# 1. P04 reusable reference UI checkpoint

- Plan `PLAN-HLLM-REFERENCE-DECOUPLING-002` v5.3; branch `feat/reference-decoupling`; input checkpoint `a49131d165e31394983d922f496baa38901ce6d6`.
- P04.S01–S14 complete. Next **P05.S01**. Issue #100 records the pushed checkpoint identity.
- Pinned Go/Node/Elixir/OTP toolchains; new decoder dependencies are pinned in both Mix locks. No browser, real provider or production mutation ran. Browser layout is unverified.

## 2. Implementation and boundaries

One independent `HardenLlmUI` package owns the existing picker/editor, input, result, statistics, history, trace, JSON and pagination components, form/option transformations, client and styles/hooks. Host routes, Portal admission, server configuration, draft keys and workspace async lifecycle remain in Phoenix. There are no compatibility delegates, host/Portal/Ecto imports in the package, copied helpers or second client request constructors.

The explicit server `Client.Context` and host `APIContext.server/1` replace host-configuration reads in reusable code. One bundled canonical OpenAPI decoder validates response/status/error fields; Go checks the bundle is byte-identical to the canonical contract. Downloads validate binary/media contracts and preserve bytes independently of history. Same-origin trace/artifact delivery has one controller and no external redirect or Portal restart on a server-key rejection.

Restored baseline `/profiles` and `/embed/llm`, complete profile inputs, immutable new UUID/date Save, draft refresh, preset hiding, entered-credential revisions, bundle upload/download and all five repair/rerun leaves. Full history loads on existing details expansion, explicitly accepted by Kirill. Recorded settings restore without Save/Run/name matching; captured accounting remains exact/partial/unknown. One component source owns each markup/style/decision. Custom CSS and JSON-viewer CSS exactly match the pinned PR #95 baseline.

## 3. Verification

- [EVAL-424](p04-eval-424.json): all 32 frozen capabilities map to passing executable cases at seeds 104729/130363/155921; 45 focused host cases and 44 standalone package cases pass at each seed. Independent input/result/stats/history/pagination/picker instances and one production-source owner are verified.
- [Final fast report](p04-final-fast.json): all ten tasks accepted, zero cleanup errors/warnings; full deterministic host 89 pass with three existing opt-in exclusions; package 44 pass; existing client decisions and seven progress/CLI Node cases pass.
- [Asset report](p04-final-assets.json): compile, asset deployment and actual compiled-pagination CSS check pass. Final `MIX_ENV=test mix format --check-formatted` (including the package) and `mix compile --warnings-as-errors` pass. Plan structure/local links and whitespace pass.
- P04 changes no SQL/Garage/core execution owner; their distinct integration/race gates remain P03 evidence and return in P06 release certification. No source/HTML check is claimed as browser layout or native hook delivery.

## 4. Retained failures and next gate

[Chronological progress](phase-p04-in-progress.md) retains every RED/preparation/measurement failure, including canonical binary decoding, draft/catalog preservation, nested async waits, the real editor Save echo, healthy empty catalogs and edited-option reload. The original persistence/no-replay/full-input/statistics assertions were retained. One bounded test wait observes dependent operations without reissuing them or increasing their deadline; the production echo was removed and runtime-option parsing now has one owner.

Resume P05.S01. CHECK-422 still gates shared PostgreSQL/Garage/encryption provisioning; trusted main-owned preview assets must use the reviewed metadata before promotion. P05 production schema ordering/topology/composed checks and P06 exact-SHA release/main/deployed HTTP acceptance remain. No dev/main promotion or production change is claimed.
