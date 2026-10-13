# 1. Restore Reference Workflows and Decouple Harden-LLM

- Project: Harden-LLM.
- Version: 5.3, one API-key storage scope with per-Portal-user drafts; explicit saves create complete profile snapshots; reference history lives in the HLLM PostgreSQL database; artifacts use key-scoped Garage paths; Model Recall guides component reuse; uncertain history commits stay unconfirmed without positive readback; production apply explicitly runs the schema command.
- Document ID: `PLAN-HLLM-REFERENCE-DECOUPLING-002`.
- Date: 2026-10-12 (v5.0 published at `2f12ae030b2420c132caa126933f65f9bc9b16c9`; v5.1 revised 2026-10-11 at `a90353fa7b7af622f757b0e0f0ba9ed4b82e6dcc`; v5.2 applies the [v5.2 review decisions](../ask_me/reference-decoupling-v52-review-decisions.md) and was published at `f54b8aeff276e980ac0c36684883bea5856dc4d7`; v5.3 applies the two readiness-review corrections accepted by Kirill, recorded in section 7.4).
- Owners: Kirill, product and architecture decisions; HLLM maintainer, implementation and release; Agent Platform, Garage, Control Plane and Caddy maintainers, their shared-service contracts.
- Status: Accepted current product contract: profiles, drafts, entered provider credentials, cache, history and artifacts are scoped to the validated incoming HLLM API key. Portal controls frontend admission only. Explicit Save creates a new complete profile snapshot. Model Recall is the first interface-design reference for reusable components. Nonblocking recording remains accepted. v5.1 changes four v5.0 decisions after review: reference history moves from Laminar to one key-scoped table in the HLLM PostgreSQL database, the way baseline `llm_runs` stored it; artifacts are scoped by their Garage object path; the restored workspace draft is partitioned by the host-supplied stable Portal user ID inside the key scope; and key-rotation data handling is deferred to manual operator work. v5.2 fixes execution gaps found in review without changing product scope: gateway storage settings move into P02 so every `dev` merge stays deployable, previews report artifacts disabled, P03 builds the artifact prefix delete before history delete/clear, status exposes history size, all settings keep the `HARDEN_LLM_*` prefix, and history full-row SQL gets named time budgets. A second v5.2 pass makes the plan executable by a smaller implementer: RED steps in P02 and P03 list numbered cases, section 5.2 adds implementer notes, TEST-425 is storage-free while TEST-441 becomes the integration test for HTTP storage isolation, scope selectors get the `scope_selector_rejected` error, the schema command runs as a one-shot Compose service, and every environment gets its own credential-encryption key. The current priority is product-market-fit functionality; security hardening beyond the stated invariants is deferred. Earlier [reference-decoupling](../ask_me/reference-decoupling-decisions.md) and [history-policy](../ask_me/shared-history-retention-and-access-decisions.md) records retain historical context; their user-ID ownership of shared data, additional REST session verification, mutable saved-profile overrides, Laminar history ownership and deployment-wide deletion rules are superseded by this revision. Implementation is in progress; actual completed steps and gates are recorded in section 11 and the reference-decoupling phase ledger.
- Planning source: `879106ff5da3b0b0d90f31e9d7e6f897e4888bbc`.
- UI behavior baseline: `754690c8d0890c664d69fbdf526b16b3e5601060`, PR #95, before PR #97.

Restore the baseline Harden-LLM UI and its useful workflows while separating one stateless Go execution engine, a reusable stateful wrapper, shared services, reusable Phoenix components, and shared ingress. Ordinary OpenAI-compatible clients use one base URL and a configured HLLM bearer without profiles or login. Reference REST uses that same bearer contract for API-key-scoped profiles, history, drafts and diagnostics. The core owns no authentication or storage scope. Reuse Agent Platform PostgreSQL for saved application state and reference history, existing Garage for artifacts, and existing Laminar only for the diagnostic traces it already receives; remove superseded code/storage without migrating obsolete HLLM data.

v5.3 changes only two execution contracts: a missing or failed readback after an uncertain COMMIT means `unconfirmed`, because a delayed commit can become visible later; and production `runApply` explicitly runs the accepted image's one-shot schema service before starting selected applications, retaining `--no-deps`. These corrections add TEST-450/451 within existing steps; there are still seven phases and 72 steps. P00–P03 are complete at the phase checkpoints; P04 is next. Main and production have not been promoted. Section 11 links the verified phase checkpoints; uncommitted work is not a completed phase.

## 2. Design consensus and trade-offs

| Topic | Verdict | Decision and rationale |
| --- | --- | --- |
| Meaning of previous | DECISION | Restore the HLLM markup, styling, interactions and feature semantics at the full baseline SHA above. Do not restore arbitrary older code, utility-llm's React UI, or old database contents. |
| Stateless versus pure | DECISION | Name the core stateless. It performs effects through injected provider/cache/event dependencies; it owns no durable state, service lifecycle, authentication or telemetry SDK. Keep `Client.Call(ctx, request)` where practical. |
| Reliability ownership | FOR | One core owns retries, JSON repair, recovery/error fixers, validation, budgets, cache identity/hit validation and per-call accounting. The wrapper implements concrete service access and application behavior. |
| Libraries and services | DECISION | Add public Go package `stateful/`; extract a Phoenix package under `frontend/packages/harden_llm_ui/`. Compose one Go process and one Phoenix app. Model Recall supplies the first real consumer interface review; its migration is outside this release. An injected interface does not require another HTTP service. |
| Saved profiles | DECISION | `/profiles` is the restored Phoenix page; `/api/v1/profiles` belongs to the stateful wrapper. Explicit Save creates a new complete snapshot of the provider definition and all run inputs. `/v1/models` discovers models and cannot replace profiles, credentials, bundles, pricing or recovery controls. |
| Data scope | DECISION | One `api_key_hash`, derived by SHA-256 from a validated incoming HLLM access key, scopes saved profiles, drafts, entered provider credentials, cache, history and artifacts. Same key means same data across clients/logins; different keys mean separate data. Inside that scope, a draft is further selected by a host-supplied `draftKey`; the reference host uses the stable Control Plane user ID so concurrent Portal users do not overwrite one another's unsaved form. The draft key is opaque data, never authority. No separate user/owner entity, key-to-user mapping, local user directory or key-issuance API. |
| Configuration ownership | DECISION | Git owns credential-free starting presets. Deployment `.env` supplies accepted HLLM access keys, selected frontend key, initial provider keys and operational secrets. PostgreSQL owns explicitly saved complete profile snapshots, mutable drafts and encrypted entered provider credentials. A selected preset fills the draft; saving copies only that chosen setup, with no live inheritance or restart synchronization. |
| Public API | DECISION | `https://harden-llm.prls.co/v1` with `Authorization: Bearer <HLLM_ACCESS_KEY>` supports the documented OpenAI-compatible subset. `CPA_API_KEY` is upstream-only. The incoming key alone also authorizes reference REST. Neither the core nor clients parse `.env`; deployment loads approved environment sources. |
| Reference API | DECISION | Preserve one feature-complete reference execution route, proposed `/api/v1/run`, with live progress and diagnostics. It shares core validation/execution with `/v1`; wire formats serve distinct consumers. Do not duplicate engines or retain route aliases. |
| Reference access | DECISION | Publish `/api/v1` and `/v1` on the same origin with one bearer-authentication boundary. Derive `api_key_hash` after successful key validation and pass it to wrapper operations; reject caller-supplied ownership/scope fields. No Portal session reference, user ID or second authentication header is required by Go REST. |
| Frontend admission | DECISION | PRLS Portal/Control Plane continue to own logins, passwords, sessions, resets and disabling users. Every enabled login may enter the frontend without company selection or an HLLM grant. Portal identity does not select stored HLLM data; the frontend uses its configured HLLM access key. No user-to-key assignment or new key-selection UI is included. |
| History scope | DECISION | Only reference execution calls enter product history, scoped to their validated HLLM access key. Any holder of that key may read, restore, delete entries or clear history in that scope. Different keys cannot access one another's records, statistics or artifacts. Direct `/v1` calls do not enter product history. |
| History retention | DECISION | Keep records until a holder of the key deletes or clears them. No automatic expiry, retention setting, quota or scheduler in this release; operator cleanup of obsolete HLLM rows/objects stays manual. `/api/v1/status` exposes history size so P06 and later releases record real growth; revisit when the table passes the 5 GiB review trigger. |
| History failure | DECISION | Accepted 3B: a resolved, authorized call proceeds without a successful history write. Preserve the inference result and report recording separately. Disabled, misconfigured, empty, failed and unconfirmed history states must not be conflated. No outbox, spool or inference replay. |
| Debugging | DECISION | One structured error model supplies safe UI messages and detailed operator diagnostics: stable code, operation, correlation IDs, bounded timing, sanitized cause and separate inference/persistence outcomes. Reuse existing logs/traces; never expose credentials or secret connection strings. |
| History owner | DECISION | v5.1: the wrapper-owned HLLM PostgreSQL database is the sole history owner, using the same pool and schema command as profiles/drafts/cache. Insert, numbered pages, total counts, statistics, delete and clear are ordinary key-scoped SQL. This restores the baseline `llm_runs` ownership without its old engine/migrations. Laminar no longer owns product history: its pinned deployment showed no demonstrated service-key SQL read and no supported service-key delete, and adopting it would add an HTTP integration, readback polling and an external blocking gate. Laminar keeps receiving existing sampled diagnostic OTEL traces only. |
| Shared persistence | FOR | Use Agent Platform's existing PostgreSQL service with isolated HLLM database/role and one bounded Go pool. Store key-scoped snapshots, credentials, hidden-preset preferences, cache, drafts and reference history there. Garage holds artifact bytes under key-scoped paths. No database/service per key and no dedicated HLLM PostgreSQL deployment. Remove Phoenix Ecto persistence after moving its responsibilities. |
| Simplicity | AGAINST | No universal plugin/configuration graph, authentication framework, microfrontend per widget, generic cache microservice, Temporal workflow, compatibility layer, duplicate CSS or second scheduler. Infrastructure configuration stays typed deployment configuration. |
| Clean cut | FOR | Breaking HLLM contracts and deletion of obsolete HLLM-owned data are allowed. Replacing an incoming HLLM key starts a new storage scope; no automatic ownership transfer, dual-key alias or legacy reader. Key-rotation data handling is deferred: if rotation is needed before a designed flow exists, an operator may manually rewrite `api_key_hash` values and Garage prefixes for HLLM rows/objects; entered provider credentials must then be re-entered because their encryption binds the old hash. Other products, shared identities/services, credentials and unrelated dirty work remain outside the reset. |
| Acceptance | DECISION | Use browser-free component, contract, database and composed checks. Browser layout and live-provider acceptance require separate explicit requests; neither may be claimed from these checks. |

Baseline capabilities include profile picker/editor/CRUD/bundles/model refresh/credential replacement; prompt/schema inputs; JSON repair/recovery/error-fixer controls; response/JSON viewer; usage/cost/attempt/cache/progress details; history pagination/restore/delete/clear; trace/artifact views and downloads; `/embed/llm`; and Portal login/logout. The accepted behavior changes are API-key ownership for all stored application data (with per-Portal-user drafts inside the key scope) and create-on-save profile snapshots containing the complete setup. Preserve the baseline visual design and other feature semantics. Loading/restoring only changes the current key's draft; explicit Save and Run remain separate actions.

### 2.1. Grounded evidence and limits

- Current boundaries are in `AGENTS.md`, `plans/proxy-and-reference-app-simplification-plan.md`, `api/openapi.yaml`, `docs/adr/ADR-HLLM-031-proxy-reference-boundary.md` and ADR-HLLM-032. P00 reconciles these with the new wrapper direction; historical evidence stays historical.
- Current root `client.go` initializes concrete dependencies; Phoenix `frontend/lib/harden_llm/reference.ex` owns Ecto history/drafts. These are the ownership seams to change, not proof that the target already exists.
- Agent Platform source is `/home/kirill/p/agent-platform-infra/compose/base/compose.yaml`; its `postgres` service and `compose/base/postgres/init/00-databases.sh` do not yet provision HLLM. Existing initialized volumes need explicit provisioning, not just a new init file. Live grants/network attachment still need owner verification. Consult `/home/kirill/p/ops/tech/technical_strategy_constraints.xml` and the service records before infrastructure edits.
- Laminar evaluation (superseded as history owner in v5.1): inspection on 2026-10-09 matched `/home/kirill/p/laminar-shared/deployment.lock.yaml`, revision `5f14c5c86f1bfc18c852dbc7c712609e44b8d69c`, v0.2.1. `POST /v1/spans` accepts spans through a queue (acknowledgement is not storage), project-scoped `POST /v1/sql/query` is SELECT-only and returned 404 for the existing HLLM key, and trace deletion exists only behind native frontend sessions. No supported service-key read or delete was demonstrated. These gaps, plus the extra HTTP integration and readback polling, are why v5.1 stores product history in PostgreSQL. Laminar continues to receive existing diagnostic OTEL traces.
- Baseline `754690c` stored history in PostgreSQL (`internal/postgres/migrations/0011_application.sql`: `llm_runs`, `llm_traces`, owner-scoped) and artifacts in Garage with a PostgreSQL artifact journal and reconciler. v5.1 keeps the PostgreSQL history ownership with a smaller key-scoped schema and drops the journal/reconciler in favor of key-scoped object paths.
- `internal/runtime/telemetry.go` excludes prompt/response payloads; `deploy/otel/collector.yaml` removes content attributes and configures tail sampling with a 75-second decision wait. Export acceptance is not query visibility or durable history. Diagnostic traces are therefore not product history; the complete record is written to PostgreSQL.
- Current `frontend/lib/harden_llm_web/router.ex` uses shared `PrlsWeb.Access` with `:require_access`; `/home/kirill/p/masked-recall/web/lib/model_recall_web_web/router.ex` uses the same integration with an additional `{:product, "model-recall"}` requirement. Reuse the shared identity authority, not Model Recall's account/product access policy.
- `/home/kirill/p/prls-portal/lib/prls_portal_web/router.ex` exposes operator-only `/admin`; `admin_live.ex` implements create-user, reset-password and disable-user through Control Plane. `/home/kirill/p/prls-control-plane/contracts/openapi.yaml` and `docs/operations.md` own those contracts. This is source evidence, not a claim that live provisioning was exercised.
- Current Go REST `internal/gateway/httpapi/httpapi.go:authorize` checks one bearer; Phoenix resolves Portal identity for frontend admission/drafts. The target replaces frontend user-ID draft ownership with key scope and generalizes the one HTTP bearer validator to the configured key list. Shared Control Plane token issuance exists in its source, but its account/product permissions are outside this design: do not add that verification path or an HLLM token registry. Source evidence is not deployed acceptance.

### 2.2. API keys, complete saved profiles and frontend admission

The incoming **HLLM access key** authorizes clients to Harden-LLM. It is distinct from upstream provider credentials such as `CPA_API_KEY`, the credential-encryption key, and the Garage service credentials. Derive `api_key_hash = hex(SHA256(validated_access_key_bytes))` after bearer validation. Hash the entire exact credential, not its display prefix. The hash is an internal storage scope, not another accepted credential or a browser-supplied selector. Use high-entropy deployment access keys; never persist their plaintext in profiles, drafts, Laminar, Garage metadata, logs or traces.

| Information | Authority | Update rule |
| --- | --- | --- |
| Starting preset IDs, base URLs, model names, prices and options | Credential-free Git catalog | Selecting a template fills a draft. Do not seed every template into every key scope. |
| Accepted HLLM access keys | Connection configuration's `api_key_envs` names plus protected environment values | One required array of environment-variable names is the authoritative allowlist. Initially use `["HARDEN_LLM_TOKEN"]`; add another environment-variable name only for a deliberately separate key. No raw keys in JSON, startup-generated keys, database registry or implicit scalar fallback. |
| Frontend's selected HLLM access key | Protected shared deployment environment, `HARDEN_LLM_TOKEN` | The host passes this server-held key in the ordinary Authorization bearer header. Its environment name is an accepted `api_key_envs` entry. The gateway and frontend receive the same approved `.env` value; do not create a duplicate secret or another gateway authentication path. |
| Initial provider keys and service/encryption secrets | Protected deployment environment / `.env` | Keep deployment credentials environment-backed. Git names only provider connection metadata/environment variable names. |
| Saved profile snapshots, drafts, entered provider credentials, cache and reference history | Wrapper-owned HLLM database in Agent Platform PostgreSQL | Key-scoped. Explicit Save creates a new snapshot; autosave updates the caller's draft only (draft key chosen by the host; the reference host uses the stable Portal user ID). Entered provider credentials remain encrypted separate records referenced by the snapshot. History rows are inserted once per reference execution and removed only by key-scoped delete/clear. |
| Artifact bytes | Existing Garage bucket, object path `artifacts/<api_key_hash>/<run_id>/<artifact_id>` | The path prefix is the scope: downloads build the object key from the authenticated hash and never accept a caller-supplied key, so access works whether or not history was recorded. Delete/clear remove the run's prefix. |
| Human identities, passwords and sessions | Shared PRLS Control Plane/Portal | Control admission to the frontend only. The reference host passes the stable user ID as an opaque draft key; no human ID scopes profiles, credentials, cache, history or artifacts. |

The same access key gives the same data across SDKs, backends and Portal logins. Different access keys give isolated data. The reference deployment initially selects the existing deployment key, so all its enabled logins share that key's profiles, history and statistics. Each login keeps its own unsaved draft through the host-supplied draft key. This is intentional key ownership, not per-person isolation: any login can still open another login's draft through the same key if it supplies that draft key. Another backend/reference host can supply a different configured key through the same REST contract. No automatic Portal-to-key mapping or browser key picker is added.

Use the existing profile-save control to save the complete current setup: provider definition/base URL/model/prices, system/user prompts, schema and shorthand, reasoning, provider options, web search, retries/repair/rerun settings, cache choices and timeout. Save generates a UUID and server UTC timestamp and creates a display name such as `CPA JSON · 2026-10-11 14:32Z · a7c92e` using a short UUID suffix. UUID is the identity; the generated name is display text, not a content hash or an authorization key. Editing a saved setup changes the draft. Saving it creates another snapshot and never updates the old snapshot. Repeated explicit saves are separate records; no deduplication, parent/version graph or automatic snapshot per keystroke.

Keep one complete snapshot representation for saved profiles and history's safe resolved settings. A snapshot references an explicitly selected deployment or entered provider credential; it does not contain a plaintext key. A changed/deleted Git template cannot alter a saved snapshot. Model refresh updates the current draft's model catalog; explicit Save creates the next profile. Credential replacement updates its separate key-scoped credential record and cache identity while saved nonsecret setup remains fixed. Invalid/missing credentials fail before dispatch; never substitute an environment key.

Portal operators continue using the existing `/admin` create/reset/disable flow. The enabled-user check stays in the Phoenix host. Go REST does not call Control Plane, accept a session reference, or require an account/product grant. Changing Portal identity alone does not change the selected HLLM scope. Replacing/removing an HLLM key removes access through that credential; a new key selects a fresh scope. Old rows/objects remain until explicit HLLM-only cleanup (manual for now); no startup deletion or implicit key-to-key transfer is authorized.

The reusable Phoenix client accepts a host-supplied server-held API key and draft key. It does not own login, discover keys, copy sessions, or resolve a user ID itself. The stateless core receives neither incoming keys nor key hashes; scope enters through concrete cache/history/artifact implementations in the wrapper.

### 2.3. History states and actionable diagnostics

History is stored in the HLLM PostgreSQL database. Expose it with an explicit enabled/disabled setting. Enabled history with a missing or invalid database configuration is a configuration error; a runtime outage never changes the setting to disabled.

| Condition | Required behavior |
| --- | --- |
| Explicitly disabled / intentionally not set up | Report history disabled as information; perform no history I/O and do not label inference failed. |
| Enabled with missing or invalid configuration | Validate and report a configuration error on the history feature/readiness surface. Preserve deployment-configured inference; do not silently disable history or discard the diagnostic. |
| Healthy query returns no records | Show an empty history only after a successful read. |
| Insert committed | Report saved. One record per reference execution during healthy operation. |
| Definite write failure | The insert failed before commit (pool unavailable, constraint/oversize rejection, statement error). Preserve the result and report not saved, with the cause and run ID. |
| Commit outcome unknown | The connection failed or timed out after COMMIT was sent. Perform one bounded read by run ID on a fresh connection: found means saved; absent or failed readback means unconfirmed. An empty read cannot rule out a delayed commit. Never re-insert, poll or rerun inference. |
| Read failure | Report unavailable; never present a failed read as an empty history. |

Generate a run ID in memory, validate authorization/profile/credentials, execute once, then attempt one bounded insert. No history write is an inference prerequisite. Progress and current-call statistics come from the execution itself. History-derived totals cover available records only; missing records are not zero-cost calls. A process crash before the insert loses that record in this synchronous design.

Use one structured failure value for reference responses, UI messages and sanitized logs. Include a stable code, component/operation, execution/request IDs, trace ID when available, history mode, separate inference and persistence outcomes, cause category (configuration/authentication/connection/timeout/schema), duration and a safe corrective action. Bound message size; do not copy raw driver errors containing DSNs, tokens, passwords, cookies, session references or user payloads into diagnostics. Preserve existing OpenAI error envelopes on `/v1`; define new reference fields/codes in OpenAPI before implementing them. Reuse the current logging and diagnostic OTEL/Laminar integration rather than adding a debug service.

### 2.4. Confidence review and unresolved engineering checks

The current policy is resolved: history stays until a holder of the accepted HLLM key deletes or clears it, and any holder of that key may do so. Earlier login-wide deletion in the [history-policy record](../ask_me/shared-history-retention-and-access-decisions.md) and Laminar ownership are superseded. Remaining checks below are engineering work; they do not reopen UI preservation or shared identity decisions. Detailed step definitions and document consistency checks are not evidence that these integrations already work.

| Confidence area | Current evidence and required follow-up |
| --- | --- |
| History storage: high | Baseline used PostgreSQL history; v5.1 reuses the planned HLLM pool and schema command. No external owner gate. Full-record size, page/count queries and delete/clear are proven by integration tests against a real PostgreSQL lease (TEST-427/442). |
| Artifact scope and cleanup: medium | Resolved design: Garage object key `artifacts/<api_key_hash>/<run_id>/<artifact_id>`, built only from the authenticated hash. Downloads therefore work when history is disabled or failed, and a foreign key cannot construct another scope's path. Delete removes a run's prefix before its history row; clear does the same per run. Objects from runs whose history was disabled, not saved or unconfirmed are not cleaned automatically; an operator can remove a key prefix manually. Verify Garage prefix list/delete permissions in P05 (CHECK-422). No metadata database, journal, reconciler or lifecycle rule. |
| Artifacts in previews: decided | v5.2: no preview, including `dev`, has Garage; artifacts report disabled there and TEST-443 proves Garage behavior against a real lease. A `dev` bucket can be added later under CHECK-422 if deployed trace views on `dev` matter for product feedback. |
| Shared service deployment: medium | Source identifies Agent Platform PostgreSQL and existing frontend Portal admission; HLLM database/role grants, actual network access and Garage bucket credentials still require P05 owner checks. Every configured HLLM access key must be isolated at all storage boundaries; no Go identity-authority integration is required. Repository configuration alone is not live acceptance. |
| Exact baseline preservation: medium | Baseline SHA is fixed, but individual implementation details still need comparison before extraction. Baseline bundle import probes candidate providers; preserve that behavior and its test oracle. Component/HTTP checks do not establish actual browser layout. |
| Key rotation: deferred | Rotation starts a fresh scope. Manual operator handling only, as described in section 2. Not a release blocker. |

## 3. PRD: stakeholder and system needs

- Problem: the simplification removed useful UI workflows and conflated separation of history with removal of application capabilities. The desired core remains a robust LLM caller usable independently.
- Users: ordinary OpenAI-compatible clients; Go library consumers; applications embedding the stateful library or Phoenix components; enabled Portal users of the reference app; shared-service operators.
- Value: familiar API access, preserved reference workflows, reusable components and fewer independently operated services.
- Business goals: restore capability without redesign; maintain one implementation per responsibility; keep service/RAM footprint small; make ownership and release evidence understandable.
- Success metrics: all baseline manifest capabilities pass with the two accepted semantic changes recorded; zero direct-call history records; zero cross-key profile/credential/draft/cache/history/artifact access; same-key data shared across two client contexts; zero lost unsaved drafts between two concurrent Portal users of one key; zero overwritten snapshots after redeployment or Git changes; zero duplicate provider calls caused by storage/telemetry failure; zero added production service instances; all required exact-SHA release gates pass. One isolated logical HLLM database reuses shared PostgreSQL. No new throughput/latency SLA is asserted.
- Scope: stateless core boundary, wrapper/profile/cache/draft/history ownership, reference API, reusable UI, shared-service integration, ingress, canonical docs and eventual release.
- Non-goals: full OpenAI/CPA feature parity, provider-token progressive streaming, durable job orchestration, automatic inference replay, old-data migration, unrelated service retirement, high availability or a new identity system.
- Dependencies: shared PostgreSQL grants/network; existing Garage bucket with prefix list/delete; frontend Portal admission; shared Caddy; approved build/deployment credentials. Laminar is not a dependency of product history. Model Recall is reviewed read-only to guide component contracts; its migration is not a release dependency.
- Risks: hidden baseline behavior, history table growth without retention, orphaned artifacts from unrecorded runs, a missed key predicate, unsafe credential exposure, snapshot/draft confusion and accidental replacement of saved credentials by deployment defaults.
- Assumptions: breaking HLLM-only changes remain acceptable; product-market-fit functionality takes priority over further security hardening; the existing deployment key initially serves the reference host; configured additional keys use the identical bearer contract; replacing a key creates a fresh scope and is handled manually for now; cache-off inference must work during storage/Control Plane outage; current test/build infrastructure is retained. Missing owner contracts block dependent work rather than creating a fallback.

## 4. SRS: canonical requirements

| ID | Type | Requirement and acceptance criteria |
| --- | --- | --- |
| REQ-420 | func | Restore every capability enumerated in section 2 using the pinned HLLM UI layout/styles/interaction contracts. Record intended ownership changes explicitly; remove no feature silently. |
| REQ-421 | func | Root Go library performs all reliability decisions through injected effects, with immutable per-call configuration and no owned service lifecycle/global state. Concurrent calls cannot mutate one another. |
| REQ-422 | int | One public `/v1` base exposes models, chat completions and responses with a configured HLLM bearer alone. Ordinary SDK/curl requests require no saved profile, Portal identity or custom scope header. Preserve the documented subset, tools, errors and final-only SSE; cache is scoped to the authenticated key without placing that key in the core. |
| REQ-423 | func | Wrapper owns key-scoped complete saved-profile snapshots, deletion/hiding, encrypted bundles, provider credentials, model discovery and starting-template selection. Explicit Save generates a new UUID/date/suffix and snapshots every run input and provider setting. Editing/refresh changes the draft; no saved-profile update or live preset inheritance exists. Resolve one validated snapshot/credential binding before execution; deleted/invalid profiles fail without provider calls or substitution. |
| REQ-424 | data | One shared PostgreSQL pool owns profile/credential/hidden-preset/cache/draft/history persistence with isolated HLLM resources. Every row/namespace is key-scoped. Core owns cache eligibility/key/hit validation; wrapper owns scope/storage/expiry and bounded write-triggered cleanup. No Phoenix database owner remains. |
| REQ-425 | security | One Go REST bearer boundary validates a configured incoming HLLM key and derives its SHA-256 fingerprint server-side. Both `/v1` and reference `/api/v1` use that credential alone. Missing/invalid keys fail before storage/provider work; caller-supplied ownership/hash/session fields cannot select data. Portal/Control Plane own frontend login/admission only. No HLLM user/password/key-issuance store, per-user key assignment or Go session introspection. |
| REQ-426 | data | The HLLM PostgreSQL database alone records reference executions with server-derived `api_key_hash`, including cache hits and terminal validation errors when enabled. Healthy operation inserts one complete record per execution. Disabled/configuration-error/empty/saved/not-saved/unconfirmed/read-failure outcomes differ; recording failure preserves inference. List/detail/stats/restore/delete/clear and associated artifact cleanup enforce the same key. Preserve numbered pagination/total counts and full restore into the current draft. Records stay until key-scoped delete/clear; no automatic expiry, shadow store or hide-only substitute. |
| REQ-427 | data | Profiles, encrypted entered credentials, hidden-preset preferences, drafts, cache, history and artifacts are scoped to `api_key_hash`, never raw access keys. Within that scope, drafts are selected by a host-supplied opaque draft key (the reference host uses the stable Portal user ID); no other record uses a human ID. Different keys cannot read/overwrite/delete each other's data; the same key sees the same profiles/history/artifacts across logins/clients. Saved snapshots survive Git changes/restarts. Replacing the access key gives a fresh scope without copying or automatically deleting the previous scope; rotation handling is manual for now. |
| REQ-428 | nfr | Reusable Phoenix UI/client package has no dependency on the reference app, internal Go types, Ecto or Portal session implementation. Host supplies a server-held HLLM API key, a draft key and admission/login routes. Model Recall provides the first consumer interface review; a separate test host proves package independence. Component state is instance-local, persistent drafts are key-scoped, and logic/decoders/styles each have one source. |
| REQ-429 | func | Preserve progress, response/JSON inspection, attempts/cache/usage/cost, trace/artifact views and downloads. Core supplies facts, wrapper aggregates once, UI formats. Recovered errors and history failures do not mark successful calls failed; unknown cost is not zero. One structured diagnostic supplies safe messages plus stable codes, operation/correlation IDs, timing, sanitized causes and separate execution/persistence outcomes. |
| REQ-430 | int | Deploy one Go process and one Phoenix app using existing shared PostgreSQL/Garage/Control Plane and Caddy (Laminar only for existing diagnostic traces). Serve UI and `/v1` at `harden-llm.prls.co`; no second API hostname is required. |
| REQ-431 | reliability | Cache-off configured inference neither opens nor waits for application storage or human identity services. Local bearer validation and key-scoped client construction perform no network I/O; constructors do not ping databases. Authorization/profile/credential failures prevent reference inference, history availability does not. Release DB resources before provider work; bounded sink failure never replays inference or switches credentials/stores. |
| REQ-432 | security | Incoming HLLM keys, deployment provider keys, database/encryption/service secrets stay in protected environment configuration. Entered provider keys are encrypted in PostgreSQL with API-key-hash/credential/origin binding. Passwords/sessions stay in Control Plane. Neither raw HLLM keys nor provider secrets appear in read DTOs, exports, history rows, traces, Laminar, Garage paths/metadata, logs or evidence. Hashes cannot authenticate or be supplied as ownership selectors. No environment-to-database key sync/fallback; retain bounded sanitized diagnostics. |
| REQ-433 | nfr | Preserve assertion purposes and canonical test identifiers; add failing deterministic coverage before changed behavior. Retain the existing runner, tier policy and browser/provider opt-ins; every changed test file carries specification and test tags. |
| REQ-434 | reliability | A complete release has a recorded source SHA, successful exact-SHA release attempt, component image identities, shared-owner configuration evidence, URL and browser-free checks. Docs/build/local tests alone never count as deployed acceptance. |
| REQ-435 | nfr | Maintain one engine, route per operation, persistence owner and component/style source. Delete superseded HLLM paths/data only; no migration/fallback/compatibility model or unrelated shared-resource reset. |

Performance constraints retain ADR-HLLM-032's current 256 KiB request, 16 MiB upstream/encoded response, 60-second inference and 64 KiB diagnostics bounds. Baseline reference features that demonstrably cannot fit require an explicit contract decision; do not silently truncate history or increase limits to pass a test. A history record keeps the 17 MiB encoded bound inherited from current reference storage. Test runtime estimates below are scheduling estimates, not production SLAs.

Errors use the existing OpenAI envelope on `/v1` and typed OpenAPI errors on reference operations. Authentication, invalid profiles/credentials and failed profile resolution prevent provider invocation; history configuration/storage errors do not. Apply the explicit history outcomes and diagnostic fields in section 2.3. A successful result remains successful when recording fails or is unconfirmed. This synchronous design does not promise crash-proof exactly-once recording. Diagnostic export is bounded and separate from history acceptance; history payloads follow their approved access policy.

```mermaid
flowchart LR
  SDK[OpenAI client] -->|HLLM bearer /v1| C[Caddy shared]
  Browser[Browser] -->|Portal session| C
  C --> Web[Phoenix reference app]
  C --> API[Go REST composition]
  Web --> UI[Reusable UI and REST client]
  UI -->|same bearer /api/v1| API
  API -->|validated key hash| W[Stateful library]
  API --> Core[Stateless execution library]
  W --> Core
  Core -->|injected provider| CPA[CPA or configured provider]
  Core -->|injected cache and events| Ports[Wrapper service implementations]
  W --> Ports
  Ports --> PG[Agent Platform PostgreSQL: state and history]
  Ports -.->|diagnostic OTEL only| L[Shared Laminar]
  Ports --> G[Shared Garage]
  Web --> CP[Control Plane and Portal]
```

