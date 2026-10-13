# Reference Decoupling v5.2 Review Decisions

Source plan: `plans/restore-reference-workflows-decoupling-plan.md` v5.1 at
`a90353fa7b7af622f757b0e0f0ba9ed4b82e6dcc`, branch
`plan/restore-decoupled-reference-app`. Evidence was checked against
`origin/main` and baseline `754690c`.

## Terms used in this record

- **v5.1 plan**: the accepted plan revision above. **v5.2**: the proposed
  revision that applies the answers below.
- **Gateway**: the Go process `cmd/harden-llm-gateway`; its configuration is
  loaded in `cmd/harden-llm-gateway/config.go` (`serverConfig`).
- **Wrapper / stateful library**: the planned Go package `stateful/`, built by
  `stateful.NewService(Config)`. It owns profiles, drafts, cache, history and
  artifacts through one `*pgxpool.Pool`.
- **Phoenix persistence**: the current Ecto storage in
  `frontend/lib/harden_llm/reference.ex`, configured by `HARDEN_LLM_DATABASE_URL`
  in `frontend/config/runtime.exs:59`. P03.S13 deletes it.
- **dev / preview**: `dev` deploys to `https://harden-llm-dev.prls.co/` using
  `deploy/preview/compose.yml`. That file has a per-environment `postgres`
  service wired only to `web`, and `docs/preview-environments.md` says "There
  is no branch Garage service."
- **Artifacts**: trace/parse-failure/support-bundle bytes in Garage under
  `artifacts/<api_key_hash>/<run_id>/<artifact_id>` (plan section 8.3).
- **Prefix delete**: removing every Garage object under
  `artifacts/<api_key_hash>/<run_id>/`. History delete/clear call it before
  deleting the row.
- **TEST-427 / TEST-443 / TEST-449**: history delete/clear tests, artifact
  tests, and gateway startup-configuration tests, defined in plan section 7.3.

The plan also has a text error that needs no decision: P00.S03 and TEST-436
say "six" `RecordingOutcome` statuses, but section 8.4 defines five
(`disabled`, `configuration_error`, `saved`, `not_saved`, `unconfirmed`). v5.2
changes "six" to "five".

## 1. Keeping dev working between P02 and P05

### 1.1 Question

How should the gateway get its database before Phoenix stops storing drafts and
history in P03.S13, given that `dev` receives merges from the P02 exit onward?

### 1.2 Context & clarification

- Section 5.1 says to merge to `dev` "at phase boundaries whose application
  changes are deployable (P02 exit onward)."
- P03.S13 switches Phoenix drafts/history to `/api/v1/state` and
  `/api/v1/history`, then deletes Phoenix persistence.
- The gateway only learns `api_key_envs`, the database URL, the credential key,
  Garage settings and `HLLM_HISTORY_ENABLED` in P05.S01–S02 (TEST-449).
- Verified local source: `deploy/preview/compose.yml` passes
  `HARDEN_LLM_DATABASE_URL` only to `web`; the `gateway` service has no
  database variable.
- Impact: after the P03 merge, dev would return `storage_unavailable` for
  drafts and history until P05 lands. TEST-432 also asserts "no dedicated
  PostgreSQL", which the preview's own `postgres` service would fail unless
  that assertion is limited to production.

```yaml
# deploy/preview/compose.yml (origin/main), abridged
gateway:
  environment:
    HARDEN_LLM_TOKEN: ${HARDEN_LLM_TOKEN:?}
    # no database URL
web:
  environment:
    HARDEN_LLM_DATABASE_URL: postgresql://harden_llm:...@postgres:5432/harden_llm
```

### 1.3 Options

