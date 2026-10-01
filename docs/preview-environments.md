# Branch development environments

Identity and login sections dated before 2026-09-30 describe the former local
guest/operator design. ADR-HLLM-029 and section 3 below define the current
Control Plane authority and host-only product sessions; the old credentials and
`login.txt` workflow have been removed.

## 1. Daily workflow

Use `dev` for normal iteration. Its persistent URL is
<https://harden-llm-dev.prls.co/>. Production remains
<https://harden-llm.prls.co/> and is promoted only on explicit request.

Run `make test-fast`, commit, and push. Push/PR CI runs the same browser-free
T0–T2 checks. Passing pushes update enabled previews. Markdown/docs-only pushes
skip the test hierarchy and application builds. Repository-managed security
checks on `main` remain independent. Application builds use Docker layer caching;
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

## 3. Identity and data ownership

Human accounts, passwords, memberships, and product access are owned by the
PRLS Control Plane. HLLM has no guest/operator users, local login/password
store, or `login.txt`. Each HLLM host keeps an encrypted `__Host-harden_llm_web`
cookie with no `Domain` attribute; Control Plane sessions are not shared across
`*.prls.co`. Product access is checked against current Control Plane state on
each gateway request.

The trusted preview host reads one protected `.env` through `sharedEnvFile`
(reference host: `/home/kirill/p/harden-llm/.env`). Profile JSON and
credential-variable references come from the absolute `HARDEN_LLM_CONFIG_FILE`
path. `HARDEN_LLM_PROFILE_ACCOUNT_IDS` explicitly names which Control Plane
accounts receive synchronized profile configuration; no email lookup or local
account creation occurs. Provider keys/profile settings are shared only across
trusted deployments. Infrastructure credentials, encryption keys, databases,
machine tokens, and artifacts remain deployment-specific. See [shared
configuration](shared-llm-configuration.md) for updates and rotation.

Each preview owns its product data: branch Postgres, Garage objects, application
network, and disposable volumes. The browser cookie is host-only and does not
need a separate session-vault volume. Automated health/configuration checks do
not submit an LLM run or incur provider charges.

Each branch owns a Compose project containing Phoenix, Go gateway, Postgres,
and Garage, with a private network and separate persistent volumes. The
existing OpenAPI boundary is unchanged. Application history, profiles, traces,
and stats remain in branch Postgres; artifact payloads remain in branch Garage.
Diagnostic logs are bounded.
OTLP exports are disabled in previews: no preview Langfuse, Luminar, ClickHouse,
or production telemetry dependency is introduced.

## 4. Iteration efficiency and isolation boundaries

The fast loop shares Caddy, its Cloudflare tunnel, the Control Plane identity
service, and deployment runner. Each preview still owns its Postgres database,
Garage layout, application network, volumes, and disposable product data.

Application images use BuildKit Go module/build caches and stable dependency
layers. CI caches the pinned Phoenix dependencies and compile output. For a
service whose application inputs changed, a deployment reuses an existing
local image only when both its service label and exact source-SHA release
label match; otherwise it builds a new image. Services whose application
inputs did not change retain their previously verified immutable image, so
control-plane/docs-only changes do not force an application rebuild. Control
files, routes, environment files, and profile synchronization are
content-aware, so an unchanged deployment does not rewrite or reload them.
Health checks probe quickly during startup and less frequently after startup;
preview services have conservative memory and CPU limits to prevent idle
branches from consuming unbounded host resources.

The gateway has a 256 MiB container limit and `GOMEMLIMIT=192MiB`, leaving
headroom for native/container overhead. These limits originally accounted for
local Argon2 password checks, which have since moved to Control Plane and were
removed from HLLM. Keep the runtime limits until fresh deployment measurements
justify a change; idle usage alone is not evidence for reducing them.

The measured dev baseline before these changes was approximately 199 MiB for
the four idle containers (gateway 11 MiB, web 142 MiB, Postgres 33 MiB, and
Garage 13 MiB). The last application-bearing fast CI run took about four
minutes and the preview reconciliation took about 26 seconds; subsequent
no-change reconciliations are expected to reuse images and leave unchanged
containers in place.

