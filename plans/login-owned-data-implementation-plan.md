# Login-Owned HLLM Data Implementation Plan

## 1. Status and accepted decisions

- Plan: `PLAN-HLLM-LOGIN-OWNERS-001`.
- Date: 2026-10-04.
- Status: complete; P0-P6 implemented, certified, deployed and accepted.
- Reviewed HLLM source: `b18eb341d2a98411c849d06f50fbe2552b5d6e75`.
- Policy: [AGENTS.md](../AGENTS.md) and the full
  [testing guidelines](../docs/liveview-go-testing-guidelines.md).

The user selected private data per login, replacing the shared-company-account
decision in [the previous cutover record](../docs/release-certification.md#clean-control-plane-account-cutover--production-2026-10-04).
The user also confirmed both remaining product decisions:

1. Every enabled Control Plane login may enter HLLM. No separate HLLM grant is
   required.
2. The existing API token accesses the verification/test login's dataset.

Earlier authorizations still permit deleting incompatible HLLM data and using
a stop/reset/start outage. Keep the existing Control Plane credentials and
other products' data. Browser testing and paid-provider smoke calls remain
excluded. The execution record below tracks implementation and release gates.

## 2. Target behavior and terminology

**Login identity** means the stable `user_id` returned by Control Plane. Its
email and password are sign-in credentials, not storage keys. Changing an email
or password must not change the dataset. Recreating a deleted user with the same
email creates a different identity and must not inherit that user's data.

**Product owner** means `auth.Principal.OwnerID`, which scopes the existing HLLM
tables, encrypted credentials, cache, and artifact operations. After this change,
it is the Control Plane `user_id`, never the selected company `account_id`.
There is no additional HLLM account record or account-creation step.

**API token** means the existing deployment-managed `HARDEN_LLM_STATIC_TOKEN`.
The same secret is already used by Phoenix as its gateway service credential.
An API call without a human session reference accesses one explicitly configured
login's data. A Phoenix call with a reference resolves that human's identity.
These are two credentials/request forms for the same ownership model.

| Request | Owner | Expected behavior |
| --- | --- | --- |
| Administrator signs in | Administrator `user_id` | Opens their private workspace directly. |
| Verification/test user signs in | Verification/test `user_id` | Opens their different private workspace directly. |
| Another session for the same user | Same `user_id` | Reads the same persisted dataset. |
| Existing direct API token | Verification/test `user_id` | Reads/writes exactly the verification user's dataset. |
| Enabled user without a selected company account | Their `user_id` | Allowed; company selection is irrelevant. |
| Enabled user whose company has no HLLM grant | Their `user_id` | Allowed under the accepted access policy. |
| Missing, expired, revoked, or disabled human session | None | Rejected; never uses the direct token scope. |
| Identity service unavailable or malformed context | None for human requests | Fails closed. |

Administrator role does not grant access to another user's HLLM dataset.
Profiles, encrypted provider credentials, workspace state, runs, history,
statistics, traces, artifacts, cache entries, and deletion/retention operations
must all follow the same product owner.

Keep the existing direct-token lifetime: it is revoked by rotating/removing the
deployment credential or removing its configured user scope. Human logout,
password changes, and Control Plane disabling do not revoke that independent
deployment credential. Do not claim otherwise or introduce a token registry in
this change. Disabling a human does invalidate their human sessions.

## 3. Verified source and implementation boundaries

| Verified source | Finding | Consequence |
| --- | --- | --- |
| [Go auth service](../internal/gateway/auth/service.go) | Human requests currently call `access.Client.Authorize()` and use `context.Account.ID`. | Call the existing `Resolve()` after service authentication and use `context.UserID`. |
| HLLM's pinned Control Plane Go module, `0a16ac252e4e` | `Resolve()` already returns `UserID` and accepts a valid context with `Account == nil`. | Keep the pin; no Control Plane API/client upgrade is needed. |
| `/home/kirill/p/prls-control-plane-public-access-release/src/accounts.ts`, source `86c5fd0` | Context resolution requires a valid unexpired session and a non-banned user; a company account is optional. | Reuse this supported identity boundary. Do not query its database from HLLM. |
| [Current application schema](../internal/postgres/migrations/0011_application.sql) | Owner columns are text and existing resource keys include the owner. | Keep the schema and indexes; no new user table or UUID migration is needed. |
| [Router](../frontend/lib/harden_llm_web/router.ex) | HLLM mounts shared company selection and requires `{:product, "harden-llm"}`. | Remove HLLM's `/accounts` routes and use shared `:require_access`. |
| Pinned `prls_web`, `e9af6a37ae6f459ff5dc2314491f90385c557f54` | HTTP `:require_access` already accepts an authenticated user with no company. | Reuse it directly; do not write a local authentication adapter. |
| `prls_web/lib/prls_web/access/live_auth.ex` | Revalidation reacts to company-account changes even for `:require_access`. | Fix the shared identity-only behavior before consuming it. |
| `prls_web/lib/prls_web/access/plug.ex` and `auth_controller.ex` | Missing/unsafe return paths use `/overview`; HLLM has no such route. | Add one product-configurable default path to the shared implementation. |
| [Profile provisioning](../scripts/shared-profiles.mjs) and [CLI](../cmd/harden-llm-gateway/shared_profiles.go) | Targets must currently be company UUIDs. | Replace that contract with explicit opaque user IDs. |
| [Profile service](../internal/gateway/profile_service.go) | Default profiles are seeded per owner without provider credentials. | Keep this behavior for new users; never automatically copy another user's keys. |
| [Bundle service](../internal/gateway/profile_resources.go) | Imported credentials are validated against the destination owner before probing. | Retain and certify this boundary with different login IDs. |
| [Gateway assembly](../cmd/harden-llm-gateway/server.go) | Artifact prefixes already use `traces.SafeObjectKeyComponent(ownerID)`. | Reuse it; do not construct a second user-ID encoding. |

These observations establish feasibility, not a passing implementation or new
production certification. Recheck revisions and deployment inputs when work
starts. Other checkouts contain independent work: create an appropriate branch
or worktree rather than moving their current branches.

Required repositories are `harden-llm` and the shared `prls-web` library.
Control Plane server, company memberships/grants, Portal, Analytics, ingress,
`system_setup`, and observability infrastructure need no implementation changes.
Company-account support remains in shared libraries for products that use it;
HLLM has one user-owned path and no company-account compatibility mode.

## 4. New configuration contract

The following names are proposed replacements, not existing capabilities:

| Current contract | Replacement | Definition |
| --- | --- | --- |
| `HARDEN_LLM_STATIC_TOKEN_ACCOUNT_ID` | `HARDEN_LLM_STATIC_TOKEN_USER_ID` | Optional explicit user scope for direct API calls; production uses the verification/test user's actual Control Plane ID. |
| `HARDEN_LLM_PROFILE_ACCOUNT_IDS` | `HARDEN_LLM_PROFILE_USER_IDS` | Explicit comma-separated user IDs to receive the trusted host profile configuration. |
| `sync-profiles --account-id <uuid>` | `sync-profiles --user-id <id>` | Applies profiles and freshly owner-bound encrypted credentials to that user. |
| `auth.Config.StaticAccountID` | `auth.Config.StaticUserID` | Direct-token owner passed to the auth service. |
| `profileAccountIDs()` / sync result `accounts` | `profileUserIDs()` / sync result `users` | Provisioning input and nonsecret summary use the actual domain. |

User IDs are opaque, nonempty values bounded to 128 bytes. Accept ordinary
non-UUID IDs; reject leading/trailing whitespace and control characters. Preserve
case and the ID itself. Do not derive it from an email, generate a substitute,
or reinterpret an old company UUID as a user. Use one Go validation function for
auth configuration and the CLI; validate the same input contract at the Node
configuration boundary.

Delete old names from active code, Compose inputs, examples, runbooks, and private
deployment inputs. Do not read both names, accept both CLI flags, or retain a
fallback. Historical release evidence stays historical.

## 5. Implementation phases

### P0. Establish the new contract and fixtures

1. Recheck HLLM `HEAD`, working-tree changes, the two dependency pins, and shared
   library policy. Use the repository's branch policy and preserve unrelated work.
2. Update ADR-HLLM-030 and the canonical backend/frontend identity sections to
   this decision. Fix the ADR's stale pending-cutover status. Update `AGENTS.md`
   so its previous shared-dataset instruction cannot mislead implementation.
3. Keep TEST-022's authentication and isolation invariant and update its
   ownership definition to stable user IDs. Explicitly record that its old
   two-different-logins/share-one-company assertion is superseded, not relaxed.
4. Mark WEB-TEST-106 and WEB-TEST-107 as retired company-grant/selector cases.
   Keep their historical definitions identifiable. Allocate new WEB-TEST-109
   through WEB-TEST-113 for the behaviors in section 6; recheck ID availability
   first. Preserve WEB-TEST-104, WEB-TEST-105, and WEB-TEST-108.
5. Establish fixtures for two distinct non-UUID users, two sessions belonging
   to one user, no selected account, and both users in the same company.
   Configure the direct token to the second user. Use synthetic secrets only.
6. Add the relevant failing regression immediately before each implementation
   step below. Do not add a large permanently failing suite ahead of all phases.

**Exit:** the new ownership/access policy and test oracles are explicit; no
production changes and no unresolved product decisions remain.

### P1. Correct the shared login/session library

Repository: `/home/kirill/p/prls-web`. Do not edit HLLM's installed dependency.

1. Add cheap tests in the shared library's test suite (new
   `test/access_live_auth_test.exs` and `test/auth_controller_test.exs` where
   needed) showing identity-only LiveAuth accepts `account: nil`, remains
   on the same user's workspace when their selected company changes, and stops
   actions if the resolved user changes or the session is revoked/unavailable.