```text
System: Harden-LLM
  People: enabled Portal users             External clients: OpenAI SDK/curl
  Ingress: caddy-shared, one public origin
  Containers:
    Phoenix reference app -> reusable UI/REST client -> reference REST
    Go gateway composition -> stateless core + stateful library
  Components:
    core: reliability decisions, resolved inputs, injected effects
    wrapper: profiles, cache persistence, drafts, PostgreSQL history, artifacts, diagnostics
  External systems:
    Agent Platform PostgreSQL; Garage; Control Plane; CPA/provider; Laminar (diagnostic traces only)
  Trust boundaries:
    /v1 and reference REST: configured HLLM bearer -> server-derived key hash
    browser admission: Portal -> Phoenix; identity does not select data
    stateless core: resolved inputs/effects only, no incoming credential or scope
  Data authority:
    credential-free presets: Git; deployment secrets: protected environment
    key-scoped saved snapshots/credentials/drafts/cache/preferences/history: shared PostgreSQL
    key-scoped artifact bytes: Garage path prefix artifacts/<api_key_hash>/
    human passwords/sessions: Control Plane
    diagnostic traces only: Laminar via existing OTEL path
```

### 4.1. Implementation map and package rules

The names below are the target API, not claims that these symbols already exist. Preserve existing types/functions when listed; move their implementation once instead of wrapping a second copy. P00 copies these contracts into OpenAPI and the canonical specifications. Do not restore an entire old commit over current fixes.

| Responsibility | Current or baseline source | Target and named entry points |
| --- | --- | --- |
| Execution | Current `client.go`, `types.go`, `connections.go`, `cache.go`, `internal/runtime/` | Keep `hardenllm.New(Options)`, `Client.Call`, `Client.Models`, `CacheStore` and the one `runtime.Execute`. Add public aliases `Executor`, `ProviderCall`, `ProviderConnection`, `ProviderCredential`, `PreparedOperation`, `ProviderResult` for the existing runtime types needed by an external executor; alias the prepared cache-operation types too. |
| Concrete provider and telemetry construction | Current `client.go`, `internal/providers/`, `internal/runtime/telemetry.go` | Move construction into `stateful/client.go: NewClient(ClientOptions)`. Move OTEL implementation to `internal/observability/telemetry.go`. `internal/providers/` stays one concrete implementation; do not add provider transports. |
| Profile definitions and encryption | Baseline `internal/profiles/{profiles,credentials,default_catalog}.go` and `default-profile-catalog.json` | Move reusable validation/types/encryption into `stateful/{profile,credential,presets}.go`; catalog into `stateful/presets.json`. Keep `ValidateCatalog`, `NormalizeModels`, `CredentialVault.Seal/Open`; bind to the incoming key hash and remove old user/seeding/live-inheritance assumptions. |
| Wrapper persistence | Current Phoenix `frontend/lib/harden_llm/reference.ex`; baseline storage semantics | New `stateful/store.go`, `profile_store.go`, `cache.go`, `draft.go`, `schema.sql`. Direct pgx queries in this package; no generic repository interface and no restored `internal/postgres/` package. |
| Reference execution | Baseline `internal/gateway/{run_service,runtime_profiles}.go` | New `stateful/run.go: Service.Run`, `resolve.go: resolveRun`, `project.go: projectRun`. Reuse the baseline `RunOutput` projection, not its old engine/storage/resolver. |
| History and artifacts | Baseline `llm_runs` semantics; baseline `internal/artifacts/garage.go` and `internal/gateway/artifact_coordinator.go` | New `stateful/{history,artifact}.go` and `stateful/sql/` with direct pgx queries against the same pool. Restore only the Garage object put/get/list/delete operations needed by `ArtifactStore`, addressed by `artifacts/<api_key_hash>/<run_id>/<artifact_id>`. No artifact journal, reconciler or Laminar client. |
| Reference HTTP | Current `internal/gateway/httpapi/httpapi.go`; baseline reference route contracts | New `reference.go`, `reference_access.go`, `reference_stream.go` in that package. Extend the existing `net/http.ServeMux` and `Config`; do not restore the old router framework. |
| UI | Baseline `frontend/lib/harden_llm_web/live/{workspace_live,profiles_live,embedding_live,profile_widget_component}.ex`, associated templates, forms, trace components and CSS | Restore the workspace shell first in P03. Move reusable parts once into `frontend/packages/harden_llm_ui/` in P04. The host keeps its Router, LiveViews and Portal integration. |

Core dependency rule: root/runtime/retry/accounting/cache-key decision code may depend on value types, standard-library context/time and injected interfaces. They must not import pgx, concrete providers, OTEL SDK, slog, Phoenix or application profile/history packages. Library constructors validate values but perform no network I/O. Nil cache disables cache; nil observer performs no observation. A missing executor is a construction error, not a hidden default provider.

### 4.2. Stateless call and recovery contract

- Replace concrete-effect fields in root `Options` with `Connections ConnectionCatalog`, `DefaultConnection string`, `Executor Executor`, `DiscoverModels ModelDiscoverer`, `Observer Observer`, and existing `Cache CacheStore`. Define `type ModelDiscoverer func(context.Context, Connection) ([]Model, error)`. `Client.Models` calls it; absence returns the typed unsupported-operation error. `Client.Call` never discovers models.
- `stateful.ClientOptions` contains the current concrete construction settings: connections/default connection/cache, endpoint policy, web search, tracer provider, meter provider and logger. `stateful.NewClient(ClientOptions) (*hardenllm.Client, error)` is the ready-to-use library constructor. Its private `newEffects` helper creates provider router/model discovery/observer once; `NewService` reuses that helper. Both call the same root constructor. Delete `internal/gateway.NewStaticClient` after its callers use this composition.
- Define `Observer` using the existing start/finish contracts: `StartRuntime`, `StartProvider`, `StartAttempt`, `StartCache`; expose the existing observation/target types through public aliases. `StartCall(ctx, CallObservation)` finishes with `func(CallSummary, error)`, where `CallSummary` contains only IDs, selected/generation targets, status, durations, attempt count, cache outcome and existing accounting ledgers. It must not contain output, messages, credentials or prepared operations. Replace effectful telemetry methods with `StartRetry(ctx, target, callType, classification, delay) (context.Context, func(error))` and `StartValidation(ctx, connection, repair) (context.Context, func(error))`. Core itself performs waiting and validation and calls each finish once. A nil observer must not change results, retry count, timing budgets or cancellation. Move concrete metric/span tests with the OTEL code, retaining names and bounded labels.
- Keep `Request`, `Result`, `ProgressEvent`, `CacheRecord`, `CacheStore` and their supported wire meanings. Add the optional Go-only `Request.CaptureDiagnostics func(DiagnosticCapture)` callback (`json:"-"`) for reference artifacts, as defined below. No database/user/profile identifier, incoming API key or key hash becomes necessary for a root call. Prepared provider payloads and credentials must never be exposed to telemetry merely to reuse `CallRecord`; use its safe observation projection.
- Preserve `retry.Policy` and its validation. Add `ConnectionID string` with JSON name `upstream` to `retry.RecoveryTarget`, applicable to `source:"model"`. Absent `upstream` means the request's connection. `source:"generation"` selects the actual generation target. A supplied nonexistent connection fails validation before any provider dispatch.
- Add an immutable resolved connection/credential map to runtime input, keyed by connection ID. Resolve all referenced targets before execution. `preparePlannedGeneration`, `preparePlannedRepair` and rerun planning select from that map; each planned work item carries the selected credential. Do not read profiles, query storage or reread environment during an attempt.
- The wrapper accepts the baseline `source:"profile", profileId` targets at the five existing leaf positions: `jsonRepair.initial`, `jsonRepair.escalation`, `rerun.target`, `rerun.jsonRepair.initial`, and `rerun.jsonRepair.escalation`. `resolveRun` binds them to the authenticated key scope's resolved connections and rewrites them to the core model/upstream form. Assign fixed per-call connection IDs `generation`, `repair-initial`, `repair-escalation`, `rerun`, `rerun-repair-initial`, `rerun-repair-escalation`; do not use potentially 1500-byte profile names as 128-byte upstream IDs. It does not follow the referenced profile's recovery policy recursively. Missing/deleted/unauthorized repair profiles or keys fail before the first generation. Shared history restore never bypasses this check.
- Add optional `Pricing *Pricing` to resolved `Connection`, using a public alias to the existing `internal/accounting.Pricing` value. Price each dispatched attempt with that attempt's snapshot, using `accounting.ResolveCost`; provider-reported cost wins. Nil rate is unknown and a zero rate is known zero. Sum the existing ledger once, including repair/rerun targets with different rates. Cache hits retain recorded accounting and identify cached usage without inventing a new billed invocation. Never price aggregate tokens retrospectively using the current selected profile.

The existing `internal/traces` and `internal/diagnostics` packages already provide storage-neutral trace projection, artifact projection and redaction. Preserve their assertion oracles. Define public value aliases `Trace` and `ArtifactProjection` to the existing trace types, and `DiagnosticCapture{Trace Trace; Artifacts []ArtifactProjection; ErrorCode string}`. At terminal core completion, when the request callback is non-nil, use `traces.Project` and `traces.ArtifactProjections` once to produce detached, bounded values redacted with the resolved credential values. Invoke the callback once, including parse-failure evidence when available. An authenticated-encryption key or whole prepared operation is never part of this public capture. Capture/projection failure invokes the same callback once with `ErrorCode:"diagnostic_projection_failed"` and no unredacted values, preserving inference.

The wrapper callback only assigns this per-call capture in memory; it performs no storage or logging. Wrapper `publishArtifacts` uploads the existing baseline trace/parse-failure projections through Garage after core completion with the baseline two-second persistence bound, then adds returned metadata to `RunOutput` before the history insert. Keep `internal/diagnostics.Build` and its 4 MiB support-bundle bound rather than rebuilding diagnostics in Phoenix. The OTEL observer receives only safe `CallSummary`, never this payload capture. A nil callback produces no artifact work for ordinary `/v1` calls. Do not restore the baseline PostgreSQL artifact journal, 30-second reconciler, publisher-versus-store fallback or secondary scheduler.

### 4.3. Stateful library contract

`stateful.Config` fields remain `Client ClientOptions`, `Pool *pgxpool.Pool`, `Presets Catalog`, `Vault *CredentialVault`, `History HistoryConfig{Enabled bool}`, and `Artifacts *ArtifactStore`, and `CacheTTL time.Duration` (omission selects 86400 seconds). History uses `Pool`; there is no separate history client. `NewService(Config) (*Service, error)` calls `newEffects` once and retains shared concrete effects for immutable call clients. `Service.ClientForKey(apiKeyHash string) (*hardenllm.Client, error)` binds one `PostgresCache` scope to that key and the deployment credential snapshot, then uses the one root constructor. It performs no database/identity/network lookup. `/v1` uses this composition for every authenticated key; there is no unscoped cache path or duplicated engine. Standalone `stateful.NewClient(ClientOptions)` remains the storage/auth-free convenience constructor for library consumers.

The executable owns pool/client shutdown; `Service` neither provisions a database nor creates another pool. Optional storage/history/artifact configuration errors remain typed feature status and do not prevent deployment inference. A nil pool/vault produces explicit storage/credential configuration errors on affected operations; it never substitutes environment credentials. History retains enabled/configuration status: enabled with a nil pool is a configuration error, not disabled. Inject clock and ID functions through unexported test options; no general dependency container.

Use `ProfileCredentialBinding{Source string, DeploymentUpstreamID string, EnteredCredentialID string}` with wire names `source`, `deploymentUpstreamId`, `enteredCredentialId`; source is `deployment` or `entered` and exactly its one source-specific ID is present. `ProfileRecord{APIKeyHash, ID, Name, PresetID string; Definition, Inputs json.RawMessage; Binding ProfileCredentialBinding; CreatedAt time.Time}` represents an immutable complete saved profile. `PresetID` is provenance only, never an inheritance link. `Draft{DraftKey string; Revision int64; Payload json.RawMessage}` and `SaveDraftInput{Revision int64; Payload json.RawMessage}` preserve mutable draft compare-and-swap. `ResolvedProfile` contains validated definition, saved inputs and detached connection/credential metadata; never serialize it whole.

`validateSnapshot` is the value-only validator for a complete definition plus saved inputs. Git template selection and saved-profile loading both fill the same draft shape. Missing optional input fields normalize once through existing core/provider validation; explicit false/zero/empty values keep their meaning. Remove the earlier override merge/reset protocol. Core retry and provider semantics remain the single validators; the UI only serializes chosen fields.

| Operation | Go signature and file |
| --- | --- |
| Resolve profile | `ResolveProfile(ctx context.Context, apiKeyHash, profileID string) (ResolvedProfile, error)` in `resolve.go`; saved definitions never reread a Git template; template IDs resolve only as starting choices. |
| List/get profiles | `ListProfiles(ctx context.Context, apiKeyHash string) ([]ProfileView, error)` and `GetProfile(ctx context.Context, apiKeyHash, profileID string) (ProfileDraft, error)` in `profile.go`; list projects picker fields, detail returns the full safe setup. |
| Save/delete | `SaveProfile(ctx context.Context, apiKeyHash string, input SaveProfileInput) (ProfileView, error)` and `DeleteProfile(ctx context.Context, apiKeyHash, profileID string) error` in `profile.go`; Save always creates a fresh UUID; deleting a Git template hides it only for this key. |
| Model refresh | `RefreshModels(ctx context.Context, apiKeyHash, profileID string) (ProfileDraft, error)` in `profile.go`; discover models with the current explicit credential, return changed draft, never update a saved snapshot or invoke generation. |
| Bundles | `ExportBundle(ctx context.Context, apiKeyHash string) ([]byte, error)` and `ReplaceBundle(ctx context.Context, apiKeyHash string, bundle []byte) error` in `bundle.go`; one encrypted current format, same access-key scope/encryption key only. |
| Drafts | `GetDraft(ctx context.Context, apiKeyHash, draftKey string) (Draft, error)` and `SaveDraft(ctx context.Context, apiKeyHash, draftKey string, input SaveDraftInput) (Draft, error)` in `draft.go`. `draftKey` is host-supplied opaque data; the reference host passes `workspace:<control-plane-user-id>`. |
| Run | `Run(ctx context.Context, apiKeyHash string, input RunInput) (RunOutput, RunState, error)` in `run.go`; baseline execution fields plus recording outcome. |
| History | `ListHistory(ctx context.Context, apiKeyHash string, query HistoryQuery) (HistoryPage, error)`, `GetHistory(ctx context.Context, apiKeyHash, runID string) (HistoryRecord, error)`, `DeleteHistory(ctx context.Context, apiKeyHash, runID string) error`, `ClearHistory(ctx context.Context, apiKeyHash string, query HistoryQuery) (ClearResult, error)` and `Statistics(ctx context.Context, apiKeyHash string, query HistoryQuery) (Statistics, error)` in `history.go`. |
| Artifacts | `OpenArtifact(ctx context.Context, apiKeyHash, runID, artifactID string) (ArtifactReader, error)` in `artifact.go`; validates UUID run ID and artifact ID syntax, builds the object key from the authenticated hash and streams it with stored content type/length/digest metadata. Works without a history row. No caller-supplied Garage key/URL. |

`SaveProfileInput` is `{name?, presetId?, definition, inputs, credentialBinding, credential?}`. `definition` contains the complete baseline provider/model/pricing/hardening definition; `inputs` contains the full current run-form values, excluding authentication, scope, result/run/trace IDs and UI process state. Fields are `modelId`, `systemPrompt`, `userPrompt`, `schema`, `schemaShorthand`, `callType`, `reasoningEffort`, `webSearch`, `providerOptions`, `cacheMode`, `cacheVersion`, `timeoutMs`, `recoveryPolicy`, and existing input-origin fields. `ProfileView` is `{id, name, kind:"preset"|"saved", presetId?, createdAt?, definition, credentialStatus}`; its definition supports existing picker/model/repair controls, while full inputs are fetched on selection. `ProfileDraft` is `{sourceProfileId?, presetId?, definition, inputs, credentialBinding?, credentialStatus}` with no credential value; an unconfigured template has no binding until explicitly selected. Explicit Save freezes normalized values, creates ID/time/name and inserts; supplied profile IDs/revisions/creation times are rejected. Credential omission preserves an explicitly selected binding, not an alternative key. Raw provider keys are accepted only on explicit credential save and never echoed. Snapshot save/import retains the existing explicit provider probe; it does not execute the user's prompt.

In `resolveRun`, keep the baseline `rootProfile` option translation: model/reasoning, provider-specific token parameter names, web-search capability, options and per-call choices resolve once. Resolve saved inputs, selected definition, recovery leaves and current credentials into a detached snapshot using one read-only repeatable-read transaction; release it before network I/O. A referenced repair profile must belong to the same API-key hash. History restore uses the recorded safe setup to fill the current draft, never resolves profiles by matching display names. Missing/deleted credential bindings stay unresolved until an explicit current-key choice; Restore never saves or runs automatically. Run retains its selected profile/template ID and submitted per-call inputs. If restored/provider-definition edits have no matching existing snapshot/template, require explicit Save to create that setup before Run; do not discard those edits or introduce an unsaved-provider execution route.

Preserve the baseline bounded provider probe (`"Reply with OK."`, one attempt, ten-second deadline) on explicit profile save. Probe the candidate before the short insert transaction. Failed or unknown probe leaves snapshots/credentials unchanged and is not retried. Refresh does no generation. Explicit bundle import probes each enabled candidate before atomic replacement, as baseline `ProfileService.ReplaceBundle` does. Test with local providers; no startup/deployment probes. Keep the paid-operation message in the editor's existing status/help surface.

Deleting a saved profile removes its key-scoped row without selecting another profile. Hiding a Git preset writes `hidden_presets`; it changes no Git source or other key's picker. Recreating a setup is explicit Save with a new ID; there is no automatic reseed. Credential replacement is a separate key-scoped record update with revision-based concurrency; profile inputs/settings stay immutable. All caller-supplied IDs remain data identifiers, never authority.

### 4.4. HTTP bearer authentication and composition contract

- Replace the gateway's scalar `httpapi.Config.Token` with `APIKeys []string`, resolved only from required connection-configuration `api_key_envs []string`. This array contains environment-variable names, not secret values; deployment loads the existing protected `.env`. Initially configure `["HARDEN_LLM_TOKEN"]`. Validate distinct nonempty names and resolved keys using the existing token constraints; missing/duplicate values fail without printing secrets. No implicit scalar fallback or key-registration API; preserve the existing deployed key value.
- Replace the HTTP composition's single `Config.Client` with `ClientForKey func(string) (*hardenllm.Client, error)` assigned from `Service.ClientForKey`; retain `Reference *stateful.Service`. Update handlers/tests once rather than wrapping old and new client paths. Build provider/telemetry effects once; per-key client construction is immutable local composition and never checks storage out.
- One `authorize` middleware protects `/v1` and `/api/v1`. Validate exactly one Authorization bearer using the existing parser/constant-time comparison rules. Match accepted keys, derive `api_key_hash` once from the matched exact credential, and place the hash plus its locally composed client in private request context. Every handler uses this context; reject scope/owner/user/key-hash/session selectors in request data with 400 `scope_selector_rejected`. A hash alone cannot authenticate. Missing/invalid credentials return 401 before provider, PostgreSQL, Laminar or Garage work.
- No `ResolveAccess`, Go Control Plane request, `X-PRLS-Session-Reference` requirement, account/product permission lookup, key-to-user mapping or persisted incoming-key registry exists. Portal guards the Phoenix host only. `/v1` and reference REST continue to authenticate during Control Plane outage; the frontend's normal sign-in availability is its own boundary.
- Phoenix keeps `HARDEN_LLM_TOKEN` server-side as the selected key and sends only `Authorization: Bearer ...` to gateway REST. `HardenLlmUI.Client.Context` contains `api_key`, `draft_key` and base URL/request metadata, not session material. The reference host sets `draft_key` to `workspace:<stable Control Plane user ID>`; Go treats it as an opaque draft selector inside the key scope, never as authority. Hosts can provide another accepted key through the same context. Browsers never receive gateway/provider secrets; do not add an automatic user mapping or a key selector to the restored UI.
- Library methods accept the trusted host's `apiKeyHash` as a storage scope, require its canonical 64-character lowercase hexadecimal form, and never derive authority from arbitrary browser fields. HTTP authentication is the sole public derivation boundary; the core owns neither key verification nor this scope.

### 4.5. Phoenix extraction map

Package namespace is `HardenLlmUI`; the host retains `HardenLlmWeb`. Package modules do not import host modules, `HardenLlm.Reference`, Ecto or PrlsWeb. Keep the existing baseline function/event names when moving them so assertions can follow their owner.

| Baseline responsibility | Single package owner |
| --- | --- |
| `HardenAPI`, `APIError`, `LlmDiagnosticsWire` | `lib/harden_llm_ui/{client,api_error,diagnostics_wire}.ex`; keep `list_profiles`, `save_profile`, `run`, `run_stream`, `list_history`, state/bundle/trace/artifact functions; add `get_profile` for complete saved-input loading. All decoding goes through the same OpenAPI-checked fixture set. |
| `ProfileForm`, `ProfileWidgetState`, `ProfileDefaults` | `lib/harden_llm_ui/{profile_form,profile_widget_state,profile_defaults}.ex`; form presentation/template selection only. One complete draft holds all inputs/settings; explicit save creates a new snapshot. Provider definitions/prices come from REST, not a second Elixir catalog. |
| `ProfileWidgetComponent` | `lib/harden_llm_ui/profile_widget_component.ex`; preserve `select-profile`, `profile-save`, credential, refresh and repair/rerun events. Every component instance has an explicit unique `id`. |
| Response, input, stats, pagination and history components | `lib/harden_llm_ui/{input_components,response_components,stats_components,history_components,pagination}.ex`; move the baseline `PrlsUI.Pagination` implementation once to package `HardenLlmUI.Pagination` and preserve numbered pages, total counts and markup; extract other baseline helpers without changing labels/folds/layout. Async HTTP orchestration remains in the host LiveView; component messages are typed tuples including component ID. |
| `LlmTraceProjection`, `LlmTraceComponents`, `WorkspaceSchema` | `lib/harden_llm_ui/{trace_projection,trace_components,workspace_schema}.ex`; preserve projection/helpers once. No independent UI accounting/retry engine. |
| Styles and client decisions | `assets/css/harden_llm_ui.css` and package `assets/js/` hold the moved baseline sources. Host `app.css`/`app.js` import them once; move production run-progress modules and update existing Node test imports. Keep host-only shell CSS outside the package. |

Host `WorkspaceLive` owns its async lifecycle and uses package functions/components; `ProfilesLive` and `EmbeddingLive` use the same picker/editor. Artifact/trace/bundle controllers call the package client using server context. Read Model Recall's current Phoenix forms, routes and `web/mix.exs` in P00 to check the component/API-key context against a real consumer. Record required host responsibilities without modifying Model Recall or adding a migration gate. A minimal independent test host proves reuse without importing `frontend/lib`; it is a fixture, not a second deployed app. Register one package Mix task in the existing runner before using it in `make test-fast`.

## 5. Iterative implementation and test plan

### 5.1. Execution controls and common phase gates

This plan is standards-informed, not a claim of ISO/IEEE/FAA compliance. It is not a safety-critical certification plan. Execute one subtask at a time. A documented blocker permits only work outside its dependency chain. Phases run in order and each requires the preceding phase's exit; steps within a phase follow their `Unlocks` chain. P00 has no external owner gate. Shared-owner checks (CHECK-422) gate only P05.S02 apply preparation and P06. No blocked gate is a completed gate.

- `branch_limits: 1` active implementation branch for the whole plan, `feat/reference-decoupling`, created from the current `origin/main` tip (at least `879106ff5da3b0b0d90f31e9d7e6f897e4888bbc`) in a fresh worktree. Do not branch from the primary checkout's `feat/openai-proxy-shared-reference`: it was squash-merged as PR #97 and is stale. Preserve unrelated work. This planning worktree is not authority to overwrite a newer branch.
- Branch bootstrap completed before implementation: `feat/reference-decoupling` was published at documentation-only `f54b8ae` on 2026-10-12; its local worktree is `/home/kirill/.cache/harden-llm-tmp/hllm-reference-decoupling-impl`. The v5.3 decision update was committed at `ce09df303ba9c04f489563cf68fcdec25325ee36`, followed by implementation checkpoints. Continue this branch with fast-forward commits; keep `plan/restore-decoupled-reference-app` frozen at v5.2. The approved one-time `dev` reset was completed in P00 after rechecking patch equivalence; do not repeat it. At this handoff, `origin/dev` and `origin/main` remain at `879106ff5da3b0b0d90f31e9d7e6f897e4888bbc`, and P02 is pushed at `8b02fe71b85e50d8253ee094f916efe85a587530`.
- Historical branch state before P00: `origin/dev` (`d0eb72f`) was 233 commits behind `origin/main` and held one unique commit (`d0eb72f test: allow bounded child startup in runner abort check (#90)`), whose change was already on `main` as `4278234` (same PR). P00.S01 rechecked that equivalence and completed the one reset approved by Kirill on 2026-10-11 (DEC-HLLM-V51-06); any other force-push still needs fresh approval. Push phase checkpoints to `feat/reference-decoupling`; merge to `dev` only at phase boundaries whose application changes are deployable (P02 exit onward), and to `main` only on explicit request. The preview descriptor prepared in P02 gives the gateway its per-environment database; previews run without Garage and report artifacts disabled. Actual deployment also requires the trusted control path described in the next gate.
- Preview promotion gate found during P02: the existing preview workflow loads trusted deployer/control assets from `main`, including the secret generator and Compose descriptor. A candidate source SHA alone does not activate their reviewed replacements. Before promoting `dev`, verify that the existing trusted control path uses the reviewed assets and approved connection metadata containing `api_key_envs`; preserve the current preview until that is established. This is a deployment gate, not permission to add a second deployer or promote `main` before P06. The [P02 checkpoint](evidence/harden-llm/reference-decoupling/phase-p02.md) records the source evidence.
- `reflection_passes: 2`: one contract review before implementation and one ownership/duplication review after green coverage. Additional passes require a recorded unresolved defect.
- `early_stop%: 0`: no failed required assertion may be accepted. Stop immediately for secret exposure, wrong data scope or an invalid test environment; after two identical unexplained failures diagnose before another attempt. A retry cannot erase ambiguous effects.
- Warm targeted checks have expected durations in section 7. Existing runner timeouts remain authoritative; investigate overruns rather than raising them. No browser, DOM dependency or real-provider execution is implicit.
- RED must fail an intended behavioral assertion, not compilation, missing credentials or missing fixture infrastructure. Minimal API declarations may precede RED execution; behavior may not. GREEN uses the identical test IDs/commands. Preserve old test purposes when replacing an obsolete owner.
- After focused success, application-changing phases run `make test-fast` once before their boundary checkpoint. Retain every required assertion; additional integration/race/composed gates follow the changed boundary. Documentation/probe-only work uses its focused checks.
- New paths below are proposed and created in their named step before any command targets them. Recheck identifier collisions before registration. Use `SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001` and the assigned test IDs in Go/Node/Elixir comments; frontend tests also carry their WEB identifiers.
- Phase-boundary checkpoint: record `git rev-parse HEAD`, `git status --short`, selected command results and redacted evidence under `plans/evidence/harden-llm/reference-decoupling/`; commit only reviewed phase changes and push the verified feature checkpoint. Record remote branch/SHA with `git ls-remote origin`. These are boundary actions, not implementation subtasks. Do not mark a known-RED phase green or promote it.
- Proceed: every required phase test/evaluation passes, traceability is complete, and the checkpoint is recorded. Escalate: missing product decision, external contract, unstable test or changed requirement. Stop: acceptance requires a scope change or risks other products' data. Resume after the decision/contract is recorded and the failing step passes with its original oracle.
- Reconcile canonical policy in P00 before production-code changes. Do not rewrite historical release evidence or reuse older plan phase statuses as this plan's results.

| Risk | Trigger | Mitigation and suspension boundary |
| --- | --- | --- |
| Accidental redesign | Baseline markup/style/control removed | Freeze capability manifest and source provenance in P00; reject unexplained changes. |
| History growth | History table or orphaned artifacts grow without retention in shared Agent Platform PostgreSQL | `/api/v1/status` reports the caller key's history row count and `pg_total_relation_size('history')`; P06 records both after deploy and at each later release. A table above 5 GiB is a review trigger for the deferred retention decision, not an enforced limit. Operators can delete by key prefix manually. No quota or scheduler in this release. |
| Cross-key leak | Caller-supplied hash or missing key predicate selects another scope | Derive the hash only in bearer middleware; deny spoofed scope fields and cross-key IDs at PostgreSQL profile/draft/history, stats, bundle and Garage artifact-path boundaries. Prove same-key sharing across two clients. |
| Shared draft overwrite | Two Portal users of one key autosave one draft | Host-supplied per-user draft key; TEST-428/439 prove two users keep separate drafts while sharing profiles/history. |
| Saved setup changes | Live Git inheritance, in-place profile edits or startup seeding changes a snapshot | Test Git changes, repeated Save/new UUIDs, refresh-to-draft, immutable saved inputs and explicit credential replacement in P02; keep one snapshot schema. |
| Resource duplication | New app database/telemetry container appears | P05 topology gate; provision isolated HLLM resources inside existing owners. |
| Outage couples plain inference | Constructor ping, held transaction or global readiness gate | P01/P02 outage and concurrency regressions; keep inference readiness separate. |
| False release evidence | Wrong SHA, skipped job, health-only evidence | P06 exact-attempt and image checks; state browser/provider limitations explicitly. |
| Scope creep | New service, compatibility path or generic abstraction | Stop for a recorded decision; remove superseded paths instead of supporting both. |

### 5.2. Implementer notes

Read these before starting any phase. They exist so an implementer does not have to infer intent.

- **Reading order.** Sections 2, 4 and 8 are the contract; section 7.4 lists settled decisions that must not be reopened. For each step, the numbered cases in its RED step plus the matching test definition in section 7.3 are the authoritative acceptance list. Where a step and section 8 seem to disagree, stop and report rather than choosing.
- **Names that exist today on `origin/main`.** `hardenllm.New`, `Client.Call`, `Client.Models`, `CacheStore` (`cache.go`), `runtime.Execute` (`internal/runtime/execute.go`), `auth.ValidToken` and `auth.MatchBearer` (`internal/gateway/auth/token.go`), `API.authorize` (`internal/gateway/httpapi/httpapi.go`), `serverConfig`, `connectionFile` and `validEnvironmentName` (`cmd/harden-llm-gateway/config.go`), `integrationtest.PostgresLease` and `integrationtest.GarageLease` (`internal/integrationtest/pool.go`), `HardenLlmWeb.HardenAPI`, `HardenLlmWeb.APIError`, `HardenLlm.Reference`, `PrlsUI.Pagination`, and `secrets()` in `scripts/preview-environment.mjs`.
- **Names that exist only at baseline `754690c`.** `internal/profiles/{profiles,credentials,default_catalog}.go` (including `CredentialVault.Seal/Open`), `internal/artifacts/garage.go`, `internal/gateway/{run_service,runtime_profiles,artifact_coordinator}.go` and `ProfileService.ReplaceBundle`. Read them with `git show 754690c:<path>` and move the needed code once; do not restore whole files or the old commit.
- **Everything else named in this plan is new** (for example everything under `stateful/` and `frontend/packages/harden_llm_ui/`, `ApplySchema`, `ClientForKey`, `publishArtifacts`, `deleteRunPrefix`, `previewSecrets`). Section 4.1 says the same.
- **Integration tests** need the runner-owned service pool. Run them through the existing tier runner (`make test-integration`) or with the pool environment it exports; a test that cannot get a lease fails, which is an infrastructure failure, not a valid RED. `GarageLease` returns a key namespace, so artifact tests prepend `fixture.Namespace` through an unexported `ArtifactStore` test option.
- **Uncertain history commits.** Section 8.3 is authoritative: positive readback permits `saved`; empty or failed readback keeps `unconfirmed`. TEST-451 is the cheap classification regression; TEST-442 keeps the distinct real-PostgreSQL delayed-commit case. Never insert again or add polling.
- **Production schema execution.** Section 8.2 defines the one production sequence in existing `runApply`. TEST-450 checks command order and failure behavior under Node; TEST-433 proves fresh-schema startup with real composed storage. `depends_on` alone cannot run the schema service when application startup uses `--no-deps`.
- **Toolchain.** Before any `mix` or aggregate frontend command, run `export PATH=/home/kirill/.local/elixir-1.20.2/bin:/home/kirill/.local/otp-28.4.3/bin:$PATH`.
- **Stop and report instead of doing any of these:** adding a fake store or repository interface to make a test pass; adding a compatibility path, alias or fallback; weakening, skipping or reordering a test case; raising a limit or timeout; calling a real provider; launching a browser; force-pushing (except the one approved `dev` reset in P00.S01); merging to `main`; touching production or shared-owner resources outside P06.
- **Step discipline.** One step at a time, in `Unlocks` order. RED must fail on an intended assertion; if it fails on compilation, add only the minimal declarations and rerun. GREEN uses the identical command. Commit and push only at phase boundaries (section 5.1).

### Phase P00: Contracts, history and artifact data shapes are executable

