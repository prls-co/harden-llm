# Shared CPA admission: source and documentation checks

## 1. Official usage scope

The [official Codex pricing and usage documentation](https://learn.chatgpt.com/docs/pricing) says model choice, workload/context, reasoning, tools and caching affect usage; local and cloud work share allowances and longer limits may apply. It does not provide this account's short-term CPA concurrency or 200/61-second contract. The documented usage dashboard, rather than message estimates, is the account-specific source for subscription limits. Checked 2026-10-16. This does not establish a suspension threshold or authorize ignoring provider restrictions.

## 2. Passive CPA quota signals

At pinned source `c93978c4ea2e908255a2a06c37599fda3651554a`, [Codex quota parsing](https://github.com/router-for-me/CLIProxyAPI/blob/c93978c4ea2e908255a2a06c37599fda3651554a/internal/runtime/executor/helps/codex_quota.go) supports primary/secondary usage windows, reset times and bounded additional limits. [Passive observation](https://github.com/router-for-me/CLIProxyAPI/blob/c93978c4ea2e908255a2a06c37599fda3651554a/sdk/cliproxy/auth/quota_signals.go) replaces an observed snapshot when new signals exist; responses without signals retain the older snapshot. Observation does not itself change scheduler cooldown state.

A read-only local management-metadata inspection during the first competing run exposed only `Retry-After` in the Codex and gpt-6-luna signal maps. No numeric used-percent/remaining/reset watermark was available in that observation. It is therefore not evidence that a client can calculate spare account capacity from passive signals. Check snapshot freshness; an old Retry-After value is relative to its observation time.

Only an explicit numeric/boolean quota-header allowlist was retained. No raw auth record, identity value, OAuth/session material or management secret was printed or written. `passive-quota-during-throttle.json` contains the bounded observation. The management endpoint is private administration, not a client-facing scheduling API.

## 3. Retry ownership granularity

The pinned [credential retry override](https://github.com/router-for-me/CLIProxyAPI/blob/c93978c4ea2e908255a2a06c37599fda3651554a/sdk/cliproxy/auth/types.go#L501)
uses auth metadata `request_retry`. [Retry selection](https://github.com/router-for-me/CLIProxyAPI/blob/c93978c4ea2e908255a2a06c37599fda3651554a/sdk/cliproxy/auth/conductor_selection.go#L1065)
chooses that value when present, otherwise the global default. This scopes a
change to one credential rather than all CPA providers, but still affects every
consumer of that credential. No override appeared in the inspected Codex record;
no override or shared setting was changed. These source checks do not establish
a public per-request retry override.
