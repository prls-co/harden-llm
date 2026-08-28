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
| cookie session and durable encrypted token vault | session controller, auth hook, `SessionVault` | WEB-TEST-004, WEB-TEST-005 |
| profiles, workspace, history, traces, artifacts | LiveViews and narrow controllers | WEB-TEST-006 through WEB-TEST-008 |
| security, telemetry, responsive UI | endpoint/config, observability, components | WEB-TEST-009, WEB-TEST-010 |
| real user and deployment workflows | Wallaby browser tests | WEB-TEST-011, WEB-TEST-012 |
| utility-llm frontend parity extension | `WorkspaceLive`, `ProfilesLive`, `HistoryLive`, `ProfileWidgetComponent`, `EmbeddingLive`, `HardenAPI`, and `api/openapi.yaml` | WEB-TEST-031 through WEB-TEST-043; ADR-HLLM-012, ADR-HLLM-014 |
| embeddable widget utility-informed follow-up | `ProfileWidgetState`, `ProfileWidgetComponent`, `WorkspaceLive`, `EmbeddingLive`, `HardenAPI`, `client_core.mjs`, and the existing tier runner | `PLAN-HLLM-WIDGET-PARITY-001`; TEST-101 through TEST-118; EVAL-101 through EVAL-104; ADR-HLLM-014, ADR-HLLM-016 |

## Parallel test-feedback traceability

The following matrix is the durable summary of
`PLAN-HARDEN-LLM-TEST-FEEDBACK-002`. The manifest and runner remain the only
task-selection source; this document records ownership and acceptance without
repeating command composition.

| Requirement | Intent | Primary owner | Acceptance evidence |
| --- | --- | --- | --- |
| TFH-REQ-001 | One broad cheap coding loop | `Makefile`, `test/test-tiers.json` | TEST-041, TEST-050 |
| TFH-REQ-002 | One tier/resource/cleanup/test-ID owner per task | `test/test-tiers.json`, runner validator | TEST-041, TEST-054 |
| TFH-REQ-003 | Maximum safe parallelism by resource class | `scripts/run-test-tier.mjs` | TEST-049, EVAL-002, EVAL-006 |
| TFH-REQ-004 | Fingerprinted reproducible budgets | benchmark/KER | TEST-048, EVAL-001 through EVAL-007 |
| TFH-REQ-005 | Exact assertion oracle despite lower fidelity | test specifications, ADR-015 | TEST-044, TEST-046, TEST-047, TEST-054 |
| TFH-REQ-006 | Broad server-owned widget coverage without Chromium | LiveViewTest component/workspace/embedding tests | WEB-TEST-044 |
| TFH-REQ-007 | Async frontend ownership with named serial exceptions | ConnCase, frontend policy | WEB-TEST-045, EVAL-003 |
| TFH-REQ-008 | Pure client rules use production imports | `frontend/assets/js/client_core.mjs` | WEB-TEST-046, EVAL-004 |
| TFH-REQ-009 | Small browser boundary-specific suite | two ordinary canaries plus Compose canary | WEB-TEST-047, EVAL-005 |
| TFH-REQ-010 | Shared services with isolated mutable state | integration lease/pool support | TEST-042, TEST-053, EVAL-006 |
| TFH-REQ-011 | Destructive Garage lifecycle is exclusive | `garage-restart-exclusive` | TEST-043, EVAL-006 |
| TFH-REQ-012 | Existing `make verify` meaning is preserved | Make/static release policy | TEST-041, TEST-055 |
| TFH-REQ-013 | Cheap tiers are offline and credential-free | manifest and runner policy | TEST-041, TEST-050 |
| TFH-REQ-014 | Expensive-tier defect gains cheap regression when representable | AGENTS/spec/PR evidence policy | TEST-054, CHECK-002 |
| TFH-REQ-015 | Exact cleanup and redacted bounded evidence | runner, fixtures, deployed launcher | TEST-049, TEST-042, TEST-056 |
| TFH-REQ-016 | Plans/docs/ADR/KER/status/issues agree | traceability test and lifecycle records | TEST-054, CHECK-001, CHECK-005 |
| TFH-REQ-017 | Hosted jobs invoke canonical Make targets | `.github/workflows/test-hierarchy.yml` | TEST-041, TEST-054 |
| TFH-REQ-018 | Deployed frontend identity and behavior match merge | deployed canary/release process | TEST-056, CHECK-004 |

| Case | Tier | Primary path | Canonical command/evidence |
| --- | --- | --- | --- |
| TEST-041 | policy | `internal/testkit/test_tier_policy_test.go` | `go test ./internal/testkit/... -run TestTestTierPolicy -count=1` |
| TEST-042 | T3 service isolation | `internal/integrationtest/isolation_test.go` | `node scripts/run-test-tier.mjs --task integration-isolation` |
| TEST-043 | T3 exclusive lifecycle | `internal/artifacts/garage_restart_test.go` | manifest task `garage-restart-exclusive` |
| TEST-044/045 | T1 frontend server/ownership | frontend LiveView tests | `mix test` and policy test |
| TEST-046/051 | T2 client/boundary | `frontend/assets/test/client_core.test.mjs` | `node --test frontend/assets/test/client_core.test.mjs` |
| TEST-047/052 | T4 browser policy/canaries | frontend browser tests | `make test-browser` |
| TEST-049 | runner contract | `scripts/test/run_test_tier_test.mjs` | `node --test scripts/test/run_test_tier_test.mjs` |
| TEST-050 | fast execution | manifest-selected T0-T2 tasks | `make test-fast` |
| TEST-053 | pooled normal/race consumers | integration packages | `make test-integration` and `make test-integration-race` |
| TEST-054 | lifecycle traceability | `internal/testkit/test_feedback_traceability_test.go` | `go test ./internal/testkit/... -run TestTestFeedbackTraceability -count=1` |
| TEST-055 | release composition | `internal/testkit/release_gate_test.go` | `make test-release`, EVAL-007 |
| TEST-056 | deployed identity/canary | deployed launcher and WEB-TEST-048 | `node scripts/run-deployed-browser-test.mjs` |

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
cursor/limit history, server-owned profile defaults and credentials, a
utility-shaped cache/retry projection, an explicit saved-profile boundary for
endpoint identity changes, and same-origin trace/artifact access; no Firebase,
GCP, browser provider call, or second domain-persistence/runtime path is introduced.

The embeddable widget follow-up is specified in
`plans/llm-widget-utility-parity-implementation-plan.md`. Its 18 requirements
are classified in the parity inventory and verified by cheap LiveView/pure
state/Node/static tests, one targeted native browser boundary, the release
graph, and the final deployed canary. The host owns an optional model catalog;
Harden-LLM defaults are a no-catalog preset, and saved-profile model refresh
remains an ID-only backend operation. No Happy DOM, jsdom, React, or other DOM
emulator/runtime dependency is introduced.

The current backend profile seed is also sourced from that revision's
`examples/react-trace-studio/llm-profile-catalog.json`. `TEST-017` verifies all
28 names and transport mappings, catalog pricing/reasoning and credential-free
serialization, every-profile provider preparation, and concurrent first-use
owner seeding. The source's paid all-profile execution is intentionally kept
as opt-in live evidence; deterministic gates do not require provider
credentials or network access.