- `Option A`: Move gateway storage configuration into P02 and wire the preview
  PostgreSQL to the gateway
  - **Rubrics**: `Conf:80% | Invest:i | Blast:i | Reversal:i | Fit:i |
    Reuse:i | Obs:i | Surface:ii | Perf:na`
  - **Approach**: Move P05.S01–S02 (TEST-449) to the end of P02. Add one P02
    step that passes the preview database URL to `gateway` and runs
    `harden-llm-gateway schema` before startup. Phoenix keeps its own variable
    until P03.S13 removes it. Limit TEST-432's "no dedicated PostgreSQL" rule
    to the production descriptor.
  - **Example**: `gateway.environment.HARDEN_LLM_DATABASE_URL` points at
    `postgres:5432/harden_llm` in the preview Compose file. P03.S13 then deletes
    the `web` database variable.
  - **Architecture**: Matches the documented per-environment preview database.
    The gateway becomes its only user, as section 2 intends for production.
  - **SSoT**: Gateway configuration parsing stays in
    `cmd/harden-llm-gateway/config.go`. The preview database stays the one store
    per environment.
  - **System limits**: The preview `postgres` has `mem_limit: 256m`. One Go
    pool with the planned default maximum of 10 connections replaces Phoenix's
    pool of 10.
  - **Trade-offs**: dev keeps working at every merge. The cost is moving one
    test group earlier and editing the preview descriptor in P02.
- `Option B`: Keep the phase order and stop merging to dev until the P05 exit
  - **Rubrics**: `Conf:90% | Invest:ii | Blast:ii | Reversal:ii | Fit:ii |
    Reuse:ii | Obs:ii | Surface:i | Perf:na`
  - **Approach**: Change section 5.1 so `dev` receives merges only after the
    P05 exit. P02–P04 checkpoints go only to `feat/reference-decoupling`.
  - **Example**: After the dev reset in P00.S01, `dev` stays at `main` until
    P05 passes.
  - **Architecture**: No descriptor change before P05.
  - **SSoT**: Unchanged.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Simple and safe, but there is no deployed feedback on
    restored UI work for three phases.
- `Option C`: Keep the order and merge to dev anyway
  - **Rubrics**: `Conf:90% | Invest:iii | Blast:ii | Reversal:ii | Fit:iii |
    Reuse:ii | Obs:iii | Surface:i | Perf:na`
  - **Approach**: Accept that dev drafts/history are unavailable from P03 until
    P05.
  - **Example**: The dev workspace runs inference, but history shows
    "unavailable".
  - **Architecture**: Contradicts the "deployable" wording in section 5.1.
  - **SSoT**: Unchanged.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: No extra work, but dev is knowingly broken.

### 1.4 Recommendation

Option A. Correctness comes first, and section 5.1 already promises that every
merge to dev is deployable. A also matches the existing preview architecture,
which already has one database per environment.

## 2. Artifacts in dev and preview environments

### 2.1 Question

Should dev and preview environments store artifacts in Garage, or report the
artifact feature as not configured?

### 2.2 Context & clarification

- Verified local source: `docs/preview-environments.md` says "There is no
  branch Garage service." Production uses the existing shared Garage bucket.
- Plan section 8.1 already allows "separate buckets or prefixes per
  environment", but no step provisions one for dev.
- Artifact behavior is proven against a real Garage lease in TEST-443,
  independent of any deployment.
- Impact: without Garage, dev cannot show trace views or downloads. Runs still
  succeed because artifact publication failure preserves output (P03.S08).

### 2.3 Options

- `Option A`: Give `dev` a bucket or prefix in the existing shared Garage;
  disable artifacts in other previews
  - **Rubrics**: `Conf:60% | Invest:i | Blast:i | Reversal:i | Fit:ii |
    Reuse:i | Obs:i | Surface:ii | Perf:na`
  - **Approach**: The Garage owner creates a dev bucket or key restricted to a
    dev prefix. The trusted preview host gets its credentials through
    `sharedEnvFile`. Other branch previews leave Garage unset.
  - **Example**: `HARDEN_LLM_ARTIFACT_BUCKET=harden-llm-dev` on the dev
    project only.
  - **Architecture**: Adds no service; adds an owner-provisioned credential
    before P03 deploys to dev.
  - **SSoT**: Garage stays the one artifact store; one path scheme.
  - **System limits**: Unknown - not available in local context (Garage quotas
    and key scoping for the shared deployment were not inspected).
  - **Trade-offs**: Full trace UI on dev, but owner coordination moves earlier
    than CHECK-422.
