# Widget parity verification record

This KER records evidence for `PLAN-HLLM-WIDGET-PARITY-001`. It is a
redacted, repository-local record of the utility-informed embeddable widget
implementation. It must not contain provider credentials, bearer tokens,
session material, or unredacted provider responses.

## Evidence files

- `baseline.json` records the source, implementation, test, cleanup, and
  release-identity fields for the current verification attempt.
- `evaluation.json` is the tracked, redacted requirement classification and
  EVAL-101 through EVAL-104 record. Raw run output remains under the ignored
  `plans/evidence/harden-llm/` tree and is not a clean-checkout dependency.

## Recording rules

1. Record exact commands, exit status, test counts, seeds, and artifact
   identities; do not infer a pass from a focused test when a required gate
   was not run.
2. Keep provider and deployment credentials outside the repository. Public or
   live checks require an approved environment injection and a redacted
   result.
3. A deployment is certified only when the merged source SHA, immutable
   verification artifact, promoted production artifact, health probes, and
   authenticated canary all agree. `pending` or `not-run` is not a pass.
4. Temporary containers, browser profiles, screenshots, uploads, and test
   databases are runner-owned and must be removed or explicitly accounted for
   before closure.

For this execution, no separate authorized verification target or immutable
image transport was available. EVAL-104 therefore records a direct-production
verification deviation explicitly: the isolated release Compose/browser gate
was green before merge, the exact merged images were built once and deployed
with `--no-build`, and production image labels, digests, probes, authenticated
canary, and cleanup were verified. This is not represented as staged promotion.

## Verification commands

The canonical commands are defined by `test/test-tiers.json` and delegated by
the Make targets. The cheap checks are:

```text
node scripts/verify-test-tiers.mjs
node --test scripts/test/widget_parity_traceability_test.mjs
node --test frontend/assets/test/client_core.test.mjs
```

The Phoenix checks run in the pinned `harden-llm-browser-test:local` image
when the host does not provide the required Mix toolchain. Browser and
deployed checks remain separate expensive tiers.
