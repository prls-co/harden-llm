# Harden-LLM Phoenix LiveView Frontend Specification

## 1. Title and metadata

- Project name: `harden-llm-web`
- OTP application: `:harden_llm`
- Target repository: `/home/kirill/harden-llm`
- Target application directory: `/home/kirill/harden-llm/frontend`
- Backend contract: `/home/kirill/harden-llm/api/openapi.yaml`
- Source UX reference: `/home/kirill/utility-llm/examples/react-trace-studio`
- Version: `1.0.3-multi-instance-embedding-amendment`
- Owners: frontend and self-hosted runtime maintainers
- Date: 2026-08-23
- Document ID: `SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001`
- Summary: This specification defines the separate Elixir/Phoenix LiveView frontend for Harden-LLM. Phoenix renders HTML, owns the browser session and CSRF boundary, and calls the Go gateway server to server through its published REST/OpenAPI contract. The frontend owns no application database, provider integration, retry policy, object storage, pricing, schema validation, cache identity, or domain persistence. The 2026-08-18 parity amendment incorporates the source-derived controls and explicit self-hosted adaptations recorded in `docs/utility-llm-frontend-parity-inventory.md` and ADR-HLLM-012; the 2026-08-22 embedding amendment makes the Workspace and Profiles visual surfaces single-column, stable-root components that can sit inside a host shell; the 2026-08-23 multi-instance amendment makes `id_prefix` a complete DOM, parent-message, and upload namespace contract.

## 2. Canonical stack

| Layer | Canonical choice | Responsibility |
| --- | --- | --- |
| Language/runtime | Elixir 1.20.2 on Erlang/OTP 28.4 | Supervised web runtime and server-side concurrency. |
| Web framework | Phoenix 1.8.9 | Endpoint, router, controllers, secure browser sessions, CSRF, assets, and releases. |
| Interactive UI | Phoenix LiveView 1.2.9 | Authenticated workspace, profile editing, runs, history, traces, and live state updates. |
| HTTP client | Req 0.6.1 | The only Phoenix-to-Go REST client. |
| API contract | Backend OpenAPI 3.1 document at `api/openapi.yaml` | Canonical operations, bearer security, request/response schemas, errors, and examples. |
| Components | Phoenix function components and generated core components | Shared forms, compact profile cards, in-flow folds, focused trace views, status displays, and icons. |
| Assets | Phoenix-generated esbuild and Tailwind wrappers | Compile static assets; no Node.js runtime service is deployed. |
| Browser auth | Encrypted and signed Phoenix cookie plus supervised ETS token vault | The cookie carries a random frontend session handle; only the server-memory vault holds the Go bearer token. |
| Frontend persistence | None | The token vault is ephemeral. Durable user, profile, state, run, trace, and artifact data remain in the Go REST service. |
| Tracing | OpenTelemetry Erlang plus Phoenix instrumentation | Export server-side HTTP and REST-client spans to the existing Collector. |
| Metrics | PromEx 1.12.0 | Expose bounded Phoenix, LiveView, BEAM, and REST-client metrics for the existing Prometheus service to scrape privately. |
| Logs | `LoggerJSON` 7.0.4, `opentelemetry_logger_metadata` 0.2.0, and OTP `:logger_std_h` | Emit redacted correlated JSON to stdout and a bounded named volume that the existing Collector tails into Loki, without relying on the unstable OTel Erlang logs API. |
| Browser tests | Phoenix.ConnTest, Phoenix.LiveViewTest, and Wallaby | Deterministic controller/LiveView tests plus one real-browser workflow. |

Version pins are initial implementation pins and must be recorded in `mix.lock`. Upgrades require the normal frontend test suite, not a backend compatibility layer.

Runtime feature dependencies are limited to the Phoenix-generated HTTP/asset packages plus `req`, `opentelemetry`, `opentelemetry_api`, `opentelemetry_exporter`, Phoenix OTel instrumentation, `opentelemetry_logger_metadata`, `prom_ex`, and `logger_json`. Test-only additions are Wallaby and `mix_audit`. Compatible OTel instrumentation packages are resolved once and pinned in `mix.lock`; no Ecto adapter, provider SDK, Firebase package, Langfuse client, Garage client, or general workflow framework is allowed.

## 3. Decisions

| Topic | Verdict | Rationale |
| --- | --- | --- |
| Use Phoenix LiveView | DECISION | The product is an authenticated operational console with forms, tables, progress states, and server-owned API credentials. LiveView provides interactive server-rendered UI without recreating a separate browser state/API layer. |
| Keep frontend and backend separate | DECISION | Phoenix consumes only REST/OpenAPI. It never imports Go internals or accesses the backend database, Garage, providers, or Langfuse directly. |
| Keep the frontend in the same repository | DECISION | `frontend/` keeps contract changes and product releases reviewable together while preserving independent Go and Mix build boundaries. |
| Do not use Ecto | DECISION | Postgres belongs to the Go backend. A Phoenix database would create a second persistence and migration path without a v1 requirement. |
| Use one Req client module | DECISION | All REST paths, bearer injection, envelope decoding, timeout behavior, trace propagation, and redaction pass through `HardenLlmWeb.HardenAPI`. LiveViews and controllers do not call Req directly. |
| Keep the backend token out of client-carried session data | DECISION | A supervised ETS vault stores the Go bearer token under a random frontend session handle. The encrypted cookie and LiveView session carry only that handle, so the reusable Go token never enters rendered LiveView session data. |
| Keep browser-to-Go traffic disabled | DECISION | The browser talks only to Phoenix over same-origin HTTP/WebSocket. The Go API needs no CORS or browser CSRF behavior. |
| Do not generate an API client in v1 | DECISION | One small hand-written client boundary is simpler than committing generated code. Contract tests compare its operation registry to OpenAPI so route drift still fails. |
| Do not retry REST calls automatically | DECISION | A hidden retry could duplicate an ambiguous synchronous `/api/v1/run`. Provider retries remain owned by the Go library. Users may explicitly submit a new run after an ambiguous result. |
| Keep frontend observability operational | DECISION | Frontend traces go to Tempo through the existing Collector. The Collector's Langfuse export filter continues to select Go gateway traces only. |

