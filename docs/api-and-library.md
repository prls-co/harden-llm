# API and Library Usage

The root package is the portable execution library. The gateway is a thin
adapter that adds local auth, owner-scoped persistence, and the published REST
contract. `api/openapi.yaml` is authoritative for routes, schemas, examples,
status codes, and the `{state,result,error}` envelope.

## Go library

```go
package main

import (
    "context"
    "fmt"
    "time"

    hardenllm "github.com/prls-co/harden-llm"
)

type credential string

func (key credential) ResolveCredential(
    context.Context,
    hardenllm.CredentialRequest,
) (hardenllm.Credential, error) {
    return hardenllm.Credential{APIKey: string(key)}, nil
}

func main() {
    profile := hardenllm.Profile{
        SchemaVersion: 1,
        LLMProfile: "Primary",
        Provider: "openai",
        APIInferenceType: "responses",
        EndpointCredentialScope: "user",
        BaseURL: "https://api.openai.com/v1",
        ModelID: "replace-with-model-id",
        SupportsContractedStructuredOutput: true,
        ResponsesTokensParam: "max_output_tokens",
        DefaultOptions: map[string]any{"max_tokens": 64},
    }
    client, err := hardenllm.New(hardenllm.Options{
        Credentials: credential("resolve-from-a-secret-store"),
        EndpointPolicy: hardenllm.EndpointPolicy{
            AllowedHosts: []string{"api.openai.com"},
        },
    })
    if err != nil { panic(err) }

    ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    defer cancel()
    result, err := client.Call(ctx, hardenllm.Request{
        ProfileID: "Primary",
        Profiles: hardenllm.ProfileCatalog{"Primary": profile},
        UserPrompt: "Reply with OK.",
        CallType: hardenllm.CallTypeText,
        CacheMode: hardenllm.CacheModeOff,
        RetryPolicy: hardenllm.RetryPolicy{MaxAttempts: 1},
    })
    if err != nil { panic(err) }
    fmt.Printf("output=%v trace=%s tokens=%d\n",
        result.Output, result.TraceID, result.Usage.TotalTokens)
}
```

Credential resolution happens only after endpoint validation and is bound to
the normalized origin. Inject OTel providers, cache, artifact store, and logger
through `Options`; the library never initializes globals or reads deployment
environment variables.

## REST gateway

Health probes are the only unenveloped non-auth responses. Login returns an
opaque token once; store it only in process memory. For a machine-only CLI,
configure `HARDEN_LLM_STATIC_TOKEN` and its
`HARDEN_LLM_STATIC_TOKEN_OWNER_ID`, then use the token directly:

```bash
API=https://api.example.net
curl "$API/api/v1/run" \
  -H "Authorization: Bearer $HARDEN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"profileId":"CurlStructured","userPrompt":"Tell me a joke about yourself.","callType":"text"}' | jq
```

The static token is not revocable through `/api/v1/auth/logout`; rotate or
remove it in deployment configuration. Without a static token, login returns
an opaque session token. The following keeps the password off the curl
argument list:

```bash
API=https://api.example.net
read -rsp 'Password: ' PASSWORD; echo
TOKEN="$(printf '%s' "$PASSWORD" | \
  jq -Rs --arg email operator@example.net '{email:$email,password:.}' | \
  curl --fail-with-body --silent --show-error \
    -H 'Content-Type: application/json' --data-binary @- \
    "$API/api/v1/auth/login" | jq -er '.result.accessToken')"
unset PASSWORD
```

On an owner's first `GET /api/v1/profiles`, the gateway inserts any missing
entries and returns the current 28 utility-llm preset profiles alongside the
owner's existing custom profiles. New preset rows are credential-free and
report `credential.configured:false`; use the returned non-secret
`credentialId` when storing a key for a preset. Credential fields are
write-only; subsequent reads return configured status, not plaintext. An
unconfigured preset cannot run until its credential is stored; the run API
returns `422 credential_required` and records the failed attempt in history
without contacting a provider:

```bash
printf '%s' "$OPENAI_API_KEY" | jq -Rs '{
  profile: {
    schemaVersion:1, llmProfile:"Primary", provider:"openai",
    apiInferenceType:"responses", endpointCredentialScope:"user",
    baseUrl:"https://api.openai.com/v1", modelId:"replace-with-model-id",
    pricing:null, supportsTemperature:false,
    supportsContractedStructuredOutput:true, tokensParam:null,
    responsesTokensParam:"max_output_tokens", defaultOptions:{max_tokens:64},
    backupProfiles:[]
  },
  credentialId:"primary-openai", credential:{apiKey:.}
}' | curl --fail-with-body --silent --show-error \
  -X PUT -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' --data-binary @- \
  "$API/api/v1/profiles/Primary" | jq
```

Execute one synchronous call. Do not automatically retry an ambiguous network
failure; inspect history before deciding whether to submit another run.

```bash
jq -n '{profileId:"Primary",userPrompt:"Reply with OK.",callType:"text",
  cacheMode:"off",maxAttempts:1,timeoutMs:60000}' | \
curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' --data-binary @- \
  "$API/api/v1/run" | jq
```

For the deployed structured smoke call, `scripts/harden-structured-call.sh`
uses `HARDEN_LLM_STATIC_TOKEN` from the local ignored `.env` when configured;
otherwise it reads `HARDEN_LLM_LIVE_USER_EMAIL` and
`HARDEN_LLM_LIVE_USER_PASSWORD`, logs in, submits the `CurlStructured` request,
prints the JSON response, and logs out:

```bash
make live-structured-call
```

Artifact authorization returns HTTP 303 with a short-lived, owner-authorized
HTTPS URL. Validate the location against your configured artifact origin, then
fetch it without forwarding the bearer token to storage.

## Contract verification

```bash
go test ./internal/gateway/... -run TestOpenAPIContract -count=1
```

Phoenix maintains a small operation registry and tests it against the same
OpenAPI document; no generated or handwritten second schema catalog exists.
