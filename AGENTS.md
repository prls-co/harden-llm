# Repository Guidelines

## Project structure and ownership

The Go library is at the repository root, internal packages are under
`internal/`, the REST gateway is under `cmd/harden-llm-gateway/`, and
`api/openapi.yaml` is the canonical gateway contract. Phoenix code belongs only
in `frontend/`. Keep Go and Phoenix coupled through OpenAPI, never internal Go
types.

The accepted boundary is defined in
`plans/restore-reference-workflows-decoupling-plan.md` (v5.3), ADR-HLLM-033 and
ADR-HLLM-034. The older simplification plan and ADR-HLLM-031 are historical.
Implementation progress is recorded under
`plans/evidence/harden-llm/reference-decoupling/`; a target contract is not
proof that its implementation or deployment is complete. Backend specifications
and the canonical test catalog are under `plans/from_utility-llm/`; follow
ADR-HLLM-015 and the existing test-feedback policy.

The root Go library is the stateless execution core: retries, JSON repair,
recovery, cache identity and accounting through injected effects. It owns no
authentication, profile/history storage, concrete provider construction or
telemetry SDK. Public `stateful/` composes those concrete effects and owns
key-scoped profiles, drafts, encrypted credentials, cache, reference history and
artifacts. One Go gateway serves ordinary `/v1` and reference `/api/v1` through
one bearer validator and one engine. Derive `api_key_hash` only after matching
one configured key; reject caller-supplied scope selectors. `CPA_API_KEY` is
upstream-only. The required `api_key_envs` connection metadata array names
protected environment values, initially `HARDEN_LLM_TOKEN`; no scalar fallback
or local key-issuance/user registry exists.

Git presets contain metadata and starting values, never keys. Explicit Save
uses create-on-save semantics: a new immutable complete setup with UUID,
server UTC date and name suffix. Editing/refresh only changes the draft. The
wrapper uses one isolated HLLM database/role on Agent Platform PostgreSQL and
one pool; Garage holds path-scoped artifacts. Laminar receives diagnostic
traces only. Direct `/v1` calls do not enter history. Unknown history COMMIT
with absent/failed readback stays unconfirmed; never replay inference or insert.

Phoenix owns Portal admission, routes and async lifecycle. Every enabled login
may enter; the host's configured API key selects profiles/history/statistics.
Drafts additionally use host-supplied `workspace:<stable Portal user ID>` inside
that key scope. `HardenLlmUI` under `frontend/packages/harden_llm_ui/` owns the
reusable components/client/styles and receives a server-held key and draft key
from its host. Components use OpenAPI, never internal Go types or host identity
modules. Model Recall is the read-only interface reference, not a migration gate.

## Build and test commands

- `make test-fast` is the repeated coding loop: default-tag Go,
  parity/static checks, deterministic Phoenix/LiveViewTest, and dependency-free
  client-core tests. It is offline, credential-free, and must not start Docker,
  Chromium, or a public provider path.
- `make verify` is the aggregate deterministic backend gate and requires Docker
  for integration slices.
- `make test-unit`, `make test-parity`, `make test-integration`, and
  `make test-api` are focused backend gates.
- `make test-browser` and `make test-browser-compose` are explicit browser
  opt-ins. Run them only when the user specifically requests browser testing.
- `make test-release` is the broader browser-free certification for explicit
  releases and cross-system changes.
- `cd frontend && mix test` runs deterministic Phoenix tests with the Elixir
  and OTP versions pinned by `frontend/mix.exs`.
- `git diff HEAD --check` validates whitespace.

On the reference host, expose the pinned toolchain before invoking `mix`,
`make test-fast`, or `make test-release`:

```sh
PATH=/home/kirill/.local/elixir-1.20.2/bin:/home/kirill/.local/otp-28.4.3/bin:$PATH
```

Hosted jobs install the equivalent toolchain. Never claim Docker,
live-provider, or browser tests passed unless they ran in this environment or
are clearly identified as retained evidence.

## Test feedback methodology

Read and follow `docs/liveview-go-testing-guidelines.md` in full before
planning changes, writing tests, or running verification. Use fast Go,
Elixir/LiveView, and plain Node tests by default; optional DOM tests require a
concrete need and an ADR-HLLM-015 update; browsers are opt-in by explicit user
request. These practical levels do not renumber T0-T5.

