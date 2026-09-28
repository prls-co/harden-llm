# Shared production web ingress transition

- Plan: `PLAN-HLLM-SHARED-CADDY-001`
- Version: 5.3
- Updated: 2026-09-28
- Status: implementation in progress
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

- Production still uses Harden-LLM Caddy and the current web connector. No
  production container, DNS record, tunnel, credential, or application service
  was changed in this work.
- Harden-LLM removal branch `fix/remove-hllm-production-caddy-20260926` now
  includes current `main` through merge commit
  `f8a9c99de8e3013c639935c4e0fc5a751734058a`; the full P03 source commit is
  `d9fd490484b817b3bbc52ac090e444c75ae0760a`. Local `make test-fast` passed on
  the merged candidate (10 tasks, zero failures or cleanup warnings); the
  runner report is
  `tmp/test-feedback/runner-1790636446468-67079-17116cd8163c1f7a.json`. Hosted
  checks on the earlier source commit
  `d9fd490484b817b3bbc52ac090e444c75ae0760a` included:
  `make test-fast` run `36266841961` and browser-free release run
  `36266872024`. The updated candidate still needs exact-SHA hosted fast and
  browser-free release checks. A previous separate release attempt had a
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
  passes against the merged source. No production Caddy/tunnel service was
  started or changed.
- The current Harden-LLM production configuration is read-only `equivalent`;
  no apply has run. Refresh this and all container identities immediately
  before cutover.
- Recorded live web-owner containers (refresh immediately before P05): old
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
- Current dependency resolution reports locked `mint` 1.10.1 advisories in the
  HLLM frontend dependency audit data. This ingress transition does not modify
  application dependency/runtime versions; track and resolve the Mint finding
  through the HLLM dependency owner before a future application release.

## Owner phases

### P03 — Prepare Harden-LLM source for Caddy retirement

1. Fetch current `main`. Merge/rebase it into the existing removal branch and
   resolve overlapping test-catalog changes without dropping either TEST-062
   credential coverage or the Caddy-removal assertions.
2. Review the full branch diff by file. Keep only removal of Harden-LLM's
   production Caddy configuration/ownership and the necessary affected
   application, deployment, documentation, and test updates. Preserve the
   existing gateway, web, shared-service consumers, and data.
   The test-only `lazy_html` constraint/lock update is retained because current
   Hex advisory data flags the old locked version; the unrelated `mint`
   advisory is recorded above rather than silently changing app dependencies.
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
Keep an authenticated SSH session and the exact rollback commands available.
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
- A local Compose render or green CI does not prove the live route, TLS, auth,
  artifact, runtime identity, or rollback behavior. P05 evidence is still
  required.
- A protected runtime descriptor or credential path may differ from source;
  inspect metadata and permissions immediately before use, without exposing
  values. Stop if they differ.
- Do not change Cloudflare DNS, tunnel identity, credentials, SSH, or Mosh to
  solve a web-route problem. The current SSH setup remains independently owned
  and unaffected.
- After cutover, investigate whether any HLLM runbook or automation still
  expects the retired Caddy service. Remove only verified stale references;
  do not delete shared services or infrastructure by name pattern.
