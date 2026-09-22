# Gateway Image Build and Deployment Specification

- Specification: `SPEC-HLLM-IMAGE-DEPLOYMENT-001`.
- Status: Active; local-build/local-deployment lifecycle.
- Decision record: [ADR-HLLM-028](../docs/adr/ADR-HLLM-028-local-image-build-deployment.md).
- Requirements: [Gateway image deployment KERs](gateway-image-build-deployment-kers.md).
- Historical publisher source: [compressed reference bundle](../docs/archive/README.md).

## 1. Purpose and scope

Define how Harden LLM's gateway image is built, identified, deployed, verified,
and rolled back on the existing single production Docker host. The build source
is the exact application-bearing commit that passed the release gates. The
runtime image is retained locally on that host and addressed by a never-reused
full-source-SHA tag.

The active lifecycle does not publish or pull this image from a registry. It
does not introduce a second build system, deployment scheduler, image service,
database, or CI release path. The existing Dockerfile, Make gates, Compose
configuration, production-config command, and host remain the owners.

This specification does not claim high availability, off-host disaster
recovery, bit-for-bit reproducible image layers, a production capacity result,
or a supported multi-host rollout. Those require separate evidence and a new
decision.

## 2. Current artifact and production identity

The 2026-09-22 retirement transition keeps the already-running application
image and changes only its local Docker reference in the protected descriptor.
The image itself is not rebuilt or changed by this documentation and publisher
cleanup.

| Field | Value |
| --- | --- |
| Application-bearing source SHA | `6887fcd8146961dc64598dd7a236e7a9fc522c9c` |
| Runtime platform | `linux/amd64` |
| Existing Docker image ID | `sha256:036d82749a1e5a29e36c848d5fca955c4b416858c9ea272f2cf1b4428210905b` |
| Local immutable-by-policy tag | `harden-llm-gateway:release-6887fcd8146961dc64598dd7a236e7a9fc522c9c` |
| Production Docker context | `default` on the existing host |
| Compose service/container | `harden-llm-gateway` / `harden-llm-harden-llm-gateway-1` |
| Source, revision, and version labels | `https://github.com/prls-co/harden-llm`, full source SHA, full source SHA |

Docker image IDs are recorded for exact local identity. Rebuilding the same
source is required to preserve source/version/platform identity, but this
specification does not assert that separate builds produce identical image
IDs or OCI digests; that has not been certified.

## 3. Active ownership and boundaries

1. Git `main` owns source and versioned build inputs.
2. The root `Dockerfile` owns the pinned Go builder and image construction.
3. The existing release gates own source acceptance; no registry-only test
   gate is active.
4. The target Docker daemon owns the local image and rollback image.
5. `/home/kirill/.config/harden-llm/production.json` owns non-secret host
   deployment identity. It remains outside Git, mode `0600`, and contains no
   provider or production credential values.
6. `scripts/production-config.mjs` owns scoped Compose resolution, image-ID
   checks, runtime comparison, and application. It remains read-only by
   default; `apply` requires an explicit service scope and release SHA.
7. This specification, its KERs, ADR-HLLM-028, the release record, and the
   checksummed reference archive own the durable lifecycle decision and
   restoration information.

The frontend image, Postgres, Garage, telemetry services, configuration
sources, secrets, profiles, sessions, and persistent volumes are outside this
gateway-only lifecycle change.

## 4. Build contract for a future application release

1. Select the full 40-character application-bearing source SHA from merged
   `main`. The source tree used as the Docker build context must correspond to
   that SHA and contain no uncommitted changes. Do not derive the image release
   from a later documentation-only commit.
2. Require the exact-SHA hosted `make test-release` and normal main-branch
   checks to pass before building on the production host. Do not run the
   service-heavy release suite against the live production daemon. Do not raise
   test or provider timeouts to compensate for failures; inspect the runner
   reports and resolve the actual failing task. For the current hosted
   workflow, dispatch its existing release suite with
   `gh workflow run test-hierarchy.yml --ref main --field suite=release`, then
   verify the workflow run's checked-out SHA is exactly the source SHA selected
   for the image. A run against a different `main` head is not evidence for
   this image.
3. Use a detached worktree of the selected merged SHA as the Docker build
   context. The following is the current command shape; use the already
   recorded full application SHA, and stop if the tag exists with a different
   image ID:

   ~~~sh
   RELEASE_SHA=REPLACE_WITH_FULL_CERTIFIED_SHA
   BUILD_ROOT="/var/tmp/harden-llm-build-$RELEASE_SHA"
   IMAGE_TAG="harden-llm-gateway:release-$RELEASE_SHA"
   git fetch origin main
   git merge-base --is-ancestor "$RELEASE_SHA" origin/main
   git worktree add --detach "$BUILD_ROOT" "$RELEASE_SHA"
   if docker image inspect "$IMAGE_TAG" >/dev/null 2>&1; then
     echo "Release tag already exists; verify and reuse it, never overwrite it." >&2
     exit 1
   fi
   docker build --context "$BUILD_ROOT" --file "$BUILD_ROOT/Dockerfile" \
     --platform linux/amd64 \
     --build-arg VERSION="$RELEASE_SHA" \
     --build-arg REVISION="$RELEASE_SHA" \
     --tag "$IMAGE_TAG" "$BUILD_ROOT"
   docker image inspect --format '{{.Id}} {{.Os}}/{{.Architecture}} {{index .Config.Labels "org.opencontainers.image.revision"}} {{index .Config.Labels "org.opencontainers.image.version"}}' "$IMAGE_TAG"
   docker run --rm --network none "$IMAGE_TAG" version
   ~~~

   If a tag already exists, do not build over it. Validate its exact image ID,
   OS/architecture, version/revision labels, and isolated `version` output
   against the approved descriptor; reuse it only if every identity matches.
   Remove the temporary worktree only after the image is validated and the
   exact worktree path is confirmed to have no uncommitted changes.
