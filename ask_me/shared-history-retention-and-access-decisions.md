# Harden-LLM shared-history retention and access decisions

- Date: 2026-10-10.
- Status: Historical answers were 1B and 2B. Retention 1B remains active. Plan revision 5.0 replaces login-wide shared deletion 2B with read/delete/clear restricted to holders of the same validated HLLM access key; no administrator distinction within that key scope. No further answer is requested.
- Scope: The two history-policy questions from the latest confidence review. Laminar remains the sole history owner.
- Canonical plan: [Restore reference workflows and decouple Harden-LLM](../plans/restore-reference-workflows-decoupling-plan.md), revision 5.0.
- Earlier decisions: [Reference decoupling decisions](reference-decoupling-decisions.md).
- Planning checkout: `plan/restore-decoupled-reference-app` in `/home/kirill/.cache/harden-llm-tmp/hllm-reference-decoupling-plan`.
- Evidence boundary: Source and planning contracts were reviewed. Live retention, service-key deletion, artifact cleanup and deployed permissions remain unverified. No application implementation or infrastructure change is part of this record.

The current plan is authoritative for API-key ownership; the historical questions/options below preserve prior context only. Options run from greater policy/authorization investment to the simpler shared-service approach. Unselected options preserve the question context; they are not implementation stages or fallbacks. Option letters apply only within this document.

Rubrics describe evidence and relative trade-offs, not scores to add together. `Conf` estimates implementation evidence confidence, not confidence in the user's answer. For the other axes, `i` means highest investment, widest blast radius, hardest reversal, strongest architecture fit, strongest reuse, strongest observability, or smallest surface respectively. Ties reflect equivalent ownership. `Perf:na` means performance is not established or material to this policy choice.

### 1.1 Question

Should Harden-LLM inherit the existing shared Laminar retention policy, or require a specific retention period for its reference-call history?

### 1.2 Context & clarification

**History** means reference-application calls, including their prompts, results and execution facts, shared across enabled logins. Ordinary `/v1` calls do not enter this product history. **Retention** means how long the storage owner keeps records. A date filter or pagination cutoff selects records for a query; it must not silently become an additional expiry policy.

The earlier draft introduced `HLLM_HISTORY_RETENTION_SECONDS` and `HistoryConfig.Retention`, then used that duration as an implicit lower query bound. Those were proposed names, not implemented settings. They were removed in revision 4.1 because they duplicated Laminar policy and could hide records Laminar still retained.

Source and proposed-contract anchors:

- Current [HardenLlm.Reference](../frontend/lib/harden_llm/reference.ex) implements `record_call/1`, `list_calls/2`, `get_call/1` and `statistics/0` using Phoenix-owned storage. It does not prove a Laminar retention contract.
- The plan proposes `stateful.Service.ListHistory`, `GetHistory`, `Statistics` and `HistoryQuery`. These symbols are not implemented. `HistoryQuery.From` is optional; default listing includes retained records before a fixed pagination cutoff, and detail lookup has no implicit age restriction.
- The shared [Laminar operations runbook](/home/kirill/p/laminar-shared/runbooks/operations.md:94) records an obligation for Laminar references of at least 120 days and keeps retention/restore certification separate. This documents an obligation, not a measured deployment TTL or proof that every HLLM record receives that retention.

The actual shared policy and its enforcement still require owner verification. No finite period, expiry API or automatic Garage cleanup capability is assumed here.

### 1.3 Options

- **Option A: Define an explicit HLLM retention requirement at the Laminar owner**
  - **Rubrics**: `Conf:60% | Invest:i | Blast:i | Reversal:i | Fit:ii | Reuse:i | Obs:i | Surface:ii | Perf:na`
  - **Approach**: State a required period and have the Laminar owner verify and configure supported enforcement. Keep that policy in Laminar; do not add an HLLM expiry setting or application cleanup process.
  - **Example**: If the product requires 180 days, record that requirement with the storage owner and verify supported enforcement before promising it. This is an example, not an accepted period.
  - **Architecture**: Uses the selected history owner. Any project-specific enforcement remains conditional on its supported configuration; no new store or guessed API is introduced.
  - **SSoT**: Laminar owns retention configuration and stored history. HLLM queries available records without duplicating the retention calculation.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Appropriate for a concrete retention requirement, but adds owner configuration and acceptance work. Expired data cannot be recovered merely by lengthening the policy later. Shared policy changes must not alter other consumers unintentionally.

- **Option B: Inherit the existing shared Laminar policy**
  - **Rubrics**: `Conf:80% | Invest:ii | Blast:ii | Reversal:ii | Fit:i | Reuse:i | Obs:ii | Surface:i | Perf:na`
  - **Approach**: Use the existing owner policy, verify what it provides, and remove separate HLLM expiry configuration and implicit age filtering.
  - **Example**: A retained record remains accessible through history or its detail view even if it is older than a previously assumed application window.
  - **Architecture**: HLLM remains a client of the shared history service. Optional date filters, bounded queries and pagination are query behavior, not data-retention enforcement.
  - **SSoT**: Laminar alone controls retention. PostgreSQL continues to own profiles, keys, drafts and cache, with no history copy.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Requires less application configuration and avoids policy drift. HLLM does not promise a separate minimum or maximum period; the real shared policy still needs verification and documentation.