## 4. Scope

### In scope

- Local email/password login and logout through the backend auth endpoints.
- Session validation during HTTP mount, connected LiveView mount, and reconnect.
- Client-state and prompt-draft load/save through `/api/v1/state`.
- Profile create/edit/delete, model refresh, credential replacement, and backup-profile selection.
- Profile bundle import/export without inspecting or exposing opaque encrypted credential material.
- Prompt, optional system prompt, structured-output schema, repair settings, profile/model selection, and synchronous run submission.
- Result output, normalized usage/cost, attempts, cache facts, run ID, and trace ID display.
- Paginated history, restore-to-workspace, single deletion, and clear-all with confirmation.
- Domain trace and observation display plus authorized artifact download.
- Correlated frontend traces and redacted structured logs.
- An optional Compose overlay and Caddy route for self-hosted deployment.
- Deterministic controller, LiveView, REST-client, security, observability, and real-browser tests.

### Out of scope

- Provider SDKs, provider payload construction, retries, repair, backup-profile execution, schema validation, pricing, usage calculation, cache keys, or redaction rules.
- Direct Postgres, Garage, Firebase, Langfuse, Tempo, Loki, Prometheus, or Grafana data access from product UI code.
- Ecto, SQLite, Redis, Oban, Temporal, Broadway, or another durable frontend state store.
- Browser calls to the Go API, a JavaScript SPA state layer, React compatibility, or copied React components.
- Public registration, password reset email, OIDC, SAML, RBAC administration, or multi-tenant organization management.
- Background run queues, automatic replay of ambiguous runs, streaming provider output, or offline mode.
- Embedding Grafana or Langfuse with shared authentication.

## 5. Ownership boundary

```text
browser
  |-- HTTPS + LiveView WebSocket
  v
Phoenix LiveView
  |-- encrypted browser session handle
  |-- ephemeral server-side token vault
  |-- CSRF and form validation for presentation
  |-- Authorization: Bearer <opaque-token>
  |-- traceparent propagation
  v
Go REST API /api/v1
  |-- auth and owner authorization
  |-- application Postgres
  |-- Garage artifacts
  |-- provider calls and retries
  `-- domain validation and persistence
```

Boundary rules:

- Phoenix is a REST client, not a second backend implementation.
- `api/openapi.yaml` is the only cross-runtime contract. No Go struct, Elixir struct, database table, or source fixture is imported across the boundary.
- The frontend may perform presentation validation such as required fields, local JSON syntax feedback, and file-size checks. The Go API remains authoritative and every backend `fieldErrors` entry is rendered next to the matching input.
- LiveViews and controllers use `HardenLlmWeb.HardenAPI`; direct `Req` calls elsewhere fail the static boundary test.
- Phoenix does not read backend environment secrets other than its own API base URL. Provider, Postgres, Garage, Langfuse, and Grafana credentials are not present in the frontend container.
- The source React app informs workflow fixtures and acceptance behavior only. No JSX, Firebase client, React state, or copied JavaScript business logic enters `frontend/`.

## 6. Target structure

```text
/home/kirill/harden-llm/
├── api/
│   └── openapi.yaml
├── frontend/
│   ├── mix.exs
│   ├── mix.lock
│   ├── config/
│   ├── lib/
│   │   ├── harden_llm.ex
│   │   ├── harden_llm/
│   │   │   └── application.ex
│   │   ├── harden_llm_web.ex
│   │   └── harden_llm_web/
│   │       ├── endpoint.ex
│   │       ├── router.ex
│   │       ├── harden_api.ex
│   │       ├── api_error.ex
│   │       ├── observability.ex
│   │       ├── prom_ex.ex
│   │       ├── auth.ex
│   │       ├── session_vault.ex
│   │       ├── controllers/
│   │       ├── live/
│   │       └── components/
│   ├── assets/
│   ├── priv/static/
│   ├── test/
│   │   ├── support/
│   │   ├── harden_llm_web/
│   │   └── browser/
│   ├── Dockerfile
│   └── README.md
└── deploy/frontend/
    ├── compose.frontend.yml
    ├── Caddyfile.frontend
    ├── otel.frontend.yaml
    └── grafana-dashboard.json
