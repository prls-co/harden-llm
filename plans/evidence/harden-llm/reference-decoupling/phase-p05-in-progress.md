# 1. P05 shared deployment work in progress

- Plan `PLAN-HLLM-REFERENCE-DECOUPLING-002` v5.3, 2026-10-13.
- HLLM worktree `/home/kirill/.cache/harden-llm-tmp/hllm-reference-decoupling-impl`, branch `feat/reference-decoupling`, input checkpoint `335f29bb1fb8e6f5a2675fcd8c4c6b27c670944d`.
- P00–P04 remain complete and pushed. P04 hosted fast CI run `38357625438` passed; skipped integration/release jobs are not acceptance evidence.
- P05.S01 complete locally (63/72 steps); P05.S02 in progress. All current changes are uncommitted. Per section 5.2, commit/push only at the completed phase boundary. P05.S03 and later steps have not started.
- No production mutation, dev/main promotion, browser or real-provider call. Tests used isolated Caddy/origin containers and synthetic credentials. Their labeled container inventory is empty after cleanup. Existing unrelated containers remain untouched.

## 2. P05.S01 RED evidence

`TestReferenceTopology` (TEST-432) compiled and failed the intended assertions:
required protected Go storage settings, external PostgreSQL attachment,
retired Phoenix database, selected bearer/75-second client bound, one-shot schema
image/command, both REST paths and HLLM-only reset manifest. See
`p05-s01-topology-red-complete.log`; the earlier narrower RED is retained too.

The existing Node production runner fixture covers TEST-450 schema ordering,
accepted image override, mismatched image/command/database/build, failure before
application start, no-change/frontend-only behavior and secret-safe arguments and
diagnostics. `p05-s01-schema-red-complete.log` has 25 cases: 17 passing, eight
intended failing assertions. No Docker process runs in these tests.

TEST-432 is registered with existing `go-unit`; TEST-450 with existing `go-static`.
No runner or deployment entrypoint was added.

## 3. P05.S02 local changes and GREEN checks

- Existing `runApply` validates and runs the accepted image's `gateway-schema`
  exactly once before application `up`; both retain `--no-deps`. Nonzero schema
  exit prevents all application starts. The schema image override derives from
  the gateway's one image authority; the one-shot is not a managed daemon.
- Production Compose gives only Go the database URL, encryption key and Garage
  settings. It joins an explicitly named external owner network. One YAML anchor
  owns the database settings shared by gateway/schema. Phoenix's dedicated
  database/container/volume configuration is removed; its API timeout is 75s.
- `deploy/reference-reset.json` names only existing
  `harden-llm-harden-postgres-1` and `harden-llm_harden-postgres-data`. Read-only
  Docker inspect verified the old container mounts that volume. No shared
  database, identity, artifact bucket/key or other product is reset.
- Preview routing includes `/api/v1/*`. Existing auth, network, image,
  configuration precedence, release identity and private-port assertions remain.
  Old storage assertions moved to the Go owner; retired no-reference-API checks
  now assert the accepted routes. Documentation/examples describe one public
  host, shared storage and schema-before-start through the existing apply command.

Passing evidence:

- `p05-s02-topology-green.log`: focused TEST-432.
- `p05-s02-schema-green.log`: all 25 existing/new production-config cases.
- `p05-s02-node-final.log`: all 43 production/preview/cutover cases.
- `p05-s02-compose-config.log`: native Compose deployment and collector endpoint
  checks, with synthetic configuration and no application startup.
- `p05-s02-fast-final.json`: all ten fast tasks accepted, no cleanup errors or
  warnings. Pinned Elixir/OTP toolchain; no browser/provider/Docker starts in fast.
- `node scripts/verify-test-tiers.mjs`: accepted, ten fast tasks.
- `git diff --check`: passed in HLLM and the Caddy preparation checkout.

