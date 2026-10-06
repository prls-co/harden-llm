# Self-hosting

Harden LLM runs as a Go proxy, a Phoenix reference app, and PostgreSQL for
frontend-owned history and drafts. Shared Caddy owns the public listeners. The
Go gateway itself has no application database or human-login dependency.

## Configure

1. Copy `.env.example` to `.env`; keep `.env` private and mode 0600. Set
   `HARDEN_LLM_TOKEN`, `CPA_API_KEY`, database and Phoenix secrets, the shared
   Control Plane and Portal settings, public hostnames, and release identity.
2. Copy `config/upstreams.example.json` to an ignored local file such as
   `config/upstreams.local.json`. Keep its connection metadata unchanged unless
   the upstream contract requires a deliberate edit. It contains the name
   `CPA_API_KEY`, not the credential. Set `HARDEN_LLM_CONFIG_FILE` in `.env` to
   that file's absolute path.
3. Ensure the Docker host is joined to the existing `prls-observability`
   network and has the private-module build token available to the Compose
   process when building images.

`HARDEN_LLM_TOKEN` is the bearer for `/v1/*` and the same bearer the Phoenix
server uses. Standard OpenAI SDKs point to `https://<api-host>/v1` and use this
token. `CPA_API_KEY` authenticates only the configured gateway-to-CPA request.
See [the environment reference](environment.md) and
[connection configuration](shared-llm-configuration.md).

## Run the reference database migration

The Phoenix release owns the history schema. Start PostgreSQL, build the
services, run the one release migration command, then start the application:

```bash
compose=(docker compose -f docker-compose.yml -f deploy/frontend/compose.frontend.yml)
"${compose[@]}" config --quiet
"${compose[@]}" up -d --build harden-postgres
"${compose[@]}" build harden-llm-gateway harden-llm-web
"${compose[@]}" run --rm --no-deps \
  -e PHX_SERVER=false harden-llm-web eval HardenLlm.Release.migrate()
"${compose[@]}" up -d --wait --wait-timeout 300
```

The command uses the pinned release image and existing PostgreSQL volume. It
does not contact an LLM provider. Run it once before the web service starts for
a fresh installation and after a release that adds a migration.

## Access and history

Human authentication and enabled-login checks remain in the shared Control
Plane and Portal. Any enabled login can enter the reference app; there is no
company selector or HLLM-specific grant. The host-only Phoenix cookie carries
the shared identity session.

The Phoenix workspace records its completed `/v1/responses` requests and
outcomes in PostgreSQL. All enabled logins share this history. Direct requests
to the Go API are stateless and are never recorded. Workspace drafts are scoped
to the stable user ID. A history-storage failure does not repeat inference.

## Browser-free checks

Check local service health and readiness without making an upstream inference
request:

```bash
curl --fail-with-body -sS https://<api-host>/healthz
curl --fail-with-body -sS https://<api-host>/readyz
curl --fail-with-body -sS https://<web-host>/healthz
```

`/healthz` and `/readyz` are process/configuration checks; they do not prove
that a browser flow or paid-provider request works. Use `make test-fast` and
the documented browser-free release gates for code verification. Browser and
live-provider checks remain explicit opt-ins.
