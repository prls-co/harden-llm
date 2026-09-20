# Shared LLM configuration

## 1. Source and boundaries

The operator-owned `/home/kirill/p/harden-llm/.env` is the source of shared
provider keys, credentials and portable scalar application settings. Model
catalogs live in a separate JSON configuration file. Preview host
`sharedEnvFile` points to this file. Production's infrastructure `.env` remains
separate; do not copy database passwords, encryption keys, artifact credentials,
bearer tokens, session secrets, URLs or environment identity across deployments.

Every enabled **trusted** branch receives the same configured profiles for both
`TEST_LOGIN` and `HARDEN_LLM_LOCAL_OPERATOR_EMAIL`. These provider keys grant real
provider access and spending authority. Never enable previews for untrusted code.

The configuration uses profile schema version 2. A complete synthetic example is
[`config/llm-profiles.example.json`](../config/llm-profiles.example.json). Each
profile requires its own complete `recoveryPolicy`; old catalogs and bundles
are rejected. Recovery settings do not belong in provider `defaultOptions`.
The coordinated data/configuration cutover is described in
[preview environments, section 6.1](preview-environments.md#61-recovery-policy-cutover-adr-hllm-020).

The configuration uses the existing catalog boundary:

- `HARDEN_LLM_CONFIG_FILE`: absolute path to a JSON file with `profiles` (the full
  catalog keyed by `llmProfile`) and `credentialEnv` (profile-name to `*_API_KEY`
  variable-name mapping). Reference host: `config/llm-profiles.local.json`.
  Model IDs, endpoints, capabilities, pricing, recovery policies and default options stay
  in that file, not `.env`. Actual secret values must not appear in this JSON.
- Referenced `*_API_KEY` values: ordinary secret `.env` variables. Missing or
  empty referenced keys fail deployment; profiles deliberately without a mapping
  remain unconfigured. Never invent dummy keys to make readiness appear green.
- `HARDEN_LLM_MAX_RUN_DURATION_MS`, `HARDEN_LLM_PROVIDER_ALLOWED_HOSTS`, and
  `HARDEN_LLM_PROVIDER_PRIVATE_ALLOWLIST`: shared gateway settings. The private
  allowlist must be reviewed before allowing internal endpoints in branch code.
- `JINA_API_KEY`: optional server-side credential for the web-search fallback;
  keep it in the protected environment file and never expose it to Phoenix or
  browser clients.
- `HARDEN_LLM_TOKEN`: the persistent dev API bearer token, used directly by cURL.
  Dev binds it to its existing `preview-local` operator using the existing static
  token implementation. No refresh interval or extra client auth flow. Changing
  this variable and redeploying dev rotates it. Browser login sessions remain
  independent; this token is not propagated to other previews or production.
- Artifact presign/session TTLs and frontend API/run timeout and log-size limits
  are also shared; encryption/session **secrets** are not.

Use literal single-quoted dotenv values to protect dollar signs. Keep `.env`
mode 0600 and outside Git. No branch build receives this file or these keys.

When reusing an explicitly authorized application's `.env`, copy only the
needed inference/search variables into this canonical file, preserving existing
values unless rotating them intentionally. Bind new inference keys to existing
profiles through `credentialEnv` in the JSON config; `JINA_API_KEY` is a runtime
setting, not a model profile. Do not copy unrelated service or infrastructure
credentials. Configuration sync alone does not validate those upstream keys.

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

For a production runtime-variable change, use
`scripts/production-config.mjs`. It parses the approved shared application
source with the existing `sharedApplicationVariables()` allowlist, combines it
with the ordered observability and production `--env-file` sources, and checks
the selected runtime identity before any recreation:

```bash
node scripts/production-config.mjs check \
  --descriptor /home/kirill/.config/harden-llm/production.json
node scripts/production-config.mjs apply \
  --descriptor /home/kirill/.config/harden-llm/production.json \
  --services harden-llm-gateway,harden-llm-web
```

These values override infrastructure `.env` interpolation without copying its
secrets. The apply path is explicit, image-pinned, `--no-deps`, and refuses an
unapproved mount/image/configuration difference. It recreates only selected
services whose settings changed and retains their named volumes. A no-op does
not invoke `up`. The profile-sync command remains a separate administrative
step and does not recreate services or change runtime variables; it is never a
validation side effect of the configuration command.

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

When the persisted managed profiles, bindings, decrypted key payloads, and
custom-profile endpoint bindings already match the shared configuration, the
command returns `changed: false` and performs no profile or credential write.
The deployment still runs this local comparison because a user may have
changed a profile through the UI since the previous deployment. A configuration
change returns `changed: true`; the subsequent readback remains the source of
truth for all accounts.

Managed profile names are authoritative: deployment overwrites edits to those
profiles. Removing a credential mapping explicitly unbinds that managed profile;
other bindings/custom profiles and old encrypted credential records are retained.
Removing an entire profile from the shared catalog stops managing it; it does
not delete it from databases. To revoke a key globally, revoke it at the provider
and synchronize every environment. UI changes to custom profiles stay local;
promote desired profile settings into the shared JSON catalog and keys/scalar
runtime settings into `.env` to share them. Custom models on a shared
endpoint reuse that endpoint's shared key (same origin, scope and inference
type), so key rotation cannot leave contradictory credentials in one catalog.

