
# ADR-HLLM-032: OpenAI-compatible inference API

- Status: Accepted for implementation
- Date: 2026-10-06
- Requirements: REQ-400–405, REQ-409, REQ-414–415
- Tests: TEST-400, TEST-401, TEST-403, TEST-404, TEST-408–409

## Context

The custom /api/v1/run contract accepts string prompts. Ordinary OpenAI clients send ordered conversations, native model IDs, function calls and protocol-specific streams. CPA exposes Chat Completions and Responses.

## Decision

- Keep GET /v1/models and POST /v1/chat/completions and /v1/responses as the ordinary-client contract. ADR-HLLM-033 adds the reference /api/v1/run companion using the same engine and bearer boundary. `api/openapi.yaml` is authoritative. Clients use Harden-LLM's /v1 base URL and existing `HARDEN_LLM_TOKEN` as API key.
- Support stateless text, JSON schema, function call/results and native generation options through the ordinary OpenAPI contracts. Two codecs call one Go model and one engine.
- Clients carry conversation state. Reject stored conversation state, background execution, unsupported hosted tools and media. Do not claim full OpenAI/CPA parity.
- Optional controls/diagnostics use one `harden` property. Normal requests work without it. Go/OpenAPI own defaults; Phoenix does not duplicate engine decisions.
- Streaming validates/repairs first, then emits standard endpoint-specific SSE, delaying first content. No provisional output, custom progress protocol or retry after disconnect.
- Keep 256 KiB request, 16 MiB upstream response and 60 s inference limits; cap encoded outcomes at 16 MiB including at most 64 KiB diagnostics. Reject before headers, never truncate.
- Optional `X-HLLM-Tracking-ID` and key-scoped `GET /v1/requests/{trackingID}` retain bounded execution metadata in the existing stateful PostgreSQL pool. They preserve request-bound execution and do not retain responses, add reference history, retry inference, or implement background/result retrieval. The root core remains stateless. See `docs/request-status.md` and issue #136.
- `CPA_API_KEY` authenticates upstream only. The binary reads process environment and does not parse dotenv.

## 2026-11-23 amendment for issue 186

The gateway consumes fields it executes or renders and forwards remaining JSON to a matching wire protocol. The existing provider builder performs each known cross-protocol translation. An unrepresentable field yields a named local error for that attempt. Ordinary caller options survive model changes, and a recovery target's native tuning applies to that attempt. Inference cache identity continues to describe the prepared operation. Batch idempotency continues to describe the serialized job items.

Explicit false storage is forwarded and omission delegates to the upstream. Function declarations require cache off. Explicit search selection requires a search declaration. Supported output formats preserve sibling tuning; unsupported output requests name the rejected field. REQ-600–610 and TEST-600 cover this amendment. [Implementation plan](../../plans/protocol-aware-option-forwarding-plan.md)

## Consequences and verification

Requests become conversation-aware. Adapters preserve supported order/tool linkage. Native reasoning survives or is rejected. Default structured repair uses the selected model with no implicit alternate. The custom REST path/diagnostic stream retire. TEST-403 exercises local handlers with the pinned OpenAI SDK and retries disabled.