- Phase goal: Freeze baseline behavior, encode accepted product decisions and freeze the PostgreSQL history and Garage artifact-path contracts before dependent code changes.
- Scope/objectives: REQ-420, REQ-425, REQ-426, REQ-432, REQ-433, REQ-435; complete only the surfaces below.
- Impacted surfaces: `AGENTS.md`, `api/openapi.yaml`, `docs/architecture.md`, canonical test/frontend specifications, new baseline fixture and ADRs; owner repositories read-only during discovery.
- Lifecycle evidence: requirements are the linked SRS entries; design/code evidence is the reviewed diff against these surfaces; verification is the ordered commands below; validation demonstrates the phase goal; the configuration checkpoint records source SHA, dependency locks and evidence paths under the common boundary policy.
- Risks/assumptions: Key-scoped state, bearer-only REST, complete create-on-save profiles and nonblocking history are accepted. Existing Portal admission stays host-owned. History and artifacts use only repository-owned contracts; no external owner probe is required in this phase.
- Exit gates: apply section 5.1; proceed only after this phase's checks pass, escalate unresolved contracts, and stop for scope changes.
- Estimated phase metrics, not measured acceptance: Confidence 88% because the contracts are repository-owned; long-term robustness 91% because ownership and failure behavior become explicit; internal interactions 4 because this phase crosses that many application seams; external interactions 0 because owner contracts move to P05; complexity 40% because of the listed integration boundaries; feature creep 8% because scope is constrained to restored behavior; technical debt 6% because superseded paths are removed; YAGNI 5/5 (5 is strongest) because infrastructure follows demonstrated needs; MoSCoW Must because later acceptance depends on this outcome; scope non-local because of the listed ownership boundaries; architectural changes 3 because the listed ownership/runtime boundaries change.

- P00.S01 Capture missing architecture and baseline assertions
  - Action: Create the contract test and `test/fixtures/reference-workflows.json`; extract baseline capability/selector/style provenance at the pinned SHA. Record the two accepted deltas: key-scoped state (with per-Portal-user drafts) and new complete profile snapshots on Save. Before the first implementation commit, recheck and perform the approved `dev` reset per section 5.1; continue the already-created `feat/reference-decoupling` branch rather than recreating it. Review Model Recall's Phoenix host read-only as the first consumer. Assert canonical policy has one bearer boundary, no user/owner storage identity, server-only keys and truthful history outcomes before production code changes.
  - Why now: Protect the restoration target before extraction.
  - Files/surfaces: internal/testkit/reference_decoupling_contract_test.go; baseline frontend files in section 2.
  - Requirement link: REQ-420, REQ-425, REQ-432, REQ-433, REQ-435.
  - Verification link: TEST-420.
  - Verification mode: RED.
  - Command/procedure: `go test ./internal/testkit -run '^TestReferenceDecouplingContract$' -count=1`.
  - Expected result: The assertion fails because current guidance removes profiles and assigns persistence to Phoenix.
  - Evidence produced: Tagged fixture/test and exact RED output.
  - Stop/escalate condition: Baseline source unavailable or failure is only a missing-file/compiler error.
  - Unlocks: P00.S02.
- P00.S02 Record accepted access and configuration ownership in canonical contracts
  - Action: Transfer current CHECK-420 into ADR-HLLM-033; reconcile AGENTS, architecture, OpenAPI, canonical test/frontend specifications and `.env` ownership with sections 2/4/8. Specify one bearer/key-hash scope for both REST surfaces, per-user host draft keys, PostgreSQL history ownership, complete create-on-save profiles and the host-owned Portal admission/client context. Record Model Recall interface review without adding its migration. Preserve historical evidence.
  - Why now: Accepted access/catalog and history-failure rules determine profile, draft and diagnostic tests.
  - Files/surfaces: `docs/adr/ADR-HLLM-033-stateless-core-stateful-reference.md`, `AGENTS.md`, `docs/requirements-traceability.md`, `plans/from_utility-llm/harden-llm-self-hosted-test-spec.md`, `plans/from_utility-llm/phoenix-liveview-frontend-spec.md`.
  - Requirement link: REQ-420, REQ-425, REQ-432, REQ-433, REQ-435.
  - Verification link: TEST-420, CHECK-420.
  - Verification mode: GREEN.
  - Command/procedure: `go test ./internal/testkit -run '^TestReferenceDecouplingContract$' -count=1`.
  - Expected result: One authenticated API-key scope, complete saved-profile semantics, host-owned admission and distinct recording states are explicit; contract assertions pass.
  - Evidence produced: Decision record, policy diff and GREEN output.
  - Stop/escalate condition: A canonical contract still requires a user/session/owner selector, exports raw keys, or claims a real owner capability from fixtures; record and resolve the concrete mismatch.
  - Unlocks: P00.S03.
- P00.S03 Specify history and artifact contract fixtures
  - Action: Add a static test that validates the shared `test/fixtures/reference/` history, recording-outcome and artifact fixtures against OpenAPI and asserts their invariants: key-hash scope on every history row, numbered pages with total counts, the five `RecordingOutcome` statuses (no `pending`), and artifact object keys of the form `artifacts/<api_key_hash>/<run_id>/<artifact_id>` with no caller-supplied key. Add minimal schema stubs only.
  - Why now: History and artifact shapes must be frozen before P02/P03 storage code depends on them.
  - Files/surfaces: `internal/testkit/reference_history_contract_test.go`; `test/fixtures/reference/{history-outcomes,history-pages,artifact-manifest}.json`; `api/openapi.yaml` stubs.
  - Requirement link: REQ-426, REQ-432.
  - Verification link: TEST-436.
  - Verification mode: RED.
  - Command/procedure: `go test ./internal/testkit -run '^TestReferenceHistoryContract$' -count=1`.
  - Expected result: Fixture/schema invariant assertions fail because the canonical schemas are not yet defined.
  - Evidence produced: Fixtures, test and RED output.
  - Stop/escalate condition: A fixture needs a field the section 8 contract does not define.
  - Unlocks: P00.S04.
- P00.S04 Define history and artifact schemas in OpenAPI
  - Action: Define `HistoryPage`, `HistoryRecord`, `RecordingOutcome`, `Statistics`, `ClearResult`, artifact metadata and the history/artifact error codes from section 8.4 in `api/openapi.yaml`, plus the section 8.2 history table and 8.3 artifact path rules in the canonical specification.
  - Why now: P00.S03 fixed the assertions.
  - Files/surfaces: `api/openapi.yaml`, `internal/testkit/reference_history_contract_test.go`, `plans/from_utility-llm/harden-llm-self-hosted-test-spec.md`.
  - Requirement link: REQ-426, REQ-432.
  - Verification link: TEST-436.
  - Verification mode: GREEN.
  - Command/procedure: `go test ./internal/testkit -run '^TestReferenceHistoryContract$' -count=1`.
  - Expected result: All fixtures validate and invariants pass with the same command.
  - Evidence produced: Schema diff and GREEN output.
  - Stop/escalate condition: The schema requires an unresolved product decision.
  - Unlocks: P00.S05.
- P00.S05 Record the history and artifact owner decision
  - Action: Write ADR-HLLM-034: HLLM PostgreSQL is the sole history owner (baseline `llm_runs` precedent), Laminar keeps only diagnostic OTEL traces, records stay until key-scoped delete/clear, artifacts are scoped by Garage path, and orphaned artifacts from unrecorded runs need manual cleanup. Mark the Laminar sections of the superseded `ask_me` records as historical.
  - Why now: Removes the v5.0 external Laminar gate before implementation.
  - Files/surfaces: `docs/adr/ADR-HLLM-034-reference-history-owner.md`, `docs/adr/README.md`, `ask_me/shared-history-retention-and-access-decisions.md` header.
  - Requirement link: REQ-426, REQ-432.
  - Verification link: TEST-420, TEST-436, EVAL-420, CHECK-421.
  - Verification mode: MEASURE.
  - Command/procedure: `go test ./internal/testkit -run '^TestReferenceDecouplingContract$' -count=1` ; `go test ./internal/testkit -run '^TestReferenceHistoryContract$' -count=1`.
  - Expected result: EVAL-420 thresholds pass: exactly one history store declared (PostgreSQL), zero canonical documents still naming Laminar as history owner, zero artifact paths outside the key prefix.
  - Evidence produced: ADR, policy diff and test output.
  - Stop/escalate condition: A canonical document still requires Laminar history or a caller-supplied artifact key.
  - Unlocks: P00.S06.
- P00.S06 Review the frozen ownership surface
  - Action: Remove conflicting current-policy statements and duplicate fixtures if found; otherwise record “No refactor needed” because the phase adds two contract tests and their fixtures.
  - Why now: Close the decision gate before implementation.
  - Files/surfaces: internal/testkit/reference_decoupling_contract_test.go; internal/testkit/reference_history_contract_test.go; `docs/adr/README.md`; fixtures.
  - Requirement link: REQ-420, REQ-433, REQ-435.
  - Verification link: TEST-420, TEST-436.
  - Verification mode: VERIFY.
  - Command/procedure: `go test ./internal/testkit -run '^TestReferenceDecouplingContract$' -count=1` ; `go test ./internal/testkit -run '^TestReferenceHistoryContract$' -count=1`.
  - Expected result: Local P00 checks pass; P00 exits.
  - Evidence produced: Ownership review, test outputs and phase checkpoint.
  - Stop/escalate condition: An unresolved decision remains hidden in default behavior.
  - Unlocks: P00 exit.


### Phase P01: Stateless execution and ordinary OpenAI access remain independent

- Phase goal: Separate core effects without changing supported plain-client behavior.
- Scope/objectives: REQ-421, REQ-422, REQ-431, REQ-432; complete only the surfaces below.
- Impacted surfaces: `client.go`, `types.go`, `cache.go`, `internal/runtime/`, `internal/gateway/httpapi/`, `cmd/harden-llm-gateway/`, `api/openapi.yaml`.
- Lifecycle evidence: requirements are the linked SRS entries; design/code evidence is the reviewed diff against these surfaces; verification is the ordered commands below; validation demonstrates the phase goal; the configuration checkpoint records source SHA, dependency locks and evidence paths under the common boundary policy.
- Risks/assumptions: Constructor changes can change defaults; retain existing core assertions and final-only SSE.
- Exit gates: apply section 5.1; proceed only after this phase's checks pass, escalate unresolved contracts, and stop for scope changes.
- Estimated phase metrics, not measured acceptance: Confidence 86% because the contracts have repository-backed seams; long-term robustness 93% because ownership and failure behavior become explicit; internal interactions 4 because this phase crosses that many application seams; external interactions 1 because it touches that many owner/service contracts; complexity 52% because of the listed integration boundaries; feature creep 5% because scope is constrained to restored behavior; technical debt 8% because superseded paths are removed; YAGNI 5/5 (5 is strongest) because infrastructure follows demonstrated needs; MoSCoW Must because later acceptance depends on this outcome; scope local because of the listed ownership boundaries; architectural changes 2 because the listed ownership/runtime boundaries change.

- P01.S01 Add failing coverage for injected core effects
  - Action: Add an external-package executor construction case, nil-observer equivalence, cancellation/retry/cache invariants, concurrent snapshot cases and single terminal diagnostic-capture/redaction cases. Add only compile-ready declarations before RED.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: `types.go`, `client.go`, `internal/runtime/{types,execute,telemetry}.go`, `stateless_contract_test.go`.
  - Requirement link: REQ-421, REQ-424, REQ-431, REQ-432.
  - Verification link: TEST-421.
  - Verification mode: RED.
  - Command/procedure: `go test . -run '^TestStatelessContract$' -count=1`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P01.S02.
- P01.S02 Implement injected core effects
  - Action: Implement section 4.2 Options/public aliases and observer boundary. Move actual wait/schema validation into core; inject executor/model discovery; clone immutable inputs. Keep one runtime.Execute; retain the existing trace/artifact projection through the optional per-call value capture, with no persistence in core.
  - Why now: P01.S01 establishes the failing assertions and exact command.
  - Files/surfaces: `types.go`, `client.go`, `internal/runtime/{types,execute,telemetry}.go`, `stateless_contract_test.go`.
  - Requirement link: REQ-421, REQ-424, REQ-431, REQ-432.
  - Verification link: TEST-421.
  - Verification mode: GREEN.
  - Command/procedure: `go test . -run '^TestStatelessContract$' -count=1`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P01.S03.
- P01.S03 Add failing coverage for resolved recovery and pricing
  - Action: Add two-provider repair/rerun fixtures with distinct keys/rates and invalid target rejection before dispatch. Freeze exact attempt/ledger expectations.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: `connections.go`, `internal/retry/`, `internal/runtime/recovery_execute.go`, `internal/accounting/`, `recovery_snapshot_test.go`.
  - Requirement link: REQ-421, REQ-423, REQ-429, REQ-432.
  - Verification link: TEST-437.
  - Verification mode: RED.
  - Command/procedure: `go test . -run '^TestRecoverySnapshot$' -count=1`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P01.S04.
- P01.S04 Implement resolved recovery and pricing
  - Action: Add recovery target upstream selection and immutable per-call bindings; price attempts using the existing accounting resolver before aggregation. Preserve all existing policy branches and constraints.
  - Why now: P01.S03 establishes the failing assertions and exact command.
  - Files/surfaces: `connections.go`, `internal/retry/`, `internal/runtime/recovery_execute.go`, `internal/accounting/`, `recovery_snapshot_test.go`.
  - Requirement link: REQ-421, REQ-423, REQ-429, REQ-432.
  - Verification link: TEST-437.
  - Verification mode: GREEN.
  - Command/procedure: `go test . -run '^TestRecoverySnapshot$' -count=1`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P01.S05.
- P01.S05 Add failing coverage for reusable concrete construction
  - Action: Create the stateful package declarations and local provider/in-memory exporter cases without a pool or identity configuration.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: `stateful/client.go`, `internal/observability/telemetry.go`, moved telemetry tests, `stateful/client_test.go`.
  - Requirement link: REQ-421, REQ-431, REQ-432, REQ-435.
  - Verification link: TEST-445.
  - Verification mode: RED.
  - Command/procedure: `go test ./stateful -run '^TestClientComposition$' -count=1`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P01.S06.
- P01.S06 Implement reusable concrete construction
  - Action: Move concrete router/discovery/telemetry construction into NewClient/private newEffects. Retain metric/span identities and redaction. No database or service liveness precondition.
  - Why now: P01.S05 establishes the failing assertions and exact command.
  - Files/surfaces: `stateful/client.go`, `internal/observability/telemetry.go`, moved telemetry tests, `stateful/client_test.go`.
  - Requirement link: REQ-421, REQ-431, REQ-432, REQ-435.
  - Verification link: TEST-445.
  - Verification mode: GREEN.
  - Command/procedure: `go test ./stateful -run '^TestClientComposition$' -count=1`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P01.S07.
- P01.S07 Add failing coverage for ordinary OpenAI composition
  - Action: Extend existing OpenAI contract cases to invoke the new composition with unavailable application services and only the deployment bearer.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: `cmd/harden-llm-gateway/server.go`, `internal/gateway/shared_runtime.go`, `internal/gateway/httpapi/openai_contract_test.go`.
  - Requirement link: REQ-422, REQ-431, REQ-432.
  - Verification link: TEST-422.
  - Verification mode: RED.
  - Command/procedure: `go test ./internal/gateway/httpapi -run '^TestOpenAIContract' -count=1`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P01.S08.
- P01.S08 Implement ordinary OpenAI composition
  - Action: Wire the executable to stateful.NewClient initially; P02 then composes NewService once and supplies ClientForKey without constructing a second provider transport. Preserve /v1 models/chat/responses and final-only SSE. Delete NewStaticClient after its callers move; retain canonical OpenAPI request limits/errors. P01's ordinary-client gate uses the existing one-key fixture; P02 adds accepted-key isolation through TEST-425 after its RED step.
  - Why now: P01.S07 establishes the failing assertions and exact command.
  - Files/surfaces: `cmd/harden-llm-gateway/server.go`, `internal/gateway/shared_runtime.go`, `internal/gateway/httpapi/openai_contract_test.go`.
  - Requirement link: REQ-422, REQ-431, REQ-432.
  - Verification link: TEST-422.
  - Verification mode: GREEN.
  - Command/procedure: `go test ./internal/gateway/httpapi -run '^TestOpenAIContract' -count=1`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P01.S09.
- P01.S09 Remove duplicated concrete ownership
  - Action: Remove moved concrete dependency imports and redundant constructors; compare original retry/cache/accounting assertions and moved OTEL test tags.
  - Why now: All focused behavior is green before deletion.
  - Files/surfaces: root/runtime imports and `internal/observability/`; canonical test catalog.
  - Requirement link: REQ-421, REQ-422, REQ-423, REQ-424, REQ-429, REQ-431, REQ-432, REQ-435.
  - Verification link: TEST-421, TEST-437, TEST-445, TEST-422.
  - Verification mode: REFACTOR.
  - Command/procedure: `go test . -run '^TestStatelessContract$' -count=1` ; `go test . -run '^TestRecoverySnapshot$' -count=1` ; `go test ./stateful -run '^TestClientComposition$' -count=1` ; `go test ./internal/gateway/httpapi -run '^TestOpenAIContract' -count=1`.
  - Expected result: One engine/provider implementation and one telemetry owner remain; prior assertions retain their purpose.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: Any deleted test asserts behavior not covered at the new owner.
  - Unlocks: P01.S10.
- P01.S10 Measure stateless isolation and ordinary access
  - Action: Record exact provider counts, cross-call mutations and plain-client failures from fixed adversarial cases.
  - Why now: Measure the now-green effect boundaries.
  - Files/surfaces: same tests and evidence ledger.
  - Requirement link: REQ-421, REQ-422, REQ-423, REQ-424, REQ-429, REQ-431, REQ-432, REQ-435.
  - Verification link: TEST-421, TEST-437, TEST-445, TEST-422, EVAL-421.
  - Verification mode: MEASURE.
  - Command/procedure: `go test . -run '^TestStatelessContract$' -count=1` ; `go test . -run '^TestRecoverySnapshot$' -count=1` ; `go test ./stateful -run '^TestClientComposition$' -count=1` ; `go test ./internal/gateway/httpapi -run '^TestOpenAIContract' -count=1`.
  - Expected result: EVAL-421 thresholds pass; make test-fast follows once at the phase boundary.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: Any unexpected provider call, mutation or service dependency.
  - Unlocks: P01 exit.

### Phase P02: Profiles cache and drafts have one stateful owner

- Phase goal: Provide one reusable Go wrapper with key-scoped complete profile snapshots, provider credentials, cache/drafts, the history schema and bearer-only reference access.
- Scope/objectives: REQ-422, REQ-423, REQ-424, REQ-425, REQ-427, REQ-430, REQ-431, REQ-432; complete only the surfaces below.
- Impacted surfaces: New `stateful/`; direct pgx storage in `stateful/`; `api/openapi.yaml`; `internal/gateway/httpapi/`; `cmd/harden-llm-gateway/` startup settings and schema command; `deploy/preview/compose.yml`; existing integration pool.
- Lifecycle evidence: requirements are the linked SRS entries; design/code evidence is the reviewed diff against these surfaces; verification is the ordered commands below; validation demonstrates the phase goal; the configuration checkpoint records source SHA, dependency locks and evidence paths under the common boundary policy.
- Risks/assumptions: Explicit snapshots, template-copy-on-selection and API-key ownership are accepted; deployment provider keys stay environment-backed. Two synthetic accepted HLLM keys exercise one service/pool; the database lease is isolated from production.
- Exit gates: apply section 5.1; proceed only after this phase's checks pass, escalate unresolved contracts, and stop for scope changes.
- Estimated phase metrics, not measured acceptance: Confidence 82% because the contracts have repository-backed seams; long-term robustness 92% because ownership and failure behavior become explicit; internal interactions 5 because this phase crosses that many application seams; external interactions 2 because it touches that many owner/service contracts; complexity 64% because of the listed integration boundaries; feature creep 8% because scope is constrained to restored behavior; technical debt 10% because superseded paths are removed; YAGNI 4/5 (5 is strongest) because infrastructure follows demonstrated needs; MoSCoW Must because later acceptance depends on this outcome; scope non-local because of the listed ownership boundaries; architectural changes 2 because the listed ownership/runtime boundaries change.

- P02.S01 Add failing coverage for complete saved-profile snapshots
  - Action: Add these cases to `TestProfileContract` in `stateful/profile_test.go`, using value fixtures only (no database, no fake SQL repository). The step is complete when every numbered case exists and the run fails on at least one intended assertion (not on compilation or fixtures).
    1. Selecting a Git template for key A fills a draft whose definition and inputs equal the `presets.json` entry; nothing is saved or executed.
    2. Explicit `false`, `0`, `null` and empty-string inputs pass `validateSnapshot` unchanged; an absent optional field is normalized once by the existing core/provider validation.
    3. Saving a valid draft returns a new UUID, the injected UTC time and the name `<base> · <YYYY-MM-DD HH:MMZ> · <first six UUID hex characters>`; saving identical input twice gives two different IDs.
    4. A generated name over 1500 bytes returns a field error; it is never truncated.
    5. Changing or deleting the Git template after Save leaves the saved snapshot value identical.
    6. A template hidden for key A is absent from key A's picker and still present for key B.
    7. Each of the five recovery leaves with `source:"profile"` is rewritten to its fixed connection ID (`repair-initial`, `repair-escalation`, `rerun`, `rerun-repair-initial`, `rerun-repair-escalation`) in core model/upstream form; a leaf that names a key-B profile fails before any dispatch.
    8. Credential binding `deployment` requires only `deploymentUpstreamId` and `entered` requires only `enteredCredentialId`; both or neither is rejected.
    9. The picker DTO omits prompts, schema and inputs; the detail DTO contains full inputs; neither contains a credential value or the leak sentinel.
    10. A `SaveProfileInput` carrying a profile ID, revision or creation time is rejected.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: `stateful/{profile,presets,resolve}.go`, `stateful/presets.json`, `stateful/profile_test.go`.
  - Requirement link: REQ-423, REQ-427, REQ-432.
  - Verification link: TEST-423.
  - Verification mode: RED.
  - Command/procedure: `go test ./stateful -run '^TestProfileContract$' -count=1`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P02.S02.
- P02.S02 Implement complete saved-profile snapshots
  - Action: Move baseline validation and credential-free templates once; implement validateSnapshot, safe picker/detail DTOs and five-leaf recovery translation using core validation. A template fills the same draft shape that Save freezes. No live inheritance, per-key seeded catalog, content deduplication or saved-profile update path.
  - Why now: P02.S01 establishes the failing assertions and exact command.
  - Files/surfaces: `stateful/{profile,presets,resolve}.go`, `stateful/presets.json`, `stateful/profile_test.go`.
  - Requirement link: REQ-423, REQ-427, REQ-432.
  - Verification link: TEST-423.
  - Verification mode: GREEN.
  - Command/procedure: `go test ./stateful -run '^TestProfileContract$' -count=1`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P02.S03.
- P02.S03 Add failing coverage for encrypted credential binding
  - Action: Port the baseline `CredentialVault` oracle (`git show 754690c:internal/profiles/credentials_test.go`) into `TestCredentialContract` in `stateful/credential_test.go` and add these cases. The step is complete when every numbered case exists and the run fails on at least one intended assertion (not on compilation or fixtures).
    1. Seal then Open with the same key hash, credential ID and origin returns the original secret.
    2. Open fails when exactly one of key hash, credential ID, origin, key ID or one ciphertext byte differs (one subcase each).
    3. A credential sealed under key A's hash cannot be opened under key B's hash.
    4. The moved baseline associated-data encoder gives different bytes for boundary-shifted inputs such as (`ab`, `c`) and (`a`, `bc`).
    5. With the injected nonce reader the ciphertext equals the fixed expected bytes; the production constructor uses `crypto/rand`.
    6. Credential read DTOs, bundle entries and diagnostics never contain the plaintext sentinel.
    7. A missing entered credential returns `credential_invalid`; no environment key is substituted.
    8. An encryption key that does not decode from standard base64 to exactly 32 bytes is rejected at construction.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: `stateful/credential.go`, `stateful/credential_test.go`.
  - Requirement link: REQ-423, REQ-427, REQ-432.
  - Verification link: TEST-444.
  - Verification mode: RED.
  - Command/procedure: `go test ./stateful -run '^TestCredentialContract$' -count=1`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P02.S04.
- P02.S04 Implement encrypted credential binding
  - Action: Move CredentialVault encoding/Seal/Open once with one active 32-byte encryption key; use the existing unambiguous associated-data encoder with API-key hash, credential ID and origin. Exclude plaintext from reads and namespace values.
  - Why now: P02.S03 establishes the failing assertions and exact command.
  - Files/surfaces: `stateful/credential.go`, `stateful/credential_test.go`.
  - Requirement link: REQ-423, REQ-427, REQ-432.
  - Verification link: TEST-444.
  - Verification mode: GREEN.
  - Command/procedure: `go test ./stateful -run '^TestCredentialContract$' -count=1`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P02.S05.
- P02.S05 Add failing coverage for the fresh shared database schema
  - Action: Create integration-tag `TestReferenceStorage` in `stateful/storage_integration_test.go` using `internal/integrationtest.PostgresLease`, and register the `stateful` integration selection in `test/test-tiers.json` before running it. Add these cases. The step is complete when every numbered case exists and the run fails on at least one intended assertion (not on compilation or fixtures).
    1. `ApplySchema` on an empty database creates every section 8.2 table and index and records version 1.
    2. Running `ApplySchema` again is a no-op.
    3. A version table holding another version makes `ApplySchema` fail with a typed error and change nothing.
    4. A failure part-way through (pre-create a conflicting table) leaves no partial schema.
    5. Every table rejects an `api_key_hash` that is not 64 lowercase hexadecimal characters.
    6. A key-A profile cannot reference a key-B entered credential (composite foreign key).
    7. The database enforces the deployment/entered binding exclusivity check on `profiles`.
    8. Credential and draft revisions must be positive.
    9. `history` rejects `finished_at < started_at` and any status outside `succeeded`/`failed`/`timeout`; index `history_page` exists.
    10. No table named `users`, `sessions`, `incoming_keys`, `outbox` or `history_tombstones` exists.
    11. A canceled context during connection checkout returns within the 600 ms cap.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: `stateful/{schema.sql,store.go}`, `stateful/storage_integration_test.go`, `test/test-tiers.json`.
  - Requirement link: REQ-423, REQ-424, REQ-427, REQ-431, REQ-432.
  - Verification link: TEST-424.
  - Verification mode: RED.
  - Command/procedure: `go test -tags=integration ./stateful -run '^TestReferenceStorage$' -count=1`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P02.S06.
- P02.S06 Implement the fresh shared database schema
  - Action: Implement ApplySchema and the section 8.2 constraints/version checks, including the `history` table and its page index, against one injected pool. Test fresh/idempotent initialization and reject an incompatible existing version; no reset in the server.
  - Why now: P02.S05 establishes the failing assertions and exact command.
  - Files/surfaces: `stateful/{schema.sql,store.go}`, `stateful/storage_integration_test.go`, `test/test-tiers.json`.
  - Requirement link: REQ-423, REQ-424, REQ-427, REQ-431, REQ-432.
  - Verification link: TEST-424.
  - Verification mode: GREEN.
  - Command/procedure: `go test -tags=integration ./stateful -run '^TestReferenceStorage$' -count=1`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P02.S07.
- P02.S07 Add failing coverage for atomic snapshot creation
  - Action: Add these cases to integration-tag `TestProfilePersistence` in `stateful/profile_integration_test.go` (real PostgreSQL lease, local provider for probes). The step is complete when every numbered case exists and the run fails on at least one intended assertion (not on compilation or fixtures).
    1. `SaveProfile` for key A inserts one row; `ListProfiles` for key A returns it and for key B does not.
    2. `GetProfile(A, id)` returns definition and inputs equal to the normalized input; `GetProfile(B, id)` returns `profile_not_found`.
    3. Two concurrent saves of identical input create two rows with distinct UUIDs.
    4. Saving edited input creates a new row; the earlier row is byte-for-byte unchanged. No update function exists.
    5. Two Service instances on one pool, both using key A, list the same profiles.
    6. Save with a new entered credential inserts profile and credential in one transaction; an injected failure after the credential insert leaves neither row.
    7. Credential replacement with a stale revision returns `revision_conflict` and keeps the old ciphertext.
    8. Deleting a saved profile removes only that row; deleting a Git template ID writes `hidden_presets` for key A only.
    9. A key-A profile cannot bind a key-B credential.
    10. After closing and reopening the pool, snapshots and bindings are unchanged; editing `presets.json` does not change them.
    11. A failing probe leaves no row, and no connection is checked out while the probe runs.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: `stateful/{profile_store,resolve}.go`, `stateful/profile_integration_test.go`.
  - Requirement link: REQ-423, REQ-427, REQ-432.
  - Verification link: TEST-438.
  - Verification mode: RED.
  - Command/procedure: `go test -tags=integration ./stateful -run '^TestProfilePersistence$' -count=1`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P02.S08.
- P02.S08 Implement atomic snapshot creation
  - Action: Implement NewService/ClientForKey composition plus hash-scoped List/Get/Save/Delete and hidden-preset storage. Save inserts a full detached snapshot and any explicit provider credential atomically; it never overwrites a prior saved profile. Release transactions before I/O and preserve snapshots/credential bindings on restart.
  - Why now: P02.S07 establishes the failing assertions and exact command.
  - Files/surfaces: `stateful/{profile_store,resolve}.go`, `stateful/profile_integration_test.go`.
  - Requirement link: REQ-423, REQ-427, REQ-432.
  - Verification link: TEST-438.
  - Verification mode: GREEN.
  - Command/procedure: `go test -tags=integration ./stateful -run '^TestProfilePersistence$' -count=1`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P02.S09.
- P02.S09 Add failing coverage for cache and draft persistence
  - Action: Add these cases to integration-tag `TestCacheDraftContract` in `stateful/cache_draft_integration_test.go` (real PostgreSQL lease, fixed clock). The step is complete when every numbered case exists and the run fails on at least one intended assertion (not on compilation or fixtures).
    1. Cache Set then Get for key A returns the envelope; the same operation for key B misses.
    2. Two clients of key A share a cache hit.
    3. Replacing the entered provider credential changes the namespace, so the earlier entry is not served.
    4. Changing a recovery-target credential also changes the namespace.
    5. An entry past its TTL is not served.
    6. One cache write deletes at most 100 expired rows of the same key and keeps live rows and other keys' expired rows.
    7. A corrupt cache envelope fails core validation and is not returned as a hit.
    8. `GetDraft` for a missing or expired draft returns revision 0 and an empty payload.
    9. `SaveDraft` with revision 0 creates revision 1; a stale revision returns `revision_conflict` and keeps the stored payload.
    10. Draft keys `workspace:user-1` and `workspace:user-2` under key A stay independent; the same draft key under key B is separate.
    11. A payload over 256 KiB or a draft key over 128 bytes is rejected.
    12. With pool size one and a blocked provider, a cache-on call holds no connection during the provider wait (another storage call succeeds meanwhile).
    13. A cache-off call acquires zero connections.
    14. A storage outage on `GetDraft` returns `storage_unavailable`, never revision 0.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: `stateful/{cache,draft}.go`, `stateful/cache_draft_integration_test.go`.
  - Requirement link: REQ-424, REQ-427, REQ-431.
  - Verification link: TEST-439.
  - Verification mode: RED.
  - Command/procedure: `go test -tags=integration ./stateful -run '^TestCacheDraftContract$' -count=1`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P02.S10.
- P02.S10 Implement cache and draft persistence
  - Action: Implement existing CacheStore through one scoped PostgresCache and mutable revisioned drafts. Bind every operation to the authenticated hash (and drafts to the host draft key), resolve credential identity once, clean at most 100 expired cache/draft rows on their owning writes, and hold no connection during provider calls. Phoenix keeps its current Ecto drafts/history until P03 exposes the REST routes; no Phoenix call site changes in P02.
  - Why now: P02.S09 establishes the failing assertions and exact command.
  - Files/surfaces: `stateful/{cache,draft}.go`, `stateful/cache_draft_integration_test.go`.
  - Requirement link: REQ-424, REQ-427, REQ-431.
  - Verification link: TEST-439.
  - Verification mode: GREEN.
  - Command/procedure: `go test -tags=integration ./stateful -run '^TestCacheDraftContract$' -count=1`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P02.S11.
- P02.S11 Add failing coverage for profile probes refresh and bundles
  - Action: Add these cases to integration-tag `TestProfileResources` in `stateful/profile_resources_integration_test.go` (real PostgreSQL lease, local provider). The step is complete when every numbered case exists and the run fails on at least one intended assertion (not on compilation or fixtures).
    1. Save probes the candidate exactly once: one attempt, ten-second deadline, prompt `"Reply with OK."`.
    2. A failed or timed-out probe saves nothing and is not retried.
    3. `RefreshModels` returns a draft with the discovered models, sends no generation request and leaves the saved snapshot unchanged.
    4. `ExportBundle(A)` then `ReplaceBundle(A)` roundtrips snapshots, preset provenance, hidden presets and encrypted credentials.
    5. `ReplaceBundle` rejects a bundle with a different `apiKeyHash`, a different encryption key or a version other than 4, leaving rows unchanged.
    6. A failing candidate probe during import leaves the catalog unchanged; probes finish before the transaction starts.
    7. A failure inside the replacement transaction restores the previous catalog.
    8. The exported bundle contains no plaintext sentinel, HLLM key or deployment provider key.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: `stateful/{profile,bundle}.go`, `stateful/profile_resources_integration_test.go`.
  - Requirement link: REQ-420, REQ-423, REQ-427, REQ-432.
  - Verification link: TEST-440.
  - Verification mode: RED.
  - Command/procedure: `go test -tags=integration ./stateful -run '^TestProfileResources$' -count=1`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P02.S12.
