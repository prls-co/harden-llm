# Shared production web ingress transition

- Plan: `PLAN-HLLM-SHARED-CADDY-001`
- Version: 6.0
- Updated: 2026-09-28
- Status: implementation in progress; P05 accepted, P06 and P09 pending
- Owners: Harden-LLM for application-origin configuration and retirement; `prls-co/caddy-shared` for production Caddy and its web Tunnel connector; Ops for the shared-service record.

## Goal and boundaries

Move only the existing production web Caddy router and its Cloudflare Tunnel
connector from Harden-LLM into `caddy-shared`. Keep the route set, tunnel
identity and credential, DNS, Caddy volumes, `prls-observability` network,
application images, and service data unchanged.

SSH and Mosh remain independently owned by `prls-co/system_setup`. This plan
does not change SSH, Mosh, Tailscale, router mappings, public DNS for SSH, or
any SSH hostname. Harden-LLM does not own host access. The existing
`shaman.prls.co:2222` path remains outside this web cutover.

Do not create a second tunnel, temporary DNS name, canary Compose stack,
container, deployment framework, or transition-only test suite. Use the
existing Caddy owner checks, exact old container metadata, current service
logs, and bounded public route checks.

## Current evidence (2026-09-28)

- P05 transferred the live web edge to `caddy-shared` on 2026-09-28 PDT.
  Public routes, login/artifact authorization, signed download integrity, and
  unsigned denial passed against the pre-cutover contract. The old HLLM Caddy
  and connector containers remain stopped and intact for rollback until P06.
  SSH/Mosh, DNS, tunnel identity/credential, application services, and shared
  Caddy volumes were not changed or deleted.
