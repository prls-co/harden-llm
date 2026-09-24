# Environment Reference

Copy `.env.example` to `.env`; Compose reads it from the repository root. Keep
the file mode 0600 and out of Git. Values marked secret must be generated
independently and stored in a secrets manager.

## Edge and release identity

| Variable | Required/default | Purpose |
| --- | --- | --- |
| `HARDEN_LLM_API_HOST` | required | Public REST hostname. |
| `HARDEN_LLM_WEB_HOST` | frontend overlay | Public Phoenix hostname. |
| `HARDEN_LLM_GRAFANA_HOST` | required | Public Grafana hostname. |
| `HARDEN_LLM_LANGFUSE_HOST` | required | Public Langfuse hostname. |
| `HARDEN_LLM_ARTIFACT_HOST` | required | Public Garage presign hostname. |
| `PRLS_ALLURE_HOST` | required by the shared-observability release | Authenticated Allure Storage hostname routed by the existing Caddy edge. |
| `PRLS_TESTS_BASIC_AUTH_USER` / `PRLS_TESTS_BASIC_AUTH_HASH` | required by the shared-observability release | Edge user and bcrypt hash for the Allure route. Generate the hash with `caddy hash-password` and single-quote it in `.env`. |
| `HARDEN_LLM_ARTIFACT_EXTERNAL_ENDPOINT` | required HTTPS origin | Origin embedded in presigned URLs; must match the artifact host. |
| `HARDEN_LLM_TLS_MODE` | required | Caddy `tls` argument: ACME email in production or `internal` for private-PKI environments. |
| `HARDEN_LLM_BIND_ADDRESS` | `0.0.0.0` | Address for Caddy's only published ports. |
| `HARDEN_LLM_HTTP_PORT` / `HARDEN_LLM_HTTPS_PORT` | `80` / `443` | Caddy host ports. |
| `HARDEN_LLM_RELEASE` | required | Immutable release/version label used by images and telemetry. |
| `HARDEN_LLM_ENVIRONMENT` | `production` | Bounded deployment identity. |

## Gateway and application storage

| Variable | Required/default | Purpose |
| --- | --- | --- |
| `HARDEN_LLM_POSTGRES_PASSWORD` | secret, required | Dedicated application role password. |
| `HARDEN_LLM_ENCRYPTION_KEYS` | secret JSON, required | Key-ID to unpadded base64url 32-byte key mapping, for example `{"primary":"..."}`. Keep retired keys while rows reference them. |
| `HARDEN_LLM_ACTIVE_ENCRYPTION_KEY_ID` | required | Key ID used for new credential writes. |
| `HARDEN_LLM_ARTIFACT_BUCKET` | `harden-llm-artifacts` | Dedicated Garage bucket. |
| `HARDEN_LLM_ARTIFACT_ACCESS_KEY_ID` / `HARDEN_LLM_ARTIFACT_SECRET_ACCESS_KEY` | secret, required | Existing bucket-scoped S3 credentials supplied only to the gateway. |
| `PRLS_LOKI_S3_ACCESS_KEY` / `PRLS_LOKI_S3_SECRET_KEY` | secret, required by the shared-observability release | Dedicated Garage key restricted to the `prls-loki` bucket; supplied only to Loki. |
| `HARDEN_LLM_ARTIFACT_PRESIGN_TTL` | `1m`, max `5m` | Lifetime of an authorized artifact redirect. |
| `HARDEN_LLM_SESSION_TTL` | `24h` | Opaque bearer-session lifetime. |
| `HARDEN_LLM_STATIC_TOKEN` / `HARDEN_LLM_STATIC_TOKEN_OWNER_ID` | optional pair | Direct CLI bearer token and the existing owner ID it may access. The token is never persisted or returned; remove or rotate it to disable access. Static-token logout is not a revocation path. |
| `JINA_API_KEY` | optional server secret | Jina Search API credential used only when a run requests web search and its selected profile does not advertise native web-search support. A cache hit does not call Jina. |
| `HARDEN_LLM_CONFIG_FILE` | shared host scalar | Absolute path to profile/credential-reference JSON; large catalogs do not belong in `.env`. |
| `HARDEN_LLM_TOKEN` | dev API credential | Persistent bearer token used directly by cURL, bound at dev deployment to the existing operator through the static-token mechanism. No refresh or expiry flow. |
| `HARDEN_LLM_MAX_RUN_DURATION_MS` | `60000`, range `1..60000` | Deployment and request ceiling for synchronous runs. Requests may lower it. |
| `HARDEN_LLM_PROVIDER_ALLOWED_HOSTS` | empty | Optional comma-separated restriction for public provider hostnames. |
| `HARDEN_LLM_PROVIDER_PRIVATE_ALLOWLIST` | empty | Explicit comma-separated private hostnames/CIDRs; never use broad ranges casually. |
| `HARDEN_LLM_METRICS_RETENTION` | `30d` | Prometheus local retention. |

