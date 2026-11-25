# Issue #184 decisions for PR #185

Reply with **1A or 1B**, **2A or 2B**, and the name of any report reader that needs compatibility. I recommend **1A and 2A**. Both decisions concern PR head `9c401a6`. [PR #185](https://github.com/prls-co/harden-llm/pull/185)

### 1.1 Question

If the only unlabeled assistant text comes **before** a completed web search and the response ends there, should Harden LLM return `empty_response` or deliver that text as the answer?

### 1.2 Context & clarification

In the OpenAI Responses API, an assistant message can carry a `phase` of `commentary` for a progress update or `final_answer` for the answer. The field is optional. When it is absent, using item order to interpret the message is a Harden LLM compatibility decision. [OpenAI Responses API reference](https://developers.openai.com/api/reference/resources/responses/methods/create)

Today `finalResponseOutput` selects the last item with nonempty content. The local tests cover an unlabeled preamble, a search, and a later answer. The RCA also reproduced a caller receiving `Searching...` when explicitly labeled commentary was the only text. Both options honor explicit phases and accept a valid answer when optional search was never used. The decision below concerns **unlabeled** text. [Final-message selector](../internal/providers/search_result.go) · [Search contract tests](../internal/providers/web_search_contract_test.go)

### 1.3 Options

**Option A · Require an answer after the completed search when labels are absent**

`Conf:70% | Invest:i | Blast:i | Reversal:i | Fit:i | Reuse:i | Obs:i | Surface:i | Perf:na`

- **Approach** Use the last completed search as the boundary for unlabeled answer text.
- **Example** An unlabeled `Searching...` followed only by a completed search yields `empty_response`. An unlabeled `Go documentation is at go.dev/doc` following the search is eligible as the answer.
- **Architecture** Apply the rule in the provider adapter to both JSON and streamed Responses.
- **SSoT** Use `finalResponseOutput` for the answer, citation attribution, and diagnostic final-text length.
- **System limits** One linear scan of the response items with constant selection state. The selector performs no I/O.
- **Trade-offs** Prevents a progress update from qualifying as an answer. A provider that lists a genuine unlabeled answer before its search item could have that answer rejected. The frequency of that ordering is unknown.

**Option B · Preserve the current ordering rule for unlabeled messages**

`Conf:90% | Invest:ii | Blast:ii | Reversal:ii | Fit:i | Reuse:i | Obs:ii | Surface:i | Perf:na`

- **Approach** When phases are absent, accept the last eligible assistant message regardless of its position relative to search.
- **Example** The same unlabeled `Searching...` followed only by a completed search is returned to the caller.
- **Architecture** Keep the existing adapter behavior for legacy responses and apply phase handling to labeled responses.
- **SSoT** Use the same `finalResponseOutput` selection for the answer, citations, and diagnostics.
- **System limits** One linear scan with constant selection state and no I/O.
- **Trade-offs** Preserves compatibility for providers that omit phases. An ambiguous progress update can still qualify as a successful text answer.

### 1.4 Recommendation

**1A.** A completed search followed by no answer is more useful to expose as an empty response than as a successful progress update. This is stricter than the tested prototype. Add the exact unlabeled sequence as a regression in both JSON and streaming modes.

### 2.1 Question

Does a dashboard, script, or manual workflow use `status == "succeeded"` to count preset test passes? If so, which reader owns that check, and can it change alongside the report?

### 2.2 Context & clarification

`TestLivePresetMatrix` runs each selected model preset and output mode. **Inference outcome** records what happened to the call. **Qualification** records whether the run met the test requirements, including sufficient diagnostic evidence. Today `recordLivePresetOutcome` starts with `RunOutput.Status` and can replace a successful outcome with `invalid_diagnostics`. A dispatched call whose remote outcome is unknown is recorded as `unconfirmed`. I found no independent reader in the sibling repositories searched. A private dashboard or manual workflow is still possible. [Matrix reporter](../cmd/harden-llm-gateway/preset_matrix_helpers_test.go) · [Qualification guide](../docs/preset-live-qualification.md)

### 2.3 Options

**Option A · Give execution and qualification distinct fields**

`Conf:80% | Invest:i | Blast:i | Reversal:i | Fit:i | Reuse:i | Obs:i | Surface:i | Perf:na`

- **Approach** Keep the execution outcome or uncertainty in `status`. Add a private report field, `qualificationStatus`, for the test verdict.
- **Example** A successful answer with missing model diagnostics records `status=succeeded` and `qualificationStatus=invalid_diagnostics`. A pass counter reads `qualificationStatus=passed`.
- **Architecture** Change the private matrix reporter and its readers. Preserve the run, call, and trace IDs for investigation.
- **SSoT** `RunOutput` supplies the run facts. `recordLivePresetOutcome` computes one verdict for both the report and the Go test.
- **System limits** The check scans one row’s operation records. It uses no provider requests or additional workers.
- **Trade-offs** Makes both outcomes directly queryable. An existing pass counter that reads only `status` could overcount until updated.

**Option B · Preserve the existing pass counter and add the service outcome**

`Conf:90% | Invest:ii | Blast:ii | Reversal:ii | Fit:ii | Reuse:i | Obs:i | Surface:i | Perf:na`

- **Approach** Keep the combined verdict in `status`. Add a private report field, `runStatus`, copied from `RunOutput.Status`.
- **Example** The same call records `status=invalid_diagnostics` and `runStatus=succeeded`. Existing pass counters keep working.
- **Architecture** Make an additive change in the private reporter. Retain `unconfirmed` in `status` for ambiguous dispatches.
- **SSoT** Copy the service outcome once. Use the same reporter verdict for the legacy field and the Go test.
- **System limits** The same per-row scan applies, with no provider requests or additional workers.
- **Trade-offs** Supports readers that cannot migrate now. Readers investigating a failure must interpret the combined field alongside `runStatus`.

### 2.4 Recommendation

**2A** if readers can migrate together. It gives “did the call succeed?” and “did the test pass?” stable meanings. Choose **2B** if you identify an existing reader that must keep counting passes through `status`. Historical reports remain evidence under the format that produced them.
