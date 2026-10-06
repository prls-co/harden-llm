# Architecture and ownership

Harden LLM has one provider-execution path and keeps reference-app state out of
the public proxy.

```text
OpenAI client ──> shared Caddy ──> Go gateway ──> hardenllm.Client.Call ──> upstream
                                      ▲
                                      │ /v1/models and /v1/responses
browser ──> shared Caddy ──> Phoenix LiveView ────────────────────────────┘
                                  │
                                  ├── Control Plane: login and access
                                  └── PostgreSQL: shared call history and per-user draft
```

## Component boundaries

| Component | Owns | Does not own |
| --- | --- | --- |
| Root Go library | Provider payloads, retries, validation/repair, cache identity, usage and results | Environment loading, auth, SQL, HTTP routes, UI |
| Go gateway | OpenAI-compatible HTTP, one startup connection catalog, bearer auth, API conversion and health | Human identity, profiles, history, persistence, login, UI |
| Phoenix reference app | Shared PRLS sign-in, workspace, server-side calls, shared history and user-scoped draft | Provider SDKs, provider credentials, duplicate inference logic |
| Control Plane and Portal | Human authentication and current access | HLLM inference configuration or call history |
| PostgreSQL | Phoenix reference calls and drafts | Gateway inference state or identity authority |
| Shared Caddy | Public TLS and host/path routing | Application authorization or service ownership |

`api/openapi.yaml` is the only Go-to-Phoenix contract. The frontend is a normal
OpenAI-compatible client of the gateway and sends `HARDEN_LLM_TOKEN` from its
server process. That value is also the bearer used by external API clients.
`CPA_API_KEY` is available only to the gateway and authenticates its configured
upstream.

Human logins are access checks. Every enabled login may use the reference
application; no company picker, HLLM grant, or user-owned history is involved.
The shared history contains only calls made through the Phoenix workspace.
Direct API calls are stateless and are not recorded. Workspace drafts are
private to the stable Control Plane user ID.

## Configuration and inference

The gateway reads one connection-only JSON file at startup. It contains the
upstream ID, provider, protocol, base URL, default connection, and the name of
the environment variable holding the upstream key. Secrets do not go into the
JSON. The deployment uses `.env` for `HARDEN_LLM_TOKEN` and `CPA_API_KEY`.
Clients select native model IDs in each request; there are no profile records,
profile endpoints, or per-model presets.

The OpenAI-compatible API supports `/v1/models`, `/v1/chat/completions`, and
`/v1/responses`. Both inference routes convert into the same root Go request and
call the same engine. Unsupported request fields fail validation before
provider dispatch. Streaming returns the final hardened result as standard SSE;
it does not expose provisional provider tokens.

The gateway has no application database dependency. Phoenix owns its single
Ecto Repo for the reference records, using the existing PostgreSQL service.
There is no history REST API, history write in the gateway, profile adapter,
or second history store.

## Reference records and privacy

All enabled logins see the same call history. The frontend records the observed
request and outcome after the proxy call completes; a process crash can lose a
record, and a history-store failure never repeats inference. Direct API requests
never enter this table. Calls can be restored into the workspace, downloaded,
deleted, or cleared by any enabled login.

Drafts are isolated by stable user ID and a single workspace namespace. Both
history and drafts are bounded and redact credential-shaped fields before
storage. The host-only encrypted Phoenix cookie keeps the shared login session;
Control Plane remains authoritative for enabled identity and access.

## Deployment and verification

The deployment is one Linux Docker host with the gateway, Phoenix web app,
PostgreSQL, and shared observability services. Only shared Caddy publishes
public HTTP/S. The production route sends `/v1/*`, `/healthz`, and `/readyz` to
the gateway and sends the reference-app host to Phoenix. The gateway readiness
check validates startup configuration, not upstream availability or a paid
provider call.

Use `make test-fast` during edits, `make verify` for backend integration, and
`make test-release` for browser-free release certification. Browser and live
provider checks remain opt-in under `AGENTS.md`; a deployment or health check
does not imply browser or provider acceptance.