2. In shared LiveAuth, compare the authenticated user on every revalidation.
   For `:require_access`, company changes do not change the resource scope.
   Retain account-change enforcement for the existing company-scoped consumers.
   Keep one shared implementation and the existing event/timer revalidation.
3. Add a shared `:default_return_to` configuration setting, consumed through
   the existing safe-return-path implementation. HLLM will set it to `/`.
   Existing consumers retain their established `/overview` default. Do not add
   a duplicate login controller, redirect route, or hardcoded HLLM product case.
4. Test GET/POST login without `return_to`, a valid deep link, and unsafe return
   paths. The product default must be validated as a safe local path. Preserve
   CSRF, cookie forwarding, sign-out, and existing company-flow tests.
5. Keep tests process-owned/parallel where possible. Do not add suite-wide
   serialization for application configuration; a genuine global-config test
   needs a named, bounded exception. Use the existing Req ownership tools.
6. Run the shared repository's `make verify`. Publish a verified immutable
   library revision through its normal source workflow, then update only HLLM's
   `frontend/mix.exs` and `mix.lock` pin to that revision.

**Exit:** shared library checks pass and HLLM can fetch the verified revision.
The login implementation has one safe-path validator and one LiveAuth hook.

### P2. Change the backend owner and deployment/provisioning inputs

