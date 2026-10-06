# Harden LLM

Harden LLM is an OpenAI-compatible hardening proxy and a small Phoenix reference
application. The gateway exposes `/v1/models`, `/v1/chat/completions`, and
`/v1/responses`; its inference API is stateless and owns no login, profile,
history, or product database. The reference application uses shared PRLS login
for access, records its own calls in PostgreSQL, and shares that history across
all enabled logins.

The incoming API bearer is `HARDEN_LLM_TOKEN`, stored in the ignored root
`.env`. `CPA_API_KEY` is used only by the gateway for its configured upstream.
The checked-in connection example is [`config/upstreams.example.json`](config/upstreams.example.json).
See [the API contract](api/openapi.yaml), [architecture](docs/architecture.md),
and [self-hosting guide](docs/self-hosting.md).

## Repository map

- Root Go package: portable hardening client with one execution engine.
- `cmd/harden-llm-gateway/`: stateless OpenAI-compatible HTTP gateway.
- `internal/`: provider transports, recovery, cache, API codecs, telemetry, and tests.
- `api/openapi.yaml`: authoritative gateway contract and Go/Phoenix boundary.
- `frontend/`: independent Phoenix reference application and its history store.
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
password, hostnames, and existing Control Plane/Portal settings. Keep provider
credentials in environment variables; the connection JSON stores only an
environment-variable name. Follow [the self-hosting guide](docs/self-hosting.md)
for migrations, startup, and browser-free HTTP checks.

OpenAI-compatible clients use the API origin as their base URL with the same
`HARDEN_LLM_TOKEN` value. For example, an SDK should use
`https://harden-llm-api.prls.co/v1`; it does not need a Harden-specific request
wrapper for supported OpenAI fields. See [API and Go library examples](docs/api-and-library.md)
for request examples and the supported subset.

## Current implementation plan

[`plans/proxy-and-reference-app-simplification-plan.md`](plans/proxy-and-reference-app-simplification-plan.md)
tracks the coordinated profile-free proxy, Phoenix reference store, and
deployment cutover. Older ADRs and release reports remain historical records;
the current ownership and API contract are defined by OpenAPI and that plan.
