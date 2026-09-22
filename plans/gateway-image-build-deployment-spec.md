# Gateway Image Build and Deployment Specification

- Specification: `SPEC-HLLM-IMAGE-DEPLOYMENT-001`.
- Status: Active; local-build/local-deployment lifecycle.
- Decision record: [ADR-HLLM-028](../docs/adr/ADR-HLLM-028-local-image-build-deployment.md).
- Requirements: [Gateway image deployment KERs](gateway-image-build-deployment-kers.md).
- Historical publisher source: [compressed reference bundle](../docs/archive/README.md).

## 1. Purpose and scope

Define how Harden LLM's gateway image is built, identified, deployed, verified,
and rolled back on the existing single production Docker host. The build source
is the exact merged commit checked out by a recorded successful release run. The
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
3. The existing release gates own source acceptance. A specific successful
   release workflow run and attempt own the candidate SHA; a moving branch name
   or a different workflow run cannot substitute for that evidence. No
   registry-only test gate is active.
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

1. Select a specific completed attempt of `test-hierarchy.yml` whose
   `browser-free release` job and `make test-release` step both succeeded. Read
   that attempt's full 40-character `headSha`; this exact tested commit is the
   candidate source SHA. Record the run ID, attempt, URL, and SHA. Fetch
   `origin/main` and require the SHA to be an ancestor of it. Do not replace the
   recorded SHA with the current value of a moving branch.
2. Require the recorded release attempt to pass before building on the
   production host. Record any other exact-SHA checks selected by repository
   policy; a path-filtered or otherwise unselected check is `not applicable`,
   not evidence to borrow from another SHA. A successful
   fast-only run with the release job skipped is not release certification. A
   later documentation-only commit can be a valid candidate when it is the
   exact source tested by the accepted release run; documentation changes alone
   do not require a new image or permit relabelling an existing image. Do not
   run the service-heavy release suite against the live production daemon. Do
   not raise test or provider timeouts to compensate for failures; inspect the
   runner reports and resolve the actual failing task.

   Fetch the trusted branch, record the SHA that the dispatch should select,
   then dispatch a future release run only when a release is intended. Require
   the run-specific URL returned by `gh`; do not discover the candidate as the
   latest run:

   ```bash
   if ! git fetch --no-tags origin \
     '+refs/heads/main:refs/remotes/origin/main'; then
     printf 'Could not fetch the trusted main branch; stop.\n' >&2
     exit 1
   fi
   if ! HLLM_DISPATCH_SHA="$(git rev-parse origin/main)"; then
     printf 'Could not resolve the fetched main branch; stop.\n' >&2
     exit 1
   fi
   if [[ ! "$HLLM_DISPATCH_SHA" =~ ^[0-9a-f]{40}$ ]]; then
     printf 'The fetched main SHA is not a full lowercase commit ID; stop.\n' >&2
     exit 1
   fi
   if ! HLLM_DISPATCH_OUTPUT="$(gh workflow run test-hierarchy.yml \
     --repo prls-co/harden-llm --ref main --field suite=release)"; then
     printf 'The release workflow dispatch failed; stop.\n' >&2
     exit 1
   fi
   printf '%s\n' "$HLLM_DISPATCH_OUTPUT"
   if [[ "$HLLM_DISPATCH_OUTPUT" =~ ^[[:space:]]*https://github\.com/prls-co/harden-llm/actions/runs/([0-9]+)[[:space:]]*$ ]]; then
     HLLM_RELEASE_RUN_ID="${BASH_REMATCH[1]}"
   else
     printf 'The dispatch did not return an unambiguous run URL; stop.\n' >&2
     exit 1
   fi
   ```

   After it completes, inspect that exact workflow file and attempt:

   ```bash
   (
     set -euo pipefail
     : "${HLLM_RELEASE_RUN_ID:?Set the recorded release run ID}"
     : "${HLLM_DISPATCH_SHA:?Set the full pre-dispatch main SHA}"
     HLLM_RELEASE_API_JSON="$(gh api \
       "repos/prls-co/harden-llm/actions/runs/$HLLM_RELEASE_RUN_ID")"
     jq -e --arg expected_sha "$HLLM_DISPATCH_SHA" '
       .path == ".github/workflows/test-hierarchy.yml" and
       .event == "workflow_dispatch" and
       .head_branch == "main" and
       (.head_sha | test("^[0-9a-f]{40}$")) and
       .status == "completed" and
       .conclusion == "success" and
       (.run_attempt | type == "number" and . >= 1) and
       .head_sha == $expected_sha
     ' <<<"$HLLM_RELEASE_API_JSON" >/dev/null
     HLLM_RELEASE_RUN_ATTEMPT="$(jq -r .run_attempt \
       <<<"$HLLM_RELEASE_API_JSON")"
     HLLM_RELEASE_API_SHA="$(jq -r .head_sha \
       <<<"$HLLM_RELEASE_API_JSON")"
     HLLM_RELEASE_API_URL="$(jq -r .html_url \
       <<<"$HLLM_RELEASE_API_JSON")"
     if [[ "$HLLM_RELEASE_API_URL" != \
           "https://github.com/prls-co/harden-llm/actions/runs/$HLLM_RELEASE_RUN_ID" ]]; then
       printf 'The API response URL does not match the recorded run ID.\n' >&2
       exit 1
     fi
     HLLM_RELEASE_RUN_JSON="$(gh run view "$HLLM_RELEASE_RUN_ID" \
       --attempt "$HLLM_RELEASE_RUN_ATTEMPT" \
       --json attempt,workflowName,event,headBranch,headSha,status,conclusion,url,jobs)"
     jq -e --argjson attempt "$HLLM_RELEASE_RUN_ATTEMPT" \
       --arg expected_sha "$HLLM_RELEASE_API_SHA" \
       --arg expected_url "$HLLM_RELEASE_API_URL" '
       .attempt == $attempt and
       .workflowName == "Harden-LLM test hierarchy" and
       .event == "workflow_dispatch" and
       .headBranch == "main" and
       .status == "completed" and
       .conclusion == "success" and
       .headSha == $expected_sha and
       .url == $expected_url and
       ([.jobs[] |
         select(.name == "browser-free release" and .conclusion == "success") |
         .steps[] |
         select(.name == "Run make test-release" and .conclusion == "success")]
        | length) == 1
     ' <<<"$HLLM_RELEASE_RUN_JSON" >/dev/null
     HLLM_RELEASE_SHA="$(jq -r .headSha <<<"$HLLM_RELEASE_RUN_JSON")"
     HLLM_RELEASE_URL="$(jq -r .url <<<"$HLLM_RELEASE_RUN_JSON")"
     printf 'Accepted release run %s attempt %s at %s for %s\n' \
       "$HLLM_RELEASE_RUN_ID" "$HLLM_RELEASE_RUN_ATTEMPT" \
       "$HLLM_RELEASE_URL" "$HLLM_RELEASE_SHA"
   )
   ```

   Preserve the recorded values outside the subshell for the later build step,
   or set them again from the accepted evidence. Never select an arbitrary
   latest successful run.