1. Add TEST-022 auth regressions before changing the auth service: different
   users have different owners even inside the same company; two sessions for
   one user share an owner; a user without a selected company is accepted; the
   direct token resolves only the configured second user.
2. Keep current service-bearer/header/cookie validation. For a present human
   reference, call `controlPlane.Resolve(request.Context(), reference)` and
   return `Principal{OwnerID: context.UserID}`. Never call the product-entitlement
   `Authorize()` path or use the selected account as owner.
3. Preserve fresh identity resolution for every human request and typed error
   handling. A present malformed/revoked reference always fails, even when a
   valid direct-token scope is configured. Do not strip the reference and retry.
4. Rename auth/config/server assembly fields to the contract in section 4.
   Remove company UUID validation from HLLM auth. Use the shared Go user-ID
   validator for static scope and trusted profile CLI input.
5. Replace profile CLI and Node provisioning inputs/results together. Preserve
   stdin-only secret transport, environment allowlists, target-container checks,
   owner-bound encryption, and the existing no-provider-call sync behavior.
6. Update `.env.example`, `docker-compose.yml`, `deploy/preview/compose.yml`,
   `scripts/harden-structured-call.sh`, config/CLI tests, Node provisioning tests,
   capacity fixtures, and Compose fixture environment generation. The shell
   API example only needs the token: remove its redundant owner-ID parsing and
   UUID check because the gateway owns token scope.
7. Keep shared profile JSON and provider keys as the existing authoritative
   sources. Explicitly provision the administrator and verification user as
   separate owners. Other enabled users get unconfigured defaults unless the
   trusted provisioning command explicitly includes their IDs.
8. Remove old config reads and unknown-flag acceptance. Test that old-only token
   scope configuration cannot enable direct API use. Do not add a legacy-name
   adapter or a second deployment/provisioning command.