Compose constructs `HARDEN_LLM_DATABASE_URL`, the public Garage signing origin,
gateway service identity, and the Collector endpoint. The gateway's fixed S3
endpoint is `http://garage-shared:3900` on the existing `prls-observability`
network. Keep the shared service healthy before starting the gateway. Do not
override these to point at Langfuse-owned stores.

## Phoenix frontend

| Variable | Required/default | Purpose |
| --- | --- | --- |
| `HARDEN_LLM_WEB_SECRET_KEY_BASE` | secret, 64+ random bytes | Phoenix signing/encryption root. |
| `HARDEN_LLM_WEB_SESSION_SIGNING_SALT` / `HARDEN_LLM_WEB_SESSION_ENCRYPTION_SALT` | independent secrets | Separate cookie signing and encryption salts. |
| `HARDEN_LLM_WEB_SESSION_VAULT_PATH` | `/var/lib/harden-llm-web/session-vault.dets` | Encrypted single-replica Phoenix bearer-token vault file; keep its named Docker volume across releases. |
| `HARDEN_LLM_WEB_INSTANCE_ID` | `harden-llm-web-1` | Bounded OTel instance identity. |
| `HARDEN_LLM_WEB_API_TIMEOUT_MS` | `15000` | Normal server-to-server REST timeout. |
| `HARDEN_LLM_WEB_RUN_TIMEOUT_MS` | `65000` | Run transport timeout; startup requires it to exceed the gateway cap. |
| `HARDEN_LLM_WEB_LOG_MAX_BYTES` / `HARDEN_LLM_WEB_LOG_MAX_FILES` | `10485760` / `5` | Bounded JSON log rotation. |

The overlay supplies `HARDEN_LLM_API_BASE_URL`,
`HARDEN_LLM_ARTIFACT_PUBLIC_ORIGIN`, `HARDEN_LLM_WEB_PORT`,
`HARDEN_LLM_WEB_OTEL_EXPORTER_OTLP_ENDPOINT`, `HARDEN_LLM_WEB_SERVICE_NAME`,
`HARDEN_LLM_WEB_ENVIRONMENT`, and `HARDEN_LLM_WEB_RELEASE` from the private
topology and shared release identity. The frontend receives no Postgres,
Garage, provider, Grafana, or Langfuse credential.

## Grafana and pinned Langfuse

`GRAFANA_ADMIN_USER` and secret `GRAFANA_ADMIN_PASSWORD` secure Grafana.
Langfuse requires independent Postgres, salt, encryption, NextAuth, ClickHouse,
Redis, and MinIO secrets plus `LANGFUSE_INIT_*` organization/project/user values.
The protected harden-LLM exporter receives only the initialized Langfuse
project public/secret keys.
The separate secret `PRLS_LAMINAR_PROJECT_API_KEY` authorizes the dedicated
full-content PRLS trace exporter to the existing Laminar deployment; it is not
shared with the harden-LLM Langfuse path.
These variables belong to the unchanged upstream service graph documented in
[`deploy/langfuse/UPSTREAM.md`](../deploy/langfuse/UPSTREAM.md); they must never
be reused for Harden LLM Postgres or Garage.

The production `.env` may intentionally keep the shared-observability values in
an approved, mode-0600 environment file instead of copying them into the
checkout. Do not shell-source that file: dotenv values are data and can contain
JSON, dollar signs, or shell-significant characters. The tested production
entrypoint parses the three approved files, passes only the existing shared
application allowlist through the child environment, and supplies the two
ordered Compose environment files without printing their values:

```bash
node scripts/production-config.mjs check \
  --descriptor /home/kirill/.config/harden-llm/production.json
```

For a release acceptance check, supply the certified candidate SHA explicitly
and select only the application service being promoted. Descriptor equivalence
alone does not prove that the intended candidate image is running. For a
gateway-only release:

```bash
node scripts/production-config.mjs check \
  --descriptor /home/kirill/.config/harden-llm/production.json \
  --services harden-llm-gateway \
  --expected-release <40-hex-commit-sha>
```

Run an analogous service-specific check for `harden-llm-web` when promoting
the web application. A combined candidate check is valid only when both
services intentionally use the same release SHA. Use the ordinary check
without `--expected-release` to verify whole-descriptor equivalence when their
release identities differ.

The descriptor is nonsecret host metadata. It records the fixed Compose graph,
the separate `composeRoot` and application checkout, Docker context, approved
source paths, retained image/release identities, and service-specific nonsecret
overrides. Copy the shape from
[`config/production-config.example.json`](../config/production-config.example.json);
do not put credentials, merged environment contents, or bearer values in it.
The source files remain mode `0600`. The check is read-only, compares declared
environment keys and managed mounts/identity in memory, and exits nonzero for
unexplained drift. A scoped apply is explicit and uses the same fresh
resolution:

```bash
node scripts/production-config.mjs apply \
  --descriptor /home/kirill/.config/harden-llm/production.json \
  --services harden-llm-web \
  --expected-release <40-hex-commit-sha>
```

`--expected-release` is restricted to the gateway and web application
services, requires a full lowercase Git SHA, and compares desired image
metadata plus the running image label, release environment, and healthy state.
An old running application may be replaced by a matching desired candidate;
the command never treats an old desired image or missing candidate metadata as
ready to apply. It cannot be combined with `--resolve-only`.

Apply is blocked when the desired local image does not resolve to the exact
descriptor digest, when a mount/configuration difference is outside the
selected policy, or when the approved source files change during review. It
uses `up -d --no-build --no-deps --pull never --wait`; it never runs `down`,
deletes volumes, synchronizes profiles, or recovers missing values from a
container. A no-op reports that no service recreation was required.

The deployed browser launcher remains a separate, explicitly authorized
browser boundary. It performs its own identity check and must be run from its
documented release process; the production-config command is not a browser
test and does not launch one:

```bash
HARDEN_LLM_EXPECTED_RELEASE=<merged-sha> node scripts/run-deployed-browser-test.mjs
```

The same injection is required for `PRLS_LOKI_S3_ACCESS_KEY`,
`PRLS_LOKI_S3_SECRET_KEY`, and `PRLS_LAMINAR_PROJECT_API_KEY` when they are
managed by the shared observability host. Never commit a merged environment
file or place its values in a release command, plan, log, or KER.

When Loki authentication is enabled, the existing harden-LLM log path retains
the canonical `fake` tenant used by Loki while authentication was disabled.
Both the protected Collector exporter and the `harden-loki` Grafana datasource
must keep that tenant so pre-cutover and new harden-LLM logs remain one query
surface. The separate `prod`, `nonprod`, and `test` tenants are reserved for
PRLS agent telemetry.

## Optional live certification

Deterministic gates need no provider credential. TEST-037 runs only when
`HARDEN_LLM_LIVE_PROVIDERS` is a JSON array. Each item names the environment
variable containing its key; never put a key in the JSON:

```json
[
  {
    "name": "openai-responses",
    "apiKeyEnv": "OPENAI_API_KEY",
    "profile": {
      "schemaVersion": 3,
      "llmProfile": "LiveOpenAI",
      "provider": "openai",
      "apiInferenceType": "responses",
      "endpointCredentialScope": "user",
      "baseUrl": "https://api.openai.com/v1",
      "modelId": "replace-with-certified-model",
      "pricing": null,
      "supportsTemperature": false,
      "supportsContractedStructuredOutput": true,
      "tokensParam": "",
      "responsesTokensParam": "max_output_tokens",
      "defaultOptions": {"max_tokens": 32},
      "recoveryPolicy": {"maxAttempts":1,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}
    }
  }
]
```

TEST-038 reads the path in `HARDEN_LLM_LIVE_GATEWAY_CONFIG`. The mode-0600 JSON
file contains HTTPS origins, a dedicated test-user email, a profile, artifact
host allowlist, and the names—not values—of user password, provider key,
Grafana, and Langfuse credential environment variables. Partial configuration
fails; an absent config records `not run: credentials absent`. The test creates
unique profile/run records and deletes them before logout.

```json
{
  "gatewayUrl": "https://api.example.net",
  "email": "certification-user@example.net",
  "passwordEnv": "HARDEN_LLM_LIVE_USER_PASSWORD",
  "providerApiKeyEnv": "OPENAI_API_KEY",
  "profile": {
    "schemaVersion": 3,
    "llmProfile": "replaced-by-the-test",
    "provider": "openai",
    "apiInferenceType": "responses",
    "endpointCredentialScope": "user",
    "baseUrl": "https://api.openai.com/v1",
    "modelId": "replace-with-certified-model",
    "pricing": null,
    "supportsTemperature": false,
    "supportsContractedStructuredOutput": true,
    "tokensParam": null,
    "responsesTokensParam": "max_output_tokens",
    "defaultOptions": {"max_tokens": 32},
    "recoveryPolicy": {"maxAttempts":1,"retryOn":[],"jsonRepair":null,"rerun":null,"backoff":{"baseDelayMs":0,"maxDelayMs":0}}
  },
  "artifactAllowedHosts": ["artifacts.example.net"],
  "grafanaUrl": "https://grafana.example.net",
  "grafanaUserEnv": "HARDEN_LLM_LIVE_GRAFANA_USER",
  "grafanaPasswordEnv": "HARDEN_LLM_LIVE_GRAFANA_PASSWORD",
  "langfuseUrl": "https://langfuse.example.net",
  "langfusePublicKeyEnv": "HARDEN_LLM_LIVE_LANGFUSE_PUBLIC_KEY",
  "langfuseSecretKeyEnv": "HARDEN_LLM_LIVE_LANGFUSE_SECRET_KEY"
}
```