- P02.S12 Implement profile probes refresh and bundles
  - Action: Implement probes using the existing core/provider transport, discovery returning a changed draft and atomic version-4 bundle replacement for one hash scope. No generation on refresh, no profile update, no legacy conversion and no deployment key export; imports use the baseline bounded candidate probes before their transaction.
  - Why now: P02.S11 establishes the failing assertions and exact command.
  - Files/surfaces: `stateful/{profile,bundle}.go`, `stateful/profile_resources_integration_test.go`.
  - Requirement link: REQ-420, REQ-423, REQ-427, REQ-432.
  - Verification link: TEST-440.
  - Verification mode: GREEN.
  - Command/procedure: `go test -tags=integration ./stateful -run '^TestProfileResources$' -count=1`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P02.S13.
- P02.S13 Add failing coverage for API-key authentication and isolation
  - Action: Add these storage-free cases to unit `TestReferenceAccess` in `internal/gateway/httpapi/reference_access_test.go`. Use `httptest` with two accepted keys from `access-keys.json` and a recording handler that captures the request context; no database and no fake store. Cross-key storage behavior over HTTP belongs to TEST-441. The step is complete when every numbered case exists and the run fails on at least one intended assertion (not on compilation or fixtures).
    1. No Authorization header on `/v1/models` and `/api/v1/status` returns 401 with the existing `invalid_api_key` envelope; the handler is not reached.
    2. An unknown bearer returns 401 `invalid_api_key`.
    3. Two Authorization headers return 401.
    4. A bearer equal to key A's hash instead of the key returns 401.
    5. Key A reaches the handler with a context hash equal to the fixed expected hash in `access-keys.json`; key B gets key B's hash.
    6. `apiKeyHash`, `owner`, `userId` or `sessionReference` in the query or JSON body returns 400 `scope_selector_rejected`; the handler is not reached.
    7. Zero requests reach the Control Plane counter, including when the Control Plane stub is down.
    8. `ClientForKey` is called once per request with the derived hash and performs no storage call.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: `internal/gateway/httpapi/reference_access.go`, `reference_access_test.go`, `api/openapi.yaml`.
  - Requirement link: REQ-425, REQ-427, REQ-432.
  - Verification link: TEST-425.
  - Verification mode: RED.
  - Command/procedure: `go test ./internal/gateway/httpapi -run '^TestReferenceAccess' -count=1`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P02.S14.
- P02.S14 Implement API-key authentication and isolation
  - Action: Replace `httpapi.Config.Token` with `APIKeys []string` (tests inject the slice; reading `api_key_envs` from the connection file is P02.S16); use existing bearer parser/constant-time validation, derive SHA-256 after matching and attach hash/client to private request context. Bind every REST handler to that scope. Remove the proposed ResolveAccess/session boundary; use no scope supplied by HTTP callers.
  - Why now: P02.S13 establishes the failing assertions and exact command.
  - Files/surfaces: `internal/gateway/httpapi/reference_access.go`, `reference_access_test.go`, `api/openapi.yaml`.
  - Requirement link: REQ-425, REQ-427, REQ-432.
  - Verification link: TEST-425.
  - Verification mode: GREEN.
  - Command/procedure: `go test ./internal/gateway/httpapi -run '^TestReferenceAccess' -count=1`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P02.S15.
- P02.S15 Add failing coverage for typed reference startup settings
  - Action: Add these cases to unit `TestReferenceConfig` in `cmd/harden-llm-gateway/reference_config_test.go` (synthetic environment maps only), and one case to `scripts/test/preview_policy_test.mjs`. The step is complete when every numbered case exists and the run fails on at least one intended assertion (not on compilation or fixtures).
    1. `api_key_envs: ["HARDEN_LLM_TOKEN"]` with that variable set yields `serverConfig.apiKeys` holding one value.
    2. A missing `api_key_envs` array is a typed startup error; there is no scalar-token fallback.
    3. Empty name, invalid name (per `validEnvironmentName`), duplicate name, unset variable, empty value, value failing `auth.ValidToken`, and two names resolving to the same value are each a typed error whose text names the variable and never prints its value.
    4. An unknown JSON field carrying a literal key (for example `api_key`) is rejected.
    5. No database URL and `HARDEN_LLM_HISTORY_ENABLED=false` reports storage and history disabled without an error.
    6. `HARDEN_LLM_HISTORY_ENABLED=true` with a missing or invalid `HARDEN_LLM_DATABASE_URL` reports history `configuration_error` while inference configuration stays valid.
    7. `HARDEN_LLM_HISTORY_ENABLED` other than `true`/`false` is a startup error.
    8. `HARDEN_LLM_DATABASE_POOL_SIZE` defaults to 10; a non-positive value is an error.
    9. `HARDEN_LLM_CREDENTIAL_KEY` absent reports entered credentials as a configuration error; a value that is not standard base64 of 32 bytes is an error; the value is never printed.
    10. `HARDEN_LLM_CACHE_TTL_SECONDS` defaults to 86400; zero, negative or non-integer values are errors.
    11. All `HARDEN_LLM_ARTIFACT_*` absent means artifacts disabled; some present and some absent is a typed configuration error.
    12. Building the config opens no database connection (an unreachable host still builds), generates no key and makes no Control Plane request; Control Plane variables are ignored by Go.
    13. The `schema` subcommand calls the injected `ApplySchema` once and exits 0; without a database URL it exits nonzero; it has no reset or drop option.
    14. `deploy/preview/compose.yml` (parsed with `gopkg.in/yaml.v3`): `gateway` receives `HARDEN_LLM_DATABASE_URL`, `HARDEN_LLM_HISTORY_ENABLED=true` and `HARDEN_LLM_CREDENTIAL_KEY`; a `gateway-schema` service runs the gateway image with `command: ["schema"]`; `gateway` depends on it with `condition: service_completed_successfully`; no service sets `HARDEN_LLM_ARTIFACT_*`.
    15. Node: exported `previewSecrets()` in `scripts/preview-environment.mjs` returns a `HARDEN_LLM_CREDENTIAL_KEY` that decodes from standard base64 to 32 bytes, and the existing merge keeps a stored value.
  - Why now: P02 exit is the first `dev` merge; the gateway must own its storage configuration before P03 moves Phoenix drafts/history to REST.
  - Files/surfaces: `cmd/harden-llm-gateway/{config,server}.go`, new schema subcommand, `.env.example`, `deploy/preview/compose.yml`, `scripts/preview-environment.mjs`, `scripts/test/preview_policy_test.mjs`, `reference_config_test.go`.
  - Requirement link: REQ-424, REQ-430, REQ-431, REQ-432.
  - Verification link: TEST-449.
  - Verification mode: RED.
  - Command/procedure: `go test ./cmd/harden-llm-gateway -run '^TestReferenceConfig$' -count=1` ; `node --test scripts/test/preview_policy_test.mjs`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P02.S16.
- P02.S16 Implement typed reference startup settings
  - Action: Resolve gateway accepted keys once from api_key_envs metadata and protected environment; the frontend uses the same HARDEN_LLM_TOKEN value. Implement typed feature status/schema command, preserve that existing key without printing/rotating it, give the gateway the existing `HARDEN_LLM_DATABASE_URL`/`HARDEN_LLM_DATABASE_POOL_SIZE` names and construct one pool without pinging it. In `deploy/preview/compose.yml`, pass the per-environment preview database URL, `HARDEN_LLM_HISTORY_ENABLED=true` and `HARDEN_LLM_CREDENTIAL_KEY` to `gateway`. The gateway image is `FROM scratch` (no shell), so add a one-shot `gateway-schema` service using the same image with `command: ["schema"]` and make `gateway` depend on it with `condition: service_completed_successfully`. In `scripts/preview-environment.mjs`, rename `secrets()` to exported `previewSecrets()` and add `HARDEN_LLM_CREDENTIAL_KEY: randomBytes(32).toString("base64")`; the existing merge keeps stored values, so current previews gain a key on next deploy without changing others; Phoenix keeps its own database variable on the same preview database (no table names overlap) until P03.S13. Leave `HARDEN_LLM_ARTIFACT_*` unset in every preview, including `dev`, so artifacts report disabled.
  - Why now: P02.S15 establishes the failing assertions and exact command.
  - Files/surfaces: `cmd/harden-llm-gateway/{config,server}.go`, new schema subcommand, `.env.example`, `deploy/preview/compose.yml`, `scripts/preview-environment.mjs`, `scripts/test/preview_policy_test.mjs`, `reference_config_test.go`.
  - Requirement link: REQ-424, REQ-430, REQ-431, REQ-432.
  - Verification link: TEST-449.
  - Verification mode: GREEN.
  - Command/procedure: `go test ./cmd/harden-llm-gateway -run '^TestReferenceConfig$' -count=1` ; `node --test scripts/test/preview_policy_test.mjs`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P02.S17.
- P02.S17 Consolidate stateful storage ownership
  - Action: Remove duplicate stores, live preset resolvers, saved-profile update paths and user/session-dependent Go REST ownership. Keep one snapshot validator, AES-GCM implementation and pgx pool; Phoenix drafts/history stay until P03.S13.
  - Why now: Each persistence boundary has its focused tests.
  - Files/surfaces: `stateful/` and integration runner.
  - Requirement link: REQ-420, REQ-423, REQ-424, REQ-425, REQ-427, REQ-431, REQ-432.
  - Verification link: TEST-423, TEST-424, TEST-438, TEST-439, TEST-440, TEST-444, TEST-425, TEST-449.
  - Verification mode: REFACTOR.
  - Command/procedure: `go test ./stateful -run '^TestProfileContract$' -count=1` ; `go test -tags=integration ./stateful -run '^TestReferenceStorage$' -count=1` ; `go test -tags=integration ./stateful -run '^TestProfilePersistence$' -count=1` ; `go test -tags=integration ./stateful -run '^TestCacheDraftContract$' -count=1` ; `go test -tags=integration ./stateful -run '^TestProfileResources$' -count=1` ; `go test ./stateful -run '^TestCredentialContract$' -count=1` ; `go test ./internal/gateway/httpapi -run '^TestReferenceAccess' -count=1` ; `go test ./cmd/harden-llm-gateway -run '^TestReferenceConfig$' -count=1`.
  - Expected result: One key-scoped implementation per storage responsibility; no live inheritance, seed catalog or premature history removal.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: Deletion would remove an assertion or still-used history path.
  - Unlocks: P02.S18.
- P02.S18 Measure API-key state and pool isolation
  - Action: Run focused persistence/access suites; collect cross-key violations, same-key visibility, overwritten snapshots, credential substitution, stale cache use and connections held during provider waits.
  - Why now: Green transactions can now be measured under concurrency.
  - Files/surfaces: real isolated integration leases and evidence ledger.
  - Requirement link: REQ-420, REQ-422, REQ-423, REQ-424, REQ-425, REQ-427, REQ-431, REQ-432.
  - Verification link: TEST-438, TEST-439, TEST-440, TEST-425, TEST-422, EVAL-422.
  - Verification mode: MEASURE.
  - Command/procedure: `go test -tags=integration ./stateful -run '^TestProfilePersistence$' -count=1` ; `go test -tags=integration ./stateful -run '^TestCacheDraftContract$' -count=1` ; `go test -tags=integration ./stateful -run '^TestProfileResources$' -count=1` ; `go test ./internal/gateway/httpapi -run '^TestReferenceAccess' -count=1` ; `go test ./internal/gateway/httpapi -run '^TestOpenAIContract' -count=1`.
  - Expected result: EVAL-422 thresholds pass; existing aggregate integration gate remains required at the phase boundary.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: Any isolation leak, stale cache acceptance or held connection.
  - Unlocks: P02 exit.

### Phase P03: One restored workspace workflow records key-scoped PostgreSQL history

- Phase goal: Deliver profile selection through repaired response and key-scoped complete history restore in the baseline workspace.
- Scope/objectives: REQ-420, REQ-426, REQ-427, REQ-429, REQ-431, REQ-432; complete only the surfaces below.
- Impacted surfaces: New wrapper history implementation in `stateful/`; Garage artifact paths; `/api/v1/run`; `frontend/lib/harden_llm_web/live/workspace_live.ex`; baseline template; OpenAPI fixtures.
- Lifecycle evidence: requirements are the linked SRS entries; design/code evidence is the reviewed diff against these surfaces; verification is the ordered commands below; validation demonstrates the phase goal; the configuration checkpoint records source SHA, dependency locks and evidence paths under the common boundary policy.
- Risks/assumptions: History uses the P02 schema and pool; no external owner gate. Garage is exercised through the existing integration lease.
- Exit gates: apply section 5.1; proceed only after this phase's checks pass, escalate unresolved contracts, and stop for scope changes.
- Estimated phase metrics, not measured acceptance: Confidence 85% because history reuses repository-owned storage; long-term robustness 91% because ownership and failure behavior become explicit; internal interactions 5 because this phase crosses that many application seams; external interactions 1 because it touches that many owner/service contracts; complexity 70% because of the listed integration boundaries; feature creep 7% because scope is constrained to restored behavior; technical debt 10% because superseded paths are removed; YAGNI 4/5 (5 is strongest) because infrastructure follows demonstrated needs; MoSCoW Must because later acceptance depends on this outcome; scope non-local because of the listed ownership boundaries; architectural changes 2 because the listed ownership/runtime boundaries change.

- P03.S01 Add failing coverage for the PostgreSQL history store
  - Action: First add storage-free TEST-451 cases in `stateful/history_test.go`, then add these cases to integration-tag `TestHistoryStore` in `stateful/history_store_integration_test.go` (real PostgreSQL lease, injected clock and commit hook). The step is complete when every numbered case exists and both commands fail on an intended assertion (not on compilation or fixtures).
    1. `insertHistory` for key A stores one row whose `api_key_hash` is the server-supplied hash; the record type has no caller-settable hash.
    2. `GetHistory(A, run)` returns a record equal to the inserted one; `GetHistory(B, run)` returns `history_not_found`.
    3. An unknown run UUID returns `history_not_found`; a stored row with an invalid schema version or payload returns `history_record_invalid`.
    4. Page 1 (size 25) fixes `Before` to the injected clock; page 2 with the same `Before` excludes a row inserted after page 1 was read.
    5. Rows with equal `started_at` order by `run_id` descending, stable across pages.
    6. `totalCount` equals key A's rows in the window; key B rows are never counted.
    7. The optional `From` bound filters list and count identically.
    8. Page size outside 10/25/50/100, page below 1, or an overflowing offset is a validation error.
    9. List items contain only summary fields, never `request` or `result`.
    10. A record whose encoded `request`+`result` is exactly 17 MiB is saved and reloaded; 17 MiB + 1 byte is `not_saved` with no row and no truncation. Record both durations; each must be under its 2 s budget.
    11. The commit hook loses the acknowledgement after COMMIT was sent: a committed row gives one readback and `saved`; an absent row or failed readback gives `unconfirmed`. Definite pre-COMMIT rejection gives `not_saved` with no readback. Each case makes exactly one insert attempt.
    12. A failed list query returns `history_read_failed`, never an empty page.
    13. No error or diagnostic contains the DSN password sentinel.
    14. The status usage query returns key A's row count (unaffected by key B rows) and a positive `pg_total_relation_size('history')`.
    15. With the real PostgreSQL commit delayed behind the single readback, the empty read returns `unconfirmed`; releasing the test-owned barrier allows the row to appear afterward. Use the per-call commit fault hook, channels and bounded waits, not arbitrary sleeps or a fake store. Assert one insert, one readback and no inference replay.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: `stateful/history.go`, `stateful/sql/`, `stateful/history_test.go`, `stateful/history_store_integration_test.go`.
  - Requirement link: REQ-426, REQ-427, REQ-429, REQ-431, REQ-432.
  - Verification link: TEST-442, TEST-451.
  - Verification mode: RED.
  - Command/procedure: `go test ./stateful -run '^TestHistoryRecordingOutcome$' -count=1` ; `go test -tags=integration ./stateful -run '^TestHistoryStore$' -count=1`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P03.S02.
- P03.S02 Implement the PostgreSQL history store
  - Action: Implement `insertHistory`, `GetHistory`, `ListHistory` with one server-owned key-hash/window filter shared by list/count/stats/clear, and the bounded write (two-second insert, one readback on unknown commit outcome within one second), with `GetHistory` under its two-second detail budget; list/count keep the 500 ms statement cap. Keep recording-status classification in one unexported `recordingStatus` function in `history.go`, used by the real insert path and TEST-451: confirmed success or positive readback is `saved`, definite rejection is `not_saved`, and unknown commit with absent/failed readback is `unconfirmed`. Direct pgx queries; no repository interface, cursor adapter, polling or retry of the insert.
  - Why now: P03.S01 establishes the failing assertions and exact command.
  - Files/surfaces: `stateful/history.go`, `stateful/sql/`, `stateful/history_test.go`, `stateful/history_store_integration_test.go`.
  - Requirement link: REQ-426, REQ-427, REQ-429, REQ-431, REQ-432.
  - Verification link: TEST-442, TEST-451.
  - Verification mode: GREEN.
  - Command/procedure: `go test ./stateful -run '^TestHistoryRecordingOutcome$' -count=1` ; `go test -tags=integration ./stateful -run '^TestHistoryStore$' -count=1`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P03.S03.
- P03.S03 Add failing coverage for authenticated artifacts and cleanup
  - Action: Add these cases to integration-tag `TestReferenceArtifacts` in `stateful/artifact_integration_test.go` using `internal/integrationtest.GarageLease`. The lease gives a key namespace, so `ArtifactStore` takes an unexported test option that prepends `fixture.Namespace`; production keys start at `artifacts/`. Inject object-delete failures through an unexported per-call hook, not a fake store. The step is complete when every numbered case exists and the run fails on at least one intended assertion (not on compilation or fixtures).
    1. `publishArtifacts` for key A, run R writes `trace.json`, the parse-failure evidence and the support bundle under `artifacts/<hashA>/<R>/`, with content type, length and SHA-256 in object metadata.
    2. `OpenArtifact(A, R, id)` streams the exact bytes and media type; two key-A clients both succeed.
    3. `OpenArtifact(B, R, id)` returns `artifact_not_found` because the object key is built from key B's hash.
    4. An invalid run UUID or artifact ID such as `../x` is rejected before any Garage call.
    5. A stored digest mismatch returns an error and no bytes.
    6. Objects stay downloadable when history was disabled or the insert was not saved.
    7. An unreachable Garage endpoint during publication returns a diagnostic within the two-second bound and leaves the run output intact.
    8. `deleteRunPrefix(A, R)` removes every object under the prefix, counts a missing object as removed, and leaves other runs and key B untouched.
    9. An injected failure on one object delete returns `artifact_cleanup_failed` with the run ID; repeating the call completes.
    10. With all artifact settings absent: publication is skipped without a diagnostic, `OpenArtifact` returns `artifact_not_found`, and status reports artifacts disabled.
    11. No exported function accepts a Garage key or URL from a caller.
  - Why now: History delete/clear (P03.S05–S06) depend on the prefix delete, so the artifact boundary is tested first.
  - Files/surfaces: `stateful/artifact.go`, baseline Garage methods, `stateful/artifact_integration_test.go`.
  - Requirement link: REQ-426, REQ-429, REQ-432.
  - Verification link: TEST-443.
  - Verification mode: RED.
  - Command/procedure: `go test -tags=integration ./stateful -run '^TestReferenceArtifacts$' -count=1`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P03.S04.
- P03.S04 Implement authenticated artifacts and cleanup
  - Action: Implement publishArtifacts (put under `artifacts/<api_key_hash>/<run_id>/`, metadata in Garage object headers) plus OpenArtifact streaming through the authenticated wrapper and the prefix delete used by P03.S06 delete/clear. The inference terminal SSE event follows bounded artifact publication and precedes the history insert, which P03.S08 wires; publication failure preserves output with a diagnostic. No redirect to the retired artifact host, journal, reconciler or second metadata store.
  - Why now: P03.S03 establishes the failing assertions and exact command.
  - Files/surfaces: `stateful/artifact.go`, baseline Garage methods, `stateful/artifact_integration_test.go`.
  - Requirement link: REQ-426, REQ-429, REQ-432.
  - Verification link: TEST-443.
  - Verification mode: GREEN.
  - Command/procedure: `go test -tags=integration ./stateful -run '^TestReferenceArtifacts$' -count=1`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P03.S05.
- P03.S05 Add failing coverage for history statistics, delete and clear
  - Action: Add these cases to integration-tag `TestHistoryQueries` in `stateful/history_queries_integration_test.go` (real PostgreSQL and Garage leases; reuse the P03.S03 delete-failure hook). The step is complete when every numbered case exists and the run fails on at least one intended assertion (not on compilation or fixtures).
    1. `Statistics(A)` over the list window sums known costs, counts unknown-cost observations and cache-served results, and returns `scope:"available_records"`.
    2. A key-B row never changes key A's statistics.
    3. A missing cost makes the total partial/unknown, never zero.
    4. `DeleteHistory(A, R)` removes R's Garage prefix, then its row; `GetHistory(A, R)` then returns `history_not_found`.
    5. `DeleteHistory(B, R)` for key A's run returns `history_not_found`; row and objects remain.
    6. A missing object counts as removed.
    7. An injected object-delete failure keeps the row and returns `artifact_cleanup_failed` with the run ID; a retry succeeds.
    8. `ClearHistory(A)` with 150 key-A rows before the frozen cutoff removes them in batches of at most 100, keeps key B rows and a key-A row started after the cutoff, and returns `{deleted:150, cutoff, complete:true}`.
    9. `ClearHistory` stops at the first failing run and returns `complete:false` with that run ID; later rows remain.
    10. With artifacts disabled, delete and clear remove rows only.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: `stateful/{history,artifact}.go`, `stateful/history_queries_integration_test.go`.
  - Requirement link: REQ-426, REQ-427, REQ-429, REQ-431, REQ-432.
  - Verification link: TEST-427.
  - Verification mode: RED.
  - Command/procedure: `go test -tags=integration ./stateful -run '^TestHistoryQueries$' -count=1`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P03.S06.
- P03.S06 Implement history statistics, delete and clear
  - Action: Implement `Statistics`, `DeleteHistory` and `ClearHistory` (batches of 100 run IDs before the frozen cutoff; per run delete the Garage prefix with the P03.S04 prefix delete, then the row; when artifacts are disabled, delete only the row). Return deleted count, `complete` and the first failed run ID; never touch another key or a run after the cutoff.
  - Why now: P03.S05 establishes the failing assertions and exact command.
  - Files/surfaces: `stateful/{history,artifact}.go`, `stateful/history_queries_integration_test.go`.
  - Requirement link: REQ-426, REQ-427, REQ-429, REQ-431, REQ-432.
  - Verification link: TEST-427.
  - Verification mode: GREEN.
  - Command/procedure: `go test -tags=integration ./stateful -run '^TestHistoryQueries$' -count=1`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P03.S07.
- P03.S07 Add failing coverage for reference execution and recording outcomes
  - Action: Add these cases to integration-tag `TestReferenceHistory` in `stateful/history_integration_test.go` (real PostgreSQL lease, local provider with a call counter, closed-pool and commit-hook faults). The step is complete when every numbered case exists and the run fails on at least one intended assertion (not on compilation or fixtures).
    1. Healthy run, history enabled: one provider call, one key-A row, outcome `saved`.
    2. A plain `/v1`-style call through `ClientForKey` writes no row.
    3. History disabled: no pool acquisition for history, outcome `disabled`, result returned.
    4. History enabled with a nil pool: outcome `configuration_error`, inference succeeds.
    5. Closed pool: outcome `not_saved` with cause `connection`, output preserved.
    6. Oversize record: `not_saved`, output preserved.
    7. Commit outcome dropped after COMMIT: one readback, row found, `saved`.
    8. Readback is empty or fails: `unconfirmed`; a delayed commit may still appear. Definite pre-COMMIT rejection remains `not_saved`.
    9. Across all cases: exactly one insert attempt, at most one readback, and the provider call count never exceeds one.
    10. Cache hit: zero provider calls, one row whose summary marks it cache-served.
    11. Request validation error before dispatch: zero provider calls, one `failed` row.
    12. Invalid profile or missing credential: zero provider calls and one `failed` row (section 8.3 records validated application errors).
    13. Caller cancels during the provider call: the terminal row is still inserted through the detached context.
    14. Key B cannot read, delete or count key A's records.
    15. Driver errors containing secret sentinels produce diagnostics without them.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: `stateful/{run,resolve,project,history}.go`, `stateful/history_integration_test.go`.
  - Requirement link: REQ-426, REQ-429, REQ-431, REQ-432.
  - Verification link: TEST-426.
  - Verification mode: RED.
  - Command/procedure: `go test -tags=integration ./stateful -run '^TestReferenceHistory$' -count=1`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P03.S08.
- P03.S08 Implement reference execution and recording outcomes
  - Action: Implement resolveRun → one core Call with per-call diagnostic capture → projectRun → bounded artifact publication → bounded history insert. Preserve baseline RunOutput/progress/core accounting; return inference and persistence outcomes separately. Never replay work.
  - Why now: P03.S07 establishes the failing assertions and exact command.
  - Files/surfaces: `stateful/{run,resolve,project,history}.go`, `stateful/history_integration_test.go`.
  - Requirement link: REQ-426, REQ-429, REQ-431, REQ-432.
  - Verification link: TEST-426.
  - Verification mode: GREEN.
  - Command/procedure: `go test -tags=integration ./stateful -run '^TestReferenceHistory$' -count=1`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P03.S09.
- P03.S09 Add failing coverage for reference routes and progress
  - Action: Add these cases to integration-tag `TestReferenceContract` in `internal/gateway/httpapi/reference_contract_test.go` (real PostgreSQL and Garage leases, local provider, shared OpenAPI fixtures). Register the `internal/gateway/httpapi` integration selection in `test/test-tiers.json` first. The step is complete when every numbered case exists and the run fails on at least one intended assertion (not on compilation or fixtures).
    1. Every section 8.4 route returns bodies that validate against OpenAPI for success and each documented error.
    2. `POST /api/v1/profiles` returns 201 with server UUID, name and time; `GET /api/v1/profiles/{id}` returns the full setup.
    3. A forged scope field returns 400 `scope_selector_rejected` and dispatches zero providers.
    4. A missing bearer on every `/api/v1` route returns 401 and dispatches zero providers.
    5. Key B gets 404 for key A's profile, history entry and artifact; two key-A clients see the same profiles and history; drafts are shared only under the same `draftKey`.
    6. SSE `/api/v1/run` emits progress, exactly one `run.completed` or `run.failed`, then `history.recorded`, then closes; every event has `runId` and a monotonic sequence.
    7. The JSON run response returns result and recording outcome in one envelope.
    8. `GET /api/v1/status` includes `history:{rows,tableBytes}` when history is enabled, omits it when disabled, and reports the figures unavailable (not zero) with a closed pool.
    9. `/api/v1/profiles/bundle` is not captured by the `{profileID}` route.
    10. Existing `/v1` contract tests still pass unchanged.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: `internal/gateway/httpapi/{reference,reference_stream}.go`, `reference_contract_test.go`, `api/openapi.yaml`, `test/test-tiers.json`.
  - Requirement link: REQ-422, REQ-425, REQ-426, REQ-429, REQ-432.
  - Verification link: TEST-441.
  - Verification mode: RED.
  - Command/procedure: `go test -tags=integration ./internal/gateway/httpapi -run '^TestReferenceContract$' -count=1`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P03.S10.
- P03.S10 Implement reference routes and progress
  - Action: Bind section 8.4 routes to the wrapper using the existing ServeMux, one bearer boundary/hash context and shared DTOs. Return safe errors; stream result and recording separately. No user/session prerequisite or internal Go type in Phoenix.
  - Why now: P03.S09 establishes the failing assertions and exact command.
  - Files/surfaces: `internal/gateway/httpapi/{reference,reference_stream}.go`, `reference_contract_test.go`, `api/openapi.yaml`, `test/test-tiers.json`.
  - Requirement link: REQ-422, REQ-425, REQ-426, REQ-429, REQ-432.
  - Verification link: TEST-441.
  - Verification mode: GREEN.
  - Command/procedure: `go test -tags=integration ./internal/gateway/httpapi -run '^TestReferenceContract$' -count=1`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P03.S11.
- P03.S11 Add failing coverage for the restored workspace workflow
  - Action: Add these cases to `frontend/test/harden_llm_web/live/restored_workflow_test.exs` (LiveViewTest with private Req stubs serving the shared OpenAPI fixtures; the existing `HardenLlmWeb.HardenAPI` is the REST client until P04). The step is complete when every numbered case exists and the run fails on at least one intended assertion (not on compilation or fixtures).
    1. Selecting a template fills every form field from the fixture.
    2. Explicit Save posts the complete draft and selects the returned new profile.
    3. Run shows progress, the repaired result and stats.
    4. Restore from history fills the form with recorded inputs and sends no save or run request (assert the Req stub saw none).
    5. Two Portal identities with one key autosave to `workspace:<user id>`, keep different drafts and see the same profiles and history.
    6. A host context with key B sees only key-B fixture data.
    7. Each warning outcome (`disabled`, `configuration_error`, `not_saved`, `unconfirmed`) shows its message, keeps the output and triggers no new run request.
    8. Baseline selectors and CSS classes listed in `test/fixtures/reference-workflows.json` are present.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: baseline `WorkspaceLive`/template and helpers, `frontend/test/harden_llm_web/live/restored_workflow_test.exs`.
  - Requirement link: REQ-420, REQ-426, REQ-427, REQ-429.
  - Verification link: TEST-428.
  - Verification mode: RED.
  - Command/procedure: `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_workflow_test.exs`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P03.S12.
- P03.S12 Implement the restored workspace workflow
  - Action: Restore the baseline workspace shell with existing styles/controls and reference REST calls. Retain output before recording completes; history errors cannot resubmit inference. Move no new business logic to LiveView.
  - Why now: P03.S11 establishes the failing assertions and exact command.
  - Files/surfaces: baseline `WorkspaceLive`/template and helpers, `frontend/test/harden_llm_web/live/restored_workflow_test.exs`.
  - Requirement link: REQ-420, REQ-426, REQ-427, REQ-429.
  - Verification link: TEST-428.
  - Verification mode: GREEN.
  - Command/procedure: `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_workflow_test.exs`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P03.S13.
- P03.S13 Remove superseded Phoenix persistence
  - Action: Switch Phoenix drafts to `/api/v1/state` with `draftKey=workspace:<stable Control Plane user ID>` and history to the REST routes, then delete Phoenix Reference persistence, Ecto schemas/Repo/migrations/deps and retired database task/config. Move all existing persistence assertions (including per-user draft isolation) to the corresponding Go owner before deletion.
  - Why now: Replacement ownership is now verified end to end at the required boundaries.
  - Files/surfaces: `frontend/lib/harden_llm/reference.ex`, Repo/schema files, `frontend/mix.exs`, runtime config, the `web` database variable in `deploy/preview/compose.yml` and test runner.
  - Requirement link: REQ-420, REQ-424, REQ-426, REQ-427, REQ-429, REQ-431, REQ-432.
  - Verification link: TEST-426, TEST-427, TEST-428, TEST-439.
  - Verification mode: REFACTOR.
  - Command/procedure: `go test -tags=integration ./stateful -run '^TestReferenceHistory$' -count=1` ; `go test -tags=integration ./stateful -run '^TestHistoryQueries$' -count=1` ; `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_workflow_test.exs` ; `go test -tags=integration ./stateful -run '^TestCacheDraftContract$' -count=1`.
  - Expected result: Phoenix calls REST only; all original data/concurrency assertions remain covered; one PostgreSQL history authority.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: Any needed replacement assertion or real owner gate is missing.
  - Unlocks: P03.S14.
- P03.S14 Measure recording truth and restored execution
  - Action: Record exact history/provider counts, payload fidelity and failure outcomes against real PostgreSQL/Garage leases.
  - Why now: Implementation and owner contracts are both available.
  - Files/surfaces: same history/workspace suites and evidence ledger.
  - Requirement link: REQ-420, REQ-426, REQ-427, REQ-429, REQ-431, REQ-432.
  - Verification link: TEST-426, TEST-427, TEST-428, TEST-442, TEST-443, TEST-451, EVAL-423.
  - Verification mode: MEASURE.
  - Command/procedure: `go test ./stateful -run '^TestHistoryRecordingOutcome$' -count=1` ; `go test -tags=integration ./stateful -run '^TestReferenceHistory$' -count=1` ; `go test -tags=integration ./stateful -run '^TestHistoryQueries$' -count=1` ; `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_workflow_test.exs` ; `go test -tags=integration ./stateful -run '^TestHistoryStore$' -count=1` ; `go test -tags=integration ./stateful -run '^TestReferenceArtifacts$' -count=1`.
  - Expected result: EVAL-423 passes; no saved claim without a committed row and no lost-result regression.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: Payload/delete acceptance fails or a fake store is being mistaken for PostgreSQL proof.
  - Unlocks: P03 exit.

### Phase P04: All reference workflows are reusable without redesign

