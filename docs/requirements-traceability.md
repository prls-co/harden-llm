# Requirements and verification traceability

The current cross-component requirements and their canonical test IDs are in
[`PLAN-HLLM-PROXY-REFERENCE-001`](../plans/proxy-and-reference-app-simplification-plan.md).
The backend acceptance catalog remains
[`SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001`](../plans/from_utility-llm/harden-llm-self-hosted-test-spec.md),
and frontend tests use `WEB-TEST-###` IDs in `frontend/test/`. The OpenAPI file
is the authoritative public request/response contract.

## Current ownership checks

| Concern | Implementation owner | Evidence |
| --- | --- | --- |
| Standard model, chat, and Responses routes | Go gateway and `api/openapi.yaml` | `TEST-403`, `TEST-409`, gateway OpenAPI contract tests |
| Shared hardening and provider execution | Root Go client | `TEST-401`, `TEST-402`, provider and recovery tests |
| Bearer auth and connection startup | Gateway configuration and HTTP middleware | `TEST-404`, configuration and auth tests |
| Frontend-only shared history and per-user drafts | Phoenix `HardenLlm.Reference` | `TEST-405` through `TEST-407`, reference integration tests |
| Profile-free deployment and coordinated cutover | Compose, config tooling, and deployment process | `TEST-408`, `TEST-409`, `TEST-260` |
| Browser-free default feedback | `make test-fast` and tier scheduler | `TEST-400`, `TEST-408`, deterministic Go/Phoenix/Node tasks |

Earlier profile-based traceability and certification sections in the historical
specifications describe the superseded implementation. The current specs were
updated to mark those cases retired and identify their replacement tests; the
old release reports remain evidence of the older shipped behavior, not of this
cutover.

## Verification boundaries

`make test-fast` proves deterministic source behavior without Docker, browser,
or provider credentials. `make verify` adds real local service integration.
`make test-release` is the broader browser-free candidate gate. Browser layout
and native events require an explicit browser-test request. Live provider
acceptance is separate and must be identified as such; source tests, build
success, readiness, or a model-list response alone do not prove end-to-end
inference acceptance.
