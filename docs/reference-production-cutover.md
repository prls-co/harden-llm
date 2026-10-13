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

The initial cutover prepared Caddy owner branch `feat/hllm-reference-routing`, worktree
`/home/kirill/.cache/harden-llm-tmp/caddy-reference-routing` (since removed after
merge). PR #9 is deployed on owner branch `codex/shared-portal-login` at
`0afe6c6677c1666608326316d560dd6e3d17ce75`; current UI corrections need no ingress
change. The following ingress instructions describe that completed cutover.
For a new routing change, publish only its
reviewed changes through `caddy-shared`, then validate the configuration and
restart only Caddy using that owner's documented commands. Remove the old
`harden-llm-api.prls.co` rule from the remotely managed tunnel and verify its
remaining rules; the local mirror alone does not publish ingress. Preserve the
artifact hostname because other consumers use it.

After the main origin passes public acceptance, remove only the retired API
hostname's explicit Cloudflare DNS record. Read it by ID and require name
`harden-llm-api.prls.co`, type `CNAME`, proxy enabled, and target
`b9686ab5-270b-4bd6-9aa7-a271c5a02f9d.cfargotunnel.com`; the reviewed record ID is
`714cdd9a7444198ef36bbd15fb51d45e`. Back up the response privately, re-read it to
detect intervening changes, delete that exact record through the existing
owner credential, and require an empty exact-name lookup afterward. Do not
remove any wildcard, main-origin or artifact-hostname record.

After successful application/ingress checks, stop/remove only
`harden-llm-harden-postgres-1`, then remove its detached
`harden-llm_harden-postgres-data` volume. Verify container/volume labels and mounts
against `deploy/reference-reset.json` immediately before removal. Never use
`down -v` on any shared-owner project. Record source SHA, images, schema exit,
public HTTP/auth checks and independent feature status before declaring release.

## 5. Post-cutover recovery and retained checkpoints

The 2026-10-13 release is complete; [P06](../plans/evidence/harden-llm/reference-decoupling/phase-p06.md)
records its exact identities and verification. The old HLLM-only database was
intentionally deleted. A prior application image alone cannot restore that data
or its architecture. Use only a schema-compatible retained image with the current
shared-service settings, or certify a forward fix. Do not restore an old whole
descriptor over current shared-owner configuration.

Keep the stable credential-encryption key, database/artifact secrets, production
descriptor and protected cutover checkpoints outside Git. They are operational
recovery material, not disposable temporary artifacts. Task cleanup may remove
operator scratch scripts, synthetic test files, compiled test binaries and
redundant logs once durable redacted evidence is committed. It must preserve
live mounts, current/previous release images, shared volumes and the publisher
source archive. An off-host restore drill remains separate follow-up work.


## 6. Reference UI correction and provider bindings

Issue #109 / [RCA](rca/reference-preset-schema-and-recovery-ui.md) records the
structured-preset and finite-editor correction. `config/upstreams.example.json`
is the minimal CPA example, not a restriction to one provider. Declare matching
upstreams only for configured deployment credentials:

| Preset binding ID | Provider/protocol | Base URL | Key environment name |
| --- | --- | --- | --- |
| `google` | `google` / `gemini-generate-content` | `https://generativelanguage.googleapis.com` | `GEMINI_API_KEY` |
| `novita` | `novita` / `chat-completions` | `https://api.novita.ai/openai/v1` | `NOVITA_API_KEY` |
| `openrouter` | `openrouter` / `chat-completions` | `https://openrouter.ai/api/v1` | `OPENROUTER_API_KEY` |
| `perplexity` | `perplexity` / `responses` | `https://api.perplexity.ai/v1` | `PERPLEXITY_API_KEY` |
| `openai` | `openai` / `responses` | `https://api.openai.com/v1` | `OPENAI_API_KEY` |
| `anthropic` | `anthropic` / `anthropic-messages` | `https://api.anthropic.com/v1` | `ANTHROPIC_API_KEY` |

Metadata contains environment references only; actual keys remain in protected
settings or entered encrypted credentials. Compose passes optional documented
keys only to the gateway. A referenced empty key fails startup; an undeclared
binding remains visibly unconfigured. Default upstream remains CPA. Verify
configured preset counts after a scoped apply without making inference calls.
The RCA defines the precise one-time correction for the introduced placeholder
in persisted drafts; preserve other fields and increment revision for CAS.

This correction is applied in production at
`739cfda1ea6a67fb7a2ee737e31d8342c58f36d0`. All 24 preset drafts match the
certified catalog; 14 have deployment credentials, while the ten OpenAI/
Anthropic presets need explicitly supplied keys. One faulty-default draft was
corrected with other fields preserved, and no faulty-default rows remain. The
[RCA acceptance](rca/reference-preset-schema-and-recovery-ui.md#7-merged-and-deployed-acceptance)
records release/images, scoped application, full equivalence and limitations.
Do not repeat initial provisioning, retired-resource cleanup or the completed
one-time draft correction as a routine deployment step.

## 7. Preset workflow release (#115)

Application source is `ff5c968bc3c2263956fb1f946935395506347bfc`, certified by
release 38455292756 attempt 1 (29 tasks). The
[RCA](rca/preset-draft-persistence-and-live-qualification.md#5-acceptance-and-rollout)
records exact image identities, scoped application, full equivalence and public
HTTP acceptance. The live catalog contains 40 templates; 30 are configured and
qualified in both text/structured modes, including 19 CPA models. The remaining
ten need OpenAI/Anthropic credentials and disable Run until configured.

This rollout changes only the two application image/release identities. Shared
owners, credentials, upstream metadata and routing are verified unchanged. No
draft correction or provisioning is required; rejected credential fields were
never stored. Retain schema-compatible previous images and the protected
checkpoint. Subsequent docs-only commits do not relabel the application source.

## 8. Current mixed recovery-profile release (#119)

Application source is `0773784111baf5858fb799a730e169dfb052a3c1`, certified by
release 38474634696 attempt 1 (29 tasks, no cleanup issues). The
[RCA](rca/mixed-recovery-profile-reasoning.md#5-release-and-scope) records exact
image identities, scoped/full equivalence and public HTTP acceptance. The same
40 templates and 30 credential-configured drafts match source. The originally
reported failed record is preserved in full.

Apply only the gateway/frontend image and release fields. The shared reasoning
projection also handles existing saved drafts, so no SQL correction or data
reset is required. Keys, metadata, shared/telemetry owners and Caddy are unchanged.
Retain the protected checkpoint and schema-compatible previous images; docs-only
closeout does not change application identity. Deployment made no inference call.
