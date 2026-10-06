# Harden LLM Web

This Phoenix LiveView application is the browser-facing reference client for
the Go OpenAI-compatible proxy. It uses shared PRLS Web authentication and the
Control Plane access contract, then calls the Go gateway through
`../api/openapi.yaml`. Phoenix owns HTML, CSRF, the encrypted host-only session
cookie, shared reference history, and ordered session/component drafts. The Go
gateway owns the stateless `/v1` API, provider credentials, execution, recovery,
cache policy, and diagnostics. Human identity and access remain owned by the
shared identity services.

## Shared login and history

Only PRLS Portal hosts password forms. Unauthenticated HTML requests and access
denials go to `PRLS_PORTAL_URL` with the protected return path. Each enabled
login enters directly without company selection or an HLLM grant. All enabled
logins share Phoenix history. Direct gateway calls never create
history records. Phoenix records only submissions made through this client,
after showing the returned outcome. A history-storage failure leaves that
outcome available and reports that history could not be saved.

The browser calls the gateway with the existing `HARDEN_LLM_TOKEN`; the
gateway uses `CPA_API_KEY` only for its configured upstream. Neither token is
sent to the browser. The gateway has no profiles, human-user ownership, or
history routes.

## Local development

Use Elixir 1.20.2 on Erlang/OTP 28.4.3. Start the Go gateway separately on
`http://127.0.0.1:8080`, configure its one or more upstream connections, and
provide the same `HARDEN_LLM_TOKEN` to Phoenix and the gateway. Then:

```bash
mix setup
mix ecto.migrate
mix phx.server
```

Visit `http://localhost:4000`. Run Portal at the configured `PRLS_PORTAL_URL`
(default local origin `http://localhost:4200`) with Control Plane; passwords
are submitted there. Development sessions are non-production. Production
requires independent signing/encryption salts, a 64-byte secret key base,
HTTPS, and the Compose topology.

## Verification

The normal edit loop is repository-root `make test-fast`. From this directory,
`mix test` runs the deterministic Phoenix and LiveView suite. Database tests
run through the existing tier runner against PostgreSQL:

```bash
mix format --check-formatted
mix compile --warnings-as-errors
mix test
mix deps.audit
mix hex.audit
MIX_ENV=prod mix assets.deploy
MIX_ENV=prod mix release
```

Browser tests are opt-in and run only when specifically requested. The browser
canary checks shared login, one standard `/v1/responses` request, and the
frontend history failure boundary; it is excluded from deterministic tests.
`make test-release` is browser-free. See
[`../docs/liveview-go-testing-guidelines.md`](../docs/liveview-go-testing-guidelines.md)
for the test tiers and evidence policy.

## Production

The Phoenix release runs in `deploy/frontend/compose.frontend.yml`, layered
over the stateless proxy in `docker-compose.yml`. The frontend overlay owns the
PostgreSQL service used for reference history and drafts. Caddy remains the
only public-port owner. The browser talks only to Phoenix; Phoenix calls the
Go API server-side.

Operational setup, backup, and upgrade procedures are in the
[`self-hosting guide`](../docs/self-hosting.md).
