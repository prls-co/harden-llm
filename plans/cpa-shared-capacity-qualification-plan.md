# CPA scheduling decision protocol

## 1. Question and boundaries

Select a practical policy for at least 1,000 distinct gpt-6-luna low requests when CPA/account capacity is shared and changes over time. Absolute certainty about an undocumented, changing external service is unattainable; completion means a justified operational choice, tested alternatives, explicit limits and repeatable falsification criteria.

Only the existing CPA account is used. No official API, account rotation, shared runtime changes, browser or production deployment. All inference has fresh nonces, independent terminal/content validation, and unknown outcomes are retained without replay. Explicit refusals may retry after cooldown. Account restrictions stop the campaign.

## 2. Missing evidence and comparisons

1. Production controller under repeatable quota models: fixed/sliding windows, token refill, active-call limit, changing competing demand, retry hints, unknowns and delayed outcomes. Deterministic cases establish invariants, not live capacity.
2. Matched native CPA runs using the same production controller and request workload: current 200/61s window versus continuous admission with the window disabled. Alternate order across repeated runs. An independent competing client starts before the foreground job, continues during it, then stops; all offered work and accepted/refused outcomes are counted.
3. If evidence shows avoidable waste or a controller defect, add a deterministic regression before improving it; qualify changes against old Temporal histories and unchanged result oracles.
4. Confirm the selected strategy through the real self-hosted Temporal/PostgreSQL/Garage API with multiple jobs/clients and more than 1,000 requests. Keep item failure, worker liveness and provider progress distinct.
5. Retain token-length evidence; add a sustained mixed-output run if the decision otherwise rests only on short responses.

## 3. Decision metrics and acceptance

Compare complete validated foreground and combined outcomes, wall time including the final result, caller refusals, CPA-correlated native refusals when available, peak execution overlap, latency distributions, controller recovery, fairness and idle-capacity intervals. Native internal attempts are unknown. No unknown/failed item may be relabeled as success or silently replayed. A failed sample cannot win a complete-results comparison.

Choose continuous adaptive dispatch if it matches isolated throughput and responds better to competing demand without material loss/failure/retry amplification. Choose a hybrid learned/window guard if continuous probing adds refusals without a demonstrated completion benefit. Prefer one shared account-wide dispatcher for participating clients; separately calling clients remain outside its authority.

## 4. Spend and stopping

The user authorizes at most 10 million gpt-6-luna tokens through CPA. Target much less than that; account reported input plus output (reasoning already included), reserve against active/unknown calls, keep a campaign ledger, and stop well below the allowance if hidden usage cannot be observed. Requests have hard deadlines and progress every five seconds. Stop on account restrictions, substantial unknowns, budget boundary or changed CPA runtime identity. Preserve observations before deciding whether another distinct experiment adds information.


## 5. Qualified outcome

The [qualification report](evidence/harden-llm/cpa-strategy-qualification/2026-10-16/report.md)
and [requirement audit](evidence/harden-llm/cpa-strategy-qualification/2026-10-16/completion-audit.md)
retain all comparisons, failures, source identities and acceptance. The selected
strategy is continuous durable submission through one shared controller with the
measured admission estimate and adaptive recovery. Actual CPA qualification
validated 3,000 separate results across three jobs/two clients; the final
browser-free release passed all 30 tasks. Exact provider quota/concurrency and
suspension thresholds remain unestablished. No deployment is claimed.
