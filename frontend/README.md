# Harden LLM Web

This Phoenix LiveView application is the browser-facing operations console for
Harden LLM. It is an independent REST client of `../api/openapi.yaml`: it owns
HTML, CSRF, an encrypted session cookie, and an ephemeral ETS token vault. It
does not own a database, provider integration, retry policy, cache identity,
pricing, Garage access, or domain persistence.

## Local development

Use Elixir 1.20.2 on Erlang/OTP 28.4.3. Start the Go gateway separately on
`http://127.0.0.1:8080`, then:

```bash
mix setup
mix phx.server
```

Visit `http://localhost:4000`. Development sessions are intentionally
non-production; production requires independent signing/encryption salts, a
64-byte secret key base, HTTPS, and the Compose topology.

## Verification

```bash
mix format --check-formatted
mix compile --warnings-as-errors
mix test
mix test --only browser test/browser/full_workflow_test.exs
mix deps.audit
mix hex.audit
MIX_ENV=prod mix assets.deploy
MIX_ENV=prod mix release
```

`mix test` runs WEB-TEST-001 through WEB-TEST-010 plus the source-derived
WEB-TEST-031 through WEB-TEST-043 parity extensions, and excludes browser and
Compose tags. The embedded `ProfileWidgetComponent` coverage includes the
compact no-tabs row, searchable custom-value controls, nested profile folds,
utility cache/retry projection, explicit profile-save gating, fallback/options
interactions, namespaced nested form IDs and bundle inputs, independent
multi-instance host routing, and canonical profile mutations. The
browser test requires Chromium and ChromeDriver.
The primary Run Prompt submitter deliberately uses `formnovalidate` because
optional nested profile editors share the outer form; LiveView remains the
server-side validation boundary. The gateway's TEST-012 regression accepts
utility request controls such as `max_tokens` while still rejecting
credential-shaped option names.
The authenticated `/embed/llm` fixture mounts two instances with distinct
`id_prefix` and upload namespaces; downstream hosts can copy that mounting
pattern without adopting tabs or page-level navigation.
WEB-TEST-012 is the release-only sixteen-service test and additionally requires
Go, Docker, and Compose:

```bash
mix test --only compose test/browser/compose_smoke_test.exs
```

`Dockerfile.browser` pins the certified browser, Go, Docker CLI, Compose,
Elixir, and OTP versions. When driving the host Docker socket from that image,
mount the repository at its identical absolute host path and use host
networking so Compose paths and published smoke ports remain valid.

## Production

`Dockerfile` builds assets and one OTP release, then copies only runtime files
into a non-root Alpine image. The service is stateless; restarting it revokes
all frontend sessions because bearer tokens live only in ETS. V1 supports one
frontend replica.

Deploy with `../deploy/frontend/compose.frontend.yml` layered over the backend
and pinned Langfuse files. The overlay supplies the private API/Collector
origins and the variables documented in
[`../docs/environment.md`](../docs/environment.md). Caddy remains the only
public-port owner, and the browser never talks directly to the Go API.

Operational setup, backup, and upgrade procedures are in the
[`self-hosting guide`](../docs/self-hosting.md).