- Phase goal: Extract reusable components and complete the full baseline capability set.
- Scope/objectives: REQ-420, REQ-423, REQ-425, REQ-428, REQ-429; complete only the surfaces below.
- Impacted surfaces: `frontend/packages/harden_llm_ui/`; baseline profile/widget/embedding/trace/artifact surfaces; Phoenix API client; frontend assets and Node decision modules.
- Lifecycle evidence: requirements are the linked SRS entries; design/code evidence is the reviewed diff against these surfaces; verification is the ordered commands below; validation demonstrates the phase goal; the configuration checkpoint records source SHA, dependency locks and evidence paths under the common boundary policy.
- Risks/assumptions: LiveView/markup evidence does not establish browser layout; browser testing remains explicit opt-in.
- Exit gates: apply section 5.1; proceed only after this phase's checks pass, escalate unresolved contracts, and stop for scope changes.
- Estimated phase metrics, not measured acceptance: Confidence 84% because the contracts have repository-backed seams; long-term robustness 92% because ownership and failure behavior become explicit; internal interactions 4 because this phase crosses that many application seams; external interactions 1 because it touches that many owner/service contracts; complexity 61% because of the listed integration boundaries; feature creep 8% because scope is constrained to restored behavior; technical debt 8% because superseded paths are removed; YAGNI 5/5 (5 is strongest) because infrastructure follows demonstrated needs; MoSCoW Must because later acceptance depends on this outcome; scope local because of the listed ownership boundaries; architectural changes 1 because the listed ownership/runtime boundaries change.

- P04.S01 Add failing coverage for standalone component packaging
  - Action: Create the independent test host/package task and two-instance cases after the Model Recall interface review. Host supplies base URL/API key/admission paths; package cannot import Model Recall, HLLM host or Portal modules. Add failing baseline assertions before extraction.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: `frontend/packages/harden_llm_ui/{mix.exs,lib,test}`, host imports, `test/test-tiers.json`.
  - Requirement link: REQ-420, REQ-428.
  - Verification link: TEST-429.
  - Verification mode: RED.
  - Command/procedure: `cd frontend/packages/harden_llm_ui && mix test --seed 104729 test/harden_llm_ui/components_test.exs`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P04.S02.
- P04.S02 Implement standalone component packaging
  - Action: Move reusable baseline component/form/projection helpers per section 4.5 into HardenLlmUI once. Move the existing client/error declarations with the component dependencies so the independent host compiles; S03/S04 then establish and implement their explicit-context/decoder behavior. Keep host routing/identity/workspace async lifecycle; the existing reusable widget owns its local Save/refresh pending state. Package code cannot import host modules.
  - Why now: P04.S01 establishes the failing assertions and exact command.
  - Files/surfaces: `frontend/packages/harden_llm_ui/{mix.exs,lib,test}`, host imports, `test/test-tiers.json`.
  - Requirement link: REQ-420, REQ-428.
  - Verification link: TEST-429.
  - Verification mode: GREEN.
  - Command/procedure: `cd frontend/packages/harden_llm_ui && mix test --seed 104729 test/harden_llm_ui/components_test.exs`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P04.S03.
- P04.S03 Add failing coverage for the shared REST client
  - Action: Add OpenAPI-checked fixture decoding, same-key/two-host context sharing, different-key credential isolation, complete snapshot detail/save DTOs, safe errors and no-retry SSE lifecycle assertions.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: `frontend/packages/harden_llm_ui/lib/harden_llm_ui/{client,api_error,diagnostics_wire}.ex`, package client tests.
  - Requirement link: REQ-425, REQ-428, REQ-429, REQ-432.
  - Verification link: TEST-446.
  - Verification mode: RED.
  - Command/procedure: `cd frontend/packages/harden_llm_ui && mix test --seed 104729 test/harden_llm_ui/client_test.exs`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P04.S04.
- P04.S04 Implement the shared REST client
  - Action: Move HardenAPI and diagnostics decoder to the package; accept server Context per request, preserve response/error behavior and interpret recording events separately. Replace host calls, then remove old implementations.
  - Why now: P04.S03 establishes the failing assertions and exact command.
  - Files/surfaces: `frontend/packages/harden_llm_ui/lib/harden_llm_ui/{client,api_error,diagnostics_wire}.ex`, package client tests.
  - Requirement link: REQ-425, REQ-428, REQ-429, REQ-432.
  - Verification link: TEST-446.
  - Verification mode: GREEN.
  - Command/procedure: `cd frontend/packages/harden_llm_ui && mix test --seed 104729 test/harden_llm_ui/client_test.exs`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P04.S05.
- P04.S05 Add failing coverage for profile editor and picker controls
  - Action: Freeze baseline picker/editor/refresh/bundle/credential and repair/rerun bindings. Assert every explicit Save creates a new date/suffix profile including all run fields; model refresh edits the draft, Git changes leave snapshots fixed, and failed Save preserves the draft.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: package `profile_widget_component.ex`, baseline `ProfilesLive`/template, `restored_profiles_test.exs`.
  - Requirement link: REQ-420, REQ-423, REQ-428, REQ-432.
  - Verification link: TEST-447.
  - Verification mode: RED.
  - Command/procedure: `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_profiles_test.exs`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P04.S06.
- P04.S06 Implement profile editor and picker controls
  - Action: Restore these controls through one reusable form/state implementation using complete snapshots. Selecting a saved profile loads definition plus inputs; refresh/edits update the draft; explicit Save selects the newly created snapshot. Keep the visual baseline, safe credential replacement and failed-save draft behavior.
  - Why now: P04.S05 establishes the failing assertions and exact command.
  - Files/surfaces: package `profile_widget_component.ex`, baseline `ProfilesLive`/template, `restored_profiles_test.exs`.
  - Requirement link: REQ-420, REQ-423, REQ-428, REQ-432.
  - Verification link: TEST-447.
  - Verification mode: GREEN.
  - Command/procedure: `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_profiles_test.exs`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P04.S07.
- P04.S07 Add failing coverage for history trace and artifact controls
  - Action: Add baseline numbered-pagination/count/restore/delete/clear/trace/download events and unavailable/incomplete/unknown states; exercise same-key/two-client sharing and cross-key denial. Restore fills the full draft without automatic Save/Run or name-based profile matching.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: package history/trace/stats components, baseline controllers, `restored_history_test.exs`.
  - Requirement link: REQ-420, REQ-426, REQ-428, REQ-429.
  - Verification link: TEST-448.
  - Verification mode: RED.
  - Command/procedure: `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_history_test.exs`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P04.S08.
- P04.S08 Implement history trace and artifact controls
  - Action: Restore history/stats/trace/download controls through one REST projection and the moved baseline pagination component. Keep summary pages in memory and load each full record when its existing details control expands (Kirill accepted this on 2026-10-13); preserve the full captured data and controls. Enforce key scope; restore recorded settings exactly into the draft and require an explicit valid binding when absent. Never borrow foreign credentials or recompute accounting.
  - Why now: P04.S07 establishes the failing assertions and exact command.
  - Files/surfaces: package history/trace/stats components, baseline controllers, `restored_history_test.exs`.
  - Requirement link: REQ-420, REQ-426, REQ-428, REQ-429.
  - Verification link: TEST-448.
  - Verification mode: GREEN.
  - Command/procedure: `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_history_test.exs`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P04.S09.
- P04.S09 Add failing coverage for all baseline reference workflows
  - Action: Create remaining manifest cases for /embed/llm, login/logout, schema helpers, bundles and controller downloads. Every baseline row must have executable ownership.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: baseline `EmbeddingLive`/template, host routes, `restored_workflows_test.exs`, capability manifest.
  - Requirement link: REQ-420, REQ-423, REQ-425, REQ-426, REQ-429.
  - Verification link: TEST-431.
  - Verification mode: RED.
  - Command/procedure: `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_workflows_test.exs`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P04.S10.
- P04.S10 Implement all baseline reference workflows
  - Action: Complete the baseline host routes and reference compositions with package components. Preserve labels/selectors/layout and existing Portal flow; no local registration or frontend provider calls.
  - Why now: P04.S09 establishes the failing assertions and exact command.
  - Files/surfaces: baseline `EmbeddingLive`/template, host routes, `restored_workflows_test.exs`, capability manifest.
  - Requirement link: REQ-420, REQ-423, REQ-425, REQ-426, REQ-429.
  - Verification link: TEST-431.
  - Verification mode: GREEN.
  - Command/procedure: `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_workflows_test.exs`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P04.S11.
- P04.S11 Add failing coverage for shared client decision modules
  - Action: Extend existing production-module tests for independent instances, monotonic progress, one inference terminal and later recording/disconnect events.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: package assets, host `app.js`/`app.css`, `scripts/test/run_progress_test.mjs`.
  - Requirement link: REQ-428, REQ-429.
  - Verification link: TEST-430.
  - Verification mode: RED.
  - Command/procedure: `node --test scripts/test/run_progress_test.mjs`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P04.S12.
- P04.S12 Implement shared client decision modules
  - Action: Move production decisions/styles to one package source and update imports. Keep hooks thin; no DOM emulator or new client state framework.
  - Why now: P04.S11 establishes the failing assertions and exact command.
  - Files/surfaces: package assets, host `app.js`/`app.css`, `scripts/test/run_progress_test.mjs`.
  - Requirement link: REQ-428, REQ-429.
  - Verification link: TEST-430.
  - Verification mode: GREEN.
  - Command/procedure: `node --test scripts/test/run_progress_test.mjs`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P04.S13.
- P04.S13 Delete duplicate UI sources
  - Action: Compare the baseline capability manifest and moved test tags; delete old helpers/assets/decoders only after callers import the package.
  - Why now: All baseline workflow slices are covered.
  - Files/surfaces: host and package modules/assets; OpenAPI shared fixture references.
  - Requirement link: REQ-420, REQ-423, REQ-425, REQ-426, REQ-428, REQ-429, REQ-432.
  - Verification link: TEST-429, TEST-446, TEST-447, TEST-448, TEST-431, TEST-430.
  - Verification mode: REFACTOR.
  - Command/procedure: `cd frontend/packages/harden_llm_ui && mix test --seed 104729 test/harden_llm_ui/components_test.exs` ; `cd frontend/packages/harden_llm_ui && mix test --seed 104729 test/harden_llm_ui/client_test.exs` ; `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_profiles_test.exs` ; `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_history_test.exs` ; `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_workflows_test.exs` ; `node --test scripts/test/run_progress_test.mjs`.
  - Expected result: One source for each helper/style/client decision; package tests do not depend on host implementation.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A removed control/style has no accepted reason or an independent host cannot use the package.
  - Unlocks: P04.S14.
- P04.S14 Measure capability preservation and component isolation
  - Action: Evaluate every frozen capability and two-instance state case without changing the manifest to fit the implementation.
  - Why now: Complete extraction can now be compared with the frozen baseline.
  - Files/surfaces: component/workflow suites and capability evidence.
  - Requirement link: REQ-420, REQ-423, REQ-425, REQ-426, REQ-428, REQ-429, REQ-432.
  - Verification link: TEST-429, TEST-431, TEST-446, TEST-447, TEST-448, TEST-430, EVAL-424.
  - Verification mode: MEASURE.
  - Command/procedure: `cd frontend/packages/harden_llm_ui && mix test --seed 104729 test/harden_llm_ui/components_test.exs` ; `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_workflows_test.exs` ; `cd frontend/packages/harden_llm_ui && mix test --seed 104729 test/harden_llm_ui/client_test.exs` ; `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_profiles_test.exs` ; `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_history_test.exs` ; `node --test scripts/test/run_progress_test.mjs`.
  - Expected result: EVAL-424 thresholds pass; browser layout remains explicitly unmeasured.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: Any missing capability, duplicated source or cross-instance state leak.
  - Unlocks: P04 exit.

### Phase P05: Shared deployment topology works through real service boundaries

- Phase goal: Prepare deployable shared-service composition and browser-free acceptance without altering production yet.
- Scope/objectives: REQ-422, REQ-425, REQ-430, REQ-431, REQ-432, REQ-435; complete only the surfaces below. Startup settings (TEST-449) moved to P02.S15–S16 in v5.2.
- Impacted surfaces: `docker-compose.yml`, `deploy/frontend/compose.frontend.yml`, `deploy/preview/` (gateway storage wiring already landed in P02.S16), deployment scripts, Caddy owner contract, shared PostgreSQL provisioning and composed fixtures.
- Lifecycle evidence: requirements are the linked SRS entries; design/code evidence is the reviewed diff against these surfaces; verification is the ordered commands below; validation demonstrates the phase goal; the configuration checkpoint records source SHA, dependency locks and evidence paths under the common boundary policy.
- Risks/assumptions: No dedicated production PostgreSQL (each preview keeps its own per-environment database, attached only to `gateway`); temporary isolated test services are allowed; live owner provisioning needs reviewed scope.
- Exit gates: apply section 5.1; proceed only after this phase's checks pass, escalate unresolved contracts, and stop for scope changes.
- Estimated phase metrics, not measured acceptance: Confidence 78% because the contracts have external gaps; long-term robustness 91% because ownership and failure behavior become explicit; internal interactions 4 because this phase crosses that many application seams; external interactions 4 because it touches that many owner/service contracts; complexity 68% because of the listed integration boundaries; feature creep 7% because scope is constrained to restored behavior; technical debt 9% because superseded paths are removed; YAGNI 4/5 (5 is strongest) because infrastructure follows demonstrated needs; MoSCoW Must because later acceptance depends on this outcome; scope non-local because of the listed ownership boundaries; architectural changes 2 because the listed ownership/runtime boundaries change.

- P05.S01 Add failing coverage for shared topology and routing
  - Action: Add one shared PostgreSQL attachment, gateway key allowlist/frontend selected key, server-only secrets, same-origin bearer-only REST and HLLM-only reset fixtures. Portal credentials belong to Phoenix only, with no Go identity-authority dependency. Extend the existing injected process runner in `scripts/test/production_config_test.mjs` with TEST-450 cases: (1) an apply selecting the gateway runs `gateway-schema` exactly once before application `up`, using the same accepted image/config/context and retaining `--no-deps`; (2) nonzero schema exit prevents all application starts; (3) a missing schema service or image/command mismatch is rejected before any schema/application start; (4) a frontend-only apply starts no schema service; (5) a no-change apply starts neither schema nor applications; (6) credentials appear in neither Docker arguments nor captured diagnostics. No actual Docker process runs in this Node test.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: Compose/preview descriptors, `internal/deploytest/reference_topology_test.go`, `scripts/{production-config.mjs,test/production_config_test.mjs}`, Agent Platform and Caddy owner diffs.
  - Requirement link: REQ-425, REQ-430, REQ-432, REQ-435.
  - Verification link: TEST-432, TEST-450.
  - Verification mode: RED.
  - Command/procedure: `go test ./internal/deploytest -run '^TestReferenceTopology$' -count=1` ; `node --test scripts/test/production_config_test.mjs`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P05.S02.
- P05.S02 Implement shared topology and routing
  - Action: Prepare the concrete owner-reviewed isolated HLLM database/role grants and initialized-volume provisioning. Use one Go/one Phoenix service and shared PostgreSQL/Garage/Control Plane/Caddy; confirm the Garage credential can list/delete under `artifacts/`. Add the same one-shot `gateway-schema` service to the production descriptor and implement section 8.2's explicit schema invocation in existing `runApply`, before application startup; do not rely on `depends_on` through `--no-deps`. Add the production `HARDEN_LLM_CREDENTIAL_KEY` reference and route `/api/v1/*` to `gateway` in both the production Caddy contract and `routeFor` in `scripts/preview-environment.mjs`. Remove superseded aliases and dedicated Phoenix DB configuration. Record exact owner commands before apply.
  - Why now: P05.S01 establishes the failing assertions and exact command.
  - Files/surfaces: Compose/preview descriptors, `internal/deploytest/reference_topology_test.go`, `scripts/{production-config.mjs,test/production_config_test.mjs}`, Agent Platform and Caddy owner diffs.
  - Requirement link: REQ-425, REQ-430, REQ-432, REQ-435.
  - Verification link: TEST-432, TEST-450.
  - Verification mode: GREEN.
  - Command/procedure: `go test ./internal/deploytest -run '^TestReferenceTopology$' -count=1` ; `node --test scripts/test/production_config_test.mjs`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P05.S03.
- P05.S03 Add failing coverage for the composed browser-free boundary
  - Action: Extend the HTTP-only composed suite for two configured access keys, same-key frontend/backends, cross-key state/history/cache/artifact denial, frontend Portal admission, storage/Control Plane outage and recording states on one hostname. Start from a fresh leased database and run the schema command before application startup; verify the schema version and a real reference write. Register resource selection before invocation.
  - Why now: The named behavior must have an unchanged acceptance oracle before implementation.
  - Files/surfaces: `internal/smoke/compose_smoke_test.go`, test Compose/local provider/Control Plane fixtures.
  - Requirement link: REQ-422, REQ-425, REQ-426, REQ-430, REQ-431.
  - Verification link: TEST-433.
  - Verification mode: RED.
  - Command/procedure: `make test-compose`.
  - Expected result: The named behavioral assertion fails; fixture setup and minimal declarations already compile.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P05.S04.
- P05.S04 Implement the composed browser-free boundary
  - Action: Wire actual Go/Phoenix/Caddy containers and isolated PostgreSQL/Garage; use local provider for deterministic composition. History uses the composed PostgreSQL; no Laminar stub is needed.
  - Why now: P05.S03 establishes the failing assertions and exact command.
  - Files/surfaces: `internal/smoke/compose_smoke_test.go`, test Compose/local provider/Control Plane fixtures.
  - Requirement link: REQ-422, REQ-425, REQ-426, REQ-430, REQ-431.
  - Verification link: TEST-433.
  - Verification mode: GREEN.
  - Command/procedure: `make test-compose`.
  - Expected result: All named assertions pass using the same command and test IDs as RED, without skips or weaker expectations.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A failure is compilation/infrastructure rather than the intended assertion, or the accepted contract cannot be met.
  - Unlocks: P05.S05.
- P05.S05 Remove superseded service ownership
  - Action: Review descriptor/network/env/reset diffs with shared owners; remove only HLLM-owned obsolete service references and duplicate startup paths.
  - Why now: Composed routing and failure behavior pass before removal/apply.
  - Files/surfaces: deployment descriptors, owner-reviewed provisioning/reset manifest.
  - Requirement link: REQ-422, REQ-424, REQ-425, REQ-426, REQ-430, REQ-431, REQ-432, REQ-435.
  - Verification link: TEST-449, TEST-432, TEST-450, TEST-433.
  - Verification mode: REFACTOR.
  - Command/procedure: `go test ./cmd/harden-llm-gateway -run '^TestReferenceConfig$' -count=1` ; `go test ./internal/deploytest -run '^TestReferenceTopology$' -count=1` ; `node --test scripts/test/production_config_test.mjs` ; `make test-compose`.
  - Expected result: No duplicate production service, Phoenix database owner or competing API hostname remains.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: A shared identity/storage resource or unrelated service is in the reset list.
  - Unlocks: P05.S06.
- P05.S06 Measure composed operation and resource count
  - Action: Record service count, route/auth failures, ordinary inference independence and zero browser/real-provider starts.
  - Why now: Exact tested configuration is ready for release review.
  - Files/surfaces: compose report, topology diff and evidence ledger.
  - Requirement link: REQ-422, REQ-425, REQ-426, REQ-430, REQ-431, REQ-432, REQ-435.
  - Verification link: TEST-432, TEST-450, TEST-433, EVAL-425.
  - Verification mode: MEASURE.
  - Command/procedure: `go test ./internal/deploytest -run '^TestReferenceTopology$' -count=1` ; `node --test scripts/test/production_config_test.mjs` ; `make test-compose`.
  - Expected result: EVAL-425 passes; CHECK-422 owner review and P06 release gates remain required before apply.
  - Evidence produced: Tagged assertion/fixture diff, exact command result and the focused implementation or review diff; add measured counts for MEASURE.
  - Stop/escalate condition: Any unintended service/provider/browser operation or failed declared route.
  - Unlocks: P05 exit.

### Phase P06: The accepted release is documented pushed and deployed

- Phase goal: Certify and deploy the reviewed implementation at one recorded source revision.
- Scope/objectives: REQ-433, REQ-434; complete only the surfaces below.
- Impacted surfaces: Existing Git/CI/build lifecycle, `plans/gateway-image-build-deployment-spec.md`, production descriptor, shared owners, release record and deployed HTTP acceptance.
- Lifecycle evidence: requirements are the linked SRS entries; design/code evidence is the reviewed diff against these surfaces; verification is the ordered commands below; validation demonstrates the phase goal; the configuration checkpoint records source SHA, dependency locks and evidence paths under the common boundary policy.
- Risks/assumptions: Only proceed under explicit implementation/release authorization; earlier authorization must be honored if it still applies. No browser/provider request is implied.
- Exit gates: apply section 5.1; proceed only after this phase's checks pass, escalate unresolved contracts, and stop for scope changes.
- Estimated phase metrics, not measured acceptance: Confidence 80% because the contracts have repository-backed seams; long-term robustness 92% because ownership and failure behavior become explicit; internal interactions 3 because this phase crosses that many application seams; external interactions 4 because it touches that many owner/service contracts; complexity 55% because of the listed integration boundaries; feature creep 4% because scope is constrained to restored behavior; technical debt 5% because superseded paths are removed; YAGNI 5/5 (5 is strongest) because infrastructure follows demonstrated needs; MoSCoW Must because later acceptance depends on this outcome; scope non-local because of the listed ownership boundaries; architectural changes 0 because release applies already accepted architecture.

- P06.S01 Certify the candidate and record exact release identity
  - Action: Run deterministic/backend and browser-free release gates in the test environment. Push verified checkpoints; promote through current branch policy. Dispatch the existing release workflow and record its exact successful run/attempt/SHA using the existing image-deployment specification.
  - Why now: Production apply requires complete candidate evidence.
  - Files/surfaces: `Makefile`, `.github/workflows/test-hierarchy.yml`, `plans/gateway-image-build-deployment-spec.md`, release evidence directory.
  - Requirement link: REQ-433, REQ-434.
  - Verification link: EVAL-426.
  - Verification mode: MEASURE.
  - Command/procedure: `make verify` ; `make test-release` ; `git diff HEAD --check`; follow the exact dispatch/attempt-validation shell blocks in section 4 of `plans/gateway-image-build-deployment-spec.md`.
  - Expected result: Required gates pass at the accepted revision; a skipped release job is not accepted.
  - Evidence produced: Local/hosted results, exact run URL/attempt/SHA, remote branch identity.
  - Stop/escalate condition: Any failure, ambiguous candidate identity, missing authorization or required owner change.
  - Unlocks: P06.S02.
- P06.S02 Apply the accepted component and shared-owner configuration
  - Action: Complete CHECK-422, apply reviewed HLLM database/role provisioning and Caddy changes through their owners, build affected images from the accepted SHA using the existing release specification, then use the tested `runApply` sequence in section 8.2: explicitly run the one-shot schema service from that accepted image and require exit 0 before applying the two application services. Record the schema command/image/exit result. Do not run service-heavy certification on the production daemon.
  - Why now: All implementation acceptance and owner prerequisites precede release mutation.
  - Files/surfaces: `scripts/production-config.mjs`, protected production descriptor, root/frontend Dockerfiles and owner-reviewed provisioning from P05.
  - Requirement link: REQ-430, REQ-434.
  - Verification link: TEST-434, CHECK-422.
  - Verification mode: VERIFY.
  - Command/procedure: Use section 4 gateway build procedures and the P05-recorded owner commands. For the affected frontend, require a clean checkout at the accepted SHA and an unused release tag, then execute `docker build --secret=id=private_module_token,env=PRIVATE_MODULE_TOKEN --build-arg VERSION="$HLLM_RELEASE_SHA" --tag "harden-llm-web:release-$HLLM_RELEASE_SHA" --file frontend/Dockerfile frontend`; record its image ID in the protected descriptor. Then `node scripts/production-config.mjs apply --services harden-llm-gateway,harden-llm-web --expected-release "$HLLM_RELEASE_SHA"` ; `node scripts/production-config.mjs check --services harden-llm-gateway,harden-llm-web --expected-release "$HLLM_RELEASE_SHA"`.
  - Expected result: The accepted gateway image applies the schema successfully before either application starts; both services converge to the accepted source/config/image identity.
  - Evidence produced: Build IDs, schema command/image/exit result, scoped apply output and configuration check.
  - Stop/escalate condition: Unreviewed provisioning command, descriptor mismatch, reused release tag or unsafe reset scope.
  - Unlocks: P06.S03.
- P06.S03 Validate the public origin without paid inference
  - Action: Run explicit HTTP-only acceptance with the production URL, accepted source SHA and protected selected bearer. Check frontend Portal entry, authenticated reference status and scope-selector rejection, and record the status `history.rows`/`history.tableBytes` figures as the history-growth baseline (5 GiB review trigger); no provider call, browser or stored-data mutation. Shared-owner readiness remains separate from inference readiness.
  - Why now: Deployed identity must be paired with actual public access evidence.
  - Files/surfaces: internal/smoke/deployed_reference_test.go; scripts/production-config.mjs.
  - Requirement link: REQ-422, REQ-425, REQ-434.
  - Verification link: TEST-434, TEST-435.
  - Verification mode: VERIFY.
  - Command/procedure: `node scripts/production-config.mjs check --services harden-llm-gateway,harden-llm-web --expected-release "$HLLM_RELEASE_SHA"` ; `go test -tags=live ./internal/smoke -run '^TestDeployedReferenceHTTP$' -count=1`.
  - Expected result: Same-origin UI/API and public reference authentication match the contract; requests without a valid bearer and requests with forged scope selectors are denied; no provider request is executed.
  - Evidence produced: Redacted HTTP/status report and production config output.
  - Stop/escalate condition: Route/auth behavior differs or checks would mutate user data.
  - Unlocks: P06.S04.
- P06.S04 Publish the release evidence and close the phase ledger
  - Action: Record complete results, limitations and owner revisions; update release/docs evidence and verify pushed source identity. Record “No refactor needed” because this phase deploys accepted code without changing behavior.
  - Why now: Closeout must distinguish implementation, CI, deployment and unperformed checks.
  - Files/surfaces: `docs/release-certification.md`, `docs/requirements-traceability.md`, this plan execution log and evidence directory.
  - Requirement link: REQ-433, REQ-434.
  - Verification link: TEST-420, TEST-434.
  - Verification mode: VERIFY.
  - Command/procedure: `go test ./internal/testkit -run '^TestReferenceDecouplingContract$' -count=1` ; `node scripts/production-config.mjs check --services harden-llm-gateway,harden-llm-web --expected-release "$HLLM_RELEASE_SHA"` ; `git diff HEAD --check`.
  - Expected result: Documentation matches deployed identity; no required phase remains open; browser layout and live-provider inference are explicitly unverified unless separately exercised.
  - Evidence produced: Final branch/SHA/image/URL/check matrix and pushed evidence commit.
  - Stop/escalate condition: Any gate is missing or documentation falsely claims browser/provider acceptance.
  - Unlocks: P06 exit.


## 6. Evaluations

Evaluations reuse the tests below; they do not introduce another harness or production SLA. Counts are exact over the declared fixtures. Record durations and sample counts; do not manufacture confidence intervals for a single observation. Threshold changes require an ADR before acceptance. EVAL-420 measures that the history and artifact ownership contract is frozen consistently before implementation.

```yaml
evaluations:
  - id: EVAL-420
    purpose: dev
    metrics: [history_store_count, canonical_docs_naming_laminar_history, artifact_paths_outside_key_prefix, fixture_schema_failures]
    thresholds:
      history_store_count: "1: HLLM PostgreSQL"
      canonical_docs_naming_laminar_history: 0
      artifact_paths_outside_key_prefix: 0
      fixture_schema_failures: 0
    seeds: [104729]
    runtime_budget: "1 minute for P00 static checks"
  - id: EVAL-421
    purpose: adversarial
    metrics: [unexpected_provider_calls, cross_call_mutations, plain_client_failures]
    thresholds: {unexpected_provider_calls: 0, cross_call_mutations: 0, plain_client_failures: 0}
    seeds: [104729, 130363, 155921]
    runtime_budget: "3 minutes for focused cases"
  - id: EVAL-422
    purpose: adversarial
    metrics: [scope_violations, overwritten_saved_snapshots, credential_substitutions, stale_cache_hits_accepted, connections_held_during_provider_wait]
    thresholds: {scope_violations: 0, overwritten_saved_snapshots: 0, credential_substitutions: 0, stale_cache_hits_accepted: 0, connections_held_during_provider_wait: 0}
    seeds: [104729, 130363, 155921]
    runtime_budget: "Existing 40-minute integration ceiling; expected 2–8 minutes"
  - id: EVAL-423
    purpose: adversarial
    metrics: [healthy_reference_records_per_execution, direct_call_records, disabled_history_operations, false_persistence_claims, history_blocked_valid_inferences, secret_diagnostic_leaks, payload_mismatches, replayed_inferences, cross_key_artifact_reads]
    thresholds:
      healthy_reference_records_per_execution: 1
      direct_call_records: 0
      disabled_history_operations: 0
      false_persistence_claims: 0
      history_blocked_valid_inferences: 0
      secret_diagnostic_leaks: 0
      payload_mismatches: 0
      replayed_inferences: 0
      cross_key_artifact_reads: 0
    seeds: [104729, 130363, 155921]
    runtime_budget: "Existing 40-minute integration ceiling; expected 2–8 minutes"
  - id: EVAL-424
    purpose: holdout
    metrics: [baseline_capability_pass_fraction, component_state_leaks, duplicate_production_sources]
    thresholds: {baseline_capability_pass_fraction: 1.0, component_state_leaks: 0, duplicate_production_sources: 0}
    seeds: [104729, 130363, 155921]
    runtime_budget: "5 minutes focused; existing fast-task ceilings for aggregate gate"
  - id: EVAL-425
    purpose: adversarial
    metrics: [composed_contract_failures, added_production_services, browser_starts, real_provider_requests]
    thresholds: {composed_contract_failures: 0, added_production_services: 0, browser_starts: 0, real_provider_requests: 0}
    seeds: [104729]
    runtime_budget: "Existing 30-minute Compose ceiling"
  - id: EVAL-426
    purpose: holdout
    metrics: [failed_required_gates, mismatched_release_identities, missing_acceptance_evidence]
    thresholds: {failed_required_gates: 0, mismatched_release_identities: 0, missing_acceptance_evidence: 0}
    seeds: [104729]
    runtime_budget: "Existing per-task release/CI ceilings; no repeated full suite without a new cause"
```

For EVAL-425, `added_production_services` counts additional long-lived application services, not the one-shot schema command running the existing gateway image. Record both the daemon count and the schema invocation in the phase evidence.

EVAL-420 uses the P00.S05 MEASURE commands. Other evaluation commands are the exact commands in their phase's MEASURE step. Seeded permutations run inside the test cases; repeat the same command, not a new scheduler. The frozen baseline manifest is the holdout for visual/control restoration. P00 explicitly records the accepted API-key ownership (with per-user drafts) and snapshot-save semantic deltas; no other oracle is weakened or capability removed to fit implementation.

## 7. Tests

### 7.1. Existing inventory and command ownership

- Go standard testing/httptest: root `*_test.go`, `internal/**/*_test.go`, `cmd/**/*_test.go`; `make test-unit`, `make test-api`, `make test-parity`, `make test-static`, `make test-race`, `make verify`.
- Real PostgreSQL/Garage integration: integration-tag tests and `internal/integrationtest/`; `make test-integration`, `make test-integration-race`; service leases and selection stay in `test/test-tiers.json` and `scripts/run-test-tier.mjs`.
- Phoenix ExUnit/ConnCase/LiveViewTest/Req.Test: `frontend/test/**/*_test.exs`; `cd frontend && mix test`. Existing `frontend/test/harden_llm/reference_integration_test.exs` persistence assertions move to Go before its Ecto task is removed.
- Dependency-free Node tests: `scripts/test/*_test.mjs`; `node --test scripts/test/run_progress_test.mjs`. Retain the existing client decision code as the test subject.
- Browser-free Compose: `internal/smoke/*` with compose tag; `make test-compose`. Do not silently renumber historical task-tier labels while changing selection.
- Aggregate fast/release: `make test-fast`, `make test-release`; CI owns selection through `.github/workflows/test-hierarchy.yml`.
- Formatting/policy: `git diff HEAD --check`, `make format`, `make lint`; `cd frontend && mix format --check-formatted` after Elixir changes.
- Existing browsers: `make test-browser`, `make test-browser-compose` are inactive unless explicitly requested. Existing live-provider tests remain outside deterministic gates. The new HTTP-only deployed test uses a narrow anchored selector and never calls inference.
- New package test tasks are registered in P04 before invocation; `stateful` integration selection is registered in P02.S05 and `internal/gateway/httpapi` integration selection in P03.S09. No new framework, DOM emulator or scheduler is required.

Expose the pinned toolchain before Mix or aggregate frontend commands:

```bash
export PATH=/home/kirill/.local/elixir-1.20.2/bin:/home/kirill/.local/otp-28.4.3/bin:$PATH
```

### 7.2. Suites overview

