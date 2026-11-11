# Main reconciliation and client compatibility audit

## 1. Scope and evidence

Observed 2026-11-11. The checkout must finish on pushed `main`; `dev` must
contain the same accepted work. Application image identity is checked separately
from branch state. Earlier deployment receipts remain dated evidence.

Opsu's [system map](https://github.com/prls-co/ops/blob/main/tech/technical_system_map.xml)
and [reuse catalog](https://github.com/prls-co/ops/blob/main/tech/reuse_catalog.xml)
identify independent Harden, Utility LLM and direct CPA/OpenAI paths. Their
September repository snapshots are discovery evidence; the findings below use
current source and, where stated, running binaries or release modules. A source
version does not establish a published package or deployed Firebase version.

## 2. Client findings

| Client | Verified path and model | Adjustment |
| --- | --- | --- |
| Query Analytics backend | Running Go binary pins Harden `a91c35d76393`; reference `/api/v1/run`, including search, with explicit profile bindings. | Required strict reference-client upgrade is already deployed. No further API adjustment for this release. |
| Query Analytics frontend | `frontend/mix.exs` and `mix.lock` pin reusable Harden UI `6bfe9bbeb063`. A provider-free evaluation of the running release selected the first fixture profile instead of the later CPA preset. | Bump the UI package to `c6f7e6c90573` or a qualified descendant to adopt CPA GPT-6 Luna as the initial picker default. Existing selections should remain intact. This is a default-policy follow-up, not an API blocker. |
| Clustering Go | Running binary pins Harden `a91c35d76393`; reference Run uses the caller's profile/model, including Query Analytics bindings. | Required strict-client upgrade is deployed. No forced model or API change. Its standalone form requires an explicit profile. |
| NER Go | Running binary pins Harden `c884eb8f7b6d`; ordinary `/v1/responses`, no search tools; running API config selects `gpt-6-luna` / `low`. | Existing requests remain compatible. Using the new search feature later needs a client bump plus explicit `Tools`, `ToolChoice`, `Include` and provenance consumption. Retrieval adoption changes the extraction contract and remains consumer-owned. |
| Model Recall / Masked Recall | Custom standard `/v1/responses` client; running product module fixes `gpt-5.6-luna` / `low`. No dependency on the strict reference Run decoder. | No API migration identified for current calls. A move to GPT-6 Luna requires an explicit versioned product-model policy, qualification and deployment; retain historical model identities and tools-disabled evaluation rules. |
| App | Current `dev` source uses Utility LLM `0.14.8`, Query Set `0.10.2`, and shared `gpt-5.4-mini` defaults. | Review and update its new-work defaults and consumer pins in the App/Query Set release. Persisted profiles can override source defaults; live Firebase configuration was not audited here. No Harden API change is required by this rollout. |
| Query Set | Current source pins Utility LLM `0.19.0`; its installed runtime defaults to CPA `gpt-6-luna`. NER and Clustering are separately owned services. | No model-default change needed in the inspected source. Its own release/consumer acceptance remains separate; this audit does not certify deployed Firebase adoption. |
| Utility LLM and AIK/n8n | Utility LLM `0.19.0` source and the AIK native workflow use CPA `gpt-6-luna`; they call CPA directly. | No Harden API migration. Existing workflow/release qualification remains with those owners. |
| Agent Runtime and its reference agents | `provider/cpa/client.go` fixes direct CPA Chat Completions to `gpt-5.6-luna`; Product Opportunity and Synthetic Product Dataset import that provider. | An independently qualified runtime/model release is needed to move these paths to GPT-6 Luna. No mandatory Harden gateway migration follows from this release; deployed agent model identity was not verified. |
| Standalone Product Opportunity / Company Site | `internal/config/config.go` and `companysite.go` default to and require CPA `gpt-5.6-luna`. | Updating this model requires changing and qualifying the owning service's policy. This is a separate direct CPA client; its deployed configuration was not verified here. |

The shared Harden UI default applies to fresh workspaces that include the CPA
preset. Ordinary OpenAI requests still supply their own `model`. Changing the
gateway default does not rewrite saved profiles or downstream product policies.

## 3. Deployment gap and correction

During reconciliation, dev's state advanced to `dcb1c04b801b` while retaining
gateway source `951df690540c`. The build selector omitted `stateful/` inputs.
Production already ran the corrected admission code at `3e21d3a43d30`.

[PR #173](https://github.com/prls-co/harden-llm/pull/173) includes `stateful`,
`httpclient`, embedded API and reusable frontend package inputs. Build decisions
compare each actual component source to the target, recovering the missed
rebuild even after branch state has advanced. Unchanged images retain their
original source labels. `TEST-062` and the preview runbook document this rule.

The stateful input check failed before the fix and passed afterwards; all 19
preview-policy tests pass. Final hosted checks, branch identities, component
images and browser-free HTTP acceptance are recorded in the
[companion receipt](2026-11-11-main-client-closeout.json).

No browser or paid inference was run for this audit. Browser layout and a new
provider-backed end-to-end campaign are not established by these checks.
