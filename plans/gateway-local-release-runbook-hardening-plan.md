# Gateway Local Release Runbook Hardening Plan

## 1. Objective, status, and implementation boundary

- Plan: `PLAN-HLLM-LOCAL-RELEASE-RUNBOOK-001`.
- Date: 2026-09-22.
- Status: complete; P00-P05 implemented and verified.
- Reviewed source: `30adad594ec3ed61b186a32a7b72636e0e01fb51`.
- Audience: an implementer such as GPT-5.6 Luna running in Codex CLI.
- Governing instructions: [AGENTS.md](../AGENTS.md) and the complete
  [testing guidelines](../docs/liveview-go-testing-guidelines.md).
- Existing specification: [SPEC-HLLM-IMAGE-DEPLOYMENT-001](gateway-image-build-deployment-spec.md).
- Existing requirements: [KER-IBD-001 through KER-IBD-010](gateway-image-build-deployment-kers.md).
- Existing decision: [ADR-HLLM-028](../docs/adr/ADR-HLLM-028-local-image-build-deployment.md).

Correct three defects in the instructions for future releases and one factual
overstatement about deleted-package recovery. Keep the current local build,
Dockerfile, hosted release workflow, and scoped production deployment tool.
The intended deliverable is a documentation correction with meaningful checks
of the executable examples. No new production code or permanent test framework
is expected.

This plan is not a request to perform a gateway release. Commands that build
images, dispatch release CI, edit a production descriptor, apply a deployment,
or restore a package are instructions to document for a future operator. Do
not execute them while implementing these documentation corrections. Validate
the build example with intercepted commands as specified in Section 6.

Do not restore publishing, add a registry abstraction, change OpenAPI, modify
the Dockerfile or Compose graph, add a workflow input, increase timeouts, change
credentials, prune Docker resources, or restart production. Browser and live
provider checks are unnecessary for this work.

## 2. Confirmed findings and retained evidence

These observations came from the preceding review. They are baseline evidence,
not implementation results; refresh only the checks needed for a new claim.

| Finding | Evidence | Required correction |
| --- | --- | --- |
| The build example does not stop after a failed prerequisite. | With Git and Docker intercepted, the documented ancestry check returned failure, but the example reached the simulated build and exited zero. | Fail immediately; verify the checkout before building; prove failures prevent later commands. |
| Source selection can disagree with workflow selection. | The spec excludes later documentation commits, while the documented dispatch uses `--ref main` and checkout uses the workflow's commit. | Use the exact merged commit from a specific successful release run. |
| The pre-deployment wording forbids ordinary upgrade differences. | Spec Section 5 says to resolve every difference before applying. `runApply()` intentionally accepts approved differences from an older running release. | Distinguish invalid candidate/configuration state from reviewed upgrade differences. |
| Recovery wording is too absolute. | GitHub documents a conditional 30-day package restoration window. | Explain that window without treating it as a permanent backup. |

Retained operational evidence from the review:

- Gateway `production-config check --expected-release` returned `equivalent`.
- Gateway was healthy with zero restarts; current and prior rollback images
  were present locally.
- `runApply()` uses `--no-build --no-deps --pull never` and verifies convergence.
- `node --test scripts/test/production_config_test.mjs` passed 13/13 cases.
- The archived publisher bundle matched SHA-256
  `60b771ebdb807f306557f383208d6c2c30e6b6cb87ebfc0337f7f8001bb6c785`.

The current deployed source remains
`6887fcd8146961dc64598dd7a236e7a9fc522c9c`. A documentation correction must
not relabel that existing image with the documentation commit's SHA.

## 3. Files and ownership

