# OpenAI web-search parity: three implementation decisions

These findings block version 3 as written. Their correctness requirements are fixed; the questions concern implementation choices. They are newly formulated from the review findings, not questions previously asked verbatim.

Evidence: local source inspected on 2026-11-09. No tests or provider calls ran. Proposed behavior is not implementation evidence. These choices concern local code, not external vendor guarantees. They do not authorize the paid probe or deployment.

## 1.1 Question

Should runtime repair preparation or provider preparation clear search-specific settings?

## 1.2 Context & clarification

**Forced search** means `WebSearch == true` and `ToolChoice.Mode == "required"`. **Repair** generates corrected structured output without another search.

`prepareNextPlannedWork` in `internal/runtime/recovery_execute.go` and `repairInputs` in `internal/providers/payload.go` disable `WebSearch` but retain the required choice. The proposed `validatePayloadCapabilities` guard still rejects that repair because no tools remain. The proposed `WebSearchTool` must also be handled.

Both options must clear search-specific settings for repair, preserve generation settings for genuine reruns, and leave function-tool behavior unchanged.

## 1.3 Options

### Option A: Establish the invariant in runtime repair preparation

**Rubrics**: `Conf:80% | Invest:i | Blast:i | Reversal:i | Fit:i | Reuse:i | Obs:i | Surface:ii | Perf:na`

- **Approach:** Centralize the complete reset before `Executor.Prepare`; consolidate runtime reset sites. Provider preparation enforces the boundary contract without owning another transition policy.
- **Example:** A repair reaches the executor with search disabled, no raw search tool, and no search-specific required choice.
- **Architecture:** Runtime already owns recovery stages and generation-versus-repair lifecycle.
- **SSoT:** One private runtime transformation owns the repair settings.
- **System limits:** No additional network calls. API rate and concurrency limits: Unknown - not available in local context.
- **Trade-offs:** All executors receive coherent repair settings; requires coordinating existing runtime and provider reset behavior.

### Option B: Complete the reset in provider preparation

**Rubrics**: `Conf:80% | Invest:ii | Blast:ii | Reversal:ii | Fit:i | Reuse:ii | Obs:ii | Surface:i | Perf:na`

- **Approach:** Extend `repairInputs` to clear the required search choice and raw tool before payload validation.
- **Example:** Runtime passes a repair retaining `required`; provider preparation removes it before `validatePayloadCapabilities`.
- **Architecture:** Uses the existing common preparation path for concrete providers.
- **SSoT:** `repairInputs` owns the complete provider-facing reset; protocol builders do not duplicate it.
- **System limits:** No additional network calls. API rate and concurrency limits: Unknown - not available in local context.
- **Trade-offs:** Smaller change; injected executors still receive partially normalized settings. This requires explicitly treating normalization as a provider preparation responsibility.

## 1.4 Recommendation

**Option A.** Repair semantics belong to the runtime lifecycle. Test forced search followed by successful repair through the local provider path, asserting no repair search and intact generation settings for reruns.

## 2.1 Question

Should provenance be normalized once before structured parsing, or attached specifically on the parse-error path?

## 2.2 Context & clarification

**Provenance** means calls, sources and citations in `runtime.SearchResult`. In `internal/providers/normalize.go`, `normalizeDecodedResponse` returns on `STRUCTURED_PARSE` before invoking `normalizeSearch`. Its partial result therefore lacks search evidence.

Malformed JSON and valid JSON that fails schema validation follow different paths. Both need repair coverage. Calls must survive; annotations must disappear when the proposed `CitationTextSHA256` no longer matches the returned text.

Both options preserve existing accounting, completion, envelope, refusal and empty-output checks. Failed generation remains a failure.

## 2.3 Options

### Option A: Normalize once before structured parsing

**Rubrics**: `Conf:80% | Invest:i | Blast:i | Reversal:i | Fit:i | Reuse:i | Obs:i | Surface:i | Perf:na`

- **Approach:** After existing admission checks, populate the partial result's search evidence before `ParseProviderOutput`. Reuse it on success and parse failure.
- **Example:** A completed envelope with a search call and malformed answer JSON returns `STRUCTURED_PARSE` with that call in `partial.Search`.
- **Architecture:** Provider normalization interprets the envelope; runtime handles recovery.
- **SSoT:** One `normalizeSearch` invocation supplies both outcomes.
- **System limits:** One normalization pass; router response size defaults to a configurable 16 MiB. API rate and concurrency limits: Unknown - not available in local context.
- **Trade-offs:** Consistent evidence across outcomes; changes common normalization flow and needs success-path regression coverage.