| Suite | Purpose | Runner | Command | Runtime budget | Cadence |
| --- | --- | --- | --- | --- | --- |
| Unit | Core, API, LiveView and client decisions | Existing tier runner | `make test-fast` | Existing constituent task ceilings; no new aggregate SLA | Pre-commit and CI |
| Integration | Real storage/lifecycle/race boundaries | Go plus existing service pool | `make test-integration` and `make test-integration-race` | Existing 40/50-minute ceilings | CI for changed backend boundaries |
| E2E | Actual composed service boundaries with fake provider | Go Compose harness | `make test-compose` | Existing 30-minute ceiling | Release CI |
| Perf | Test feedback/resource measurements, not provider throughput | Existing benchmark runner | `make benchmark-test-feedback` | Existing selected task ceilings, 5 warm/3 cold samples | Only after a measured test-cost regression |
| Data Drift | Frozen baseline and OpenAPI fixture conformance | Go contract checks | `go test ./internal/testkit -run '^TestReferenceDecouplingContract$' -count=1` | Expected 10–30 seconds | CI; no external dataset/nightly service |
| Static | Boundaries, policy and fixture traceability | Existing Make/Node/Go checks | `make test-static` | Existing 120-second task ceiling | Pre-commit and CI |

### 7.3. Concrete test definitions

All proposed tests are created in the phase that first uses them. Existing test files retain their old tags and assertion purposes and receive the new tags for new coverage. Default deterministic controls: fixed synthetic fixtures; process-owned stubs; bounded waits tied to request context; no public network/provider credentials; no automatic retries. Go randomized fixture permutations use seeds 104729, 130363 and 155921 internally. ExUnit uses the seed shown in its exact command; Node event sequences are fixed. Integration requires a healthy isolated service lease before RED/GREEN assertions begin. Deployed checks are explicit exceptions to offline execution and use no user data or paid inference.

- **TEST-420: Architecture and baseline contract**
  - Type: static; verifies: REQ-420, REQ-425, REQ-432, REQ-433, REQ-435.
  - Location: `internal/testkit/reference_decoupling_contract_test.go`.
  - Command: `go test ./internal/testkit -run '^TestReferenceDecouplingContract$' -count=1`.
  - Fixtures/mocks/data: Pinned baseline manifest, canonical documentation and OpenAPI ownership declarations.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: Every baseline capability retains an owner with the two accepted semantic changes recorded. Canonical policy specifies one bearer/key-hash scope, full create-on-save snapshots, Git/`.env`/PostgreSQL authority, frontend-only Portal admission and Model Recall interface review. No Go user/session ownership or conflicting store declaration. These source checks do not substitute for authentication/storage behavioral tests.
  - Expected runtime: 10–30s.

- **TEST-421: Stateless execution and effect boundaries**
  - Type: unit; verifies: REQ-421, REQ-424, REQ-431, REQ-432.
  - Location: `stateless_contract_test.go`.
  - Command: `go test . -run '^TestStatelessContract$' -count=1`.
  - Fixtures/mocks/data: Local provider, recording cache/event ports, injected clock/wait, concurrent distinct profile snapshots.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: Retry/repair/budgets/cache/accounting and optional trace/artifact capture retain their assertions; nil capture does no artifact work, and capture never leaks fixture credentials; no cross-call mutation, infrastructure SDK ownership or repeated inference after sink failure.
  - Expected runtime: 10–30s.

- **TEST-422: Ordinary OpenAI client contract**
  - Type: unit; verifies: REQ-422, REQ-431, REQ-432.
  - Location: `internal/gateway/httpapi/openai_contract_test.go`.
  - Command: `go test ./internal/gateway/httpapi -run '^TestOpenAIContract' -count=1`.
  - Fixtures/mocks/data: Existing official-SDK harness and selected deployment-key fixture, local provider, missing/invalid bearers, unavailable database/Control Plane and supported/unsupported requests. This existing protocol oracle stays runnable in P01 before P02 adds multi-key composition.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: Models/chat/responses accept the selected deployment bearer without login/profile/custom identity header. Errors/tools/final-only SSE match OpenAPI; cache-off calls succeed during storage/identity outage and direct calls create zero history entries. No incoming key/hash enters root Request or diagnostic payload. P02's TEST-425/439 add two-key authentication/cache isolation without changing this existing protocol oracle; rerun this gate after P02 composition changes.
  - Expected runtime: 10–45s.

- **TEST-423: Complete saved profiles and credential bindings**
  - Type: unit; verifies: REQ-423, REQ-427, REQ-432.
  - Location: `stateful/profile_test.go`.
  - Command: `go test ./stateful -run '^TestProfileContract$' -count=1`.
  - Fixtures/mocks/data: Value-only complete snapshot/template fixtures for two API-key hashes, all run fields and explicit false/zero/empty values; changed/deleted Git templates, hidden choices, credential bindings and five recovery leaves. No fake SQL repository.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: One snapshot validator preserves all nonsecret setup/input values. Template selection fills a draft; Save freezes it with a new UUID/time/suffix, never a live override or update. Changed/deleted Git templates leave saved snapshots unchanged. Invalid origin/binding and foreign-key records fail without substitution. Picker/detail DTOs omit secrets, and core retry validation owns policy semantics. Real CRUD/restart/refresh/bundles remain TEST-438/440; encryption remains TEST-444.
  - Expected runtime: 10–30s.

- **TEST-424: PostgreSQL storage and concurrency**
  - Type: integration; verifies: REQ-423, REQ-424, REQ-427, REQ-431, REQ-432.
  - Location: `stateful/storage_integration_test.go`.
  - Command: `go test -tags=integration ./stateful -run '^TestReferenceStorage$' -count=1`.
  - Fixtures/mocks/data: Real PostgreSQL lease; two key hashes; snapshot, hidden-preset, entered-credential, cache, mutable-draft and history rows; cross-key FKs, invalid binding/hash/null cases, credential/draft positive revisions and incompatible schema version.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: Fresh/versioned ApplySchema is transactional and idempotent; mismatch fails without reset. Composite key-hash predicates/FKs isolate every row and forbid cross-key credential binding. Complete snapshot rows and hidden-preset preferences satisfy their separate constraints. History rows carry the key hash and a page index; no incoming-key/user/session/outbox registry tables. Only leased data is cleaned; cancellation bounds connection checkout. Full transaction/restart/cache behavior remains TEST-438/439/440.
  - Expected runtime: 30–120s with warm service pool; existing aggregate integration gate remains required.

- **TEST-425: API-key REST access**
  - Type: unit; verifies: REQ-425, REQ-427, REQ-432.
  - Location: `internal/gateway/httpapi/reference_access_test.go`.
  - Command: `go test ./internal/gateway/httpapi -run '^TestReferenceAccess' -count=1`.
  - Fixtures/mocks/data: One httptest API with two accepted synthetic HLLM keys from `access-keys.json`, a recording handler that captures request context, invalid/duplicate/hash-only bearers, caller owner/user/hash/session selectors and a Control Plane request counter. No database and no fake store; HTTP-level storage isolation is TEST-441.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: A valid key alone authorizes both REST surfaces and derives its hash server-side; missing/invalid bearer fails before storage/provider work. Scope/session selectors return 400 `scope_selector_rejected`, and fingerprints cannot authenticate. The handler receives exactly the fixed expected hash for each key, and `ClientForKey` does no storage work. Go performs zero identity-authority requests, including during outage. Portal login/admission remains a separate Phoenix host assertion; ordinary `/v1` is independent.
  - Expected runtime: 10–30s.

- **TEST-426: Reference history semantics**
  - Type: integration; verifies: REQ-426, REQ-429, REQ-431, REQ-432.
  - Location: `stateful/history_integration_test.go`.
  - Command: `go test -tags=integration ./stateful -run '^TestReferenceHistory$' -count=1`.
  - Fixtures/mocks/data: Real PostgreSQL and Garage leases; local provider; repaired success, cache hit, validation failure, canceled request, explicit disablement, enabled-with-nil-pool configuration error, closed pool, oversize record, injected commit hook that drops the outcome after COMMIT, failed readback, and synthetic secrets inside driver-error fixtures.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: Healthy enabled recording inserts one complete record with the authenticated hash; direct/disabled calls do no history I/O. Every detail/list/stats/restore/delete/clear is key-scoped. History outage/configuration failure preserves otherwise authorized inference; invalid profiles/credentials prevent dispatch. Saved, not saved (definite), unconfirmed (absent or failed readback after unknown commit), empty and read failure differ. Exactly one insert attempt and at most one readback per run; no inference retry, polling or secret diagnostic leak.
  - Expected runtime: 30–120s with warm service pool.

- **TEST-427: History statistics, delete and clear**
  - Type: integration; verifies: REQ-426, REQ-427, REQ-429, REQ-431, REQ-432.
  - Location: `stateful/history_queries_integration_test.go`.
  - Command: `go test -tags=integration ./stateful -run '^TestHistoryQueries$' -count=1`.
  - Fixtures/mocks/data: Real PostgreSQL and Garage leases; history rows for key A (two client contexts) and key B with known/unknown/zero costs and cache hits; artifact objects under each run prefix; one missing object; one run started after the clear cutoff.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: Statistics use the same key/window filter as list, sum known costs, count unknown observations and never treat missing records as zero-cost. Delete removes the run's Garage prefix then its row; a missing object counts as removed and a failed object delete keeps the row and reports `artifact_cleanup_failed`. Clear removes only key A rows before the cutoff in batches, leaves key B and the later run intact and returns deleted count, `complete` and the first failed run ID.
  - Expected runtime: 30–120s with warm service pool.

- **TEST-428: Restored workspace vertical workflow**
  - Type: unit; verifies: REQ-420, REQ-426, REQ-427, REQ-429.
  - Location: `frontend/test/harden_llm_web/live/restored_workflow_test.exs`.
  - Command: `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_workflow_test.exs`.
  - Fixtures/mocks/data: WEB-TEST-118; OpenAPI-valid private Req fixtures, full saved-input snapshots and baseline selectors/forms; two Portal identities sharing one HLLM key with distinct draft keys, plus a host context using the second key.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: Selection → complete draft/new profile Save → repair/progress/result/stats → same-key history restore works. Portal identities share profiles/history but keep separate drafts; a different key partitions everything. Restore copies prompts/settings without name lookup or implicit Save/Run. All recording warning states preserve output and forbid resubmission. Baseline markup/style remains, with only accepted snapshot naming/save semantics.
  - Expected runtime: 15–60s.

- **TEST-429: Reusable component library**
  - Type: unit; verifies: REQ-420, REQ-428.
  - Location: `frontend/packages/harden_llm_ui/test/harden_llm_ui/components_test.exs`.
  - Command: `cd frontend/packages/harden_llm_ui && mix test --seed 104729 test/harden_llm_ui/components_test.exs`.
  - Fixtures/mocks/data: WEB-TEST-119; independent test host informed by read-only Model Recall interface review, schema-valid API fixtures and two component instances with host-supplied API-key contexts.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: Picker/input/result/stats/history/pagination components render and dispatch independently; package imports no host/Portal/Ecto module, stores no incoming keys and owns no login. API-key context/instance state do not leak; explicit save/detail and baseline visual bindings work. Assets/decoders/styles have one source; Model Recall migration is not required.
  - Expected runtime: 15–60s.

- **TEST-430: Progress and client decisions**
  - Type: unit; verifies: REQ-428, REQ-429.
  - Location: `scripts/test/run_progress_test.mjs`.
  - Command: `node --test scripts/test/run_progress_test.mjs`.
  - Fixtures/mocks/data: Existing production decision functions, fixed event sequences including duplicate/out-of-order/disconnect cases.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: Progress remains monotonic and terminal outcomes stable; multiple widgets do not leak state; tests import production functions without a DOM emulator.
  - Expected runtime: 1–5s.

- **TEST-431: Complete restored reference workflows**
  - Type: unit; verifies: REQ-420, REQ-423, REQ-425, REQ-426, REQ-429.
  - Location: `frontend/test/harden_llm_web/live/restored_workflows_test.exs`.
  - Command: `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_workflows_test.exs`.
  - Fixtures/mocks/data: WEB-TEST-120; baseline profile/editor/bundle/history/trace/artifact/embed cases; private Req ownership and shared OpenAPI fixtures.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: Every baseline manifest capability passes success/error interactions; presets and persisted private customizations/credential replacement remain usable; history messages use the common error contract. No removed controls, replacement layout or second logic/style implementation; shared login/logout and artifact authorization remain correct without an HLLM registration/password flow.
  - Expected runtime: 20–90s.

- **TEST-432: Shared deployment topology**
  - Type: static; verifies: REQ-425, REQ-430, REQ-435.
  - Location: `internal/deploytest/reference_topology_test.go`.
  - Command: `go test ./internal/deploytest -run '^TestReferenceTopology$' -count=1`.
  - Fixtures/mocks/data: Compose/Caddy/env/descriptor fixtures; approved shared-service attachments; HLLM-only reset allowlist.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: One Go and one Phoenix service share UI/`/v1`/`/api/v1` origin. Go validates one configured key-array source for all REST; Phoenix uses its server-held selected key and Portal admission. No Go Control Plane/session requirement, private-only API route, second hostname, dedicated production PostgreSQL, Phoenix DB credentials or broad reset. Production `gateway-schema` is a one-shot command with no build stanza, using the accepted gateway image and the same protected database configuration; it is not a third long-lived application. Preview descriptors may keep their per-environment `postgres`, attached only to `gateway`, and set no `HARDEN_LLM_ARTIFACT_*` values. No key issuance, secret echo or per-user key synchronization.
  - Expected runtime: 10–30s.

- **TEST-433: Browser-free composed workflow**
  - Type: e2e; verifies: REQ-422, REQ-425, REQ-426, REQ-430, REQ-431.
  - Location: `internal/smoke/compose_smoke_test.go`.
  - Command: `make test-compose`.
  - Fixtures/mocks/data: Existing fake provider and frontend Control Plane admission fixture, two synthetic HLLM access keys, local Caddy, isolated real PostgreSQL/Garage, HTTP/LiveView harness.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: Actual one-host boundaries preserve restored workflow and frontend Portal admission without company/product grant. A fresh leased database receives schema version 1 from the gateway's schema command before serving; a real reference write succeeds afterward. Valid bearer alone reaches reference REST; key A shares data across contexts and key B cannot access it. PostgreSQL/Control Plane outage leaves cache-off REST inference and local key authentication independent; history outage preserves resolved output. No browser or real provider.
  - Expected runtime: 3–15min within existing 30min runner ceiling.

- **TEST-434: Deployed configuration identity**
  - Type: integration; verifies: REQ-430, REQ-434.
  - Location: `scripts/production-config.mjs`.
  - Command: `node scripts/production-config.mjs check --services harden-llm-gateway,harden-llm-web --expected-release "$HLLM_RELEASE_SHA"`.
  - Fixtures/mocks/data: Protected production descriptor and running Docker metadata; exact successful release-attempt SHA.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks.
  - Pass criteria: Both selected services match intended source/image/config/network identity; checker exits nonzero for mismatch and emits no secrets.
  - Expected runtime: 5–30s.

- **TEST-435: Deployed HTTP access and contract**
  - Type: e2e; verifies: REQ-422, REQ-425, REQ-434.
  - Location: `internal/smoke/deployed_reference_test.go`.
  - Command: `go test -tags=live ./internal/smoke -run '^TestDeployedReferenceHTTP$' -count=1`.
  - Fixtures/mocks/data: Deployment URL/release SHA plus protected process-env bearer; no browser session, stored user data or provider execution.
  - Deterministic controls: section 7.3 defaults; use the stated isolated real service for integration and exact release identity for deployed checks. Required deployment variables: `HARDEN_LLM_ACCEPTANCE_URL`, `HLLM_RELEASE_SHA`, `HARDEN_LLM_TOKEN`; fail explicitly when absent.
  - Pass criteria: Health/readiness, Portal entry, unauthenticated denial, models, local request-validation errors and bearer-only reference status match contract. Forged ownership/hash/session inputs cannot select another scope. Read only the status figures (`history.rows`, `history.tableBytes`); read no stored records and make no paid inference or mutation. These checks do not claim a signed-in interactive workflow or exhaustive deployed cross-key isolation.
  - Expected runtime: 5–30s; each HTTP request bounded to 5s.

- **TEST-436: History and artifact contract fixtures**
  - Type: static; verifies: REQ-426, REQ-432.
  - Location: `internal/testkit/reference_history_contract_test.go`.
  - Command: `go test ./internal/testkit -run '^TestReferenceHistoryContract$' -count=1`.
  - Fixtures/mocks/data: `test/fixtures/reference/{history-outcomes,history-pages,artifact-manifest}.json` and `api/openapi.yaml`.
  - Deterministic controls: section 7.3 defaults.
  - Pass criteria: Every fixture validates against OpenAPI; every history row carries a 64-character key hash; pages carry `page`, `pageSize`, `totalCount`; `RecordingOutcome.status` uses only the five section 8.4 values; every artifact object key matches `artifacts/<api_key_hash>/<run_id>/<artifact_id>` with the row's own hash; no fixture contains a caller-supplied object key, raw key or leak sentinel.
  - Expected runtime: 1–10s.

- **TEST-437: Resolved recovery targets and attempt pricing**
  - Type: unit; verifies: REQ-421, REQ-423, REQ-429, REQ-432.
  - Location: `recovery_snapshot_test.go`.
  - Command: `go test . -run '^TestRecoverySnapshot$' -count=1`.
  - Fixtures/mocks/data: recovery-cross-profile.json; two local providers with independent credentials/rates; fixed clock and attempt budget.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: Cross-profile repair/escalation/rerun invokes the exact resolved targets; invalid targets dispatch zero calls; concurrent snapshots stay isolated; reported/unknown/zero/mixed pricing and cache ledgers remain correct.
  - Expected runtime: 10–30s.

- **TEST-438: Atomic API-key profile snapshots**
  - Type: integration; verifies: REQ-423, REQ-427, REQ-432.
  - Location: `stateful/profile_integration_test.go`.
  - Command: `go test -tags=integration ./stateful -run '^TestProfilePersistence$' -count=1`.
  - Fixtures/mocks/data: Real PostgreSQL lease; key A/two client contexts and key B; full prompt/schema/repair snapshots, hidden templates, changed/deleted Git templates, repeated saves, credential replacement/CAS and concurrent draft writes.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: Every explicit Save inserts a distinct UUID/time/suffix with exact normalized inputs/settings and atomically saves any entered credential. No old profile update exists. List/detail/delete/hide are key-scoped; same-key contexts share rows and foreign keys cannot access/bind them. Git changes/reconstruction leave snapshots fixed. Credential/draft conflicts are explicit and failed transactions/probes preserve prior data. No connection remains held during probes.
  - Expected runtime: 30–120s with warm service pool.

- **TEST-439: Cache and draft ownership**
  - Type: integration; verifies: REQ-424, REQ-427, REQ-431.
  - Location: `stateful/cache_draft_integration_test.go`.
  - Command: `go test -tags=integration ./stateful -run '^TestCacheDraftContract$' -count=1`.
  - Fixtures/mocks/data: Real PostgreSQL lease; two accepted key hashes, same-key concurrent clients with the same and different draft keys, fixed TTL clock, expired/corrupt cache, entered/deployment key changes, recovery-target credential changes, draft conflicts, pool size one and blocked provider.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: Cache/draft ownership is key-scoped; same-key clients share cache, share a draft only under the same draft key, and different keys share nothing. Changed provider credentials or recovery credential snapshots cannot hit prior credential-scoped entries. Expired cache cannot be served; at most 100 expired rows are physically deleted per owning write, preserving live/foreign rows. Corrupt records follow core validation. Draft CAS/outage differs from absence. No checked-out connection during provider waits; cache-off calls never acquire pool.
  - Expected runtime: 30–120s with warm service pool.

- **TEST-440: Profile probes models and bundle replacement**
  - Type: integration; verifies: REQ-420, REQ-423, REQ-427, REQ-432.
  - Location: `stateful/profile_resources_integration_test.go`.
  - Command: `go test -tags=integration ./stateful -run '^TestProfileResources$' -count=1`.
  - Fixtures/mocks/data: Real PostgreSQL lease; local provider model discovery and explicit Save/import probes, old/new complete snapshots and full draft values, current encrypted same-hash bundle and wrong-hash/encryption-key/version variants.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: Explicit Save probes once/one attempt; failed/unknown probe never saves/retries. Model refresh returns a changed draft, performs no generation and leaves old snapshot intact. Bundle import is atomic for its matching API-key hash/encryption key and retains candidate probes before the transaction. Complete saved inputs/provenance and hidden preferences roundtrip; no incoming/environment/plaintext key or legacy schema export/import.
  - Expected runtime: 30–120s with warm service pool.

- **TEST-441: Reference HTTP wire, streaming and storage isolation**
  - Type: integration; verifies: REQ-422, REQ-425, REQ-426, REQ-429, REQ-432.
  - Location: `internal/gateway/httpapi/reference_contract_test.go`.
  - Command: `go test -tags=integration ./internal/gateway/httpapi -run '^TestReferenceContract$' -count=1`.
  - Fixtures/mocks/data: OpenAPI-valid shared fixtures, httptest server over the real `stateful.Service`, real PostgreSQL and Garage leases, two synthetic bearer keys, local provider and SSE recorder; no reference session/Control Plane stub and no fake store.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: Every section 8.4 route validates one bearer/hash scope and its wire/error schema, including POST snapshot creation, GET full setup and status `history:{rows,tableBytes}` (present only when history is enabled; unavailable rather than zero on read failure). Missing/forged scope authentication dispatches zero providers. Key B gets 404 for key A's profiles, history and artifacts; the same key shares data across two clients; drafts are shared only under the same draft key. SSE emits one inference terminal followed by bounded recording outcome. Ordinary OpenAI contracts remain supported without login, scope headers or root identity fields.
  - Expected runtime: 10–30s.

- **TEST-442: PostgreSQL history store**
  - Type: integration; verifies: REQ-426, REQ-427, REQ-429, REQ-431, REQ-432.
  - Location: `stateful/history_store_integration_test.go`.
  - Command: `go test -tags=integration ./stateful -run '^TestHistoryStore$' -count=1`.
  - Fixtures/mocks/data: Real PostgreSQL lease; history-pages.json rows for two hashes with equal start timestamps; 17 MiB boundary record; injected clock and commit hook.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: One insert per run records the server key hash; callers cannot choose hash or SQL. One fixed parameterized filter drives list/count/detail; numbered pagination/total counts preserve baseline navigation, tie order and the fixed `before` cutoff. Foreign-key run IDs return not found. Oversize is rejected as not saved without truncation. The usage query returns the key's own row count and a positive table size, unaffected by another key's rows. Unknown commit gets exactly one readback: found means saved; absent or failed means unconfirmed. A delayed real commit becomes visible after an empty readback without another insert; definite pre-COMMIT rejection alone means not saved. No polling, insert retry or secret-bearing error. The 17 MiB boundary record inserts and loads within the named history budgets (2 s insert, 2 s detail) on the real lease, and its measured durations are recorded.
  - Expected runtime: 30–120s with warm service pool.

- **TEST-443: Artifact streaming and cleanup**
  - Type: integration; verifies: REQ-426, REQ-429, REQ-432.
  - Location: `stateful/artifact_integration_test.go`.
  - Command: `go test -tags=integration ./stateful -run '^TestReferenceArtifacts$' -count=1`.
  - Fixtures/mocks/data: Real Garage lease; key A/two clients and key B, baseline trace/parse-failure bytes/support bundle/media/digest, missing object/digest mismatch, history disabled/not saved and interrupted prefix delete.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: Bounded publication preserves baseline bytes/redaction. Same-key authenticated downloads return exact media/bytes even with history disabled or not saved; with all artifact settings absent, publication is skipped, status reports disabled and downloads return `artifact_not_found`; other keys and guessed foreign IDs cannot read/delete because the object key is built from the authenticated hash. No arbitrary Garage key or external redirect. Delete cleans only selected-scope objects and reports partial/unknown outcomes without replaying inference.
  - Expected runtime: 30–120s with warm service pool.

- **TEST-444: Credential encryption and binding**
  - Type: unit; verifies: REQ-423, REQ-427, REQ-432.
  - Location: `stateful/credential_test.go`.
  - Command: `go test ./stateful -run '^TestCredentialContract$' -count=1`.
  - Fixtures/mocks/data: Fixed 32-byte encryption key/test nonce reader, baseline associated-data encoder using key hash/credential ID/origin, same-key clients, tampered hash/ID/origin/encryption-key/ciphertext and synthetic raw-key leak sentinels.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: One encrypted current format rejects associated-data mismatch and foreign-key decrypt. Plaintext HLLM/provider secrets are absent from read DTOs/bundles/diagnostics/metadata. Credential source is explicit with no environment substitution. Production uses crypto randomness; no legacy decryptor or raw incoming-key registry.
  - Expected runtime: 1–10s.

- **TEST-445: Concrete effects composition**
  - Type: unit; verifies: REQ-421, REQ-431, REQ-432, REQ-435.
  - Location: `stateful/client_test.go`.
  - Command: `go test ./stateful -run '^TestClientComposition$' -count=1`.
  - Fixtures/mocks/data: Existing local provider fixtures; in-memory OTEL exporter; no database config; disabled/failed observer.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: NewClient composes one core engine without database/identity/history requirements; preserved span/metric names and safe attributes; provider/cancellation/retry behavior independent of telemetry outcome; no duplicate transport or root concrete SDK dependency.
  - Expected runtime: 10–30s.

- **TEST-446: Reusable REST client contract**
  - Type: unit; verifies: REQ-425, REQ-428, REQ-429, REQ-432.
  - Location: `frontend/packages/harden_llm_ui/test/harden_llm_ui/client_test.exs`.
  - Command: `cd frontend/packages/harden_llm_ui && mix test --seed 104729 test/harden_llm_ui/client_test.exs`.
  - Fixtures/mocks/data: WEB-TEST-121; OpenAPI fixtures, private Req ownership, two host API-key contexts, same-key client sharing, caller scope-selector rejection, unknown errors and SSE terminal/recording/disconnect sequences.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: One client/decoder uses API key in Authorization only; no session/user/hash authority header. Complete snapshot detail/save fields and pagination decode to canonical schemas. Key A/B context secrets cannot leak across requests or to the browser; errors remain safe. Inference completion differs from recording completion and disconnect never retries work.
  - Expected runtime: 15–60s.

- **TEST-447: Restored profile editor interactions**
  - Type: unit; verifies: REQ-420, REQ-423, REQ-428, REQ-432.
  - Location: `frontend/test/harden_llm_web/live/restored_profiles_test.exs`.
  - Command: `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_profiles_test.exs`.
  - Fixtures/mocks/data: WEB-TEST-122; baseline picker/editor/refresh/bundle/credential/repair events, complete snapshot and draft fixtures, two component instances with explicit IDs.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: Existing visual controls select full setups, edit the current draft and save each explicit action as a new UUID/date/suffix profile. Refresh updates draft only; failed Save retains inputs. Delete/hide/recreate/bundles/credential replacement and repair/rerun remain usable. Loading/restoring never saves or executes; secrets are not echoed and form logic has one source.
  - Expected runtime: 15–60s.

- **TEST-448: Restored history trace and artifact interactions**
  - Type: unit; verifies: REQ-420, REQ-426, REQ-428, REQ-429.
  - Location: `frontend/test/harden_llm_web/live/restored_history_test.exs`.
  - Command: `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_history_test.exs`.
  - Fixtures/mocks/data: WEB-TEST-123; baseline numbered pages/counts and history/stat/trace/download bindings, key A shared by two contexts, key B and every history/error/cleanup state.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: Numbered pagination/counts and restore/delete/clear/stats/download controls retain baseline bindings. Reads obey key scope; same key shares history and foreign keys cannot access it. Restore fills complete recorded inputs/settings in the draft, without display-name lookup, foreign credentials or automatic Save/Run. Failed reads are not empty; recording preserves output and partial cleanup/unknown costs are explicit.
  - Expected runtime: 15–60s.

- **TEST-449: Reference startup configuration and schema command**
  - Type: unit; verifies: REQ-424, REQ-430, REQ-431, REQ-432.
  - Location: `cmd/harden-llm-gateway/reference_config_test.go`; one Node case in `scripts/test/preview_policy_test.mjs`.
  - Command: `go test ./cmd/harden-llm-gateway -run '^TestReferenceConfig$' -count=1` ; `node --test scripts/test/preview_policy_test.mjs`.
  - Fixtures/mocks/data: Synthetic required api_key_envs metadata arrays plus environment maps and selected frontend key, missing/duplicate/empty names/values, forbidden literal secrets in JSON, storage/encryption/feature settings, absent Control Plane settings in Go, schema-command inputs, `deploy/preview/compose.yml` and the `previewSecrets()` output.
  - Deterministic controls: section 7.3 defaults; real-storage cases use `internal/integrationtest.PostgresLease`/`GarageLease`, unique scopes and bounded cleanup, never production. No browser or real provider.
  - Pass criteria: One gateway metadata/env path validates without printing secrets or using scalar/session fallback. JSON contains only key environment names; frontend and gateway use the approved shared HARDEN_LLM_TOKEN value. No DB ping/key generation/identity request at construction. Disabled history differs from bad enabled config; one pool/independent status; schema command cannot reset broad data. The preview descriptor wires the gateway database, history flag and credential key, runs the one-shot `gateway-schema` service before `gateway`, and sets no artifact settings; `previewSecrets()` yields a standard-base64 32-byte credential key and existing values are kept.
  - Expected runtime: 10–30s.

- **TEST-450: Production schema invocation precedes application startup**
  - Type: unit; verifies: REQ-430, REQ-432, REQ-435.
  - Location: `scripts/test/production_config_test.mjs`.
  - Command: `node --test scripts/test/production_config_test.mjs`.
  - Fixtures/mocks/data: Existing protected-descriptor fixtures and injected process runner; accepted gateway image/SHA, one-shot `gateway-schema` service, synthetic database credential sentinel, successful/failed schema exits, missing service and mismatched image/command, frontend-only and no-change apply cases. Extend the existing test file in P05.S01; do not create another deployer or runner.
  - Deterministic controls: Node only, process-owned fixtures, fixed command results; no Docker daemon, database, provider or browser. Assertions inspect calls to the production `runApply`, not a test-only implementation.
  - Pass criteria: For an apply selecting the gateway that needs changes, validated schema invocation runs exactly once before any application `up`, with the accepted gateway image/config/context; application startup retains `--no-deps`. Nonzero schema exit, missing service or mismatched image/command prevents application startup. Frontend-only and no-change apply do not start the schema service. No credential appears in command arguments or diagnostics.
  - Expected runtime: 1–10s.

- **TEST-451: History recording outcome classification**
  - Type: unit; verifies: REQ-426, REQ-429, REQ-431.
  - Location: `stateful/history_test.go`.
  - Command: `go test ./stateful -run '^TestHistoryRecordingOutcome$' -count=1`.
  - Fixtures/mocks/data: Table-driven committed, definitely rejected and unknown commit outcomes with found/absent/failed readback. Created in P03.S01; exercise the one production classifier used by `insertHistory`.
  - Deterministic controls: Default-tag Go, no storage/provider or fake store; synthetic outcomes and fixed expected status strings.
  - Pass criteria: Confirmed commit or positive readback after unknown commit returns saved; definite pre-COMMIT rejection returns not_saved; unknown commit with absent or failed readback returns unconfirmed. Empty readback never proves rejection. TEST-442 separately proves the real delayed-commit boundary.
  - Expected runtime: 1–10s.

### 7.4. Decision record and remaining owner checks

- CHECK-420: Current accepted policy is sections 2/4/8: incoming HLLM bearer alone authenticates all Go REST; its server-derived `api_key_hash` scopes every stored application record. Portal controls frontend admission only. Same key shares profiles/history across logins/clients, drafts are per host-supplied draft key (per Portal user in the reference host), different keys isolate everything, and replacing a key starts a fresh scope handled manually for now. Explicit Save creates a complete UUID/date/suffix profile; templates have no live inheritance. Model Recall is the first read-only component interface reference. History lives in HLLM PostgreSQL with nonblocking recording and no automatic expiry; delete/clear applies only to the authenticated key; artifacts are scoped by Garage path. Historical decision records remain linked for provenance but their conflicting user/session/shared-history/update rules are superseded. P00 updates ADR-HLLM-033/OpenAPI/policy; no new approval is needed for these settled choices.
- CHECK-421: v5.1 replaces the Laminar owner gate. P00.S05 records ADR-HLLM-034 (PostgreSQL history owner, Garage path-scoped artifacts, manual orphan cleanup, Laminar for diagnostics only) and passes EVAL-420. No external owner input is required.
- CHECK-422: Before release, review the exact HLLM database/role/resource reset allowlist, the production `HARDEN_LLM_CREDENTIAL_KEY` (generated once by the operator with `openssl rand -base64 32`, stored only in the protected production environment and backed up with it, because losing it makes every entered provider credential unreadable), Garage bucket credential with list/delete under `artifacts/`, owner-provided provisioning commands, network/Caddy diff, exact successful release attempt and current deployment authorization. Confirm no shared identity or other product resources are in the reset. Missing review evidence blocks apply; it does not justify inventing a deployment command.

v5.1 decisions accepted by Kirill on 2026-10-11 after the v5.0 review. They are settled; do not ask again.