9. Run focused Go/Node checks and `make test-fast`. Fix every affected caller;
   do not publish or deploy a partly renamed configuration.

**Exit:** backend and configuration/provisioning contracts use actual user IDs,
fast checks pass, and there is no accepted company-account input in this path.

### P3. Make HLLM sign in directly to private data

1. In `frontend/config/runtime.exs`, set the shared `:default_return_to` to `/`
   for every HLLM environment. Reuse the pinned shared login controller.
2. In `frontend/lib/harden_llm_web/router.ex`, use `PrlsWeb.Access.Plug` and
   `PrlsWeb.Access.LiveAuth` with `:require_access`. Rename the HLLM pipeline to
   reflect authenticated identity and remove the unused account-selection
   pipeline and GET/POST `/accounts` routes.
3. Update `frontend/test/support/access_fixtures.ex` to model user identity,
   including `account: nil`. Remove its now-unused account list/select behavior.
   Keep the supported Control Plane context shape; do not invent a smaller API.
4. Add the new deterministic HTTP/LiveView regressions from section 6. Make the
   normal initial HLLM fixture account-less so ordinary tests catch accidental
   dependence on company selection. Use a separate company-switch fixture only
   for its specific regression.
5. Verify login POST goes directly to `/` or a valid protected deep link and
   `/accounts` has no HLLM route. Preserve login/logout, cookies, CSRF, service
   credential confinement, and per-event revalidation.
6. Keep the existing custom HLLM layout and styles. The signed-in identity can
   identify the owner; do not add a workspace picker, account UI, or new CSS.
7. Run the focused deterministic Phoenix tests, formatting checks, and
   `make test-fast` with the pinned Elixir/OTP toolchain exposed.

**Exit:** sign-in works without company selection, HTTP and LiveView share the
same identity rule, and fast checks pass.

### P4. Prove isolation across real storage and synchronize documentation

1. Extend existing gateway/resource/Postgres/Garage integration tests using the
   real auth service plus a process-owned Control Plane HTTP fixture. A fake
   principal alone does not prove the new auth-to-storage connection.
2. Cover the exact boundaries in section 6: state, profiles/credentials/bundles,
   runs/history/stats/traces, artifacts/deletion, cache, and same-user token
   equivalence. Reuse existing fixtures and provider stubs instead of adding
   another full test stack or duplicating the same matrix at every tier. Keep
   persistence/restart certification in the disposable integration/Compose
   topology, rather than recreating production again only to test durability.
3. Leave owner-scoped SQL, existing text keys, the current migration, encryption
   bindings, and artifact prefix generation unchanged unless a regression
   demonstrates a specific missing boundary. There is no schema redesign.
4. Update `internal/smoke/harness_compose.go` and the frontend Control Plane
   fixture to use user IDs. Align the fixture's human `user_id` with its token
   owner when the scenario expects the same data. Start with `account: nil` and
   remove fixture account-list/select endpoints HLLM no longer calls.
5. Update affected browser-test source/helpers only if the changed routing or
   fixtures require it. Do not run browser gates or claim browser certification.
6. Update current documentation once at its owner: ADR-HLLM-030, architecture,
   self-hosting, environment, shared LLM configuration, API/library guide,
   preview guide, README, canonical backend/frontend specifications, and
   requirements traceability. Replace the current decision record with the new
   accepted decision and a link to historical release evidence.
7. In `api/openapi.yaml`, describe user ownership and the two request forms in
   the existing bearer scheme. Do not add a client-supplied owner field or a new
   resource-selection parameter. Wire resource payloads remain unchanged.
8. Register new/retired frontend IDs in `test/test-tiers.json` and its existing
   policy checks. Retain tier ownership, assertion strength, race coverage,
   cleanup evidence, and browser opt-in policy.
9. Run the focused integration/API tracks and `make test-fast`; record the exact
   assertions each tier proves. Address failures before proceeding.

**Exit:** real storage proves private ownership, maintained specs match the
implementation, and no current HLLM guide instructs company selection.

### P5. Certify and perform the clean production cutover

1. Run `make test-release` on the completed candidate. It already includes the
   deterministic backend/integration/Compose/race release gates. Do not run a
   second broad suite merely to increase the number of checks. All required
   checks must pass with clean resource receipts.
