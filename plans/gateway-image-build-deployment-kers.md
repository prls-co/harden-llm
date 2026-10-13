# Gateway Image Build and Deployment KERs

- Specification: `SPEC-HLLM-IMAGE-DEPLOYMENT-001`.
- Meaning: KER means Key Engineering Requirement in this document.
- Decision authority: [ADR-HLLM-028](../docs/adr/ADR-HLLM-028-local-image-build-deployment.md).

| KER | Type | Requirement | Verification/evidence |
| --- | --- | --- | --- |
| KER-IBD-001 | Reproducibility | Every production gateway image identifies the full merged source SHA checked out by a specific successful release workflow run and attempt. The same SHA is used for the build context, local release tag, and OCI version/revision metadata. A documentation-only change does not relabel an existing image. The Go builder base remains digest-pinned. | Recorded release run ID/attempt/URL/SHA; merged ancestry; image inspection; isolated `version` output. |
| KER-IBD-002 | Simplicity | The supported production image path builds on the existing target host and uses its local Docker image store. No active GHCR publisher, package-write workflow, registry abstraction, or registry pull dependency is required. | Search active source/config; production descriptor check; release record. |
| KER-IBD-003 | Artifact identity | The tag is `harden-llm-gateway:release-<full-source-sha>` and must never be overwritten with a different image. The host descriptor records the exact Docker image ID and source SHA. | Inspect local tag/ID and descriptor; `production-config check --expected-release`. |
| KER-IBD-004 | Scope safety | Promotion and rollback may change only the gateway identity/image fields unless a separate approved change explicitly expands scope. The Compose graph, web, infrastructure, secrets, data, sessions, and volumes remain unchanged. | Scoped `production-config check`/`apply`; normalized before/after descriptor comparison; service inventory. |
| KER-IBD-005 | Safe application | A candidate is applied only after exact-SHA release certification, local image metadata validation, and review of every scoped difference. An old running release may differ from a valid desired candidate; desired-candidate errors, unapproved fields, and unexplained changes block application. A reference-only change to the currently running exact image must not force an unnecessary restart, and a successful apply must converge to equivalence. | Existing release gate; image inspection; TEST-269 production-config contract; post-apply equivalence and health evidence. |
| KER-IBD-006 | Rollback | The prior known-good image and a private descriptor checkpoint remain available through the rollback window. Image-only rollback requires schema compatibility; an approved destructive clean cut must explicitly record that the old data-dependent application is not a usable rollback target. The checkpoint supplies prior gateway values; rollback must not restore an old whole descriptor over unrelated later changes. Rollback uses the same service-specific identity checks and does not mutate data or remove unrelated resources. | Descriptor/image-ID checkpoint and rollback runbook evidence. |
| KER-IBD-007 | Security | Secrets, credentials, bearer/session data, registry tokens, and unredacted runtime configuration stay out of Git, the archive, build report, and release record. The production descriptor stays host-local and mode `0600`. | Content/mode review; archive inspection; redacted release record. |
| KER-IBD-008 | Historical recoverability | Removing publication code from active gates must preserve a compact historical implementation reference, its source commit, contents, and SHA-256, plus the decision/specification needed to restore it deliberately. | `docs/archive/README.md` and verified compressed bundle. |
| KER-IBD-009 | Bounded architecture | Do not add a second scheduler, publisher abstraction, image database, automatic prune job, or host-side deployment daemon for a single local image. | Repository review and ADR-HLLM-028. |
| KER-IBD-010 | Reconsideration | Reintroduce remote image publication only after a concrete distribution/recovery need is documented and a new decision covers ownership, private access, provenance, retention, restore, verification, and rollback. | New accepted ADR and approved release plan. |

## Acceptance summary

All KERs must hold for the active local lifecycle. KER-IBD-008 is met by a
non-executable compressed source bundle, not by leaving the retired workflow
or its publisher-specific regression in routine CI. Existing records of the
one completed private GHCR publication remain historical facts and are not
rewritten as if the publication never occurred. The registry package itself
was separately deleted with owner approval on 2026-09-22; the compressed bundle
preserves source code, not the removed image artifact. See ADR-HLLM-028 for
verification and operational implications.

## Reference decoupling acceptance and exceptions (2026-10-13)

