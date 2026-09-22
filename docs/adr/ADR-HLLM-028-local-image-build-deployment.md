# ADR-HLLM-028: Local Gateway Image Build and Deployment

- Status: Accepted and implemented.
- Date: 2026-09-22.
- Requirements: KER-IBD-001 through KER-IBD-010 in `plans/gateway-image-build-deployment-kers.md`.
- Specification: `plans/gateway-image-build-deployment-spec.md`.
- Historical publisher source: `docs/archive/harden-llm-ghcr-publisher-reference-6887fcd.tar.gz`.

## Context

The gateway image was first built locally and deployed by exact source SHA.
P04 later added a manual GitHub Actions publisher, a publisher-only contract
test, GHCR package permissions, provenance handling, and registry-specific
release documentation. One private image from source
`6887fcd8146961dc64598dd7a236e7a9fc522c9c` was published and deployed by
digest. The production descriptor consequently referred to that GHCR digest
until the later local-reference transition, while the same image was already
present on the existing Docker host.

The current deployment is one actively developed application on one existing
Docker host. The operator is willing to build from source there. There is no
current multi-host distribution or off-host image-restore requirement that
justifies a standing publisher, its workflow permissions, registry gate, and
ongoing tests. The user's simplicity decision is to remove that machinery from
the active codebase while retaining enough evidence to recreate it if the
operating model changes.

## Decision

1. Use the existing root Dockerfile to build the exact merged source SHA
   checked out by a recorded successful release workflow run and attempt on
   the target production Docker host. Tag the result
   `harden-llm-gateway:release-<full-source-sha>` and record its exact image ID.
   A later documentation-only commit does not relabel an existing image; it is
   used as image identity only when that exact commit is the accepted release
   run's tested source and a new image is actually required.
2. Use the existing scoped `production-config` commands to check and apply
   only `harden-llm-gateway`. Keep the target local image and a prior
   known-good local image through the rollback period. The release lifecycle
   is specified by `SPEC-HLLM-IMAGE-DEPLOYMENT-001`.
3. Remove the GHCR publisher workflow and publisher-specific test from active
   execution. Retire REQ-353/TEST-283 from active requirement/test selection;
   retain their IDs and explain their historical status so identifiers are
   never silently reused.
4. Preserve the exact publisher workflow, its contract test, and the Dockerfile
   as a checksummed compressed reference bundle from commit
   `6887fcd8146961dc64598dd7a236e7a9fc522c9c`. The bundle is archival input,
   not an executable workflow or test fixture.
5. Keep Dockerfile OCI source, revision, and version labels. The version/revision
   identity is used by existing candidate/deployment checks and supports
   source attribution independent of any registry.
6. At the initial 2026-09-22 retirement, leave the already-published private
   GHCR package untouched and unused. A later owner-approved cleanup deleted
   it; see the dated follow-up below. The package was never treated as a
   certified disaster-recovery system.
7. Keep the production descriptor, credentials, image store, and service state
   on the host. Do not add cleanup, pruning, migration, new secrets, provider
   calls, or browser checks as part of this transition.

## Alternatives considered

| Option | Decision |
| --- | --- |
| Keep GHCR publisher as the default deployment path | Rejected for the current one-host active-development model; its distribution/recovery value does not justify an additional active release subsystem. |
| Build locally but retain publisher workflow as a fallback | Rejected for now; a dormant path still carries permissions, tests, and maintenance. The exact source is kept in a compact archive and Git history. |
| Delete the existing private GHCR package | Not selected in the initial retirement; subsequently authorized and completed on 2026-09-22 after verifying the local production image and descriptor. |
| Add a generic OCI/registry abstraction or another build scheduler | Rejected; there is one image and one host, and existing Docker/Compose/config tools already own the lifecycle. |
| Remove all OCI labels | Rejected; existing release checks consume image version/revision metadata, and labels are useful for local provenance. |

## Consequences and risks

- The release path has fewer active files, permissions, gates, and registry
  assumptions. Builds consume resources on the production host and may take
  longer than using a prebuilt remote image.
- The target host becomes the sole retained image store unless an operator
  separately backs it up. Host loss can require a source rebuild. This ADR does
  not improve disaster recovery or certify host capacity.
- A same-source build is expected to carry matching version/revision labels
  and platform, but byte-identical layers/digests have not been demonstrated.
- The original GHCR publication remains a historical fact, but the package was
  deleted in the follow-up below. The production host's local image is now the
  only verified, immediately usable copy of that image artifact recorded by
  the project.
- Reconsider this decision if multiple hosts, independent build/deploy
  environments, image restore requirements, or build-resource constraints
  emerge. A change requires a new ADR amendment, not silently restoring the
  old workflow.

## Follow-up — GHCR package cleanup (2026-09-22)

After the initial retirement, the owner explicitly approved deleting the
dormant private package. Immediately before deletion, GitHub reported the
package as private with three versions: one source/run-qualified tag and two
untagged versions. The production descriptor selected the local tag
`harden-llm-gateway:release-6887fcd8146961dc64598dd7a236e7a9fc522c9c`, whose
image ID matched both the configured expected image and the running container.
The gateway was healthy.

The package `prls-co/harden-llm-gateway` was deleted through the GitHub Packages
API. A subsequent package lookup returned HTTP 404 (`Package not found`). This
removed the tagged GHCR digest
`sha256:036d82749a1e5a29e36c848d5fca955c4b416858c9ea272f2cf1b4428210905b` and
its two untagged versions. No production service, local image, descriptor,
volume, or data was changed, and no restart was performed. The archive bundle
preserves publisher source only; it is not a backup of the deleted image.

The existing running container retains the historical GHCR string in its
creation-time `Config.Image`, but its image object is local and its immutable
image ID matches the production descriptor. Future recreation must use the
local-image descriptor path; the deleted digest is unavailable for pulls while
deletion remains in effect.

GitHub [documents a conditional restoration window](https://docs.github.com/en/packages/learn-github-packages/deleting-and-restoring-a-package):
a deleted package or version can be restored within 30 days only if its
namespace and version have not been reused and the operator has the required
access. No restoration was attempted or certified here, and the exact deletion
timestamp was not recorded. This possibility is not durable image retention or
an active release path.

## Rollback and restoration

Operational rollback means restoring the prior host descriptor and verified
local image using the scoped production-config procedure in the specification.
It does not require registry access.

The deleted package is not a current rollback source. It could become available
only if GitHub's time, namespace/version, and access conditions still hold and
a restoration succeeds; no such restoration has been tested. Rebuild from
tested source if the host-local image is lost.

Reintroducing publication is a separate decision. Review the archived workflow
and test rather than copying them blindly; restore their selectors and
traceability entries from the archive's source context, add a new ADR/amendment
for current security and retention requirements, and run current exact-source
release certification before granting package-write permissions.
