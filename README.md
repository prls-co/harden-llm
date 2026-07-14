# Harden LLM

Harden LLM is a provider-neutral Go library plus a thin, self-hosted REST
gateway and optional Phoenix LiveView operations console. It preserves the
certified `utility-llm` retry, schema, cache, provider, usage, and diagnostic
contracts while replacing Firebase persistence with dedicated Postgres and
Garage services.

## Repository map

- Root `*.go`: public `hardenllm` library and its single `Client.Call` path.
- `cmd/harden-llm-gateway/`: production process, healthcheck, and user bootstrap.
- `internal/`: providers, runtime, persistence, gateway, telemetry, and tests.
- `api/openapi.yaml`: authoritative OpenAPI 3.1 REST contract.
- `frontend/`: independent Phoenix/LiveView REST client; no database or provider SDK.
- `deploy/` and `docker-compose.yml`: pinned single-host deployment artifacts.
- `fixtures/parity/`: source-SHA-pinned deterministic compatibility fixtures.
- `docs/`: architecture, operations, API examples, traceability, and ADRs.

See [architecture and ownership](docs/architecture.md) for the runtime boundaries.

## Development and certification

Go 1.26.5, Node 22, Docker, and Compose are required.

```bash
make build                 # compile every Go package
make test-unit             # deterministic Go tests
make test-parity           # fixture integrity and compatibility contracts
make test-integration      # isolated Postgres and Garage integration tests
make test-compose          # real fifteen-service correlated smoke test
make verify                # format, vet, build, tests, race, and govulncheck
```

`make verify` intentionally excludes `frontend/` and live provider credentials.
The frontend has its own pinned Mix gates in [frontend/README.md](frontend/README.md).

## Self-hosted quick start

1. Copy `.env.example` to `.env`, replace every placeholder independently, and
   point the five hostnames at the Docker host.
2. Validate and start the full product:

```bash
docker compose \
  -f docker-compose.yml \
  -f deploy/langfuse/docker-compose.upstream.yml \
  -f deploy/langfuse/compose.private.yml \
  -f deploy/frontend/compose.frontend.yml \
  config --quiet
docker compose \
  -f docker-compose.yml \
  -f deploy/langfuse/docker-compose.upstream.yml \
  -f deploy/langfuse/compose.private.yml \
  -f deploy/frontend/compose.frontend.yml \
  up -d --build --wait --wait-timeout 300
```

3. Bootstrap the first local user with the password on standard input; never
   place it in shell arguments. Follow the exact command in the
   [self-hosting guide](docs/self-hosting.md#bootstrap-an-operator).

Only Caddy publishes host ports. The Go API, Phoenix app, data stores, and
telemetry services remain on the private Compose network. Review the
[environment reference](docs/environment.md) and back up every owned volume
before upgrades.

## Contracts and provenance

- [API and library examples](docs/api-and-library.md)
- [Requirements traceability](docs/requirements-traceability.md)
- [Release certification](docs/release-certification.md)
- [Langfuse upstream provenance](deploy/langfuse/UPSTREAM.md)
- [Architecture decisions](docs/adr/README.md)

Live provider calls are opt-in release evidence only. Deterministic acceptance
never requires paid credentials or network access to an LLM provider.
