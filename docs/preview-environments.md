# Development and branch preview environments

## Daily workflow

Use `dev` for normal iteration. Its persistent URL is
<https://harden-llm-dev.prls.co/>. Production is
<https://harden-llm.prls.co/> and is promoted only on explicit request.

Run `make test-fast`, commit, and push. Push and PR CI run browser-free checks.
Docs-only changes skip application builds. A gateway-only or web-only change
rebuilds its affected service. Neither CI nor automatic deployment launches a
browser or makes an LLM inference call.

Browser assertions remain opt-in through `make test-browser` and
`make test-browser-compose`; `make test-release` is browser-free. A request to
deploy does not authorize a browser run.

## Branch previews

Create feature branches from the current `dev` or `main` policy. Enable a
trusted same-repository branch with the preview workflow or the `deploy:preview`
PR label. Fork code is never built or deployed on the preview host. Preview
URLs use `https://harden-llm-<branch-slug>-<10-character-hash>.prls.co/`, with
`dev` as the special persistent environment.

The branch owns its URL and preview data. Closing a PR or deleting its branch
removes that preview. Manual cleanup uses the preview workflow's `destroy`
action and removes only the matching DNS record, route, containers, network,
volumes, and deployment worktree. Preview history and drafts are disposable;
they have no automatic recovery. Do not run broad Docker prune commands on the
shared host.

## Identity and application data

Human accounts and enabled-login access belong to the PRLS Control Plane and
Portal. Each preview uses the shared login flow. Every enabled login in that
environment sees the same Phoenix call history; the preview's own PostgreSQL
volume isolates it from other previews and production. Workspace drafts are
scoped to the stable user ID. Direct `/v1` API calls are never recorded.

The trusted preview host reads the protected shared application environment
from its configured `sharedEnvFile`. The gateway connection file is selected by
`HARDEN_LLM_CONFIG_FILE`; its upstream secret is supplied through
`CPA_API_KEY`. `HARDEN_LLM_TOKEN` is the single incoming API bearer and is also
used by Phoenix. These application credentials are available only to trusted
branches because they authorize access to the configured provider. Infrastructure
credentials, session-signing keys, and database passwords are environment-
specific. No profiles are synchronized and no HLLM users are provisioned.

Each branch Compose project contains Phoenix, the stateless Go gateway,
PostgreSQL, and bounded web logs. There is no branch Garage service. The
gateway mounts the connection file read-only; Phoenix owns the per-environment
history database. Preview telemetry export is disabled. Automated health and
configuration checks make no provider request.

## Host and cleanup boundaries

A dedicated Caddy router and Cloudflare tunnel serve preview hostnames. The
router joins each branch's private network; application containers do not
share an ingress network with sibling branches or production. HTTPS terminates
at Cloudflare. `/v1/*`, `/healthz`, and `/readyz` route to the gateway; the app
host routes to Phoenix. `/metrics` is not public.

One repository-scoped self-hosted runner executes orchestration from the
trusted default branch, never from the PR checkout. Service images are reused
only when both the service label and exact source-SHA release label match.
Control files and routes are written only when their contents change.

The current preview topology uses conservative per-service CPU and memory
limits and separate PostgreSQL volumes. Keep those boundaries until measured
concurrency or resource pressure justifies a simpler alternative. The backend
configuration and migration flow are implemented in
[`scripts/preview-environment.mjs`](../scripts/preview-environment.mjs) and
[`deploy/preview/compose.yml`](../deploy/preview/compose.yml).