Retained failures: `p05-s02-node-check.log` and `p05-s02-static-check.log` exposed
old preview-route/service-inventory expectations. The first broad fast run
`p05-s02-fast.json` found a second obsolete preview assertion rejecting `/api/`;
its sibling tasks were cancelled, not counted as passing. The exact accepted
route assertion replaces that prohibition; the final run passed all tasks.

## 4. Caddy owner preparation

Isolated worktree `/home/kirill/.cache/harden-llm-tmp/caddy-reference-routing`,
branch `feat/hllm-reference-routing`, base
`b98dbc84d615f2d74bdb383b7fe779992935cf62`. The live bind-mounted checkout
`/home/kirill/p/caddy-shared` remains unchanged.

The prepared diff sends `/v1/*`, `/api/v1/*`, `/healthz` and `/readyz` to Go,
other paths to Phoenix, on `harden-llm.prls.co`. It removes the obsolete API host
from Caddy and the checked-in tunnel mirror. Publishing later must also remove
that rule from the remotely managed Cloudflare tunnel; changing the mirror alone
is not deployment. Keep the artifact hostname: Agent Platform's runbook identifies
other artifact consumers; HLLM uses its new authenticated downloads.

- `p05-caddy-owner-red.log`: adapted-route inventory mismatch, intended RED.
- `p05-caddy-routes-red.log`: fixture error caused by assigning the old API host
  variable the main hostname while both old route blocks still existed. This
  infrastructure failure is not RED evidence. The fixture was corrected.
- `p05-caddy-routes-red-fixed-fixture.log`: intended RED, `/readyz` reached port
  4000 instead of 8080 through the main hostname.
- `p05-caddy-routes-green.log`: found a real Caddy directive-order defect;
  top-level `respond` ran after fallback `handle`, exposing `/metrics`.
  Moved the denial into its own `handle` and added a cheap structural regression.
- `p05-caddy-routes-final.log`: actual isolated HTTPS checks pass for both APIs,
  bearer/header preservation, URI forwarding, frontend routing, metrics denial,
  exact body boundaries, TLS rejection and backend-failure isolation.
- `p05-caddy-owner-final.log`: owner/native Compose/Caddy adaptation and tunnel
  rule inventory pass after the fix. Tests are browser-free and clean up their
  exact owned resources.

## 5. CHECK-422 findings and remaining dependency

The planned Agent Platform standalone PostgreSQL is not deployed on this daemon.
`compose-postgres-1` has owner labels pointing to
`/home/kirill/p/self-imp-bin-eval/deploy/compose/docker-compose.yml`, project
`compose`, service `postgres`, network `compose_default`. Read-only SQL reports
current database/user `bin_eval` and no `harden_llm` database/role.

Agent Platform's local `shared-services.lock.yaml` and `runbooks/deploy.md` now
identify the same owner for shared Temporal and warn against starting a second
standalone PostgreSQL/Temporal plane. Those owner repositories contain unrelated
dirty work; it was inspected read-only and preserved. No owner handoff has been
accepted merely because a dirty local runbook names the service.

Pending user guidance: reuse an isolated `harden_llm` database/role on that
existing server (recommended), or provide another existing endpoint. This is a
correction to an unverified deployment assumption. Until settled, do not invent
an Agent Platform storage network, create another database process or provision
against a different owner's database.

Shared Garage's read-only bucket inventory has no dedicated HLLM artifact bucket.
The pinned v2.3.0 CLI confirms `bucket create`, `key create <name>` and
`bucket allow --read --write <bucket> --key <name>` ownership commands; the new
key's output must be captured only in protected files. No bucket/key was created.
Production still needs reviewed bucket/key provisioning and an isolated-prefix
put/list/get/delete acceptance check; integration test evidence is not that live
permission check. Do not reuse the Agent Platform artifact credential.

Next: settle the database target, prepare its owner-reviewed isolated role/database
provisioning and Garage/network commands, then finish P05.S02. The trusted
main-owned preview assets gate remains. Only after S02 completes may S03 introduce
the real composed Go/Phoenix/PostgreSQL/Garage boundary. P06 owns all actual shared
resource changes, exact-SHA release, main promotion and deployed acceptance.

