# 1. Architecture and ownership

This is the accepted target contract in
[the v5.3 execution plan](../plans/restore-reference-workflows-decoupling-plan.md)
and ADR-HLLM-033/034. The phase ledger distinguishes source, tests and deployed
acceptance; this document alone does not claim that the target is live.

## 2. Libraries, composition and services

| Component | Ownership |
| --- | --- |
| Stateless root Go core | Retry/repair/recovery decisions, validation, budgets, cache identity, attempt pricing and accounting; injected effects only. No auth, SQL, profiles or telemetry SDK. |
| Public `stateful/` library | Concrete provider/observer composition; key-scoped profiles, credentials, cache, drafts, history and Garage artifacts; one shared pgx pool. |
| Go gateway | One bearer validator for `/v1` and `/api/v1`; protocol conversion, scope derivation and HTTP health/status. One execution engine. |
| `HardenLlmUI` Phoenix package | REST client/decoders, picker/editor, input/response/stats/history/trace components and shared CSS/JS. No Ecto, host module or Portal dependency. |
| Phoenix host | Routes, shared Portal admission, component contexts and async orchestration; REST calls only after P03 transfers persistence. |
| Shared infrastructure | Agent Platform PostgreSQL for HLLM's isolated database/role; Garage for artifacts; Control Plane/Portal for humans; Caddy for one origin; Laminar for diagnostic OTEL only. |

The gateway serves ordinary OpenAI clients at `https://harden-llm.prls.co/v1`
and reference operations at `/api/v1`, with UI routes on the same origin.
Ordinary clients use `GET /v1/models`, `POST /v1/chat/completions` and
`POST /v1/responses`. Direct API requests do not enter product history.
Both REST surfaces use `Authorization: Bearer <HLLM access key>` alone.
`api_key_hash = hex(SHA256(validated_key_bytes))` is derived server-side and
scopes all application state. A hash or user/session field cannot authenticate
or select another scope. Control Plane availability is independent of ordinary
cache-off inference. Release SQL transactions before any provider work.

## 3. Configuration and complete profiles

The connection JSON requires `api_key_envs` naming protected environment values.
Initially it names `HARDEN_LLM_TOKEN`, also used by the reference host. Provider
credentials such as `CPA_API_KEY` are separate. Git stores preset IDs, origins,
models, prices and options; protected deployment environment stores incoming,
provider, database and encryption secrets. Libraries accept typed values and
never parse `.env` or generate incoming keys at startup.

Selecting a preset copies one complete draft. Explicit create-on-save inserts a
new immutable UUID/date/suffix profile snapshot containing provider definition
and all run inputs; no live Git inheritance, update path or implicit reseeding.
Mutable drafts and entered credentials have revision-based concurrency. Entered
provider credentials are encrypted with the key-hash/credential/origin binding;
missing bindings fail before dispatch, without substituting deployment keys.
Save/import preserve the baseline explicit, bounded provider probe; refresh
only discovers models. No deployment smoke check invokes a paid provider.

## 4. Reference history and artifacts

Only reference runs enter the wrapper-owned PostgreSQL history. Direct `/v1`
calls do not. The same key shares profiles/history/statistics/downloads across
clients and Portal logins; each reference login keeps a separate draft using
`draftKey=workspace:<stable Portal user ID>`. Draft keys are selectors inside the
key scope, not a second authorization system. There is no local user table.

Recording is separate from inference: disabled, configuration error, saved,
not saved, unconfirmed and failed reads have distinct messages. Insert once;
after unknown COMMIT, one readback can confirm saved only if found. Empty or
failed readback remains unconfirmed. Never poll, repeat the insert or replay
inference. Records remain until key-scoped delete/clear; status exposes growth.

Garage paths are `artifacts/<api_key_hash>/<run_id>/<artifact_id>`. Authorized
downloads work without a history row. Delete/clear remove the run prefix before
the row; partial failure retains the row. Orphans from unrecorded runs need
manual operator cleanup. No artifact journal/reconciler, second history store,
expiry scheduler or Laminar history integration exists.

## 5. Model Recall host interface review

Read-only review on 2026-10-12 inspected `masked-recall/web/mix.exs`,
`web/lib/model_recall_web_web/router.ex`, `live/experiment_live.ex` and
`components/operation_status.ex`. Its Phoenix 1.8.9/LiveView 1.2.10 host uses
shared PrlsWeb product admission, server-owned `start_async` orchestration and
function components. Reusable HLLM components need only Phoenix/Req and a
host-supplied REST context (`api_key`, `draft_key`, base URL), explicit component
IDs, admission/login paths and instance-tagged messages. Keep Model Recall's
session/product policy in its host; do not import it into the package or copy
its automatic workflow retries into inference. An independent test host proves
reuse. Model Recall migration is outside this release.

## 6. Production and acceptance

Run one Go and one Phoenix application using shared services. The accepted
image's one-shot schema command must exit 0 before production application
startup; the existing apply path retains `--no-deps`. Preview schema startup
uses its Compose dependency. This is a command, not another long-lived service.

Restore UI markup/styles/interaction contracts at
`754690c8d0890c664d69fbdf526b16b3e5601060` (PR #95, before PR #97).
`test/fixtures/reference-workflows.json` freezes provenance and capabilities.
Use the lowest sufficient Go/LiveView/Node/storage tier and retain test purposes.
Release evidence records branch/SHA/images/owner revisions/URL and HTTP checks;
browser layout and live-provider compatibility require explicit separate runs.

Use `make test-fast` for application checkpoints, `make verify` for backend
integration and `make test-release` for browser-free release certification.
