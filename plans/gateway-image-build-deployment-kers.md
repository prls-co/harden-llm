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
| KER-IBD-006 | Rollback | The prior known-good image and a private descriptor checkpoint remain available through the rollback window. The checkpoint supplies prior gateway values; rollback must not restore an old whole descriptor over unrelated later changes. Rollback uses the same service-specific identity checks and does not mutate data or remove unrelated resources. | Descriptor/image-ID checkpoint and rollback runbook evidence. |
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
