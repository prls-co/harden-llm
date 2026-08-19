# Requirements Traceability Matrix

The canonical backend requirements are defined in
`plans/from_utility-llm/harden-llm-self-hosted-implementation-plan.md`; test
procedures are defined once in the companion test specification. This matrix
routes each requirement to its implementation home and release gate.

| Requirement | Primary implementation | Acceptance tests |
| --- | --- | --- |
| REQ-001 module/root API | root `*.go`, `go.mod` | TEST-001, TEST-002, TEST-005 |
| REQ-002 single call path | `client.go`, `types.go` | TEST-002, TEST-006 |
| REQ-003 retry/repair/backup parity | `internal/runtime/`, `internal/retry/` | TEST-008, TEST-009, TEST-035, TEST-039 |
| REQ-004 provider coverage | `internal/providers/`, embedded profile catalog | TEST-012, TEST-013, TEST-017, TEST-035, TEST-037 |
| REQ-005 endpoint security | `internal/providers/endpoint.go`, credential vault | TEST-014, TEST-018, TEST-022, TEST-038 |
| REQ-006 schema/cache parity | `internal/schema/`, `internal/cachekey/` | TEST-010, TEST-011, TEST-035 |
| REQ-007 canonical projections | `internal/pricing/`, `profiles/`, `traces/`, `stats/` | TEST-015, TEST-016, TEST-017, TEST-035 |
| REQ-008 encrypted credentials | `internal/profiles/credentials.go`, seeded credential state | TEST-017, TEST-018, TEST-022, TEST-038 |
| REQ-009 isolated persistence | `internal/postgres/`, `deploy/garage/`, first-use profile seed | TEST-017, TEST-020, TEST-021, TEST-033, TEST-034, TEST-040 |
| REQ-010 local auth/owners | `internal/gateway/auth/`, bearer middleware, owner-locked profile seed | TEST-017, TEST-022, TEST-023, TEST-024, TEST-038 |
| REQ-011 REST resources | `internal/gateway/httpapi/`, `api/openapi.yaml` | TEST-017, TEST-023 through TEST-026, TEST-038 |
| REQ-012 frontend-independent contract | `api/openapi.yaml`, backend static boundaries | TEST-026, TEST-027 |
| REQ-013 bounded diagnostics | gateway/runtime telemetry and logging | TEST-028, TEST-029, TEST-034 |
| REQ-014 failure isolation | telemetry queues/shutdown, timeout policy | TEST-031, TEST-039 |
| REQ-015 single Collector fanout | `deploy/otel/collector.yaml` | TEST-030, TEST-034 |
| REQ-016 Grafana provisioning | `deploy/grafana/` | TEST-032, TEST-034 |
| REQ-017 fifteen-service deployment | Compose, Caddy, image lock, Langfuse fragment | TEST-033, TEST-034, TEST-039 |
| REQ-018 migration independence | `fixtures/parity/`, static dependency scans | TEST-003, TEST-004, TEST-027, TEST-035 |
| REQ-019 release quality | Makefile, AST/static checks, pinned toolchain, catalog provenance | TEST-001 through TEST-003, TEST-017, TEST-036 |
| REQ-020 Garage artifacts | `internal/artifacts/`, owner-authorized routes | TEST-024, TEST-034, TEST-038, TEST-040 |

TEST-036 is the aggregate deterministic backend gate. TEST-034 separately
certifies the real fifteen-service deployment. TEST-037 and TEST-038 are opt-in
live evidence; absence of credentials is recorded explicitly and never weakens
deterministic acceptance.

## Frontend traceability

The separate `SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001` contract maps as follows:

| Surface | Implementation | Tests |
| --- | --- | --- |
| package/runtime boundary and OpenAPI client | `frontend/mix.exs`, `HardenAPI` | WEB-TEST-001 through WEB-TEST-003 |
| cookie session and ephemeral token vault | session controller, auth hook, `SessionVault` | WEB-TEST-004, WEB-TEST-005 |
| profiles, workspace, history, traces, artifacts | LiveViews and narrow controllers | WEB-TEST-006 through WEB-TEST-008 |
| security, telemetry, responsive UI | endpoint/config, observability, components | WEB-TEST-009, WEB-TEST-010 |
| real user and deployment workflows | Wallaby browser tests | WEB-TEST-011, WEB-TEST-012 |
| utility-llm frontend parity extension | `WorkspaceLive`, `ProfilesLive`, `HistoryLive`, `HardenAPI`, and `api/openapi.yaml` | WEB-TEST-031 through WEB-TEST-036; ADR-HLLM-012 |

The base backend gates never invoke `frontend/`. The frontend imports no Go
implementation and synchronizes only through `api/openapi.yaml`.

## Controlled differences

Source-to-Go semantic projections and deployment differences are accepted only
in [`docs/adr/`](adr/README.md). The fixture manifest annotates parity classes
and hashes every captured artifact. A new unannotated difference is a release
failure, not an implicit compatibility decision.

The source-derived frontend extension is based on `utility-llm` revision
`5c0309e` (`0.15.0`) and is tracked separately from the backend fixture source
snapshot. Its self-hosted adaptations are one canonical profile editor,
cursor/limit history, server-owned profile defaults and credentials, and
same-origin trace/artifact access; no Firebase, GCP, browser provider call, or
second persistence/runtime path is introduced.

The current backend profile seed is also sourced from that revision's
`examples/react-trace-studio/llm-profile-catalog.json`. `TEST-017` verifies all
28 names and transport mappings, catalog pricing/reasoning and credential-free
serialization, every-profile provider preparation, and concurrent first-use
owner seeding. The source's paid all-profile execution is intentionally kept
as opt-in live evidence; deterministic gates do not require provider
credentials or network access.
