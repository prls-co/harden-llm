# Release Certification

This document is the durable release summary for the 2026-07-13 v1 candidate,
its 2026-08-09 utility-llm parity refresh, and the 2026-08-10 production
validation refresh.
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
- Phoenix LiveView `1.2.9` is the current exact security pin; ADR-HLLM-009 records the original `1.2.7` patch and this subsequent advisory-driven update.

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
| P07 release closure | `2e6b026` | complete |
| utility-llm `0.14.6` parity refresh | `25e81a8` | complete |
| production CPA/LiveView refresh | `df69d93` | complete |

## Final gate record

| Gate | Disposition |
| --- | --- |
| TEST-039 timeout policy | pass |
| TEST-035 `make test-parity` | pass: 33 source-derived parity fixtures; every fixture has an executable semantic consumer or ADR-backed intentional-difference classification |
| TEST-036 `make verify` | pass: format, vet, build, static, unit, parity, Docker integration/integration-race, API, observability, unit race, and vulnerability gates |
| TEST-034 `make test-compose` | pass: fifteen production services plus the test-only provider, full correlation, and clean teardown in 598.138s |
| TEST-037 live providers | pass: OpenAI Responses text and contracted structured calls in 4.301s; no credential or live output persisted |
| TEST-038 live gateway lifecycle | pass in 82.838s against the public production origins: login, profile/model refresh, OpenAI Responses run, signed Garage artifact integrity/redaction, bundle export, Tempo/Prometheus/Loki/Langfuse correlation, and cleanup |
| Frontend format/compile/unit/audits | pass in the exact Elixir `1.20.2` / OTP `28.4.3` container with LiveView `1.2.9`: 68 tests, 3 excluded; format, warnings-as-errors, dependency, and Hex audits clean |
| WEB-TEST-011 browser | pass: two desktop/mobile Chromium workflows in 64.0s |
| WEB-TEST-012 Compose browser | pass: sixteen services, browser/recovery checks, runtime-image contract, and cross-runtime diagnostics in 285.3s |
| Production gateway image | pass: Docker-reported 28,319,041-byte image at `sha256:a3045cfb779751db762a79251118e4fe5e7f8eed4920a228b58d9d7a56b8338e`; embedded/OCI release `df69d93` |
| Production frontend image | pass: Docker-reported 47,625,486-byte OTP release at `sha256:4773e3eb086dea97c1f3e1fbfce46f9e09780e477cc7099b3c744501d8c1311c`; OCI release `df69d93`, runtime UID `10001`, and no Mix/Hex/Rebar/Node/npm/Go toolchain |
| Public frontend browser acceptance | pass: Chromium reached `/login`, rendered the operator form, and produced zero console errors after the hostname-scoped Cloudflare edge rule disabled Zaraz and RUM injection |

The 2026-08-09 refresh upgraded `google.golang.org/grpc` to `v1.83.0`, Bandit to
`1.12.4`, and Mint to `1.9.3`. `govulncheck` reports zero vulnerabilities in
called symbols or imported packages; its module inventory still contains one
uncalled vulnerability. The frontend dependency and Hex audits report no known
advisories or retired packages. TEST-038 used the deployed operator plus real
provider, Grafana, and Langfuse credentials; credential values and live provider
output were not persisted in this document or committed fixtures.

## Production deployment

| Surface | Production value |
| --- | --- |
| Release | `df69d93` |
| Frontend | `https://harden-llm.prls.co` |
| API gateway | `https://harden-llm-api.prls.co` |
| Artifact endpoint | `https://harden-llm-artifacts.prls.co` |
| Grafana | `https://harden-llm-grafana.prls.co` |
| Langfuse | `https://harden-llm-langfuse.prls.co` |
| Cloudflare Tunnel | `koldun-harden-llm` / `b9686ab5-270b-4bd6-9aa7-a271c5a02f9d` |
| Tunnel image | `cloudflare/cloudflared@sha256:e39ee8da81ad5e05d77f38d2f51c60ca51bf2a8450ac3abab50c17fdb91d91bf` (`2026.7.3`) |
| Origin | `koldun`, Docker `29.6.2`, Compose `v5.3.1` |

The sixteen application services and the dedicated tunnel connector use
restart policies and were running after deployment. Caddy remains the only
application ingress owner and binds host ports only on `127.0.0.1`; the tunnel
uses four outbound QUIC connections and validates Caddy's private CA. Exact
proxied CNAMEs route the five production hostnames to this tunnel. A
hostname-scoped Cloudflare Configuration Rule disables Zaraz and RUM only for
the frontend so Cloudflare does not inject scripts that conflict with the
application's strict Content Security Policy.

On 2026-08-10, the production `CurlStructured` profile was switched from
OpenAI `gpt-4o` to the canonical CPA Responses gateway at
`https://cpa.prls.co/v1` with model `gpt-5.4-mini`. CPA v7.2.80 was restored on
the active host, its Codex OAuth state was reauthenticated, and the public CPA
model catalog exposed the required model. A real Harden structured request
then passed with HTTP 200 and a strict JSON-schema result; live output and
credential values were not persisted here.

The frontend and gateway security/provider refresh was deployed as release
`df69d93`. The resulting public frontend and API health/readiness probes all
returned HTTP 200, and a post-restart structured request through the public
gateway completed in one attempt through `CurlStructured`.

This is a single-origin deployment, not a high-availability topology. The
intended `shaman` origin was unreachable during deployment, so availability is
currently tied to `koldun`, WSL, and Docker Desktop remaining online. Persistent
Postgres, Garage, Langfuse, and observability data live in named Docker volumes
on that origin.

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
