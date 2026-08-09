# Release Certification

This document is the durable release summary for the 2026-07-13 v1 candidate
and its 2026-08-09 utility-llm parity refresh.
Detailed command output belongs under ignored
`plans/evidence/harden-llm/<run-id>/`; secrets and live provider output never do.

## Provenance checkpoint

| Input | Certified value |
| --- | --- |
| Target implementation baseline | `a9fcb88104495479e6a7e63f66a5451573ea3bcd` plus the P07 closure commit |
| Source repository | `github.com/prls-co/utility-llm` |
| Captured source SHA | `09769424ca34b9d759e273a7e9dccf4fd00a5f6c` |
| Source package | `@prls-co/utility-llm` `0.14.6` |
| Parity manifest SHA-256 | `973f138211910fbe58deca867d1569adf2b9660b53e1441a3607570e1f2c98a6` |
| Langfuse release / commit | `v3.212.0` / `3a572984276dd2dc2f8f77f1b2aadb799aa17fdf` |
| Upstream Compose SHA-256 | `f4502f5240857cf9189113fe6c32837ec28f46699415f7efb4b59a6f16423741` |

The source worktree was clean during capture, and the capture script verified
that its checkout HEAD matched the recorded SHA. `README.md` and `specs/` were
explicitly excluded from the fixture slices. Fixtures used no live credentials
and are self-contained in this repository. The source contract gate passed;
the full source core manifest remains blocked by the unavailable local
`typescript/bin/tsc` dev dependency.
Langfuse image digests and resolution time are in `deploy/images.lock.json`.

## Toolchain checkpoint

- Go `1.26.5`; Node `22.22.0` for the parity capture; npm `9.2.0`.
- Docker `29.1.3`; Compose `2.40.3` on the original certification host.
- Current WSL refresh: Docker client/server `29.6.2`; Compose `v5.3.1`.
- `govulncheck` `1.6.0` with the official Go vulnerability database.
- Frontend builder: Elixir `1.20.2`, Erlang/OTP `28.4.3`, Phoenix `1.8.9`.
- Frontend browser image: Docker CLI `29.5.2`, Compose `2.40.3`, Chromium/ChromeDriver `149.0.7827.53`.
- Phoenix LiveView `1.2.7` is the required security patch recorded by ADR-HLLM-009.

## Phase execution log

| Phase | Release commit | Result |
| --- | --- | --- |
| P00 foundation/provenance | `3a3a231` | complete |
| P01 core runtime parity | `ce98643` | complete |
| P02 providers/security/projections | `2e574ad` | complete |
| P03 persistence/local identity | `3f5b5e4` | complete |
| P04 REST/OpenAPI gateway | `7282bb4` | complete |
| P05 observability | `6437d18` | complete |
| P06 production stack | `a516ffd` | complete |
| Frontend WEB-TEST-001..012 | `a9fcb88` | complete |
| P07 release closure | this closure commit | complete |

## Final gate record

| Gate | Disposition |
| --- | --- |
| TEST-039 timeout policy | pass |
| TEST-035 `make test-parity` | pass: 33 source-derived parity fixtures; every fixture has an executable semantic consumer or ADR-backed intentional-difference classification |
| TEST-036 `make verify` | pass: format, vet, build, static, unit, parity, Docker integration/integration-race, API, observability, unit race, and vulnerability gates |
| TEST-034 `make test-compose` | pass: fifteen production services plus the test-only provider, full correlation, and clean teardown in 598.138s |
| TEST-037 live providers | pass: OpenAI Responses text and contracted structured calls in 4.301s; no credential or live output persisted |
| TEST-038 live gateway lifecycle | not run: `HARDEN_LLM_LIVE_GATEWAY_CONFIG` and dedicated deployed user/Grafana/Langfuse access are absent |
| Frontend format/compile/unit/audits | pass in the exact Elixir `1.20.2` / OTP `28.4.3` container: 68 tests, 3 excluded; format, warnings-as-errors, dependency, and Hex audits clean |
| WEB-TEST-011 browser | pass: two desktop/mobile Chromium workflows in 58.0s |
| WEB-TEST-012 Compose browser | pass: sixteen services, browser/recovery checks, runtime-image contract, and cross-runtime diagnostics in 169.0s |
| Production gateway image | pass: 9,524,841-byte non-root scratch image at `sha256:675aef5f8fe2efe6b2ced76c03023588bed6f6572cdb0367a6d1e95371568c8e`; embedded/OCI version `0.1.0` |
| Production frontend image | pass: Docker-reported 47,460,842-byte OTP release at `sha256:9977297f87b0f225c78d574f2fb47965642a47952b83ce44ea99ec20784e3711`; runtime UID `10001` and no Mix/Hex/Rebar/Node/npm/Go toolchain |

The 2026-08-09 refresh upgraded `google.golang.org/grpc` to `v1.83.0`, Bandit to
`1.12.4`, and Mint to `1.9.3`. `govulncheck` reports zero vulnerabilities in
called symbols or imported packages; its module inventory still contains one
uncalled vulnerability. The frontend dependency and Hex audits report no known
advisories or retired packages. TEST-038 remains optional release evidence and
must not be replaced by fabricated deployment credentials or the local fake
provider smoke.

## Certified invariants

- The target runs without Firebase or the source repository.
- Provider candidates share one caller deadline, and each candidate's retry and repair path has one total-attempt budget.
- The gateway has one root library execution path and one OpenAPI contract.
- Harden LLM Postgres/Garage never share Langfuse Postgres/MinIO ownership.
- The Collector is the only Langfuse exporter and excludes Phoenix traces from
  Langfuse while retaining cross-runtime traces in Tempo.
- Telemetry and artifact failures cannot change a completed provider result.
- Caddy is the only public-port owner in both effective Compose projects.
- Phoenix stores bearer tokens only in its ephemeral ETS vault and never retries runs.

Accepted deviations are ADR-HLLM-001, ADR-HLLM-002, ADR-HLLM-008,
ADR-HLLM-009, ADR-HLLM-010, and ADR-HLLM-011. No other implementation drift is
accepted.
