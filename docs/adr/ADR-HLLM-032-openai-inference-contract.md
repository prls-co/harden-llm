
# ADR-HLLM-032: OpenAI-compatible inference API

- Status: Accepted for implementation
- Date: 2026-10-06
- Requirements: REQ-400–405, REQ-409, REQ-414–415
- Tests: TEST-400, TEST-401, TEST-403, TEST-404, TEST-408–409

## Context

The custom /api/v1/run contract accepts string prompts. Ordinary OpenAI clients send ordered conversations, native model IDs, function calls and protocol-specific streams. CPA exposes Chat Completions and Responses.

## Decision

- Replace it with GET /v1/models and POST /v1/chat/completions and /v1/responses. `api/openapi.yaml` is authoritative. Clients use Harden-LLM's /v1 base URL and existing `HARDEN_LLM_TOKEN` as API key.
- Support stateless text, JSON schema, function call/results and only fields explicitly listed in OpenAPI. Two codecs call one Go model and one engine.
- Clients carry conversation state. Reject store:true, previous_response_id, background, hosted tools, media and unsupported fields; never silently discard. Do not claim full OpenAI/CPA parity.
- Optional controls/diagnostics use one `harden` property. Normal requests work without it. Go/OpenAPI own defaults; Phoenix does not duplicate engine decisions.
- Streaming validates/repairs first, then emits standard endpoint-specific SSE, delaying first content. No provisional output, custom progress protocol or retry after disconnect.
- Keep 256 KiB request, 16 MiB upstream response and 60 s inference limits; cap encoded outcomes at 16 MiB including at most 64 KiB diagnostics. Reject before headers, never truncate.
- `CPA_API_KEY` authenticates upstream only. The binary reads process environment and does not parse dotenv.

## Consequences and verification

Requests become conversation-aware. Adapters preserve supported order/tool linkage. Native reasoning survives or is rejected. Default structured repair uses the selected model with no implicit alternate. The custom REST path/diagnostic stream retire. TEST-403 exercises local handlers with the pinned OpenAI SDK and retries disabled.