2. Push verified source checkpoints, promote the completed change to `main`
   through the established workflow, and verify hosted required checks. The
   exact application SHA promoted and built must have release certification;
   recertify if promotion incorporates additional application changes. Build
   gateway and web with that SHA and the published shared-library pin. Record
   exact source revisions and image IDs.
3. Using the existing Control Plane admin interface/API and canonical credential
   source, resolve the actual administrator and verification/test user IDs.
   Match the latter to the existing `TEST_LOGIN` identity. Do not rotate login
   credentials, copy passwords into HLLM, or inspect/reuse someone's session.
4. Prepare the existing production inputs and trusted shared host provisioning
   input: token scope is the verification user's ID; provisioning targets are
   both actual user IDs. Remove the old account-scope variables wherever these
   approved inputs currently contain them. Retain private file permissions and
   other environment ownership. Recheck preview input consistency too.
5. Use the existing production descriptor/preflight and approved Compose
   procedure. Rebuild/recreate only gateway and web; retain the other service
   images and durable mounts. Prepare candidate images/configuration before
   the outage. Do not deploy either half against the old ownership model.
6. Stop HLLM gateway and web. Clear only the dedicated HLLM application database
   and `harden-llm-artifacts` objects. Do not delete the shared Garage service,
   Control Plane users/sessions/accounts, other buckets, or observability data.
   Reuse the established reset procedure; no rehome bridge or dual-read mode.
7. Initialize the unchanged current schema, run the trusted `sync-profiles
   --user-id <id>` path for both users, and then resume gateway/web. Credentials
   are encrypted separately with each owner binding. Confirm both data owners
   exist and the previous shared company owner has no remaining product rows.
8. Verify service health/readiness and production-descriptor equivalence against
   the exact candidate release. A stopped/failed initialization stays an outage
   while it is fixed; do not serve a mixed release or reconnect old shared data.

**Exit:** the certified application is running with private user-owned fresh
data. New production acceptance is still required before calling it complete.

### P6. Accept production behavior and close out

1. Run authenticated HTTP acceptance with fresh probe-owned sessions for both
   real users. Submit the actual Phoenix login forms with CSRF handling. Verify
   direct entry and profile/bundle reads through Phoenix and the gateway, with
   no request or redirect to `/accounts` and no call to company-selection APIs.
2. Prove separation with distinct, nonsecret `userPrompt` markers in the valid
   `/api/v1/state` documents for the two users. Each human sees only their own
   marker; the direct token sees and changes only the verification user's marker.
   Restore the original state documents before ending acceptance. Do not treat
   identical freshly seeded profile lists as proof of sharing or separation.
3. Verify a second fresh session for one user sees their same persisted state.
   Inspect fresh owner IDs and empty legacy history without printing secrets.
   Use the certified integration/Compose evidence for restart durability and
   other storage paths; do not add another production outage for those checks.
4. Log out owned sessions and prove their human references are rejected even
   with a valid service bearer and configured token owner. Anonymous/wrong-token
   requests also fail. Cover disabled-user and unavailable-authority behavior
   with deterministic fixtures; do not disable a real production user or stop
   the shared identity service just for a smoke check.
5. Record HLLM branch/source SHA, shared-library revision, component image IDs,
   URLs, hosted checks, release results, observed user/token ownership, and probe
   cleanup in the established release journal and bounded nonsecret evidence.
6. Publish the final release evidence and documentation through the established
   main workflow and verify local `HEAD`, `origin/main`, and remote main agree.
   Record separately any later docs-only commit from the actual application-image
   SHA. Deployment preflight and HTTP results must refer to the running
   application SHA; a documentation-only closeout needs no image rebuild.
7. Close only issues fully satisfied by this change. Leave other products' work
   untouched. Report browser layout and paid-provider behavior as unchecked.

**Exit:** all required tests/CI pass, deployed HTTP acceptance proves private
ownership and verification-token equivalence, documentation is current, and
source/runtime identities are recorded accurately.

## 6. Required test oracles

Add or identify cheap regressions before implementation. Keep the higher tier
only for the distinct storage/network boundary it proves.

