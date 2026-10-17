# Harden LLM

Harden LLM provides a stateless Go hardening core, a reusable stateful wrapper,
an OpenAI-compatible gateway, and a Phoenix reference application. The core
owns retries, JSON repair, recovery, validation and accounting through injected
effects. The wrapper owns API-key-scoped profiles, cache, drafts, history and
artifacts. Ordinary `/v1` requests never enter product history.

The reference UI restores the picker/editor, input, response, statistics,
history and trace workflows from PR #95. Its reusable `HardenLlmUI` package
uses REST; shared PRLS Portal handles human access. Logins using the same host
API key share profiles/history and keep separate workspace drafts.

The incoming API bearer is `HARDEN_LLM_TOKEN`, stored in the ignored root
`.env`. `CPA_API_KEY` is used only by the gateway for its configured upstream.
The checked-in connection example is [`config/upstreams.example.json`](config/upstreams.example.json).
See [the API contract](api/openapi.yaml), [architecture](docs/architecture.md),
and [self-hosting guide](docs/self-hosting.md).

## Repository map

- Root Go package: stateless hardening client with one execution engine.
- `stateful/`: provider effects, scoped PostgreSQL state and Garage artifacts.
- `httpclient/`: HTTP-only Go client for the deployed profile/run reference API; no in-process provider effects or inference replay.
- `cmd/harden-llm-gateway/`: `/v1` inference and `/api/v1` reference HTTP gateway.
- `internal/`: provider transports, recovery, cache, API codecs, telemetry, and tests.
- `api/openapi.yaml`: authoritative gateway contract and Go/Phoenix boundary.
- `frontend/`: Phoenix host; `frontend/packages/harden_llm_ui/` is the reusable UI/client package.
- `deploy/` and `docker-compose.yml`: deployment and isolated test topologies.
- `plans/from_utility-llm/`: canonical Go, API, frontend, and test specifications.
- `docs/`: operating instructions, ownership, and retained release evidence.

## Development and tests

Use `make test-fast` as the repeated edit-test loop. It runs offline, credential-
free Go, static/parity, Phoenix/LiveViewTest, and plain Node checks. It does not
start Docker, Chromium, or an LLM provider.

```bash
make test-fast
make verify                 # Go verification; Docker required for integration slices
make test-release           # browser-free release certification; Docker required
```

Frontend deterministic tests use the Elixir and OTP versions pinned in
`frontend/mix.exs`. Browser and browser-containing Compose tests are explicit
opt-ins and require a direct browser-testing request. Live provider calls are
separate opt-in evidence.

See [`docs/liveview-go-testing-guidelines.md`](docs/liveview-go-testing-guidelines.md)
for test-tier selection and [`test/test-tiers.json`](test/test-tiers.json) for
the repository task graph.

## Self-hosted setup

Copy `.env.example` to `.env`, preserve it as a private mode-0600 file, and set
the incoming token, CPA key, absolute path to a connection JSON file, database
URL, credential-encryption key, Garage settings, hostnames, and existing
Control Plane/Portal settings. Keep provider
credentials in environment variables; the connection JSON stores only an
environment-variable name. Follow [the self-hosting guide](docs/self-hosting.md)
for the one-shot schema command, startup, and browser-free HTTP checks.

OpenAI-compatible clients use the API origin as their base URL with the same
`HARDEN_LLM_TOKEN` value. For example, an SDK should use
`https://harden-llm.prls.co/v1`; it does not need a Harden-specific request
wrapper for supported OpenAI fields. See [API and Go library examples](docs/api-and-library.md)
for request examples and the supported subset.

Go services using key-scoped profiles and structured recovery over HTTP use
[`github.com/prls-co/harden-llm/httpclient`](httpclient/README.md). It consumes
the same `/api/v1` contract as the reusable Phoenix client, distinguishes a
complete inference from uncertain history recording, and never retries an
inference POST. It is separate from the root in-process stateless engine.

## Current implementation plan

[`plans/restore-reference-workflows-decoupling-plan.md`](plans/restore-reference-workflows-decoupling-plan.md)
defines the accepted v5.3 boundaries and phase gates. Its
[execution ledger](plans/evidence/harden-llm/reference-decoupling/progress.json)
distinguishes implementation and deployment; older ADRs and release reports
remain historical evidence tied to their original revisions.


## Structured output contracts

The stateless library owns the supported schema subset. Use `NormalizeSchema`
through the root package to validate a caller schema before execution. Nested
fields may use one supported type or `[type, "null"]` in either order; nullable
fields remain required and respect enum restrictions. Roots remain non-nullable
closed objects with every property required. Unsupported unions fail explicitly.

The OpenAI Responses and Chat Completions projections preserve original provider
assistant text alongside the library's parsed structured result, including key
order and whitespace. Known invalid output uses `STRUCTURED_SCHEMA` or
`STRUCTURED_PARSE`; refusal and incomplete completion retain their existing
codes. With `harden.diagnostics` enabled, a failed response includes original
`output_text` when available. It is evidence and is never repaired or scored as
success. Caller-schema rejection is HTTP 400 before dispatch; an invalid contract
generated inside the gateway is HTTP 500 `internal_contract_error`.

TEST-472 covers nullable types and the exact Model Recall consumer contracts
through the actual gateway and provider adapter with local TLS fixtures. Live
provider qualification and deployment remain separate release activities.
