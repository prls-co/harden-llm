# Release Certification

This document is the durable release summary for the 2026-07-13 v1 candidate.
Detailed command output belongs under ignored
`plans/evidence/harden-llm/<run-id>/`; secrets and live provider output never do.

## Provenance checkpoint

| Input | Certified value |
| --- | --- |
| Target implementation baseline | `a9fcb88104495479e6a7e63f66a5451573ea3bcd` plus the P07 closure commit |
| Source repository | `github.com/prls-co/utility-llm` |
| Captured source SHA | `9d0492b070f1f45a6ea63eeecd7942a8aef8ae71` |
| Source package | `@prls-co/utility-llm` `0.12.18` |
| Parity manifest SHA-256 | `6f66ba283eb730710db307a32b472645f2a5b6412856df9dcef0e415ed84300e` |
| Langfuse release / commit | `v3.212.0` / `3a572984276dd2dc2f8f77f1b2aadb799aa17fdf` |
| Upstream Compose SHA-256 | `f4502f5240857cf9189113fe6c32837ec28f46699415f7efb4b59a6f16423741` |

The source worktree was dirty during capture; `README.md` and `specs/` were
explicitly excluded. Fixtures were produced from `git archive` of the recorded
SHA, used no live credentials, and are self-contained in this repository.
Langfuse image digests and resolution time are in `deploy/images.lock.json`.

## Toolchain checkpoint

- Go `1.26.5`; Node `22.22.1`; npm `9.2.0`.
- Docker `29.1.3`; Compose `2.40.3` on the certification host.
- `govulncheck` `1.6.0` with the official Go vulnerability database.
- Frontend builder: Elixir `1.20.2`, Erlang/OTP `28.4.3`, Phoenix `1.8.9`.
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
| TEST-035 `make test-parity` | pass: 31 source-derived parity fixtures |
| TEST-036 `make verify` | pass: format, vet, build, static, unit, parity, integration/race, API, observability, and vulnerability gates |
| TEST-034 `make test-compose` | pass: fifteen services and clean teardown in 147.512s |
| TEST-037 live providers | not run: credentials absent |
| TEST-038 live gateway lifecycle | not run: credentials absent |
| Frontend format/compile/unit/audits | pass: 68 tests, 3 excluded; warnings-as-errors, dependency, and Hex audits clean |
| WEB-TEST-011 browser | pass: two desktop/mobile workflows in 54.0s |
| WEB-TEST-012 Compose browser | pass: sixteen services and cross-runtime diagnostics in 153.1s |
| Production gateway image | pass: 9,524,841-byte non-root scratch image at `sha256:675aef5f8fe2efe6b2ced76c03023588bed6f6572cdb0367a6d1e95371568c8e`; embedded/OCI version `0.1.0` |
| Production frontend image | pass: 18,147,722-byte non-root OTP release at `sha256:5c3feb38d29ca0a9edd7a44ef4897513f722a44522474c2d9ef9e43ac02d29e3` |

`govulncheck` reports no vulnerabilities called by the code. Its verbose module
inventory retains GO-2026-5932 for `golang.org/x/crypto/openpgp`, which this
project does not import; the used Argon2 surface is unaffected. Optional live
tests are release evidence only and remain explicitly skipped when their named
credentials are absent.

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