Postgres and Garage are deliberately not shared between previews yet. Sharing
them would reduce idle memory but would add database/schema ownership,
per-branch bucket prefixes and credentials, cleanup, and noisy-neighbor
coordination. Revisit that tradeoff when concurrent preview count and measured
resource pressure justify the additional operational surface.

## 5. Host architecture and setup

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

For the first setup, supply a Harden-LLM-owned Cloudflare token through
`HARDEN_LLM_PREVIEW_CLOUDFLARE_API_TOKEN`. A non-echoing prompt in Bash avoids
putting the token value in shell history:

```bash
read -r -s -p 'Harden-LLM preview Cloudflare token: ' HARDEN_LLM_PREVIEW_CLOUDFLARE_API_TOKEN
export HARDEN_LLM_PREVIEW_CLOUDFLARE_API_TOKEN
node scripts/setup-preview-host.mjs
unset HARDEN_LLM_PREVIEW_CLOUDFLARE_API_TOKEN
```

Once host configuration exists, setup reuses its stored credential:

```sh
node scripts/setup-preview-host.mjs
systemctl --user start github-actions-harden-llm-preview.service
systemctl --user status github-actions-harden-llm-preview.service
# After the trusted preview workflow has finished:
systemctl --user stop github-actions-harden-llm-preview.service
```

Requires Docker, Node, Git, `flock`, authenticated `gh` with runner registration
authority, and a Cloudflare token allowed to read the `prls.co` zone, edit its
DNS records, and manage Cloudflare Tunnels in its account. Bootstrap requires
the explicit environment variable when `host.json` does not exist; it never
imports host SSH credentials. The setup creates a **new** tunnel
named `shaman-harden-llm-preview`; it does not change the production tunnel.
It installs the checksum-verified GitHub runner and a persistent user service.
The service is disabled for automatic startup so it does not consume RAM while
idle. Start it before a trusted preview workflow; jobs requiring the
`harden-llm-preview` label remain queued while it is offline. Setup disables the
unit but leaves an already-running runner alone. To keep the runner online at
all times, explicitly run `systemctl --user enable --now
github-actions-harden-llm-preview.service`. User lingering must be enabled for
operation after logout (already enabled on the reference host). Runner
auto-updates remain GitHub-managed.

Host credentials/config live in `~/.config/harden-llm-preview/host.json` (0600).
The bootstrap token is stored there for subsequent operations. Supplying the
environment variable to an already configured host does not rotate that stored
token. Manage its rotation separately from host SSH DNS credentials, preserving
all other host configuration fields. Before revoking a legacy shared token,
confirm that every former consumer has migrated to its own credential.
State lives in `~/.local/share/harden-llm-previews` (private). Cloudflare is
configured through its [remote tunnel API](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/get-started/create-remote-tunnel-api/).
DNS writes refuse existing records without the expected ownership marker.

## 6. Deployment checks and recovery

The trusted workflow requires passing fast (or browser-free release) checks
for the exact current branch SHA. It rechecks the branch after builds, uses a
host lock for router/state mutations, and records component image IDs and
release labels separately from the branch SHA. A docs/test-only update need
not change component identities. Superseded CI revisions are ignored.
Compose pins immutable local image IDs, so a shared tag rebuilt for another
branch cannot change this environment's next restart or rollback.

Deployment checks healthy container/image identity and public `/healthz`,
`/readyz`, and login-page delivery. Those HTTP probes do not authenticate a
user. Separate release acceptance must verify PRLS Control Plane sign-in,
current HLLM product access, and host-local logout/session revocation. HLLM no
longer has a product-owned API password login or session endpoint. These checks
are not a claim of browser layout or LiveSocket certification.
First-time hostname creation allows up to five minutes for Cloudflare route
propagation; updates to an existing hostname use a 90-second readiness budget.

On failed updates, previous application images are restored and branch data
is retained. This is **not database migration rollback**: for incompatible
development migrations, explicitly recreate the disposable feature preview.
Failed initial setups retain their scoped resources for inspection/retry or
explicit removal. Inspect only the named preview project and private state;
never collect complete environment dumps into issue/workflow logs.

