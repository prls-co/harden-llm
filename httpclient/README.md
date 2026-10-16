# Go reference HTTP client

Import `github.com/prls-co/harden-llm/httpclient` when a Go service consumes an
already deployed Harden gateway. This package performs HTTP encoding and
canonical OpenAPI validation. It does not construct providers, access databases,
issue credentials, schedule jobs, or restore the retired durable operation SDK.
The server owns cache, search, repair and its bounded recovery policy.

## Configuration and API

Construct `httpclient.New(httpclient.Config{BaseURL: origin, APIKey: key})` from
server-held configuration. `origin` is an absolute HTTPS origin such as
`https://harden-llm.prls.co`, without `/v1`, a path prefix, credentials, query or
fragment. Explicit loopback HTTP is supported for local handler tests only;
there is no failed-HTTPS fallback. The bearer must match the gateway's accepted
key. A supplied `HTTPClient` is shallow-copied, not modified.

- `ListProfiles(ctx)` → `GET /api/v1/profiles`, returning `ProfilesEnvelope`.
- `GetProfile(ctx, id)` → `GET /api/v1/profiles/{id}`, returning `ProfileDraft`.
- `Run(ctx, input)` → one `POST /api/v1/run`, consuming its bounded SSE stream
  and returning `RunOutcome` plus a possible typed error.

All three operations use the same bearer. The key selects the profiles/cache/
history scope; a product `accountId`, customer token or execution grant is not
sent to Harden. Products sharing saved profile IDs must use the same approved
key scope. Keep public product authorization in the product, not this client.

`RunInput` matches the current `api/openapi.yaml` reference contract: required
`ProfileID`, `UserPrompt`, `CallType` and explicit `RecoveryPolicy`, optional
model override, schema, prompts, reasoning, provider options, search/cache,
timeout and correlation origin. `ReasoningEffort` uses `lowest`, `middle` or
`highest`, not ordinary OpenAI `low`/`high`. Use
`json.Marshal(hardenllm.DefaultStructuredRecoveryPolicy())` when the product
selects that owner default; no repair algorithm is copied into this package.

`ResolveReasoningEffort(definition, requested, preferred)` uses the profile's
configured portable levels without fetching or mutating it. Explicit
unsupported choices reject; omission selects the supported preference, then
lowest/middle/highest configured order. A profile without configured levels
returns empty, meaning no reasoning override. The caller owns its role
preference and must freeze the result with the accepted job, preserving the
original request/digest for retries. Do not re-resolve a saved result against a
later catalog or infer that this helper relaxes Run's strict wire validation.

For a structured call provide a JSON-object schema. The package validates the
request before dispatch and validates terminal output against that schema.
Request/schema/options bounds are 256/64/32 KiB. `timeoutMs` cannot exceed the
gateway's 60-second limit. The HTTP deadline is at most 75 seconds, shortened
by a caller deadline or shorter supplied client timeout. Redirects are disabled.
Individual SSE frames are bounded to 16 MiB; the full stream to 16 MiB + 64 KiB.

## Result and failure handling

Inspect both the returned `RunOutcome` and `error`:

| Outcome | Meaning and action |
|---|---|
| `err == nil` | Complete validated inference. `Result` retains output, selected/producing targets, attempts, accounting, cache and search; `State` retains the owner IDs. |
| `*RejectedError` | Valid pre-stream compact owner error with HTTP status. It is not a complete attempted-run envelope and does not establish zero provider usage. |
| `*RunFailure` | Definitive failed/timeout terminal. The nonzero outcome retains all available full terminal facts; it is not a usable successful output. |
| `*UnknownOutcomeError` | Dispatch may have happened without a complete validated terminal. Never automatically POST again. The error unwraps cancellation/transport causes without putting their untrusted text in `Error()`. |
| local validation/already-cancelled context error | No request was dispatched; correct the input or cancellation at its owner. |

`Recording` is independent from inference. A complete terminal followed by EOF,
transport failure or invalid history tail remains known with recording
`unconfirmed`. A valid `history.recorded` preserves its actual saved/disabled/
failed/unconfirmed status. Do not rerun inference to repair history.

`Evidence` is an optional compact projection for products, not a remote job
handle. `origin.operationId` is correlation only, never idempotency. When it
exists, `ProviderAttemptCount` counts actual `attempts[].providerUsed` flags,
not all attempts or remote billing. A cache-producing recovery can follow
earlier provider work. `search.executed` describes the answer's grounding and
can survive a cache hit; it is not a fresh search-charge counter.

A request with no optional product operation ID still returns its complete
reference result, but no product evidence is invented. A definitive failure
before target resolution also retains its failure outcome without a fabricated
target/evidence. Products that require compact evidence must enforce that
requirement before publishing their own successful results.

There is no reconnect, stream resume, history polling, alternate origin/profile,
SDK retry, remote cancellation or automatic inference replay. Durable products
must commit their own intent before calling, persist complete results before
stage use, and fence retries after an unknown outcome. Context cancellation
closes this request; it cannot promise to undo remote provider work.

## Verification

Canonical case: `SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-472`.

```sh
GOWORK=off go test -mod=readonly -race -count=1 ./httpclient ./api
GOWORK=off go test -mod=readonly -count=1 ./internal/gateway/httpapi -run '^TestHTTPClientUsesActualReferenceGatewayAndRecovery$'
go vet ./httpclient
make test-fast
```

The real gateway composition uses a local TLS provider and the actual recovery
engine. It proves client/server conformance and one HTTP POST across two owner
repair attempts; it is not shared-SQL, browser, production or paid-provider
certification. The larger owner tracks remain separate.