3. Set `HLLM_RELEASE_SHA` from the accepted release-run evidence, then use the
   following fail-fast Bash subshell. Run the subshell directly; do not place it
   in an `if`, `&&`, or `||` condition that changes Bash's `errexit` behavior.
   Do not run two builds for the same release tag concurrently.

   <!-- gateway-local-build:start -->
   ```bash
   (
     set -euo pipefail

     : "${HLLM_RELEASE_SHA:?Set the accepted release-run head SHA}"
     HLLM_DOCKER_CONTEXT="${HLLM_DOCKER_CONTEXT:-default}"
     case "$HLLM_RELEASE_SHA" in
       (????????????????????????????????????????)
         if [[ ! "$HLLM_RELEASE_SHA" =~ ^[0-9a-f]{40}$ ]]; then
           printf 'Release SHA must be 40 lowercase hexadecimal characters.\n' >&2
           exit 1
         fi
         ;;
       (*)
         printf 'Release SHA must be 40 lowercase hexadecimal characters.\n' >&2
         exit 1
         ;;
     esac

     HLLM_IMAGE_TAG="harden-llm-gateway:release-$HLLM_RELEASE_SHA"
     git fetch --no-tags origin \
       '+refs/heads/main:refs/remotes/origin/main'
     git merge-base --is-ancestor "$HLLM_RELEASE_SHA" origin/main
     docker --context "$HLLM_DOCKER_CONTEXT" info --format '{{.ServerVersion}}' \
       >/dev/null

     HLLM_EXISTING_IMAGE_IDS="$(docker --context "$HLLM_DOCKER_CONTEXT" \
       image ls --quiet --no-trunc --filter "reference=$HLLM_IMAGE_TAG")"
     if [[ -n "$HLLM_EXISTING_IMAGE_IDS" ]]; then
       printf 'Release tag already exists; verify and reuse it separately.\n' >&2
       exit 1
     fi

     HLLM_BUILD_PARENT="$(mktemp -d /var/tmp/harden-llm-build.XXXXXX)"
     printf 'Owned diagnostic build directory: %s\n' "$HLLM_BUILD_PARENT" >&2
     HLLM_BUILD_ROOT="$HLLM_BUILD_PARENT/source"
     git worktree add --detach "$HLLM_BUILD_ROOT" "$HLLM_RELEASE_SHA"

     HLLM_WORKTREE_HEAD="$(git -C "$HLLM_BUILD_ROOT" rev-parse HEAD)"
     if [[ "$HLLM_WORKTREE_HEAD" != "$HLLM_RELEASE_SHA" ]]; then
       printf 'Detached worktree does not match the accepted release SHA.\n' >&2
       exit 1
     fi
     HLLM_WORKTREE_STATUS="$(git -C "$HLLM_BUILD_ROOT" \
       status --porcelain --untracked-files=all)"
     if [[ -n "$HLLM_WORKTREE_STATUS" ]]; then
       printf 'Detached release worktree is not clean.\n' >&2
       exit 1
     fi

     docker --context "$HLLM_DOCKER_CONTEXT" build \
       --file "$HLLM_BUILD_ROOT/Dockerfile" \
       --platform linux/amd64 \
       --build-arg "VERSION=$HLLM_RELEASE_SHA" \
       --build-arg "REVISION=$HLLM_RELEASE_SHA" \
       --tag "$HLLM_IMAGE_TAG" "$HLLM_BUILD_ROOT"

     HLLM_IMAGE_METADATA="$(docker --context "$HLLM_DOCKER_CONTEXT" \
       image inspect \
       --format '{{.Id}}|{{.Os}}|{{.Architecture}}|{{index .Config.Labels "org.opencontainers.image.source"}}|{{index .Config.Labels "org.opencontainers.image.revision"}}|{{index .Config.Labels "org.opencontainers.image.version"}}' \
       "$HLLM_IMAGE_TAG")"
     HLLM_METADATA_SEPARATORS="${HLLM_IMAGE_METADATA//[^|]/}"
     if [[ "$HLLM_IMAGE_METADATA" == *$'\n'* ||
           "${#HLLM_METADATA_SEPARATORS}" -ne 5 ]]; then
       printf 'Built image metadata is malformed.\n' >&2
       exit 1
     fi
     IFS='|' read -r HLLM_IMAGE_ID HLLM_IMAGE_OS HLLM_IMAGE_ARCH \
       HLLM_IMAGE_SOURCE \
       HLLM_IMAGE_REVISION HLLM_IMAGE_VERSION <<<"$HLLM_IMAGE_METADATA"
     if [[ ! "$HLLM_IMAGE_ID" =~ ^sha256:[0-9a-f]{64}$ ]]; then
       printf 'Built image ID is not a sha256 identity.\n' >&2
       exit 1
     fi
     if [[ "$HLLM_IMAGE_OS" != "linux" || "$HLLM_IMAGE_ARCH" != "amd64" ]]; then
       printf 'Built image platform is not linux/amd64.\n' >&2
       exit 1
     fi
     if [[ "$HLLM_IMAGE_SOURCE" != "https://github.com/prls-co/harden-llm" ]]; then
       printf 'Built image source label is incorrect.\n' >&2
       exit 1
     fi
     if [[ "$HLLM_IMAGE_REVISION" != "$HLLM_RELEASE_SHA" ||
           "$HLLM_IMAGE_VERSION" != "$HLLM_RELEASE_SHA" ]]; then
       printf 'Built image revision/version does not match the release SHA.\n' >&2
       exit 1
     fi

     HLLM_VERSION_OUTPUT="$(docker --context "$HLLM_DOCKER_CONTEXT" run \
       --pull never --network none --rm "$HLLM_IMAGE_TAG" version)"
     if [[ "$HLLM_VERSION_OUTPUT" != "$HLLM_RELEASE_SHA" ]]; then
       printf 'Gateway version output does not match the release SHA.\n' >&2
       exit 1
     fi

     HLLM_FINAL_WORKTREE_STATUS="$(git -C "$HLLM_BUILD_ROOT" \
       status --porcelain --untracked-files=all)"
     if [[ -n "$HLLM_FINAL_WORKTREE_STATUS" ]]; then
       printf 'Release worktree changed during the build.\n' >&2
       exit 1
     fi
     git worktree remove "$HLLM_BUILD_ROOT"
     rmdir "$HLLM_BUILD_PARENT"
     printf 'Accepted local image: %s %s %s\n' \
       "$HLLM_RELEASE_SHA" "$HLLM_IMAGE_TAG" "$HLLM_IMAGE_ID"
   )
   ```
   <!-- gateway-local-build:end -->

   If any command fails after allocation, keep the printed owned worktree and
   local image for diagnosis. Do not force-remove the worktree or automatically
   delete the image. If the release tag already exists, do not build over it;
   validate and reuse it only through a separate operator review of its exact
   image ID, platform, source/revision/version labels, and isolated `version`
   output. The version probe uses `--pull never`; the source build can still
   require network access for build dependencies.
