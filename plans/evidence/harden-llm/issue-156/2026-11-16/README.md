# Issue 156 optional search and preset qualification

## 1. Decision and implementation

Keep Anthropic native search optional and expose observed execution. Its
documented `required` hint does not become forced tool use. Production prompts,
schemas, catalog, runtime policy and services are unchanged. TEST-455 now offers
an explicitly selected search campaign through the existing stateful engine,
including configured Jina fallback. TEST-548 protects its credential-free
invariants. PR #183 targets `dev`; no production rollout is part of this work.

## 2. Exact campaign and outcomes

The authorized campaign ran from `154ad631cc565322442346624ad7e378d9f15c4e`
on 2026-11-16, once per configured preset/mode, with no cache, retry, repair or
rerun. Every one of the 40 shipped presets has text and structured rows.
[Source identity](source.json), [all 80 case rows](live-search.json), and the
[machine-readable summary](summary.json) retain the observations. The live
command exited **1**: qualification is partial, not a passing all-provider gate.

| Outcome | Cases |
| --- | ---: |
| Successful generation with observed search | 47 |
| Successful generation without observed native search | 2 |
| Failed | 10 |
| Unconfirmed transport outcome | 1 |
| Missing provider credentials | 20 |

The 20 unconfigured rows are ten direct Anthropic/OpenAI presets. Thirty
configured presets account for 60 attempted cases; the model was invoked in
54 cases. Search itself is an independent external operation, so those counts
do not establish search settlement or provider billing.

| Provider route | Succeeded | Failed | Unconfirmed | Unconfigured |
| --- | ---: | ---: | ---: | ---: |
| CPA Responses | 35 | 3 | 0 | 0 |
| Google native | 9 | 0 | 1 | 0 |
| Novita with Jina | 1 | 1 | 0 | 0 |
| OpenRouter with Jina | 2 | 6 | 0 | 0 |
| Perplexity Responses | 2 | 0 | 0 | 0 |
| Direct Anthropic | 0 | 0 | 0 | 10 |
| Direct OpenAI | 0 | 0 | 0 | 10 |

The two successful answers without reported search were Gemini 3.1 Pro Preview
and Gemini 3.5 Flash Lite in structured mode. These remain successful optional
search outcomes and do not certify retrieval. Text citations were hash/range
checked where present. Original structured assistant text is unavailable in
the reference result, so its citation-text verification is not claimed.

## 3. Retained failures and uncertainty

- CPA Claude Haiku 4.5 structured generation failed after reported search;
  the retained evidence has one search call and no sources/citations.
- CPA GPT-5.5 text and structured requests returned HTTP 400.
- Gemini 3.7 Flash structured generation ended with an unconfirmed transport
  outcome after about 30 seconds. It was not replayed.
- Six Jina cases failed before model execution: Novita text; OpenRouter
  DeepSeek V4 Flash text; DeepSeek V4 Pro text/structured; GPT-OSS 120B text;
  GPT-OSS 20B text. Several reached the existing 20-second search timeout.
- OpenRouter GPT-OSS 20B structured generation also failed, with model
  invocation recorded and no retained search evidence.

An independent unauthenticated HEAD to Jina, without a search query or key,
completed TCP/TLS but received no HTTP response within ten seconds. This does
not establish the search failures' root cause. No deeper provider-cause claim
is made from the fixed metadata, and no failed or ambiguous paid call was
repeated to obtain a green result. Follow-up qualification must preserve these
original observations and separately scope any new live experiment.

## 4. Deterministic and hosted checks

The new regression was added before implementation and initially failed to
compile because the matrix helpers did not exist. Its implemented default-tag
cases pass, covering all shipped inputs, optional unobserved search, empty
results, malformed/missing evidence, citation hashes/Unicode bounds, structured
text limits, and private atomic report preservation. Static/parity/whitespace
checks pass. The live-tag authorization guard compiled and skipped inference
when the explicit live flag was absent.

`make test-fast` passed all ten tasks with the pinned Elixir/OTP toolchain and
no cleanup errors or warnings; see [fast receipt](fast.json). Hosted fast
[run 43973295931](https://github.com/prls-co/harden-llm/actions/runs/43973295931)
also passed for the campaign source.

A subsequent reporting-only correction moves the started receipt inside the
selected Go subtest. A synthetic-credential check using
`-run '^TestLivePresetMatrix$/^does-not-exist$'` confirms that all 80 filtered
cases remain unstarted with no provider calls. Its first short synthetic bearer
was rejected during configuration; the corrected existing fixture length
passed. This correction changes no execution input or route, and does not
replay the retained campaign. Latest-head CI is recorded in the PR.
