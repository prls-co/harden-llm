# Release Certification

This document is the durable release summary for the 2026-07-13 v1 candidate,
its 2026-08-09 utility-llm parity refresh, the 2026-08-10 production
validation refresh, the 2026-08-16 merged production handoff, the
2026-08-18 P07.S09 parity/security deployment, the 2026-08-18 P07.S10
profile-catalog backfill deployment, and the 2026-08-19 P07.S10 runtime
credential correction and final application deployment. It also records the
2026-08-22 P07.S11-P07.S13 visual-topology, browser-fold, and workspace-draft
corrections, the P07.S14 reusable no-tabs widget deployment, and the P07.S15
profile-aware reasoning correction.
Detailed command output belongs under ignored
`plans/evidence/harden-llm/<run-id>/`; secrets and live provider output never do.

## Provenance checkpoint

| Input | Certified value |
| --- | --- |
| Target implementation baseline | `a9fcb88104495479e6a7e63f66a5451573ea3bcd` plus the P07 closure commit |
| Source repository | `github.com/prls-co/utility-llm` |
| Captured source SHA | `09769424ca34b9d759e273a7e9dccf4fd00a5f6c` |
| Source package | `@prls-co/utility-llm` `0.14.6` |
| Current frontend parity source | `utility-llm` `5c0309e` / `0.15.0` |
| Current profile catalog source | `examples/react-trace-studio/llm-profile-catalog.json` at `utility-llm` `5c0309e2508dc5b7a87d0880c8d794123353c5b0`; SHA-256 `864552eb5e8bf63de590704ef65c2e45ad228e7cc15d4af048609e680348b2f9` |
| Current merged release | PR `#19` / `9a57dcdeb48373cb7d8a8c46aa4670fa5e0095c2` |
| Security dependency remediation | `github.com/getkin/kin-openapi` `v0.144.0` |
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

- Go `1.26.6`; Node `22.22.0` for the parity capture; npm `9.2.0`.
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
| merged production handoff/static CLI auth | `738d530` | complete |
| P07.S09 utility-llm frontend parity and Tempo parser correction | `2c1a34f` | complete; PR `#4` merged |
| P07.S10 utility-llm profile catalog and all-profile test parity | `527f86c` | complete; PR `#9` merged and deployed |
| P07.S10 runtime credential handling and UI error classification | `ab461d3` | complete; PR `#11` merged and deployed |
| P07.S10 final frontend/API contract deployment | `8f69e2b` | complete; PR `#12` merged and deployed |
| P07.S11 utility-aligned visual topology | `9f3741e` | complete; PR `#15` merged and deployed |
| P07.S12 browser fold event serialization correction | `31d3106` | complete; PR `#16` merged and deployed |
| P07.S13 workspace draft preservation for select events | `7c55266` | complete; PR `#17` merged and deployed |
| P07.S14 reusable no-tabs embedded profile widget | `9a57dcd` | implementation complete; PR `#19` merged and deployed |
| P07.S15 profile-aware reasoning capability guard | pending publication | local implementation and hosted pre-fix diagnosis complete; final deployment verification pending |
| kin-openapi security remediation | `2c1a34f` | complete; patched release `v0.144.0` |

## Final gate record