```

Create the application with:

```bash
mix phx.new frontend --module HardenLlm --app harden_llm --no-ecto --no-mailer
```

LiveView is included by the Phoenix 1.8 generator unless `--no-live` is supplied. Do not use a non-existent `--live` flag. Remove generated demo pages and retain one implementation home for each workflow.

## 7. REST client contract

`HardenLlmWeb.HardenAPI` is the only REST access boundary.

It must:

- Construct requests from a validated `HARDEN_LLM_API_BASE_URL` with no user-controlled host or scheme.
- Expose one function per OpenAPI operation used by the frontend.
- Set `Accept: application/json` and JSON request content types where required.
- Add exactly one bearer header for authenticated calls and never add it to login.
- Accept the random frontend session handle for authenticated operations and resolve its backend token through `SessionVault` internally; controllers and LiveViews never receive the token.
- Inject W3C `traceparent` and `tracestate` from the active Phoenix span.
- Disable automatic HTTP retries and redirect following.
- Apply separate bounded timeouts for normal API calls and synchronous runs. The default run client timeout is 65,000 ms: five seconds above the backend's 60-second cap. Startup rejects a run client timeout that is not greater than the configured backend cap.
- Require the documented `{state, result, error}` envelope for every non-health response.
- Return `{:ok, result, state}` or `{:error, %HardenLlmWeb.APIError{}}`; it must not leak Req response structs into LiveViews.
- Normalize transport failures, malformed JSON, unexpected content types, undocumented statuses, and malformed envelopes into redacted stable frontend errors.
- Never log request bodies, response bodies, bearer tokens, imported bundles, artifact presigned URLs, or provider credentials.
- Record the stable OpenAPI operation ID, method, route template, status class, duration, and backend trace ID as safe telemetry fields.

Contract synchronization:

- Maintain a small operation registry in `HardenAPI` containing operation ID, method, and path template.
- A test parses `../api/openapi.yaml` and proves every registry entry exists with the same method/path and security requirement.
- The same test proves every backend operation intended for the frontend has one client function or is explicitly allowlisted as backend-only (`healthz` and `readyz`).
- Request and success/error fixtures are copied from OpenAPI examples into test assertions at test time; they are not maintained as a second hand-authored schema catalog.

## 8. Authentication and session contract

### Login

1. The unauthenticated Phoenix controller renders an email/password form with Phoenix CSRF protection.
2. On submit, the controller calls `POST /api/v1/auth/login` server to server.
3. On success, Phoenix renews the Plug session, generates a random frontend session handle, stores the backend token and expiry in the supervised ETS vault under a SHA-256 digest of that handle, stores only the handle and expiry in the encrypted cookie session, drops the token from local variables, and redirects to the authenticated LiveView.
4. Passwords and access tokens are never included in flash messages, logs, telemetry, rendered assigns, URLs, or client-side storage.

### Session cookie

- Cookie name: `__Host-harden_llm_web` in production.
- Flags: `Secure`, `HttpOnly`, `SameSite=Lax`, path `/`, and no `Domain` attribute.
- The cookie is signed and encrypted with independent salts and `HARDEN_LLM_WEB_SECRET_KEY_BASE`.
- The session contains only the random frontend session handle, expiry, and minimal non-sensitive display identity. It contains no backend token, prompt, profile, provider credential, bundle, or API response body.
- Session renewal occurs after login. Session clearing occurs after logout, backend `401`, malformed session data, or expiry.
- The LiveView session payload and socket state may carry the random handle but never the backend bearer token. `HardenAPI` resolves the token only while constructing an authenticated request; it must never be returned to the LiveView, rendered, placed in DOM data attributes, sent in hook payloads, or inspected by logs.

### Token vault

- `HardenLlmWeb.SessionVault` owns one private ETS table under the application supervisor; all access is serialized through its narrow API.
- Vault keys are SHA-256 digests of 256-bit random frontend session handles. Values contain only the backend token and absolute expiry.
- Lookup, insert, revoke, and expired-entry cleanup are the only operations. The table is never dumped, logged, exposed through observability, or persisted.
- Login revokes any prior handle before rotating to a new one. Logout removes the vault entry after attempting backend revocation.
- A missing vault entry is treated as an expired local session and clears the cookie.
- A Phoenix restart intentionally invalidates all frontend sessions. V1 runs one Phoenix instance and does not add Redis or distributed session replication. Multi-instance frontend deployment requires a later ADR and shared secret-vault design.

### LiveView authorization

- Authenticated routes use one `on_mount` hook.
- The hook validates the session shape, resolves its handle through the token vault, and calls `GET /api/v1/auth/session` on initial connected mount.
- An invalid or expired backend session clears the browser session through a controller redirect and returns the user to login with a generic message.
- WebSocket reconnect repeats backend session validation without issuing another login token.
- Logout calls `POST /api/v1/auth/logout`; Phoenix clears its session even if the backend is unavailable so the browser cannot continue using stale local state.

## 9. LiveView workflows

### Workspace

- The first authenticated screen is the actual run workspace, not a landing page.
- The workspace loads client state, profiles, and the first history page through REST.
- Inputs include profile, model, prompt, optional system prompt, optional JSON Schema, repair mode, and supported call options defined by OpenAPI.
- Draft state saves through a debounced server-side LiveView event to `/api/v1/state`; only accepted backend state replaces the local persisted version.
- Field-local browser `phx-change` events merge into the current draft before
  persistence, so changing Reasoning or Cache cannot erase the selected profile
  or other form fields.
- JSON syntax feedback is local presentation validation. Backend schema constraints and run errors remain authoritative.
- Run submission uses `start_async/3` so the LiveView process remains responsive.
- The Run command is disabled only while that submission is active. Duplicate browser events for the same active submit are ignored locally.
- Phoenix never retries a run. An ambiguous transport failure says the outcome is unknown and directs the user to refresh history before choosing whether to run again.
- Results show output, usage, cost, attempts, cache status, run ID, trace ID, and normalized errors without raw provider envelopes.

### Profiles

- List profiles with provider family, API interface, endpoint host, model, pricing status, and backup references.
- Create and edit through one shared component and one REST mutation path.
- Credential fields are write-only. Existing secrets render as configured/not configured and are never repopulated.
- Saving displays backend field errors and probe failures without persisting an invalid local shadow profile.
- Model refresh is an explicit command and does not run on every render or edit.
- Delete requires an inline confirmation fold and handles backend dependency errors, such as a profile still referenced as a backup.
- Bundle export is a Phoenix controller download that streams the backend payload without logging or persisting it.
- Bundle import validates file size/content type, sends the bytes once to the backend, and replaces UI state only after the atomic backend response succeeds.

### History and traces

- History uses stable cursor pagination from the REST contract and LiveView streams for rows.
- Restoring a history item updates the workspace through backend-returned safe fields; it does not reconstruct hidden provider or credential state.
- Single delete updates the stream only after backend success.
- Clear-all requires explicit confirmation and reloads the canonical empty page after success.
- Opening a trace fetches `/api/v1/traces/{traceID}` and renders normalized observations in sequence.
- Prompt/response content is shown only when the backend includes its redacted projection. The frontend never requests a storage object directly.

### Artifacts

- The browser requests a same-origin Phoenix artifact controller route.
- The controller calls the authenticated backend artifact endpoint with redirects disabled.
- It accepts only the expected redirect status and a `Location` whose parsed scheme, host, and port exactly match `HARDEN_LLM_ARTIFACT_PUBLIC_ORIGIN`.
- Phoenix returns a `303` redirect to that short-lived URL with `Cache-Control: no-store` and a strict referrer policy.
- Presigned URLs, signatures, query strings, and object keys are never logged, traced, stored in session, or rendered before the redirect.

## 10. UI contract

- Use a quiet operational layout with a compact host-neutral top bar. Do not
  render persistent primary navigation, tabs, or a side rail inside the
  reusable surface; the host application owns route selection.
- Use a narrow vertical widget stack for Workspace and Profiles. Profile cards keep endpoint, credential, model, and capability facts visible without a horizontal table or a hidden side rail.
- Use in-flow disclosure folds for profile editing, credentials, options, retry/repair, pricing, advanced input, output details, and history. Opening `New profile` expands `#profile-editor` while the profile list remains in the same document flow.
- Treat `#workspace-page` and `#profiles-page` as reusable visual surfaces with
  stable `studio-page`, `studio-stack`, `studio-card`, and `studio-fold` roots.
  They must not require tabs, a side rail, a fixed overlay, or page-level
  navigation state from the host application. `ProfileWidgetComponent` is the
  current reusable in-flow profile widget at `#workspace-llm-widget`; pass an
  `id_prefix` when mounting multiple instances. The prefix namespaces every
  generated form/control ID, parent message, and main/escalation upload name;
  the host must register the corresponding upload channels and route tagged
  messages. `Layouts.app` remains the current route adapter and host messages
  keep selection/UI state explicit. `/embed/llm` is the checked-in two-instance
  host fixture for this contract.
