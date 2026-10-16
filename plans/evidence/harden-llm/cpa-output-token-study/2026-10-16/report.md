# CPA output-token study, 2026-10-16

## 1. Result

The bounded test found **no output-token throttle at the tested load**. It does
not establish the account's token quota or prove that token volume never
affects admission. No TPM cap is inferred from these observations.

All 66 caller requests returned HTTP 200 and a completed response. **65 passed
the independent content qualification**; one long response failed the
nonce/exact-array qualification. It was retained as a failed result and not
replayed. CPA main-log correlation covered all 66 bounded trace-ID suffixes,
with no correlated native 429 or `auth_unavailable` entry. CPA's total internal
attempt count was not exposed.

## 2. Controlled workloads

The existing CPA public endpoint and account used `gpt-6-luna`, reasoning `low`,
native streaming and no HLLM execution/retries. Two calibration requests were
followed by four 16-request runs in short–long–long–short order, separated by
90-second test-quiet intervals. Each run launched ten requests/second with at
most 16 active. Every request reported exactly **81 input tokens**.

Short requests required four `x` strings in a nonce-bearing JSON array; long
requests required 512. The qualification checked model, reasoning, completed
status, usage, nonce, exact array length and every array value. A successful
HTTP status or complete stream alone did not count as a valid result.

| Run | Workload | Validated | Median output tokens | Median duration |
| --- | --- | --- | --- | --- |
| 1 | Short | 16/16 | 34 | 2.394 s |
| 2 | Long | 15/16 | 1,186.5 | 9.451 s |
| 3 | Long | 16/16 | 1,164.5 | 9.824 s |
| 4 | Short | 16/16 | 34 | 2.303 s |

Calibration produced 46 and 1,180 output tokens. Total reported generated
usage was **40,558 tokens, including reasoning**; reasoning is not added again.
The complete experiment lasted approximately 6 minutes 51 seconds, under its
15-minute/66-caller-request bound. Long responses were slower, which alone is
not evidence of throttling.

## 3. Requested caps and interpretation

The [pinned Codex translator](https://github.com/router-for-me/CLIProxyAPI/blob/c93978c4ea2e908255a2a06c37599fda3651554a/internal/translator/codex/openai/responses/codex_openai-responses_request.go#L26-L27)
removes `max_output_tokens` before forwarding. The long calibration exceeded
the caller's nominal 512-token setting, consistent with that source behavior.
Changing a requested cap alone would not have tested output-volume sensitivity.

The tested runs stayed below the previously observed 200-request burst and do
not isolate longer account-wide quota windows or unrelated account traffic.
They demonstrate actual output-length and latency differences without a
detected throttle in this envelope. Larger sustained mixed-output qualification
remains distinct from this small experiment.

## 4. Provenance

`summary.json` records source-file/binary hashes, the active CPA image and
configuration hashes, aggregate outcomes, timing and correlated error counts.
The runner was built from the current uncommitted implementation on base
`b2ebaa5f1c8727364bed2ad3c729cbbc6964aa0c`; the binary hash identifies what ran.
Private request-level measurements remain in the ignored run directory and are
not committed. No prompt outputs, credentials or full CPA trace prefixes are
included in the retained evidence. Shared CPA configuration was unchanged.

Reproduction is explicit opt-in with the protected CPA key already in the
environment:

```sh
go run -tags live ./cmd/harden-llm-cpatokens --live --output NEW_PRIVATE_DIRECTORY
```