| Gate | Disposition |
| --- | --- |
| TEST-039 timeout policy | pass |
| TEST-017 current profile catalog parity | pass: exact 28-profile catalog, profile graph rules, pricing/reasoning, credential non-disclosure, every-profile text/structured preparation, concurrent missing-row backfill/custom-row preservation, and unconfigured-runtime handling |
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
| Current production images | pass: gateway `28,331,329` bytes at `sha256:78dd6afd26b1f5151267e82acad507cc8ed3991da316061580109941a7152218`, frontend `47,625,486` bytes at `sha256:1f0d4ed9ea629688177b7ce92126a168d6675b776885192a301ebfd398ace4ed`; both OCI release `738d530`, frontend runtime UID `10001` |
| P07.S10 production gateway image | pass: container image `sha256:0a0a3acb2a75f3ca002596da68049ffd0dca052ffe01c657d86ab3cf77dfb91c`, OCI release `527f86c`, and container health `healthy` |
| P07.S10 production profile catalog | pass: authenticated `GET /api/v1/profiles` returned 30 rows: 28 unconfigured current presets and 2 existing configured profiles; existing rows were preserved |
| P07.S10 runtime correction | pass: merged PR `#11` deployed as OCI release `ab461d3`; gateway healthy, API `/healthz` and `/readyz` returned 200, fresh History read returned 6 prior records with no `CPA GPT-5.6 Luna` run, and no retry was issued |
| P07.S10 final application images | pass: gateway `sha256:1dc2f2037176633ec338b47d99254bcdc5f15bd773d65d9683eb2b76bc5e757b` and frontend `sha256:6d7df59edd51fa27267298ac62f4452b7704b89714feaa5fab3e54ad2b628235`; both OCI release `8f69e2b`, both healthy, public API/UI probes 200, and frontend OTLP exporter initialized after startup |
| P07.S09 frontend parity gates | pass: Phoenix 77 passed/3 excluded, browser 2 passed, `make verify`, and final post-upgrade `make test-compose` 183.924s (earlier parity run: 176.997s) |
| P07.S11-P07.S13 frontend closeout | pass: focused workspace/rendering suite 20 passed; full deterministic frontend suite 83 passed/3 excluded; Wallaby desktop/mobile workflow 2 passed in 99.3s; CodeQL and Go/JavaScript analyses passed on PR `#17` |
| P07.S12/P07.S13 real browser regressions | pass: deployed Playwright opened model, advanced-input, retry, history, output, request, and response folds; changing Reasoning and Cache preserved the selected profile; CPA GPT-5.6 Luna returned output; 29 profile cards exposed refresh/edit/delete and metadata; zero page errors |
| P07.S14 embedded widget gate | pass: PR `#19` merged as `9a57dcd`; pinned Phoenix suite 85 passed/3 excluded; desktop/mobile Chromium 2 passed in 102.4s; `make verify` passed; no tabs, nested profile folds, fallback/options behavior, credential staging, CRUD, and bundle delegation are covered |
| P07.S14 production frontend image | pass: image `sha256:f40cc5bf549f4fac3cdca15946004d80b9aba1fdec46a4629592770bb9b63fb5`, OCI release `9a57dcd`, container healthy; gateway remained healthy at release `8f69e2b` / `sha256:1dc2f2037176633ec338b47d99254bcdc5f15bd773d65d9683eb2b76bc5e757b` |
| P07.S14 public probes and API smoke | pass: three consecutive samples returned HTTP 200 for frontend `/healthz` and `/login`, API `/healthz` and `/readyz`; the real static-token structured API smoke also passed |
| P07.S15 profile-capability regression | pass locally: WEB-TEST-040, focused workspace/widget 18 passed, full Phoenix 86 passed/3 excluded, pinned desktop/mobile Chromium 2 passed, and the browser failure was reproduced as an unsupported reasoning option before the outbound CPA call |
| P07.S15 authenticated hosted browser recheck | pending deployment of the profile-aware frontend fix; the existing operator credentials are available in the production environment and no new account is required |
| Tempo trace-ID normalization | pass: 31/32-character external IDs covered by regression tests; no timeout budget changed |
| kin-openapi security alerts | code fix pass: `v0.144.0` is the first patched release for both alerts and CodeQL Go/JavaScript checks passed; GitHub alert records remained open at final readback pending Dependabot rescan |

The 2026-08-09 refresh upgraded `google.golang.org/grpc` to `v1.83.0`, Bandit to
`1.12.4`, and Mint to `1.9.3`. `govulncheck` reports zero vulnerabilities in
called symbols or imported packages; its module inventory still contains one
uncalled vulnerability. The frontend dependency and Hex audits report no known
advisories or retired packages. TEST-038 used the deployed operator plus real
provider, Grafana, and Langfuse credentials; credential values and live provider
output were not persisted in this document or committed fixtures.

The 2026-08-18 closeout upgraded `github.com/getkin/kin-openapi` from
`v0.142.0` to `v0.144.0` to remediate the critical authentication-bypass and
medium validation-panic advisories. `govulncheck` reports zero called
vulnerabilities; its only remaining module-level result is the unmaintained
`golang.org/x/crypto/openpgp` package, which is not called and has no patched
version.

The patched dependency is present in merged `main` and both deployed
application images. GitHub's Dependabot API still reported the two historical
alert records as open at the final readback; this is scanner state awaiting its
next dependency-graph refresh, not an unpatched dependency in the release.

## Production deployment