| ID | Decision |
| --- | --- |
| DEC-HLLM-V51-01 | Product-market-fit functionality is the priority. Security hardening beyond the stated invariants (key scope, no plaintext secrets, server-derived hash) is deferred. |
| DEC-HLLM-V51-02 | Reference history lives in one key-scoped `history` table in the HLLM PostgreSQL database (sections 8.2/8.3). Laminar keeps only existing diagnostic traces. Chosen over Laminar-with-split, Laminar-without-delete and keeping Phoenix Ecto because it adds no service, integration or RAM and has the least complexity and debt. |
| DEC-HLLM-V51-03 | Artifacts live at `artifacts/<api_key_hash>/<run_id>/<artifact_id>` in Garage; the path is the scope. Orphans from unrecorded runs are cleaned manually. |
| DEC-HLLM-V51-04 | Drafts are per host-supplied draft key; the reference host uses `workspace:<stable Control Plane user ID>`. Profiles, history and statistics stay shared per access key. |
| DEC-HLLM-V51-05 | Key rotation keeps `sha256(key)` as the scope and is handled manually by an operator when needed (section 2, Clean cut). A designed rotation flow is future work. |
| DEC-HLLM-V51-06 | Implementation uses `feat/reference-decoupling` from current `origin/main`. Resetting `origin/dev` to `origin/main` at the start of P00.S01 is approved. |
| DEC-HLLM-V51-07 | Issue #100 gets a handoff comment pointing to this revision. |

v5.2 decisions accepted by Kirill on 2026-10-12 from the [v5.2 review record](../ask_me/reference-decoupling-v52-review-decisions.md). They are settled; do not ask again.

| ID | Decision |
| --- | --- |
| DEC-HLLM-V52-01 | Gateway startup/storage settings (TEST-449) move to P02.S15–S16. From P02 exit the preview `gateway` uses the per-environment preview database and runs the schema command; TEST-432's no-dedicated-PostgreSQL rule applies to production only. |
| DEC-HLLM-V52-02 | Every preview, including `dev`, runs without Garage and reports artifacts disabled until production. A `dev` bucket is optional later work under CHECK-422. |
| DEC-HLLM-V52-03 | P03 order: history store, then artifacts (TEST-443, with prefix delete), then statistics/delete/clear (TEST-427), then run/recording. |
| DEC-HLLM-V52-04 | No history quota. `/api/v1/status` reports `history:{rows,tableBytes}`; P06 records them; 5 GiB is the retention review trigger. |
| DEC-HLLM-V52-05 | All moved and new settings use `HARDEN_LLM_*`; no `HLLM_*` setting names. |
| DEC-HLLM-V52-06 | History insert 2 s, readback 1 s and detail 2 s are named constants; other storage SQL keeps 600/500 ms. The 17 MiB bound stays. |
| DEC-HLLM-V52-07 | Each preview, including `dev`, gets its own generated `HARDEN_LLM_CREDENTIAL_KEY`; production's is generated once by the operator under CHECK-422. |

v5.3 readiness-review corrections accepted by Kirill on 2026-10-12. They are settled; do not ask again.

| ID | Decision |
| --- | --- |
| DEC-HLLM-V53-01 | After an unknown COMMIT, one bounded readback may confirm saved only when the row is found. Empty or failed readback remains unconfirmed because the commit can finish later. No polling, insert retry or inference replay. Add the cheap TEST-451 regression and the delayed real-commit case to TEST-442 within P03.S01–S02. |
| DEC-HLLM-V53-02 | Existing production `runApply` explicitly invokes the accepted gateway image's one-shot `gateway-schema` service and requires exit 0 before application startup. Retain `--no-deps`; do not rely on `depends_on` or add a second deployment command. TEST-450 covers ordering/failure under Node in P05.S01–S02; TEST-433 covers fresh-schema startup through real composed storage. |

The accepted record and remaining owner checks cannot replace automated behavioral acceptance. No human browser check is required or implied by this plan.

## 8. Data contract

| Record | Storage and schema snapshot | Invariants |
| --- | --- | --- |
| Git preset | Credential-free Git template: ID/provider/base URL/models/prices/default hardening options | Selecting copies one complete setup into the current draft. No catalog seeding or live inheritance. Saved snapshots retain their values after template changes/deletion. |
| Deployment configuration | Connection JSON `api_key_envs`, upstream metadata/environment names and typed shared-service settings | JSON contains names only; protected environment holds incoming/provider/service secrets. One accepted-key path; no startup key generation/rotation. |
| Saved profile | PostgreSQL: `api_key_hash`, UUID, display name, optional provenance preset ID, complete definition/inputs, explicit credential binding, creation time | Explicit Save inserts a new immutable snapshot. The access-key hash is a partition, not another owner/user entity. No raw incoming/provider key or result in snapshot JSON. |
| Entered provider credential | PostgreSQL: `api_key_hash`, credential ID, origin, encrypted payload, nonce, encryption-key ID and revision | Separate mutable secret record. Encrypt with baseline associated-data framing using hash/credential/origin; explicit replacement uses CAS and changes cache identity. No environment fallback. |
| Hidden preset preference | PostgreSQL: `api_key_hash`, preset ID and creation time | Hide affects only this key's template picker; no credential needed and no saved snapshot modified. |
| Human session | Existing Control Plane/Portal | Controls Phoenix admission only. The stable user ID appears in HLLM persistence only inside the opaque draft key chosen by the reference host. No session reference or human ID is Go REST authority. |
| Cache | PostgreSQL: `api_key_hash`, credential namespace, core operation hash, result envelope/expiry/version | Key-scoped adapter implements the existing core CacheStore. Namespace binds the resolved provider credential snapshot. No incomplete/error response treated as success; physical cleanup is bounded. |
| Draft | PostgreSQL: `api_key_hash`, host draft key, revision, full payload and update/expiry times | Autosave updates the caller's draft, not saved profiles. The reference host uses `workspace:<stable user ID>`, so concurrent Portal users of one key keep separate drafts. Same draft key in two contexts gets explicit CAS conflicts; no last-writer override. |
| Reference history | PostgreSQL `history`: `api_key_hash`, run UUID, start/finish, status, summary JSON for list/stats, safe complete request/result JSON | Only reference executions; one row per healthy run, inserted once. Every read/stat/restore/delete/clear is key-scoped. No key plaintext or truncation; rows stay until key-scoped delete/clear. |
| Recording outcome/diagnostic | Wrapper response/event | Separate inference/storage outcomes, bounded safe error/correlation fields, truthful saved/not-saved/unconfirmed/disabled states; not another history store. |
| Artifact | Existing Garage bytes at `artifacts/<api_key_hash>/<run_id>/<artifact_id>`, content type/length/digest in object metadata | Path prefix is the scope; same-key clients may download, foreign keys cannot construct the path. Works without a history row. Delete/clear remove the run prefix; orphans from unrecorded runs need manual cleanup. |
| Diagnostic spans | Shared Laminar observability via the existing sampled OTEL collector | Safe correlation/usage facts only; never product history. |

Reuse one isolated HLLM database/role on Agent Platform PostgreSQL and the existing Garage bucket. No service, project or database per HLLM access key is required. Keep preview/test data and service credentials isolated. The authorized clean cut affects obsolete HLLM-owned data only; no shared identity/product reset or old-format migration. Cross-repository consumer changes require their owner; Model Recall is inspected read-only for the interface design.

History deliberately contains prompts/results and safe resolved configuration for holders of its incoming access key. Even if two Portal users differ, the same key grants the same record access; only unsaved drafts are per user. A second key cannot access those records, stats or downloads. Restoring a record fills the complete current-key draft; it does not borrow a different scope's provider credential, match display names, save a profile or invoke inference. Missing/removed credential bindings are explicit until the caller selects an authorized binding.

### 8.1. Configuration names and validation

Existing environment names stay where applicable, and every moved or new setting uses the existing `HARDEN_LLM_*` prefix (v5.2); no `HLLM_*` setting names. Proposed typed fields become OpenAPI configuration schemas, `.env.example`, command config and deployment descriptors in P00/P05. Libraries accept typed values and never parse `.env`. Secret examples contain names only.

| Setting | Composition rule |
| --- | --- |
| `HARDEN_LLM_CONFIG_FILE` | Existing nonempty JSON file, at most 64 KiB and at most 32 upstreams. Add required `connectionFile.APIKeyEnvs []string` with JSON `api_key_envs` beside existing `default_upstream`/`upstreams`. Validate environment names with existing `validEnvironmentName`, load their values once and validate with `auth.ValidToken`. Missing array/name/value or duplicate names/resolved values is a typed startup error; no scalar fallback. Return accepted values in `serverConfig.apiKeys []string`, replacing `serverConfig.token`. No raw key in JSON. |
| `HARDEN_LLM_TOKEN` | Preserve the existing protected value and include its name in `api_key_envs`; this is the initial frontend-selected access key and deployment acceptance credential. Frontend and gateway consume the same approved shared-environment source. Other configured names select genuinely distinct keys using the same protocol, not new user identities. No key issuance/rotation at startup. |
| `CPA_API_KEY` and upstream `api_key_env` names | Provider credentials loaded at startup; preset/deployment bindings name configured upstream IDs, never arbitrary environment variables from a request. These keys do not determine application-data scope. |
| `HARDEN_LLM_DATABASE_URL`, `HARDEN_LLM_DATABASE_POOL_SIZE` | Move Phoenix's existing names (`frontend/config/runtime.exs`) to Go unchanged; the same pool serves history. One pool, default maximum 10/minimum connections 0. Invalid storage settings become feature diagnostics without blocking independent inference. Profile, draft, cache, list, count, statistics and status reads cap acquisition/transaction context at 600 ms and SQL statements at 500 ms, with no fresh budget per statement or constructor ping. History full-row operations use named constants defined once in `stateful/history.go`: insert 2 s, unknown-outcome readback 1 s and detail (`GetHistory`) 2 s, because a valid record can be 17 MiB. |
| `HARDEN_LLM_CREDENTIAL_KEY` | Required standard-base64 32-byte AES-GCM key when entered provider credentials are used, one key ID `primary`. Each preview, including `dev`, gets its own value generated once by `previewSecrets()` and kept in that preview's `secrets.json`; production's value is generated once by the operator (CHECK-422). No generated-on-restart key, alternate decryptor or old-schema import. |
| `HARDEN_LLM_CONTROL_PLANE_URL`, `HARDEN_LLM_CONTROL_PLANE_INTERNAL_TOKEN` | Existing Phoenix admission settings only. Do not supply/consume them as Go REST authentication dependencies. Portal owns passwords/session lifecycle. |
| `HARDEN_LLM_HISTORY_ENABLED` | Explicit boolean, default false; reference deployment sets true. Disabled performs no history I/O. Enabled with invalid/missing database settings reports configuration error without becoming false or preventing inference. |
| `HARDEN_LLM_ENVIRONMENT` | Existing deployment label for diagnostics. History needs no environment column because each environment has its own HLLM database. |
| `HARDEN_LLM_CACHE_TTL_SECONDS` | Positive integer, default 86400. Cache adapter binds `api_key_hash` plus a namespace derived once from the resolved provider credential snapshot and existing core CacheDomain. Never put raw credentials in keys/namespaces. Same key/credential/operation shares cache; different access keys or provider-credential changes do not. |
| Existing `HARDEN_LLM_ARTIFACT_*` endpoint/bucket/access-key/secret-key names | Restore baseline Garage client names. Authenticated same-origin streaming retires external presign/endpoint settings. Object keys follow `artifacts/<api_key_hash>/<run_id>/<artifact_id>`. Production sets them; every preview, including `dev`, leaves them unset (there is no branch Garage). All absent means artifacts are disabled: `/api/v1/status` reports the artifact feature disabled, publication is skipped without a diagnostic, trace/download routes return `artifact_not_found`, and delete/clear remove only history rows. Partially set values are a typed configuration error. No additional metadata service. |
| `HARDEN_LLM_WEB_API_TIMEOUT_MS` | Set reference transport timeout to 75000, matching the Go response bound. Keep 60-second inference budget and bounded post-call artifact publication and history insert; no ordinary `/v1` timeout increase. |

`GET /readyz` reports deployment inference readiness. Authenticated `GET /api/v1/status` reports independent storage/history/artifact feature states and safe diagnostics. When history is enabled it also returns `history:{rows,tableBytes}`: the caller key's row count (served by the `history_page` index) and `pg_total_relation_size('history')`. `tableBytes` is an aggregate deployment figure and reveals no other key's records. Both reads use the ordinary 500 ms statement cap; a failed read reports the figures unavailable, never zero. It returns no access-key list, raw credential, user/session identity or scope selector. Go's key validation and client construction are local and unaffected by Control Plane outage; Phoenix admission can fail independently through its existing owner path.

### 8.2. PostgreSQL schema and transaction rules

Create `stateful/schema.sql` in P02 for the fresh HLLM database provisioned by the shared owner. `ApplySchema(ctx,pool)` transactionally creates version 1 only when the version table is absent; matching version is a no-op and mismatch fails for operator action. P02.S15–S16 create and test the `harden-llm-gateway schema` subcommand; previews run it through the one-shot `gateway-schema` service from P02 exit, and production runs the same one-shot service, prepared in P05.S02 and applied in P06.S02. The serving process never drops/resets data or auto-converts old schemas.

Production execution has one path in existing `scripts/production-config.mjs:runApply`. For a change that selects `harden-llm-gateway`, keep the existing scope/candidate/image/freshness checks, then validate the resolved `gateway-schema` service: image reference equals the accepted gateway image, command is `["schema"]`, no build stanza, and database configuration comes from the same protected source. Align protected descriptor image overrides for both services. Inside the existing `withOverrideFile` callback, first call `buildComposeArguments(descriptor, ["run", "--rm", "--no-deps", "--pull", "never", "gateway-schema"], overridePath)` through the existing process runner and `buildComposeEnvironment`; require exit 0 through `processResultOutput`. Only then invoke the existing application `up --no-deps` command. A schema failure starts no application; no Docker dependency traversal, manual schema fallback, new CLI operation, extra pool or long-lived service is added. Frontend-only and no-change applies preserve their existing behavior and run no schema command. Preview startup continues using its existing Compose dependency path.

TEST-450 proves this production command sequence without Docker; TEST-433 separately proves the schema command against a fresh real PostgreSQL database. P06 records the exact schema command, accepted image identity and exit result as release evidence before application convergence.

```sql
CREATE TABLE hllm_schema_version (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  version integer NOT NULL CHECK (version = 1)
);
CREATE TABLE credentials (
  api_key_hash text NOT NULL CHECK (api_key_hash ~ '^[0-9a-f]{64}$'),
  id text NOT NULL,
  origin text NOT NULL,
  key_id text NOT NULL CHECK (key_id = 'primary'),
  nonce bytea NOT NULL CHECK (octet_length(nonce) = 12),
  ciphertext bytea NOT NULL,
  revision bigint NOT NULL CHECK (revision > 0),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (api_key_hash, id)
);
CREATE TABLE profiles (
  api_key_hash text NOT NULL CHECK (api_key_hash ~ '^[0-9a-f]{64}$'),
  id uuid NOT NULL,
  name text NOT NULL,
  preset_id text,
  definition jsonb NOT NULL CHECK (jsonb_typeof(definition) = 'object'),
  inputs jsonb NOT NULL CHECK (jsonb_typeof(inputs) = 'object'),
  credential_source text NOT NULL CHECK (credential_source IN ('deployment', 'entered')),
  deployment_upstream_id text,
  entered_credential_id text,
  created_at timestamptz NOT NULL,
  PRIMARY KEY (api_key_hash, id),
  FOREIGN KEY (api_key_hash, entered_credential_id) REFERENCES credentials (api_key_hash, id),
  CHECK ((credential_source = 'deployment' AND deployment_upstream_id IS NOT NULL AND entered_credential_id IS NULL)
      OR (credential_source = 'entered' AND entered_credential_id IS NOT NULL AND deployment_upstream_id IS NULL))
);
CREATE TABLE hidden_presets (
  api_key_hash text NOT NULL CHECK (api_key_hash ~ '^[0-9a-f]{64}$'),
  preset_id text NOT NULL,
  created_at timestamptz NOT NULL,
  PRIMARY KEY (api_key_hash, preset_id)
);
CREATE TABLE drafts (
  api_key_hash text NOT NULL CHECK (api_key_hash ~ '^[0-9a-f]{64}$'),
  draft_key text NOT NULL,
  revision bigint NOT NULL CHECK (revision > 0),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  updated_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  PRIMARY KEY (api_key_hash, draft_key)
);
CREATE TABLE cache_entries (
  api_key_hash text NOT NULL CHECK (api_key_hash ~ '^[0-9a-f]{64}$'),
  namespace text NOT NULL,
  operation_hash text NOT NULL,
  schema_version integer NOT NULL,
  record jsonb NOT NULL CHECK (jsonb_typeof(record) = 'object'),
  expires_at timestamptz NOT NULL,
  PRIMARY KEY (api_key_hash, namespace, operation_hash)
);
CREATE INDEX cache_entries_expiry ON cache_entries (api_key_hash, expires_at);
CREATE INDEX drafts_expiry ON drafts (api_key_hash, expires_at);
CREATE TABLE history (
  api_key_hash text NOT NULL CHECK (api_key_hash ~ '^[0-9a-f]{64}$'),
  run_id uuid NOT NULL,
  schema_version integer NOT NULL CHECK (schema_version = 1),
  status text NOT NULL CHECK (status IN ('succeeded', 'failed', 'timeout')),
  started_at timestamptz NOT NULL,
  finished_at timestamptz NOT NULL CHECK (finished_at >= started_at),
  summary jsonb NOT NULL CHECK (jsonb_typeof(summary) = 'object'),
  request json NOT NULL CHECK (json_typeof(request) = 'object'),
  result json NOT NULL CHECK (json_typeof(result) = 'object'),
  PRIMARY KEY (api_key_hash, run_id)
);
CREATE INDEX history_page ON history (api_key_hash, started_at DESC, run_id DESC);
```

- Bind every SQL parameter and include `api_key_hash` in reads/writes/FKs. Only bearer middleware derives public request scope. Unknown foreign-key IDs behave as not found. No owner/user/key registry table or different per-key database exists.
- Save allocates UUID/time once after candidate validation/probe, constructs the full detached setup and inserts with its explicit credential binding in one short transaction. Concurrent explicit saves create distinct IDs; no update/upsert/revision for saved profile settings. Generate display name from supplied base name or selected template label, UTC timestamp and first six UUID hexadecimal characters. Apply the baseline 1500-byte name bound to the final generated name and report a field error rather than truncating. Do not derive it from prompts/secret content or use it as identity. Preset provenance has no Git FK and cannot change a saved setup.
- Credential replacement and draft autosave use the existing CAS pattern with hash/ID/expected revision, incrementing only their mutable records. Create expects revision 0. No match returns revision_conflict; never silently overwrite another same-key client's draft. Explicit Save snapshots the submitted current form, not a stale server-global copy. Selecting a saved profile, history restore or model refresh changes the draft only.
- A credential supplied on Save includes its current ID/revision or an explicit new credential, normalized origin and encrypted secret payload. Save key plus profile atomically; any failure rolls back both. AES-GCM associated data keeps the baseline unambiguous framing with API-key hash/credential/origin as the binding values. Fresh crypto nonce per encryption. Changing origin requires a compatible explicit credential; no copied environment key.
- Resolve definition/inputs/recovery profiles/credential bindings once in a read-only repeatable-read transaction; release it before provider/Garage I/O. Load saved definitions directly, without Git merge. Deleted/missing Git templates affect starting-template selection only; old snapshots still work with valid credentials. Model refresh returns a changed safe draft. It cannot update the old profile or save implicitly.
- Profile definition/bundle version becomes 4. Identity is the outer snapshot/template ID and display name; remove the embedded v3 `llmProfile` field rather than storing a second identity. Move catalog/ID validation with that outer ID and keep the provider-option/model/pricing/recovery validation once in the moved package. Remove retired endpointCredentialScope, replaced by the explicit binding. Save/detail `definition` and `inputs` use one shared schema also reused by history request snapshots. Reject unknown fields and forbidden scope/secret/result values; encoded complete snapshot/draft is at most 256 KiB. List picker summaries omit prompts/schema/complete inputs; `GetProfile` loads those one at a time within existing response caps.
- `ProfileBundle` version 4 includes schemaVersion/bundleId/createdAt/apiKeyHash, complete snapshots, hidden preferences and encrypted entered credentials. The bundle hash must equal the authenticated scope; it cannot select scope. Import is an explicit atomic catalog replacement for that scope, retaining the baseline workflow, separate from ordinary create-on-save. Require same encryption key and current format; no deployment/incoming plaintext key, resolved Git catalog, cross-key rebind or old-format conversion. Candidate probes happen before transaction; other scopes remain unchanged.
- `PostgresCache` binds one hash/credential namespace and implements existing CacheStore Get/Set/Delete. Namespace derives from a deterministic hash of the resolved generation/repair/rerun connection/origin/credential fingerprints and revisions, plus existing CacheDomain; provider key changes cannot reuse prior credential-scoped rows. Fingerprint actual loaded provider keys once, including environment-backed ones; never store raw key text in the namespace or expose it as an authority. Credentials/recovery targets are detached before execution, with no per-attempt storage/identity lookup.
- Get excludes expired rows, Set upserts a complete validated envelope and Delete removes only the bound hash/namespace/operation. The owning cache write deletes at most 100 expired rows across that access key's namespaces using the expiry index. Draft writes do the same for their key's expired drafts. Cleanup preserves live/other-key rows and creates no worker/scheduler. Dormant or removed-key expired rows wait for explicit operator cleanup; no claim of automatic physical expiry without writes.
- Draft key is host-supplied, up to 128 UTF-8 bytes. The restored reference host uses `workspace:<stable Control Plane user ID>`, so each Portal user has a separate draft inside the shared key scope. Preserve seven-day expiry and 256 KiB encoded payload bound. Two contexts with the same access key and the same draft key share it and encounter explicit revision conflicts.
- `history.summary` holds the list projection and accounting numbers used by statistics (profile/model/status/result source/cache served, result and provider token counts, known cost, known/unknown cost observations, duration); `request` and `result` hold the complete safe record as PostgreSQL `json`, which preserves the validated encoding without JSONB rewriting or full-payload normalization. `summary` stays JSONB; no query filters the opaque payloads. List and count read `summary` only; detail reads the full row. The encoded `request`+`result` is at most 17 MiB; larger records are rejected as `not_saved`, never truncated.
- One insert per run inside a short transaction after artifact publication; no update path. Delete/clear are key-scoped `DELETE ... WHERE api_key_hash = $1 AND run_id = ANY($2)` after the runs' Garage prefixes are removed. No history expiry, tombstone or background job.
- No users/sessions/incoming_keys/history_tombstones/outbox/span table or automatic incoming-key transfer exists. New access key means new hash/scope; old HLLM data is removed only through its selected key's explicit operations or reviewed HLLM-only operator cleanup.

### 8.3. History record, recording and artifacts

`HistoryRecord` schema version 1 is a complete terminal reference execution: `apiKeyHash` (server-derived, never returned to clients), `runId` (UUID), `startedAt`, `finishedAt`, `status`, `request` (safe original form and resolved configuration snapshots), `result` (baseline `RunOutput` excluding recording state), and nullable typed `error`. The result includes original attempts, result/provider accounting, cache facts, trace correlation and artifact metadata. Never persist API keys, authorization headers, session references, decrypted credentials or a serialized whole runtime/HTTP request. Do not redact arbitrary prompt text by heuristic; prompts/results are the intended history content for holders of that HLLM access key.

Create the run ID after authentication and before validation. Authentication failures are not history entries. Validated application errors with no provider dispatch are terminal reference records when history is enabled.

`stateful/sql/history_list.sql` uses the baseline numbered-page query shape. One server-owned key/window filter supplies list/count/stats/clear; a named Go builder adds only the optional bound date predicate, never caller SQL or duplicated filters:

```sql
SELECT run_id, status, started_at, finished_at, summary
FROM history
WHERE api_key_hash = $1
  AND started_at < $2
  AND ($3::timestamptz IS NULL OR started_at >= $3)
ORDER BY started_at DESC, run_id DESC
LIMIT $4 OFFSET $5
```

- `HistoryQuery` fields are `From`, `Before`, `Page`, `PageSize`. Default page 1/page size 25; allowed sizes 10/25/50/100 retain baseline controls. First page fixes `Before=clock.Now().UTC()` and navigation carries it. Validate positive page/integer-safe `(page-1)*pageSize`; count and list use identical filters and existing query deadlines. Return `{items,pagination:{page,pageSize,totalCount},window,history}`. No cursor compatibility path or UI navigation redesign.
- `GetHistory` selects by key hash and run UUID without a window. Validate schema version and complete payload. A missing or foreign run returns `history_not_found`; malformed content returns `history_record_invalid`. A failed query returns unavailable, never an empty list.
- `Statistics` applies the identical key/window filter and sums `summary` fields: provider usage/cost for actual spend, result accounting for cached output displays, known subtotal plus unknown-observation counts. Any missing cost leaves the total partial/unknown. Publish window and `scope:"available_records"`; missing records are not zero-cost calls.
- Recording: after the terminal result and artifact publication, insert once with a detached context capped at two seconds (caller cancellation dropped, correlation kept). Commit success means `saved`. An error before COMMIT is sent (pool unavailable, constraint/oversize, statement error) means `not_saved`. A connection loss or timeout after COMMIT is sent triggers one read by run ID on a fresh connection within one more second: found means `saved`; absent or failed readback means `unconfirmed`. The empty query can race a delayed commit and is not proof of rollback. Never poll, re-insert or rerun inference. For SSE, emit the inference result promptly, then the recording outcome; JSON returns one envelope within the 75-second reference transport timeout, retaining the 60-second inference budget.
- A process death before the insert leaves no history record. There is no background reconciliation or local spool.

Delete loads the scoped row, deletes every object under `artifacts/<api_key_hash>/<run_id>/`, then deletes the row. Already-missing objects count as removed. If an object delete fails, keep the row, return `artifact_cleanup_failed` with the run ID, and let the user retry; the operation is idempotent. Any holder of the validated key may delete/clear only matching-hash rows. Clear freezes a cutoff, then repeatedly selects up to 100 run IDs before the cutoff for that key, deletes their prefixes and rows, and stops at the first failure. Return `{deleted,cutoff,complete}` plus the first failed run ID. Runs started after the cutoff are untouched. A history deletion does not erase diagnostic spans in Laminar.

Artifacts: `publishArtifacts` writes the baseline trace projection (`trace.json`), parse-failure evidence and support bundle under `artifacts/<api_key_hash>/<run_id>/<artifact_id>`, with content type, length and SHA-256 digest in object metadata, within the baseline two-second bound. `OpenArtifact` and `GET /api/v1/traces/{runID}` build the key from the authenticated hash and validated UUID/artifact ID, verify metadata and stream bytes; they do not need a history row. Runs with history disabled, not saved or unconfirmed keep their objects; cleanup of those orphans is manual (delete a key or run prefix). Do not delete objects after an unconfirmed insert. No PostgreSQL artifact journal, reconciler, lifecycle rule or second metadata store.

### 8.4. Reference REST schema and events

Canonical definitions go in `api/openapi.yaml` before implementation; Go DTO JSON names and the package client use them. Preserve the baseline field names below. Breaking ownership/version changes are deliberate; no aliases for retired endpoints or wire schemas.

| Method/path | Request and response |
| --- | --- |
| `GET /api/v1/status` | Feature states, sanitized diagnostics and, when history is enabled, `history:{rows,tableBytes}` (section 8.1); no secret settings. |
| `GET /api/v1/profiles` | `{profiles:[ProfileView]}` listing credential-free templates and selected-key saved picker summaries, excluding key-hidden templates. Summary omits full prompts/schema/inputs; kind is preset or saved. |
| `POST /api/v1/profiles` | Complete `SaveProfileInput` → 201 `{profile:ProfileView}` with new server UUID/name/time. No PUT/PATCH update of saved settings or caller-supplied profile revision. |
| `GET /api/v1/profiles/{profileID}` | Full safe `ProfileDraft` for selected-key saved profile or visible Git template; loading fills draft and never saves/runs. |
| `DELETE /api/v1/profiles/{profileID}` | Delete selected-key saved snapshot or hide its Git template → 204; other-key/unknown saved IDs return 404. Credential/draft CAS is separate from immutable profiles. |
| `POST /api/v1/profiles/{profileID}/models:refresh` | Resolve selected-key binding/discover models → full changed `ProfileDraft`. No generation or implicit profile/draft storage; host queues its normal draft save, explicit Save creates a profile. |
| `GET /api/v1/profiles/bundle`, `PUT /api/v1/profiles/bundle` | Export current encrypted bundle; replace with one validated bundle → `{profiles:[ProfileView]}`. Literal bundle route registered explicitly before variable profile handling. |
| `GET /api/v1/state?draftKey=...`, `POST /api/v1/state` | `{draftKey,revision,payload}`; draft key is host-supplied (reference host: `workspace:<stable user ID>`). POST requires expected revision, returns new revision. Missing/expired draft returns revision 0 and empty payload, not storage-unavailable success. |
| `POST /api/v1/run` | Baseline `RunInput`; JSON response `{result:RunOutput,state:RunState}` or the single SSE variant selected by Accept header. |
| `GET /api/v1/history` | Optional window/page/pageSize → `{items,pagination:{page,pageSize,totalCount},window,history}` for the authenticated hash; disabled/config/read failure is not empty success. |
| `GET /api/v1/history/{runID}`, `DELETE /api/v1/history/{runID}` | Full safe record for restore; deletion → 204 after the run's artifact prefix and row are removed. |
| `DELETE /api/v1/history` | Clear the authenticated key's records before a frozen cutoff → `{deleted,cutoff,complete}`. Partial failure includes typed error and the first affected run ID; runs after the cutoff remain. |
| `GET /api/v1/stats` | Same window as history → counts, core-ledger aggregates, unknown observations and `scope:"available_records"`. |
| `GET /api/v1/traces/{runID}` | Authenticated stream of `trace.json` from `artifacts/<api_key_hash>/<runID>/`; no history row required. |
| `GET /api/v1/traces/{runID}/artifacts/{artifactID}` | Authenticated stream of that run's artifact, with content type/length/disposition from validated Garage object metadata. |

`RunInput` keeps `profileId`, optional `modelId`, `systemPrompt`, `userPrompt`, `callType`, `schema`, `reasoningEffort`, `webSearch`, `providerOptions`, `cacheMode`, `cacheVersion`, `timeoutMs`, `recoveryPolicy`, `origin`. Reject caller `ownerId`, `userId`, `apiKeyHash`, credentials, session references and environment/history selectors; scope is always server-derived. Use the same rejection rule for draft/snapshot data; bundle hash is a compared stored value, never authority. Preserve current inference/request/response/diagnostic limits; bundle import retains the baseline 2 MiB limit from `internal/gateway/httpapi/resources.go`; artifact downloads are streamed and retain validated object metadata/size limits. Do not apply the 256 KiB inference request limit to either operation.

`RunOutput` keeps baseline `schemaVersion`, `runId`, `status`, `output`, `callId`, `traceId`, `origin`, `selectedTarget`, `generationTarget`, `resultSource`, `accounting`, `attempts`, `cache`, `artifacts`, `providerInvoked`, duration/wait/over-budget fields, `usedRepair`, `diagnostics`, `stopReason`, `search`; add `history:RecordingOutcome`. `RunState` keeps `lastRunId`, `lastTraceId`. Store neither `RecordingOutcome` nor a self-referential record inside history. On key-scoped restore, copy the complete recorded safe definition/inputs into the current draft without profile-name matching. A missing/deleted same-key credential binding stays unresolved with an explicit selection message; never borrow a foreign binding. Restore does not save or run; Save creates a fresh snapshot.

P03 representation details: an application error before target resolution has `selectedTarget:null`, a null output, and local call/trace correlation IDs; successful results still require a complete target. `api/contract.go` embeds and loads the canonical local OpenAPI schemas once, without I/O, so history insertion and restore validate full request/result payloads through one contract rather than a second handwritten schema. History timestamps and their JSON summary use PostgreSQL's microsecond precision. The wrapper detaches call inputs before execution and excludes structurally supplied credential fields from history without altering prompt text.

`RecordingOutcome` is `{mode:"disabled"|"enabled", status:"disabled"|"configuration_error"|"saved"|"not_saved"|"unconfirmed", runId, diagnostic?}`. Query errors use a separate error envelope; a failed read does not change a recorded inference outcome. `unconfirmed` means the commit outcome was unknown and the single readback was absent or failed. It does not assert that no row exists or will appear later.

A diagnostic is `{code, component, operation, requestId, runId?, traceId?, inferenceOutcome, persistenceOutcome, cause, durationMs, message, action}`. `cause` is a bounded enum (`configuration`, `authentication`, `connection`, `timeout`, `schema`, `rejected`, `unknown`), not the raw driver error. `message` and `action` each have a 512-byte limit; aggregate diagnostics retain the existing 64 KiB cap. Logs use these same fields with severity, never a second error projection.