- Use focused views only where the content is genuinely a separate trace inspection; profile editing and destructive profile confirmation must not use viewport overlays that hide the surrounding workspace.
- Use the generated Phoenix icon component for structural icons and compact emoji labels for high-frequency controls (`🤖`, `🧠`, `💾`, `⚙`, `🔁`, `💰`), with accessible text or labels retained.
- Every form control has a visible label, error association, keyboard focus state, and disabled/submitting state.
- Status must not rely on color alone. Loading, empty, success, ambiguous, unauthorized, and backend-unavailable states are distinct.
- Long IDs and model names wrap or truncate with an accessible full-value affordance; they cannot resize table controls or overlap neighboring content.
- Desktop and mobile layouts preserve all commands without horizontal page overflow. Profile cards and folds reflow in place rather than relying on an overlay or an independently scrolling table.
- Do not embed Grafana or Langfuse. Provide configured external diagnostic links only when a safe URL is supplied by deployment configuration.

## 11. Failure and concurrency behavior

- Each async operation has its own state; model refresh does not block history navigation, and trace loading does not block the workspace.
- Stale async responses are ignored when their request reference no longer matches the current selection.
- A backend `401` terminates the local authenticated session. A `403` remains an authorization error and does not silently log in again.
- `409` and `422` responses preserve backend field errors and conflict guidance.
- A run `422 credential_required` response is a known, non-ambiguous validation state: show the safe endpoint-credential guidance and do not show the refresh-history warning.
- `429` and `503` show a retry-later state but are not automatically retried.
- Unexpected response shapes are diagnostic errors, not treated as empty successful data.
- LiveView process crashes may restart the UI, but no client-side replay resubmits the last mutation.
- Flash messages contain safe summaries only. Diagnostic details are correlated by request/trace ID.

## 12. Security contract

- Production traffic reaches Phoenix only through Caddy HTTPS.
- Phoenix checks the configured public host, trusted proxy boundary, origin, and WebSocket origin.
- State-changing browser forms and controller actions use Phoenix CSRF protection.
- The Content Security Policy permits only same-origin scripts/styles/connect/WebSocket plus explicitly configured diagnostic navigation targets; no inline remote scripts or third-party analytics are required.
- Response headers include HSTS at Caddy, `X-Content-Type-Options: nosniff`, restrictive `Referrer-Policy`, and frame denial.
- Backend access tokens, passwords, provider credentials, bundle contents, artifact URLs, prompt/response bodies, and raw errors are classified as sensitive.
- Logger and telemetry metadata pass through a frontend redaction allowlist. Arbitrary maps and form params are never attached to logs or spans.
- Phoenix never accepts an API base URL, diagnostic URL, or artifact origin from a browser parameter.
- File uploads have bounded byte limits and are not written to a durable frontend volume.
- Production secrets are mounted or supplied at runtime and are absent from images, source, crash dumps, and test fixtures.

## 13. Observability contract

- Set `service.name=harden-llm-web`, deployment environment, release, and instance ID as OTel resource attributes.
- Instrument Phoenix endpoint/router/LiveView lifecycle and one child span per backend operation.
- Capture the active OTel context before `start_async/3` and attach it inside the task so asynchronous REST spans remain children of the initiating LiveView trace.
- Propagate active W3C trace context to the Go gateway so one Tempo trace crosses Phoenix, gateway, Postgres/Garage, and provider spans.
- Record only bounded attributes: route template, LiveView module, API operation ID, HTTP status class, outcome, and error category.
- User ID, session token, prompt, response, profile ID, model ID, raw URL, form params, and backend error text are not metric labels or span attributes.
- Add trace/span IDs to structured `Logger` metadata using the pinned logger-metadata bridge.
- Format production logs as JSON with `LoggerJSON` and an explicit metadata allowlist. The default handler writes stdout for `docker compose logs`; a second built-in OTP `:logger_std_h` file handler writes the same JSON records to `/var/log/harden-llm-web/app.jsonl` with bounded rotation.
- The frontend Compose overlay mounts that path on a private named volume and mounts it read-only into the existing Collector. `otel.frontend.yaml` adds one `filelog` receiver with a JSON parser, batches records, and exports them through the existing Loki exporter. It does not add another log agent or service.
- File-handler or Collector failure is reported safely to stderr and cannot fail login, navigation, API calls, or LiveView rendering. Rotation bounds retained local bytes.
- PromEx exposes a private `/metrics` endpoint for Phoenix request/latency/error, LiveView mount/event/exception, BEAM memory/run-queue/process, token-vault count, and REST-client operation/latency/outcome series. Prometheus labels are limited to bounded route, LiveView module, operation ID, status class, and outcome values.
- `otel.frontend.yaml` adds a private Collector Prometheus receiver for that endpoint and a separate `metrics/frontend` pipeline using the base processors/exporter that the existing Prometheus service scrapes. The overlay adds a Grafana dashboard band for the frontend. Caddy never routes `/metrics` publicly.
- Frontend traces export to the existing Collector and Tempo. The Collector must not export `service.name=harden-llm-web` traces to Langfuse.
- Telemetry export failure cannot fail login, navigation, API calls, or LiveView rendering.