| Boundary | Exact oracle | Primary evidence |
| --- | --- | --- |
| Identity and owner | Different users in the same company return different owner IDs; two sessions for one user return the same ID; changing company/email/role does not change that owner. | TEST-022, Go `httptest`; real storage in existing integration cases. |
| Login-independent company context | Valid `account: nil` and an unrelated company's product list allow entry. | TEST-022; new WEB-TEST-109. |
| Direct sign-in | Missing/unsafe return path resolves to HLLM `/`; a valid deep link is preserved; no selector redirect or route exists. | New WEB-TEST-110; shared library controller/Plug tests. |
| Connected identity | Company switch keeps identity-only view usable; changed user, revocation, or outage halts events and never reuses loaded data. | New WEB-TEST-111; shared LiveAuth tests using LiveViewTest. |
| Token and denial | Token owner is the configured verification ID; same-user sessions share its data; administrator differs; malformed/revoked human references never become token requests. | TEST-022; new WEB-TEST-112; production state-marker HTTP checks. |
| State/profile isolation | Both users may store the same resource ID with different content; edits/deletion by one do not change the other; default seeding copies no provider credentials. | TEST-022/TEST-024/TEST-053; real Postgres. |
| Credential/bundle isolation | Another user's credential record or encrypted bundle is rejected before provider probing or writes; each legitimate owner's bundle round trip still works. | TEST-018/TEST-022/TEST-024; existing vault and integration tests. |
| History and statistics | A user's runs/history/stats contain only their records. Other-user trace IDs do not reveal traces or artifacts. Clear/delete affects only the authenticated user. | TEST-022/TEST-024; real Postgres/Garage and a local provider stub. |
| Cache isolation | Identical requests from different users do not reuse another user's cache; a subsequent same-user call retains the existing hit semantics. | TEST-011/TEST-022/TEST-024; real owner-scoped cache adapter. |
| Artifact scope | Artifact keys use the existing owner-derived prefix; cross-owner reads/presigns/deletes fail, and deletion/retention preserves the other owner's objects. | TEST-021/TEST-022/TEST-024 and existing artifact lifecycle integration tests. |
| Persistence and cookie boundary | Reconnect/new session and application restart retain the same user's data; browser-visible material contains no service bearer/provider key; secure encrypted host-only cookie remains unchanged. | WEB-TEST-108; new WEB-TEST-113; real storage and HTTP acceptance. |
| Provisioning contract | Non-UUID user IDs work; missing/duplicate/invalid IDs fail; sync applies only explicit owners, with secrets on stdin and no provider call; old CLI flag is rejected. | TEST-062; Go CLI and plain Node tests. |

Do not weaken resource-isolation or credential-probe oracles to accommodate the
new identity source. Retire only assertions tied specifically to the explicitly
replaced company-grant/selector feature, and record that reason in the catalog.

## 7. Verification commands

Expose the pinned toolchain before local HLLM/frontend verification:

```bash
export PATH=/home/kirill/.local/elixir-1.20.2/bin:/home/kirill/.local/otp-28.4.3/bin:$PATH
```

The normal development checks, selected at the phases above, are:

```bash
go test ./internal/gateway/auth ./cmd/harden-llm-gateway -count=1
node --test scripts/test/shared_profiles_test.mjs
make test-fast
git diff HEAD --check
```

Run the focused Phoenix command from `frontend/`:

```bash
mix test test/harden_llm_web/shared_identity_test.exs
mix format --check-formatted
```

Run `make verify` in the shared `prls-web` repository after its changes.
Run `make test-integration` / `make test-api` for the changed storage/API
boundaries, then one completed-candidate `make test-release`. Use existing task
registration and runner policy; do not introduce another test orchestrator.
No application tests, builds, browsers, or provider calls are needed to validate
this plan-only change.

## 8. Plan review and completion conditions

The source review found and resolved these weaknesses in a naive implementation:

1. **Removing the picker alone leaves company-owned data.** The plan changes
   `Principal.OwnerID` and both HTTP/LiveView access requirements together.
2. **User IDs are not company UUIDs.** Configuration and provisioning use the
   verified opaque identity contract and preserve IDs rather than renaming a
   variable while retaining UUID checks.
3. **Shared login has non-HLLM behavior.** Missing return paths and connected
   company switches are fixed once in `prls-web`, with tests for its existing
   consumers; no local copies or `/overview` alias are introduced.
4. **A token must have an explicit owner and lifetime.** The user chose the
   verification login. The existing deployment-managed lifetime is documented
   accurately; human sessions still cannot fall back to that token scope.
5. **Default provisioning must not share secrets implicitly.** New users get
   unconfigured defaults. Only explicit user-ID targets receive trusted host
   provider credentials, encrypted separately for each owner.