4. Before deployment, inspect the image ID, OS/architecture, and OCI
   source/revision/version labels; run the isolated gateway `version` command.
   Require the full SHA in the version output and labels, and require the image
   ID to be the one recorded in the descriptor candidate.
5. Keep at least the current and prior known-good gateway images until the
   rollback window is deliberately closed. Do not prune images or volumes as
   part of this workflow.

The release identity inputs are intentionally the application-bearing SHA,
not an arbitrary documentation commit. A release that changes Dockerfile,
Go dependency, build flags, or gateway code is an application build and must
complete the release gate before promotion.

## 5. Deployment contract

1. Confirm target Docker context and exact image availability on that same
   daemon. A local image on another machine is not a production artifact.
2. Capture a mode-restricted copy and checksum of the current host descriptor
   before changing it. Preserve its owner and mode.
3. Set only the gateway `serviceImageOverrides` reference to the new local
   immutable-by-policy tag, the gateway `expectedImage` to the exact local
   image ID, and the gateway's release identity to the full application SHA.
   Preserve all other descriptor values and all unrelated services.
4. Run `node scripts/production-config.mjs check --descriptor
   /home/kirill/.config/harden-llm/production.json --services harden-llm-gateway
   --expected-release <full-source-sha>`. Resolve any difference before
   applying. The command verifies that the desired local tag resolves to the
   expected image ID and that release metadata agrees.
5. Run the matching `apply` command only for `harden-llm-gateway` and the same
   full source SHA. Never apply an unreviewed complete Compose graph as part of
   a gateway release.
6. Re-run `check`; require `equivalent`. Verify the gateway is running and
   healthy with zero unexpected restarts, its container image ID and release
   environment match the selected build, API `/healthz` and `/readyz` return
   HTTP 200, and web `/healthz` and `/login` remain HTTP 200.
7. Run only the documented read-only artifact inventory probe for the
   post-deploy integrity boundary. Do not invoke browser or paid-provider
   checks automatically.
8. Record branch, source SHA, image tag and ID, descriptor before/after
   checksums, affected service, health/HTTP results, and checks not performed
   in `docs/release-certification.md`.

If a descriptor change selects the already-running exact image and every
runtime field remains equivalent, no container recreation is required. That
is a successful reference transition, not a claim that a new application
image was deployed.

## 6. Rollback

1. Stop if the intended prior image is not present on the target daemon or its
   image ID/version cannot be verified.
2. Restore the private descriptor checkpoint or edit only the same gateway
   image and release identity fields to the exact prior values.
3. Run scoped `check`, then scoped `apply` with the prior full source SHA.
4. Verify gateway health, image ID, release identity, API and web probes, and
   the read-only artifact inventory again.
5. Retain the failed release image and diagnostics until the cause is
   understood. Do not delete volumes, databases, sessions, or unrelated
   containers during rollback.

## 7. Verification and completion evidence

The minimum evidence for a repository-only lifecycle cleanup is:

- the fast browser-free gate, manifest/tier policy verifier, and whitespace
  checks pass;
- no active workflow, task, or code path requires GHCR publication;
- the compressed historic publisher reference matches the recorded SHA-256;
- the production descriptor resolves the gateway's local tag to its recorded
  image ID and is equivalent to the running service;
- the public health/readiness checks and read-only artifact inventory pass;
- `main` contains the documentation and retirement changes.

Application source changes still use the application's release gates. A
docs-only or test-selection cleanup does not require rebuilding an unchanged
gateway image.

## 8. Risks and reconsideration triggers

- The current Docker host is the only retained copy of local image layers.
  Losing the host or its Docker data can require a source rebuild; this is not
  off-host disaster recovery.
- The prior GHCR package remains private but is not a supported build or
  deployment dependency. Its deletion is a separate package-administration
  decision and is not part of repository cleanup.
- A single-host local image lifecycle is not a multi-host promotion system.
- Registry publishing may be proposed again only with a concrete need such as
  multiple deploy targets, host/build separation, tested off-host image
  recovery, or reproducible promotion between environments. Reopening it
  requires a new ADR amendment, ownership/cost review, access controls,
  retention/restore policy, and scoped tests.
