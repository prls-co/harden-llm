# Harden LLM Web

This Phoenix LiveView host uses the reusable `HardenLlmUI` package in
`packages/harden_llm_ui/`. The package owns the REST client, picker/editor,
input/response, statistics, history, trace components, CSS and JavaScript.
The host owns routes, async orchestration, CSRF and shared Portal admission.
OpenAPI in `../api/openapi.yaml` is the Go/Phoenix boundary.

## Shared login and scoped history

Only PRLS Portal hosts password forms. Enabled logins enter without company
selection or an HLLM grant. Logins share the host API key's history and profiles;
drafts additionally use `workspace:<stable Portal user ID>`. Phoenix holds no
database credentials and owns no history store. The Go stateful wrapper records
reference `/api/v1/run` calls in shared PostgreSQL. Direct `/v1` calls are not
recorded. Recording failures preserve the returned inference result and expose
the distinct failure or unconfirmed outcome.

Phoenix sends its server-held `HARDEN_LLM_TOKEN` to the gateway. Provider keys
and HLLM tokens never reach the browser. Full history records load only when
the existing details control is opened; summaries drive the initial page.

## Local development

Use Elixir 1.20.2 on Erlang/OTP 28.4.3. Configure the gateway separately on
`http://127.0.0.1:8080`, apply its `schema` command to an isolated HLLM database,
and provide the same `HARDEN_LLM_TOKEN` to Phoenix and the gateway. Then:

```bash
mix setup
mix phx.server
```

Visit `http://localhost:4000`. Configure shared Portal/Control Plane for login;
production requires HTTPS, independent session salts and the protected host
secret. See the [self-hosting guide](../docs/self-hosting.md).

## Verification

Use repository-root `make test-fast` for the normal edit loop. Host and package
checks are deterministic and require no PostgreSQL:

```bash
MIX_ENV=test mix format --check-formatted
mix test
cd packages/harden_llm_ui
mix test
```

Browser tests are opt-in and run only when specifically requested.
`make test-release` is browser-free; its composed HTTP check uses isolated real
storage and local provider/identity fixtures. Actual browser layout requires a
separate authorized check. See the
[testing guidelines](../docs/liveview-go-testing-guidelines.md).

## Deployment and reuse

The frontend overlay `deploy/frontend/compose.frontend.yml` runs one Phoenix
service alongside one Go gateway. Shared Caddy routes `/v1/*`, `/api/v1/*` and
health probes to Go and UI routes to Phoenix on the same hostname. Shared
PostgreSQL/Garage belong to their infrastructure owners.

Another Phoenix host can depend on `HardenLlmUI` and supply a server-side API
context, draft key, unique component IDs and host admission/lifecycle callbacks.
The package has no Ecto, host-module or Portal dependency. Its independent test
host exercises this boundary. See [architecture](../docs/architecture.md).
