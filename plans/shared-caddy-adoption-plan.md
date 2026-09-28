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
- Harden-LLM removal branch `fix/remove-hllm-production-caddy-20260926` is at
  `2055b05d5f6655d10db2300c1654399c5109f22d`. Its source commit
  `d9fd490484b817b3bbc52ac090e444c75ae0760a` passed hosted `make test-fast`
  run `36266841961` and browser-free release run `36266872024`. Later commits
  on that branch only changed this plan. The branch has no PR yet and predates
  the merged main commit `b2bbdad0d8627ed6aa3ab410e670f3f60d188ddd`; refresh
  and review the complete diff against current main before opening a PR.
- The Harden-LLM removal diff is broad (43 files at the recorded source
  comparison). Verify every file is required by removing production Caddy and
  preserving the affected app/test contracts. Drop unrelated changes rather
  than bundling them.
- `caddy-shared` PR #3 is an existing draft on
  `feat/https-canary-transition-20260926`, based at
  `c05dfe9efc26ad7f9826ba51acabbd1e2b01fa97`. The pending local change removes
  its unused canary files, route, tests, and instructions. The production
  `runtime.env` is mode 0600, ignored by Git, and
  `docker compose --env-file ./runtime.env -f compose.yaml config --quiet`
  passes.
- Caddy owner checks pass locally after the canary removal:
  `python3 tests/test_config.py TestOwner`,
  `python3 tests/test_live.py TestProbeContract`, and
  `python3 tests/test_routes.py`. The last check creates only uniquely named,
  isolated test containers/network and cleans them up; a post-test label query
  found no leftovers.
- The current Harden-LLM production configuration is read-only `equivalent`;
  no apply has run. Refresh this and all container identities immediately
  before cutover.
- No Ops shared-Caddy record has been published yet.

## Owner phases

### P03 — Prepare Harden-LLM source for Caddy retirement

1. Fetch current `main`. Merge/rebase it into the existing removal branch and
   resolve overlapping test-catalog changes without dropping either TEST-062
   credential coverage or the Caddy-removal assertions.
2. Review the full branch diff by file. Keep only removal of Harden-LLM's
   production Caddy configuration/ownership and the necessary affected
   application, deployment, documentation, and test updates. Preserve the
   existing gateway, web, shared-service consumers, and data.
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

1. Merge the reviewed P03 PR and deploy the exact candidate with the existing
   Harden-LLM production-config path. Limit the apply to the intended
   application services; do not rebuild application images or mutate data,
   shared infrastructure, or unrelated services.
2. Verify the exact gateway/web release and health are unchanged and the full
   Harden-LLM production-config check is equivalent. Confirm shared Caddy
   continues to serve the accepted public routes.
3. Update the protected production descriptor to remove only the retired Caddy
   service after checking its current schema and backup/restore path. Preserve
   the source and rollback record before changing this host-owned file.
4. Remove only the exact stopped old Caddy and web-connector containers after
   verifying they are no longer needed. Retain the existing Caddy volumes,
   production tunnel credential, origin CA, DNS records, and unrelated
   containers.

Record the point at which restart of the old containers ceases to be a valid
rollback. Do not prune, use `down -v`, delete shared volumes, or revoke any
credential as part of P06.

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
