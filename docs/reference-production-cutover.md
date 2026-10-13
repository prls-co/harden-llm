# 1. Reference production cutover

The approved database target (Kirill, 2026-10-13) is the existing PostgreSQL
used by Agent Platform's Temporal service, physically owned by
`prls-co/self-imp-bin-eval`, Compose project `compose`, service `postgres`,
container `compose-postgres-1`, network `compose_default`. Source:
`/home/kirill/p/self-imp-bin-eval/deploy/compose/docker-compose.yml`.
Do not start Agent Platform's standalone base stack or edit another owner's
unrelated dirty work. HLLM attaches to the existing network and creates only
its isolated `harden_llm` database and login role.

These commands are P06 operations after release qualification. P05 records and
reviews them; it does not apply them. See CHECK-422 and `deploy/reference-reset.json`.

## 2. PostgreSQL and protected environment

Verify the container's owner labels/network and query `pg_database`/`pg_roles`
for `harden_llm` first. The initial provision requires both to be absent; an
unexpected existing resource requires inspection, never a drop or password reset.
Generate a random URL-safe role password and one standard-base64 32-byte
`HARDEN_LLM_CREDENTIAL_KEY` into the protected production environment. Keep both
stable and backed up. Never print values or pass them in process arguments.

Run the following SQL through the existing owner's `psql` as its administrator,
using `docker exec -i compose-postgres-1` and a private stdin stream. The
`hllm_password` psql variable is supplied in that private stream. Use
`ON_ERROR_STOP=1`; capture/redact failures before displaying diagnostics.

```sql
CREATE ROLE harden_llm LOGIN PASSWORD :'hllm_password'
  NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
CREATE DATABASE harden_llm OWNER harden_llm;
REVOKE ALL ON DATABASE harden_llm FROM PUBLIC;
\connect harden_llm
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT USAGE, CREATE ON SCHEMA public TO harden_llm;
```

No other database/role/grant is changed. Set `HARDEN_LLM_DATABASE_URL` to that
role/database at `postgres:5432` and `HARDEN_LLM_POSTGRES_NETWORK=compose_default`
in the descriptor's protected `productionEnvFile`. The owner PostgreSQL volume
is retained. Verify access using the HLLM role, including failure to select an
administrator-owned sentinel table; never inspect other products' rows.

## 3. Garage

The existing physical owner is `garage-shared`, container
`garage-shared-garage-shared-1`, S3 endpoint `http://garage-shared:3900` on
`prls-observability`. Its v2.3.0 CLI exposes these commands:

```bash
docker exec garage-shared-garage-shared-1 /garage bucket create harden-llm-artifacts
docker exec garage-shared-garage-shared-1 /garage key create harden-llm-reference
docker exec garage-shared-garage-shared-1 /garage bucket allow \
  --read --write harden-llm-artifacts --key harden-llm-reference
```

Verify names are absent before first creation. Capture key creation stdout only
into a mode-0600 file, extract the key/secret directly into protected production
settings, and remove the temporary file. Grant no owner/admin permission. Set
`HARDEN_LLM_ARTIFACT_ENDPOINT`, `HARDEN_LLM_ARTIFACT_BUCKET`,
`HARDEN_LLM_ARTIFACT_ACCESS_KEY_ID`, `HARDEN_LLM_ARTIFACT_SECRET_ACCESS_KEY`.
Use that credential for a unique synthetic `artifacts/<key-hash>/<run-id>/`
put/list/get/delete acceptance probe and verify empty listing after deletion.
Do not reuse another product's bucket/key or delete old shared objects.

## 4. Application and ingress

Use the accepted source SHA and existing build procedure in
`plans/gateway-image-build-deployment-spec.md`. Update the protected descriptor
with both accepted image identities; include the reviewed gateway network
change in the allowed differences. Remove the retired HLLM database from that
descriptor's managed services. Existing `runApply` explicitly invokes
`gateway-schema` using the gateway image before application `up --no-deps`.
See [self-hosting](self-hosting.md) for the one apply/check entrypoint.

The prepared Caddy owner branch is `feat/hllm-reference-routing`, worktree
`/home/kirill/.cache/harden-llm-tmp/caddy-reference-routing`. Publish only its
reviewed changes through `caddy-shared`, then validate the configuration and
restart only Caddy using that owner's documented commands. Remove the old
`harden-llm-api.prls.co` rule from the remotely managed tunnel and verify its
remaining rules; the local mirror alone does not publish ingress. Preserve the
artifact hostname because other consumers use it.

After successful application/ingress checks, stop/remove only
`harden-llm-harden-postgres-1`, then remove its detached
`harden-llm_harden-postgres-data` volume. Verify container/volume labels and mounts
against `deploy/reference-reset.json` immediately before removal. Never use
`down -v` on any shared-owner project. Record source SHA, images, schema exit,
public HTTP/auth checks and independent feature status before declaring release.
