# Harden-LLM reference decoupling decisions

> Historical decisions. ADR-HLLM-033/034 and the accepted v5.3 reference plan
> supersede Laminar history and human-owner storage choices. Laminar now owns
> diagnostic traces only; HLLM PostgreSQL owns reference history.


- Date: 2026-10-10.
- Status: Historical decision record, superseded where noted by plan revision 5.0 (2026-10-11). Original 1A user-owned overlays and 2A extra REST session verification are replaced by API-key-scoped complete snapshots and bearer-only REST. Nonblocking recording 3B, shared services and external frontend admission remain accepted. Implementation is pending.
- Related plan: [Restore reference workflows and decouple Harden-LLM](../plans/restore-reference-workflows-decoupling-plan.md).
- Planning checkout: `plan/restore-decoupled-reference-app` in `/home/kirill/.cache/harden-llm-tmp/hllm-reference-decoupling-plan`.
- UI restoration baseline: `754690c8d0890c664d69fbdf526b16b3e5601060`, PR #95.
- Scope: The three product questions raised during plan review. Laminar capabilities, infrastructure grants and visual verification remain investigation/acceptance work, not additional product questions here.

The options below preserve the decision context. The linked current plan is authoritative where later choices supersede these historical answers; unselected alternatives are not stages, fallbacks or outstanding questions. Options run from greater structural investment to the more pragmatic implementation.

Rubrics are comparative observations, not votes. `Conf` estimates evidence confidence, not reliability or certainty about your preference. Other values rank within each question: `i` means highest investment, widest blast radius, hardest reversal, strongest fit/reuse/observability, or smallest surface respectively. Ties indicate indistinguishable positions. `Perf:na` means available evidence does not support a meaningful performance ranking.

Historical answers were **1A, 2A, 3B**, with **Laminar as the sole history owner**. Current plan sections 2/4/8 replace user ownership/live overrides with one API-key scope and complete create-on-save snapshots; REST requires the bearer only. Portal controls frontend entry. The later Model Recall choice 2A belongs to a separate consumer question and is also recorded in the current plan. No settled product choice requires another answer.

### 1.1 Question

Should all enabled logins share and edit one saved LLM-profile catalog, including replacing provider credentials, or should each login own private profiles?

### 1.2 Context & clarification

A **saved LLM profile** is provider/model configuration, hardening settings, pricing and a credential reference. It is not a user account. The restored profile picker selects this configuration; login determines access to the application.

The **stateless core** is the Go execution library anchored by `New(options Options)` and `(*Client).Call(ctx, request)` in [client.go](../client.go), with `Options` in [types.go](../types.go). It receives resolved inputs and injected effects. It owns no profile database or human identity. Deterministic decision logic stays separate from concrete I/O implementations; “stateless” does not mean the LLM call is mathematically pure.

The **stateful wrapper** is the proposed reusable Go owner of profile storage, credential resolution and application services. Its `stateful/` package is planned, not implemented.

Verified source anchors:

- At the restoration baseline, `ProfileService.Save(ctx, request)` uses `SaveProfileRequest.OwnerID`; `ProfileService.Credential(ctx, ownerID, credentialID)` retrieves owner-scoped credentials. These are in historical `internal/gateway/profile_service.go`, not the current profile-free implementation.
- Baseline `ProfileWidgetComponent.handle_event("select-profile", ...)` uses `selected_profile_id` in `frontend/lib/harden_llm_web/live/profile_widget_component.ex`.
- Current draft ownership already uses `socket.assigns.access_context.user_id`, `Reference.get_draft/1` and `Reference.save_draft/3` in [WorkspaceLive](../frontend/lib/harden_llm_web/live/workspace_live.ex) and [pre-decoupling Reference](https://github.com/prls-co/harden-llm/blob/2a8aede9096996963f74107fded8218aa15a9011/frontend/lib/harden_llm/reference.ex).

History shared across enabled logins and user-private drafts are already settled. This question changes profile ownership only. Sharing profiles also shares the effect of edits: replacing a credential changes future calls using that profile. Hiding credential values in reads does not make a shared profile private.

The accepted private model allows separate provider accounts without introducing a new role system. Git presets are shared read-only definitions; saved user customizations and credentials are private.

### 1.3 Options

- **Option A: Private profiles per login**
  - **Rubrics**: `Conf:70% | Invest:i | Blast:ii | Reversal:ii | Fit:ii | Reuse:i | Obs:i | Surface:ii | Perf:na`
  - **Approach**: Scope profile operations and credential resolution to the authenticated stable user ID. Retain one canonical wrapper implementation.
  - **Example**: Alice’s “CPA JSON” profile and Bob’s identically named profile are separate records; either can replace their own credential without affecting the other.
  - **Architecture**: Reuses the baseline ownership model and the current draft identity concept. The calling boundary supplies verified identity according to Question 2; the core receives only a resolved configuration snapshot.
  - **SSoT**: Git owns credential-free presets; the wrapper owns private saved changes and encrypted personal credentials in shared PostgreSQL. Deployment keys remain environment-backed. Each value has one owner; there is no shared editable user catalog or compatibility lookup.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Preserves per-user isolation and baseline semantics. Adds owner-dependent CRUD and credential behavior when logins otherwise exist only for access. Identical settings may be stored by multiple users; implementation logic remains centralized.

- **Option B: One shared editable profile catalog**
  - **Rubrics**: `Conf:70% | Invest:ii | Blast:i | Reversal:i | Fit:i | Reuse:i | Obs:ii | Surface:i | Perf:na`
  - **Approach**: All enabled logins use and edit the same profiles, including credential replacement. Keep credentials server-side and bind each running call to an immutable resolved snapshot.
  - **Example**: Alice updates the shared “CPA JSON” profile; Bob’s next call uses the new revision. Bob’s already-running call keeps its original snapshot.
  - **Architecture**: Fits the target shared workspace and “login is for access” direction. Drafts remain private; profile queries do not introduce user ownership into the core.
  - **SSoT**: One deployment-wide profile/credential catalog in shared PostgreSQL, owned solely by the wrapper. No per-user overlays, copied defaults or fallback catalog.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Smaller ownership and permission model, with broad impact from edits. Later conversion to private profiles requires an explicit ownership decision; no hidden attribution is assumed. This is appropriate only for mutually trusted editors.

### 1.4 Accepted decision

**Option A is accepted.** Preset base URLs, models, prices and options are committed in Git. Initial deployment provider accounts/keys and `HARDEN_LLM_TOKEN` come from protected `.env` configuration. User-saved settings and provider keys persist in Agent Platform's shared PostgreSQL; keys are encrypted and private records use the verified stable Control Plane user ID.

Save a preset reference plus explicit changes, or a custom definition without a preset. Resolve one immutable snapshot and one explicit credential binding. Do not copy every preset per user, overwrite saves on restart, synchronize environment secrets into mutable records or fall back to deployment credentials after a personal-key failure. History remains shared; drafts remain private. Private storage does not require private networking.

Use the existing Portal operator `/admin` page to add users and reset/disable access through Control Plane. Every enabled login may enter Harden-LLM without company selection or a product grant. Human passwords/sessions stay with Control Plane; HLLM stores only application records and owner references. The existing Control Plane `.env` bootstrap is limited to its documented credential pairs, not a new HLLM user importer.

### 2.1 Question

Must other repositories access profile/history/draft REST operations over the public internet, or can their trusted application backends reach those operations through private service networking?

### 2.2 Context & clarification

The **public inference API** is the OpenAI-compatible `/v1` surface authenticated by `HARDEN_LLM_TOKEN`. It stays publicly available at `https://harden-llm.prls.co/v1` under either option, without a human login or saved profile.

The **reference REST API** means proposed application operations for saved profiles, shared history, private drafts and the richer reference execution flow. These are separate contracts from ordinary `/v1` inference; restored `/api/v1/...` routes belong to the wrapper, not the core. Their final schemas must be specified in `api/openapi.yaml` before implementation.

A **trusted backend** authenticates its human caller, derives that caller’s stable ID, and is admitted to the private service network. A browser-supplied owner ID is not authenticated identity. Using the same deployment bearer for inference and private service calls does not establish a human identity.

Verified source anchors:

- `(*API).authorize` in [httpapi.go](../internal/gateway/httpapi/httpapi.go) validates the bearer for `/v1/models`, `/v1/chat/completions` and `/v1/responses`.
- [router.ex](../frontend/lib/harden_llm_web/router.ex) uses `PrlsWeb.Access.Plug` and `PrlsWeb.Access.LiveAuth` for human access. `WorkspaceLive` consumes `socket.assigns.access_context.user_id`.
- [HardenAPI](../frontend/lib/harden_llm_web/harden_api.ex) implements `validate_config!/0`, `models/0` and `responses/1`; its server-side request uses the deployment bearer, with redirects and automatic retries disabled.
- `routeFor(state)` in [preview-environment.mjs](../scripts/preview-environment.mjs) currently routes `/v1/*`, `/healthz` and `/readyz` to Go and other paths to Phoenix. This is source evidence, not verification of production ingress.

The existing Phoenix identity integration does not prove that an external machine client has a supported Control Plane identity protocol. Public reference access would require verification against the Control Plane’s owner-maintained contract. No new identity header, token format or authentication endpoint is assumed here.

### 2.3 Options

- **Option A: Public reference REST with verified human identity**
  - **Rubrics**: `Conf:50% | Invest:i | Blast:i | Reversal:i | Fit:ii | Reuse:i | Obs:i | Surface:ii | Perf:na`
  - **Approach**: Publish the reference operations on the same hostname, with one documented Control Plane-backed identity mechanism at the HTTP boundary. A deployment bearer alone cannot access private drafts.
  - **Example**: Another application hosted outside the private network requests its signed-in user’s draft through an authenticated reference endpoint. The service derives ownership from verified identity, not a supplied user-ID string.
  - **Architecture**: Keep identity verification at the reference HTTP boundary. The reusable wrapper receives a validated actor value; the stateless core remains identity-free. Reuse the established identity authority, with no local users or token issuer.
  - **SSoT**: Control Plane owns human identity; the wrapper owns application records; OpenAPI owns the reference wire contract. Do not also maintain a less-protected public path.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Supports remote consumers and provides an explicit identity boundary per request. Expands the public API and integration obligations. Feasibility remains conditional on a supported external identity contract; no custom authentication protocol is authorized.

- **Option B: Private reference REST for trusted backends**
  - **Rubrics**: `Conf:70% | Invest:ii | Blast:ii | Reversal:ii | Fit:i | Reuse:ii | Obs:ii | Surface:i | Perf:na`
  - **Approach**: Publish `/v1` and the Phoenix UI; keep reference REST reachable only by admitted backend services. Use the deployment bearer server-to-server, with the calling backend responsible for authenticated human identity.
  - **Example**: Phoenix authenticates Alice through Portal, derives her stable ID and requests her draft privately. An external OpenAI client can invoke `/v1/responses` but cannot reach draft operations.
  - **Architecture**: Fits the existing server-side `HardenAPI` and Portal integration. Caddy routing and direct-port exposure must enforce the private boundary. The wrapper contains no Phoenix or Portal client dependency.
  - **SSoT**: Control Plane remains the identity authority; each calling application uses its existing authentication boundary; the wrapper is the single application-data owner. No public reference alias or second authentication path is retained.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Smaller public surface and less authentication machinery. Remote services must obtain private connectivity. The wrapper trusts admitted backends to supply correct identity; private reachability is a required part of the contract, not proof that arbitrary callers are trustworthy.

### 2.4 Accepted decision

**Option A is accepted.** Publish `/v1`, reference REST and the Phoenix UI on `harden-llm.prls.co`. Use authentication at the HTTP boundary instead of a public/private service-network split. Ordinary `/v1` still accepts only `HARDEN_LLM_TOKEN` and a standard request; no saved profile or human login is required.

Reference user operations require the deployment bearer and a Control Plane-verified actor. Reuse its supported session-reference/access-context contract, with service credentials kept server-side; never trust a supplied owner ID. Use the shared Go access client’s accountless `Resolve` method; P00 validates the pinned owner schema rather than inventing an authentication protocol. The account-scoped Portal API-token/product-authorizer contract must not silently impose company/product requirements on HLLM. This is an implementation-contract check, not an unanswered public/private product choice.

### 3.1 Question

For a reference-app request whose model/profile is already resolved, should unavailable history storage prevent inference, or should inference proceed with an explicit warning that its history was not saved?

### 3.2 Context & clarification

**Inference outcome** means the LLM request’s success, failure, cancellation or unknown outcome. **History persistence outcome** means whether the reference application recorded that result. These are different facts: a successful LLM response can exist even when history storage fails.

This question does not permit bypassing invalid profiles, missing credentials or failed authorization. It concerns only history after those prerequisites are satisfied. Direct `/v1` calls already exclude product history and remain unaffected.

Verified source anchors:

- In [WorkspaceLive](../frontend/lib/harden_llm_web/live/workspace_live.ex), `handle_async(:run, {:ok, {:ok, response}}, socket)` sets `run_result` before starting `Reference.record_call/1`.
- `handle_async(:record_call, _result, socket)` sets `recording_error` to “The result is shown, but shared history could not save it.” The returned result remains visible.
- `handle_async(:history, _result, socket)` reports that the workspace and inference remain available while history is unavailable.
- [pre-decoupling Reference](https://github.com/prls-co/harden-llm/blob/2a8aede9096996963f74107fded8218aa15a9011/frontend/lib/harden_llm/reference.ex) owns `record_call/1` and `statistics/0` today. These move to the wrapper in the proposed design.
- `HardenAPI` already disables automatic request retries. This supports separating a persistence failure from authorization to execute the LLM request again.

The earlier draft's initial-persistence gate is superseded by accepted Option B and removed in plan revision 4.1. Source inspection confirms the current result-first behavior; it does not prove any external history store's durability guarantee.

For either option, represent inference outcome and recording outcome explicitly in their owning contracts, using mutually exclusive states for each lifecycle instead of additional overlapping booleans. Do not introduce a durable job system, backup store or automatic inference replay.

### 3.3 Options

- **Option A: Require an acknowledged history start before inference**
  - **Rubrics**: `Conf:60% | Invest:i | Blast:i | Reversal:ii | Fit:ii | Reuse:i | Obs:i | Surface:ii | Perf:na`
  - **Approach**: Record the reference execution’s start before dispatch. If storage fails or acknowledgement is uncertain, return a storage error without calling the provider. Finalize the record after execution.
  - **Example**: History storage is down, so the reference app rejects the run before provider invocation. If terminal persistence later fails, preserve the returned LLM result and report that the stored record is incomplete.
  - **Architecture**: The wrapper owns the sequence; core logic stays unchanged. The selected history owner must support the required acknowledgement and update semantics through its supported contract.
  - **SSoT**: One history owner stores the execution lifecycle. No local outbox, diagnostic-store substitute or replay mechanism is added.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Establishes a record before dispatch and exposes history outage immediately. Makes reference inference depend on history availability and changes current behavior. It cannot guarantee terminal recording across crashes or eliminate unknown provider outcomes.

- **Option B: Return inference results and report recording failure separately**
  - **Rubrics**: `Conf:80% | Invest:ii | Blast:ii | Reversal:i | Fit:i | Reuse:i | Obs:ii | Surface:i | Perf:na`
  - **Approach**: Execute the valid request, attempt history persistence with a bounded operation, and expose its recording outcome separately. Never substitute another store or repeat inference because recording failed.
  - **Example**: The provider returns valid JSON while history storage is unavailable. The UI displays the JSON and a clear unsaved-history warning; history-derived statistics cannot count that missing entry.
  - **Architecture**: Preserves the current `run_result`/recording-error semantics while moving persistence from Phoenix to the reusable wrapper. Keep the public inference contract independent of application history.
  - **SSoT**: One configured history owner receives records. The response is the current call’s result, not a second durable history database. Do not add a disk spool or reconciliation service.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Preserves useful inference during history outages with little additional machinery. Accepts missing history after outages or process crashes; this is unsuitable if every invocation requires a durable audit record. Missing records cannot be retroactively recovered without another retained source.

### 3.4 Accepted decision

**Option B is accepted.** Execute an authorized, resolved request without requiring a successful history write. Preserve the result and expose recording separately. One complete history record per reference execution is required during healthy enabled operation; outages/crashes may leave missing records without implying recording success.

Distinguish deliberately disabled/not-set-up history, enabled-but-invalid configuration, successful empty reads, readback-confirmed saves, acknowledged writes pending visibility, definite write failures, unconfirmed writes after lost acknowledgement, and read failures/delayed visibility. Never silently disable a failing integration or represent a failed query as empty history. No alternate store, outbox, inference replay or blind retry of an ambiguous write.

Provide actionable UI messages and detailed sanitized diagnostics from one structured error: stable code, component/operation, execution/request/trace correlation, mode, separate inference/persistence outcomes, cause category and timing. Exclude tokens/passwords/session references/secret DSNs and user payloads from diagnostic errors. Laminar is the accepted sole history owner. CHECK-421 verifies its supported service-key ingestion/read/delete, visibility, payload and retention contracts; it does not reopen the storage choice. PostgreSQL owns saved settings/keys/drafts/cache, with no history fallback. Missing Laminar capability is an external integration blocker.

### 4.1 Additional accepted history decisions

The later confirmation `1B; 2B` refers to the [history-policy decision record](shared-history-retention-and-access-decisions.md), not to the earlier questions in this document.

- **Retention:** Use the existing shared Laminar retention policy. Verify its actual configuration; do not add an HLLM expiry setting, require finite retention, or hide data that Laminar still retains.
- **Deletion access:** Every enabled login may delete entries and clear history for everyone. No additional HLLM administrator role or ownership restriction applies to shared history. Private profiles, provider keys and drafts keep their accepted owner isolation.
- These answers resolve product policy. Laminar read/delete permissions, actual retention configuration, and the independent artifact download/cleanup design remain engineering checks; none authorize a different history backend or lost UI functionality.

### 4.2 Current authoritative scope and snapshot policy

Plan revision 5.0 adopts the validated incoming HLLM key hash for profiles, drafts, entered provider credentials, cache, history and artifacts. Same key shares data across logins/clients; different keys isolate data and replacing a key starts a fresh scope. Save creates a new full UUID/date/suffix profile, with no live Git inheritance or old snapshot edit. Human identities remain outside HLLM; Go REST has no session/user ownership protocol. Historical options below do not authorize a legacy path.