- `Option B`: Report artifacts as not configured in every preview, including
  dev, until production
  - **Rubrics**: `Conf:85% | Invest:ii | Blast:ii | Reversal:ii | Fit:i |
    Reuse:ii | Obs:ii | Surface:i | Perf:na`
  - **Approach**: With Garage settings absent, `/api/v1/status` reports the
    artifact feature as not configured. Trace/download routes return a typed
    `artifact_not_found` or configuration error. Inference and history work.
  - **Example**: The dev history row opens, and the trace panel shows
    "Artifacts are not configured in this environment."
  - **Architecture**: Matches current preview documentation exactly.
  - **SSoT**: Unchanged.
  - **System limits**: None added.
  - **Trade-offs**: No owner dependency before P05, but trace UI is only
    verified by tests until production.

### 2.4 Recommendation

Option B for P02–P05. It matches the verified preview architecture and adds no
external gate. Real Garage behavior is already covered by TEST-443. A dev
bucket can be added later under CHECK-422 if deployed trace views on dev turn
out to matter for product feedback.

## 3. Order of P03 history delete and artifact steps

### 3.1 Question

How should P03 be ordered, given that history delete/clear depends on prefix
delete, which is implemented later?

### 3.2 Context & clarification

- P03.S03–S04 (TEST-427) require delete/clear to remove the run's Garage
  prefix before the row, against a real Garage lease.
- P03.S08 implements the prefix delete and says it is "used by P03.S04".
- Impact: P03.S04 GREEN cannot pass as written. The plan's rule "RED must fail
  an intended behavioral assertion" also breaks if the failure is a missing
  function.

### 3.3 Options

- `Option A`: Move the artifact steps ahead of stats/delete/clear
  - **Rubrics**: `Conf:90% | Invest:i | Blast:i | Reversal:i | Fit:i |
    Reuse:i | Obs:i | Surface:i | Perf:na`
  - **Approach**: New order is P03.S01–S02 history store, S03–S04 artifacts
    (TEST-443, including prefix delete), S05–S06 stats/delete/clear (TEST-427),
    then run/recording, routes, workspace and cleanup as before. Update the
    `Unlocks` chain.
  - **Example**: `DeleteHistory` is written after `deleteRunPrefix` already
    exists and is tested.
  - **Architecture**: Same files and tests, reordered.
  - **SSoT**: One prefix delete in `stateful/artifact.go`, used by delete and
    clear.
  - **System limits**: Clear does one Garage list plus deletes per run, in
    batches of 100 runs.
  - **Trade-offs**: Least change; test oracles stay unchanged.
- `Option B`: Split the Garage part out of TEST-427
  - **Rubrics**: `Conf:80% | Invest:ii | Blast:ii | Reversal:ii | Fit:ii |
    Reuse:ii | Obs:ii | Surface:i | Perf:na`
  - **Approach**: TEST-427 covers PostgreSQL-only stats/delete/clear. TEST-443
    adds "delete/clear removes the prefix before the row" when P03.S08 wires
    them together.
  - **Example**: `TestHistoryQueries` uses no Garage lease.
  - **Architecture**: Keeps the step order but moves one assertion.
  - **SSoT**: Same code; the ordering assertion moves to a different test.
  - **System limits**: Same as Option A.
  - **Trade-offs**: Keeps numbering, but delete exists for a while without its
    artifact ordering, and the oracle is split across two tests.

### 3.4 Recommendation

Option A. It keeps each test's purpose whole and needs only a reorder.

## 4. History growth in shared PostgreSQL

### 4.1 Question

What guard, if any, should limit or expose history growth now that history
lives in Agent Platform's shared PostgreSQL?

### 4.2 Context & clarification

- The v5.1 decision (DEC-HLLM-V51-02) stores records of up to 17 MiB in the
  `history` table with no expiry (section 2, History retention).
- Agent Platform PostgreSQL also serves other products, so HLLM growth now
  shares their disk.
- The risk table says to "measure row/object counts in P05", but P05 runs only
  against temporary test services. There is no real data to measure.
- Assumption (not verified locally): whether Agent Platform already alerts on
  PostgreSQL disk use is unknown.

### 4.3 Options