Configuration sync is not evidence that an upstream provider is currently
accepting calls. Browser-free checks cover guest/operator login, full profile
configuration equality, credential binding, and gateway readiness. Deterministic
tests cover actual runtime credential resolution, rotation, isolation and retained
custom profiles. Real paid calls and browser tests remain separate opt-ins.

## 3. Rollback

The trusted deployment controller checks out `main`; updating a `dev`
application does not update that controller. Configuration-format changes must
ship the matching loader, runtime-variable mappings, preview Compose template,
and tests to the controller together. A controller-only update does not promote
the application or deploy production. Verify the normal `workflow_run` path
after a passing dev push, not only a manual host deployment. Keep large JSON
catalogs in the config file; never restore JSON-in-`.env` to bypass validation.

Keep a private backup of `.env` and the JSON config before rotation. Restore the prior shared values
and rerun synchronization against affected environments. Reverting application
images alone does **not** revert synchronized profile/key configuration. Do not
reset databases, copy production sessions, or delete user history to recover.

## 4. Verified rollout — 2026-09-11

Configuration implementation: `934e00a0b35c314045aa663581b4568d61a76317`, pushed
to `dev` and trusted `main`. The single private `.env` now contains 31 managed
profiles, with seven configured bindings using the two existing CPA/LiteLLM
provider keys. Both guest and operator accounts were synchronized in dev and
production. Providers without existing keys remain explicitly unconfigured.

Validation performed in this rollout:

- `make test-fast` passed twice locally. Full gateway/Postgres integration
  packages passed through the runner-owned local service pool.
