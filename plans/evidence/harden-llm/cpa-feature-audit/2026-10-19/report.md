# CPA feature audit and request-status ownership decision

- Local date: 2026-10-19; final live metadata check: 2026-10-20 02:30:54 UTC.
- Status: read-only deployment/source investigation; no inference calls, configuration changes, builds, container recreation or deployment qualification.
- Related: [HLLM #136](https://github.com/prls-co/harden-llm/issues/136), [HLLM #134](https://github.com/prls-co/harden-llm/issues/134), [CPA setup #6](https://github.com/prls-co/CLIProxyAPI-setup/issues/6).
- Setup source inspected: `96e65def8a43b2df7ec0327c2bd89ffa578cbc8a` in `/home/kirill/p/CLIProxyAPI-setup`, with a clean worktree.
- Scope: whether existing/current CPA features replace the planned HLLM status lookup, and where to implement future shared-account protection.

## 1. Decision

Keep the caller-facing request-status feature in HLLM. CPA cannot observe the
whole HLLM operation, establish the HLLM caller's key scope, or report completion
of HLLM's final validation/repair/projection. A CPA core patch would still leave
most of that application-level work necessary.

Evaluate existing CPA/Home facilities before building a custom global
credential-concurrency limiter. For additional CPA-native diagnostics, prefer
the existing plugin interfaces over another source patch. These are independent
workstreams: status lookup does not require upgrading CPA or installing Home.

An upgrade evaluation is justified: the running deployment is a maintained
v7.3.8 build while latest upstream at inspection is v8.0.35. A newer version is
not itself throughput evidence or authorization to change shared production.

## 2. Verified deployed baseline

| Item | Observed value / method |
| --- | --- |
| Version label | `v7.3.8-prls.3`, from the running Docker image labels |
| Running immutable image | `ghcr.io/prls-co/cli-proxy-api-patched@sha256:99bedd436cf04530451aeff67b88d3e76dfff2f2c48691dbf68d07e0c27c7288` |
| Setup image revision | `f375487d29a06bd4cb0ad204cc19dbcf6e7dfb6d` |
| Upstream source base | `c93978c4ea2e908255a2a06c37599fda3651554a` |
| Local patch SHA-256 | `50141c6d2fec71ddb8627a77aa4c0db6bb9bd3feb4b2f8a93097064d0fc15660` |
| Container health | Healthy |
| Effective retries | `request-retry=3`, `max-retry-credentials=1`, `max-retry-interval=30` seconds |
| Cooling | Enabled (`disable-cooling=false`); persisted cooldown status disabled |
| Home | Not configured |
| Plugins | Plugin object present, `enabled=false`, zero configured plugin entries |
| Keepalives | Nonstream setting omitted; `streaming={}`; no enabled optional keepalive policy established |
| Credentials | One enabled Codex credential and one enabled Claude credential; no credential identifiers retained |
| CPA client keys | One configured key |
| Usage | Enabled; queue retention 3,600 seconds |
| Response header passthrough | Disabled; existing trusted recovery-hint exceptions are separate |
| Local model list | HTTP 200, 32 entries, `gpt-6-luna` present |
| Public edge | `https://cpa.prls.co/healthz` and `/v1/models`: HTTP 200; origin `shaman`; public model count 32 |

The effective policy and credential counts came from authenticated read-only
management GETs, projected through a strict output allowlist. Keys were loaded
only in-process from the canonical protected `.env`; no raw config, credential
file, management body or trace credential selector was retained.

A Python public request returned HTTP 403; curl probes succeeded through the
public edge. The rejection's cause was not established. The successful curl
checks prove health/catalog reachability, not model-inference correctness.
No usage-queue endpoint was consumed and no second collector was installed.

## 3. Current upstream and upgrade implications

Latest stable release observed:
[v8.0.35](https://github.com/router-for-me/CLIProxyAPI/releases/tag/v8.0.35),
published 2026-10-19 19:02:46 UTC, source
`ae3ee3be303fb07fb8173279923413005bf32177`.
GitHub comparison reported 456 commits ahead of the deployed upstream base.
The source module is now `/v8`, rather than `/v7`.

v8 introduces a structured configuration/management surface. Existing v0
management remains supported, and the loader accepts historical YAML aliases;
do not claim that every existing flat setting immediately breaks. However,
rendering, reads/writes, OAuth operations, CPA Manager Plus collection and the
maintained image's regression contracts need qualification together. See
[v8 Management API](https://github.com/router-for-me/CLIProxyAPI/blob/ae3ee3be303fb07fb8173279923413005bf32177/docs/management-api-v8.md).

The maintained cancellation patch passes `git apply --check` against its exact
deployed base. It does **not** apply to v8.0.35: the Codex stream hunk fails.
This was a non-mutating check against isolated source checkouts. The patch adds
`codex.stream.cancelled` lifecycle evidence without usage fabrication; no
equivalent exact log event was found in the inspected latest stream source.
Retain that acceptance oracle when porting the patch or replacing it with a
qualified supported lifecycle integration. Do not silently drop the behavior.

## 4. Features relevant to this work

| Capability | Existing support | Deployment / implication |
| --- | --- | --- |
| Retry rounds, bounded cooldown waits, credential selection | Deployed base and latest | Already configured; these are not a queue or account-capacity guarantee |
| Credential-specific `request_retry` override | Deployed base and latest | Useful policy granularity, but every client sharing that credential is affected; not a per-HLLM-client override |
| Trusted local `Retry-After` | Deployed maintained baseline and latest | Reuse existing recovery hints; no replacement parser/blanket passthrough needed |
| Request interception and completion plugins | Deployed base and latest | Disabled here; reusable extension points for CPA-native observations |
| Home credential concurrency and in-flight publication | Deployed base contains integration; latest CPA/Home expands the management/control surface | Home is not deployed/configured here; cannot activate limits with only a standalone CPA number |
| Codex quota header/event observations | Deployed base and latest include usage-header plumbing | Inspect existing collector retention before adding any extra sink; quota facts are not necessarily short-window RPM/concurrency |
| Home quota snapshots | Current Home provides passive Codex header ingestion and active collection | Potentially useful operational visibility; not adopted or live-qualified here |
| Nonstream whitespace / streaming heartbeats | Existing configuration mechanisms | Optional idle-transport aids; do not extend HLLM's hard deadline or supply a status API |
| HLLM-style caller-scoped live status | Not found in inspected stock CPA routes | No `/v1/requests/{trackingID}`, generic LLM background retrieval or batch-job contract replacing HLLM #136 |

Important source anchors:

- [Deployed plugin lifecycle types](https://github.com/router-for-me/CLIProxyAPI/blob/c93978c4ea2e908255a2a06c37599fda3651554a/sdk/pluginapi/types.go).
- [Current plugin lifecycle types](https://github.com/router-for-me/CLIProxyAPI/blob/ae3ee3be303fb07fb8173279923413005bf32177/sdk/pluginapi/types.go).
- [Current stock API routes](https://github.com/router-for-me/CLIProxyAPI/blob/ae3ee3be303fb07fb8173279923413005bf32177/internal/api/server_routes.go).
- [Current config example](https://github.com/router-for-me/CLIProxyAPI/blob/ae3ee3be303fb07fb8173279923413005bf32177/config.example.yaml).
- [Current usage publication](https://github.com/router-for-me/CLIProxyAPI/blob/ae3ee3be303fb07fb8173279923413005bf32177/internal/runtime/executor/helps/usage_helpers.go).

The plugin completion contract is asynchronous. Current `CompleteRequestExcept`
starts goroutines and invokes plugins without a durable delivery mailbox. It
does not supply exactly-once or restart-safe completion delivery. A plugin-built
status service would still need its own persistence, scope rules, stale/unknown
handling and failure qualification. This is extensibility, not a finished job API.

## 5. Home is relevant to shared admission, not a status-only dependency

The inspected Home source is
`ad49a63f2e112096e143f80d5fb339d67e50e5f0`.
Its database-backed control plane supports per-credential `max_in_flight` and
`max_in_flight_by_model`, an authoritative `admitted_in_flight` counter, and
separate bounded in-flight observations. Limit changes and observation freshness
are separate contracts; stale observation is not authority to mint capacity.

CPA translates Home concurrency saturation to `429` with
`credential_concurrency_exceeded` and trusted retry guidance. Its current retry
decision returns immediately for that typed busy error. This rejects admission;
it does not durably retain 1,000 rejected requests or drain their producer queue.

Home is self-hostable. Current documentation supports SQLite with exactly one
active Home for strict limits, or PostgreSQL for multiple active Homes. It uses
its own RESP/mTLS protocol; do not assume a separate Redis server is required
merely because the protocol is RESP. Adoption adds a control-plane lifecycle,
database state, certificate identities, credentials/config ownership and rollout
gates. Do not add it just to serve HLLM request status.

For a shared credential used by multiple clients, evaluate Home before inventing
a custom account-wide semaphore. Reuse existing infrastructure only with its
owner's explicit database/network/resource handoff. Qualify compatible CPA/Home
membership, releases, cancellation, restart/release behavior, capability checks
and aggregate mixed-client admission. See
[strict concurrency deployment](https://github.com/router-for-me/CLIProxyAPIHome/blob/ad49a63f2e112096e143f80d5fb339d67e50e5f0/docs/README.md)
and [management contracts](https://github.com/router-for-me/CLIProxyAPIHome/blob/ad49a63f2e112096e143f80d5fb339d67e50e5f0/docs/management/api.md).

A concurrent-call cap is different from a request-start rate or subscription
quota. Home features do not establish that the earlier 200-start/61-second
operating estimate is a fixed provider rule, or that 128 is a physical upstream
limit. A producer still needs pacing and an explicit wait/retry policy.

## 6. Why the HLLM status resource belongs in HLLM

One accepted HLLM request may perform zero CPA calls on cache hit, one ordinary
generation, or several calls under its existing validation/repair/recovery
policy. CPA sees its own executions, not HLLM's final application outcome.

Example: CPA returns syntactically invalid model output. Its local request may
have succeeded, while HLLM validates, repairs or ultimately fails the operation.
A CPA-only status endpoint cannot truthfully mark that HLLM request complete.
HLLM can also wait or reject before any CPA request exists.

Authentication reinforces this boundary. HLLM derives scope from its validated
caller bearer. CPA currently has one configured upstream access key and does not
receive those HLLM bearer scopes. An untrusted tracking header cannot establish
caller ownership. Moving lookup into CPA would require a new trusted identity
handoff or duplicate client-key management while leaving end-to-end aggregation
in HLLM. It would also bind HLLM status to one upstream provider deployment.

Recommended responsibilities:

| Owner | Responsibility |
| --- | --- |
| HLLM | Caller tracking ID, validated key scope, complete operation state, existing deadline/cancellation and metadata retention |
| CPA | Native transport, credential selection, trusted refusal/reset hints and native attempt diagnostics |
| CPA/Home if adopted | Shared credential admission across participating clients/nodes |
| Existing producer/Temporal path when separately implemented | Durable pending work, job recovery and client-independent results |

## 7. Next work and qualification boundary

1. Implement the focused [request-status plan](../../../../request-status-lookup-implementation-plan.md) in HLLM without a new CPA dependency.
2. Keep shared CPA settings unchanged while preparing a pinned v8 upgrade candidate. Reconcile current v8 configuration, existing management consumers and the cancellation acceptance oracle.
3. First run isolated synthetic refusal/retry/stream/cancellation/collection gates on the candidate; do not send 1,000 real calls to qualify an image upgrade.
4. Evaluate Home only for the separate aggregate credential-admission requirement, using its existing policy and counter contracts rather than a custom replacement.
5. If CPA-native diagnostics remain insufficient, prototype a small allowlisted interceptor/lifecycle extension. Add a core patch only for a demonstrated missing hook, incorrect hint or lifecycle defect.
6. Qualify any retry-policy change against all shared consumers. `request-retry=0` is a candidate for an external durable retry owner, not an automatic global setting; per-credential overrides still affect every user of that credential.
7. Preserve one usage-queue consumer. Prefer the existing collector/metadata path for quota diagnostics; no second destructive queue reader or raw request logging is authorized by this audit.

No new model calls, throughput measurements, upstream test-suite passes or
upgrade/deployment acceptance are claimed. All source checkouts and patch checks
were isolated from the deployment repositories and runtime state. Shared runtime
configuration, credentials and container identity were left unchanged.