`AGENTS.md`, ADR-HLLM-015, and TEST-062 define this policy. Production deployment
procedures remain separate; this feature does not promote or restart production.

### 6.1 Recovery policy cutover (ADR-HLLM-020)

This change requires matching callers, gateway, Phoenix and configuration.
It is not an ordinary image-only update. Implementation tests do not authorize
an operational cutover. Use this sequence for an explicitly approved environment:

1. Prepare every maintained caller and the configuration named by
   `HARDEN_LLM_CONFIG_FILE` for profile/state/bundle version 2 and RunResult
   version 3. Every profile and run request carries a complete `recoveryPolicy`.
   Use the [current configuration example](../config/llm-profiles.example.json)
   and [API examples](api-and-library.md). External files and exported bundles
   are not database rows and are not converted by migration 6. Do not modify the
   shared active file while other environments still run the old contract.
2. Stop old writers, including old gateway/frontend instances and administrative
   jobs. Take the normal recoverable database, private configuration and component
   checkpoint; record source SHA and both component image identities. Preserve
   each environment's own encryption keys, data and account bindings.
3. Run the matching gateway's normal embedded migration/startup path. Migration
   `0006_recovery_policy.sql` is one transaction under the existing advisory lock
   and applied-version record. The prior canonical-history cutover must already
   have produced RunResult version 2. Unsupported/invalid documents fail with
   their owner/document identity and field; the transaction remains unapplied.
   Correct the identified source preference before trying again; do not reset
   histories or suppress the validation.
4. Start the matching gateway/Phoenix and select the prepared current-format
   configuration. The existing trusted `sync-profiles` command provisions it
   for both guest and operator accounts. It never acts as a database converter.
   Migration preserves credential bytes; a later provisioning/rotation command
   has its existing separate credential semantics.
5. Check HTTP readiness, guest/operator authentication, profile configuration
   read-back and required `result.defaults.recoveryPolicy`, current saved state,
   history and traces. Record branch, source SHA, component images, environment
   URL and results. Use existing administrative read-back checks, with no
   interactive profile-save probes, automatic provider call or browser test.

The mapping preserves explicit false flags and zero delays. Missing historical
settings use the fixed mapping in ADR-HLLM-020, including four attempts and
500/8000 ms backoff; invalid attempt counts/types/ranges abort. A valid flat
profile setting takes precedence over its nested repair counterpart. Invalid
mapped values are rejected even when another value would override them.
Original run requests, observations, artifacts and credentials are retained.
Old requests remain evidence and cannot be rerun by inventing a current policy.

An old binary cannot roll back migrated formats. Keep the deployment on an image
compatible with the current database and fix incompatible changes forward;
there is no database restore path. Structured cache keys use response
projection v2; old structured entries are not read on a miss. Text cache
semantics remain unchanged.

## 7. Initial verification (2026-09-11 UTC)

