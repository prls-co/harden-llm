# Repository Guidelines

## Project Structure & Module Organization

This checkout contains the self-hosted implementation and its canonical specifications in `plans/from_utility-llm/`:

- `harden-llm-self-hosted-implementation-plan.md` defines phased backend delivery.
- `self-hosted-go-stack-spec.md` defines the Go, REST, storage, and deployment architecture.
- `harden-llm-self-hosted-test-spec.md` is the canonical backend test catalog.
- `phoenix-liveview-frontend-spec.md` defines the separate `frontend/` application.
- `harden-llm-parallel-test-feedback-plan.md` defines the approved but not yet
  implemented hierarchy for cheap parallel feedback and targeted expensive
  certification.

The public Go package is at the repository root, internal packages are under `internal/`, the gateway is under `cmd/harden-llm-gateway/`, and the shared REST contract is `api/openapi.yaml`. Phoenix code belongs only in `frontend/`. Keep backend and frontend coupled through OpenAPI, never through internal types.

## Build, Test, and Development Commands

Use these implemented repository gates:

- `make test-fast` — the repeated coding loop: default-tag Go, parity/static
  checks, deterministic Phoenix/LiveViewTest, and dependency-free client-core
  tests. It is T0-T2, offline, credential-free, and must not start Docker,
  Chromium, or a public provider path.
- `make verify` — aggregate deterministic backend gate; requires Docker for integration slices.
- `make test-unit`, `make test-parity`, `make test-integration`, and `make test-api` — focused backend gates.
- `make test-browser` — the two ordinary targeted Chromium canaries; the
  Compose browser feature remains release-only.
- `make test-release` — the complete manifest-owned release candidate. Use it
  before review or promotion, not after every edit.
- `cd frontend && mix test` — deterministic Phoenix tests with the exact Elixir version pinned by `frontend/mix.exs`.
- `cd frontend && mix test --only browser` — opt-in browser tests.
- `git diff HEAD --check` — whitespace validation for tracked changes.

Do not report Docker, live-provider, or browser gates as passing unless they ran in the current environment or the result is explicitly identified as retained certification evidence.

## Test Feedback Methodology

Use the lowest sufficient tier for the invariant being changed. Run `make
test-fast` repeatedly while coding; it is intentionally broad and cheap. T0
pure checks cover parsing, validation, fixtures, static policy, and pure client
decisions. T1 in-process checks cover `httptest`, ConnCase, LiveView events and
diffs, and process-local stubs. T2 covers the extracted JavaScript decision
core under Node without a DOM emulator. T3 is for real Postgres/Garage,
integration lifecycle, and race boundaries. T4 is for native browser events,
LiveSocket patching, focus, layout, and hook adapters. T5 is for full Compose,
deployed behavior, or explicitly authorized live providers.

Keep the assertion oracle unchanged when moving a case down a tier. A higher
tier may remain for a fact the lower tier cannot observe, but it must not carry
permutations that LiveViewTest or a pure function can prove exactly. Prefer
unique process-owned fixtures, `async: true`, private Req ownership, and
parallel execution. A serial exception needs a named global resource and a
machine-checked rationale; do not add serialization to hide a race.

When an expensive-tier defect is found, first add or identify a T0-T2 cheap
regression for its root invariant. Keep the T3-T5 case only for its distinct
service, browser, deployment, or provider boundary. Never make a test green
by weakening assertions, retrying an ambiguous run, skipping a required case,
or replacing a real boundary with an unproved fake.

Happy DOM and jsdom are not dependencies in the current design. Promote a DOM
emulator only after concrete adapter defects require APIs that pure functions
cannot express; record the missing APIs, compare both candidates, retain a
real browser canary, and amend ADR-HLLM-015 first.

## Coding Style & Naming Conventions

Use ATX Markdown headings, numbered major specification sections, fenced code blocks with language tags, and backticks for paths, commands, IDs, and symbols. Preserve the established kebab-case document names. Go code must be `gofmt`-clean, use lowercase package names, and name tests `*_test.go`. Elixir code must pass `mix format`; use `snake_case` files and `*_test.exs` tests.

## Testing Guidelines

Backend tests use canonical `TEST-###` identifiers and must reference `SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001`. Frontend cases use `WEB-TEST-###`. Add deterministic coverage before implementation. Deterministic provider tests use local `httptest` servers; only tests using public internet or real provider credentials use the `live` build tag and stay outside `make verify`.

## Commit & Pull Request Guidelines

History favors concise conventional subjects such as `docs: define backend REST contract`; continue with `docs:`, `feat:`, `fix:`, or `test:` as appropriate. PRs should summarize the change, name affected specification or test IDs, call out OpenAPI or ownership-boundary changes, list validation run, and link relevant issues. Include screenshots only for rendered UI changes.

## Security & Configuration

Never commit provider credentials, bearer tokens, session material, or unredacted diagnostic fixtures. Treat `/home/kirill/utility-llm` as a read-only contract source and record fixture provenance rather than copying secrets or live output.
