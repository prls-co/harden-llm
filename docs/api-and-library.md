# API and Go library

The authoritative HTTP contract is [`api/openapi.yaml`](../api/openapi.yaml).
Harden LLM accepts standard OpenAI-style requests on these routes:

| Route | Purpose |
| --- | --- |
| `GET /v1/models` | List native model IDs from the configured default or selected upstream. |
| `POST /v1/chat/completions` | Chat Completions text, structured-output, function-tool, or final-only stream request. |
| `POST /v1/responses` | Responses text, structured-output, function-tool, web-search, or final-only stream request. |

The public bearer is `HARDEN_LLM_TOKEN`. Use that same value from the ignored
root `.env` for normal OpenAI SDK clients and the Phoenix reference app. The
upstream `CPA_API_KEY` is private to the gateway. Direct `/v1` requests are
stateless and never recorded in product history. The separate `/api/v1`
reference API composes key-scoped saved settings, cache, history and artifacts.
Both surfaces use the same bearer validator; neither requires Portal cookies.

## OpenAI-compatible request

Use an OpenAI SDK's normal base URL and token settings. This example uses
Responses input as a single string:

```bash
API=https://harden-llm.prls.co/v1
curl --fail-with-body --silent --show-error \
  "$API/responses" \
  -H "Authorization: Bearer ${HARDEN_LLM_TOKEN}" \
  -H 'Content-Type: application/json' \
  --data '{"model":"<native-model-id>","input":"Reply with OK.","store":false}'
```

An OpenAI Chat Completions client can use `/chat/completions` with ordinary
`model` and `messages` fields. The supported request fields and response
shapes are documented in OpenAPI. Unknown fields are rejected rather than
silently ignored. This provides an OpenAI-compatible inference subset, not
every feature in the OpenAI platform. In particular, server-side conversations,
background runs, stored responses, multiple choices, and unsupported media or
tools are not accepted. Model IDs come directly from the upstream; Harden LLM
does not provide profile aliases.

## Optional hardening fields

Ordinary OpenAI requests need no Harden-specific wrapper. Add `harden` only
when the caller needs one of these controls:

```json
{
  "model": "<native-model-id>",
  "input": "Return a valid JSON object containing an answer.",
  "text": {
    "format": {
      "type": "json_schema",
      "name": "answer",
      "strict": true,
      "schema": {
        "type": "object",
        "properties": {"answer": {"type": "string"}},
        "required": ["answer"],
        "additionalProperties": false
      }
    }
  },
  "harden": {
    "cache": "off",
    "diagnostics": true,
    "timeout_ms": 30000,
    "recovery": {
      "maxAttempts": 1,
      "retryOn": [],
      "backoff": {"baseDelayMs": 0, "maxDelayMs": 0},
      "jsonRepair": null,
      "rerun": null
    }
  }
}
```

`harden.cache` accepts `off`, `cache`, or `refresh`; the default is `off`.
`harden.timeout_ms` can lower the deployment's synchronous limit. Recovery
policies are explicit and bounded. When `diagnostics` is enabled, the response
includes bounded hardening metadata. Provider-specific fields not included in
OpenAPI are rejected.

Streaming uses standard endpoint-specific SSE event formats, but the gateway
emits only the completed hardened result after validation and recovery. It does
not forward provisional provider tokens. Clients requiring first-token
streaming should not treat this final-only stream as equivalent.

## Go library

For built-in provider transports and telemetry composition, use the public
`stateful` package with explicit configuration. This client requires no database
or HTTP server:

```go
apiKey := os.Getenv("CPA_API_KEY")
client, err := stateful.NewClient(stateful.ClientOptions{
    DefaultConnection: "cpa",
    Connections: []hardenllm.Connection{{
        ID: "cpa", Provider: "cpa", Protocol: "responses",
        BaseURL: "https://cpa.prls.co/v1", APIKey: apiKey,
        SupportsWebSearch: true,
    }},
})
if err != nil {
    return err
}

result, err := client.Call(ctx, hardenllm.Request{
    ModelID: "<native-model-id>",
    Messages: []hardenllm.Message{{
        Role: "user", Content: json.RawMessage(`"Reply with OK."`),
    }},
    CallType: hardenllm.CallTypeText,
    RecoveryPolicy: hardenllm.DefaultRecoveryPolicy(),
})
```

Import `github.com/prls-co/harden-llm` as `hardenllm` and
`github.com/prls-co/harden-llm/stateful`. Libraries do not read `.env`.
`stateful.NewClient` creates the provider effects; `stateful.NewService` adds
reference storage using an application-supplied pool, credential vault, catalog
and artifact client. Neither requires an HLLM access token when used in process.

For fully injected effects, the root `hardenllm.New(hardenllm.Options{...})`
accepts a `ConnectionCatalog`, executor and optional cache/observer/model
discovery functions. It owns no authentication, SQL or telemetry SDK. All entry
points use this one engine; `Client.Call` returns results/accounting without
creating a history record.

## Access, health, and history boundaries

`GET /healthz` reports process liveness. `GET /readyz` reports that the proxy
loaded valid startup configuration; neither probe calls CPA. The `/v1/*`
routes require `Authorization: Bearer <HARDEN_LLM_TOKEN>`.

The `/api/v1` reference surface exposes profile snapshots/bundles, drafts,
runs, history, statistics and authenticated artifact downloads. The server
derives the storage scope from the validated key. Equal keys share data;
different keys are isolated. Callers cannot submit an owner, user ID or key hash
to select another scope. Human login belongs only to the Phoenix host and shared
Portal; the host sends its configured HLLM key to the gateway server-side.

Git presets contain metadata and starting values. Explicit Save creates a new
complete snapshot with a UUID and date suffix, after one bounded provider
probe; supplied provider secrets are encrypted in PostgreSQL. Environment-bound
presets use explicitly configured deployment credentials. Editing alone saves
only a draft. History summaries load first and full records load on expansion.
See OpenAPI and [architecture](architecture.md) for the reference contracts and
persistence failure outcomes.