## 14. Deployment contract

The backend remains independently runnable with its canonical fifteen services. The optional frontend overlay adds one service:

```text
internet
  |
  v
caddy
  |-- web host -> harden-llm-web:4000
  `-- api host -> harden-llm-gateway:8080

harden-llm-web
  |-- private REST -> harden-llm-gateway:8080
  |-- OTLP traces -> otel-collector:4317
  |-- JSON log volume -> otel-collector filelog -> Loki
  `-- private /metrics -> otel-collector -> Prometheus
```

Rules:

- `deploy/frontend/compose.frontend.yml` extends the base Compose project and adds `harden-llm-web`; it does not copy or modify the pinned Langfuse fragment.
- The overlay mounts `Caddyfile.frontend` into the base Caddy `conf.d` extension directory. It does not replace or duplicate the base Caddyfile.
- The overlay passes the base Collector file and `otel.frontend.yaml` as two supported `--config=file:...` inputs. The frontend file adds uniquely named `filelog/harden_llm_web` and `prometheus/harden_llm_web` receivers plus separate `logs/frontend` and `metrics/frontend` pipelines that reference base processors/exporters; it does not replace lists, copy base configuration, or enable experimental merge flags.
- The overlay mounts one read-only frontend log volume into the existing Collector. The full topology remains sixteen services.
- The overlay extends the Collector with one private PromEx scrape target; it does not change the base Prometheus service or expose the frontend metrics endpoint through Caddy.
- The release image is a multi-stage Elixir build containing one OTP release and compiled assets. It contains no Hex/Rebar caches, source secrets, Node runtime, or Go toolchain.
- Caddy remains the only service with public host ports.
- Phoenix exposes `/healthz` for process health. Readiness verifies endpoint startup and static configuration only; backend readiness remains the Go `/readyz` contract.
- The frontend starts when the backend is temporarily unavailable and renders a bounded unavailable state rather than crash-looping.
- V1 deploys exactly one Phoenix replica. Restarting it clears the ephemeral token vault and requires users to log in again.
- The frontend hostname is separate from the API hostname. Browser requests never need API CORS.

Required variables:

- `HARDEN_LLM_WEB_HOST`
- `HARDEN_LLM_WEB_PORT`
- `HARDEN_LLM_WEB_SECRET_KEY_BASE`
- `HARDEN_LLM_WEB_SESSION_SIGNING_SALT`
- `HARDEN_LLM_WEB_SESSION_ENCRYPTION_SALT`
- `HARDEN_LLM_API_BASE_URL`
- `HARDEN_LLM_MAX_RUN_DURATION_MS`
- `HARDEN_LLM_WEB_API_TIMEOUT_MS`
- `HARDEN_LLM_WEB_RUN_TIMEOUT_MS`
- `HARDEN_LLM_ARTIFACT_PUBLIC_ORIGIN`
- `HARDEN_LLM_WEB_OTEL_EXPORTER_OTLP_ENDPOINT`
- `HARDEN_LLM_WEB_SERVICE_NAME`
- `HARDEN_LLM_WEB_ENVIRONMENT`
- `HARDEN_LLM_WEB_RELEASE`

The default normal API timeout is 15,000 ms and the default run timeout is 65,000 ms. Compose supplies the same `HARDEN_LLM_MAX_RUN_DURATION_MS=60000` value to both services; Phoenix validates that its run timeout is greater without exposing either value to the browser. Timeout increases require timing RCA rather than arbitrary padding.

## 15. Test specification

All tests are free, self-hosted, deterministic, and isolated from live LLM providers unless the final Compose browser smoke is explicitly configured with a local deterministic provider fixture.

