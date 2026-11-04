# CPA nonproduction qualification

## 1. Scope and current gate

At the planning checkpoint, P6 of `plans/cpa-durable-execution-architecture.md`
remained open pending verification of an immutable runtime image, isolated
deployment and bounded live CPA campaign. Section 7 records completed execution. Synthetic results are not deployment or live-quota evidence. No
production promotion or browser run is authorized by this document.

Use an isolated local nonproduction API/worker environment. The ordinary branch
preview topology currently supplies no Temporal worker or Garage configuration;
retrofitting the unchanged frontend is unnecessary for this API acceptance.
The two API replicas and worker must run the same exact gateway image. All
runtime data/credentials belong to the new environment; no existing HLLM,
Control Plane or other-product dataset or bearer session is copied.

## 2. Verified host prerequisites

Read-only inspection on 2026-10-31 confirms:

1. Existing HLLM uses `postgres-shared:5432` on `compose_default`. The shared
   PostgreSQL container is `prls-control-plane-postgres-1`; its configured
   administrative role can provision an isolated HLLM database/role. Do not
   alter existing databases/roles or restart this shared service.
2. Existing self-hosted Temporal is `compose-temporal-1`, available on
   `compose_default`. Provision a uniquely named HLLM namespace; do not change
   cluster history shards, other namespaces or accepted workflow state.
3. Existing HLLM Garage is `garage-shared-garage-shared-1`, reachable as
   `garage-shared:3900` on `prls-observability`. Its CLI can read the current
   HLLM bucket. Provision a different HLLM acceptance bucket/key with only that
   bucket's read/write permissions; never reuse production artifact credentials.
4. The approved preview host's `sharedEnvFile` is
   `/home/kirill/p/harden-llm/.env`. It supplies CPA upstream access but lacks
   the database/Garage settings needed for this environment. Read the canonical
   CPA value in-process only; do not print or copy it into another credential
   registry. Runtime Compose can read this approved env file alongside the
   private environment-specific file.
5. The repository Dockerfile builds only the gateway and requires a protected
   private-module BuildKit secret. Its final scratch image receives no build
   credential. `.dockerignore` excludes `.env`, `.git`, `tmp`, plans and tests.

No resource has been provisioned merely by these prerequisite checks.

## 3. Prepare a concrete deployment

1. Finish and publish the source checkpoint after fast, canonical SQL/race and
   real Temporal/replay gates. Verify clean worktree, local/tracking/remote refs,
   PR head and hosted checks. Keep `main` and production unchanged.
2. Allocate a unique stable environment ID under
   `~/.local/share/harden-llm-cpa-acceptance/`. Protect directory/config/env files
   with 0700/0600 permissions. Retain a private lifecycle manifest so partially
   provisioned resources can be inspected instead of recreated blindly.
3. Generate new nonproduction application bearer/encryption/database credentials
   and a metadata-only connection JSON: `api_key_envs=["HARDEN_LLM_TOKEN"]`,
   one CPA Responses upstream, and `api_key_env="CPA_API_KEY"`. No local user
   registry or key-issuance endpoint is introduced. The upstream CPA key remains
   separate from incoming authentication.
4. Record proposed names for its database/limited role, Garage bucket/key,
   Temporal namespace, task queue, pool ID and Compose project. Refuse collisions;
   do not overwrite or adopt an unrelated existing resource.
5. Prepare a private Compose file for two loopback-only API listeners, one worker
   and a one-shot schema command. Reuse the existing private PostgreSQL/Temporal
   and Garage networks. Do not create additional storage/Temporal services.
6. Use `HARDEN_LLM_BATCH_ENABLED=true`, the same pool/queue/namespace on all three
   processes, cap 128, the qualified default 200-start/61-second operating window,
   default-zero recovery jitter and bounded pools/memory. Keep ordinary timeout
   60 seconds. Use the explicit development/no-exporter setting; do not create a
   telemetry stack or rebuild the unchanged frontend.
7. Build the existing Dockerfile with `VERSION` and `REVISION` equal to the exact
   published SHA, passing the private module credential through BuildKit secret
   input only. Record image ID/digest and version/revision labels. Build logs stay
   private; never print credentials on success or failure.

