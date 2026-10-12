# 1. Requirements and verification traceability

The current accepted contract is
[PLAN-HLLM-REFERENCE-DECOUPLING-002](../plans/restore-reference-workflows-decoupling-plan.md),
ADR-HLLM-033/034 and `api/openapi.yaml`. The full 16-requirement/32-test RTM is
plan section 10; its 72-step execution ledger supplies actual phase evidence.
Planned cases are not passing evidence. The former proxy transition stays
historical and its retained assertions keep their purposes.

## 2. Ownership and evidence

| Requirement | Production owner | Acceptance |
| --- | --- | --- |
| Baseline UI and component reuse | `HardenLlmUI` and host lifecycle | TEST-420/428–431/446–448, frozen baseline manifest |
| Stateless reliability and attempt pricing | Root/runtime core with injected effects | TEST-421/437/445 |
| Ordinary OpenAI inference | One gateway/core | Existing TEST-401–403 plus TEST-422 |
| API-key scope and snapshot credentials | One HTTP bearer boundary, `stateful/` | TEST-423–425/438–440/444/449 |
| Reference history and artifacts | One PostgreSQL pool and scoped Garage paths | TEST-426/427/436/441–443/451 |
| Shared topology and explicit schema apply | Shared owners, existing production apply path | TEST-432–435/450, CHECK-422 |

Only reference executions create product history. Direct `/v1` calls stay free
of profile/login/history requirements. Same-key clients share stored data;
different keys are isolated. Drafts also use the host-supplied opaque draft key.
Release records separate source, tests, CI attempt/SHA, image identity, owner
configuration, deployed HTTP, and unperformed browser/provider acceptance.

## 3. Historical transition evidence

[PLAN-HLLM-PROXY-REFERENCE-001](../plans/proxy-and-reference-app-simplification-plan.md)
records the former transition. TEST-400 retains ordinary OpenAI protocol and
runner traceability; TEST-409 retains the composed HTTP boundary. Their current
assertions move with ownership, while older source/CI/deployment results stay
bound to their original SHA and never prove this release.
