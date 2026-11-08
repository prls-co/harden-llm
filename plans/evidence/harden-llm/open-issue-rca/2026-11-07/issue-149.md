# Issue #149: RCA and solution options

## 1. Scope and evidence

Analysis on 2026-11-07 against Harden `3f8a5173086ea245841551fe988f6a3b03cff7f8`. This is analysis only. No application fix, main-branch change, deployment, browser test or paid inference is claimed. Retained incident evidence and fresh observations are identified separately.

## 2. Cause and boundary inventory

The observed rejection is caused by encoded HTTP bytes, not a model token ceiling. The accepted decision in this issue remains **retain 256 KiB now**; the proposed 32 MiB increase was removed before PR #148 merged. Approximately 32K typical English tokens is only a working expectation, and the future approximately 64K-token research target is not currently guaranteed.

| Owner | Current code and unit | Consequence |
| --- | --- | --- |
| Authenticated scope scan | [internal/gateway/httpapi/reference_access.go:44–58](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/gateway/httpapi/reference_access.go#L44-L58); ordinary default 256 KiB, bundle 2 MiB and jobs separately bounded | Reads/bounds the request before the handler, then restores it. Changing only the handler cannot raise admission. |
| Ordinary handler | [internal/gateway/httpapi/httpapi.go:24–26](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/gateway/httpapi/httpapi.go#L24-L26) and [internal/gateway/httpapi/requests.go:126–135](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/gateway/httpapi/requests.go#L126-L135); 256 KiB encoded bytes | Both declared and unknown-length requests are bounded. |
| Reference Run | [internal/gateway/httpapi/reference.go:100–106](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/gateway/httpapi/reference.go#L100-L106) and [api/openapi.yaml:1814–1815](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/api/openapi.yaml#L1814-L1815) | 256 KiB wire body plus distinct systemPrompt 32768/userPrompt 65536 character constraints. These are not interchangeable with ordinary Responses. |
| Typed client | [httpclient/client.go:20–24](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/httpclient/client.go#L20-L24), [httpclient/responses.go:108–125](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/httpclient/responses.go#L108-L125) and [httpclient/run.go:25](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/httpclient/run.go#L25) | Rejects oversized encoded request locally; nonstream response bound 16 MiB. |
| Upstream provider reader | [internal/providers/router.go:38–41](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/providers/router.go#L38-L41), router.go:241–258,954–962 | Defaults to 16 MiB response wire bytes before full decode. |
| Upstream SSE reader | [internal/providers/router.go:610–625](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/providers/router.go#L610-L625) | Cumulative wire and event guard; repeated deltas/envelopes can exceed final text size. |
| Public output/diagnostics | [internal/gateway/httpapi/responses.go:199–218](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/gateway/httpapi/responses.go#L199-L218) and httpapi.go:234–244 | 16 MiB response, 64 KiB diagnostics. Diagnostics and model output have different purposes. |
| Reference client SSE | [httpclient/stream.go:21–41](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/httpclient/stream.go#L21-L41) and client.go:21–23 | Per-frame 16 MiB plus stream total 16 MiB + 64 KiB. A final JSON size does not prove a stream fits. |
| Shared ingress | [Caddy route lines 10–17](https://github.com/prls-co/caddy-shared/blob/16568645215339c81000f4765bef30ea1e4dc3a6/routes/harden-llm.caddy#L10-L17) | Checked-in API request bound 8 MB, a separate ingress owner. Source is not live-config proof. |

The reference wrapper separately bounds schema at 64 KiB and provider options at 32 KiB ([stateful/run.go:226–230](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/stateful/run.go#L226-L230)). Model context/output limits count tokens and do not bound arbitrary encoded bodies, repeated SSE framing, malformed responses, or allocation before an upstream rejection. Therefore upstream token enforcement alone does **not** establish that local read guards are redundant.

**Fresh minimal Go encoding witnesses:** 262144 ASCII bytes → 262174 encoded bytes; 262144 NUL bytes → 1572894 encoded bytes; 65536 copies of 界 → 196608 raw bytes / 196638 encoded bytes. These use only model/input fields, not the full NER template/schema/correction, and are not token-count measurements. Existing known/unknown-length handler, authenticated middleware, within-budget ordered correction and other-boundary controls all passed. The retained correction fixture encoded 32768 NUL bytes as a 196790-byte ordered request.

The [official Luna model documentation](https://developers.openai.com/api/docs/models/gpt-6-luna) specifies token context/output capacity; it does not establish CPA, Caddy, every configured provider's HTTP bounds, or that encoded readers can be removed. No comprehensive live provider/gateway limit audit has been performed here.

## 3. Options and recommendation

1. **Recommended now:** retain the accepted 256 KiB request policy and bounded response readers; document their wire/memory purpose. Keep this future research separate from NER adoption. Consider eliminating duplicate body scans only after preserving authenticated scope rejection and proving equivalent behavior; this is code simplification, not token-limit removal.
2. If a demonstrated workload requires larger bodies, measure its largest encoded initial and bounded correction requests with schema/Unicode/escaping. Choose one justified wire policy and coordinate client, authorization scanner, handler, ingress and docs. Keep provider/model token admission separate. Do not pick 32 MiB from a four-characters/token estimate.
3. Removing local read guards and relying on providers would delete code but leaves unbounded local reads and fragmented-stream overhead unproved. Not recommended on current evidence.

## 4. Verification and blockers

Before changing limits: inventory actual configured provider/gateway/reverse-proxy limits with units and authoritative sources, measure representative input/correction/output/stream envelopes, and compare normal/oversize/unknown-length/error/diagnostic paths. Preserve no truncation and no inference replay after a size failure or missing terminal outcome.

No user input is needed for analysis. A changed operating target or limits policy would require a new decision overriding the explicit retained-256-KiB choice. Exact live CPA/proxy settings and representative larger workload are evidence gaps for future removal/expansion research. This issue is not ready to close as a completed simplification.