## 6. Database decision and P05.S02 completion

Kirill approved the recommended existing PostgreSQL target on 2026-10-13 and
requested continued execution without reopening optional alternatives. Use
`compose_default` and an isolated HLLM role/database. Concrete owner operations
are in `docs/reference-production-cutover.md`; no shared daemon or new network
is required. The schema/topology/fast/Caddy evidence above completes the source
preparation. P05.S02 is complete locally; resume P05.S03 (64/72 steps). Actual
credential provisioning and its live Garage permission proof remain P06 under
the plan's production-mutation boundary. No production change has run.

## 7. P05.S03 composed RED

`p05-s03-compose-red.json` records actual Go/Phoenix/PostgreSQL/Garage/Caddy
startup followed by main-origin `/readyz` returning 404, proving the missing
same-origin API route. The runner completed ownership cleanup with zero errors
or warnings. The existing 300-second startup budget was unchanged; image build
now has its own existing 20-minute setup bound. A cheaper route-contract check
also failed as intended (`p05-s03-route-static-red.log`). The earlier mistyped
focused test selector ran zero tests and is not acceptance evidence.

Before GREEN, corrected the new fixture's cache enum from `read_write` to the
canonical `cache`; this input correction leaves its cache-isolation oracle
unchanged. Gateway/frontend fixtures now use one application hostname.

## 8. P05.S04 GREEN and root-cause regressions

`p05-s04-compose-attempt1.json` found missing Loki correlation because HTTP
middleware read `request.Pattern` on the original request after dispatching a
context-bearing copy. The new T1 `TestHTTPObservationUsesMatchedRoute` failed
for logs and spans, then passed when the middleware retained the dispatched
request. Route templates remain bounded and do not expose IDs/query strings.

Attempts 2 and 3 found fixture mistakes, not production defects: a unit-only
catalog name and a null schema where the snapshot contract requires an object.
The fixture now uses a real Git preset and preserves its complete schema while
changing its provider endpoint/model to the local fake. Assertions are unchanged.
The test PostgreSQL administrator and restricted application role are separate;
`p05-s04-db-role-red.log` and `-green.log` record the cheap topology regression.
The recording-failure probe uses real table permission denial on that role.

`p05-s04-compose-attempt4.json` passed the complete composed workflow, including
schema-before-start, API-key isolation across profiles/drafts/cache/history/
artifacts, same-key Phoenix access, no product history for ordinary inference,
identity outage, definite recording failure and cache-off inference with the
database stopped. No browser or paid provider ran; cleanup had zero issues.

## 9. P05.S05 cleanup and P05.S06 measurement

Removed the separate API test hostname and the retired Phoenix migration path.
Both composed entrypoints use one build/setup helper; the opt-in browser fixture
remains unexecuted. Corrected current README/frontend/API/shared-configuration/
preview documentation; historical release records remain revision-bound.

The initial fast run found stale infrastructure classification and an exact
command expectation. Compose-tagged smoke helpers are test infrastructure;
production import checks still scan them. The command expectation now includes
`-v`, which preserves success measurement in the runner evidence. The final
fast report has all 10 tasks accepted; cancelled siblings in the earlier failed
run are not passes. All native deployment contract tests passed. Production
apply tests passed all 25 cases, including schema ordering/failure behavior.

After the startup refactor, `p05-s05-compose-final.json` again passed: 11/11
required services, 42.838-second startup, 4/4 correlated telemetry backends,
140.919-second Go test (141686 ms runner wall time), 257.363 MiB command peak
RSS. This RSS is test-command evidence, not combined container/production RAM.
Zero cleanup errors/warnings, browser starts or real-provider calls.

Added production daemons: zero. Long-lived application services remain one Go
and one Phoenix; the old dedicated HLLM PostgreSQL daemon is removed by the
P06-only exact reset manifest. The schema service is one command using the
accepted gateway image. Shared PostgreSQL/Garage provisioning is prepared and
still unapplied. EVAL-425 passes; P06 release/deployed acceptance is outstanding.