| KERs | Applied scope and actual evidence |
| --- | --- |
| 001, 003 | Application `bbdb5d77bc76424804427ca30a7bf9ddc453d0cf`, release run 38377471293 attempt 1; gateway/frontend immutable identities in P06. Run URL and attempt URL are checked against their respective API shapes. Later operator/docs commits do not relabel images. |
| 004 | Explicit P05/P06 authorization covers both apps, shared HLLM database/role/bucket/key, Caddy routing and deletion of the exact obsolete HLLM database resources. Shared owner containers and other products' data/routes remain unchanged. |
| 005 | Exact-image schema exit 0 before applications; TEST-450 sequence coverage, TEST-234 physical-network comparison and TEST-434/435 deployed checks. Initial false postcheck is retained; corrected apply is a no-op. |
| 006 | The approved incompatible-data reset makes the pre-cutover PR #97 app unsuitable for image-only rollback. Protected checkpoints and old images are retained; recover only a schema-compatible image or a newly certified forward fix. No old database restore is claimed. |
| 007 | Evidence is redacted; protected settings/checkpoints stay outside Git, mode 0600. Cleanup removes disposable helpers/build artifacts, not live secrets or rollback evidence. |
| 002, 008–010 | Local image lifecycle, historical publisher archive and one deployment path remain unchanged; no registry, daemon, scheduler or automatic prune job added. |

[ADR-HLLM-028](../docs/adr/ADR-HLLM-028-local-image-build-deployment.md) records
these boundaries. [P06](evidence/harden-llm/reference-decoupling/phase-p06.md)
and the [audit](evidence/harden-llm/reference-decoupling/plan-conformance-audit.md)
separate runtime acceptance from documentation and cleanup.

## Reference UI correction acceptance (2026-10-13)

KER-IBD-001/003/005 are satisfied by application source
`739cfda1ea6a67fb7a2ee737e31d8342c58f36d0`, release 38439024276 attempt 1
(28 tasks passed), clean exact-source local builds and final scoped/full
equivalence. Actual image IDs and HTTP acceptance are in the
[RCA](../docs/rca/reference-preset-schema-and-recovery-ui.md#7-merged-and-deployed-acceptance).
Docs-only closeout revisions do not relabel those images.

The explicit #109 scope expands KER-IBD-004 to both application images/release
identities, nonsecret metadata for four existing provider credentials, and a
single exact faulty-default draft schema/revision correction. Protected settings,
shared owner containers/images and ingress are verified unchanged; no new
database/service, broader data reset or permission model is added. The prior
schema-compatible images and mode-0600 operator checkpoints are retained for
KER-IBD-006/007. The failed first gate and its port-allocation correction remain
in evidence; success is from corrected source, not an ambiguous retry.

## Preset workflow correction acceptance (#115, 2026-10-13)

KER-IBD-001/003/005 are satisfied by application source
`ff5c968bc3c2263956fb1f946935395506347bfc`, exact release 38455292756 attempt 1
(29 tasks, no cleanup issues), clean local builds, image/version verification,
schema-before-apps application and scoped/full equivalence. The
[RCA](../docs/rca/preset-draft-persistence-and-live-qualification.md#5-acceptance-and-rollout)
links immutable image IDs and deployed HTTP/catalog acceptance. Docs-only
closeout revisions do not relabel those images.

The authorized #115 scope covers both apps and the qualified Git preset catalog.
KER-IBD-004 comparison permits only their image/release fields; keys, upstream
metadata, data, shared/telemetry containers and ingress are unchanged. No draft
correction or provisioning occurs. Schema-compatible prior images and private
mode-0600 checkpoints satisfy KER-IBD-006/007. KER-IBD-002/008–010 remain unchanged:
one local build/operator path, without a registry, scheduler or compatibility
fallback. Failed live-provider candidates remain recorded separately from
release acceptance and are absent from active presets.

## Mixed recovery-profile correction acceptance (#119, 2026-10-13)

KER-IBD-001/003/005 are satisfied by application source
`0773784111baf5858fb799a730e169dfb052a3c1`, exact release 38474634696 attempt 1
(29 tasks, no cleanup issues), clean local builds, isolated gateway version and
image metadata verification, schema-before-apps application and scoped/full
equivalence. The [RCA](../docs/rca/mixed-recovery-profile-reasoning.md#5-release-and-scope)
links exact images and public HTTP/catalog acceptance.

KER-IBD-004 permits only the two application image/release identities. The full
reported failed record, keys, metadata, shared/telemetry owners and routing are
unchanged. No provisioning, SQL draft repair or data reset is required. Previous
schema-compatible images and protected checkpoints satisfy KER-IBD-006/007.
Other KERs and the one local build/operator path remain unchanged. Docs-only
closeout commits do not rebuild or relabel application images.
