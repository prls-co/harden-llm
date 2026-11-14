# Citation mappings and partial search evidence

## 1. Scope and accepted behavior

Issue [#153](https://github.com/prls-co/harden-llm/issues/153) adds reliable URL
citation mappings for native Anthropic and Gemini responses. The same change
makes omitted optional search metadata explicit without rejecting an otherwise
valid answer or repeating inference. Issue #155 (raw/image/non-URL result
support) is deferred. Provider requests, prompts, retries and answer extraction
remain unchanged.

The existing `SearchResult.Incomplete` flag remains authoritative. An optional,
bounded `warnings` array adds finite machine-readable reason codes, with no raw
provider payload, credential, source excerpt or telemetry dependency. Existing
cache/history records without warnings remain valid. Consumers retain control
over whether partial evidence satisfies their application.

## 2. Citation mapping

- Anthropic: its SDK defines citations as supporting their text block. Map a
  URL web-search citation to that block's code-point range in the existing
  concatenated assistant text. `cited_text` is source material, never an answer
  substring to search for.
- Gemini: grounding support indices are byte offsets within `partIndex`.
  Validate integer offsets, UTF-8 boundaries, optional exact segment text and
  source indices. Convert to code points only for the part actually returned
  by Harden's existing extractor and the selected candidate. Other parts keep
  source evidence but receive no guessed annotation.
- OpenAI/CPA: retain existing mappings, explicitly flag invalid, unsupported
  or omitted citation metadata, and preserve the exact-text SHA-256 guard.
- Cap retained citations using the existing 200-reference evidence budget;
  deduplicate identical URL/range citations. Dropped entries set incomplete and
  a reason code. Invalid optional metadata cannot create an invalid canonical
  result; canonical cache/history validation remains strict.

Primary references checked 2026-11-13:

- [Anthropic text block citation semantics](https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/types/text_block.py).
- [Anthropic citations](https://platform.claude.com/docs/en/build-with-claude/citations).
- [Gemini grounding support and segment byte offsets](https://aistudio.google.com/api-reference/generate-content).

## 3. Verification and delivery

Add deterministic provider cases before implementation (TEST-545), then
reference-schema, cache and gateway/client preservation checks (TEST-546).
Verify ambiguity, malformed metadata, Unicode, repeated text, multiple blocks,
structured output, repair and bounded evidence without relaxing existing tests.
Use fast Go, LiveView and Node gates; no browser. Add separately confirmed
Luna/Responses and Claude/Messages live CPA qualifications (TEST-547), outside
deterministic gates, retaining metadata-only evidence and no automatic replay
of an unconfirmed call. Native Gemini live acceptance is separate.

Implement on a feature branch from current `dev`, push a verified checkpoint
and open a reviewable PR targeting `dev`. Production promotion requires a
separate explicit instruction. Native-provider fixture qualification and CPA
runtime qualification are recorded separately.