| ID | Test | Target | Command | Pass criteria | Budget |
| --- | --- | --- | --- | --- | --- |
| WEB-TEST-001 | Project boundary | `test/harden_llm_web/boundary_test.exs` | `mix test test/harden_llm_web/boundary_test.exs` | No Ecto/database/provider/Garage/Firebase/React dependency; only `HardenAPI` uses Req; runtime pins match the spec. | 10s |
| WEB-TEST-002 | OpenAPI/client parity | `test/harden_llm_web/harden_api_contract_test.exs` | `mix test test/harden_llm_web/harden_api_contract_test.exs` | Operation IDs, methods, paths, bearer requirements, examples, and backend-only allowlist match `api/openapi.yaml`. | 10s |
| WEB-TEST-003 | REST client behavior | `test/harden_llm_web/harden_api_test.exs` | `mix test test/harden_llm_web/harden_api_test.exs` | Req.Test proves headers, trace propagation, no redirects/retries, timeouts, envelopes, safe `credential_required` classification, malformed responses, and redaction. | 15s |
| WEB-TEST-004 | Browser auth/session and token vault | `test/harden_llm_web/controllers/session_controller_test.exs`, `test/harden_llm_web/session_vault_test.exs` | `mix test test/harden_llm_web/controllers/session_controller_test.exs test/harden_llm_web/session_vault_test.exs` | Login rotates the handle; only its digest indexes ETS; cookie flags pass; token is absent from cookie/HTML/LiveView session; expiry/restart/logout clear access; CSRF rejects invalid submits. | 15s |
| WEB-TEST-005 | LiveView authorization | `test/harden_llm_web/live/auth_test.exs` | `mix test test/harden_llm_web/live/auth_test.exs` | Initial mount/reconnect validates backend session; 401 clears session; unauthenticated routes redirect; token never appears in rendered HTML/diffs. | 15s |
| WEB-TEST-006 | Profile workflows | `test/harden_llm_web/live/profiles_live_test.exs` | `mix test test/harden_llm_web/live/profiles_live_test.exs` | Create/edit/delete, write-only credentials, field errors, model refresh, backup references, and bundle import/export use only expected REST operations. | 20s |
| WEB-TEST-007 | Workspace/run workflows | `test/harden_llm_web/live/workspace_live_test.exs` | `mix test test/harden_llm_web/live/workspace_live_test.exs` | State hydration/save, validation, one run submit, async state, trace-addressed result restoration, distinct new/prompt/system reset actions, non-ambiguous missing-credential guidance, ambiguous failure, no automatic retry, and stale-response rejection pass. | 25s |
| WEB-TEST-008 | History/trace/artifacts | `test/harden_llm_web/live/history_trace_test.exs`, `test/harden_llm_web/controllers/artifact_controller_test.exs` | `mix test test/harden_llm_web/live/history_trace_test.exs test/harden_llm_web/controllers/artifact_controller_test.exs` | Pagination/stream updates, restore/delete/clear, trace rendering, artifact authorization, exact-origin redirect validation, and no-store headers pass. | 20s |
| WEB-TEST-009 | Security and diagnostics | `test/harden_llm_web/security_observability_test.exs` | `mix test test/harden_llm_web/security_observability_test.exs` | CSRF/CSP/origin rules, secret scans, async trace propagation, safe attributes, bounded PromEx series, private scrape config, JSON Logger correlation/rotation, merged Collector validation with separate frontend pipelines, failure isolation, and no Langfuse frontend export pass. | 20s |
| WEB-TEST-010 | Responsive component rendering | `test/harden_llm_web/live/rendering_test.exs` | `mix test test/harden_llm_web/live/rendering_test.exs` | Every loading/empty/success/error state renders valid landmarks, labels, focus targets, stable action controls, compact profile cards, bounded long values, and the primary Run Prompt submitter's `formnovalidate` boundary for optional nested profile fields. | 15s |
| WEB-TEST-011 | Real-browser workflow | `test/browser/full_workflow_test.exs` | `mix test --only browser test/browser/full_workflow_test.exs` | Headless Chromium completes login, profile save, model refresh, run, history restore, trace view, artifact redirect, logout, and reconnect at desktop/mobile sizes. | 120s |
| WEB-TEST-012 | Frontend Compose smoke | `test/browser/compose_smoke_test.exs` | `mix test --only compose test/browser/compose_smoke_test.exs` | Caddy HTTPS, LiveView WebSocket, private REST routing, backend-unavailable recovery, cross-service Tempo trace, correlated Loki log, Prometheus series, Grafana query, and secret absence pass in the 16-service topology. | 180s |

### Source-derived frontend parity extension

The following tests were added after the original WEB-TEST-001 through
WEB-TEST-012 baseline. They are the executable cases for the current
`utility-llm` frontend inventory at source revision `5c0309e` and use the same
Phoenix/Go/OpenAPI boundary. Their intentional cursor-pagination and
single-editor adaptations are recorded in ADR-HLLM-012.