### 1.4 Recommendation

**Recommend Option B. Accepted answer: “Use Laminar’s retention policy (recommended).”**

This fits the selected shared-service architecture and avoids a second source of retention policy. The plan now omits `HLLM_HISTORY_RETENTION_SECONDS` and `HistoryConfig.Retention`, permits an unlimited owner policy if that is the actual configuration, and does not hide retained records behind an inferred age limit. Existing owner obligations remain subject to verification; this decision does not waive them or authorize a shared retention change.

### 2.1 Question

Should every enabled login be allowed to delete entries and clear history shared by all users, or should those operations be restricted to existing PRLS administrators?

### 2.2 Context & clarification

An **enabled login** is a user whose current session is accepted by shared PRLS Control Plane. **Shared history** means the same reference-call records are visible to all such users. Deleting an entry removes it from that shared history; clearing history affects other users too.

These operations remain scoped to this Harden-LLM deployment's reference records and associated artifacts. They do not authorize clearing a shared Laminar project wholesale, deleting unrelated diagnostic traces, or modifying another user's private profiles, provider keys or drafts.

Source and proposed-contract anchors:

- Current [Phoenix routing](../frontend/lib/harden_llm_web/router.ex) uses `PrlsWeb.Access.Plug` and `PrlsWeb.Access.LiveAuth` with `:require_access`.
- The existing shared [Go access client](/home/kirill/p/prls-control-plane/go/access/access.go:139) exposes `Client.Resolve(ctx, reference)`. The plan uses this verified identity flow without requiring company selection or an HLLM product grant.
- Current [gateway authorization](../internal/gateway/httpapi/httpapi.go:175) validates the deployment bearer. Under the proposed reference contract, `HARDEN_LLM_TOKEN` alone is insufficient: the boundary also verifies `X-PRLS-Session-Reference` through Control Plane.
- Planned `Service.DeleteHistory(ctx, runID)` and `ClearHistory(ctx, query)` operate on shared records. Their supported Laminar deletion transport is still an engineering gate; the policy decision does not prove that transport works.

### 2.3 Options

- **Option A: Restrict deletion to existing PRLS administrators**
  - **Rubrics**: `Conf:60% | Invest:i | Blast:ii | Reversal:ii | Fit:ii | Reuse:i | Obs:i | Surface:ii | Perf:na`
  - **Approach**: Allow enabled users to read shared history, but require authoritative PRLS administrative permission for deletion and clearing. First verify the supported administrative-authority contract; do not infer it from an arbitrary role label.
  - **Example**: A normal enabled user can restore a call, while an authorized administrator can remove that call from everyone's history.
  - **Architecture**: Adds one authorization distinction at the reference HTTP boundary using the existing identity authority. It must not introduce HLLM roles, a local user directory or a separate administration service.
  - **SSoT**: Control Plane owns administrative authority; Laminar owns history. The UI reflects the server's decision rather than enforcing it independently.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Narrows who can remove shared records but adds permission handling and denial states. Its exact authority mapping is unverified and it departs from the requested access-only login model.

- **Option B: Give every enabled login equal deletion access**
  - **Rubrics**: `Conf:80% | Invest:ii | Blast:i | Reversal:i | Fit:i | Reuse:i | Obs:ii | Surface:i | Perf:na`
  - **Approach**: Apply the same verified-login requirement to reading, deleting and clearing shared history. Do not add an administrator check or restrict deletion to the caller who created a record.
  - **Example**: Alice may delete a call made by Bob; after successful deletion, neither user sees it. Alice still cannot read or change Bob's private provider key or draft.
  - **Architecture**: Uses the existing reference identity boundary and the shared-history scope. Ordinary deployment-token `/v1` access remains separate from human reference operations.
  - **SSoT**: Control Plane determines enabled access; Laminar stores and deletes history. One server policy governs every reference client.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Preserves the simple access model and avoids role machinery. Every enabled user can remove records other users wanted to retain. Deletion and artifact-cleanup failures must remain explicit; an uncertain deletion cannot be reported as complete.

### 2.4 Recommendation

**Recommend Option B. Accepted answer: “All enabled logins may delete/clear (recommended).”**

The plan grants equal shared-history deletion access to every enabled login. It adds no HLLM administrator role, company requirement or per-record creator restriction. Private application records retain their existing owner isolation. Laminar deletion compatibility and independent artifact download/cleanup remain engineering checks, not unanswered product-policy questions.

### 3.1 Current API-key ownership refinement

All history operations and associated statistics/artifact downloads are now scoped to the server-derived hash of the authenticated HLLM access key. Any holder of that key has equal access to its data; a different key cannot access it. Portal login alone does not grant another key's history. Laminar remains the sole history owner and retention authority; its unresolved supported read/delete/artifact capabilities remain engineering gates. This replaces the earlier all-logins/all-history scope without adding a local user or permission system.