| File | Intended work |
| --- | --- |
| [gateway-image-build-deployment-spec.md](gateway-image-build-deployment-spec.md) | Primary operational instructions: source selection, executable build example, deployment interpretation, recovery wording, and proportionate verification. |
| [gateway-image-build-deployment-kers.md](gateway-image-build-deployment-kers.md) | Clarify existing KER-IBD-001, KER-IBD-005, and KER-IBD-006 where necessary; preserve all identifiers. |
| [ADR-HLLM-028](../docs/adr/ADR-HLLM-028-local-image-build-deployment.md) | Add a short dated clarification of source identity and conditional package restoration. Correct present-tense context that describes the old descriptor as current. |
| [release-certification.md](../docs/release-certification.md) | Append actual implementation evidence and correct absolute recovery wording in the latest cleanup record. Preserve historical publication/deployment results. |
| [production-scale-efficiency-plan.md](production-scale-efficiency-plan.md) | Add a brief reference in Section 14 to the clarified lifecycle; do not rewrite historical P00-P04 evidence. |
| [production-config.example.json](../config/production-config.example.json) | Align the gateway allowlist with the existing identity change required by a real release. |
| [environment.md](../docs/environment.md) and [self-hosting.md](../docs/self-hosting.md) | Make candidate checks service-specific while retaining whole-descriptor equivalence checks. |
| This plan | Update step status, verification results, problems, and remaining risks as work proceeds. |

Read these implementation owners to understand existing behavior; no changes
are planned in them:

- [production-config.mjs](../scripts/production-config.mjs):
  `runCheck()`, `candidateDesiredDifferences()`, `runApply()`, and CLI exit codes.
- [production_config_test.mjs](../scripts/test/production_config_test.mjs):
  existing `TEST-233`, `TEST-234`, and `TEST-260` behavior coverage.
- [test-hierarchy.yml](../.github/workflows/test-hierarchy.yml): workflow inputs,
  checkout behavior, the `browser-free release` job, and path exclusions.
- [main.go](../cmd/harden-llm-gateway/main.go): `version` prints the version
  string followed by a newline; it can be compared exactly with the selected SHA.
- [Dockerfile](../Dockerfile): actual version/revision build arguments and labels.
- [environment.md](../docs/environment.md) and
  [self-hosting.md](../docs/self-hosting.md): existing deployment semantics.

If another current document contradicts the corrected rule, make a small
targeted clarification and record the additional file. Leave historical facts
and unrelated documentation alone. If an application-code defect becomes
necessary to fix, record the finding and reassess that scope separately.

## 4. Accepted design decisions

### 4.1 One tested source revision

For a future application release, identify a specific completed, successful
run of `test-hierarchy.yml` with the actual `browser-free release` job and its
`make test-release` step successful. Read that run's full `headSha`; this is the
commit to build. Verify it is an ancestor of fetched `origin/main`.

The selected commit may contain only documentation differences relative to
the latest application edit. What matters is that the tested source and build
context match exactly. A documentation change by itself does not require a
new image, deployment, or mutation of an existing image's release identity.