- `Option A`: Enforce a per-key storage quota on insert
  - **Rubrics**: `Conf:60% | Invest:i | Blast:i | Reversal:i | Fit:iii |
    Reuse:i | Obs:i | Surface:iii | Perf:ii`
  - **Approach**: Before inserting, compare the key's stored bytes with a
    configured limit. Over the limit returns `not_saved` with a new
    `history_quota_exceeded` code.
  - **Example**: `HARDEN_LLM_HISTORY_MAX_BYTES_PER_KEY=2147483648`.
  - **Architecture**: Adds a new product rule, setting and error code.
    Section 2 deferred retention policy until storage growth is measured.
  - **SSoT**: The limit lives in gateway configuration.
  - **System limits**: A per-insert size query needs a maintained running
    total or a scan of the key's rows. That is O(rows per key) without an
    extra counter.
  - **Trade-offs**: Strong protection, but it is the retention decision the
    plan explicitly postponed.
- `Option B`: Report table size in `/api/v1/status` and add a manual
  post-deploy check
  - **Rubrics**: `Conf:80% | Invest:ii | Blast:ii | Reversal:ii | Fit:i |
    Reuse:ii | Obs:ii | Surface:ii | Perf:i`
  - **Approach**: The authenticated status route adds the history row count
    for the caller's key and `pg_total_relation_size('history')`. The P06
    runbook records both after deploy, and the plan names a threshold that
    triggers a retention decision.
  - **Example**: `"history": {"rows": 1234, "tableBytes": 845000000}`.
  - **Architecture**: Extends a route the plan already defines. No scheduler,
    quota or service.
  - **SSoT**: PostgreSQL's own catalog is the measurement.
  - **System limits**: `pg_total_relation_size` is a catalog lookup. A per-key
    count uses the `history_page` index.
  - **Trade-offs**: Cheap and visible, but nothing stops growth automatically.
- `Option C`: Rely on Agent Platform's existing database monitoring
  - **Rubrics**: `Conf:40% | Invest:iii | Blast:iii | Reversal:iii | Fit:ii |
    Reuse:iii | Obs:iii | Surface:i | Perf:na`
  - **Approach**: Change nothing in HLLM; remove the impossible "measure in
    P05" wording.
  - **Example**: None in HLLM.
  - **Architecture**: Depends on an owner capability that has not been
    verified.
  - **SSoT**: Agent Platform monitoring, if it exists.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: No work, but the evidence is weakest.

### 4.4 Recommendation

Option B. It gives real numbers for the deferred retention decision at almost
no cost, and keeps the product-first priority (DEC-HLLM-V51-01). Option A
would make the retention decision early, and Option C rests on unverified
monitoring.

## 5. Gateway environment variable names

### 5.1 Question

Which prefix should the gateway's moved and new settings use?

### 5.2 Context & clarification

- Plan section 8.1 says "Move Phoenix's existing names to Go" but lists
  `HLLM_DATABASE_URL` and `HLLM_DATABASE_POOL_SIZE`.
- Verified local source: Phoenix uses `HARDEN_LLM_DATABASE_URL` and
  `HARDEN_LLM_DATABASE_POOL_SIZE` (`frontend/config/runtime.exs:59-60`). The
  existing web timeout is `HARDEN_LLM_WEB_API_TIMEOUT_MS`, not the plan's
  `HLLM_WEB_API_TIMEOUT_MS`.
- Existing gateway settings use `HARDEN_LLM_*` (`HARDEN_LLM_CONFIG_FILE`,
  `HARDEN_LLM_TOKEN`). The plan's new `HLLM_CREDENTIAL_KEY`,
  `HLLM_HISTORY_ENABLED` and `HLLM_CACHE_TTL_SECONDS` mix in a second prefix.

### 5.3 Options