| ID | Test | Target | Command | Pass criteria |
| --- | --- | --- | --- | --- |
| WEB-TEST-031 | Workspace schema/fold/retry parity | `test/harden_llm_web/live/workspace_live_test.exs` | `mix test test/harden_llm_web/live/workspace_live_test.exs` | Shorthand schema generation, persisted folds, retry/repair controls, custom typed profiles, lazy history, restore, and deletion use the canonical state/run/history operations. |
| WEB-TEST-032 | Full profile-editor parity | `test/harden_llm_web/live/profiles_live_test.exs` | `mix test test/harden_llm_web/live/profiles_live_test.exs` | Provider/interface/endpoint, options, ordered fallbacks, retry/repair/escalation, pricing, deep-link editing, and save payloads remain functional. |
| WEB-TEST-033 | History trace/resource parity | `test/harden_llm_web/live/history_trace_test.exs` | `mix test test/harden_llm_web/live/history_trace_test.exs` | Expanded redacted records expose request/result stats, copy controls, trace links, and artifact resources without credentials. |
| WEB-TEST-034 | Workspace control rendering parity | `test/harden_llm_web/live/workspace_live_test.exs` | `mix test test/harden_llm_web/live/workspace_live_test.exs` | Prompt shortcut, schema debounce, output request/response folds, trace summary, copy actions, and unavailable states render safely. |
| WEB-TEST-035 | Profile combobox/credential parity | `test/harden_llm_web/live/profiles_live_test.exs` | `mix test test/harden_llm_web/live/profiles_live_test.exs` | Searchable endpoint/model suggestions and write-only staged-key behavior remain local and never render stored or staged secrets after close. |
| WEB-TEST-036 | Cursor pagination parity | `test/harden_llm_web/live/history_trace_test.exs` | `mix test test/harden_llm_web/live/history_trace_test.exs` | Page-size changes restart the cursor query from page one; arbitrary offset/page-number quick-jump is not added to the cursor-only REST contract. |
| WEB-TEST-037 | Inline studio control coverage | `test/harden_llm_web/live/profiles_live_test.exs`, `test/harden_llm_web/live/workspace_live_test.exs`, `test/browser/full_workflow_test.exs` | `mix test test/harden_llm_web/live/profiles_live_test.exs test/harden_llm_web/live/workspace_live_test.exs && mix test --only browser test/browser/full_workflow_test.exs` | Every profile/workspace button and input has a stable rendered control, each fold opens/closes in flow, field-local select events preserve the draft, and desktop/mobile browser workflows complete without tabs, an overlay, or horizontal overflow. |
| WEB-TEST-038 | Embedded utility-style profile widget topology | `test/harden_llm_web/live/workspace_live_test.exs`, `test/browser/full_workflow_test.exs` | `mix test test/harden_llm_web/live/workspace_live_test.exs && mix test --only browser test/browser/full_workflow_test.exs` | The no-tabs compact row exposes every main and nested profile fold/action; fallback selection and option-to-JSON synchronization remain functional through the embedded component. |
| WEB-TEST-039 | Embedded profile mutation delegation | `test/harden_llm_web/live/workspace_live_test.exs` | `mix test test/harden_llm_web/live/workspace_live_test.exs` | Staged credentials stay write-only while save, model refresh, delete confirmation, and bundle import delegate to the canonical profile REST operations. |
| WEB-TEST-040 | Profile-aware reasoning capability guard | `test/harden_llm_web/live/workspace_live_test.exs`, `frontend/lib/harden_llm_web/live/workspace_live.ex`, `frontend/lib/harden_llm_web/live/profile_widget_component.ex` | `mix test test/harden_llm_web/live/workspace_live_test.exs` | Seeded profiles expose only reasoning levels present in their `reasoningEffortMap`; custom profiles without a map disable the compact selector and the run payload omits stale unsupported reasoning before the gateway can reject it. |
| WEB-TEST-041 | Utility cache state migration | `test/harden_llm_web/live/workspace_live_test.exs`, `frontend/lib/harden_llm_web/live/workspace_live.ex`, `frontend/lib/harden_llm_web/live/profile_widget_component.ex` | `mix test test/harden_llm_web/live/workspace_live_test.exs` | The widget exposes only `cache` and `refresh`, converts legacy persisted `off` to `cache`, toggles the selected mode, and persists the changed state. |
| WEB-TEST-042 | Saved-profile boundary before endpoint run | `test/harden_llm_web/live/workspace_live_test.exs`, `frontend/lib/harden_llm_web/live/profile_widget_component.ex`, `frontend/lib/harden_llm_web/live/workspace_live.ex` | `mix test test/harden_llm_web/live/workspace_live_test.exs` | Endpoint, API interface, credential, fallback, or profile-identity edits block a run until the profile mutation is explicitly saved; ordinary transient run options remain available. |
| WEB-TEST-043 | Multi-instance embedding contract | `test/harden_llm_web/live/embedding_live_test.exs`, `test/browser/full_workflow_test.exs`, `frontend/lib/harden_llm_web/live/embedding_live.ex`, `frontend/lib/harden_llm_web/live/profile_widget_component.ex` | `mix test test/harden_llm_web/live/embedding_live_test.exs && mix test --only browser test/browser/full_workflow_test.exs:43` | Two in-flow widgets have unique DOM/form IDs, tagged parent routing, independent folds/cache/profile selection, and distinct main/escalation upload names without tabs or horizontal overflow. |

### Parallel feedback hierarchy addendum

The frontend follows the lowest sufficient tier rule. LiveViewTest is the
primary owner of server-side state and rendered diffs; Node's built-in runner
owns extracted pure client decisions; Chromium is reserved for native browser,
LiveSocket, hook, focus, layout, and event-serialization boundaries. There is
no DOM emulator in the initial implementation. If an expensive-tier defect is
found, the root invariant receives a cheap regression whenever it can be
represented without browser APIs; the browser case remains only for its
distinct boundary. `mix test` remains deterministic and excludes all browser,
Compose, and deployed tags by default.