- [Dev fast CI](https://github.com/prls-co/harden-llm/actions/runs/34614866308),
  [main fast CI](https://github.com/prls-co/harden-llm/actions/runs/34614864987),
  and [automatic dev deployment](https://github.com/prls-co/harden-llm/actions/runs/34615284869)
  passed. No browser or full-release suite was triggered.
- Both public APIs returned health/readiness 200. Guest/operator profile
  readback matched all 31 shared profiles and seven configured bindings.
- All nine configured portable runtime variables matched the shared `.env` in
  both environments (host-list order normalized).
- Protected, in-memory decryption of each exported configured binding matched
  the intended `.env` key for all four account/environment combinations.
  Ciphertexts were distinct; local vault keys remained isolated. Test sessions
  were logged out; no provider calls were made.

Deployed component identities:

| Environment/component | Image identity |
| --- | --- |
| [Dev](https://harden-llm-dev.prls.co/) gateway, release `934e00a` | `sha256:d8b8b76b61b093721c9eee79267fe8f74e918b27d80768651e92cfe8bbc87424` |
| Dev web, retained release `da00a4a` | `sha256:27cc44573ed8d4ab8cf00c6f772ffc72973e904f9f33250b0f3c703698fdc403` |
| [Production](https://harden-llm.prls.co/) gateway, unchanged | `sha256:cd8a408899fe8dc478f799cd77e379ae11ae76a68b39ea777ab27018926c9736` |
| Production web, unchanged | `sha256:52edb3a68415f0325a439c87419599adeee5870b8fcf89bfae101206a79da025` |

The one-time administrative image used to apply production configuration was
`sha256:0b1326fd2d7a262c0fdc01efd85d69f5b6295fd6c30b42a5c3034687ed87049f`,
also built from `934e00a`. Production application container IDs and images
remained unchanged. A private pre-change `.env` backup was retained on the host;
no production history, sessions, or artifact data was copied to dev.

## 5. Production web-search rollout — 2026-09-14

Production gateway and web now run source
`9284df00a3270fd592051352da149077704dc521`. See the
[production release record](release-certification.md#cached-web-search-and-portable-testing-guidelines--production-2026-09-14)
for exact image identities, tests, backup, and rollback evidence. Section 4
above is the historical rollout record; its JSON-in-`.env` format and binding
counts are not the current configuration.

The current shared scalar settings and keys are in
`/home/kirill/p/harden-llm/.env`; the profile catalog and credential variable
references are in the separate JSON file selected by `HARDEN_LLM_CONFIG_FILE`.
The trusted administrative sync and authenticated readback passed for both
existing production accounts: **31 managed profiles, 21 configured bindings
each**. Unconfigured profiles remain explicitly unconfigured. No unrelated
profiles, history, or credentials were deleted.

The gateway received shared search/runtime settings, including `JINA_API_KEY`;
Phoenix did not receive that key. Production's own `HARDEN_LLM_STATIC_TOKEN`
and its owner, infrastructure credentials, vault keys, and session secrets
were retained. The development `HARDEN_LLM_TOKEN` was not copied to production.
Health/readiness, both web logins, static-token API access, and retained guest
History/trace reads passed without a browser or paid provider request. This
validates deployment/configuration, not upstream key acceptance or live search.

## 6. Astra managed-profile rollout — 2026-09-19

The approved external catalog now includes `CPA GPT-6 Astra` (`gpt-6-astra`,
Responses protocol, CPA endpoint) and was synchronized with the trusted
administrative command. Authenticated readback from both production accounts
reported **32 managed profiles and 22 configured bindings**. Astra exposes
`lowest`, `middle`, and `highest` reasoning options and is used by the default
rerun/recovery topology as documented in the recovery plan.

The profile uses the official OpenAI Standard short-context rates converted to
per-token accounting: input `0.00001`, cached input `0.000001`, cache creation
`0.0000125`, output `0.00005`, and reasoning `0`. The authoritative source is
the [OpenAI API pricing](https://developers.openai.com/api/docs/pricing) page.
The exact values are stored in the managed JSON catalog, not in `.env` and not
in CPA's provider-side pricing metadata.

The production readback and a bounded live structured request verified that
CPA Astra is configured and callable through harden-llm. The request succeeded
on its first attempt with exact profile accounting, complete trace resources,
and a 2.884-second provider call; the temporary history row was then deleted.
The authorized deployed browser canary also verified that all three Astra
pickers render in the retries/rerun UI and that their reasoning controls expose
the three supported choices.

The credential-free embedded 28-profile seed remains unchanged by design. It
is used for deterministic/local fixtures and does not contain provider keys.
New environments must provision the approved external catalog with
`sync-profiles` before relying on the Astra defaults; configuration sync and
live provider acceptance remain separately evidenced boundaries.
