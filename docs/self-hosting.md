# Self-hosting

Harden LLM runs one Go gateway and one Phoenix reference app. The Go stateful
wrapper uses an isolated HLLM database/role on shared PostgreSQL and a dedicated
Garage bucket. Phoenix has no database pool. Shared Caddy owns public routing.
The stateless Go library remains usable without any of these services.

## 1. Configure

1. Copy `.env.example` to a private environment file with mode 0600. Set the
   HLLM bearer, upstream credentials, Phoenix/Portal settings and release identity.
   Production storage secrets belong to the protected production environment.
2. Copy `config/upstreams.example.json` to an ignored local file. Its
   `api_key_envs` lists accepted incoming key environment names; each upstream
   names its provider key environment variable. The JSON contains no secrets.
   Set `HARDEN_LLM_CONFIG_FILE` to its absolute path.
3. Have the shared database owner provision the isolated `harden_llm` role and
   database on the approved existing PostgreSQL server. Set
   `HARDEN_LLM_DATABASE_URL` and `HARDEN_LLM_POSTGRES_NETWORK` to that owner's
   endpoint and external Docker network. Do not start another production database.
4. Generate `HARDEN_LLM_CREDENTIAL_KEY` once with `openssl rand -base64 32`,
   store it in the protected production environment and back it up with that file.
   Replacing it makes previously entered provider credentials unreadable.
5. Set all four Garage artifact variables. Its dedicated credential needs put,
   get, list and delete in the HLLM bucket. Attach the application to the existing
   `prls-observability` network for Garage, Caddy and observability.

Standard OpenAI clients use `https://harden-llm.prls.co/v1` with
`HARDEN_LLM_TOKEN`. The same host serves `/api/v1/*` for reference workflows and
Phoenix for other paths. The upstream `CPA_API_KEY` never authenticates incoming
clients. See [environment settings](environment.md) and
[connection configuration](shared-llm-configuration.md).

## 2. Apply the accepted release

Build the affected images using the repository release workflow. Update the
protected production descriptor from `config/production-config.example.json`
with their verified image identities and source SHA. The descriptor contains
paths and identities, never credentials. Review its allowed differences before
application; the first shared-database cutover changes the gateway's networks.

Use the existing production-config entrypoint with the accepted 40-character
source SHA:

```bash
node scripts/production-config.mjs check --expected-release "$release_sha" \
  --services harden-llm-gateway,harden-llm-web
node scripts/production-config.mjs apply --expected-release "$release_sha" \
  --services harden-llm-gateway,harden-llm-web
```

`apply` retains scope, image and freshness checks, then runs `gateway-schema`
from the same accepted gateway image and protected database configuration.
Only exit 0 permits application startup. Both commands retain `--no-deps`;
shared services are managed by their owners. A frontend-only or no-change apply
runs no schema command. Schema creation is transactional, matching version 1
is a no-op, and a version mismatch fails without modifying old data.

The approved obsolete-resource list is `deploy/reference-reset.json`. Remove
that old HLLM database container and volume only during the reviewed production
cutover. It grants no permission to reset shared PostgreSQL, identities, other
products, Garage objects or credentials. Do not use `down -v` on a shared stack.

## 3. Access and stored data

Control Plane and Portal own enabled human logins. Phoenix uses its server-held
HLLM key to call Go. Same-key clients share profiles, history, cache and artifacts;
different keys are isolated. Workspace drafts additionally use the stable Portal
user ID within the host key's scope. There is no Go user registry.

Reference runs can record history in PostgreSQL. Ordinary `/v1` calls never
write product history. Laminar receives diagnostics only. A history failure
preserves the inference result and never repeats inference. Full history records
load when details are expanded; list pages contain summaries. Explicit Save
creates a complete immutable profile snapshot; selecting or editing changes the
draft. Git presets remain credential-free starting values.

## 4. Browser-free acceptance

```bash
curl --fail-with-body -sS https://harden-llm.prls.co/healthz
curl --fail-with-body -sS https://harden-llm.prls.co/readyz
```

These paths check the gateway without upstream inference. Verify Phoenix health
through its internal `/healthz` endpoint and Portal admission through the public
host. Authenticated `/api/v1/status` reports storage/history/artifact availability
independently. Release evidence records the exact source SHA, images, schema exit
and HTTP/auth results. Browser layout and paid-provider inference require their
separate explicit opt-ins.