| Surface | Production value |
| --- | --- |
| Release | frontend `9a57dcd`; gateway `8f69e2b` |
| Frontend | `https://harden-llm.prls.co` |
| API gateway | `https://harden-llm-api.prls.co` |
| Artifact endpoint | `https://harden-llm-artifacts.prls.co` |
| Grafana | `https://harden-llm-grafana.prls.co` |
| Langfuse | `https://harden-llm-langfuse.prls.co` |
| Cloudflare Tunnel | `koldun-harden-llm` / `b9686ab5-270b-4bd6-9aa7-a271c5a02f9d` |
| CPA upstream Tunnel | `shaman-cpa-current` / `a201ae9b-9b34-4544-8ffa-1a91b4b3b2e9` |
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

On 2026-08-15, the production `CurlStructured` default was updated to
`gpt-5.6-luna` through the same CPA endpoint. CPA's public model catalog
advertised the model, and a new real structured request completed successfully
in one attempt.

The frontend and gateway security/provider refresh was deployed as release
`df69d93`. The resulting public frontend and API health/readiness probes all
returned HTTP 200, and a post-restart structured request through the public
gateway completed in one attempt through `CurlStructured`.

On 2026-08-17, merged release `738d530` was promoted to the shaman origin from
clean worktree commit `5624397`, with immutable release labels on both
application images. The gateway and frontend containers, their dependent
services, and the observability stack all reported healthy. Public `/healthz`
and `/readyz` returned HTTP 200, the frontend redirected to `/login`, and a
real structured request completed in one attempt through CPA using
`gpt-5.6-luna`. The static bearer-token CLI path was used for that request.
The active `shaman-cpa` and `koldun-harden-llm` tunnels each have four current
connectors on shaman; local Harden containers and the obsolete local tunnel
container were stopped without deleting the fifteen retained named volumes.

On 2026-08-18, merged release
`2c1a34f9737dd50b6af387c449f63d9299b166d1` was deployed from clean local
`main` using the full Compose stack and the existing retained volumes. The
gateway image was `sha256:cd9d44a8b4dc0a939bffc3239271fadb2bb1c5534640b2c3fb43701421ff6f76`
and the frontend image was
`sha256:965b30420dcbe8f754bbac3682f9534090f3cb2164dbf37e1d698e76eda5d5fd`;
both carry the immutable release label
`2c1a34f9737dd50b6af387c449f63d9299b166d1`. Gateway, frontend, Caddy,
Collector, storage, databases, and observability services reported healthy.
Public API `/healthz` and `/readyz`, frontend `/healthz`, and `/login` all
returned HTTP 200.

The first deployment invocation inherited the checkout's development
`*.harden.localhost` hostnames, so Cloudflared returned HTTP 502 with an
origin TLS error. No code or data was lost. The deployment was corrected by
keeping the tunnel's private-PKI `internal` TLS mode while supplying the five
production `*.prls.co` hostnames; the subsequent Compose wait and public probes
passed.

On 2026-08-18, merged release `527f86c6a0def2b01d18d9d3c9b7ecd9a17c1fad`
(PR `#9`) was deployed from clean local `main` with the retained production
token owner. The gateway image digest was
`sha256:0a0a3acb2a75f3ca002596da68049ffd0dca052ffe01c657d86ab3cf77dfb91c`
and its OCI release label was `527f86c`. The gateway container and dependent
services reported healthy; public `/healthz` and `/readyz` both returned HTTP
200. The authenticated profile-list verification triggered the intended
owner-scoped backfill and returned 30 profiles: the exact 28 current
utility-llm presets as unconfigured plus the 2 existing configured profiles.

On 2026-08-19, merged release
`ab461d3e8cf2f77e8d1e9cc1bcc0e4bd8daa1492` (PR `#11`) was deployed from the
clean local `main` checkout with the retained production environment. The
gateway image ID was
`sha256:9107662665a3b76f6e412ff3cf48f9aca25e95f724fb2db981b71ed94c6cedd1`
and its OCI release label was `ab461d3`; the gateway container was healthy.
Public API `/healthz` and `/readyz`, frontend `/healthz`, and `/login` all
returned HTTP 200. A fresh authenticated History read returned the six prior
records and no `CPA GPT-5.6 Luna` run. No retry was issued while the prior run
outcome was ambiguous. The runtime correction and frontend classification are
covered by TEST-017 plus the translated Phoenix API/workspace tests; the
unconfigured Luna profile still requires an endpoint credential before any
provider execution is attempted.