4. Before deployment, inspect the image ID, OS/architecture, and OCI
   source/revision/version labels; run the isolated gateway `version` command.
   Require the full SHA in the version output and labels, and require the image
   ID to be the one recorded in the descriptor candidate.
5. Keep at least the current and prior known-good gateway images until the
   rollback window is deliberately closed. Do not prune images or volumes as
   part of this workflow.

The release identity inputs are intentionally the exact merged SHA from the
accepted release run. A release that changes the Dockerfile, Go dependencies,
build flags, or gateway code must complete the release gate before promotion.
A documentation-only commit does not retroactively change the identity of an
already-built or already-deployed image.

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
   --expected-release <full-source-sha>`. The command verifies that the desired
   local tag resolves to the expected image ID and that the desired version and
   release metadata agree. Interpret its exit status as follows:

   | Operation | Exit | Meaning and required action |
   | --- | ---: | --- |
   | `check` | `0` | Runtime was verified and the selected candidate is already equivalent; do not apply. |
   | `check` | `2` | Differences were found. Review every reported field. This can represent an expected old running release or a blocking desired-candidate/configuration problem. |
   | `check` | `1` | Input, descriptor, Compose/runtime inspection, or command execution failed; stop and resolve the error. |
   | `apply` | `0` | The selected service was already equivalent or converged successfully. |
   | `apply` | `1` | Application was blocked or failed; `apply` never returns `2`. Inspect current runtime state before rollback or another attempt. |

   Exit `2` from `check` does not authorize application by itself. Wrong or
   unavailable desired images, wrong desired release metadata, unapproved
   fields, and unexplained configuration changes are blockers. Expected
   running-image/release differences can proceed only after every field is
   reviewed as part of this gateway-only promotion. Do not join `check` and
   `apply` with `&&` or `||` because that loses this review boundary.
5. After that review, run the matching explicit `apply` command only for
   `harden-llm-gateway` and the same full source SHA. Never apply an unreviewed
   complete Compose graph as part of a gateway release. Candidate desired
   mismatches and difference kinds outside the descriptor allowlist are
   rejected before Compose `up`. If `apply` exits `1`, do not retry blindly:
   Compose may already have run before a post-apply convergence failure. Inspect
   the scoped runtime and use the rollback procedure when convergence failed.
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
2. Use the private descriptor checkpoint as evidence for the prior gateway
   values. Copy only the gateway image, expected image ID, and gateway release
   identity fields into the current descriptor. Do not replace later unrelated
   service or configuration changes with an old whole-descriptor checkpoint.
3. Run the service-specific scoped `check`, review its differences using the
   exit table above, then run scoped `apply` with the prior full source SHA.
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

- The current Docker host is the only verified, immediately usable location
  for these local image layers recorded by the project. Losing the host or its
  Docker data can require a source rebuild; this is not off-host disaster
  recovery.
- The prior private GHCR package was deleted on 2026-09-22 after owner approval
  and verification that production used the matching local image. It is no
  longer available for pulls while deletion remains in effect. GitHub
  [documents conditional restoration](https://docs.github.com/en/packages/learn-github-packages/deleting-and-restoring-a-package)
  within 30 days only while the same package namespace and version remain
  available and the operator has the required access. No restore was attempted
  or certified here, so that limited administrative option is not a backup or
  the supported release path. Keep the host-local immutable image and use the
  protected production descriptor for recreations; rebuild from tested source
  if that image is lost.
- A single-host local image lifecycle is not a multi-host promotion system.
- Registry publishing may be proposed again only with a concrete need such as
  multiple deploy targets, host/build separation, tested off-host image
  recovery, or reproducible promotion between environments. Reopening it
  requires a new ADR amendment, ownership/cost review, access controls,
  retention/restore policy, and scoped tests.
