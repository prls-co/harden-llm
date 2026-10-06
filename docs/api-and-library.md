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
upstream `CPA_API_KEY` is private to the gateway. History is not part of the
REST API: direct requests are stateless and are never recorded. Phoenix
records only its own completed requests in its shared reference history.

## OpenAI-compatible request

Use an OpenAI SDK's normal base URL and token settings. This example uses
Responses input as a single string:

```bash
API=https://harden-llm-api.prls.co/v1
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

The root Go package uses the same execution engine without HTTP or environment
loading. Supply a connection directly to `hardenllm.New`, then choose its
native model ID on each request:

```go
apiKey := os.Getenv("CPA_API_KEY")
client, err := hardenllm.New(hardenllm.Options{
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

The library does not read `.env`; the application supplies credentials and
optional cache/telemetry implementations. It has no profile CRUD or history
store. `Client.Call` returns the normalized result and accounting metadata; it
does not persist the request.

## Access, health, and history boundaries

`GET /healthz` reports process liveness. `GET /readyz` reports that the proxy
loaded valid startup configuration; neither probe calls CPA. The `/v1/*`
routes require `Authorization: Bearer <HARDEN_LLM_TOKEN>`.

There are no profile, login, run-history, trace, or artifact endpoints on the
gateway. Human login is only for accessing the Phoenix reference UI. All
enabled logins share the frontend's history, while direct API calls remain
unrecorded.
