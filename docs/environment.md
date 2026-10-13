# Environment reference

Compose reads `.env` from the repository root. Keep it ignored and mode 0600.
Do not shell-source deployment dotenv files; pass them through the approved
Compose or production-config entrypoint.

## Proxy and upstream

| Variable | Required | Purpose |
| --- | --- | --- |
| `HARDEN_LLM_TOKEN` | Yes | The initial incoming bearer for the OpenAI-compatible gateway and Phoenix-to-gateway calls. This is the API token clients use. |
| `CPA_API_KEY` | Yes | Credential for the configured CPA upstream. It is not an incoming client token. |
| `HARDEN_LLM_CONFIG_FILE` | Yes | Absolute host path to the connection-only JSON file mounted read-only into the gateway. |
| `HARDEN_LLM_MAX_RUN_DURATION_MS` | No; defaults to `60000` | Maximum synchronous request duration; accepted range is `1..60000`. |
| `HARDEN_LLM_PROVIDER_ALLOWED_HOSTS` | No | Optional comma-separated outbound host restriction. |
| `HARDEN_LLM_PROVIDER_PRIVATE_ALLOWLIST` | No | Explicit private endpoint hosts or CIDRs. Leave empty for public upstreams. |
| `JINA_API_KEY` | No | Server-side web-search fallback key when the request selects web search and the upstream does not provide it. |

The JSON file selects a default upstream and names the environment variable
containing its key. It must not contain the key itself. The checked-in
[`upstreams example`](../config/upstreams.example.json) is the canonical shape.
Ordinary OpenAI requests use native model IDs. The reference API also exposes
Git presets and key-scoped saved profile snapshots. `api_key_envs` names the
accepted incoming key variables, initially `HARDEN_LLM_TOKEN`.

## Public hosts and release identity

| Variable | Purpose |
| --- | --- |
| `HARDEN_LLM_WEB_HOST` | Single public hostname for Phoenix and both REST surfaces. |
| `HARDEN_LLM_GRAFANA_HOST` | Public Grafana hostname. |
| `HARDEN_LLM_RELEASE` | Immutable release identifier used by images and telemetry. |
| `HARDEN_LLM_ENVIRONMENT` | Bounded environment label such as `production` or `development`. |

Shared Caddy owns TLS, host routing, and public ports. Route API paths
`/v1/*`, `/api/v1/*`, `/healthz`, and `/readyz` to the gateway on the same
hostname. Other paths route to Phoenix. Both REST surfaces require the HLLM
bearer; Portal admission belongs to Phoenix.

## Phoenix reference application

| Variable | Required | Purpose |
| --- | --- | --- |
| `HARDEN_LLM_CONTROL_PLANE_URL` | Yes | Internal Control Plane identity/access API. |
| `HARDEN_LLM_CONTROL_PLANE_INTERNAL_TOKEN` | Yes | Service credential for the shared identity contract. |
| `PRLS_PORTAL_URL` | Yes | Canonical shared sign-in Portal origin. |
| `HARDEN_LLM_WEB_SECRET_KEY_BASE` | Yes | Phoenix signing/encryption root. |
| `HARDEN_LLM_WEB_SESSION_SIGNING_SALT` | Yes | Independent cookie signing salt. |
| `HARDEN_LLM_WEB_SESSION_ENCRYPTION_SALT` | Yes | Independent cookie encryption salt. |
| `HARDEN_LLM_WEB_API_TIMEOUT_MS` | No; defaults to `75000` | Phoenix-to-gateway timeout; keep it above the gateway's maximum request duration. |
| `HARDEN_LLM_WEB_INSTANCE_ID` | No | Bounded runtime instance identity. |
| `HARDEN_LLM_WEB_LOG_MAX_BYTES` / `HARDEN_LLM_WEB_LOG_MAX_FILES` | No | Bounded JSON log rotation. |

The web app receives `HARDEN_LLM_TOKEN` to call the gateway. It does not receive
`CPA_API_KEY`, database credentials or the provider-credential encryption key.
The Go wrapper owns PostgreSQL access and API-key-scoped application data.

## Shared application storage

| Variable | Purpose |
| --- | --- |
| `HARDEN_LLM_DATABASE_URL` | Isolated HLLM role/database on the approved existing shared PostgreSQL server; required by production Compose. |
| `HARDEN_LLM_POSTGRES_NETWORK` | Existing external Docker network supplied by that database owner. |
| `HARDEN_LLM_DATABASE_POOL_SIZE` | One gateway pool; defaults to 10 connections. |
| `HARDEN_LLM_CREDENTIAL_KEY` | Standard-base64 32-byte AES-GCM key, generated once per environment and backed up with its protected environment file. |
| `HARDEN_LLM_HISTORY_ENABLED` | Library/gateway history setting; production reference Compose explicitly enables it. Ordinary `/v1` calls remain outside history. |
| `HARDEN_LLM_CACHE_TTL_SECONDS` | Cache lifetime; defaults to 86400 seconds. |
| `HARDEN_LLM_ARTIFACT_ENDPOINT` | Shared Garage S3 endpoint. |
| `HARDEN_LLM_ARTIFACT_BUCKET` | Dedicated HLLM bucket. |
| `HARDEN_LLM_ARTIFACT_ACCESS_KEY_ID` / `HARDEN_LLM_ARTIFACT_SECRET_ACCESS_KEY` | Bucket credential permitting get/put/list/delete; only Go receives it. |

All four artifact settings are required by production Compose. Previews omit
all four and report artifacts disabled. Production storage settings belong to
the descriptor's protected `productionEnvFile`; they are not shared preview
credentials. The one-shot schema service inherits the gateway image override
and database configuration; it is not a managed long-lived descriptor service.

## Observability and build inputs

The Compose topology also uses Grafana/Loki and Laminar credentials shown in
`.env.example`. These remain scoped to their existing observability services.
`PRIVATE_MODULE_TOKEN` is a short-lived build-only credential for private Go
and Phoenix dependencies. Compose supplies it through BuildKit; it is not
stored in a running container or production descriptor.

The production-config command resolves approved environment files and passes
only its declared application allowlist into Compose. Keep those files private,
and never include their values in logs, plans, or deployment receipts.
