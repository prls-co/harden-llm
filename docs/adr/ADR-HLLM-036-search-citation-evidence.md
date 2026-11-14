# ADR-HLLM-036: Text-bound citations and partial search evidence

Status: Accepted for implementation, 2026-11-13. Runtime qualification is
recorded separately; this record does not establish deployment completion.

## 1. Context

Issue #153 requires native Anthropic/Gemini citations to map reliably to the
returned answer. Optional metadata drift must not invalidate a valid answer or
cause paid inference replay. Issue #155 remains deferred.

Anthropic attaches citations to supporting answer text blocks; its `cited_text`
quotes source material. Gemini supplies byte ranges within a response part.
Harden's canonical citation indices are Unicode code points, bound to exact
assistant text by `citationTextSha256`. Existing answer extraction is unchanged.

## 2. Decision

Provider adapters convert only verifiable URL citations. Anthropic block ranges
are mapped into its existing concatenated answer. Gemini byte ranges must use
the selected candidate and emitted text part, valid UTF-8 boundaries, integer
indices, known source indices and matching segment text when supplied. No
substring search or guessed alignment is used. Unsupported associations retain
known sources without inventing annotation ranges.

`SearchResult` retains `Incomplete` and adds optional unique `Warnings` reason
codes. Warning codes are finite and contain no provider-controlled text. New
normalization paths report omitted evidence explicitly; old cache/history
records without warnings stay valid. Retained citations are deduplicated and
bounded at 200, matching the existing source-reference budget. Canonical
cache/history validation remains strict.

Existing cached producer evidence is not retroactively enriched. Callers use
the existing cache `off` or `refresh` mode when they need a new answer with the
new mappings; this change neither invalidates caches nor launches inference.

The Go and reference API return the evidence and omission reasons. OpenAI
Responses/Chat retain their standard annotation shapes; the opt-in `harden`
extension returns only search completeness and warning codes, not a second
copy of source/citation payloads inside its 64-KiB diagnostic budget. The UI
shows a plain partial-evidence notice while retaining the answer and sources.
Consumers choose whether partial evidence meets their application requirements.

No new service, telemetry dependency, fallback, inference retry or raw provider
payload escape hatch is introduced. CPA owns its protocol translations; these
native mappings belong to Harden's direct-provider adapters. API failures that
invalidate the answer itself retain normal error handling.

## 3. Alternatives and consequences

Returning only a boolean is compatible but cannot explain why evidence was
omitted. Throwing on optional metadata drift loses valid output and can trigger
unnecessary inference. Passing through raw provider payloads couples consumers
to provider formats and exceeds this scope. Mapping Gemini parts that Harden
does not return or matching Anthropic source quotes against generated text can
produce false citations; those cases are omitted with warnings.

Offset support is fixture-qualified separately from live CPA acceptance. A
successful GPT/Codex call through CPA does not certify native Anthropic/Gemini
runtime behavior. Revisit multipart answer extraction as its own behavior
change if a consumer needs additional Gemini parts. Richer/non-URL/raw/image
results remain issue #155.

## 4. References and checks

- [Implementation plan](../../plans/issue-153-citation-metadata-plan.md).
- [Anthropic SDK text block contract](https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/types/text_block.py).
- [Gemini segment byte indices](https://aistudio.google.com/api-reference/generate-content).
- TEST-545: native mappings, malformed/unsupported metadata, Unicode, bounds.
- TEST-546: canonical validation, cache, reference/OpenAI contracts and UI notice.
- TEST-547: opt-in live CPA qualification through the composed Go library.
