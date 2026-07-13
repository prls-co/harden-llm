# ADR-HLLM-002: One Typed Root Public API

- Status: Accepted
- Date: 2026-07-13
- Requirements: REQ-001, REQ-002, REQ-019

## Context

The source package exports a broad JavaScript inventory spanning runtime,
Firebase adapters, React helpers, and test utilities. Reproducing that inventory
would expose implementation packages and create multiple execution paths.

## Decision

Expose one Go root package with `New(Options)` and one execution method,
`Client.Call(context.Context, Request) (Result, error)`. Public extension points
are limited to credentials, cache, artifacts, endpoint policy, telemetry
providers, and logging. Provider adapters and all normalization algorithms stay
internal.

## Consequences

This is a semantic port, not a source-compatible language binding. Consumers
migrate to typed profiles and use `Result.Output` where they previously consumed
the direct JavaScript return. TEST-002 prevents a second execution surface or an
exported built-in provider constructor.