6. **A passing stub is insufficient.** Real auth-to-Postgres/Garage coverage and
   production state-marker checks prove separation and token equivalence.
7. **A schema rewrite or migration bridge would add work without value.** The
   existing text owner keys and current migration already fit. The accepted
   cutover is a bounded HLLM-only reset, with no legacy reader or recovery mode.
8. **A shared-library change need not expand into a Control Plane redesign.**
   Existing `Resolve()`/`:require_access` support the accepted access policy.
   Keep the Control Plane pin/server and other products' ownership models.
9. **A private dataset can leak through more than its list endpoint.** The
   acceptance matrix includes bundles, cache, presigns, deletion, statistics,
   and persistence, using existing boundaries rather than new frameworks.

The completed verification confirms the existing owner-scoped persistence and
fresh identity resolution support the accepted model. Shared-library session
revalidation, build credentials, hosted checks and production acceptance have
all been exercised. No product-choice or deployment blocker remains. Browser
layout and paid-provider behavior remain outside the accepted release scope.

## 9. Execution record

- P0: ownership contract and retired WEB-TEST-106/107 recorded; new IDs 109-113 allocated.
- P1: shared library regression failed before changes; full 34-test host verification
  passed with the pinned toolchain (`make verify MIX_RUN=env`). Docker's reused
  host-built LazyHTML NIF cannot run under Alpine; no assertion was changed.
  Published revision `7e013b376811a58db2348856c1e5932ebc446e40` in
  [prls-web PR 11](https://github.com/prls-co/prls-web/pull/11), merged as
  `3281ae59fab6a415daa43e6ae3bb39a7c8392fdc`; hosted branch/PR/main checks passed.
- P2: focused auth/config/CLI and Node checks passed; fast gate accepted
  `runner-1791133477850-3840368-7a095b68395256c4.json`.
- P3: the new frontend regression failed before routing/default changes; all
  11 identity tests passed. Fast gate accepted
  `runner-1791133769983-3877890-61cf293e88a61420.json`.
- P4: real Postgres/Garage and local TLS-provider tests passed, including
  same-user token scope, independent cache entries, foreign encrypted bundle
  rejection before probing/writes, and same-ID profile/history/artifact deletion
  isolation. An in-process regression first identified the foreign bundle's
  incorrect outage classification; the API now returns credential validation 422.
  Integration accepted `runner-1791134257909-3951781-dd7e96fdf905d235.json`;
  `make test-api` passed. Fast accepted
  `runner-1791134348657-3951782-087615247e4e3f93.json`. Receipts report no cleanup
  errors or warnings. Actual administrator and verification IDs were resolved
  using fresh owned Control Plane sessions, then those sessions were logged out.
- P5: browser-free `make test-release` passed 29/29 on application SHA
  `07a7781cf735764c1cb31b02a75df7d647b78a18`; report
  `runner-1791135157154-3982905-59561a8de490fb7b.json` has no cleanup errors or
  warnings. The exact commit was fast-forwarded to main; PR 91 merged and hosted
  main fast/CodeQL passed. Gateway/web images were built with matching OCI
  version/revision labels. The dedicated database was reset to schema 11 and
  both actual users received 32 profiles and 22 owner-bound credentials each.
  The dedicated bucket is empty (zero objects needed deleting). The initial
  password-less `psql` connection failed before any data change; supplying the
  existing approved credential through its environment completed the reset.
  All six support containers/images stayed unchanged and descriptor checks are
  equivalent. Preview shared inputs match both user IDs; old scope names are gone.
- P6: nine public authenticated HTTP acceptance groups passed using actual
  Phoenix forms, independent markers, a second same-user session, and fresh
  logout revocation. Token access matched only the verification user. Original
  state documents were restored and all probe sessions logged out. Trusted
  profile readback returned `changed: false` for both owners, proving the
  existing encrypted credentials match the authoritative source without a
  provider call. Schema/owner audit found no legacy company rows, no shared
  ciphertext pairs, and empty history/traces/cache/artifacts. No open HLLM
  issues remain. Final source closeout is documentation/evidence only; running
  images continue to use the certified application SHA.

The final [release record](../docs/release-certification.md#login-owned-private-data--production-2026-10-04)
and [bounded evidence](evidence/harden-llm/login-owned-production-20261004.json)
record image identities, gate results, ownership, public URLs and probe cleanup.
