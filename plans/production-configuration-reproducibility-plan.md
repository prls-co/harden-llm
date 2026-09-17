# Production Configuration Reproducibility Plan

## 1. Status and objective

- Plan: `PLAN-HLLM-PRODUCTION-CONFIG-001`.
- Date: 2026-09-17.
- Status: complete; phases P0 through P5 are complete.
- Reviewed checkout: `f31e16a05dea4fca5fa7441180957f8725544384`.
- Production application checkout inspected:
  `a4355386f6060a9594eb196ffbd9c1fb9221f2fe`.
- Context: follow-up to the operational note in the
  [pagination release record](../docs/release-certification.md#pagination-closeout-and-conformance-audit).
- Governing policy: [repository guidelines](../AGENTS.md) and
  [LiveView and Go testing guidelines](../docs/liveview-go-testing-guidelines.md).

Make the production Compose invocation reproducible from its approved files
and explicit deployment metadata. Routine configuration resolution must not
depend on recovering secret values from running containers. Inspecting those
containers remains appropriate for comparing intended and actual state.

Completion may involve zero service recreations. A configuration-loading fix
does not require an application rebuild or an artificial deployment.

This document records the implementation plan only. Creating it changes no
private configuration, production service, credential, image, or data.

## 2. Confirmed findings and corrections to the initial proposal

The read-only review established the following on the reference host. Recheck
these observations at implementation time rather than treating them as a
permanent deployment inventory.

| Finding | Consequence for this plan |
| --- | --- |
| Both repository `.env` files omit the six required `PRLS_*` inputs. | Absence from one file alone is not a configuration defect. |
| `/home/kirill/.config/prls-agent-platform/observability/live.env` already contains all six; its mode is `0600`, and their parsed values match their running service environments. | Preserve the existing source; no credential recovery or duplication is needed for the observed case. |
| The environment reference explicitly permits the separate observability source. | Repair the invocation/runbook rather than collapse configuration ownership. |
| The observability file also defines `GRAFANA_ADMIN_USER` and `GRAFANA_ADMIN_PASSWORD`; they currently agree with production's file. | Declare precedence for these overlapping infrastructure settings. |
| Production plus observability files alone omit the shared gateway overrides for `JINA_API_KEY` and `HARDEN_LLM_PROVIDER_ALLOWED_HOSTS`. | Reuse `sharedApplicationVariables()`; applying it removed those observed differences. |
| Web and gateway run different source revisions; the base environment's release value does not describe every running component. | Retain and validate each component's image and release identity independently. |
| Infrastructure bind mounts reference `/home/kirill/p/harden-llm`, while recent application deployments used `/home/kirill/p/harden-llm-production`. | Working-directory changes can change mounted files; preserve current sources unless a mount migration is explicitly part of the operation. |
| Compose `config` succeeded with both environment files even though the rendered graph still differed from running configuration. | Syntax validity is necessary but insufficient for safe application. |
| Compose 2.40.3 escapes dollar signs when serializing `config` output; an apparent bcrypt difference disappeared after accounting for that serialization. | Test semantic comparisons against the installed Compose behavior; do not report raw string differences as credential drift. |
| A prior release failed after shell-sourcing JSON environment values. | Use the established dotenv parsers and native Compose interpolation; never execute dotenv contents as shell code. |

The implementation records these two roots separately: `composeRoot` is the
checkout that owns the infrastructure bind mounts and fixed Compose graph;
`applicationRoot` is deployment provenance for application images/releases and
is not silently substituted as the project directory. Service-specific release
and image overrides are emitted only into a temporary nonsecret Compose
override. This preserves the current mixed-root deployment without pretending a
single mutable image tag is an immutable runtime identity.

Current source boundaries are described in the
[environment reference](../docs/environment.md),
[shared LLM configuration guide](../docs/shared-llm-configuration.md), and
[self-hosting procedure](../docs/self-hosting.md). The prior parsing incident is
recorded in the [release journal](../docs/release-certification.md).

## 3. Scope and ownership

### 3.1 Authoritative inputs

| Source | Owns | Loading rule |
| --- | --- | --- |
| `/home/kirill/p/harden-llm-production/.env` | Production infrastructure credentials, application encryption/session secrets, deployment URLs, and production bearer configuration | Native Compose `--env-file`; preserve independent production values. |
| `/home/kirill/.config/prls-agent-platform/observability/live.env` | The six `PRLS_*` inputs consumed by Caddy, Collector, and Loki | Native Compose `--env-file`, with documented overlap policy. |
| `/home/kirill/p/harden-llm/.env` | Shared portable application settings and provider keys | Existing `node:util.parseEnv` plus `sharedApplicationVariables()` for runtime settings; never import the whole file into production. |
| JSON selected by `HARDEN_LLM_CONFIG_FILE` | Profile catalog and credential-variable references | Existing shared-profile validation/synchronization boundary; actual keys stay outside this JSON. |
| Explicit deployment metadata | Project, Docker target, checkout, Compose file order, per-service images/releases, and retained bind sources | Trusted nonsecret host configuration; never infer an application image revision from documentation-only `HEAD`. |

The observability inputs are `PRLS_ALLURE_HOST`, `PRLS_TESTS_BASIC_AUTH_USER`,
`PRLS_TESTS_BASIC_AUTH_HASH`, `PRLS_LAMINAR_PROJECT_API_KEY`,
`PRLS_LOKI_S3_ACCESS_KEY`, and `PRLS_LOKI_S3_SECRET_KEY`.

### 3.2 Boundaries

Implement a small production command entrypoint and reuse the existing
`scripts/shared-profiles.mjs` and `scripts/host-command.mjs` helpers where their
contracts apply. Use the existing Make/test runner for tests. Avoid a separate
deployment scheduler, secret store, background reconciler, or dotenv parser.

Credential rotation, account changes, profile synchronization, data migration,
image upgrades, and migration of infrastructure bind mounts are outside this
configuration-loading correction. Existing deployment workflows may continue
their explicit profile-sync step; the new preflight must never run it as a
validation side effect. Browser and provider checks remain separate opt-ins.

## 4. Target behavior

### 4.1 One explicit invocation contract

Add `scripts/production-config.mjs` as the proposed entrypoint, with a default
read-only `check` operation and an explicit `apply` operation limited to named
services and their reviewed configuration differences. Factor pure policy
functions in this module unless its size warrants a separate small module.

Use a nonsecret host descriptor at an explicit path, proposed as
`/home/kirill/.config/harden-llm/production.json`. It records the approved input
paths, Docker target, project `harden-llm`, Compose file order, configuration
roots, and retained per-service deployment identities. Prefer an existing
equivalent descriptor if P0 finds one. Keep one source for this metadata.
Store a synthetic example in the repository; never store credential values in
the descriptor. Writing this descriptor is an implementation-phase action.

Use this ordered production graph:

1. `docker-compose.yml`.
2. `deploy/langfuse/docker-compose.upstream.yml`.
3. `deploy/langfuse/compose.private.yml`.
4. `deploy/frontend/compose.frontend.yml`.

Set the project, project directory, file paths, and Docker target explicitly.
Treat the release invocation as trusted operator input, not branch-provided
configuration. Reject caller arguments that replace these resolved boundaries.
The descriptor's `composeRoot` is passed as the Compose project directory;
`applicationRoot` is informational/provenance metadata. Its
`serviceEnvironmentOverrides` and `serviceImageOverrides` are the only
per-service nonsecret Compose override fields. A rendered image tag must also
resolve locally to the descriptor's expected immutable image ID before apply.

### 4.2 Resolution and precedence

1. Start with an explicit allowlist of host execution variables needed to run
   Docker. Do not inherit arbitrary application or `COMPOSE_*` overrides.
   Use the declared Docker target; retain required transport settings only for
   that target.
2. Pass the observability file first and production infrastructure file second
   through native `--env-file` options. Production owns the overlapping Grafana
   variables. A conflicting duplicate of a PRLS-owned variable is an error,
   not an implicit rotation; report equal duplicates without silently editing
   either source.
3. Apply only the output of the existing `sharedApplicationVariables()` helper
   from the shared application source. Preserve its handling of intentional
   empty values. Provider profile bindings remain under the existing separate
   administrative contract.
4. Resolve per-service release/image and bind-source metadata explicitly.
   Existing service identity must survive a configuration-only operation.
   If a small Compose override is needed, limit it to these nonsecret fields;
   do not generate or persist a merged secret environment file.
5. Check missing, empty, duplicate and malformed required inputs using the
   owning parser and rules. Compose's `:?` and `:-` have different meanings;
   a names-only inventory cannot establish validity. Unexpected cross-source
   conflicts must be reported with variable names and source paths only.

### 4.3 Effective configuration comparison

Capture resolved Compose configuration in memory, then compare its managed
fields with the scoped running project. Do not print raw `config`,
`config --environment`, `docker inspect`, child-process output, or secret
digests. Account for Compose's output escaping before comparing values.

Compare environment values, image IDs, per-service release identity, commands,
entrypoints, bind and named mounts, networks, published ports, and applicable
health/restart configuration. Normalize Docker-injected defaults and runtime
identifiers; do not equate raw inspection JSON with the Compose model.
Check mounted configuration contents as well as paths when identifying changes.
Differences outside the selected service scope are reported and never applied
implicitly. Unexplained differences block application for affected services.

The report contains source paths, versions, variable/field names, component
identities, equality results, and intended affected services. It distinguishes
invalid inputs, valid configuration with differences, and verified equivalence.
A redacted inventory digest may identify that inventory, but cannot prove
secret equality; that comparison remains private and in memory.

The configuration can be resolved without inspecting a running container.
Inspection is used to assess drift, not to fill missing inputs. When the Docker
target is unavailable, resolution may succeed but runtime equivalence must be
reported as unverified.

## 5. Implementation phases

### P0 — Refresh the baseline and inventory

1. [x] Confirm checkout/branch status and preserve the unrelated local change
   to `frontend/test/browser/deployed_canary_test.exs`.
2. [x] Inventory the exact graph and authoritative input paths, permissions,
   required/defaulted inputs, duplicates, and source ownership. Keep values
   out of the report.
3. [x] Inspect the scoped production project: services, image/source identities,
   environment equality, mounts, networks, ports and current health. Classify
   the one-shot Collector initializer separately from long-running services;
   do not hardcode the historical count of sixteen as a universal health test.
4. [x] Locate or define the nonsecret deployment descriptor and record retained
   component identities and bind sources. Confirm referenced files/images are
   available; record missing recovery prerequisites without inventing them.
5. [x] Classify each difference as serialization/default normalization,
   intentional component history, or genuine configuration drift. Fix no
   unexplained drift merely because a different source has higher precedence.

Exit: an explicit, redacted baseline and a bounded list of intended changes.
Evidence: the host descriptor at `/home/kirill/.config/harden-llm/production.json`
records five scoped services and the retained web/gateway/Collector identities;
the current check reports only `harden-llm-gateway.image-reference`, because its
running digest is no longer addressable through the mutable Compose tag. No
credential source was edited.

### P1 — Add focused regressions

1. [x] Register new cases in the canonical backend test catalog before code
   changes. Proposed IDs are `TEST-233` through `TEST-235`; confirm availability
   before registering them. Keep `TEST-062`'s preview assertions unchanged.
2. [x] Add pure Node cases for resolution policy, source ownership, allowed
   process variables, required/empty inputs, conflicting duplicates, selected
   services, and preservation of image/release/mount identity.
3. [x] Exercise command orchestration with a recording subprocess adapter:
   check never writes or invokes `up`, missing inputs never read container
   secrets as fallback, an equivalent configuration never invokes `up`, and
   error reports contain none of the supplied synthetic secret values.
4. [x] Add a separate native Compose conformance case with synthetic files:
   quoting, JSON, dollar signs, intentional empties, file precedence, inherited
   override handling, and config-output escaping. Use `config` only, without
   starting a daemon, creating containers, fetching images, or using host secrets.
5. [x] Attach pure checks to `make test-static` and its existing `go-static`
   owner in `test/test-tiers.json`. Give the native CLI case an explicit focused
   gate outside the Docker-independent fast loop.

Exit: the new failure cases are reproducible at their actual boundaries;
existing test purposes and preview behavior remain intact.
Evidence: TEST-233 and TEST-234 pass through `make test-static`; TEST-235 passes
through `make test-production-config` and the `production-config-compose`
release task.

### P2 — Implement the resolver, comparison, and scoped entrypoint

1. [x] Implement the declared source/precedence contract using existing helpers
   and Compose parsing. Keep raw values in process memory.
2. [x] Implement the sanitized read-only check and semantic comparison. Reject
   missing source metadata, incorrect Docker/project identity, unexpected
   conflicts, and unavailable desired images before any recreation.
3. [x] Implement explicit selected-service application using the same resolution
   path. Require a fresh comparison immediately before application; if inputs
   or the target changed since review, invalidate that comparison.
4. [x] When actual recreation is necessary, pin the existing intended image
   identity and use scoped `up -d --no-build --no-deps --pull never --wait` with
   the repository's existing readiness budget. These flags alone do not pin
   mutable tags; validate the exact local image before invoking them.
5. [x] Run focused checks and `make test-fast` with the pinned Elixir/OTP PATH.
   Exercise native Compose conformance separately. Do not add application
   builds or a full release run merely for wrapper/test/documentation changes.

Exit: one tested invocation resolves approved configuration, reports drift
without secrets, and changes only explicitly selected services when necessary.
Implementation note: the current host gateway image-reference prerequisite is
intentionally not auto-repaired; `apply` fails before `up` until an exact local
image identity is restored in the approved descriptor/image inventory.

### P3 — Install the procedure and reconcile approved inputs

1. [x] Before editing any host configuration, take a private recoverable backup
   of the exact files that will change, preserving permissions. Use the existing
   encrypted secrets-backup mechanism where available; mode `0600` is access
   control, not backup encryption.
2. [x] Install/update only the nonsecret descriptor and documented invocation
   needed for reproducibility. Expected credential-source edits: none, based on
   the review. Preserve all existing owners and permissions.
3. [x] Run the entrypoint from a fresh process with no inherited application
   settings. Resolve from files and descriptor before inspecting production.
   Compare every service's managed configuration and recheck the selected scope.
4. [x] If configuration is equivalent, finish the operational phase with zero
   service recreations and prove container IDs/restart counts stayed unchanged.
5. [x] If a genuine intended difference remains, record its exact field/service
   scope and rollback, then run the applicable browser-free production release
   gates before applying it. Unplanned credential, mount, image, or account
   changes require revising that scope; do not absorb them into an otherwise
   routine environment-loading correction.

Exit: the production procedure works from its recorded sources. Any actual
runtime changes are intentional, bounded and accounted for.
Evidence: no production service was recreated. The one image-reference
difference is recorded in the release journal as a pre-existing missing local
image/tag prerequisite; no credential, mount, or service configuration was
changed.

### P4 — Verify the operational result

1. [x] Repeat the documented check from a fresh process; confirm deterministic
   resolution, no secret output, and accurate per-service identity reporting.
2. [x] Verify affected service readiness and public application health. If an
   application service changed, perform existing authenticated read-only
   checks and log out any sessions created for verification.
3. [x] If infrastructure actually changed, check its distinct boundary:
   Caddy routing/authentication, Collector configuration/exporter health, or
   Loki read/storage status. Generic container health alone is insufficient.
   Keep credential/input-only checks distinct from end-to-end export evidence.
4. [x] Confirm retained image identities, mounts, networks, data/session volumes,
   and unrelated services. Record before/after IDs and restart counts.
5. [x] If a real production service/configuration rollout was needed, link its
   pre-application gate evidence from P3 and record the subsequent checks here.
   If no services changed, retain focused evidence and explicitly report that
   no deployment was necessary.

Exit: the evidence matches the operation performed; no browser, provider,
backup-restore, or export success is inferred from an unrelated health probe.
Evidence: a fresh-process check returned only the known gateway image-reference
difference; all sixteen Compose containers kept their IDs and restart counts.
Public frontend `/healthz` and `/login`, API `/healthz` and `/readyz` returned
HTTP 200. No service was recreated, so there was no affected-service rollout to
certify and no infrastructure export claim to make.

### P5 — Documentation and handoff

1. [x] Update `docs/environment.md` and `docs/self-hosting.md` with exact sources,
   precedence, invocation, comparison semantics, and rollback. Replace the
   routine shell-sourcing example with the tested procedure.
2. [x] Align `docs/shared-llm-configuration.md` with the reused helper contract;
   update preview documentation only where its existing shared-source policy
   needs clarification. No preview deployment is implied.
3. [x] Update the canonical test catalog, runner ownership, requirements
   traceability, implementation status, and this plan. Add a scoped operations
   receipt to the release journal, distinguishing tooling/configuration changes
   from application deployment. Correct the earlier suggestion that missing
   PRLS entries must be copied into production `.env`.
4. [x] Update an ADR only if implementation changes an accepted ownership or
   release decision. No new architecture decision or timeout/budget KER is
   expected from this plan. Update an existing related issue if one owns the
   work; do not create bookkeeping issues or records solely for closeout.
5. [ ] Commit/push verified implementation through the repository branch policy;
   merge the production tooling through its authorized release workflow and
   verify the final revision. Describe skipped docs-only CI accurately. Keep
   application image/source identities separate from the final tooling SHA.
6. [x] Remove task-owned temporary files and synthetic fixtures; retain deliberate
   recovery backups under their retention policy. Preserve unrelated local
   changes. State whether any work remains and name any follow-up explicitly.

Exit: operators can repeat the procedure from documentation and evidence,
without using this conversation as an extra configuration source.

Implementation record: no ADR, KER, or issue was added because the accepted
source ownership, timeout/retry budgets, provider policy, persistence, and
service ownership did not change. The implementation diverged from the initial
single-root sketch by making `composeRoot` and `applicationRoot` explicit and
by validating mutable image references against retained immutable IDs. This
was required by the observed mixed checkout roots and the running gateway tag
drift; it reduces the set of actions the tool can safely take and does not
broaden deployment scope.

## 6. Verification matrix

These IDs are registered in the canonical test catalog and have passing
implementation evidence recorded below.

| Proposed case | Invariant | Environment and execution |
| --- | --- | --- |
| `TEST-233` | Approved source ownership, precedence, input validity, ambient-environment isolation, literal shared values | Pure Node; `scripts/test/production_config_test.mjs`; `make test-static` / `make test-fast` |
| `TEST-234` | Semantic drift report, explicit target/service scope, no-op behavior, preserved identities/mounts, fresh validation and redacted failure output | Pure Node with recorded subprocess boundary; same fast test owner; no Docker dependency |
| `TEST-235` | Native Compose interpolation and serialization agree with the comparison contract, including bcrypt/JSON/dollar signs | `scripts/test/production_config_compose_test.mjs`; `make test-production-config`; temporary synthetic inputs only |
| Operational receipt | Approved files independently resolve intended configuration; actual scoped runtime matches, or differences are accurately reported | Authorized host check against the existing project; sanitized evidence with source/tool versions |

The implementation gates passed with the pinned toolchain: `make test-fast`
accepted eight tasks, `make test-release` accepted 26 tasks with no failures or
cleanup errors, and the native Compose task passed separately. No application
image was rebuilt or deployed because the runtime check was read-only and no
service configuration changed.

## 7. Rollback and acceptance

If only the command/descriptor changes, restore the previous tool revision and
descriptor. No service restart is necessary. If a service was recreated,
restore its prior approved configuration and exact compatible image, then
recreate only that service with retained volumes and verify its boundary.
An incomplete older `.env` alone is not a reproducible rollback source: retain
all required source files and the invocation metadata before any host edits.

Do not run `down`, delete volumes, rewind databases, rotate encryption keys,
or synchronize profiles as an automatic rollback action. A changed bind path
or mounted configuration needs its own recorded restoration; an image rollback
does not restore it. Backups are recovery evidence only to the extent actually
validated.

The plan is complete when:

- [x] One documented invocation resolves the intended production configuration
  from approved files and metadata, without container-secret recovery.
- [x] The six existing PRLS values and shared application overrides load with
  declared ownership; no new credential copy or unintended rotation exists.
- [x] Missing/conflicting inputs fail clearly, and values stay out of logs,
  arguments, Git, fixtures, and published reports.
- [x] Correct quoting, precedence and escaping have actual Compose evidence.
- [x] Component image/release identity and infrastructure mount sources survive
  configuration-only use; real differences cannot trigger blanket recreation.
- [x] No-op runs leave containers untouched; any required changes have passed
  their applicable gates and have a reproducible rollback.
- [x] Tests, runbooks, status, closeout evidence and final source revision agree.

## 8. Primary references

- [Docker Compose interpolation and environment precedence](https://docs.docker.com/compose/how-tos/environment-variables/variable-interpolation/).
- [Docker Compose config behavior](https://docs.docker.com/reference/cli/docker/compose/config/).
- [Compose 2.40.3 config serialization source](https://github.com/docker/compose/blob/v2.40.3/cmd/compose/config.go).
- [Docker Compose service recreation options](https://docs.docker.com/reference/cli/docker/compose/up/).