### Option B: Attach evidence in the parse-error branch

**Rubrics**: `Conf:80% | Invest:ii | Blast:ii | Reversal:ii | Fit:i | Reuse:ii | Obs:ii | Surface:i | Perf:na`

- **Approach:** Populate `partial.Search` immediately before returning `STRUCTURED_PARSE`; retain the existing success-path call.
- **Example:** The error branch assigns `partial.Search = normalizeSearch(prepared, response)`.
- **Architecture:** Focused change within existing error handling; no new public type.
- **SSoT:** All normalization rules remain in `normalizeSearch`, invoked from two mutually exclusive sites.
- **System limits:** One normalization pass on either path, with the same router response bound. API rate and concurrency limits: Unknown - not available in local context.
- **Trade-offs:** Narrower edit; future changes must keep success and recoverable-failure invocation rules aligned.

## 2.4 Recommendation

**Option A.** One invocation gives both outcomes the same evidence policy. Verify malformed JSON and schema-invalid JSON through provider, runtime, gateway, repair and cache replay. Preserve calls while suppressing stale annotations.

## 3.1 Question

Should Jina URL extraction iterate through matches incrementally, or collect all matches before applying the source limit?

## 3.2 Context & clarification

Jina is the existing search fallback. `normalizeSearch` in `internal/providers/search_result.go` uses `searchURLPattern.FindAllString(context, 50)`. This limits raw matches rather than distinct valid URLs. Duplicates can exhaust the allowance before later unique URLs are seen.

The proposed `Incomplete` flag reports omitted or unrepresentable provenance. Matches beyond the regex limit never reach the admission helper, so it cannot report their omission.

Both options retain the first 50 distinct valid URLs in encounter order, flag omitted sources, and avoid treating duplicates alone as truncation. A fixed 51-match lookahead is insufficient when duplicates precede additional unique URLs.

## 3.3 Options

### Option A: Iterate through matches incrementally

**Rubrics**: `Conf:80% | Invest:i | Blast:i | Reversal:i | Fit:ii | Reuse:i | Obs:i | Surface:ii | Perf:i`

- **Approach:** Use an advancing regex-match loop and feed each match to the canonical admission helper. Continue after the cap to classify omissions.
- **Example:** Fifty repetitions of URL A followed by URL B retain both. A 51st distinct valid URL sets `Incomplete`.
- **Architecture:** Pure extraction inside the provider normalizer; no routing or network change.
- **SSoT:** The admission helper owns validity, deduplication, limits and omission reporting.
- **System limits:** Linear scan; auxiliary match storage does not grow with total matches. Production Jina defaults to a configurable 512 KiB response bound. Injected `Searcher` results have no equivalent bound enforced by `preparedWebSearch.results`. API rate and concurrency limits: Unknown - not available in local context.
- **Trade-offs:** Avoids an all-matches slice but adds cursor-management code; no measured performance problem currently justifies that complexity.

### Option B: Collect all matches, then apply the canonical limit

**Rubrics**: `Conf:80% | Invest:ii | Blast:i | Reversal:i | Fit:i | Reuse:i | Obs:i | Surface:i | Perf:ii`

- **Approach:** Use `FindAllString(context, -1)` and pass every match through the existing admission helper.
- **Example:** The 51st distinct URL reaches `add`, which omits it and sets `Incomplete`.
- **Architecture:** Preserves the current loop and existing policy boundary.
- **SSoT:** The same admission helper owns all rules; regex matching only discovers candidates.
- **System limits:** Linear scan and a temporary slice proportional to match count. Production Jina has the configurable 512 KiB bound; injected searchers are not bounded there. API rate and concurrency limits: Unknown - not available in local context.
- **Trade-offs:** Smallest correction, with temporary allocations. The separate 64 KiB prompt-context limit does not bound this scan of the stored result.

## 3.4 Recommendation

**Option B.** It fixes silent truncation and duplicate-induced underfilling with the existing loop. No measured need supports a custom scanner. Test 51 distinct URLs, duplicates before a later unique URL, and duplicate-only input. Choose Option A if bounded auxiliary match storage is an explicit requirement for injected searchers.
