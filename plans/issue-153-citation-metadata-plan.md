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

## 4. Merged implementation

PR [#180](https://github.com/prls-co/harden-llm/pull/180) merged the implementation
and its review correction into `dev`. PR
[#181](https://github.com/prls-co/harden-llm/pull/181) promoted it to `main` at
`2caa7dae20babbd842d62ab2afe69447fff8f037`. The correction retains only candidate
0's Gemini entry-point HTML and reports clipping using the existing UTF-8-safe
byte limiter; seven deterministic cases protect that boundary.

The retained CPA Luna and Claude receipts qualify source
`866933bcb5a9783671533c7d3c2ad2908b7b1b5b`. Subsequent changes include strict
warning-count validation and the Gemini entry-point correction, with unchanged
provider requests, prompts, retries and answer extraction. Native Gemini live
qualification and browser layout checks remain separate from those receipts.

Exact-source [release run 43656452327, attempt 1](https://github.com/prls-co/harden-llm/actions/runs/43656452327/attempts/1)
passed all 30 browser-free release tasks at the merged `main` SHA, including
real-service integration, race, Temporal, Compose, OpenAPI and frontend gates.
Its downloaded artifact checksum and all three accepted runner reports were
verified before the production image builds.

Gateway, worker and web were then deployed from that certified source through
the protected production controller. New-worker pollers and existing pool
readiness passed before gateway/web promotion. TEST-435 HTTP/auth/reference
acceptance and Portal login passed; all three applications had zero restarts.
Protected configuration, shared storage containers and database counts were
preserved. The [production receipt](evidence/harden-llm/issue-153/production-release/receipt.json)
records exact image IDs, the earlier shared-edge recovery and qualification
limits. Issue #155 remains deferred. Documentation-only closeout commits do not
change the deployed application identity.