On 2026-08-19, merged release
`8f69e2b3062dad0cf48a7e75e072575946fc07b4` (PR `#12`) deployed the final
OpenAPI/Phoenix contract correction. The gateway image ID was
`sha256:1dc2f2037176633ec338b47d99254bcdc5f15bd773d65d9683eb2b76bc5e757b`
and the frontend image ID was
`sha256:6d7df59edd51fa27267298ac62f4452b7704b89714feaa5fab3e54ad2b628235`;
both carried the immutable `8f69e2b` release label and reported healthy.
Three sustained public samples returned API `/readyz`, frontend `/healthz`,
and `/login` HTTP 200. The frontend exporter logged successful initialization
after the collector startup window, with no subsequent exporter errors.

On 2026-08-22, PR `#15` (`9f3741e`) aligned the visual topology with the
utility studio: compact cards, a single vertical Workspace stack, and in-flow
folds. PR `#16` (`31d3106`) corrected LiveView fold payload serialization by
using `phx-value-open`; PR `#17` (`7c55266`) merged field-local select events
into the current workspace draft so Reasoning and Cache changes no longer erase
the selected profile. Only the frontend was rebuilt: it deployed as
`sha256:3a8eb2bdc9096210a1c768c87d69c365fbe09b2f1b07d37c6c3d80b64263528d`
with label `7c55266`; the gateway remained at its healthy `8f69e2b` image.
The Compose wait passed, all four public probes returned HTTP 200, and the
authenticated hosted Playwright acceptance passed at desktop and mobile sizes.
It verified all 29 profile cards and their actions/metadata, inline editor and
delete cancellation, every workspace disclosure fold, no tabs/overflow/fixed
overlays, zero page errors, and one real `CPA GPT-5.6 Luna` prompt whose output
and request/response details rendered successfully.

Deployment-method divergence: direct SSH authentication to `shaman.prls.co`
was denied by the host's public-key policy, so the exact production Compose
deployment was executed through the already-authorized local Docker control
path on that host. No routing, image ownership, volume, or application-stack
boundary was changed by this access-path choice.

This is a single-origin deployment, not a high-availability topology. Current
availability is tied to the shaman Docker host, its network, and the active
Cloudflare connectors. Persistent Postgres, Garage, Langfuse, and observability
data live in named Docker volumes on shaman.

On 2026-08-22, merged PR `#19` (`9a57dcdeb48373cb7d8a8c46aa4670fa5e0095c2`)
was built from clean local `main` and deployed as the frontend-only release
`9a57dcd`. The frontend image is
`sha256:f40cc5bf549f4fac3cdca15946004d80b9aba1fdec46a4629592770bb9b63fb5`;
the gateway stayed at healthy release `8f69e2b`. The new frontend container
reported healthy, three public probe samples returned HTTP 200 for both
frontend endpoints and both API endpoints, and the static-token structured API
smoke passed. The pre-fix browser run exposed a profile capability mismatch. The
retained production environment already contains the operator email/password
needed for the follow-up; no new production account was created. P07.S15 carries
the profile-aware fix and final hosted verification.

Pre-publication P07.S15 evidence:

- WEB-TEST-040 passed. The focused workspace/widget suite passed 18 tests; the
  full deterministic Phoenix suite passed 86 tests with 3 excluded.
- The pinned Chromium desktop/mobile workflow passed 2 tests. The hosted
  diagnostic logged a 502 before any CPA request because `CurlStructured` had
  no reasoning map while the browser sent `reasoningEffort: "lowest"`; direct
  CPA and gateway requests without that unsupported field returned 200.
- The fix keeps the backend's strict unsupported-reasoning validation, derives
  the compact selector from profile capability metadata, and strips stale
  unsupported values at the server-side run boundary. No KER, timeout budget,
  provider policy, or related issue was created.

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
ADR-HLLM-009, ADR-HLLM-010, ADR-HLLM-011, ADR-HLLM-012, and ADR-HLLM-013. No other implementation drift is
accepted.

The visual embedding boundary is intentional: Workspace and Profiles are
single-column, stable-root visual surfaces that can sit inside a host shell
without adopting tabs, a side rail, or an overlay. `ProfileWidgetComponent` is
the current functional reusable in-flow widget with optional `id_prefix`
namespacing and explicit host messages; the route layout remains only an
adapter around the LiveView behavior. P07.S15 adds a profile-capability guard
without changing the backend contract; its final hosted prompt verification is
the remaining release step before this amendment is closed.
