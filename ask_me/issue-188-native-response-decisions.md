# Issue 188 — Harden response contract decisions

The implementation adopts the recommended options below for this release. The issue requests native output on matching ordinary routes. Two decisions concern exceptions to that contract. [Implementation plan](../plans/issue-188-native-response-preservation-plan.md)

## 1. How should Harden handle unsolicited encrypted reasoning?

The visible summary and the opaque `reasoning.encrypted_content` field can arrive on the same item. #158 closed the request to support sending the opaque item back on the next turn. #188 raises a delivery and cache question if the provider sends that field unprompted. [Issue #158]( https://github.com/prls-co/harden-llm/issues/158 )

- **Recommended — Preserve the item and summary, omit the opaque field before delivery and caching.** Document one exception to native-field preservation.
- **Alternative — Preserve the opaque field on delivery and replay it when caching is enabled.** This gives broader output fidelity and adds the value to the key-scoped cache under its normal retention. Accepting it as input on a later turn would be a separate capability.

Which behavior should callers be able to rely on?

## 2. How much native metadata matters after Harden changes the result?

- **Recommended — Use the existing public representation for transformed results.** For a successful repair, return the repaired answer and valid search evidence. For Jina-assisted search, return Harden's observed search evidence with the answer. Document these exceptions and keep full native fidelity for ordinary unmodified successes. This has the smallest change in behavior for these workflows.
- **Alternative — Also preserve compatible native fields from the accepted attempt.** For example, retain an accepted repair response's provider ID or reasoning summary while adding earlier search evidence that still applies. This needs explicit rules for which attempt owns each field and tests for echoed repair instructions or search context.

Which native fields, if any, should Harden guarantee after repair or Jina search?