Use the lowest sufficient tier. T0 covers pure parsing, validation, fixtures,
static policy, and client decisions. T1 covers `httptest`, ConnCase, LiveView
events/diffs, and process-local stubs. T2 covers JavaScript decision logic
under Node without a DOM emulator. T3 covers real PostgreSQL, integration
lifecycle, and race boundaries. T4 covers native browser events, LiveSocket,
focus, layout, and hooks. T5 covers full Compose, deployed behavior, or
explicitly authorized live providers.

Keep the assertion oracle unchanged when moving a case down a tier. Prefer
unique process-owned fixtures, `async: true`, private Req ownership, and
parallel execution. Serial exceptions require a named global resource and a
machine-checked rationale. Never make a test green by weakening assertions,
retrying an ambiguous run, skipping a required case, or replacing a real
boundary with an unproved fake. When an expensive-tier defect is found, add a
cheap T0-T2 regression for its root invariant when representable; keep the
expensive test only for its distinct boundary.

Happy DOM and jsdom are not current dependencies. Do not install a DOM emulator
unless concrete adapter defects require APIs pure functions cannot express;
compare candidates, retain a real browser canary, and amend ADR-HLLM-015 first.

## Coding and test conventions

Use ATX Markdown headings, numbered major specification sections, fenced code
blocks with language tags, and backticks for paths, commands, IDs, and symbols.
Preserve kebab-case document names. Go must be `gofmt`-clean, use lowercase
package names, and name tests `*_test.go`. Elixir must pass `mix format`; use
`snake_case` files and `*_test.exs` tests.

Backend tests use canonical `TEST-###` IDs and reference
`SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001`. Frontend cases use `WEB-TEST-###`.
Add deterministic coverage before implementation. Deterministic provider tests
use local `httptest` servers; public internet or real provider credentials use
the `live` build tag and stay outside `make verify`.

## Branches and deployment

Use `dev` for normal iteration and create feature branches from the current
`dev` or `main` policy. Push verified checkpoints promptly. `dev` deploys to
`https://harden-llm-dev.prls.co/` after browser-free fast checks. Other trusted
branches get stable URLs only after explicit preview enablement (manual
workflow or `deploy:preview` PR label). See `docs/preview-environments.md` for
URLs, environment ownership, and cleanup.

Never launch Chromium, Wallaby, Playwright, a deployed browser canary, or a
browser-containing Compose test unless the user specifically requests browser
testing. UI work, deployment, `verify`, and `production-ready` do not authorize
browsers. Use component/LiveView/Node tests and HTTP health/auth checks; state
when actual browser layout has not been checked. Never run a real provider call
as an automatic deployment smoke test.

Routine changes do not require full release certification or production
deployment. Promote to `main`/production only when explicitly requested. Skip
application builds for docs/test-only changes and rebuild only affected
services. Preview logins use the shared Control Plane/Portal. The trusted
preview host reads provider keys and shared scalar settings from its approved
`sharedEnvFile`; the JSON named by `HARDEN_LLM_CONFIG_FILE` contains connection
metadata and environment-variable names only. There is no profile-sync or startup preset seeding. Profile Save is an explicit
reference operation with the preserved bounded provider probe; test it locally.

Keep infrastructure credentials, signing keys, database data, sessions, and
networks separate; never copy production datasets or bearer sessions. Only
trusted branches may receive the shared provider credential because it can
authorize paid upstream calls. Human identity/access belong to Control Plane.
Every enabled login may enter without company selection or an HLLM grant. All
clients holding the same HLLM key share its profiles/history/statistics;
different keys are isolated. The frontend initially uses `HARDEN_LLM_TOKEN`,
also accepted by external OpenAI clients. PostgreSQL persistence belongs to the
Go stateful wrapper; Phoenix Ecto persistence is removed after its verified
replacement in P03. Keep the approved clean-cut scope confined to HLLM data.

The authorized clean cut removes incompatible HLLM-owned data only; other
products and shared identity/storage data remain outside that reset. Before
reporting a deployed change, record branch, source SHA, component image
identities, environment URL, and browser-free checks. Report blockers rather
than implying an undeployed change is live.

## Security and configuration

Never commit provider credentials, bearer tokens, session material, or
unredacted diagnostic fixtures. Treat `/home/kirill/utility-llm` as a read-only
contract source and record fixture provenance rather than copying secrets or
live output.