Current verified dev application revision:
`da00a4aa7faa998d000806fdb60da96b38620c90`.
[Fast CI](https://github.com/prls-co/harden-llm/actions/runs/34558229654),
[automatic deployment](https://github.com/prls-co/harden-llm/actions/runs/34558512418),
and [no-change redeployment](https://github.com/prls-co/harden-llm/actions/runs/34558667305)
all passed. The latter rebuilt no images and retained identical application
container IDs. Public health/readiness/login and authenticated API checks
passed again afterward. Documentation-only closure does not replace these
application-bearing identities:

- Gateway image: `sha256:4da0f9e3e76d45d7e022f2b0ea5e0516aa90107ee06af77c43de87c2e756f1e5`.
- Web image: `sha256:27cc44573ed8d4ab8cf00c6f772ffc72973e904f9f33250b0f3c703698fdc403`.

- Local `make test-fast`: eight tasks passed; current script suite: 17 tests
  passed; `go test ./internal/testkit -count=1` and whitespace validation passed.
- Initial dev application revision `d5a239cf1ae480c5680831b2f2c8417a79e29706`:
  [browser-free CI](https://github.com/prls-co/harden-llm/actions/runs/34557244800)
  and [authenticated deployment](https://github.com/prls-co/harden-llm/actions/runs/34558064002) passed.
- A separate `preview/setup-canary` URL passed
  [deployment](https://github.com/prls-co/harden-llm/actions/runs/34558065617).
  A valid dev bearer session returned 200 in dev and 401 in that preview;
  the test session was revoked. No LLM/provider call was made.
- Deleting the canary branch triggered successful
  [automatic cleanup](https://github.com/prls-co/harden-llm/actions/runs/34558277373).
  Its DNS record, state/worktree, containers, network, and volumes were absent
  afterward. Dev retained the same healthy container IDs and public 200s.
- Production web/gateway container IDs and image digests remained unchanged
  and healthy throughout. No browser or production deployment was performed.

## 8. Latest efficiency verification (2026-09-11 UTC)

Control-plane revision `85cd3f346521401ee0aaa4feed02d3353516eaf5` passed
[dev fast CI](https://github.com/prls-co/harden-llm/actions/runs/34627180082),
[main fast CI](https://github.com/prls-co/harden-llm/actions/runs/34627180400),
and the [trusted dev deployment](https://github.com/prls-co/harden-llm/actions/runs/34627571985).
The deployment rebuilt no application image and retained the exact verified
application images from `cfea235`:

- Gateway: `sha256:f16fee16a297154e06c3e7636802c190e9a5085f012cd29d81d1356856636859`.
- Web: `sha256:4136f249378ee8797cc4da5d2023efc2827f96199641f9ae1460fc68f10fb81a`.

The live dev preview returned HTTP 200 for `/healthz`, `/readyz`, and `/login`.
All four Compose services were healthy with the configured limits: Postgres
and Garage 256 MiB/0.50 CPU each, gateway 128 MiB/0.50 CPU, and web 512 MiB/1
CPU. This verification used HTTP and Docker inspection only; no browser or
real provider call was made.

## 9. Final control-plane verification (2026-09-11 UTC)

Control-plane revision `b947a84dd3f5f3ff35324034952408a9e20c25ad` passed
[dev fast CI](https://github.com/prls-co/harden-llm/actions/runs/34627945820),
[main fast CI](https://github.com/prls-co/harden-llm/actions/runs/34627945003),
and the [trusted dev deployment](https://github.com/prls-co/harden-llm/actions/runs/34629012917).
The deployment reported `rebuiltServices: []`, retained the verified gateway
and web image identities from `cfea235`, and reconciled all four Compose
services. Live `/healthz`, `/readyz`, and `/login` returned HTTP 200; all four
services were healthy with the configured CPU and memory limits. No browser,
real provider call, or production restart was performed.

Startup regressions found during bring-up are covered by the cheap policy
tests: quoted Compose tmpfs options, gateway development-mode telemetry policy,
HTTPS forwarding to Phoenix, and immutable image references. Initial routing
also established the separate, bounded Cloudflare propagation budget above.

### Operator login alignment (2026-09-11)

Per user request, dev's existing `preview-local` account now uses the production
operator email/password. Its owner ID, profiles, runs, and provider credentials
were preserved; the old generated login was disabled and its sessions revoked.
Production's account was not modified. HTTP checks verified both logins and
that a production bearer token remains invalid in dev. New preview creation
uses the same approved login source, but still generates separate service and
session secrets. No application image rebuild/restart or browser was needed.

### Guest login correction (2026-09-11)

The preceding operator alignment did not provision `TEST_LOGIN`. The existing
guest account authenticated successfully in production but returned 401 in dev.
Dev now has its own `guest` account created with the exact `TEST_PASSWORD` from
`.env`, without changing the operator or copying production data. HTTP login,
session, profiles, history, and logout passed. The dev-branch provisioning fix
also checks for this separate guest account and creates it when missing; it
does not overwrite an existing account. That automation change becomes active
for future previews when promoted to the trusted `main` orchestration branch.

### Shared provider/model configuration (2026-09-11)

`934e00a` promoted the guest provisioning fix and shared configuration tooling
to trusted `main`. Guest/operator accounts now receive the same `.env`-managed
model setup and provider keys in every enabled preview. Production received
the same configuration without replacing its application images. See the
[verified rollout](shared-llm-configuration.md#4-verified-rollout--2026-09-11)
for exact image identities, browser-free evidence, and update/rotation semantics.
