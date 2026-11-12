# API and Go library

The authoritative HTTP contract is [`api/openapi.yaml`](../api/openapi.yaml).
The compatibility goal is an OpenAI `/v1` superset with optional Harden
controls. The routes and fields documented here define this release's
implemented coverage.
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

## Web search

Responses accepts the `web_search`, `web_search_preview`,
`web_search_preview_2025_03_11`, and `web_search_2025_08_26` tool types.
Request `include: ["web_search_call.action.sources"]` to receive URL sources
associated with individual search calls. Other include values are rejected.
OpenAI search tool options are forwarded to OpenAI; other upstreams accept only
`type`, `search_context_size`, and `user_location` as ignored hints and reject
other search options. Chat Completions accepts `web_search_options` with search
context and approximate location hints. Combining search with function tools
on `/v1` is not supported.

`tool_choice: "none"` disables search. The default or `"auto"` lets native
providers decide, while the Jina fallback runs a search. `"required"` forces
native OpenAI search and runs the Jina fallback; Anthropic and Gemini may still
decide whether to search. Go library callers that need search must set
`ToolChoice{Mode: "required"}` along with `WebSearch: true`. Inspect returned
calls to establish whether a search actually took place.

Responses returns observed search calls in their original order, followed by
the final answer. Chat returns URL citations on the message. Harden reports a
call only when the producer reports it or Harden performed it; it does not
assign unassociated sources to a call. Missing call IDs are generated. The
reference result retains unassociated sources, and its `incomplete` field
indicates dropped or unrepresentable search evidence. Image results, non-URL
sources, and Anthropic and Gemini citation offsets are not exposed as `/v1`
annotations. Citations are returned only when their indexed answer text is
unchanged. Harden uses Unicode code-point offsets. A two-call OpenAI probe on
2026-11-10 using `gpt-5` for Responses and `gpt-5-search-api` for Chat was
inconclusive for both APIs: code points, UTF-16 units and UTF-8 bytes all met
the probe's criterion. OpenAI's offset units therefore remain unverified.

Streams are generated from the completed result. Chat emits annotations in the
last content chunk. The probe observed a streamed Chat annotation in chunk 3
(zero-indexed) but did not record its position relative to the final content
chunk. An approved Chat-only follow-up on 2026-11-10 received HTTP 429 and
produced no further placement evidence, so placement remains unverified against
OpenAI. Search lifecycle events reflect the final status, not the timing of
upstream work. A
`Cache-Status: harden-llm; hit` header means the search evidence and answer
were replayed from cache; generated output item IDs retain the producing call's
identity. Each response retains its own response and execution IDs.

Search answers use the same manual cache controls as other answers. Callers
accept saved answers when using `cache` and select `refresh` or `off` when they
want new execution. There is no separate search freshness check or cache-age
setting. The existing generic cache expiration still applies.

The additional reference search fields require updating and deploying all
consumers that decode reference runs with `httpclient` before deploying the
updated gateway. The frontend and its schema deploy with the gateway.

The [2026-11-11 client audit](../plans/evidence/harden-llm/web-search-parity/2026-11-11-main-client-closeout.md)
records deployed reference-client upgrades and separate downstream model-default
follow-ups. Each ordinary API caller still supplies its model; the reference
workspace default does not rewrite consumer configuration.

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

- `cache` reads a saved answer and writes successful misses.
- `refresh` bypasses reads and replaces the saved answer after successful execution.
- `off` bypasses both cache reads and writes; it leaves existing entries intact.

The reference UI toggles between `cache` and `refresh`; the API also supports
`off`.

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

## Reference run outcomes

Reference JSON errors and `run.failed` events use `ReferenceError.code` to
distinguish provider uncertainty. `execution_unconfirmed` means the provider
may have completed or billed the request. Never automatically repeat the
inference POST. Local execution statuses such as `failed` and `timeout` do not
establish the provider outcome; history recording has its own independent
outcome.

The Go HTTP client returns `UnknownOutcomeError` for this explicit uncertainty.
The shared Phoenix client preserves it in the workspace and loaded history.
A missing recording tail does not erase an already validated unconfirmed
terminal. Malformed failure errors are rejected as protocol errors before
callback delivery. See the [HTTP client contract](../httpclient/README.md)
and the [provider uncertainty RCA](rca/reference-provider-uncertainty-162.md).