| Code family | HTTP/use semantics |
| --- | --- |
| `unauthorized`, `invalid_api_key` | 401 before storage/provider work; one bearer boundary. |
| `storage_unavailable`, `reference_configuration_error` | 503 on the affected reference operation; configured cache-off inference remains independent. No identity-authority error family in Go REST. |
| `profile_invalid`, `credential_invalid`, `draft_invalid` | 400 with safe `fieldErrors`; secret input never echoed. |
| `scope_selector_rejected` | 400 when query or body carries `apiKeyHash`, `owner`, `userId`, `sessionReference` or another scope selector; checked before any storage or provider work. |
| `profile_not_found`, `preset_not_found`, `history_not_found`, `artifact_not_found` | 404 for absent/foreign scope. Missing Git template does not invalidate saved snapshots; no default substitution. |
| `revision_conflict` | 409 with current revision only; user explicitly reloads/reapplies edits. |
| `history_configuration_error`, `history_write_rejected`, `history_write_unconfirmed`, `history_read_failed`, `history_record_invalid`, `artifact_cleanup_failed` | Recording diagnostic preserves successful inference; standalone failed history operations use 502/503, invalid stored schema uses 502. Do not turn a model success into an inference error. |

Reference SSE preserves baseline `run.progress`, `run.completed`, `run.failed` events. Emit exactly one terminal inference event with `{result,state}`. Add `history.recorded` afterward with `RecordingOutcome`, then close; include `runId` and monotonic sequence on every event. The client considers inference terminal separately from recording completion. A disconnect never authorizes inference retry. If the HTTP response ends before a recording event, display recording unconfirmed and allow a history read. Plain `/v1` SSE and its final-only OpenAI contract are unchanged.

### 8.5. Concrete fixture matrix

Create one shared `test/fixtures/reference/` directory in P00; Go validates JSON against OpenAPI and Elixir reads those same files through a test-only path. Fixtures use UTC clock `2026-10-10T12:00:00Z`, UUIDs with the `00000000-0000-4000-8000-` prefix, synthetic HLLM keys A/B with fixed SHA-256 expected hashes verified independently of the implementation helper, two Portal identities for frontend admission and per-user draft-key assertions, local httptest provider origins and incoming/provider-key/session leak sentinels. No real credentials or retained live payloads. TEST-425 asserts the helper matches fixed hashes; it cannot generate its own expected value through the helper under test.

| Fixture file | Required assertions |
| --- | --- |
| `access-keys.json` | Fixed key A/B values and independent expected SHA-256 hashes; accepted/missing/invalid/fingerprint-only bearer; same-key/two-client context and foreign-key denial. |
| `presets.json` | CPA response/chat and second-provider repair presets; explicit zero and absent pricing; no keys. |
| `profile-snapshots.json` | Full prompt/schema/provider/repair/cache/timeout values; false/zero/empty preservation; template changed/deleted after save; new UUID/date/suffix; repeated Save; hidden templates and cross-key binding denial. |
| `recovery-cross-profile.json` | Generation A → malformed JSON → repair B, repair escalation and rerun; exact target credentials, bounded attempts and ledger; missing B fails before A dispatch. |
| `run-repaired.json`, `run-cache-hit.json`, `run-validation-error.json` | Complete baseline result envelope; preserve original successful output after repaired errors, zero dispatched provider calls on hit/validation, correct result/provider ledgers. |
| `history-outcomes.json` | Every recording state (disabled, configuration error, saved, not saved, unconfirmed) plus empty success, query failure, oversize, unknown commit with positive/empty/failed readback, a delayed commit appearing after empty readback and process-cancel behavior. Provider, insert and readback counts are exact. |
| `history-pages.json` | Equal timestamps/distinct UUIDs, numbered navigation/counts/page sizes/fixed cutoff, deleting rows and batched clear before cutoff; same-key sharing and cross-key list/detail/stats denial. |
| `reference-errors.json` | All error families with correlation/timing/action, synthetic secret-bearing underlying errors; no sentinel reaches response/log/history. |
| `profile-bundle.json` | Current complete encrypted same-hash bundle; different access hash/encryption key/version, tamper, atomic replacement rollback, hidden preferences and unavailable deployment binding. |
| `artifact-manifest.json` | Digest/media/size, object keys `artifacts/<api_key_hash>/<run_id>/<artifact_id>`, authorized bytes, foreign/unknown artifact, partial prefix cleanup and absent object; no arbitrary Garage key supplied by user. |

Generate ciphertext deterministically only in encryption tests using an injected byte reader; production always uses crypto randomness. Tests that measure transaction/concurrency behavior use real leased PostgreSQL/Garage, not a fake store. History and artifact behavior is proven against real leased PostgreSQL/Garage (TEST-426/427/442/443); no fake store replaces them.

## 9. Reproducibility

- Record source SHA, branch, OS/architecture, tool versions, dependency locks, image IDs, test namespace, seeds and commands with each phase report.
- Planning reference: Linux/amd64, Go 1.26.6 from `go.mod`; Elixir 1.20.2 and OTP 28.4.3 from repository host guidance; Node version from the current CI/toolchain declaration. Capture actual versions with `go version`, `node --version`, `elixir --version`, `docker version` when their tracks run. No GPU is required.
- Reuse pinned image identities in `Dockerfile`, `frontend/Dockerfile`, `deploy/images.lock.json`, integration Compose and shared-owner locks. Agent Platform's source uses PostgreSQL 16.4; do not substitute the retired dedicated frontend PostgreSQL 17.9 as production authority. Verify actual shared version before schema acceptance.
- Preserve `go.sum`, `frontend/mix.lock` and asset dependency locks. A fresh worktree may require the existing `mix deps.get` setup before offline execution; dependency download is not test evidence.
- Integration uses existing leased resources, unique namespaces and bounded teardown. No arbitrary sleeps or global serialization to conceal races. Named unavoidable global-resource exceptions require the existing machine-checked rationale.
- Environment: existing `HARDEN_LLM_TOKEN` selected frontend/acceptance key and all protected values named by required connection `api_key_envs`, `CPA_API_KEY`, `HARDEN_LLM_CONFIG_FILE`, Go PostgreSQL/credential-encryption/history settings, Phoenix-only Control Plane admission settings, authorized private-module build token and narrow probe variables. JSON/examples contain environment names only. Preserve deployment keys and saved snapshots across restart; no secret printing, scalar/session fallback or environment-to-database synchronization.
- Test runtimes are estimates on the reference host, not universal limits. Resource comparisons record available CPU/RAM and competing workloads. Statistical reports include sample count; single-run categorical checks use exact counts and mark confidence intervals not applicable.

## 10. Requirements Traceability Matrix

| Phase | REQ-### | TEST-### | Test Path | Command |
| --- | --- | --- | --- | --- |
| P00 | REQ-420 | TEST-420 | `internal/testkit/reference_decoupling_contract_test.go` | `go test ./internal/testkit -run '^TestReferenceDecouplingContract$' -count=1` |
| P00 | REQ-425 | TEST-420 | `internal/testkit/reference_decoupling_contract_test.go` | `go test ./internal/testkit -run '^TestReferenceDecouplingContract$' -count=1` |
| P00 | REQ-432 | TEST-420 | `internal/testkit/reference_decoupling_contract_test.go` | `go test ./internal/testkit -run '^TestReferenceDecouplingContract$' -count=1` |
| P00 | REQ-433 | TEST-420 | `internal/testkit/reference_decoupling_contract_test.go` | `go test ./internal/testkit -run '^TestReferenceDecouplingContract$' -count=1` |
| P00 | REQ-435 | TEST-420 | `internal/testkit/reference_decoupling_contract_test.go` | `go test ./internal/testkit -run '^TestReferenceDecouplingContract$' -count=1` |
| P00 | REQ-426 | TEST-436 | `internal/testkit/reference_history_contract_test.go` | `go test ./internal/testkit -run '^TestReferenceHistoryContract$' -count=1` |
| P00 | REQ-432 | TEST-436 | `internal/testkit/reference_history_contract_test.go` | `go test ./internal/testkit -run '^TestReferenceHistoryContract$' -count=1` |
| P01 | REQ-421 | TEST-421 | `stateless_contract_test.go` | `go test . -run '^TestStatelessContract$' -count=1` |
| P01 | REQ-424 | TEST-421 | `stateless_contract_test.go` | `go test . -run '^TestStatelessContract$' -count=1` |
| P01 | REQ-431 | TEST-421 | `stateless_contract_test.go` | `go test . -run '^TestStatelessContract$' -count=1` |
| P01 | REQ-432 | TEST-421 | `stateless_contract_test.go` | `go test . -run '^TestStatelessContract$' -count=1` |
| P01 | REQ-422 | TEST-422 | `internal/gateway/httpapi/openai_contract_test.go` | `go test ./internal/gateway/httpapi -run '^TestOpenAIContract' -count=1` |
| P01 | REQ-431 | TEST-422 | `internal/gateway/httpapi/openai_contract_test.go` | `go test ./internal/gateway/httpapi -run '^TestOpenAIContract' -count=1` |
| P01 | REQ-432 | TEST-422 | `internal/gateway/httpapi/openai_contract_test.go` | `go test ./internal/gateway/httpapi -run '^TestOpenAIContract' -count=1` |
| P01 | REQ-421 | TEST-437 | `recovery_snapshot_test.go` | `go test . -run '^TestRecoverySnapshot$' -count=1` |
| P01 | REQ-423 | TEST-437 | `recovery_snapshot_test.go` | `go test . -run '^TestRecoverySnapshot$' -count=1` |
| P01 | REQ-429 | TEST-437 | `recovery_snapshot_test.go` | `go test . -run '^TestRecoverySnapshot$' -count=1` |
| P01 | REQ-432 | TEST-437 | `recovery_snapshot_test.go` | `go test . -run '^TestRecoverySnapshot$' -count=1` |
| P01 | REQ-421 | TEST-445 | `stateful/client_test.go` | `go test ./stateful -run '^TestClientComposition$' -count=1` |
| P01 | REQ-431 | TEST-445 | `stateful/client_test.go` | `go test ./stateful -run '^TestClientComposition$' -count=1` |
| P01 | REQ-432 | TEST-445 | `stateful/client_test.go` | `go test ./stateful -run '^TestClientComposition$' -count=1` |
| P01 | REQ-435 | TEST-445 | `stateful/client_test.go` | `go test ./stateful -run '^TestClientComposition$' -count=1` |
| P02 | REQ-422 | TEST-422 | `internal/gateway/httpapi/openai_contract_test.go` | `go test ./internal/gateway/httpapi -run '^TestOpenAIContract' -count=1` |
| P02 | REQ-423 | TEST-423 | `stateful/profile_test.go` | `go test ./stateful -run '^TestProfileContract$' -count=1` |
| P02 | REQ-427 | TEST-423 | `stateful/profile_test.go` | `go test ./stateful -run '^TestProfileContract$' -count=1` |
| P02 | REQ-432 | TEST-423 | `stateful/profile_test.go` | `go test ./stateful -run '^TestProfileContract$' -count=1` |
| P02 | REQ-423 | TEST-424 | `stateful/storage_integration_test.go` | `go test -tags=integration ./stateful -run '^TestReferenceStorage$' -count=1` |
| P02 | REQ-424 | TEST-424 | `stateful/storage_integration_test.go` | `go test -tags=integration ./stateful -run '^TestReferenceStorage$' -count=1` |
| P02 | REQ-427 | TEST-424 | `stateful/storage_integration_test.go` | `go test -tags=integration ./stateful -run '^TestReferenceStorage$' -count=1` |
| P02 | REQ-431 | TEST-424 | `stateful/storage_integration_test.go` | `go test -tags=integration ./stateful -run '^TestReferenceStorage$' -count=1` |
| P02 | REQ-432 | TEST-424 | `stateful/storage_integration_test.go` | `go test -tags=integration ./stateful -run '^TestReferenceStorage$' -count=1` |
| P02 | REQ-425 | TEST-425 | `internal/gateway/httpapi/reference_access_test.go` | `go test ./internal/gateway/httpapi -run '^TestReferenceAccess' -count=1` |
| P02 | REQ-427 | TEST-425 | `internal/gateway/httpapi/reference_access_test.go` | `go test ./internal/gateway/httpapi -run '^TestReferenceAccess' -count=1` |
| P02 | REQ-432 | TEST-425 | `internal/gateway/httpapi/reference_access_test.go` | `go test ./internal/gateway/httpapi -run '^TestReferenceAccess' -count=1` |
| P02 | REQ-423 | TEST-438 | `stateful/profile_integration_test.go` | `go test -tags=integration ./stateful -run '^TestProfilePersistence$' -count=1` |
| P02 | REQ-427 | TEST-438 | `stateful/profile_integration_test.go` | `go test -tags=integration ./stateful -run '^TestProfilePersistence$' -count=1` |
| P02 | REQ-432 | TEST-438 | `stateful/profile_integration_test.go` | `go test -tags=integration ./stateful -run '^TestProfilePersistence$' -count=1` |
| P02 | REQ-424 | TEST-439 | `stateful/cache_draft_integration_test.go` | `go test -tags=integration ./stateful -run '^TestCacheDraftContract$' -count=1` |
| P02 | REQ-427 | TEST-439 | `stateful/cache_draft_integration_test.go` | `go test -tags=integration ./stateful -run '^TestCacheDraftContract$' -count=1` |
| P02 | REQ-431 | TEST-439 | `stateful/cache_draft_integration_test.go` | `go test -tags=integration ./stateful -run '^TestCacheDraftContract$' -count=1` |
| P02 | REQ-420 | TEST-440 | `stateful/profile_resources_integration_test.go` | `go test -tags=integration ./stateful -run '^TestProfileResources$' -count=1` |
| P02 | REQ-423 | TEST-440 | `stateful/profile_resources_integration_test.go` | `go test -tags=integration ./stateful -run '^TestProfileResources$' -count=1` |
| P02 | REQ-427 | TEST-440 | `stateful/profile_resources_integration_test.go` | `go test -tags=integration ./stateful -run '^TestProfileResources$' -count=1` |
| P02 | REQ-432 | TEST-440 | `stateful/profile_resources_integration_test.go` | `go test -tags=integration ./stateful -run '^TestProfileResources$' -count=1` |
| P02 | REQ-423 | TEST-444 | `stateful/credential_test.go` | `go test ./stateful -run '^TestCredentialContract$' -count=1` |
| P02 | REQ-427 | TEST-444 | `stateful/credential_test.go` | `go test ./stateful -run '^TestCredentialContract$' -count=1` |
| P02 | REQ-432 | TEST-444 | `stateful/credential_test.go` | `go test ./stateful -run '^TestCredentialContract$' -count=1` |
| P02 | REQ-424 | TEST-449 | `cmd/harden-llm-gateway/reference_config_test.go` | `go test ./cmd/harden-llm-gateway -run '^TestReferenceConfig$' -count=1` |
| P02 | REQ-430 | TEST-449 | `cmd/harden-llm-gateway/reference_config_test.go` | `go test ./cmd/harden-llm-gateway -run '^TestReferenceConfig$' -count=1` |
| P02 | REQ-431 | TEST-449 | `cmd/harden-llm-gateway/reference_config_test.go` | `go test ./cmd/harden-llm-gateway -run '^TestReferenceConfig$' -count=1` |
| P02 | REQ-432 | TEST-449 | `cmd/harden-llm-gateway/reference_config_test.go` | `go test ./cmd/harden-llm-gateway -run '^TestReferenceConfig$' -count=1` |
| P03 | REQ-426 | TEST-426 | `stateful/history_integration_test.go` | `go test -tags=integration ./stateful -run '^TestReferenceHistory$' -count=1` |
| P03 | REQ-429 | TEST-426 | `stateful/history_integration_test.go` | `go test -tags=integration ./stateful -run '^TestReferenceHistory$' -count=1` |
| P03 | REQ-431 | TEST-426 | `stateful/history_integration_test.go` | `go test -tags=integration ./stateful -run '^TestReferenceHistory$' -count=1` |
| P03 | REQ-432 | TEST-426 | `stateful/history_integration_test.go` | `go test -tags=integration ./stateful -run '^TestReferenceHistory$' -count=1` |
| P03 | REQ-426 | TEST-427 | `stateful/history_queries_integration_test.go` | `go test -tags=integration ./stateful -run '^TestHistoryQueries$' -count=1` |
| P03 | REQ-429 | TEST-427 | `stateful/history_queries_integration_test.go` | `go test -tags=integration ./stateful -run '^TestHistoryQueries$' -count=1` |
| P03 | REQ-427 | TEST-427 | `stateful/history_queries_integration_test.go` | `go test -tags=integration ./stateful -run '^TestHistoryQueries$' -count=1` |
| P03 | REQ-431 | TEST-427 | `stateful/history_queries_integration_test.go` | `go test -tags=integration ./stateful -run '^TestHistoryQueries$' -count=1` |
| P03 | REQ-432 | TEST-427 | `stateful/history_queries_integration_test.go` | `go test -tags=integration ./stateful -run '^TestHistoryQueries$' -count=1` |
| P03 | REQ-420 | TEST-428 | `frontend/test/harden_llm_web/live/restored_workflow_test.exs` | `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_workflow_test.exs` |
| P03 | REQ-426 | TEST-428 | `frontend/test/harden_llm_web/live/restored_workflow_test.exs` | `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_workflow_test.exs` |
| P03 | REQ-427 | TEST-428 | `frontend/test/harden_llm_web/live/restored_workflow_test.exs` | `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_workflow_test.exs` |
| P03 | REQ-429 | TEST-428 | `frontend/test/harden_llm_web/live/restored_workflow_test.exs` | `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_workflow_test.exs` |
| P03 | REQ-424 | TEST-439 | `stateful/cache_draft_integration_test.go` | `go test -tags=integration ./stateful -run '^TestCacheDraftContract$' -count=1` |
| P03 | REQ-427 | TEST-439 | `stateful/cache_draft_integration_test.go` | `go test -tags=integration ./stateful -run '^TestCacheDraftContract$' -count=1` |
| P03 | REQ-431 | TEST-439 | `stateful/cache_draft_integration_test.go` | `go test -tags=integration ./stateful -run '^TestCacheDraftContract$' -count=1` |
| P03 | REQ-422 | TEST-441 | `internal/gateway/httpapi/reference_contract_test.go` | `go test -tags=integration ./internal/gateway/httpapi -run '^TestReferenceContract$' -count=1` |
| P03 | REQ-425 | TEST-441 | `internal/gateway/httpapi/reference_contract_test.go` | `go test -tags=integration ./internal/gateway/httpapi -run '^TestReferenceContract$' -count=1` |
| P03 | REQ-426 | TEST-441 | `internal/gateway/httpapi/reference_contract_test.go` | `go test -tags=integration ./internal/gateway/httpapi -run '^TestReferenceContract$' -count=1` |
| P03 | REQ-429 | TEST-441 | `internal/gateway/httpapi/reference_contract_test.go` | `go test -tags=integration ./internal/gateway/httpapi -run '^TestReferenceContract$' -count=1` |
| P03 | REQ-432 | TEST-441 | `internal/gateway/httpapi/reference_contract_test.go` | `go test -tags=integration ./internal/gateway/httpapi -run '^TestReferenceContract$' -count=1` |
| P03 | REQ-426 | TEST-442 | `stateful/history_store_integration_test.go` | `go test -tags=integration ./stateful -run '^TestHistoryStore$' -count=1` |
| P03 | REQ-427 | TEST-442 | `stateful/history_store_integration_test.go` | `go test -tags=integration ./stateful -run '^TestHistoryStore$' -count=1` |
| P03 | REQ-429 | TEST-442 | `stateful/history_store_integration_test.go` | `go test -tags=integration ./stateful -run '^TestHistoryStore$' -count=1` |
| P03 | REQ-431 | TEST-442 | `stateful/history_store_integration_test.go` | `go test -tags=integration ./stateful -run '^TestHistoryStore$' -count=1` |
| P03 | REQ-432 | TEST-442 | `stateful/history_store_integration_test.go` | `go test -tags=integration ./stateful -run '^TestHistoryStore$' -count=1` |
| P03 | REQ-426 | TEST-451 | `stateful/history_test.go` | `go test ./stateful -run '^TestHistoryRecordingOutcome$' -count=1` |
| P03 | REQ-429 | TEST-451 | `stateful/history_test.go` | `go test ./stateful -run '^TestHistoryRecordingOutcome$' -count=1` |
| P03 | REQ-431 | TEST-451 | `stateful/history_test.go` | `go test ./stateful -run '^TestHistoryRecordingOutcome$' -count=1` |
| P03 | REQ-426 | TEST-443 | `stateful/artifact_integration_test.go` | `go test -tags=integration ./stateful -run '^TestReferenceArtifacts$' -count=1` |
| P03 | REQ-429 | TEST-443 | `stateful/artifact_integration_test.go` | `go test -tags=integration ./stateful -run '^TestReferenceArtifacts$' -count=1` |
| P03 | REQ-432 | TEST-443 | `stateful/artifact_integration_test.go` | `go test -tags=integration ./stateful -run '^TestReferenceArtifacts$' -count=1` |
| P04 | REQ-420 | TEST-429 | `frontend/packages/harden_llm_ui/test/harden_llm_ui/components_test.exs` | `cd frontend/packages/harden_llm_ui && mix test --seed 104729 test/harden_llm_ui/components_test.exs` |
| P04 | REQ-428 | TEST-429 | `frontend/packages/harden_llm_ui/test/harden_llm_ui/components_test.exs` | `cd frontend/packages/harden_llm_ui && mix test --seed 104729 test/harden_llm_ui/components_test.exs` |
| P04 | REQ-428 | TEST-430 | `scripts/test/run_progress_test.mjs` | `node --test scripts/test/run_progress_test.mjs` |
| P04 | REQ-429 | TEST-430 | `scripts/test/run_progress_test.mjs` | `node --test scripts/test/run_progress_test.mjs` |
| P04 | REQ-420 | TEST-431 | `frontend/test/harden_llm_web/live/restored_workflows_test.exs` | `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_workflows_test.exs` |
| P04 | REQ-423 | TEST-431 | `frontend/test/harden_llm_web/live/restored_workflows_test.exs` | `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_workflows_test.exs` |
| P04 | REQ-425 | TEST-431 | `frontend/test/harden_llm_web/live/restored_workflows_test.exs` | `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_workflows_test.exs` |
| P04 | REQ-426 | TEST-431 | `frontend/test/harden_llm_web/live/restored_workflows_test.exs` | `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_workflows_test.exs` |
| P04 | REQ-429 | TEST-431 | `frontend/test/harden_llm_web/live/restored_workflows_test.exs` | `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_workflows_test.exs` |
| P04 | REQ-425 | TEST-446 | `frontend/packages/harden_llm_ui/test/harden_llm_ui/client_test.exs` | `cd frontend/packages/harden_llm_ui && mix test --seed 104729 test/harden_llm_ui/client_test.exs` |
| P04 | REQ-428 | TEST-446 | `frontend/packages/harden_llm_ui/test/harden_llm_ui/client_test.exs` | `cd frontend/packages/harden_llm_ui && mix test --seed 104729 test/harden_llm_ui/client_test.exs` |
| P04 | REQ-429 | TEST-446 | `frontend/packages/harden_llm_ui/test/harden_llm_ui/client_test.exs` | `cd frontend/packages/harden_llm_ui && mix test --seed 104729 test/harden_llm_ui/client_test.exs` |
| P04 | REQ-432 | TEST-446 | `frontend/packages/harden_llm_ui/test/harden_llm_ui/client_test.exs` | `cd frontend/packages/harden_llm_ui && mix test --seed 104729 test/harden_llm_ui/client_test.exs` |
| P04 | REQ-420 | TEST-447 | `frontend/test/harden_llm_web/live/restored_profiles_test.exs` | `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_profiles_test.exs` |
| P04 | REQ-423 | TEST-447 | `frontend/test/harden_llm_web/live/restored_profiles_test.exs` | `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_profiles_test.exs` |
| P04 | REQ-428 | TEST-447 | `frontend/test/harden_llm_web/live/restored_profiles_test.exs` | `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_profiles_test.exs` |
| P04 | REQ-432 | TEST-447 | `frontend/test/harden_llm_web/live/restored_profiles_test.exs` | `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_profiles_test.exs` |
| P04 | REQ-420 | TEST-448 | `frontend/test/harden_llm_web/live/restored_history_test.exs` | `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_history_test.exs` |
| P04 | REQ-426 | TEST-448 | `frontend/test/harden_llm_web/live/restored_history_test.exs` | `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_history_test.exs` |
| P04 | REQ-428 | TEST-448 | `frontend/test/harden_llm_web/live/restored_history_test.exs` | `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_history_test.exs` |
| P04 | REQ-429 | TEST-448 | `frontend/test/harden_llm_web/live/restored_history_test.exs` | `cd frontend && mix test --seed 104729 test/harden_llm_web/live/restored_history_test.exs` |
| P05 | REQ-425 | TEST-432 | `internal/deploytest/reference_topology_test.go` | `go test ./internal/deploytest -run '^TestReferenceTopology$' -count=1` |
| P05 | REQ-430 | TEST-432 | `internal/deploytest/reference_topology_test.go` | `go test ./internal/deploytest -run '^TestReferenceTopology$' -count=1` |
| P05 | REQ-435 | TEST-432 | `internal/deploytest/reference_topology_test.go` | `go test ./internal/deploytest -run '^TestReferenceTopology$' -count=1` |
| P05 | REQ-430 | TEST-450 | `scripts/test/production_config_test.mjs` | `node --test scripts/test/production_config_test.mjs` |
| P05 | REQ-432 | TEST-450 | `scripts/test/production_config_test.mjs` | `node --test scripts/test/production_config_test.mjs` |
| P05 | REQ-435 | TEST-450 | `scripts/test/production_config_test.mjs` | `node --test scripts/test/production_config_test.mjs` |
| P05 | REQ-422 | TEST-433 | `internal/smoke/compose_smoke_test.go` | `make test-compose` |
| P05 | REQ-425 | TEST-433 | `internal/smoke/compose_smoke_test.go` | `make test-compose` |
| P05 | REQ-426 | TEST-433 | `internal/smoke/compose_smoke_test.go` | `make test-compose` |
| P05 | REQ-430 | TEST-433 | `internal/smoke/compose_smoke_test.go` | `make test-compose` |
| P05 | REQ-431 | TEST-433 | `internal/smoke/compose_smoke_test.go` | `make test-compose` |
| P06 | REQ-420 | TEST-420 | `internal/testkit/reference_decoupling_contract_test.go` | `go test ./internal/testkit -run '^TestReferenceDecouplingContract$' -count=1` |
| P06 | REQ-425 | TEST-420 | `internal/testkit/reference_decoupling_contract_test.go` | `go test ./internal/testkit -run '^TestReferenceDecouplingContract$' -count=1` |
| P06 | REQ-432 | TEST-420 | `internal/testkit/reference_decoupling_contract_test.go` | `go test ./internal/testkit -run '^TestReferenceDecouplingContract$' -count=1` |
| P06 | REQ-433 | TEST-420 | `internal/testkit/reference_decoupling_contract_test.go` | `go test ./internal/testkit -run '^TestReferenceDecouplingContract$' -count=1` |
| P06 | REQ-435 | TEST-420 | `internal/testkit/reference_decoupling_contract_test.go` | `go test ./internal/testkit -run '^TestReferenceDecouplingContract$' -count=1` |
| P06 | REQ-430 | TEST-434 | `scripts/production-config.mjs` | `node scripts/production-config.mjs check --services harden-llm-gateway,harden-llm-web --expected-release "$HLLM_RELEASE_SHA"` |
| P06 | REQ-434 | TEST-434 | `scripts/production-config.mjs` | `node scripts/production-config.mjs check --services harden-llm-gateway,harden-llm-web --expected-release "$HLLM_RELEASE_SHA"` |
| P06 | REQ-422 | TEST-435 | `internal/smoke/deployed_reference_test.go` | `go test -tags=live ./internal/smoke -run '^TestDeployedReferenceHTTP$' -count=1` |
| P06 | REQ-425 | TEST-435 | `internal/smoke/deployed_reference_test.go` | `go test -tags=live ./internal/smoke -run '^TestDeployedReferenceHTTP$' -count=1` |
| P06 | REQ-434 | TEST-435 | `internal/smoke/deployed_reference_test.go` | `go test -tags=live ./internal/smoke -run '^TestDeployedReferenceHTTP$' -count=1` |

## 11. Execution log template

Current execution evidence is in [progress.json](evidence/harden-llm/reference-decoupling/progress.json) and the [P00](evidence/harden-llm/reference-decoupling/phase-p00.md), [P01](evidence/harden-llm/reference-decoupling/phase-p01.md), [P02](evidence/harden-llm/reference-decoupling/phase-p02.md), [P03](evidence/harden-llm/reference-decoupling/phase-p03.md) and [P04](evidence/harden-llm/reference-decoupling/phase-p04.md) checkpoints. P00–P04 are complete; resume **P05.S01** in `/home/kirill/.cache/harden-llm-tmp/hllm-reference-decoupling-impl`, branch `feat/reference-decoupling`. The issue #100 phase comment records the pushed checkpoint SHA. The chronological [P04 log](evidence/harden-llm/reference-decoupling/phase-p04-in-progress.md) retains failed attempts; its final checkpoint is authoritative. The canonical plan is this file on the implementation branch, not the frozen v5.2 planning worktree or the stale primary checkout. CHECK-422 and the trusted main-owned preview control-assets gate remain for P05/P06. No main, dev or production promotion has run. Do not repeat the completed one-time dev reset. Copy this template for later phases and retain failures and deviations.

```yaml
phase_id: ""
phase_status: Pending # Pending or Done; a blocker is recorded under issues.
source_sha: ""
completed_steps: []
quantitative_results:
  sample_count: null
  metrics: [] # Each entry: name, mean, std, CI95, units, or reason statistics do not apply.
  command_results: []
  evidence_paths: []
issues_resolutions: []
failed_attempts: []
deviations: []
lessons_learned: []
adr_updates: []
configuration_checkpoint:
  branch: ""
  remote_sha: ""
  dependency_or_image_identities: []
  deployment_url: ""
```

## 12. Appendix: ADR index

- ADR-HLLM-015: Retain test-feedback hierarchy, owned fixtures and explicit browser opt-in.
- ADR-HLLM-022: Preserve recovery integrity when separating engine and wrapper.
- ADR-HLLM-028: Retain local image build/deployment identity and exact release-attempt evidence.
- ADR-HLLM-030: Retain Control Plane/Portal human identity ownership.
- ADR-HLLM-031: Supersede its removal of wrapper profiles and Phoenix persistence ownership; retain plain proxy independence and no old-data migration.
- ADR-HLLM-032: Retain supported OpenAI wire subset and limits; amend the reference execution boundary without changing standard final-only SSE.
- ADR-HLLM-033, proposed in P00: Record stateless core/stateful wrapper/UI boundaries; one bearer/key-hash scope across all stored data with host-supplied per-user draft keys; deferred manual key-rotation handling; complete create-on-save profiles and template-copy semantics; environment-backed credentials; frontend-only Portal admission; Model Recall consumer review and no per-user/key-management subsystem.
- ADR-HLLM-034, proposed in P00: Record HLLM PostgreSQL as the sole reference-history owner (superseding v5.0's Laminar choice), Garage path-scoped artifacts with manual orphan cleanup, no automatic history expiry, Laminar for diagnostic traces only, and the accepted nonblocking, explicitly diagnosed recording semantics.

## 13. Consistency check

- All sixteen canonical requirements and thirty-two concrete tests (TEST-420–451) appear in the RTM; all referenced tests have concrete paths, commands, fixtures, controls, pass criteria and runtime estimates in section 7.3.
- Phase IDs are sequential P00–P06 with 72 steps; subtask IDs are ordered and carry all required fields. Implementation follows intentional failing coverage; matching RED/GREEN commands and test IDs are preserved.
- P06 applies already-tested artifacts and contains no new behavioral implementation. Its configuration was covered before modification in P05; deployment identity/HTTP checks add live evidence rather than replacing deterministic coverage.
- All phases include verification/refactoring review, measurement where relevant, lifecycle evidence, estimates and the common exit/suspension gates. Human decisions do not appear in the RTM or substitute for tests.
- New commands target files/tasks explicitly created before use. Existing Make/Mix/Node/Go runners remain the owners. Unknown external operational details are explicit owner/decision gates, not invented commands.
- Current source paths and baseline paths were inspected; proposed paths are identified by their creation steps. Documentation checks comprise identifier/RTM/command matching, phase ordering and `git diff HEAD --check`.
- Accepted CHECK-420 decisions are applied consistently to requirements, phase actions, test assertions and data contracts. History-count acceptance is conditional on healthy enabled recording; failure coverage requires preserved inference and truthful diagnostics. Existing Phoenix drafts/history are removed only after their Go replacements and REST routes are green (P03.S13).
- P00 contract/fixture tests and the static gate pass; its actual evidence is linked in section 11. Application runtime implementation, broader phase gates, browsers, provider calls and production deployment are not certified by P00. v5.1 removed the Laminar owner gate; remaining prerequisites are the shared-service provisioning checks in CHECK-422 (PostgreSQL database/role, Garage prefix permissions) before P05 apply and P06, followed by the required phase gates. Settled product choices, including DEC-HLLM-V51-01 to 07, DEC-HLLM-V52-01 to 07 and DEC-HLLM-V53-01 to 02 in section 7.4, do not need to be asked again. v5.2 moved two steps from P05 to P02 and reordered P03; v5.3 adds TEST-450/451 within existing RED/GREEN pairs, so the step count remains 72. Unknown commits cannot become not_saved from an empty readback; production schema execution is explicit before application startup.