Record the fetched `origin/main` SHA before using the existing future-operator
dispatch command. Capture its run-specific URL and extract that numeric run ID;
stop if `gh` does not return an unambiguous URL. Never discover this candidate
through a “latest run” query:

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
if [[ "$HLLM_DISPATCH_OUTPUT" =~ ^[[:space:]]*https://github\.com/prls-co/harden-llm/actions/runs/([0-9]+)[[:space:]]*$ ]]; then
  HLLM_RELEASE_RUN_ID="${BASH_REMATCH[1]}"
else
  printf 'The dispatch did not return an unambiguous run URL; stop.\n' >&2
  exit 1
fi
```

Document how to record the corresponding run ID and inspect it:

```bash
(
  set -euo pipefail
  : "${HLLM_RELEASE_RUN_ID:?Set the recorded release run ID}"
  : "${HLLM_DISPATCH_SHA:?Set the full pre-dispatch main SHA}"
  HLLM_RELEASE_API_JSON="$(gh api \
    "repos/prls-co/harden-llm/actions/runs/$HLLM_RELEASE_RUN_ID")"
  HLLM_RELEASE_RUN_ATTEMPT="$(jq -r .run_attempt \
    <<<"$HLLM_RELEASE_API_JSON")"
  gh run view "$HLLM_RELEASE_RUN_ID" \
    --attempt "$HLLM_RELEASE_RUN_ATTEMPT" \
    --json workflowName,event,headBranch,headSha,status,conclusion,url,jobs
  printf 'Inspected run attempt: %s\n' "$HLLM_RELEASE_RUN_ATTEMPT"
)
```

Keep all recorded job/step results tied to that attempt. These are future
operator instructions, not commands to dispatch or certify a release during
this documentation task.

Verify workflow identity, event/ref, completion, job/step conclusions, and the
40-character SHA. Cross-check the run-view SHA and URL against the exact API
response and recorded run ID. Overall workflow success alone is insufficient:
a fast-only run with the release job skipped is not release certification. Do
not select an arbitrary latest successful run. Record the specific run ID,
attempt, URL, and SHA so later movement of `main` cannot silently change the
candidate.
Record other checks selected by repository policy at that SHA. Treat a
path-filtered or otherwise unselected check as `not applicable`; do not borrow
a different SHA's result.

No additional workflow input or arbitrary-SHA checkout mechanism is needed.

### 4.2 Fail-fast build example

Keep one canonical executable example in spec Section 4. Mark it with
`<!-- gateway-local-build:start -->` and `<!-- gateway-local-build:end -->`
around a `bash` code fence so the disposable verifier can extract exactly it.

The example must implement this order:

1. Use a Bash subshell with `set -euo pipefail`, protecting the caller's shell
   from `exit` and persistent shell-option changes. Use task-specific names
   such as `HLLM_RELEASE_SHA`, `HLLM_DOCKER_CONTEXT`, and `HLLM_BUILD_ROOT`.
2. Require the selected SHA to match `^[0-9a-f]{40}$`. It is an input taken
   from the verified release run, not freshly derived from a moving `HEAD`.
3. Fetch `main`; require `git merge-base --is-ancestor` to succeed.
4. Select the production descriptor's documented Docker context explicitly
   on every Docker invocation. The current context is `default`. Check daemon
   reachability before interpreting any image-availability result.
5. Check for the exact release tag using an operation that distinguishes a
   successful empty listing from a Docker error. For example, capture
   `docker --context "$HLLM_DOCKER_CONTEXT" image ls --quiet --no-trunc
   --filter "reference=$HLLM_IMAGE_TAG"` in a standalone assignment. Any
   command error must stop; a nonempty result must stop without overwriting.
   An existing image may be verified and reused in a separate operator step.
6. Only then allocate an owned directory using `mktemp -d` under `/var/tmp`;
   print the allocated path immediately so it is known if a later command
   fails. Create the detached worktree in a previously absent child directory.
7. Capture worktree `rev-parse HEAD` and `status --porcelain --untracked-files=all`
   in separate, checked assignments. Require the exact SHA and an empty
   status before building. A failed status command is not a clean tree.
8. Build using that worktree, explicit `linux/amd64`, and both `VERSION` and
   `REVISION` equal to the selected SHA. Preserve the current Dockerfile.
9. Capture image inspection output and assert a valid image ID, platform,
   OCI source URL, revision, and version. Printing them alone is not a check.
10. Run the isolated version probe with `--pull never --network none --rm`
    on the same explicit context. Capture its output; require exact equality
    with the SHA after command substitution removes the trailing newline.
11. Check worktree status again. Remove only the verified clean worktree using
    `git worktree remove` without `--force`, then `rmdir` its owned empty parent.
    Print the accepted SHA, tag, and image ID only after successful validation.

On failure, keep any allocated worktree and local image for diagnosis and
report the owned worktree path without exposing secrets. Avoid automatic
cleanup traps that could obscure the failure, remove diagnostic evidence, or
run against an uncertain path. Do not build, probe, or report success after a
prerequisite fails. Never use `|| true` to suppress these failures.

Run the subshell directly, not as a condition of `if`, `&&`, or `||`; Bash
can disable expected `errexit` behavior in conditional contexts. Assign
command outputs before testing them so failure status is not hidden inside
`test`, `printf`, `local`, or another successful command.

This remains a one-operator release procedure. A check for an existing tag is
not a concurrency lock; document that two releases must not build the same
tag concurrently. Do not add a scheduler or lock service for this correction.
Build dependencies may still require network access; `--pull never` on the
version probe does not make source builds offline.

### 4.3 Review differences before applying

Document the existing CLI meanings accurately:

| Result of scoped `check` | Meaning | Operator action |
| --- | --- | --- |
| Exit 0, runtime verified, `equivalent` | Desired and running configuration already agree. | No application is needed. |
| Exit 2, listed differences | Comparison completed and found differences. | Review every field; proceed only when candidate identity is valid and all remaining differences belong to the intended approved gateway upgrade. |
| Exit 1 | Invalid input, unavailable dependency, or another configuration error. | Stop and resolve the reported error. |

Exit 2 is not blanket permission to apply. Wrong or unavailable desired
images, wrong desired release metadata, unexplained environment changes, and
unapproved changes remain blockers. Expected old running-image/release
differences are normal during a reviewed upgrade. Preserve the tool's existing
allowed-field checks, repeated resolution checks, and post-apply verification.

Do not put `check && apply` in the future upgrade example: the expected exit 2
would prevent the application. Do not use `check || apply`, which would apply
after errors. Show a separate review boundary and an explicit scoped `apply`;
then require a fresh `check` to exit zero and report `equivalent`.

Keep the gateway and web release identities separate. This gateway procedure
must not use the gateway's SHA as the expected release for both services when
the web image is deliberately at an older revision.

### 4.4 Conditional package restoration

Use [GitHub's package deletion and restoration documentation](https://docs.github.com/en/packages/learn-github-packages/deleting-and-restoring-a-package)
as the source. It documents restoration within 30 days of deletion, provided
the namespace remains available and the operator has the required access.

Clarify that the package was deleted on 2026-09-22 and is unavailable for pulls
unless restoration succeeds. This is an administrative possibility with a
limited window, not a standing artifact backup or a verified restore result.
Do not claim the particular package is currently restorable without checking
the conditions, and do not perform a restoration to test the wording.

The supported long-term recovery path remains the retained local image or a
source rebuild when that image is lost. A rebuilt image may have a different
image ID and requires its own identity verification. Preserve the deletion
record, the current deployed identity, and the exact archive checksum.

## 5. Ordered implementation phases

Execute phases in order. For each step, mark completion only after its stated
evidence exists. Keep a short problem/evidence entry in Section 8 when a check
fails; diagnose the cause before repeating it.

### P00 — Confirm scope and prepare evidence

- [x] **P00.S01 Read and compare.** Read the governing instructions and files
  in Section 3. Run `git status --short --branch` and `git log -1 --format=%H`.
  Preserve user changes. Confirm the three runbook defects still exist; record
  drift from the reviewed source before editing.
- [x] **P00.S02 Establish cheap baseline.** Run
  `node --test scripts/test/production_config_test.mjs`. Confirm the tests for
  invalid candidate rejection, approved old-runtime replacement, no-op apply,
  and `--pull never` remain intact. Record counts and exit status; do not weaken
  or reimplement their oracle.
- [x] **P00.S03 Prepare the isolated recipe verifier.** Follow Section 6 and
  run the ancestry-failure case against the current actual code block. Before
  P02 adds markers, select the existing Section 4 build fence containing
  `git worktree add` and `docker build`; assert exactly one match. Record
  the reproduced failure: the rejected candidate still reaches the simulated
  build. Keep the verifier outside tracked source and prevent all real Docker,
  Git worktree, and network actions from this simulation.

Completion: the existing deployment behavior is understood, and the recipe's
failure is reproduced without changing a production resource.

### P01 — Align tested source and build identity

- [x] **P01.S01 Edit source selection.** Update spec Sections 1, 3, and 4 using
  Section 4.1 above. Remove the normative requirement to reject a commit merely
  because its most recent changes are documentation. Preserve the historical
  source SHA in Section 2. Explain run-ID selection, actual release-job
  acceptance, merged ancestry, and the moving-`main` case.
- [x] **P01.S02 Align requirements and decision.** Update KER-IBD-001 and a
  short ADR clarification with the same tested-commit rule. Search the current
  lifecycle documents for `application-bearing` and `documentation-only`;
  distinguish harmless historical descriptions from contradictory rules.
  Keep the existing workflow unchanged.
- [x] **P01.S03 Verify selection examples.** Check four cases in prose or a
  disposable pure check: app A followed by docs B with a successful release
  at B selects B; a recorded successful A still selects A if main later moves;
  a successful fast-only run is rejected; an unmerged SHA is rejected. Record
  the expected decision for each. No workflow dispatch is needed for this
  documentation task.

Completion: every future build uses exactly its accepted workflow SHA, while
documentation-only changes do not force a build or relabel existing images.

### P02 — Make the build example stop safely on failure

- [x] **P02.S01 Replace the executable example.** Implement every ordered
  requirement in Section 4.2 in the one marked Bash fence. Keep commands in
  this canonical location; avoid duplicated recipes in ADRs or release notes.
- [x] **P02.S02 Verify syntax and behavior.** Extract the actual revised
  fence, run `bash -n` against its text, and execute RB-01 through RB-11 in
  isolation. RB-12 is the source-selection check from P01.S03. Assertions must
  examine exit status and intercepted command order/arguments, not just search
  for words such as `set -e`.
- [x] **P02.S03 Review failure handling.** Confirm the original ancestry
  failure now exits nonzero before worktree creation/build. Confirm an existing
  tag is never overwritten, inspection/probe failures cannot become success,
  and failures preserve owned evidence. Ensure this has added no executable
  production script, test dependency, or CI workflow.

Completion: the command block is usable as documented and all required
failure/success observations pass with real shell execution and fake tools.

### P03 — Correct deployment and rollback interpretation

- [x] **P03.S01 Update spec Sections 5 and 6.** Use the exit-code meanings and
  review boundary in Section 4.3. Replace the blanket instruction to resolve
  every difference. Keep post-apply equivalence mandatory. For rollback, restore
  the approved gateway fields from the checkpoint without replacing unrelated
  later service changes; prefer the verified retained local tag.
- [x] **P03.S02 Reconcile requirements and existing guidance.** Clarify
  KER-IBD-005/006 only if needed; check the environment and self-hosting guides
  for consistency. Preserve `production-config.mjs` and its existing tests.
  The tool's metadata check covers image ID and version; do not claim it also
  automatically verifies platform/source/revision, which the build example
  explicitly checks.
- [x] **P03.S03 Verify the described boundary.** Reuse the P00 test result if
  source/tests have not changed. Point to the existing old-runtime replacement
  and invalid-candidate cases. Check the documentation does not automatically
  apply any exit-2 result and never combines gateway/web under one mismatched
  release SHA. Do not perform a real apply to prove this wording.

Completion: normal upgrades are possible under the instructions, and invalid
or unrelated changes still stop the operator.

### P04 — Correct recovery wording and preserve history

- [x] **P04.S01 Verify the external rule.** Read the official GitHub page
  linked in Section 4.4. If its current conditions differ, record the evidence
  and use the current rule rather than the baseline statement.
- [x] **P04.S02 Amend current records.** Update spec Section 8, the ADR's
  recovery wording, and the latest release cleanup record with the conditional
  restoration window. Add a short reference from the scale plan's Section 14.
  Avoid a calculated permanent expiry date without a known exact deletion
  timestamp. Do not imply a restore was attempted or certified.
- [x] **P04.S03 Preserve artifact and historical facts.** Verify the archive
  still matches its recorded checksum. Keep the original publication,
  deletion, deployed SHA/image ID, and earlier verification entries factual.
  Correct old-descriptor context to past tense where necessary. No archive
  extraction into active workflows or external package operation is needed.

Completion: the records describe both current unavailability and the limited
administrative restoration option accurately.

### P05 — Final validation and handoff

- [x] **P05.S01 Review the complete diff.** Run `git diff --check`, validate
  the edited Markdown's local links, and search for contradictory active rules.
  Confirm changes are confined to the declared documentation surface and that
  the verifier/temp artifacts have not been staged. Summarize all Section 6
  outcomes and any limitations.
- [x] **P05.S02 Record final evidence.** Update this plan and append a concise
  release-record entry: source/documentation commit where known, changed
  files, syntax/probe/test results, and remaining risks. Label retained prior
  production observations as retained. State that no application build,
  package operation, deployment, browser test, or provider test was performed.
- [x] **P05.S03 Complete repository delivery.** Follow the session's existing
  direction for commit/main publication when implementation is requested;
  use a focused `docs:` commit and a non-force push, reviewing ancestry before
  promoting. Verify the remote SHA after any push. A docs-only commit may be
  excluded from automatic test CI; report that accurately. Deployment and image
  publication are not applicable to these documentation corrections.

Completion: all steps have evidence or a precise recorded blocker, the result
is reviewable, and there is no invented runtime release to close out.

## 6. Verification design and acceptance matrix

Use one disposable verifier in a task-owned temporary directory; creating the
verifier source should follow the repository's `apply_patch` editing rule.
Do not add it to routine CI or allocate new canonical `TEST-###` identifiers.
This verifies a consequential shell recipe, not a new application behavior.

Read the actual marked code fence from the specification. Substitute only its
explicit SHA/context input placeholders with fixture values; leave executable
logic unchanged. Execute it with Bash in a child process. Supply intercepted
`git`, `docker`, `mktemp`, and `rmdir` commands that record calls and return
scenario-controlled results. Use an isolated `PATH` containing only the fake
commands and invoke Bash by absolute path, so an unexpected external command
cannot fall through to the host's real tool. Fixtures may write only beneath
the verifier's owned temporary directory. Set a short per-case process timeout;
a timeout is a failed probe, not a reason to increase it.

Fake metadata must match the actual inspected field format. Success fixtures
must return the expected full SHA from the version command. Capture subprocess
exit status and ordered command arguments for assertions. Protect the test
oracle independently of the example: the original ancestry case must fail
before editing and pass afterwards under the same safety assertion.

The `RB-*` names below are local acceptance-case labels, not registered backend
test IDs. Run every stated variant within a grouped row.

| Case | Injected condition | Required observation |
| --- | --- | --- |
| RB-01 | Invalid or missing SHA input | Nonzero; no build or worktree creation. |
| RB-02 | Fetch failure; ancestry rejection | Each stops nonzero before worktree creation/build. |
| RB-03 | Docker daemon check failure; image-list failure | Each stops nonzero; an error is not interpreted as a missing tag. |
| RB-04 | Release tag already exists | Nonzero; no build, tag overwrite, worktree allocation, or image deletion. |
| RB-05 | Temporary-directory allocation failure; worktree-add failure | Each stops before build; no cleanup of an uncertain path. |
| RB-06 | Worktree HEAD differs; dirty tree; status command fails with empty output | Each stops before build. Empty error output must not count as cleanliness. |
| RB-07 | Docker build fails | Nonzero; no version probe or accepted-image report. |
| RB-08 | Image inspection fails; invalid ID; wrong OS/architecture; wrong OCI source/revision/version | Every variant stops nonzero before accepted-image reporting or success cleanup. |
| RB-09 | Version probe fails; returns the wrong SHA | Each stops nonzero; no accepted-image report or success cleanup. |
| RB-10 | Tree becomes dirty after build; final status command fails; worktree removal fails; parent removal fails | Dirty/status failures retain the worktree. Cleanup-command failures stop nonzero and never print the accepted report. |
| RB-11 | All prerequisites and metadata agree | Exactly one simulated build with the selected context, worktree, platform, and both SHA arguments; exact version accepted; clean worktree removed and parent removed last. |
| RB-12 | Successful workflow for docs-after-app SHA; main advances later; fast-only workflow; unmerged SHA; absent/ambiguous dispatch URL; wrong workflow/event/branch; API/view SHA or URL disagreement; missing/failed/skipped release job or step | Decisions match P01.S03 and Section 4.1; no moving-ref substitution, ambiguous selection, inconsistent exact-run evidence, or skipped-release acceptance. |

Across RB-01 through RB-11, assert every Docker call specifies the target
context and the version probe has `--pull never --network none --rm`. Assert
there is no real Docker invocation and no attempted `compose up`, package
write, `docker pull` subcommand, pull policy other than `never`, prune, forced
worktree removal, or unrelated resource operation.
These observations prove shell control flow and argument selection, not real
image build reproducibility or production recovery.

Required existing test command:

```sh
node --test scripts/test/production_config_test.mjs
```

Required archive check:

```sh
sha256sum docs/archive/harden-llm-ghcr-publisher-reference-6887fcd.tar.gz
```

Required final whitespace check:

```sh
git diff --check
```

Docs-only policy applies: no full release suite, application build, provider
call, browser, or production apply. A fresh production status claim may use
the existing read-only check when relevant, but no runtime probe is required
merely to validate corrected wording. Broaden tests only if actual code changes
or a new unresolved boundary justifies them.

## 7. Final acceptance and traceability

| Acceptance | Existing requirement | Evidence |
| --- | --- | --- |
| Tested merged SHA, image source, and labels agree; docs-only edits do not relabel a deployed image. | KER-IBD-001, KER-IBD-003 | P01; RB-06, RB-08, RB-09, RB-12. |
| The documented recipe stops before consequential actions after failed prerequisites. | KER-IBD-003, KER-IBD-005, KER-IBD-007 | P02; RB-01 through RB-11. |
| Expected upgrade differences are reviewed; invalid candidate/configuration differences still block. | KER-IBD-004, KER-IBD-005 | P03; existing TEST-234/TEST-260. |
| Local rollback and conditional GitHub restoration are described without a false restore guarantee. | KER-IBD-006, KER-IBD-008 | P04; official source and unchanged archive hash. |
| No new service, workflow, registry dependency, or permanent test framework. | KER-IBD-002, KER-IBD-009, KER-IBD-010 | P05; complete diff and scope review. |

## 8. Progress, problems, and follow-up record

| Phase | Status | Evidence or blocker |
| --- | --- | --- |
| P00 | Complete | Source/remote `main` were both `30adad594ec3ed61b186a32a7b72636e0e01fb51`; the only pre-existing worktree item was this untracked plan. Focused `production_config_test.mjs`: 13/13 passed in 0.80 seconds. An intercepted Bash reproduction selected the one current build fence and proved an ancestry failure still reached the simulated build and exited zero; no real Git worktree, Docker, or network command ran. |
| P01 | Complete | The spec, KER-IBD-001, and ADR now select the exact merged `headSha` from a recorded successful release run attempt and preserve existing-image identity across docs-only commits. A pure four-case check accepted tested docs-after-app B and recorded A after `main` moved, and rejected fast-only and unmerged candidates. Current lifecycle guidance has no remaining contradictory normative `application-bearing` rule; historical release records were preserved. All current spec shell fences passed `bash -n`. |
| P02 | Complete | The spec contains one marked 105-line Bash recipe with fail-fast execution, exact source/worktree/image/version assertions, explicit context on every Docker command, diagnostic retention, and success-only cleanup. Disposable verifier `/tmp/hllm-runbook-verifier.dlJEUQ/verify.mjs` extracted the actual fence, passed `bash -n`, and passed 30 isolated executions covering RB-01 through RB-11 plus worktree-remove/rmdir failures. The success case had exactly 14 calls in order. No real Git worktree, Docker, network, package, or production command was reachable. Evidence directory: `/tmp/hllm-runbook-verifier.dlJEUQ/run-LQUXmT`. |
| P03 | Complete | The spec and KER-IBD-005/006 now distinguish `check` exits 0/2/1 from `apply` exits 0/1, require field-by-field review, warn against blind retry after post-`up` failure, and restore only gateway fields. Environment/self-hosting examples now use service-specific candidate SHAs. The canonical descriptor example added `identity` to the gateway allowlist, matching the existing valid TEST-260 fixture and live policy. `jq empty` passed; focused TEST-233/234/260 passed 13/13 in 0.64 seconds; searches found no remaining combined gateway/web candidate command or blanket resolve-all instruction in the active guides. |
| P04 | Complete | GitHub's current documentation still describes restoration within 30 days only when the same package namespace/version remains available and the operator has the required permission. The spec, ADR, latest cleanup record, and scale-plan lifecycle note now describe that as an untested administrative possibility rather than a backup. They preserve current pull unavailability, the retained local-image/source-rebuild path, the 2026-09-22 deletion fact, and the historical publication/deployment identities. The publisher archive remains unchanged at SHA-256 `60b771ebdb807f306557f383208d6c2c30e6b6cb87ebfc0337f7f8001bb6c785` with exactly the recorded workflow, test, and Dockerfile. No restoration or package operation ran. |
| P05 | Complete | Complete-diff validation passed: 16 Bash fences parsed after substituting documented angle-bracket placeholders, 42 local Markdown links resolved, `jq empty` accepted the descriptor example, and `git diff --check` passed. RB-12's disposable pure verifier passed 27/27 exact-run acceptance/rejection cases. The final RB-01–RB-11 run passed all 30 isolated shell scenarios and retained evidence at `/tmp/hllm-runbook-verifier.dlJEUQ/run-ili8CJ`. The required focused test passed 13/13 in 0.52 seconds. The broader offline `make test-fast` gate also accepted all 10 tasks with zero nonzero statuses, timeouts, cleanup errors, or cleanup warnings; report `tmp/test-feedback/runner-1790104183595-1749921-543e678fee05d739.json`. Primary implementation commit `522aa778c6cc05c69aec6b2d80923cf47c496033` and evidence commit `eb770974c8112cdab125910d7c9e5e1d215c12c8` were pushed non-force to `origin/main`. Hosted fast run 35772546786 and CodeQL run 35772546559 passed on the evidence commit. The final closeout changes only the plan and release record; final remote identity is verified in the session handoff. Deployment and image publication were not applicable. No browser, provider, release suite, real Docker build, workflow dispatch, package operation, or production action ran. |

Planning validation on 2026-09-22 confirmed six phases, 18 ordered step IDs,
12 acceptance-case labels, 17 valid local file links, balanced code fences,
and no trailing whitespace. This is plan-structure validation; it does not
complete any implementation phase or certify the revised build recipe.

For each problem, append: step ID, observed failure, cause, change made, exact
verification, and remaining uncertainty. Do not mark a phase complete because
only its prose was edited.

- P02.S02 verifier correction: the first disposable verifier safety regex
  matched the OCI label word `source` as though it were a shell command. The
  verifier was corrected to match `eval` or `source` only at command position,
  then all cases were rerun from scratch. This was a verifier-oracle defect;
  the marked release recipe did not change to satisfy it.
- P05.S01 exact-run review correction: an independent review reproduced that
  the plan's shortened bare-regex dispatch example could continue with an empty
  run ID, and found that the active spec did not explicitly stop after failed
  fetch/SHA capture or require the pre-dispatch SHA. Both examples now use
  explicit failure branches, require a full recorded SHA, accept exactly one
  run URL, and cross-check API/view SHA and URL evidence. The expanded RB-12
  verifier rejects absent or ambiguous URLs, missing source identity,
  API/view disagreement, wrong workflow/event/branch, and missing, failed,
  skipped, or duplicate release evidence.
- P05.S01 documentation-validator correction: the first all-fence syntax pass
  interpreted documented values such as `<40-hex-commit-sha>` as Bash input
  redirection. The disposable validator was corrected to substitute only these
  explicit angle-bracket placeholders before `bash -n`; the actual marked
  build recipe remained unmodified and was still executed verbatim by its
  dedicated 30-scenario harness.

Remaining operational considerations to keep on record:

- GitHub restoration eligibility expires and can change with namespace/access;
  no restore of this package has been tested.
- The source-build path still depends on available build dependencies; identical
  source/version labels do not guarantee identical rebuilt image IDs.
- The procedure assumes one operator building a given release tag at a time.
- The existing container's historical GHCR `Config.Image` text can remain while
  the protected descriptor and retained image provide the local deployment path.
- Old descriptor checkpoints can contain historical registry references or
  unrelated service versions. Restore only reviewed gateway fields using the
  verified local image; do not blindly replace newer unrelated configuration.
- Capacity certification and broader application readiness are separate work;
  this documentation correction supplies neither result.
- The accepted fast gate emitted existing compiler/dependency deprecation and
  type warnings in its captured output. All 10 task statuses were zero and the
  runner reported no failure or cleanup warning; dependency-warning cleanup is
  separate from this runbook correction.

## 9. Implementer handoff

When implementation is requested, execute P00 through P05 in order and update
this plan after each phase. Edit the operational documentation, verify the
actual shell example with fake tools, reuse existing deployment tests, and
preserve the current production runtime. Diagnose failed checks without
weakening assertions or extending timeouts. Finish with the requested Git
delivery and an evidence-bounded closeout; an application deployment is not
part of implementing this plan.