## 4. Provision and verify without inference

1. Create only the manifest's new limited SQL role/database, Garage bucket/key
   and Temporal namespace. Use private stdin/captured CLI output for credentials.
   Persist new credentials privately and redact all receipts. Shared services,
   production credentials/data and unrelated networks remain untouched.
2. Apply the matching image's explicit `schema` command to the new database.
   Start API/worker processes from that same image; run no provider probe.
3. Run `batch-upgrade` against the empty new authority and require `v2_ready`.
   This must create zero inference. Record SQL generation and stable workflow ID.
4. Verify exact running image identities, release, namespace/network attachments
   and scoped database/bucket configuration without dumping container secrets.
5. Check loopback `/healthz` and `/readyz`; verify missing/incorrect incoming
   bearer rejection and authenticated missing request/job IDs. CPA's upstream
   bearer must not become an incoming application bearer. These checks perform
   no model generation, profile Save, profile seeding or result mutation.
6. Keep the isolated environment available for the separate live campaign. Do
   not label metadata health checks as CPA acceptance.

## 5. Explicit bounded CPA acceptance

1. Use the separately authorized CPA research/qualification budget, `gpt-6-luna`
   with low reasoning, unique nonce-schema requests and a bounded output ceiling.
   No official API fallback or batching multiple logical tasks into one provider
   request is enabled. Start with a tiny native case to verify packaged wiring.
2. Qualify caller tracking across API replicas and ordinary JSON/completed-SSE
   compatibility without extending its original deadline. Qualify durable jobs
   through the existing `/v1/jobs` status/result contract for multi-minute work.
3. The representative campaign should cover 1,000 separate logical tasks, with
   explicit total/native-attempt/token/time bounds. Known pre-output refusals may
   retry under the controller; unknown/partial outcomes may not replay. Do not
   resubmit accepted work after a lost reply: inspect the same tracking/job/
   idempotency identity.
4. Emit safe liveness/progress every five seconds: actual queued/active/completed/
   failed counts, waiting reason, eligibility floor and elapsed time. Heartbeat
   is not completed work or a promised result ETA. Polling must not mutate status.
5. Validate every nonce/result and inspect actual attempt counts, outcome kinds,
   token usage/reporting gaps, timings, cooldown/probe behavior and final empty
   permits. Report any partial failure honestly. A successful profile is a
   workload observation, never a universal CPA quota guarantee.
6. Record exact source/image/environment identity and redacted receipts in the
   delivery journal and issues #134/#136. Mark full delivery only after the
   requirement audit proves every required gate. Review/merge and production
   promotion remain separate user decisions.

## 6. Retention and cleanup boundary

Retain private runtime credentials, lifecycle manifest and accepted job/results
for review. Any later cleanup must name only this environment's resources and
accepted artifact IDs. Never use broad Docker/SQL/Garage cleanup or delete shared
services, global key-scoped production prefixes or uncertain/accepted work.

## 7. Executed qualification

This checklist was executed on 2026-10-31 against runtime source `b75d86f`.
Two API replicas and one worker use exact image
`sha256:e94803f3781ade366eb407b9d61b4ed582b2c4bc4a7b020dc452d7fb6c12c0ba`
in isolated environment `hllm-cpa-acceptance-20261031-9a4b47`.
Both provider-free deployment checks and the separate live campaign pass:
1,000 separate validated durable CPA results in 259.002 seconds, plus two
tracked ordinary JSON/existing-SSE calls. No final permits or uncertain outcomes
remain. Reported campaign usage is 112,000 input/output tokens.

Docker's exhausted automatic address pool required one explicitly allocated,
non-overlapping owned private subnet. Initial operator-client payload/parser
errors and the existing top-level completed-SSE envelope limitation are retained
in the receipt; successful inference was not repeated to correct a parser.
No shared-service reset, browser or production promotion occurred. Scoped data
and private runtime credentials remain retained for review.

Authoritative redacted receipt:
`plans/evidence/harden-llm/cpa-admission/2026-10-31-nonproduction-cpa.json`.
Full requirement reconciliation and limits are in the acceptance audit section 7.
