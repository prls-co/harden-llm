# CPA final research protocol

## 1. Decision and frozen baseline

Finish the decision-relevant gaps after the 2026-10-16 qualification. Baseline
is `6fdbf4f7eec9208a7e86c961151e97eebeeee3d3`; shared CPA configuration,
credentials and deployment remain unchanged. Root checkout changes owned by
other work are preserved in place; this campaign uses an isolated worktree.
No official API, account rotation, browser or production changes are authorized
by this protocol. Inference uses only CPA `gpt-6-luna`, reasoning `low`.

## 2. Questions and experiments

1. Characterize both Temporal rollover triggers with a virtual-time test of
   the production workflow. A 120-second activity overlaps a one-second
   activity, leaving one queued item. Assert the exact continuation timing,
   carried item and completion without replay. Do not spend provider tokens
   proving an orchestration boundary.
2. Compare short versus distinct synthetic longer inputs using ABBA order,
   256 separate requests per sample, the same 128-slot/200-start/61-second
   controller, and 90 seconds quiet between samples. Quiet is observation,
   not proof that account quota reset. Add 256 longer-output and 256 combined
   longer-input/output requests to exercise the input/output matrix. Actual
   reported and cached input tokens determine workload size, not word count.
   Nonces and exact output oracles are unchanged; context is synthetic data.
3. Use deterministic quota simulations to assess recovery jitter's benefit
   and cost when authority is shared versus independent. No runtime jitter
   is shipped based on a simulation. Do not claim a provider benefit if the
   live samples do not actually exercise refusals/recovery.
4. Inspect official usage guidance, Temporal continuation semantics and CPA's
   pinned implementation. Resolve architecture decisions with source evidence;
   do not seek a suspension threshold or invent an account entitlement.

## 3. Spend, validity and stopping

The new authorization is approximately 10 million provider tokens. Target far
less: six 256-request samples plus small qualification pilots. Count reported
input plus output, reserve 32,768 tokens for each active or uncertain attempt,
and retain the prior campaign's 3,235,681 reported plus 163,840 reserved tokens
separately. Before each run bound its allowance by the remaining new budget;
retain an atomic campaign ledger and five-second progress. Hidden native retry
usage is unobservable, so a measured allowance is not an exact native-spend cap.

Only explicit refusals may replay. Stop on account restrictions, changed CPA
image/config identity, five uncertain outcomes in one case, exceeded per-attempt
reservation, or the campaign budget. Preserve failed samples; their complete
result time cannot win a comparison. Pairwise conclusions must identify order,
background traffic, response tails and uncertainty. Stop research when no
remaining experiment is likely to change architecture or its operating policy.

## 4. Deliverables

Retain a bounded report, source identities, commands, aggregate measurements,
local regression evidence and an explicit ROI judgment for further work.
Keep prompts, outputs, credentials and raw provider logs out of published
evidence. This research does not deploy the existing draft feature.
