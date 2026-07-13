# Repository Guidelines

## Project Structure & Module Organization

This checkout is currently specification-first. All current implementation specifications live in `plans/from_utility-llm/`:

- `harden-llm-self-hosted-implementation-plan.md` defines phased backend delivery.
- `self-hosted-go-stack-spec.md` defines the Go, REST, storage, and deployment architecture.
- `harden-llm-self-hosted-test-spec.md` is the canonical backend test catalog.
- `phoenix-liveview-frontend-spec.md` defines the separate `frontend/` application.

The planned implementation places the public Go package at the repository root, internal packages under `internal/`, the gateway under `cmd/harden-llm-gateway/`, and the shared REST contract at `api/openapi.yaml`. Phoenix code belongs only in `frontend/`. Keep backend and frontend coupled through OpenAPI, never through internal types.

## Build, Test, and Development Commands

There is no executable scaffold, `Makefile`, `go.mod`, or `mix.exs` yet. For current documentation changes, run:

- `git diff HEAD --check` — catch whitespace errors in staged and unstaged tracked changes.
- `rg -n '^## ' plans/from_utility-llm/*.md` — review document structure quickly.

After the planned scaffold lands, `make verify` is the aggregate deterministic backend gate; focused targets include `make test-unit`, `make test-integration`, and `make test-api`. Run Phoenix tests with `cd frontend && mix test`; browser tests use `mix test --only browser`. Do not report these future commands as passing before their supporting files exist.

## Coding Style & Naming Conventions

Use ATX Markdown headings, numbered major specification sections, fenced code blocks with language tags, and backticks for paths, commands, IDs, and symbols. Preserve the established kebab-case document names. Go code must be `gofmt`-clean, use lowercase package names, and name tests `*_test.go`. Elixir code must pass `mix format`; use `snake_case` files and `*_test.exs` tests.

## Testing Guidelines

Backend tests use canonical `TEST-###` identifiers and must reference `SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001`. Frontend cases use `WEB-TEST-###`. Add deterministic coverage before implementation. Deterministic provider tests use local `httptest` servers; only tests using public internet or real provider credentials use the `live` build tag and stay outside `make verify`.

## Commit & Pull Request Guidelines

History favors concise conventional subjects such as `docs: define backend REST contract`; continue with `docs:`, `feat:`, `fix:`, or `test:` as appropriate. PRs should summarize the change, name affected specification or test IDs, call out OpenAPI or ownership-boundary changes, list validation run, and link relevant issues. Include screenshots only for rendered UI changes.

## Security & Configuration

Never commit provider credentials, bearer tokens, session material, or unredacted diagnostic fixtures. Treat `/home/kirill/p/utility-llm` as a read-only contract source and record fixture provenance rather than copying secrets or live output.
