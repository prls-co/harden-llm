# Harden LLM Web

This Phoenix LiveView application is the browser-facing operations console for
Harden LLM. It is an independent REST client of `../api/openapi.yaml`: it owns
HTML, CSRF, an encrypted session cookie, and an encrypted durable DETS token
vault. It does not own a database, provider integration, retry policy, cache
identity, pricing, Garage access, or domain persistence.

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
mix test --only browser test/browser/widget_canary_test.exs
mix deps.audit
mix hex.audit
MIX_ENV=prod mix assets.deploy
MIX_ENV=prod mix release
```

### Feedback tiers

During frontend edits, use the repository-root `make test-fast` loop or run
`mix test` directly for the T0-T2 boundary. T0 covers pure rules, T1 covers
in-process Go/Elixir behavior, and T2 covers the dependency-free client core.
LiveViewTest owns server-side
folding, profile/reasoning/cache/retry state, upload namespaces, parent
messages, and independent embedded instances. The extracted client decision
core is tested by Node's built-in runner; it deliberately does not emulate a
DOM.

T3 owns real service integration and race execution, T4 owns the two targeted
Chromium canaries and native hook/event/layout behavior, and T5 owns full
Compose or deployed/live certification. Use `make test-browser` and
`make test-release` only when those boundaries are relevant. A serial test
must name the global resource that prevents async execution. An expensive-tier
defect should gain a cheap T0-T2 regression for its root invariant whenever
possible; the expensive test remains for the boundary fact it uniquely proves.

Happy DOM and jsdom are intentionally not installed. Promotion requires a
concrete adapter defect that pure rules cannot express, an API comparison and
ADR-HLLM-015 amendment, and a retained real-browser canary. The deployed
canary is opt-in and reads credentials only from inherited named environment
entries; it never commits secrets or live output.

`mix test` runs WEB-TEST-001 through WEB-TEST-010 plus the source-derived
WEB-TEST-031 through WEB-TEST-043 parity extensions, and excludes browser and
Compose tags. The embedded `ProfileWidgetComponent` coverage includes the
compact no-tabs row, searchable custom-value controls, nested profile folds,
utility cache/retry projection, explicit profile-save gating, fallback/options
interactions, namespaced nested form IDs and bundle inputs, independent
multi-instance host routing, and canonical profile mutations. The
widget-specific plan cases TEST-101 through TEST-113 and TEST-115 are included
in the deterministic suite. The browser test requires Chromium and ChromeDriver
and is limited to the targeted native boundaries in TEST-114; it is not part of
the cheap edit-test loop.

The repository-root tier runner is the canonical scheduler. On a host without
Elixir/Mix, run the deterministic frontend check in the pinned browser
toolchain image (this still does not launch Chromium):

```bash
docker run --rm --network none \
  -v "$HOME/.mix:/root/.mix:ro" \
  -v "$PWD:/workspace" \
  -w /workspace/frontend \
  harden-llm-browser-test:local mix test --seed 104729
```

Do not add a DOM emulator to compensate for a missing Mix toolchain. The pure
combobox decision core remains Node-tested without a DOM; native focus,
LiveSocket patching, file inputs, CSS/layout, and browser cleanup remain
owned by TEST-114.
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
into a non-root Alpine image. The bearer-token vault is encrypted at rest on the
retained `harden-llm-web-sessions` volume, so replacing the single frontend
container preserves valid browser sessions. Removing that volume or rotating
the Phoenix secret requires users to sign in again. V1 supports one frontend
replica.

Deploy with `../deploy/frontend/compose.frontend.yml` layered over the backend
and pinned Langfuse files. The overlay supplies the private API/Collector
origins and the variables documented in
[`../docs/environment.md`](../docs/environment.md). Caddy remains the only
public-port owner, and the browser never talks directly to the Go API.

Operational setup, backup, and upgrade procedures are in the
[`self-hosting guide`](../docs/self-hosting.md).
