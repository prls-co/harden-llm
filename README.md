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

## Current implementation plan

[`plans/restore-reference-workflows-decoupling-plan.md`](plans/restore-reference-workflows-decoupling-plan.md)
defines the accepted v5.3 boundaries and phase gates. Its
[execution ledger](plans/evidence/harden-llm/reference-decoupling/progress.json)
distinguishes implementation and deployment; older ADRs and release reports
remain historical evidence tied to their original revisions.