- `Option A`: Use `HARDEN_LLM_*` for all moved and new settings
  - **Rubrics**: `Conf:90% | Invest:ii | Blast:ii | Reversal:ii | Fit:i |
    Reuse:i | Obs:ii | Surface:i | Perf:na`
  - **Approach**: Keep `HARDEN_LLM_DATABASE_URL`, `HARDEN_LLM_DATABASE_POOL_SIZE`
    and `HARDEN_LLM_WEB_API_TIMEOUT_MS`. Name the new ones
    `HARDEN_LLM_CREDENTIAL_KEY`, `HARDEN_LLM_HISTORY_ENABLED` and
    `HARDEN_LLM_CACHE_TTL_SECONDS`.
  - **Example**: The preview descriptor moves the database variable from `web`
    to `gateway` with no rename.
  - **Architecture**: Matches every existing variable.
  - **SSoT**: One naming convention.
  - **System limits**: None.
  - **Trade-offs**: Longer names; no descriptor renames.
- `Option B`: Rename everything to `HLLM_*` as part of the clean cut
  - **Rubrics**: `Conf:70% | Invest:i | Blast:i | Reversal:i | Fit:ii |
    Reuse:ii | Obs:ii | Surface:ii | Perf:na`
  - **Approach**: Rename all gateway and web variables, including
    `HARDEN_LLM_TOKEN`, in descriptors, `.env.example` and protected
    environments.
  - **Example**: `HLLM_TOKEN`, `HLLM_CONFIG_FILE`.
  - **Architecture**: Allowed by the clean-cut rule, but it touches protected
    shared environment files and the acceptance test's
    `HARDEN_LLM_TOKEN` requirement.
  - **SSoT**: One convention, after a broad rename.
  - **System limits**: None.
  - **Trade-offs**: Shorter names, but it adds work and risk with no product
    value.

### 5.4 Recommendation

Option A. It fits the existing architecture and changes no protected
environment file.

## 6. Time limits for history SQL

### 6.1 Question

How should the 500 ms statement cap coexist with history records of up to
17 MiB?

### 6.2 Context & clarification

- Plan section 8.1 caps checkout/transaction at 600 ms and every SQL statement
  at 500 ms, "with no fresh budget per statement".
- Section 8.3 gives the history insert a two-second detached context, then one
  readback within one more second. `GetHistory` reads the full row for restore.
- The 17 MiB bound is inherited from current reference storage (section 4,
  performance constraints).
- Impact: as written, a large but valid record could hit the 500 ms cap and be
  reported `not_saved`, or fail to load for restore.

### 6.3 Options

- `Option A`: Give history operations their own named budgets
  - **Rubrics**: `Conf:80% | Invest:i | Blast:ii | Reversal:ii | Fit:i |
    Reuse:i | Obs:i | Surface:ii | Perf:ii`
  - **Approach**: Keep 600/500 ms for profiles, drafts and cache. History
    insert uses 2 s, readback 1 s, and history detail 2 s. List, count and
    stats keep 500 ms because they read `summary` only. Name these constants
    once in `stateful/history.go`.
  - **Example**: `const historyWriteTimeout = 2 * time.Second`.
  - **Architecture**: Matches the budgets already written in section 8.3; it
    only removes the contradiction.
  - **SSoT**: One set of history constants.
  - **System limits**: Unknown - not available in local context (17 MiB jsonb
    insert latency on Agent Platform PostgreSQL 16.4 has not been measured);
    TEST-442's 17 MiB boundary record measures it.
  - **Trade-offs**: Clear, but adds two constants.
- `Option B`: Lower the history record bound so it fits 500 ms
  - **Rubrics**: `Conf:50% | Invest:ii | Blast:i | Reversal:i | Fit:ii |
    Reuse:ii | Obs:ii | Surface:i | Perf:i`
  - **Approach**: Reduce the 17 MiB bound (for example to 4 MiB) and keep one
    statement cap.
  - **Example**: Records over 4 MiB are `not_saved`.
  - **Architecture**: Changes an inherited contract. The plan says not to
    change limits just to pass a test.
  - **SSoT**: One cap.
  - **System limits**: Unknown - not available in local context.
  - **Trade-offs**: Simpler timing, but large baseline results stop being
    recorded.

### 6.4 Recommendation

Option A. It keeps the inherited 17 MiB contract, which ranks above simplicity
under the precedence rules. It only makes explicit the history budgets the plan
already states, and TEST-442's boundary record checks them against real
PostgreSQL.
