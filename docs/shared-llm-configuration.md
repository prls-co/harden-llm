# Upstream connection configuration

The gateway loads one immutable connection catalog at startup from
`HARDEN_LLM_CONFIG_FILE`. The file contains routing metadata and environment
variable names only. Provider credentials remain in the deployment environment.
The deployment connection catalog is independent of the reference API's saved
profile snapshots. There is no startup synchronization or preset seeding.

The example in [`config/upstreams.example.json`](../config/upstreams.example.json)
defines the production CPA connection. Its model IDs are supplied per request;
the connection does not bind a model preset.

```json
{
  "default_upstream": "cpa",
  "api_key_envs": ["HARDEN_LLM_TOKEN"],
  "upstreams": [
    {
      "id": "cpa",
      "provider": "cpa",
      "protocol": "responses",
      "base_url": "https://cpa.prls.co/v1",
      "api_key_env": "CPA_API_KEY",
      "cache_domain": "cpa-production",
      "supports_web_search": true
    }
  ]
}
```

For a local copy, keep it in the ignored `config/` directory and point
`HARDEN_LLM_CONFIG_FILE` in `.env` at its absolute path. Keep it read-only to
the gateway. Put `CPA_API_KEY` and the incoming `HARDEN_LLM_TOKEN` in `.env` or
the host's approved shared application environment. Initially this key is accepted for both REST surfaces and selected by the
reference host; the CPA key never leaves the gateway. Additional allowed keys
are explicit environment names in `api_key_envs`. Each validated key gets an
isolated storage scope. Profile snapshots and encrypted entered provider keys
are saved through `/api/v1`; Git presets remain metadata only.

## Change and rotation

Edit the one connection file and its environment values, validate Compose with
`docker compose ... config --quiet`, then restart the gateway through the normal
deployment procedure. The gateway validates the complete file at startup and
fails startup when an upstream, required key, URL, or protocol is invalid.

Rotate the CPA credential in the host's approved secret source and restart the
gateway. Rotate `HARDEN_LLM_TOKEN` in the same source for the gateway and
reference frontend, then deploy both services together; external OpenAI SDK
clients must use an accepted key. Replacing a key starts a fresh storage scope;
it does not transfer old profiles, history or cache. Do not introduce an alias or token
fallback. Do not run an inference call merely to validate configuration.

The ready check confirms the process has loaded valid configuration. It does
not contact CPA. Use a token-authenticated `/v1/models` request only when an
authorized operator wants to verify live upstream reachability; that request
does not create a history record.
