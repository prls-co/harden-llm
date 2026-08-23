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

- `make verify` — aggregate deterministic backend gate; requires Docker for integration slices.
- `make test-unit`, `make test-parity`, `make test-integration`, and `make test-api` — focused backend gates.
- `cd frontend && mix test` — deterministic Phoenix tests with the exact Elixir version pinned by `frontend/mix.exs`.
- `cd frontend && mix test --only browser` — opt-in browser tests.
- `git diff HEAD --check` — whitespace validation for tracked changes.

Do not report Docker, live-provider, or browser gates as passing unless they ran in the current environment or the result is explicitly identified as retained certification evidence.

## Coding Style & Naming Conventions

Use ATX Markdown headings, numbered major specification sections, fenced code blocks with language tags, and backticks for paths, commands, IDs, and symbols. Preserve the established kebab-case document names. Go code must be `gofmt`-clean, use lowercase package names, and name tests `*_test.go`. Elixir code must pass `mix format`; use `snake_case` files and `*_test.exs` tests.

## Testing Guidelines

Backend tests use canonical `TEST-###` identifiers and must reference `SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001`. Frontend cases use `WEB-TEST-###`. Add deterministic coverage before implementation. Deterministic provider tests use local `httptest` servers; only tests using public internet or real provider credentials use the `live` build tag and stay outside `make verify`.

## Commit & Pull Request Guidelines

History favors concise conventional subjects such as `docs: define backend REST contract`; continue with `docs:`, `feat:`, `fix:`, or `test:` as appropriate. PRs should summarize the change, name affected specification or test IDs, call out OpenAPI or ownership-boundary changes, list validation run, and link relevant issues. Include screenshots only for rendered UI changes.

## Security & Configuration

Never commit provider credentials, bearer tokens, session material, or unredacted diagnostic fixtures. Treat `/home/kirill/utility-llm` as a read-only contract source and record fixture provenance rather than copying secrets or live output.
