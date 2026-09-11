# Shared LLM configuration

## 1. Source and boundaries

The operator-owned `/home/kirill/p/harden-llm/.env` is the source of shared
provider keys, model catalogs and portable application settings. Preview host
`sharedEnvFile` points to this file. Production's infrastructure `.env` remains
separate; do not copy database passwords, encryption keys, artifact credentials,
bearer tokens, session secrets, URLs or environment identity across deployments.

Every enabled **trusted** branch receives the same configured profiles for both
`TEST_LOGIN` and `HARDEN_LLM_LOCAL_OPERATOR_EMAIL`. These provider keys grant real
provider access and spending authority. Never enable previews for untrusted code.

The configuration uses the existing profile catalog schema:

- `HARDEN_LLM_SHARED_PROFILES`: single-quoted JSON catalog keyed by `llmProfile`.
  It contains full model IDs, endpoints, capabilities, pricing, backup profiles,
  cached model lists and default options. No provider keys belong in this JSON.
- `HARDEN_LLM_SHARED_CREDENTIALS`: single-quoted JSON mapping profile names to
  explicit `*_API_KEY` variable names, for example
  `'{"CPA GPT-5.6 Luna":"CPA_API_KEY"}'`.
- Referenced `*_API_KEY` values: ordinary secret `.env` variables. Missing or
  empty referenced keys fail deployment; profiles deliberately without a mapping
  remain unconfigured. Never invent dummy keys to make readiness appear green.
- `HARDEN_LLM_MAX_RUN_DURATION_MS`, `HARDEN_LLM_PROVIDER_ALLOWED_HOSTS`, and
  `HARDEN_LLM_PROVIDER_PRIVATE_ALLOWLIST`: shared gateway settings. The private
  allowlist must be reviewed before allowing internal endpoints in branch code.
- Artifact presign/session TTLs and frontend API/run timeout and log-size limits
  are also shared; encryption/session **secrets** are not.

Use literal single-quoted dotenv values to protect dollar signs. Keep `.env`
mode 0600 and outside Git. No branch build receives this file or these keys.

## 2. Application and verification

Every preview deployment resolves the shared config, applies gateway settings
even when no image changed, provisions both accounts, and invokes the trusted
`sync-profiles` command. Config-only updates need no application image builds.
Application settings take effect on container recreation; profiles and provider
keys take effect immediately after synchronization, without restarting the app.

For production, or a configuration-only update to an existing environment:

```bash
node scripts/shared-profiles.mjs \
  /home/kirill/p/harden-llm/.env \
  harden-llm-harden-llm-gateway-1 \
  sha256:REPLACE_WITH_VERIFIED_GATEWAY_IMAGE_ID
```

Use `hllm-preview-dev-gateway-1` for dev. The administrative image must contain
`sync-profiles`; it runs temporarily in the target gateway's network namespace,
with **that target's** database/vault settings, and exits. It does not replace the
running gateway image. Keys are supplied on stdin, never command-line arguments.
Only the administrative container receives the three required local DB/vault
variables; this does not connect development to production data.

Run this same command as part of a production configuration/release rollout.
After editing `.env`, redeploy enabled previews (manual branch workflow from
trusted `main`) or run the scoped command for each existing target. There is no
background watcher or cross-environment database link. Future enabled branches
are configured automatically. Previously enabled branches must incorporate the
current administrative command before deploying with this control version.

The command validates and encrypts before an atomic upsert **per account**. It
never calls an LLM, probes a provider, deletes history, or deletes unrelated
custom profiles. Each environment encrypts identical provider keys with its own
vault key and fresh nonces. A multi-account or multi-environment failure is
reported, not disguised as a globally atomic operation; fix the cause and rerun.

Managed profile names are authoritative: deployment overwrites edits to those
profiles. Removing a credential mapping explicitly unbinds that managed profile;
other bindings/custom profiles and old encrypted credential records are retained.
Removing an entire profile from the shared catalog stops managing it; it does
not delete it from databases. To revoke a key globally, revoke it at the provider
and synchronize every environment. UI changes to custom profiles stay local;
promote desired settings into `.env` to share them. Custom models on a shared
endpoint reuse that endpoint's shared key (same origin, scope and inference
type), so key rotation cannot leave contradictory credentials in one catalog.

Configuration sync is not evidence that an upstream provider is currently
accepting calls. Browser-free checks cover guest/operator login, full profile
configuration equality, credential binding, and gateway readiness. Deterministic
tests cover actual runtime credential resolution, rotation, isolation and retained
custom profiles. Real paid calls and browser tests remain separate opt-ins.

## 3. Rollback

Keep a private backup of `.env` before rotation. Restore the prior shared values
and rerun synchronization against affected environments. Reverting application
images alone does **not** revert synchronized profile/key configuration. Do not
reset databases, copy production sessions, or delete user history to recover.
