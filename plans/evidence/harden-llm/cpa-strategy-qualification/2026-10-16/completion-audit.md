# CPA strategy completion audit

## 1. Scope and decision

The practical scheduling decision and its implementation qualification are
complete. Publication and final Git/PR closeout are the remaining actions at
this checkpoint. The objective covers at least 1,000 separate CPA requests,
shared consumers, measured alternatives, response length, self-hosted durable
execution, observable waiting and uncertain-outcome handling.

Use continuous durable submission through one shared admission controller,
with the measured 200-start/61-second estimate, 128 active slots, adapted launch
pace, cooldowns and one recovery probe. This is an operational choice for the
measured account/model/workloads. A universal concurrency ceiling, exact quota
algorithm, guaranteed future availability and a suspension-safe rate are not
established. Those limits are stated explicitly rather than inferred from green
checks or configuration.

## 2. Requirement-by-requirement evidence

| Requirement | Authoritative evidence | Finding |
| --- | --- | --- |
| Measure the boundary with doubling/bisection and complete an isolated 1,000-call drain | [Throughput search](../../cpa-throughput-search/2026-10-16/report.md) | Cold admission boundary 200; complete 1,000-result drain 255.558 s. This is not a simultaneous-generation ceiling. |
| Measure the proposed minute window and repeated waves | [Minute study](../../cpa-minute-study/2026-10-16/report.md) | Refusal at 59 s and acceptance at 61 s; strict rolling-window explanation contradicted; six 60-second waves retained their failures. |
| Compare independent continuous and guarded admission | Four source-frozen `2e909c7` summaries, derived timing and CPA counters | ABBA completed: three passed, one retained unknown. Continuous produced 631/635 foreground refusals without a consistent complete-results advantage. |
| Check a shared authority with multiple clients | `windowed-shared-1000-213144/summary.json`, `windowed-competing-1000-213914/summary.json` | Both 1,200-result cases passed. Shared zero refusals; independent 183. Same offered arrivals, different background enforcement; not a statistical causal proof. |
| Exercise dispatch after competing execution finishes | Offered-stop summaries and post-background admission counter | Continuous passed 1,000 foreground results with 339 later admissions. Windowed exercised 550 later admissions but retained one invalid response. No failed row is a 1,000-success timing. |
| Check response length beyond tiny bursts | [Output study](../../cpa-output-token-study/2026-10-16/report.md), all-long native case, actual mixed 3,000 case | All-long retained 996 valid plus four uncertain-usage errors, 1,399,852 reported tokens and an upstream 502; no TPM ceiling established. Actual mixed case independently validated all 300 long outputs. |
| Qualify the real durable/provider path above 1,000 calls | [Actual CPA/Temporal verification](temporal-sustained-mixed-windowed-3000-223016/verification.json), summary and derived counters | Three jobs, two bearer clients, all 3,000 distinct results valid, 3,000 main caller starts/finishes, peak 128, zero observed refusal/error outcomes; all downloads verified. |
| Qualify restart, replay, scope isolation and larger orchestration | TEST-480 real local stack; [10,000-result verification](ten-thousand-after.json) | Local 10,000 calls/results and natural run continuation passed on `826757c`; worker restart during known cooldown and replay passed. Synthetic provider is distinct from CPA capacity. |
| Fix observed controller defects without weakening oracles | `controller-before.json`, `controller-after.json`, TEST-474/476/483, `failure-hint-verification.json` | Probe burst, elapsed launch credits and positive failure retry hints corrected with red/green regressions. Sixteen unchanged 1,200-result models pass; old histories retain recorded behavior. |
| Preserve uncertain inference and account restrictions | Controller/activity/workflow regressions and retained failed native samples | Explicit refusals requeue. Ordinary/unknown inference is not replayed. Positive failure hints defer independent work without refunding uncertain credit. Account restrictions pause the pool. CPA's opaque native retries remain outside this guarantee. |
| Verify durable status, results and test liveness | OpenAPI `/v1/jobs`, TEST-478/479/480/482, actual 3,000 run | Scoped idempotency, authenticated URLs, verified NDJSON, separate observation/heartbeat/progress times and five-second waiting updates implemented. The actual run survived a 167.377 s response under its unchanged hard deadline. |
| Respect CPA-only authorization, spend and runtime identity | `campaign-budget.json`, `environment-before.json`, `environment-after.json` | 3,235,681 reported tokens plus 163,840 conservative uncertain-usage reservations below 10M. Image/config/start/restarts unchanged. No official API, account rotation or shared CPA mutation. Hidden native usage remains unknown. |
| Complete current-source browser-free acceptance | [Final release](release-final.json), [included fast tasks](fast-final.json), [source identities](source-identities.json) | All 30 canonical tasks passed on clean `edaea2a`, no cleanup issues. Includes all ten fast tasks. Actual provider source `1c788ac` differs only in test diagnostics; runtime remains `90e42af`. |
| Preserve failed samples and verification attempts | Failed summaries/investigations, corrected reservation, before-fix local results, both failed release attempts and focused reports | Retained without relabeling or replay. Earlier UI/storage failures did not reproduce in focused checks; causes remain unconfirmed. Diagnostics are not claimed as behavior fixes. |
| Keep host-resource qualification truthful | `release-host-resource-setting.json` | Final gate used one peer CPU task, Go `-p=3`, GOMAXPROCS=4, four Erlang schedulers; all cases and 600/500 ms SQL limits unchanged. This does not prove earlier causes or qualify every host setting. |
| Publish bounded evidence and final recommendation | `report.md`, research notes, explicit publication allowlist and secret/JSON/link checks | Prepared and validated; final commit/push and PR closeout pending at this checkpoint. Raw attempts, events, results, auth records and configuration excluded. |
| Preserve ownership and deployment boundaries | Draft HLLM PR127, CPA owner PR7, final Git/remote/worktree inspection | Source changes remain in HLLM and the existing CPA owner handoff. No merge, promotion, deployment or browser authorized/performed. Final publication closeout pending. |

## 3. Limits and remaining closeout

All planned provider experiments and the final release gate have completed.
No more paid calls are required. The account's undocumented limits may change;
representative production prompts and the environment's namespace/network/Garage
provisioning remain rollout concerns, not claims proved by local tests.

Ordinary dispatch has no 200-result completion barrier. Workflow history rollover
must drain active activities; the observed final dispatch gap is consistent with
that boundary, but its exact trigger was not retained. The 167-second response
also demonstrates that whole-job time is affected by provider tails. Direct CPA
and ordinary HLLM calls remain outside batch admission, and job round-robin is
not weighted tenant fairness.

Finish publication and the current Git/PR inspection, then record that closeout
without rerunning the accepted application gates for documentation-only changes.