| ID | Test | Target | Command | Pass criteria |
| --- | --- | --- | --- | --- |
| WEB-TEST-044 | Server-owned widget state matrix | `test/harden_llm_web/live/profile_widget_component_test.exs`, workspace/embedding tests | `mix test test/harden_llm_web/live/profile_widget_component_test.exs` | Public LiveView events and diffs cover compact no-tabs topology, all main/nested folds, profile/reasoning/cache/retry/repair transitions, uploads, tagged parent messages, capability-aware reasoning, and independent instances. |
| WEB-TEST-045 | Async frontend ownership policy | `test/harden_llm_web/test_policy_test.exs`, `test/support/conn_case.ex`, affected deterministic tests | `mix test test/harden_llm_web/test_policy_test.exs` | Safe deterministic modules use `async: true` and private Req ownership; exactly the named SessionVault and observability global-state exceptions remain serial with rationale. |
| WEB-TEST-046 | Pure client functional core | `frontend/assets/js/client_core.mjs`, `frontend/assets/test/client_core.test.mjs`, `test/harden_llm_web/boundary_test.exs` | `node --test frontend/assets/test/client_core.test.mjs` | Filtering, highlight wraparound, known/custom commit, Escape/blur, shortcut, and schema-pending decisions pass through the same production import; no package or DOM emulator is added. |
| WEB-TEST-047 | Ordinary browser canaries | `test/browser/widget_canary_test.exs`, `test/browser/authenticated_workflow_canary_test.exs`, `test/browser/compose_smoke_test.exs` | `mix test --only browser --max-cases 1` | Exactly two ordinary canaries prove browser-owned event, hook, focus, overflow, authentication, run/reconnect/logout, and two-instance boundaries; Compose remains separate and release-only. |
| WEB-TEST-048 | Deployed release canary | `test/browser/deployed_canary_test.exs`, `scripts/run-deployed-browser-test.mjs` | `node scripts/run-deployed-browser-test.mjs` | After release identity validation, one serialized authenticated session unfolds the widget, selects `CPA GPT-5.6 Luna`, performs one bounded nonce-marked prompt, resolves ambiguous history once without resubmission, deletes only its smoke record, logs out, and leaves no credential/live output in evidence. |
| WEB-TEST-052 | Utility editor defaults and help markers | `test/harden_llm_web/profile_defaults_test.exs`, `test/harden_llm_web/live/profile_widget_component_test.exs` | `mix test test/harden_llm_web/profile_defaults_test.exs test/harden_llm_web/live/profile_widget_component_test.exs` | Utility-aligned option, retry, escalation, pricing, profile/model, reasoning, and cache defaults plus rendered `?` help-marker titles remain present. |
| WEB-TEST-053 | Empty-state utility preset hydration | `test/harden_llm_web/live/workspace_live_test.exs` | `mix test test/harden_llm_web/live/workspace_live_test.exs` | An empty saved selection resolves to the backend-owned `CPA GPT-5.6 Luna` preset and its `gpt-5.6-luna` model while the backend catalog remains visible. |
| WEB-TEST-054 | Complete backend preset picker rendering | `test/harden_llm_web/live/workspace_live_test.exs` | `mix test test/harden_llm_web/live/workspace_live_test.exs` | Every one of the 28 backend-owned utility-llm catalog profiles is present in the server-rendered workspace combobox. |
| WEB-TEST-055 | Preset/model synchronization | `test/harden_llm_web/live/workspace_live_test.exs` | `mix test test/harden_llm_web/live/workspace_live_test.exs` | Selecting a non-default backend preset updates the LiveView-selected profile and synchronizes the workspace model ID to that preset. |
| WEB-TEST-056 | Workspace input widget topology and defaults | `test/harden_llm_web/live/workspace_live_test.exs` | `mix test test/harden_llm_web/live/workspace_live_test.exs` | The server-rendered input widget uses utility prompt/schema labels, placeholders, rows, monospace controls, conditional advanced rendering, action order, and one explicit Advanced input structured-output selector without a duplicate repair control. |
| WEB-TEST-057 | Contracted schema validation and run gate | `test/harden_llm_web/live/workspace_live_test.exs` | `mix test test/harden_llm_web/live/workspace_live_test.exs` | Unsupported utility JSON Schema keywords are rejected and a non-empty invalid schema disables and rejects a selected structured Run; the same schema draft does not block a text Run when structured output is Off. |
| WEB-TEST-058 | Workspace conversation restore and reset scopes | `test/harden_llm_web/live/workspace_live_test.exs` | `mix test test/harden_llm_web/live/workspace_live_test.exs` | A successful run patches the trace-addressed workspace URL; a trace URL restores the redacted output after mount; `New Prompt`, `Clear System Prompt`, and `New` clear only their documented fields and conversation state. |
| WEB-TEST-059 | Explicit response mode and repair projection | `test/harden_llm_web/live/workspace_live_test.exs` | `mix test test/harden_llm_web/live/workspace_live_test.exs` | The input widget renders one structured-output selector and no duplicate repair control; selecting On produces a structured request, selecting Off permits a text request with a retained schema draft, and the profile `Retries & Repair` setting controls structured repair. |

Detailed fixtures and isolation:

- Use `Req.Test` as the default API substitute. No test opens an unregistered network connection.
- Derive valid JSON bodies from OpenAPI examples; mutation tests cover malformed envelope, unknown status, invalid content type, delayed response, disconnect before headers, and disconnect after backend acceptance.
- Use unique synthetic users, profile IDs, run IDs, trace IDs, and artifact IDs. Fixtures contain no real provider keys or prompts.
- Freeze time for session-expiry and presigned-URL tests.
- Capture Logger and OTel test exporters in memory and assert sensitive values are absent from both keys and values.
- Decode test-only cookie and LiveView session payloads and prove they contain the random frontend handle but not the backend bearer token.
- The real-browser test uses a deterministic local REST fixture or the Compose smoke backend with a deterministic local provider; it never spends provider tokens.
- Browser assertions run at `1440x900` and `390x844`, check horizontal overflow, and preserve screenshots under an ignored evidence directory only on failure.

Canonical gates:

```bash
cd frontend
mix format --check-formatted
mix compile --warnings-as-errors
mix test
mix test --only browser
mix deps.audit
mix hex.audit
MIX_ENV=prod mix assets.deploy
MIX_ENV=prod mix release
```

`mix test` excludes `:browser` and `:compose` by default. CI runs WEB-TEST-012 only after the backend Compose smoke is healthy.

## 16. Acceptance criteria

The frontend v1 is complete when:

- WEB-TEST-001 through WEB-TEST-011 pass, and WEB-TEST-012 passes for release certification.
- `mix format --check-formatted`, warning-free compile, dependency audits, asset build, and OTP release build pass from a clean checkout.
- The browser performs every required workflow without direct Go API, Postgres, Garage, Firebase, or provider access.
- OpenAPI/client parity proves there is one cross-runtime contract.
- Login tokens remain confined to the ephemeral server-side token vault and server-side REST calls; browser and LiveView session payloads contain only a random handle.
- No mutation, especially `/api/v1/run`, is retried automatically.
- One Tempo trace correlates Phoenix request/LiveView work with the Go gateway and downstream spans.
- The base backend remains runnable without `frontend/`, while the overlay produces a healthy 16-service full product.
- No React, Firebase, Ecto, second persistence path, or duplicated backend domain logic remains in the frontend.

## 17. References

- Backend stack: `plans/from_utility-llm/self-hosted-go-stack-spec.md`
- Backend test catalog: `plans/from_utility-llm/harden-llm-self-hosted-test-spec.md`
- Backend implementation plan: `plans/from_utility-llm/harden-llm-self-hosted-implementation-plan.md`
- Phoenix installer: <https://hexdocs.pm/phoenix/installation.html>
- Phoenix project generator: <https://hexdocs.pm/phoenix/Mix.Tasks.Phx.New.html>
- Phoenix LiveView: <https://hexdocs.pm/phoenix_live_view/Phoenix.LiveView.html>
- Req: <https://hexdocs.pm/req/Req.html>
- OpenTelemetry Erlang/Elixir: <https://opentelemetry.io/docs/languages/erlang/>
