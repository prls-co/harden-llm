# Web-search parity: production impact and minimum scope

## 1. Conclusion

Keep the repair-setting correction and search-evidence correction inside issue
#137. Both reproduce through the existing provider and runtime paths and can be
addressed locally. Treat the Jina source-extraction correction as a small adjacent
fix, not a prerequisite for native CPA/OpenAI parity. No broad refactor, new
lifecycle model, configurable policy or additional service is justified.

There is no verified production incident count for any of these findings. Local
reproductions establish behavior under synthetic input; the production snapshot
establishes deployment identity and available observations, not failure frequency.

## 2. Source and production observations

- Research source: `512f6ef` on `main`, inspected 2026-11-09.
- Running gateway source: `94c7f7562c84b55d681cf4d11ee676ee59dd0bd5`.
  Its image ID is
  `sha256:baf387d9661d21f351439ce024f2b944003519db5b65861639e231ef98816363`.
  The five application files exercised by the overlays have no diff between
  that deployed revision and the research source.
- Production default upstream is CPA, with native search capability enabled.
  Google and Perplexity also declare search capability; Novita and OpenRouter
  do not. A Jina credential is configured. This proves availability, not use.
- A read-only query of HLLM's own database returned three retained history rows,
  all dated 2026-10-13: zero search requests, repairs or structured parse/schema
  error attempts. There was one cache row. Direct `/v1` requests do not enter
  reference history, so these rows cannot measure total gateway usage.
- Read-only Prometheus queries returned only successful `repair=false` schema
  operation series, both currently and using a 30-day range selector. No
  `repair=true` series was returned. Counter resets, retention, instrumentation
  coverage and the lack of a search dimension prevent an incidence estimate;
  the range query is not a count of all validations over 30 days.
- The retained September search release receipt explicitly records zero real
  provider calls. It is not evidence that these cases worked with a live model.

The local Query Analytics candidate now calls `httpclient.Client.Run` through
the reference API. The implementation plan's statement that it directly calls
the Go library is stale. Its adapter tests search evidence through
`result.Search != nil && result.Search.Executed`; its enrichment normalizer
rejects false evidence. This establishes a concrete consumer consequence of
lost evidence, but the dirty sibling checkout is candidate source and is not
proof of a deployed consumer failure. No sibling files were changed.

## 3. Reproductions and interpretation

Tests use the real `providers.Router`, loopback TLS servers, the explicit runtime
recovery plan, structured schema validation, and the existing process-local cache.
The upstream bodies are synthetic. A second identical logical request checks cache
replay without additional dispatch. The assertion oracle is identical across
source variants.

| Finding | Reproduced result | Production relevance | Minimum action |
| --- | --- | --- | --- |
| Forced-search repair | Current source rejects canonical `required` before dispatch. Applying only the plan's guard admits generation, then rejects repair after one searched dispatch. | A regression introduced by the planned canonical tool-choice change, not proof that today's ordinary search request fails. | Clear the search-specific required choice in the existing runtime repair transition. Clear the proposed raw search tool there when that field is implemented. Preserve generation state for genuine reruns. |
| Malformed JSON loses evidence | Current source successfully repairs malformed JSON in two dispatches with one search, but returns no search evidence; cache replay retains that absence. Valid JSON failing schema validation preserves evidence in the same experiment. | A current semantic defect, with a concrete candidate consumer that rejects missing search evidence. Its production frequency is unestablished. | Extract evidence once after response-admission checks and before JSON parsing; preserve it through the router only for the completed-envelope `STRUCTURED_PARSE` case. |
| Jina duplicate URLs | Fifty copies of URL A followed by URL B produce one retained source despite two distinct valid URLs. | A current source-metadata defect on fallback routes. No evidence establishes frequency; the default CPA route uses native search. | Replace `FindAllString(context, 50)` with `FindAllString(context, -1)` and use the existing deduplication/cap helper. Do not add a custom scanner. |

The malformed-JSON experiment corrected an incomplete earlier recommendation:
moving `normalizeSearch` alone does not fix the behavior. The router subsequently
calls `clearProviderOutput`, which clears both output and search. The exploratory
patch preserves evidence only at normalization-error returns for
`STRUCTURED_PARSE`. Transport errors, interrupted streams, incomplete responses
and refusals retain their existing rejection behavior.

The proposed `Incomplete` field is not implemented by this experiment. The Jina
test proves duplicate-induced underfilling today; it does not claim to test the
future truncation flag. A 51st distinct URL must reach the existing admission
helper for that flag to work when it is added.

## 4. Verification and code size

Three Go overlay variants leave checked-in application source untouched:

1. Baseline: malformed-JSON evidence retention and Jina duplicate tests fail;
   the schema-invalid JSON control passes.
2. Planned guard only: canonical `required` generation dispatches, but both
   malformed and schema-invalid repair paths fail before the repair dispatch.
3. Complete minimal experiment: all four combinations of default/required choice
   and malformed/schema-invalid JSON pass, including evidence-preserving cache
   replay with exactly two dispatches and one search. JSON and SSE paths pass.

Additional synthetic checks confirm HTTP errors, incomplete envelopes, refusals
and unterminated streams still expose neither output nor search evidence.
Existing focused provider tests for repair payloads, safe failures, completion,
search failures and cache lifecycle pass, as does `TestStructuredRepair` in the
runtime package. No full release certification or public provider test ran.

The complete exploratory application patch adds 20 lines and removes 6 across
five existing files, including the planned one-line guard change. It adds one
private shared condition/helper for the three JSON/SSE normalization-error exits;
it adds no public type, configuration, dependency or persistent field. This is a
proof that the three corrections need little code, not a size estimate for the
entire web-search parity feature or a merge-ready implementation.

## 5. Reproduction artifacts

Local artifacts are under
`/home/kirill/.local/state/harden-llm/web-search-impact-20261109/`:
`impact_test.go`, `metadata.json`, `production-aggregates.json`,
`baseline.json`, `planned.json`, `complete-minimal.json`, `exploratory.patch`,
`patch-size.json`, and the corresponding `.log` files. Overlay paths target this
checkout and must be regenerated if it moves. The harness is research-only and
has not been added to normal test gates or the canonical test catalog.

```sh
go test -overlay /home/kirill/.local/state/harden-llm/web-search-impact-20261109/baseline.json ./internal/providers -run '^TestResearch' -count=1 -v
go test -overlay /home/kirill/.local/state/harden-llm/web-search-impact-20261109/planned.json ./internal/providers -run '^TestResearch' -count=1 -v
go test -overlay /home/kirill/.local/state/harden-llm/web-search-impact-20261109/complete-minimal.json ./internal/providers -run '^TestResearch' -count=1 -v
HLLM_RESEARCH_STREAM=1 go test -overlay /home/kirill/.local/state/harden-llm/web-search-impact-20261109/complete-minimal.json ./internal/providers -run '^TestResearch' -count=1 -v
```

The first two commands are expected to fail on the stated source. All source
variants use the same correctness assertions. Production inspection used only
metadata and aggregate reads; no production data, deployment, provider call or
container lifecycle was changed. Browser layout was not checked.