- Harden-LLM removal branch `fix/remove-hllm-production-caddy-20260926` has
  rebased P03 source commit `2ae83ba9713c345ef4a8930dbdcbddfd6243b6ce`, based
  on PR #77 merge `eb4bfe5966a9f24e876e279a860e9a15a2aff8ad`. Rebase review
  matched all 14 commits with `git range-diff`; the P03 source changes and
  their separate follow-up commits remain represented. PR #76 now has no
  frontend dependency diff against main; Mint, HPAX, and `lazy_html` are owned
  by PR #77. Earlier local `make test-fast` passed on the pre-rebase source
  candidate (10 tasks, zero failures or cleanup warnings); its runner report is
  `tmp/test-feedback/runner-1790636446468-67079-17116cd8163c1f7a.json`. On the
  pre-rebase PR #76 head `65e881338d948a5c1142695e91a11d9525483980`, hosted
  fast T0–T2 and CodeQL passed (run `36500108503`, CodeQL run `36500103612`);
  these checks are historical and do not certify the rebased candidate. On
  rebased head `b6976309d803e3b174497d03c4d9ba794223930b`, hosted fast T0–T2
  (`36501229374`), CodeQL (`36501225913`), and browser-free release
  (`36501587908`) all passed. That release's `runner-contracts` task took
  140.492 s. The manifest budget was restored from 240 s to its original
  180 s without changing test assertions, retries, or cleanup. The current
  exact PR #76 head is `1086358d4a7894ab1c28f712356d331167d29c29`; hosted fast
  T0–T2, CodeQL, and browser-free release all passed on that head (runs
  `36503548987`, `36503549535`/`109199878733`, and
  [36504493543](https://github.com/prls-co/harden-llm/actions/runs/36504493543)).
  Release accepted all 29 tasks with zero failures or cleanup errors/warnings.
  `runner-contracts` took 143.035 s against the original 180 s budget and did
  not time out. This is 36.965 s of remaining margin, so keep hosted isolated
  runners as the execution environment and do not increase the budget without
  new source-backed measurements. P05 proceeded only after refreshing its
  immediate live preflight; the accepted cutover evidence is recorded below.
  An earlier candidate's release run `36496018663` failed the unchanged audit
  task on Mint 1.10.1's three advisories. The base-branch security follow-up was
  isolated in Harden-LLM PR #77: commit
  `f6461ff402aece718c0afa1b68cf7cee33220691` updates Mint to patched 1.11.0,
  HPAX to its required 1.1.0, and test-only `lazy_html` to patched 0.1.13.
  The first PR #77 attempt correctly failed because main still had vulnerable
  `lazy_html` 0.1.11. PR #77 merged as
  `eb4bfe5966a9f24e876e279a860e9a15a2aff8ad` after hosted fast T0–T2, CodeQL,
  and browser-free `make test-release` passed on exact head
  `f6461ff402aece718c0afa1b68cf7cee33220691` (runs `36498836561`,
  `36498841906`, CodeQL `36498836969`, release
  [36498851507](https://github.com/prls-co/harden-llm/actions/runs/36498851507)).
  The current host `shaman` has 6.8 GiB reported available, all 8 GiB swap used,
  `/tmp` 98% full, and 52 running containers (observed 2026-09-28). Do not run
  Docker/release suites on this production host; use isolated GitHub runners.
  After publishing the rebase, require hosted fast T0–T2, CodeQL, and
  browser-free release on the exact rebased PR #76 head. Do not merge PR #76
  until P05 passes. A previous separate release attempt had a
  45-second frontend smoke-config bootstrap timeout during a host-wide OOM;
  the smoke project was cleaned and the cause was unresolved. Do not count
  that run as passing acceptance or attribute its failure to the current
  candidate without reproducing it.
- The Harden-LLM removal diff is broad (43 files at the recorded source
  comparison). Verify every file is required by removing production Caddy and
  preserving the affected app/test contracts. Drop unrelated changes rather
  than bundling them.
- `caddy-shared` PR #3 was merged as
  `4ddf02c72a76f2010f7112d09a3390650b182ff4` (source head
  `81ae0f712492871051abf79b71a2f17490263435`). Its net diff retains the
  production least-privilege UID/GID fix and removes the temporary canary
  machinery; no new tunnel, hostname, or token was provisioned. The hosted
  `shared-ingress` job passed on that exact source head.
- Caddy owner checks pass locally on the merged source:
  `python3 tests/test_config.py TestOwner`,
  `python3 tests/test_live.py TestProbeContract`, and
  `python3 tests/test_routes.py`. The last check creates only uniquely named,
  isolated test containers/network and cleans them up; a post-test label query
  found no leftovers.
- The protected shared-owner `runtime.env` remains ignored and mode 0600;
  `docker compose --env-file ./runtime.env -f compose.yaml config --quiet`
  passes against the merged source. These source checks did not start or
  change production services; the separate live P05 transfer is recorded
  below.
- The pre-cutover Harden-LLM production configuration check was read-only
  `equivalent` for six scoped services; no apply ran. P06 must repeat the
  read-only check after removing only the `caddy` descriptor entry.
- Recorded pre-cutover web-owner containers (both are now retained and
  stopped): old
  production Caddy is `harden-llm-caddy-1`, container ID
  `69ae2df941e5f68322a729eb26128491f1f519fa5f9b457963c9b77b117a017c`, image
  `caddy:2.11.4-alpine@sha256:5f5c8640aae01df9654968d946d8f1a56c497f1dd5c5cda4cf95ab7c14d58648`,
  running with restart policy `unless-stopped` on `harden-llm_harden-private`
  and `prls-observability`. It mounts the existing `harden-llm_caddy-config`
  and `harden-llm_caddy-data` volumes at `/config` and `/data`, plus read-only
  HLLM Caddyfile, route directory, and frontend overlay binds. Its web
  connector is
  `shaman-harden-llm-cloudflared-1`, container ID
  `207f876e7dc12cf061bee3496369e230c0673290d70379d97396bf127aa7c622`, image
  `cloudflare/cloudflared@sha256:e39ee8da81ad5e05d77f38d2f51c60ca51bf2a8450ac3abab50c17fdb91d91bf`,
  running with restart policy `unless-stopped` on `harden-llm_harden-private`.
  Compose project `shaman-harden-llm` is sourced under
  `/home/kirill/.config/cloudflared/shaman-harden-llm/`; its configuration,
  tunnel credential, and origin CA are read-only bind mounts. Refresh and record
  the full exact mount sources, image IDs, networks, restart policy, and state
  immediately before P05; never copy environment values or credential contents
  into this record. The unrelated `shaman-api-cloudflared-1` is explicitly
  excluded.
- Preserve the old connector's protected config, tunnel credential, origin CA,
  and backups through cutover and acceptance. P06 retires only its exact
  container/startup owner after shared-route acceptance; deletion or rotation
  of those credential/config files is a separate audit and is not authorized
  by this transition.
- No Ops shared-Caddy record has been published yet.
- The configured static-token owner's history is empty; this is not the
  acceptance identity. The separate existing test guest had five history
  items. Before and after cutover, the same existing artifact passed the
  owner-scoped authorization/download check: anonymous API access returned
  401, authorization returned 303, signed download returned 200, and all
  2,252 bytes matched stored size and SHA-256 metadata; unsigned object access
  returned 403. The temporary session was logged out (200) and a subsequent
  anonymous session check returned 401. No session, token, signed URL, body,
  object hash, or trace ID was retained. No run was created and no provider
  was called.
- Fresh read-only P05 preflight (2026-09-28): the Harden-LLM production
  descriptor check returned `equivalent` for six scoped services and applied
  nothing. The merged `caddy-shared` source and Compose model rendered
  successfully; both pinned images were present locally, ports 80/443 were
  loopback-only, the existing Caddy config/data volumes and
  `prls-observability` network were reused, and the protected tunnel
  credential's owner/mode matched the new connector. A volume-writer scan
  found only the old HLLM Caddy writing the two shared volumes. The old
  Cloudflare route table was read in memory and compared with the new one:
  same tunnel and 16 hostnames/origins, with only the expected Caddy service
  alias changing from `caddy` to `caddy-shared`. No private configuration or
  credential value was retained.
- The public pre-cutover route responses below were collected on 2026-09-28
  with one TLS-verified unauthenticated GET per route; redirects were not
  followed and bodies were not retained. Refresh every row immediately before
  P05. In the immediate preflight, one Grafana health request timed out
  (`curl` 28); three immediate retries returned 200 in under 100 ms each, and
  the subsequent complete 16-route comparison reproduced every recorded
  result. No service had been changed. Treat another Grafana timeout or any
  unmatched result as a stop condition. Existing 502s, failed agent-alias TLS
  handshakes, and the admin redirect mismatch remain pre-existing route-owner
  follow-ups, not successful application health checks.
- Accepted P05 runtime after cutover: shared Caddy is container
  `8b5cfc3ff2acfb1aca1fcc2c66b65d87738fe32be0c004fa02bbb80aa88772a7`, using
  the pinned Caddy digest, healthy with restart policy `unless-stopped`,
  `prls-observability` alias `caddy-shared`, the existing
  `harden-llm_caddy-config` and `harden-llm_caddy-data` volumes, and only
  loopback host bindings on 127.0.0.1:80/443. Its Cloudflare connector is
  `9bba250e239edacd0b6c044faa780fde324a915aae60e775b627567a99bb3bea`, using
  the pinned cloudflared digest, running on the same network with read-only
  config/credential/CA mounts. Both old HLLM container IDs above remain
  stopped; an active-writer scan found only the new Caddy writing the two
  existing volumes. The exact HLLM web/gateway containers remain healthy at
  IDs `ac17eafff405d6334e20c9da1ad618daaf72b1ad98d3392469918aaf9a4665a1`
  and `cf580302a2e6dc82cad4586335171988c4a0f0bdc8e2b73456bc3dfb6cc6057e`.
  The Caddy startup owner is the `caddy-shared` Compose project at
  `/home/kirill/p/caddy-shared/compose.yaml`; no matching systemd service
  unit was present. Connector registration and public API readiness passed
  within the 120-second budget. All 16 public route results and redirects
  exactly matched the pre-cutover table within the 300-second budget.
  `ssh.service`, `ssh.socket`, Tailscale, Fail2ban, and the enabled/waiting
  `shaman-public-ssh.timer` remained active; the reconciler reported success.

### Pre-cutover public route baseline (2026-09-28)

| Tunnel hostname | Probe | Current public result | Interpretation / P05 comparison |
| --- | --- | --- | --- |
| `harden-llm.prls.co` | `GET /` | 302 `/login` | Expected unauthenticated web redirect. |
| `harden-llm-api.prls.co` | `GET /readyz` | 200 | Gateway readiness. |
| `harden-llm-artifacts.prls.co` | `GET /` | 403 | Expected denial for unsigned artifact access; not signed-download proof. |
| `harden-llm-grafana.prls.co` | `GET /api/health` | 200 | Grafana health. |
| `harden-llm-langfuse.prls.co` | `GET /api/public/health` | 200 | Langfuse health. |
| `allure.prls.co` | `GET /` | 401 | Expected unauthenticated denial. |
| `platform.prod.agents.prls.co` | `GET /` | TLS handshake failure | Pre-existing failed alias route; require unchanged comparison and owner follow-up. |
| `masked-recall-api.prod.agents.prls.co` | `GET /` | TLS handshake failure | Pre-existing failed alias route; require unchanged comparison and owner follow-up. |
| `product-opportunity-api.prod.agents.prls.co` | `GET /` | TLS handshake failure | Pre-existing failed alias route; require unchanged comparison and owner follow-up. |
| `synthetic-product-dataset-api.prod.agents.prls.co` | `GET /` | TLS handshake failure | Pre-existing failed alias route; require unchanged comparison and owner follow-up. |
| `platform.prls.co` | `GET /` | 502 | The same pre-existing 502 was reproduced during P05; origin-owner follow-up remains. |
| `masked-recall-api.prls.co` | `GET /` | 502 | The same pre-existing 502 was reproduced during P05; origin-owner follow-up remains. |
| `product-opportunity-api.prls.co` | `GET /` | 502 | The same pre-existing 502 was reproduced during P05; origin-owner follow-up remains. |
| `synthetic-product-dataset-api.prls.co` | `GET /` | 502 | The same pre-existing 502 was reproduced during P05; origin-owner follow-up remains. |
| `analytics.prls.co` | `GET /` | 302 `/overview` | Expected Analytics Gateway redirect. |
| `admin-aiknowledge.prls.co` | `GET /` | 307 `/login` | Public response showed Cloudflare's server header on the prior check; Caddy source expects 308 to Analytics. Treat the edge-layer explanation as an inference and verify the same public result before/after. |

This table is the captured pre-cutover compatibility baseline; P05 reproduced
all 16 outcomes and redirects exactly. It is not a claim that every route is
healthy. The already failing origins and aliases remain explicit owner
follow-ups.

## Owner phases

### P03 — Prepare Harden-LLM source for Caddy retirement

1. Fetch current `main`. Merge/rebase it into the existing removal branch and
   resolve overlapping test-catalog changes without dropping either TEST-062
   credential coverage or the Caddy-removal assertions.
2. Review the full branch diff by file. Keep only removal of Harden-LLM's
   production Caddy configuration/ownership and the necessary affected
   application, deployment, documentation, and test updates. Preserve the
   existing gateway, web, shared-service consumers, and data. Drop unrelated
   dependency edits rather than bundling them. The Mint/HPAX and test-only
   `lazy_html` audit fixes are tracked in separate HLLM PR #77 and must be
   merged first; after rebasing, remove their duplicate dependency hunks from
   PR #76. Do not silently change application dependencies in this ingress PR.
3. Run `make test-fast` and the required browser-free release gate at the exact
   candidate SHA. Require hosted CI on that same SHA. Do not run browser tests,
   Docker application deployment, provider calls, or a preview deployment for
   this source-only preparation.
4. Open a PR and keep it unmerged until P05 production acceptance succeeds.

**Stop:** an unrelated service or data path changes, an owner test is removed
without its distinct contract being preserved, or the exact main-based CI is
not green.

### P04 — Simplify and publish the shared Caddy owner

1. Finish the existing Caddy PR #3 by removing only the unused canary model,
   DNS instructions, and canary-specific test code. Keep production Compose,
   current routes, least-privilege tunnel file access, `TestOwner`,
   `TestProbeContract`, and isolated route-policy coverage.
2. Run the three Caddy checks listed above. Push the reviewed changes, wait for
   PR CI on the exact head, mark the PR ready, and merge it.
3. Refresh the ignored `runtime.env` from current live nonsecret route/TLS
   settings immediately before P05. Verify its mode, ignored status, rendered
   Compose model, file paths, owners, and the existing Caddy volume names. Do
   not print or commit its contents.

**Stop:** CI fails, private runtime files are missing or have unexpected
ownership/modes, or the rendered model changes a production route, identity,
credential, network, or volume.

### P05 — Transfer the live web route set

This is a bounded production change with a short expected web interruption.
Keep an authenticated host shell/console and exact rollback commands
available. The current operator shell is already on Shaman; no SSH agent
restoration or SSH configuration change is required for this cutover.
Do not alter SSH or stop any unrelated connector, including
`shaman-api-cloudflared-1`.

1. Refresh source SHAs and run the read-only Harden-LLM production-config
   check. Record exact IDs, image IDs, status, restart policy, networks, mount
   names/targets, and startup owner for only the old production Caddy and its
   web connector. Capture no environment values or credential contents.
2. Verify the shared source is merged, Compose renders, the pinned images are
   locally available or fetchable, the exact existing Caddy volumes and shared
   network will be reused, and current public TLS/routes respond. Keep the
   old stopped containers and the pre-cutover source/configuration intact.
3. Stop the exact old web connector, then the exact old Caddy container. Do not
   remove either container or volume. Start shared Caddy alone with the
   existing production Compose model. Check its health, validation logs,
   mounts, network, and loopback listeners. Only then start the shared
   connector with the same production tunnel credential.
4. Within 120 seconds confirm connector connection and origin readiness. Within
   300 seconds verify normal TLS and the expected authenticated/unauthenticated
   behavior for every existing public web route, including the signed artifact
   retrieval/denial contract. Record status, safe response metadata, and
   artifact byte/hash comparison; do not record cookies, tokens, bodies, or
   private environment values.
5. If any route, auth, TLS, connector, or artifact check fails, stop only the
   new connector and Caddy, restart the exact old Caddy and connector, and
   verify the old public paths. Do not delete volumes or try a second
   unbounded configuration change during rollback.
6. Keep the old stopped containers and source until the full acceptance table
   passes. If rollback is needed after P06 removes them, restore through the
   accepted shared owner; the old-container restart path will no longer exist.

**Stop/rollback:** unidentified container or mount, unexpected writer to the
Caddy data/config volumes, route drift, failed old-owner restoration, missing
runtime input, or a verification budget overrun. Keep the source and exact
containers needed to restore the last accepted owner.

**P05 accepted (2026-09-28 PDT):** the exact PR #76 source head
`1086358d4a7894ab1c28f712356d331167d29c29` passed fast T0–T2, CodeQL, and
browser-free release before cutover. The live transfer retained the existing
tunnel/DNS/volumes, started and health-checked Caddy before its connector,
confirmed connector registration and public API readiness, matched all 16
public responses, and repeated the test-guest signed-artifact contract. No
rollback was needed. Both prior containers remain available but stopped; do
not delete them before P06 confirms the old Compose owner cannot recreate them.

### P06 — Retire the old Harden-LLM owner

Only after P05 succeeds:

1. Merge the reviewed P03 PR. Do not run a broad production-config apply for
   this Caddy-only retirement: it can recreate or replace unrelated app
   services. The shared owner already serves production routes, and P06 changes
   only the HLLM descriptor and the exact obsolete containers.
2. Before editing the protected production descriptor, verify its current
   schema, mode/owner, backup/restore path, and the exact running gateway/web
   release identities. Back it up privately, remove only the `caddy` service
   entry, and run the read-only `production-config check`. Require `equivalent`
   for the remaining configured services. Do not apply/reconcile app services.
3. Confirm the shared Caddy owner continues serving every accepted P05 public
   route and that the HLLM gateway/web release and health are unchanged.
4. Remove only the exact stopped old Caddy and old web-connector containers
   after verifying their Compose startup owner can no longer recreate them.
   Retain the Caddy volumes, tunnel credential, origin CA, protected connector
   config/backups, DNS records, and unrelated containers. Do not use Compose
   `down`, prune, or remove resources by project/name pattern.

Record the point at which restart of the old containers ceases to be a valid
rollback. The old connector's protected files under
`/home/kirill/.config/cloudflared/shaman-harden-llm/` remain for a separately
reviewed credential/configuration-retirement decision. Do not prune, use
`down -v`, delete shared volumes, or revoke any credential as part of P06.

**Stop:** app image/release changes, non-equivalent descriptor check, an
unreviewed host descriptor, wrong container identity, or any attempted volume
or credential cleanup.

### P09 — Record the accepted shared owner in Ops

After P05/P06, update the existing Ops technical system map and add one concise
shared-Caddy operating record with the final source revision, two-service
runtime identity, route/network/volume ownership, startup/recovery procedure,
checks actually run, and remaining risks. Add one dated decision-log entry and
an active risk-register item only for a material unresolved operational risk.
Update `tech/index.xml` and the repo index only if a new record is added. Keep
source CI, deployed identity, live-route acceptance, and rollback evidence
separate. Validate edited XML and run the required Ops index checks.

## Risks and follow-up

- The old and new Caddy containers must not write the same Caddy volumes at the
  same time. Cutover intentionally stops the old writer first.
- Public web access will be briefly interrupted while the connector changes
  ownership. The 120/300-second limits and exact old-container rollback are
  operational gates, not test retries.
- P05 live acceptance passed, but the old HLLM startup owner and protected
  connector files remain until P06. Keep those rollback resources intact until
  the descriptor and startup-owner checks pass.
- A protected runtime descriptor or credential path may differ from source;
  inspect metadata and permissions immediately before use, without exposing
  values. Stop if they differ.
- Do not change Cloudflare DNS, tunnel identity, credentials, SSH, or Mosh to
  solve a web-route problem. The current SSH setup remains independently owned
  and unaffected.
- Before old-container deletion, verify no HLLM runbook, descriptor, or startup
  automation will recreate the retired Caddy service. Remove only exact,
  verified stale references; do not delete shared services or infrastructure
  by name pattern.
