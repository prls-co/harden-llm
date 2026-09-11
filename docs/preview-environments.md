# Branch development environments

## 1. Daily workflow

Use `dev` for normal iteration. Its persistent URL is
<https://harden-llm-dev.prls.co/>. Production remains
<https://harden-llm.prls.co/> and is promoted only on explicit request.

Run `make test-fast`, commit, and push. Push/PR CI runs the same browser-free
T0–T2 checks. Passing pushes update enabled previews. Markdown/docs-only pushes
skip CI and application builds. Application builds use Docker layer caching;
frontend-only changes rebuild/restart only Phoenix, backend-only changes only
the Go gateway. Initial environment creation builds both.

No browser or real LLM provider is launched by automatic CI or deployment.
`make test-release` and the scheduled broader check are also browser-free.
Existing browser assertions remain available through `make test-browser`,
`make test-browser-compose`, and explicit manual CI suite choices. Agents must
obtain a specific browser-testing request first; “deploy” is not authorization.

## 2. Feature branch URLs

Create branches from current `dev` or `main`, including this workflow policy.
Enable a trusted same-repository branch with either:

```sh
gh workflow run preview-environments.yml --ref main \
  -f branch=feat/my-change -f action=deploy
```

Or add `deploy:preview` to its open PR. If current fast checks have not passed,
enablement requests/waits for them instead of deploying unchecked code. Old
branches must merge current policy before the workflow will dispatch checks.
Fork PRs are never deployed on this host.

URLs are `https://harden-llm-<branch-slug>-<10-character-hash>.prls.co/`;
`dev` is the single special case. The exact URL appears in the deployment
workflow summary and GitHub environment deployment. Calculate it locally:

```sh
node --input-type=module -e \
  'import {branchIdentity} from "./scripts/preview-policy.mjs"; console.log(branchIdentity(process.argv[1]).url)' \
  feat/my-change
```

The branch name, not the commit, owns the URL. Updates preserve its data.
Closing a PR or deleting its branch removes that branch's preview. Removing
the label removes label-enabled previews; manually enabled previews persist
until explicit removal, PR closure, or branch deletion. `dev` is protected from
these cleanup actions. Manual removal:

```sh
gh workflow run preview-environments.yml --ref main \
  -f branch=feat/my-change -f action=destroy
```

Removal deletes only the matching preview DNS record, route, containers,
network, volumes, and clean deployment worktree. Its disposable history,
credentials, and artifacts have **no automatic recovery**. Shared cached build
images remain reusable; do not run broad Docker prune commands on this host.

## 3. Login and data ownership

Each environment starts empty, with a generated local operator login at:

```text
/home/kirill/.local/share/harden-llm-previews/environments/<id>/login.txt
```

Read that private file on the host; never put its password in Git, workflow
logs, or chat. The operator email is `developer@harden-llm.local`. Configure
development provider credentials through that environment's UI if needed.
Production credentials and data are not copied. Automated checks never submit
an LLM run or incur provider charges.

Each branch owns a Compose project containing Phoenix, Go gateway, Postgres,
and Garage, with a private network and separate persistent volumes. The
existing OpenAPI boundary is unchanged. Application history, profiles, traces,
and stats remain in branch Postgres; artifact payloads remain in branch Garage;
Phoenix session material remains in its own volume. Diagnostic logs are bounded.
OTLP exports are disabled in previews: no preview Langfuse, Luminar, ClickHouse,
or production telemetry dependency is introduced.

## 4. Host architecture and setup

A dedicated Caddy router and Cloudflare tunnel serve only preview hostnames.
The router joins each branch network; application containers do not share an
ingress network with sibling branches or production. HTTPS is terminated by
Cloudflare. Flat names fit the existing `*.prls.co` certificate. API and signed
artifact paths use the same branch origin. `/metrics` is not exposed.

One repository-scoped self-hosted runner, label `harden-llm-preview`, executes
orchestration from **main**, never the PR checkout. Only trusted repository
branches may be built: Docker build is not a sandbox for hostile contributors.
See GitHub's [self-hosted runner security guidance](https://docs.github.com/en/actions/reference/security/secure-use).

Initial setup on the reference Linux host:

```sh
node scripts/setup-preview-host.mjs
systemctl --user status github-actions-harden-llm-preview.service
```

Requires Docker, Node, Git, `flock`, authenticated `gh` with runner registration
authority, and the protected operator Cloudflare token at
`~/.config/shaman-public-ssh/cloudflare.env`. The setup creates a **new** tunnel
named `shaman-harden-llm-preview`; it does not change the production tunnel.
It installs the checksum-verified GitHub runner and a persistent user service.
User lingering must be enabled for operation after logout (already enabled on
the reference host). Runner auto-updates remain GitHub-managed.

Host credentials/config live in `~/.config/harden-llm-preview/host.json` (0600).
State lives in `~/.local/share/harden-llm-previews` (private). Cloudflare is
configured through its [remote tunnel API](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/get-started/create-remote-tunnel-api/).
DNS writes refuse existing records without the expected ownership marker.

## 5. Deployment checks and recovery

The trusted workflow requires passing fast (or browser-free release) checks
for the exact current branch SHA. It rechecks the branch after builds, uses a
host lock for router/state mutations, and records component image IDs and
release labels separately from the branch SHA. A docs/test-only update need
not change component identities. Superseded CI revisions are ignored.

Deployment checks healthy container/image identity and public `/healthz`,
`/readyz`, and `/login`. Initial setup also checks API login, session, profiles,
history, and logout with the generated operator. These are HTTP checks, not a
claim of browser layout or LiveSocket certification.

On failed updates, previous application images are restored and branch data
is retained. This is **not database migration rollback**: for incompatible
development migrations, explicitly recreate the disposable feature preview.
Failed initial setups retain their scoped resources for inspection/retry or
explicit removal. Inspect only the named preview project and private state;
never collect complete environment dumps into issue/workflow logs.

`AGENTS.md`, ADR-HLLM-015, and TEST-062 define this policy. Production deployment
procedures remain separate; this feature does not promote or restart production.
