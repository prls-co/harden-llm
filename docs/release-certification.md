# Release Certification

This document is the durable release summary for the 2026-07-13 v1 candidate,
its 2026-08-09 utility-llm parity refresh, the 2026-08-10 production
validation refresh, the 2026-08-16 merged production handoff, the
2026-08-18 P07.S09 parity/security deployment, the 2026-08-18 P07.S10
profile-catalog backfill deployment, and the 2026-08-19 P07.S10 runtime
credential correction and final application deployment. It also records the
2026-08-22 P07.S11-P07.S13 visual-topology, browser-fold, and workspace-draft
corrections, the P07.S14 reusable no-tabs widget deployment, and the P07.S15
profile-aware reasoning correction. It also records the P07.S16 runtime-parity
release, the P07.S17 hosted-run validation correction, and the P07.S18
multi-instance embedding implementation completed on 2026-08-23.
The resource-aware parallel test-feedback hierarchy then completed P07 merge,
deployment, and certification on 2026-08-24; its application-bearing release
is recorded separately from later launcher/test and documentation commits. The
shared PRLS observability routing was then merged through PR `#40` and deployed
as release `5b830904e7d7a2e7a0a8d7d271969e10c38397a1` on 2026-08-24.

The 2026-08-25 utility-informed embeddable-widget parity work is tracked
separately by `PLAN-HLLM-WIDGET-PARITY-001`. Its release identity and deployed
canary evidence are appended only after the merged SHA and promoted image
digest are independently verified; the existing release records above are not
retroactively relabeled.
Detailed command output belongs under ignored
`plans/evidence/harden-llm/<run-id>/`; secrets and live provider output never do.

## Parallel test-feedback release contract

The implementation candidate is evaluated through the same manifest-owned
hierarchy used during development. `make test-fast` is the broad T0-T2 loop;
`make test-integration` and its race target cover T3 service boundaries;
`make test-browser` covers exactly two ordinary Chromium canaries; and
`make test-release` is the complete release candidate, including the existing
backend/Compose and frontend/Compose checks. The workflow delegates to those
targets and does not copy their task lists.

The KER records accepted reference-host budgets and phase evaluations. A
successful release requires TEST-055, zero failed/leaked task resources, and
redacted evidence. A deployed check is an application-bearing identity check,
not merely a health probe: TEST-056 compares the merged application release
with the frontend image/container, then verifies public probes, authenticated
widget behavior, one bounded configured-profile prompt, exact smoke-history
cleanup, and logout. The documentation-only closure commit is never presented
as the deployed application-bearing SHA.

P06 EVAL-007 is the accepted local release-candidate checkpoint. One warm
sample selected 25 manifest tasks and passed all of them with zero failures,
zero leaked resources, and two cleaned service-pool starts. The release graph
observed `233358 ms` critical-path wall time and `1165.16796875 MiB` peak RSS;
the result was recorded without a task-specific budget and did not alter the
accepted P00 ceilings. The measured command used the reference host's pinned
Elixir `1.20.2` / OTP `28.4.3` PATH. P07 remains responsible for hosted checks,
merged identity, deployment, and TEST-056 public certification.

## Provenance checkpoint

| Input | Certified value |
| --- | --- |
| Target implementation baseline | `a9fcb88104495479e6a7e63f66a5451573ea3bcd` plus the P07 closure commit |
| Source repository | `github.com/prls-co/utility-llm` |
| Captured source SHA | `09769424ca34b9d759e273a7e9dccf4fd00a5f6c` |
| Source package | `@prls-co/utility-llm` `0.14.6` |
| Current frontend parity source | `utility-llm` `5c0309e` / `0.15.0` |
| Current profile catalog source | `examples/react-trace-studio/llm-profile-catalog.json` at `utility-llm` `5c0309e2508dc5b7a87d0880c8d794123353c5b0`; SHA-256 `864552eb5e8bf63de590704ef65c2e45ad228e7cc15d4af048609e680348b2f9` |
| Current merged release | PR `#40` / `5b830904e7d7a2e7a0a8d7d271969e10c38397a1` (shared PRLS observability routing) |
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
- Reference-host frontend/release commands must expose the pinned toolchain:
  `PATH=/home/kirill/.local/elixir-1.20.2/bin:/home/kirill/.local/otp-28.4.3/bin:$PATH`.
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
| P07.S15 profile-aware reasoning capability guard | `d02bee8` | complete; PR `#22` merged, deployed, and verified by authenticated hosted browser |
| P07.S16 reusable no-tabs widget runtime parity | `314e343` | complete; PR `#24` merged and deployed |
| P07.S17 hosted run validation and provider-option parity | `b3a50ce` | complete; PR `#26` merged, deployed, and verified by authenticated hosted browser |
| P07.S18 multi-instance reusable widget embedding | `34eb380` | complete; PR `#28` merged, web deployed, public embedding browser and configured-profile smoke passed |
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
| P07.S15 authenticated hosted browser recheck | pass: existing operator credentials, `CurlStructured`/CPA `gpt-5.6-luna`, all eight nested folds, no desktop/mobile overflow, successful real output, and request JSON without unsupported `reasoningEffort` |
| P07.S16 reusable widget runtime parity | pass: PR `#24` merged as `314e343`; searchable custom values, two-state cache, retry projection, nested upload namespaces, explicit profile-save gating, no tabs, all folds, and responsive behavior are covered by the deterministic/browser suites |
| P07.S17 hosted run boundary | pass: PR `#25` merged as `80397b8` added `formnovalidate` for optional nested profile fields; PR `#26` merged as `b3a50ce` stopped rejecting utility-compatible `max_tokens` provider options; authenticated hosted CPA run returned output with gateway HTTP 200 |
| P07.S17 production images | pass: web image `sha256:e8b057640e1dcc801d2b1e38276e7be6f1371e49565e5d2440a534a5aa60c6b7`; gateway image `sha256:526c1d7605050b4d7c0521663ff17dbe180b3728f85340001ef1cdfba0afda6`; both release `b3a50ce`, healthy |
| P07.S18 embedding implementation and regression gate | pass: PR `#28` merged as `34eb380`; WEB-TEST-043 deterministic coverage passed; focused workspace/embedding suite passed 21 tests; full deterministic frontend suite passed 89 tests with 4 excluded; isolated Chromium embedding workflow passed 1 test |
| P07.S18 deployed frontend image | pass: web image `sha256:9da0680b31ad75f0d5ac226def6fc2c81833fb73ec90f1f763143de765cc75dd`, OCI release `34eb380`, web container healthy; gateway remained healthy at `b3a50ce` / `sha256:526c1d7605050b4d7c0521663ff17dbe180b3728f85340001ef1cdfba0afda6a` |
| P07.S18 public embedding verification | pass: frontend `/healthz` and `/login`, API `/healthz` and `/readyz` returned HTTP 200; authenticated public Chromium opened both `/embed/llm` widgets, independent folds/cache, unique IDs, no tabs/overflow, and logged out successfully |
| P07.S18 configured-profile live smoke | pass: authenticated bounded text calls through the two configured profiles `CurlStructured` and `ShamanLiteLLM`; smoke history records were deleted before logout; the remaining 28 catalog presets stayed unconfigured |
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
| Release | frontend and gateway `5b830904e7d7a2e7a0a8d7d271969e10c38397a1` |
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

Final P07.S15 deployment evidence:

- PR `#21` merged the profile-aware capability guard as `93b7362`; the hosted
  replay then exposed one additional frontend-only edge: selecting a profile
  without a reasoning map attempted to persist an empty
  `reasoningByProfile` value. The gateway correctly rejected that invalid state
  with HTTP 400. PR `#22` merged as `d02bee8` and removes unsupported entries
  before state persistence; WEB-TEST-040 now covers this exact boundary.
- The correct production Compose project was rebuilt and updated at `d02bee8`.
  The frontend image is
  `sha256:a208f39bf3f61d706fdf1ad3bd17e2598795438bce92e7f2d3ab6953d7d0671f`;
  the gateway remains healthy at `8f69e2b` / image
  `sha256:1dc2f2037176633ec338b47d99254bcdc5f15bd773d65d9683eb2b76bc5e757b`.
  The web container is healthy and `/healthz`, `/login`, API `/healthz`, and
  API `/readyz` each returned HTTP 200.
- The authenticated hosted browser check used the existing production
  environment credentials, selected `CurlStructured` at CPA
  `https://cpa.prls.co/v1` with model `gpt-5.6-luna`, opened all eight main and
  nested folds, and completed a real prompt. Desktop width 1425 and mobile
  width 375 both reported zero horizontal overflow; the rendered request
  omitted `reasoningEffort`. No secret or live output was persisted.
- Deployment-method divergence: an initial build without the explicit
  `-p harden-llm` project flag created an isolated duplicate web project. It was
  removed immediately, along with only its newly-created temporary web-log
  volume; the retained production `harden-llm_harden-llm-web-logs` volume and
  all application data were left intact. The final build and update used the
  existing `harden-llm` project.
- P07.S15 is closed. It introduced no KER, timeout-budget, provider-policy, or
  API-ownership change, so no KER or related issue was created.

## Certified invariants

- The target runs without Firebase or the source repository.
- Provider candidates share one caller deadline, and each candidate's retry and repair path has one total-attempt budget.
- The gateway has one root library execution path and one OpenAPI contract.
- Harden LLM Postgres/Garage never share Langfuse Postgres/MinIO ownership.
- The Collector is the only Langfuse exporter and excludes Phoenix traces from
  Langfuse while retaining cross-runtime traces in Tempo.
- Telemetry and artifact failures cannot change a completed provider result.
- Caddy is the only public-port owner in both effective Compose projects.
- Phoenix stores bearer tokens only in its encrypted server-side vault on the
  retained frontend-session volume and never retries runs.

Accepted deviations are ADR-HLLM-001, ADR-HLLM-002, ADR-HLLM-008,
ADR-HLLM-009, ADR-HLLM-010, ADR-HLLM-011, ADR-HLLM-012, ADR-HLLM-013, and
ADR-HLLM-014, and ADR-HLLM-017. No other implementation drift is accepted.

The visual embedding boundary is intentional: Workspace and Profiles are
single-column, stable-root visual surfaces that can sit inside a host shell
without adopting tabs, a side rail, or an overlay. `ProfileWidgetComponent` is
the current functional reusable in-flow widget with optional `id_prefix`
namespacing and explicit host messages; the route layout remains only an
adapter around the LiveView behavior. P07.S15 adds a profile-capability guard
without changing the backend contract, and its authenticated hosted prompt
verification is complete at deployed release `d02bee8`.

## P07.S16 embedded widget runtime-parity follow-up

The post-certification utility comparison found practical differences in the
embedded widget even after the no-tabs topology was in place. This follow-up
implements the searchable custom-value controls, utility two-state cache
behavior with legacy `off` migration, saved-profile-to-gateway retry/repair
projection, namespaced main/nested bundle uploads, and the explicit profile-save
boundary for endpoint, credential, fallback, and identity changes.

The implementation is tracked by ADR-HLLM-014 and WEB-TEST-038 through
WEB-TEST-042. No KER or related issue was created: timeout, retry budget,
provider policy, API ownership, authentication, and deployment topology are
unchanged. Deterministic release and hosted browser evidence is recorded here
after the follow-up commit was published and deployed.

## P07.S17 hosted-run validation and provider-option parity

The first real browser submission after P07.S16 exposed two practical
boundaries that deterministic LiveView rendering did not exercise:

- The optional nested Escalation Model editor is rendered inside the outer run
  form. Its empty profile fields are intentionally required when that editor is
  used, but native browser validation treated them as required for every run and
  prevented `phx-submit` from firing. PR `#25` (`80397b8`) added
  `formnovalidate` to the actual Run Prompt submitter; server-side prompt/profile
  validation remains in force.
- The saved CPA profile's normal utility-compatible `defaultOptions` contains
  `max_tokens`. The gateway's prior generic secret-key scan classified any key
  containing `token` as a credential and returned HTTP 422 after the request
  reached `/api/v1/run`. PR `#26` (`b3a50ce`) narrows that classifier to
  credential-shaped names while retaining rejection of API keys, authorization,
  credentials, passwords, secrets, and bearer/session/access tokens. TEST-012
  now covers both accepted request token-limit names and rejected credential
  names.

Final verification on the deployed `b3a50ce` release:

- Full Go tests passed with `go test ./...`; the new provider-option regression
  cases passed, and CodeQL Go/JavaScript checks passed on PR `#26`.
- The current isolated `make test-compose` signal gate passed in `189.956s`,
  including service readiness, fake-provider execution, persistence, and
  cross-service correlation.
- The full Compose deployment was rebuilt with the explicit `harden-llm`
  project and reported healthy. The frontend image was
  `sha256:e8b057640e1dcc801d2b1e38276e7be6f1371e49565e5d2440a534a5aa60c6b7`;
  the gateway image was
  `sha256:526c1d7605050b4d7c0521663ff17dbe180b3728f85340001ef1cdfba0afda6`.
- Public frontend `/healthz` and `/login`, plus API `/healthz` and `/readyz`,
  returned HTTP 200. The gateway log recorded a successful CPA
  `gpt-5.6-luna` run through `POST /api/v1/run` with HTTP 200.
- The authenticated Chromium workflow passed login, the compact no-tabs widget,
  cache toggle, all main and nested folds, desktop/mobile no-overflow checks,
  and a real Run Prompt using the existing operator credential. No new account,
  provider key, raw credential, or live output was committed or persisted in
  the repository.

The deployment configuration correction is also explicit: the ignored runtime
`.env` uses the public `*.prls.co` hostnames, tunnel-trusted `internal` TLS,
and includes `cpa.prls.co` in the provider allowlist. This was configuration
repair for the existing deployment, not an application ownership or routing
change. No KER, timeout-budget change, retry-budget change, or related issue
was required.

## P07.S18 multi-instance embedding implementation

The earlier `id_prefix` promise covered only selected visible controls. A
second widget still collided on Phoenix-generated `profile_*` form IDs, and a
host could not attribute child messages or bundle uploads to the correct
instance. P07.S18 closes that practical gap by namespacing all generated
form/control IDs, tagging parent messages with the widget prefix, assigning
distinct main/escalation upload channels, and adding the authenticated
`/embed/llm` two-instance host fixture.

Final release evidence:

- WEB-TEST-043 deterministic LiveView coverage passed with unique IDs,
  independent folds/cache/profile selection, and distinct upload names.
- The full deterministic frontend suite passed with 89 tests and 4 exclusions;
  the focused workspace/embedding suite passed 21 tests. The isolated Chromium
  embedding feature passed in 50.2 seconds after
  opening both widget trees, checking independent cache state, and verifying
  no tabs, duplicate IDs, or horizontal overflow.
- PR `#28` merged as `34eb380`. The existing `harden-llm` Compose project was
  rebuilt with the frontend image
  `sha256:9da0680b31ad75f0d5ac226def6fc2c81833fb73ec90f1f763143de765cc75dd`
  carrying OCI release `34eb380`; the gateway remained healthy at release
  `b3a50ce` and image
  `sha256:526c1d7605050b4d7c0521663ff17dbe180b3728f85340001ef1cdfba0afda6`.
  All 16 services reported healthy or running in the effective full Compose
  project.
- Public frontend `/healthz` and `/login`, plus API `/healthz` and `/readyz`,
  returned HTTP 200 after deployment. The authenticated public Chromium check
  reached `/embed/llm`, opened both widget trees and their nested folds,
  verified independent cache state, unique DOM IDs, no tabs, no horizontal
  overflow, and a clean logout.
- The authenticated profile catalog contained 30 rows: exactly two configured
  profiles (`CurlStructured` and `ShamanLiteLLM`) and 28 unconfigured presets.
  One bounded text smoke passed through each configured profile; the temporary
  history records were deleted before logout. No provider output or credential
  was persisted in the repository.
- No KER or related issue was created. This is a frontend component-boundary
  correction; provider policy, credentials, retry/timeout budgets, and API
  ownership are unchanged.

## Post-certification CPA GPT-5.6 Luna binding

After the P07.S18 release, the operator requested the previously unconfigured
`CPA GPT-5.6 Luna` profile to be made usable. The profile already had the CPA
origin `https://cpa.prls.co/v1` and model `gpt-5.6-luna`; it was rebound to the
existing configured CPA credential used by `CurlStructured`, with matching
user-scoped endpoint semantics. No new provider key was created, added to
`.env`, rendered, or committed.

- The backend profile save/probe succeeded and `GET /api/v1/profiles` reported
  30 rows with 3 configured profiles: `CurlStructured`, `ShamanLiteLLM`, and
  `CPA GPT-5.6 Luna`.
- A bounded direct Luna text run passed through the public API. Its smoke
  history record was deleted before logout.
- The authenticated public browser check showed Luna as configured, selected it
  through the real searchable combobox, completed Run Prompt without the
  credential error, and returned to the login page. Its smoke history record
  was deleted afterward.
- No KER or related issue was created: this was an owner-scoped credential
  binding using an existing secret, with no code, provider policy, timeout, or
  retry-budget change.

## Parallel test-feedback hierarchy P07 closure (2026-08-24)

P07 completed `PLAN-HARDEN-LLM-TEST-FEEDBACK-002` through reviewed merge,
deployment, public behavior, and cleanup certification. Implementation PRs
`#33` through `#37` are merged. PR `#37` (`40083fd980a4c88c344dfa884c90a2edbbcc7111`)
corrected two real certification defects: the pinned browser image requires a
Hex/Rebar/dependency bootstrap before the deployed canary, and persisted
LiveView fold state plus asynchronous UI saves require enabled-control waits
and open-or-verify fold assertions. The canary's output, history nonce cleanup,
logout, and redaction oracles were preserved.

The reconciled release candidate was measured once at
`plans/evidence/harden-llm/reconciled-release-eval.json` (SHA-256
`3948602734df92c6c44744bf47cf048381b10646d6d41e2ff26561340b9753ce`) under
manifest SHA
`4f20b980f0fa5527898f2cfdfc7be8a9e07730d0e7eb27bbb4fc693ed4a206ca`.
All 25 selected tasks passed with zero failures/leaks and two service-pool
starts; the graph observed `244253 ms` critical-path wall time,
`1194.3046875 MiB` peak RSS, and aggregate task CPU p50/max
`4650/142340 ms`. This is an observation, not a new timing budget.

Hosted certification for PR `#37` was the explicit workflow-dispatch run
`32716112245`: fast passed in 3m01s, integration in 2m47s, browser in 2m23s,
and release in 16m07s. CodeQL actions, Go, and JavaScript analyses passed in
run `32715724034`. The initial pull-request event ran fast and skipped the
expensive lanes because the `test:full` label was attached after event
evaluation; the explicit full dispatch on the exact PR SHA supplied the
required lane evidence.

The pre-deployment identity check correctly rejected the running image before
authenticated browser work when given the merged expected release. The
production application-bearing release is
`761f5f76d0262ca29973fda2ca3d5214dfa920c0` (PR `#35`); later merges
`23538c2ad7d59f4becd0e8e2676d205ba597f8cb` and
`40083fd980a4c88c344dfa884c90a2edbbcc7111` changed only certification
tooling/tests and documentation, so neither is misrepresented as the image
release.

| Surface | Certified value |
| --- | --- |
| Frontend release/container | `761f5f76d0262ca29973fda2ca3d5214dfa920c0`; container `1cd12739e02c`; image `sha256:982d82e91eb2a89ea7c033457eb9297168529bb998786ecf5e17e68bf7188366`; `healthy` |
| Gateway release/container | `761f5f76d0262ca29973fda2ca3d5214dfa920c0`; container `142855d29b0b`; image `sha256:110a136efd631d5f4f6af3cb92e5dd16bb7a61641b2a6812bb56d6c896d8dd83`; `healthy` |
| Compose project | `harden-llm`, production env supplied with Compose `--env-file`; named volumes retained |
| TEST-056 | pass: public identity/health, login, CPA GPT-5.6 Luna, all nested folds, nonempty output, request/response details, exact nonce history deletion, logout, and redaction |
| Sustained public probes | three samples each of frontend `/healthz`, frontend `/login`, API `/healthz`, and API `/readyz`; every response HTTP 200 |
| Cleanup | zero task-owned test containers/volumes; exact exited stale `harden-llm-otel-collector-state-init-1` orphan removed; frontend scratch directories empty |

The first deployment invocation shell-sourced JSON environment values and
left the gateway unhealthy because embedded JSON quotes were stripped. No data
or volumes were lost. The deployment was recovered with Compose's native
`--env-file` parser, and the recovered stack plus TEST-056 passed. No DNS,
provider policy, API contract, retry/timeout budget, or test threshold was
changed.

## Shared PRLS observability deployment (2026-08-24)

While the observability branch was being prepared, PR `#40` merged the exact
same repository tree into `main` as
`5b830904e7d7a2e7a0a8d7d271969e10c38397a1`. The pending PR `#41` was therefore
closed as a duplicate after verifying that its tree and `origin/main` were
identical; no duplicate merge or review gate was required. The only surfaced
working-tree correction was the explicit `masked-recall-api.prls.co` proxy
target for the shared production container. Its deployment-test oracle now
requires that target exactly.

Final local validation on the merged tree:

- The deterministic backend verification passed, including Loki schema
  validation, Go tests, parity, integration and race tiers, API and
  observability tests, and vulnerability scanning.
- `make test-compose` passed in `285.042s` on the merged observability tree.
- `make test-release` accepted all 25 selected tasks with zero failures and
  zero cleanup errors. The release graph included the final backend
  verification and both Compose browser checks.
- A first release attempt stopped before tests because the clean checkout had
  no fetched Phoenix dependencies; the lockfile dependencies were fetched
  with Hex/Rebar and the unchanged release command was rerun. One transient
  frontend recovery assertion was reproduced and passed on a targeted rerun;
  no assertion or resource threshold was weakened.

The production Compose project was rebuilt with release
`5b830904e7d7a2e7a0a8d7d271969e10c38397a1`, using Compose-native environment
files and retaining all named volumes. The application images are:

| Surface | Certified value |
| --- | --- |
| Frontend | release `5b830904e7d7a2e7a0a8d7d271969e10c38397a1`; container `98c00d5b6dc0`; image `sha256:f6879e443e85d5243f563e8398d6a0883bbebcde4924f464213cf8bb41f3b2e3`; healthy |
| Gateway | release `5b830904e7d7a2e7a0a8d7d271969e10c38397a1`; container `18a56b568e5d`; image `sha256:4706a01d669801176ec95a217331d483b4d606eadb9ab871f55e3c3dd79d0aef`; healthy |
| Public probes | three samples each: frontend `/healthz`, frontend `/login`, API `/healthz`, and API `/readyz`; every response HTTP 200 |
| TEST-056 | accepted deployed canary; release identity matched, authenticated browser workflow passed, and nonce history cleanup was asserted |
| Ingress | Caddy configuration validated; the shared masked-recall target resolved on `prls-observability` and its unauthenticated protected endpoint returned HTTP 401 |
| Cleanup | task browser containers and volumes absent; exact exited `harden-llm-otel-collector-state-init-1` one-shot container removed; generated frontend scratch removed |

The canary used the existing operator credentials from the retained host
configuration. No credential, provider output, or live diagnostic payload was
written to Git. No new ADR or KER was needed: the change preserves the
accepted single-host topology, API ownership, test hierarchy, provider policy,
and timeout/retry budgets.

## Embeddable widget parity release candidate (2026-08-25)

The utility-informed widget follow-up is tracked separately from the earlier
production certifications under `PLAN-HLLM-WIDGET-PARITY-001`. The accepted
local release candidate, merged application commit, and verified production
identity are recorded below. The application-bearing release is
`84a06fa38da24bacbb5ffc537de509e77b0cb82b`; this section records its
post-merge production verification.

- Utility source: `/home/kirill/p/utility-llm` at
  `5c0309e2508dc5b7a87d0880c8d794123353c5b0`, clean and read-only.
- Deterministic frontend: 107 passed, 4 excluded (`browser`, `compose`, and
  `deployed` tags); Node client core 8/8; traceability 18/18; tier verifier
  fast-task count 8.
- Release graph: captured `accepted=true` for 26 tasks with `failure=null` and
  `cleanupErrors=[]`.
- Final benchmark: 32 samples over seeds `104729`, `130363`, and `155921`,
  zero failures, and zero leaked resources. Redacted metrics are recorded in
  `ker/widget-parity/evaluation.json` and the KER baseline.
- Environment deviation: this host has no Mix executable, so Phoenix tests use
  the pinned `harden-llm-browser-test:local` image; the wrapper permits network
  only for Hex dependency-audit tasks and keeps deterministic tasks offline.
  This changes execution tooling, not the test purpose or application code.

The implementation commit `e175cb4` was merged through PR `#42` as
`84a06fa38da24bacbb5ffc537de509e77b0cb82b`. The exact merged images were built
once and deployed to the authorized `harden-llm` production Compose project
with `--no-build`; both containers were healthy and their labels matched the
merged SHA:

| Surface | Release label | Image ID/digest | Runtime result |
| --- | --- | --- | --- |
| Frontend | `84a06fa38da24bacbb5ffc537de509e77b0cb82b` | `sha256:76d7140345765903249eff1c1b30b0824a42501a2ea11594e6be21860950a757` | healthy |
| Gateway | `84a06fa38da24bacbb5ffc537de509e77b0cb82b` | `sha256:97eefe84b1b560f208cf99d48e78ab6999fcdb4811b33c2486ea730d98c033c5` | healthy |

The canonical deployed launcher passed with the approved environment:
frontend `/healthz` and `/login`, API `/healthz` and `/readyz`, and three
sustained probe samples all returned HTTP 200. TEST-118 passed the
authenticated CPA GPT-5.6 Luna workspace flow, nested folds, output,
request/response details, exact nonce history deletion, logout, and redaction;
no screenshots or task-owned browser artifacts remained.

P05.S04's staged-promotion policy is the one documented deviation: this host
has no separate authorized verification deployment target or immutable image
transport. The isolated release Compose/browser gate passed before merge; the
user authorized production completion, so the exact two merged images were
verified directly on production. This is recorded as direct production
verification, not mislabeled staged promotion. Named production volumes were
retained.

## Utility profile defaults and preset follow-up (2026-08-26)

This follow-up closes the utility-llm defaults, help-marker, and backend-preset
coverage gap identified after the embeddable widget release. It keeps the
server-owned LiveView boundary: no browser-test permutations were added for
defaults, catalog rendering, or profile/model synchronization.

- Utility source remained `/home/kirill/p/utility-llm` at `5c0309e`; the
  normalized utility catalog and `internal/profiles/default-profile-catalog.json`
  are equal and contain exactly 28 profiles.
- The application-bearing implementation is `4f4f655`, with the frontend
  runtime pin correction in `c43f097`. `ProfileDefaults` now supplies the
  utility-aligned option, retry, escalation, pricing, profile/model,
  reasoning, cache, and contextual `?` help-marker defaults.
- `WEB-TEST-052` through `WEB-TEST-055` cover the defaults, rendered help
  markers, all 28 backend catalog options, empty-state `CPA GPT-5.6 Luna`
  selection, and non-default preset/model synchronization through ExUnit and
  `Phoenix.LiveViewTest`.
- The pinned deterministic frontend suite passed with 114 tests and 4
  exclusions. The focused workspace LiveView suite passed 25 tests, and the
  exact catalog comparison passed with no diff. `make test-fast` and
  `git diff --check` passed on the final checkout.

The application-bearing frontend deployment was verified directly in the
authorized production Compose project. The test/documentation follow-up does
not alter the runtime image, so no rebuild was needed after `c43f097`:

| Surface | Release label | Image ID/digest | Runtime result |
| --- | --- | --- | --- |
| Frontend | `c43f097` | `sha256:ce9859beb10a631aa1400957a2807f3ff56f2960b672ca465d9ace4819b8240b` | healthy |
| Gateway | `84a06fa38da24bacbb5ffc537de509e77b0cb82b` | `sha256:97eefe84b1b560f208cf99d48e78ab699fcdb4811b33c2486ea730d98c033c5` | healthy, unchanged |
| Public probes | frontend `/healthz`, `/login`; API `/healthz`, `/readyz` | HTTP 200 | pass |
| TEST-118 | authenticated deployed canary | release identity matched; workspace folds, bounded CPA smoke, exact smoke-history cleanup, logout, and redaction passed | pass |

The new LiveView tests are the authoritative regression coverage for these
server-owned invariants. The existing deployed browser canary remains the
minimal native LiveSocket/combobox and production-boundary check.

## Output trace details hardening (2026-08-28)

Application commit `fcda74b3824fc22a517df709f0a67939b8aa0b9c` was pushed to
`origin/main`, built once, and deployed with `--no-build --no-deps` to the
authorized `harden-llm` production Compose project. The deployment replaced
only the gateway and web containers and retained the Postgres, Garage,
frontend-session, Langfuse, and observability volumes.

The output trace details now project immutable run identity and diagnostics
through `HardenLlm.LlmTraceProjection`; the LiveViews own loading and events,
while `LlmTraceComponents` remains storage- and transport-independent. Product
history, aggregate statistics, trace observations, and artifact metadata use
the application Postgres database. Artifact bodies use Garage. Aggregate
statistics are computed directly from owner-scoped `llm_runs`; the unused
`llm_stats_totals` projection was empty before migration `2` removed it.

Deletion removes Garage objects before transactionally removing run, trace,
observation, and artifact metadata. Run persistence commits the run, trace,
observations, and artifact references in one Postgres transaction and cleans
uploaded Garage objects if that transaction fails. Failed and zero-token runs
remain traceable through immutable persisted metadata.

| Gate or production check | Result |
| --- | --- |
| `make test-fast` | accepted: 8 tasks, no failure or cleanup error |
| `make verify` | passed: deterministic backend, integration, API, observability, race, and vulnerability gates |
| `make test-browser` | accepted: 2 Chromium tasks, including computed cache-icon and trace-control/cURL assertions |
| `make test-release` | accepted: 26 tasks, no failure or cleanup error |
| Database migration | versions `1` and `2` applied; `llm_stats_totals` absent |
| Public probes | three rounds of frontend `/healthz` and `/login`, plus API `/healthz` and `/readyz`, returned HTTP 200 |
| Stats API | authenticated `GET /api/v1/stats` returned HTTP 200 with all 19 required aggregate fields |
| TEST-118 | authenticated CPA GPT-5.6 Luna run, same-row trace controls, unboxed cache icon, request/response details, public-origin credential-placeholder cURL, exact smoke-history deletion, logout, and redaction passed |

| Surface | Release label | Image ID/digest | Runtime result |
| --- | --- | --- | --- |
| Gateway | `fcda74b3824fc22a517df709f0a67939b8aa0b9c` | `sha256:d04c669ba75ce7e94442d0e3fa77358ebd24df6716d01a488358518444bc90d7` | healthy |
| Frontend | `fcda74b3824fc22a517df709f0a67939b8aa0b9c` | `sha256:281bba025f97fad8129df46b179b7b6b1b027d1be1578760adf5f95148da3d53` | healthy |

The output widget does not query telemetry backends. The default application
OTLP receiver exports traces to Tempo and a filtered/tail-sampled copy to
Langfuse, metrics to Prometheus, and logs to Loki. The separate PRLS receivers
export traces to Laminar. ClickHouse is owned by Langfuse and is not an
application source of truth. At certification time the Collector target was
up, Tempo and Langfuse had accepted spans, Loki had accepted logs, Prometheus
had accepted metric points, and no span, log, or metric exporter-failure
series was present.

## LLM stats and reusable JSON viewer (2026-09-09)

The application-bearing checkpoint `21d53f3d5fd34ceb3bc9d8c992c46d17b08a118f`
was pushed to `origin/main`, built once, and promoted with
`--no-build --no-deps` to the existing production Compose project. The
test-only deployed-canary follow-up `1c26356c6024df1de48324e0f85d779763431edc`
was pushed afterward and does not change the runtime image.

This checkpoint separates aggregate stats presentation into
`LlmStatsComponents`, extracts a transport-free type-preserving `JsonViewer`,
uses it for Output and History structured data, makes Details/JSON/Request/
Response panes independent, preserves folds across unrelated patches, removes
the cache-icon decoration, and coalesces slow UI-preference saves so the stats
row remains interactive. No Go, OpenAPI, storage, or telemetry path changed.

| Gate or production check | Result |
| --- | --- |
| `make test-fast` | accepted: 8 tasks, no failure or cleanup error |
| `make test-browser` | accepted: 4 tasks, no failure or cleanup error |
| `make test-release` | accepted: 26 tasks, no failure or cleanup error |
| Workspace LiveView regression | 40 passed, including slow UI-preference-save coalescing |
| Frontend image | `sha256:247cfd6c1ba9c630851388720d6c7e75bf208a0e74721037629f08a8790ab371`, OCI release `21d53f3d5fd34ceb3bc9d8c992c46d17b08a118f`, healthy |
| Gateway | `sha256:924f573041275184ea12df883afd89610aa14d8163bfe1fae4badc0fdf10a20f`, release `729804cdfab9ff5c2e9dd75c64609cddd6773557`, healthy and unchanged |
| Public probes | frontend `/healthz`, `/login`; API `/healthz`, `/readyz`: HTTP 200 |
| Authenticated deployed canary | exact frontend identity, trace controls/panes, bounded CPA smoke, smoke-history cleanup, logout, and redaction passed |

The widget reads product data from the existing application sources: execution
and aggregate facts from PostgreSQL, immutable trace/artifact bodies from
Garage, and existing REST projections through `HardenAPI`. Telemetry remains a
downstream projection: OTLP sends application traces to Tempo and filtered
traces to Langfuse, metrics to Prometheus, and logs to Loki; Laminar is a
separate PRLS receiver path. Langfuse-owned ClickHouse is not queried by the
widget and is not a product source of truth.

## Output control row simplification (2026-09-10)

Application commit `03645c99877a4fa66fd3e9aa6cf47edc4e338283` was pushed to
`origin/main` and deployed to the existing production Compose project. Only
the frontend service was replaced, using its once-built image with
`--no-build --no-deps`. The output row now contains `Details`, `JSON`,
`Request`, `Response`, `Copy cURL`, in that order. The inert output
`trace · N bytes` metadata and its unused component helpers/CSS were removed.
Inline JSON and History artifact downloads remain unchanged.

| Gate or production check | Result |
| --- | --- |
| Focused trace component tests, WEB-TEST-036 | 7 passed |
| `make test-fast` | accepted: 8 tasks; no failure or cleanup error |
| `make test-release` | accepted: 26 tasks, including Chromium and Compose checks; no failure or cleanup error |
| Formatting and `git diff HEAD --check` | passed |
| Frontend image | `sha256:9f47c18058020623917a25938127a6d14076689725a592f1101391316f69af19`, OCI release `03645c99877a4fa66fd3e9aa6cf47edc4e338283`, healthy |
| Gateway | `sha256:924f573041275184ea12df883afd89610aa14d8163bfe1fae4badc0fdf10a20f`, release `729804cdfab9ff5c2e9dd75c64609cddd6773557`, healthy and unchanged |
| Public probes after deployment | frontend `/healthz`, `/login`; API `/healthz`, `/readyz`: HTTP 200 |
| Authenticated deployed canary, WEB-TEST-048 / TEST-118 | **Failed** at the smoke History removal assertion, after passing exact release identity, the bounded provider run, control order/labels, inline pane, and cURL payload assertions |

The failed canary expected `#workspace-history-PM6TYB-0MTwv-tgZ5x33fw` to
disappear at `frontend/test/browser/deployed_canary_test.exs:186`. A separate
authenticated, read-only History API check returned HTTP 200 and confirmed
that this new smoke record was absent. That diagnostic session logged out
successfully (HTTP 200). The browser assertion failure is retained: it does
not establish why the UI failed to observe cleanup, and the canary's later
logout assertions did not run. No repeat provider run or History behavior
change was made. Deployment is complete; full hosted canary certification
remains unaccepted.

The previous compatible frontend image was retained as
`harden-llm-web:rollback-21d53f3`, image
`sha256:247cfd6c1ba9c630851388720d6c7e75bf208a0e74721037629f08a8790ab371`.

## Overview default and output panel labels (2026-09-10)

Application commit `333f6465567cb512f71894f00000bf3f2318ff36` was pushed to
`origin/main`, built once, and deployed to the existing production project
with `--no-build --no-deps`, replacing only the frontend. The row is now
`Overview`, `Details`, `cURL`, `Request`, `Response`. This restores the
original cURL label/position while retaining removal of the inert artifact
label. Overview is the curated summary; Details is the full trace JSON.
Expanding the stats row opens Overview and preserves other pane selections.
The existing preference-save path persists both flags together, including
while a previous save is pending. No storage or API contract changed.

| Gate or production check | Result |
| --- | --- |
| Focused component and workspace tests | 47 passed; the new Overview reopening regression failed before the implementation and passed afterward |
| `make test-fast` | accepted: 8 tasks; no failure or cleanup error |
| `make test-release` | accepted: 26 tasks, including native Overview reopening and clipboard checks; no failure or cleanup error |
| Formatting and whitespace | passed |
| Frontend image | `sha256:869377cd70ef5913f19e4030bd0b6a262ad80e9fe0bb0caae7ffd47cd27847b7`, OCI release `333f6465567cb512f71894f00000bf3f2318ff36`, healthy |
| Gateway | unchanged: `sha256:924f573041275184ea12df883afd89610aa14d8163bfe1fae4badc0fdf10a20f`, release `729804cdfab9ff5c2e9dd75c64609cddd6773557`, healthy |
| Public probes | frontend `/healthz`, `/login`; API `/healthz`, `/readyz`: HTTP 200 |
| Hosted canary, WEB-TEST-048 / TEST-118 | exact identity, Overview default, labels/order, inline panels, provider run, and cURL payload assertions passed; **overall failed** at the pre-existing History cleanup assertion (`deployed_canary_test.exs:186`) |

The failed canary left `Augi38De8Jnyf90cYfKehQ` in owner-scoped History.
A separate API check verified its exact ID and canary nonce before deleting
only that smoke record (HTTP 200); a subsequent History read confirmed its
absence. Diagnostic and cleanup sessions logged out successfully (HTTP 200).
This cleanup does not turn the failed browser assertion into a pass, and its
later browser logout assertions did not run. No canary retry or History code
change was made. Full hosted canary certification remains unaccepted.

The previous frontend image remains available as
`harden-llm-web:rollback-03645c9`, image
`sha256:9f47c18058020623917a25938127a6d14076689725a592f1101391316f69af19`.

## Shared Result and workspace History cards (2026-09-10)

Application commit `6baefd34979661b5bb52a49345d83de61c429395` was pushed to
`origin/main`, built once in the clean production worktree, and deployed with
`--no-build --no-deps`, replacing only the frontend service. Result and workspace
History now share a transport-free card with input, output, and stats rows.
Each text row has an accessible emoji copy button; one local expansion control
reveals both full values. Stats retain inline folding JSON, with rerun immediately
after cURL. Rerun submits the server-owned recorded request through the existing
execution lifecycle, preserves its cache mode and the editor draft, and prevents
duplicate active submissions. No backend, OpenAPI, storage, or dependency change
was made. See [the implementation and ownership plan](../plans/reusable-result-widget-plan.md).

| Gate or production check | Result |
| --- | --- |
| Deterministic frontend tests | 166 passed, 4 opt-in tests excluded; WEB-TEST-066/067 cover shared cards and recorded rerun |
| `make test-fast`, application checkpoint | accepted: 8 tasks, no failure or cleanup error |
| `make test-browser` | accepted: 4 tasks; repeated successfully after the test-only History cleanup correction |
| Full release selector, application checkpoint | accepted: 26 tasks, including native browser, frontend/backend Compose, packaging, and backend verification; no failure or cleanup error; local report `tmp/result-widget-release.json` |
| Formatting and whitespace | passed |
| Frontend image | `sha256:f7b76033fbd8f5ba62e61b4d766340fba9dedfac896e00f28452d6913b98d555`, OCI release `6baefd34979661b5bb52a49345d83de61c429395`, healthy |
| Gateway | unchanged: `sha256:924f573041275184ea12df883afd89610aa14d8163bfe1fae4badc0fdf10a20f`, release `729804cdfab9ff5c2e9dd75c64609cddd6773557`, healthy |
| Public probes | frontend `/healthz`, `/login`; API `/healthz`, `/readyz`: HTTP 200 |
| Authenticated deployed canary, WEB-TEST-048 / TEST-118 | **accepted**: exact release/image identity, bounded provider run, trace controls and inline data, cURL payload, History DOM removal, and logout |
| GitHub application-checkpoint workflow | fast and integration passed; browser and release jobs failed during test-image provisioning, before tests ran; see [run 34501081567](https://github.com/prls-co/harden-llm/actions/runs/34501081567) |

The first hosted canary failed at its existing History cleanup assertion. The
frontend logged a successful `deleteHistory` followed by `listHistory`; an
owner-scoped API check confirmed smoke record `_FYZr9-k5CecxP7h9Mtibg` was absent
and logged out successfully. Wallaby's `refute_has` queries presence and fails
immediately if the old row is found; it does not wait for the asynchronous DOM
removal. Test-only commit `e9e22cb01332a30e511a27414672cf72b4bfd24b`, pushed to
`origin/main`, changes both canaries to await an exact DOM count of zero,
including hidden elements. The existing timeout and deletion oracle are not
weakened. All 166 deterministic frontend tests and the targeted browser gate
passed afterward; a fresh hosted canary then passed against the unchanged
application image above. Runtime sources, assets, configuration, dependencies,
and Dockerfile were verified unchanged between these two commits.

GitHub's separate provisioning failure is a pre-existing
`frontend/Dockerfile.browser` pin: Alpine now offers `curl-8.22.0-r0` instead of
the required `curl=8.20.0-r0`. Local browser/release checks ran using the existing
pinned test image, `sha256:84fb69e72902863edb423c183d0c79ba7c2e7eb39f85e56a111268e28f801fad`.
This cold-build CI limitation remains unresolved and is not reported as a pass.

Rollback remains available as `harden-llm-web:rollback-333f646`, image
`sha256:869377cd70ef5913f19e4030bd0b6a262ad80e9fe0bb0caae7ffd47cd27847b7`.

## Compact workspace stats ownership (2026-09-10)

Application commit `378b5727116db4c34457dc35bba635133c675c4a` was pushed to
`origin/main`, built once in the clean production worktree, and deployed with
`--no-build --no-deps`, replacing only the frontend. The workspace's bottom LLM
stats panel and per-card "Inspect in audit history" shortcut are removed, along
with the unused aggregate snapshot loading, polling, and refresh callbacks.
Inline Result/History stats remain unchanged. View all still opens the audit
History page, where owner-wide aggregate totals and their refresh lifecycle
remain available. No API, storage, telemetry, or dependency changes were made.

| Gate or production check | Result |
| --- | --- |
| Focused Workspace/History tests, WEB-TEST-068 | 54 passed; the new no-aggregate-request regression failed before implementation and passed afterward; aggregate retry/snapshot/polling coverage retained on the audit page |
| `make test-fast` | accepted: 8 tasks, no failure or cleanup error |
| Full release selector | accepted: 26 tasks, including native browser and frontend/backend Compose; no failure or cleanup error; local report `tmp/compact-workspace-release.json` |
| Formatting and whitespace | passed |
| Frontend image | `sha256:9b902d8c412ede4e1e13026f5f958c10cb5c03ce535e32b804755dd18204acf7`, OCI release `378b5727116db4c34457dc35bba635133c675c4a`, healthy |
| Gateway | unchanged: `sha256:924f573041275184ea12df883afd89610aa14d8163bfe1fae4badc0fdf10a20f`, release `729804cdfab9ff5c2e9dd75c64609cddd6773557`, healthy |
| Public probes | frontend `/healthz`, `/login`; API `/healthz`, `/readyz`: HTTP 200 |
| Hosted canary, WEB-TEST-048 / TEST-118 | accepted: exact image/release, removed controls absent from the DOM, bounded provider run, inline trace controls, History removal, and logout |

[GitHub run 34512584194](https://github.com/prls-co/harden-llm/actions/runs/34512584194)
passed fast and integration jobs. Browser/release jobs remain blocked before
tests by the existing `curl=8.20.0-r0` browser-image pin; Alpine now offers
`8.22.0-r0`. Local certification used the existing pinned browser image, as
documented above. This unrelated cold-build limitation is not reported as a pass.

Rollback remains available as `harden-llm-web:rollback-6baefd3`, image
`sha256:f7b76033fbd8f5ba62e61b4d766340fba9dedfac896e00f28452d6913b98d555`.

## Canonical workspace History (2026-09-10)

Deployed revision `37f39cfd9879fd69b97770afda892732995acc38` contains the UI
cleanup from `39316358bef562164a3f8483f50f6710f98f5090` plus a deterministic
test synchronization correction. Both checkpoints were pushed to `origin/main`;
only the final revision was promoted. It was built once in the clean production
worktree and deployed with `--no-build --no-deps`, replacing only the frontend.

Workspace History is now the only History UI. View all, the separate audit page,
its Domain trace dialog, aggregate statistics renderer/projection/polling, and
obsolete frontend helpers/tests/styles are removed. Old `/history` bookmarks
redirect to Workspace, preserving optional trace selection. Ten-card cursor
Load more preserves access to older results; explicit retries retain existing
cards, and clear-all invalidates stale in-flight pages. Inline Details retains
observations and discovers available downloads through the shared stats resource
projection. No backend/OpenAPI, storage, telemetry, or dependency change was
made. Removed UI code is recoverable from Git; existing History data was not
deleted by the cleanup.

| Gate or production check | Result |
| --- | --- |
| New workspace History regressions, WEB-TEST-008/033/036/069 | Pagination/redirect/retry regressions failed before implementation and passed afterward; cursor append/deduplication, disclosure retention, duplicate-event suppression, failure retry, clear/read races, inline JSON, artifacts, and trace authorization covered |
| Offline gate | `make test-fast`: accepted, 8 tasks; final release also passed all 160 deterministic Phoenix cases, 4 opt-in cases excluded |
| Targeted native browser gate | accepted, 4 tasks including both Chromium canaries; report `tmp/unified-history-browser-verified.json` |
| Full final release selector | accepted, 26 tasks including native browser, backend/frontend Compose, packaging, and baseline verification; no failure or cleanup error; report `tmp/unified-history-release-verified.json` |
| Formatting and whitespace | passed |
| Frontend image | `sha256:9b0205e2cc15b339a177d6a4376882904cf67a12793a1a325b54e962fe5cc394`, OCI release `37f39cfd9879fd69b97770afda892732995acc38`, healthy |
| Gateway | unchanged: `sha256:924f573041275184ea12df883afd89610aa14d8163bfe1fae4badc0fdf10a20f`, release `729804cdfab9ff5c2e9dd75c64609cddd6773557`, healthy |
| Public probes | frontend `/healthz`, `/login`; API `/healthz`, `/readyz`: HTTP 200 |
| Hosted canary, WEB-TEST-048 / TEST-118 | accepted: exact image/release, retired audit URL redirect, duplicate controls/dialog absent from DOM, inline trace JSON, bounded CPA run, smoke History record removal, and logout |

The first browser attempt exposed an overly broad new assertion: an expanded
JSON parent and child both contained `observations`. The assertion now targets
the unique JSON viewer container, retaining the content check. The first full
release attempt then exposed a pre-existing profile-fold test race: it clicked
Pricing before the preceding preference save completed. The test now joins
each persisted fold operation; a controlled pending-save case explicitly checks
Pricing disabled/enabled behavior. All 45 Workspace tests passed at the release
seed before the full gate was restarted. No assertion was bypassed, timeout
increased, or production interlock changed.

[GitHub run 34522860780](https://github.com/prls-co/harden-llm/actions/runs/34522860780)
passed fast and integration. Its browser/release jobs failed before tests:
the existing `frontend/Dockerfile.browser` pin requires `curl=8.20.0-r0`, while
Alpine offers `8.22.0-r0`. Local certification used the existing pinned browser
image `sha256:84fb69e72902863edb423c183d0c79ba7c2e7eb39f85e56a111268e28f801fad`.
The unrelated cold-build limitation remains open and is not reported as a pass.

Stats-row wrapping was investigated but not changed: inherited 14px/16px
typography, wrapping flex groups, and full-length identities explain the
inconsistency. The bounded next-step recommendation is recorded in
`plans/reusable-result-widget-plan.md`, section 4. Fixture screenshots were
inspected; the test image's missing emoji glyphs limit typography/line-break
claims, as documented there.

Rollback is `harden-llm-web:rollback-378b572`, image
`sha256:9b902d8c412ede4e1e13026f5f958c10cb5c03ce535e32b804755dd18204acf7`.
## Root route and compact trace stats (2026-09-10)

Application revision `1d7d7ede5b13cbb30a479dad8fd52ac1213a251a` was committed,
pushed to `origin/main`, built once in the clean production worktree, and
deployed through the documented Compose overlay with `--no-build --no-deps`.
Only the frontend container was replaced.

The authenticated application now lives at `/`; `/?trace_id=...` selects a
stored result. `/workspace` and `/history` are removed without redirects.
Login, navigation, trace patches, browser workflows, and route telemetry labels
use the current routes. The unused redirect controller and starter page/template
are deleted and remain recoverable from Git. Profile editing/export, embedding,
authentication, trace/artifact downloads, LiveView transport, and health routes
remain functional. Internal `/metrics` remains blocked by the public proxy.

The shared stats bar owns its 14px typography. Identity and metrics have explicit
layout groups; IDs/models truncate visually while retaining full DOM text and
hover titles. Metrics do not split internally. Desktop rows stay compact;
narrow cards wrap between groups or whole metrics without page overflow.
No dependencies, backend/OpenAPI, storage, or telemetry pipeline changed.

| Gate or production check | Result |
| --- | --- |
| Cheap regressions, WEB-TEST-005/036/069 | Root routing and compact identity regressions failed before implementation; final deterministic suite passed all 162 cases, with 4 opt-in cases excluded |
| Offline gate | accepted, 8 tasks; `tmp/root-stats-fast.json` |
| Targeted Chromium | accepted, 4 tasks including both canaries; `tmp/root-stats-browser.json`; native layout assertions at 900/700/320px in Result and History, with single-row desktop assertions and intact metrics |
| Rendered inspection | Result and History fixture screenshots inspected; matching compact desktop rows. The pinned test image lacks emoji glyphs, so this does not certify platform-specific glyph widths |
| Full release gate | accepted, all 26 tasks, no cleanup errors; `tmp/root-stats-release.json` |
| Frontend image | `sha256:501316e3d70025744f536b94769bedc8f2b2c2ef4b1943bf91c50b56d60f0b63`, OCI release `1d7d7ede5b13cbb30a479dad8fd52ac1213a251a`, healthy |
| Gateway | unchanged: image `sha256:924f573041275184ea12df883afd89610aa14d8163bfe1fae4badc0fdf10a20f`, release `729804cdfab9ff5c2e9dd75c64609cddd6773557`, healthy |
| Public route probes | Anonymous `/`: 302 to login; `/login` and frontend/API health/readiness: 200; `/workspace`, `/history`, and both trace-query variants: 404; public `/metrics`: 404 |
| Hosted canary, WEB-TEST-048 / TEST-118 | accepted: exact image/release, login and root application, inline trace details, bounded CPA run, deletion of its smoke History record, logout |

[GitHub run 34532899986](https://github.com/prls-co/harden-llm/actions/runs/34532899986)
passed fast and integration. Browser/release jobs failed before testing because
the existing browser Dockerfile pins `curl=8.20.0-r0` while Alpine offers
`8.22.0-r0`; this was verified in the current job log. Local certification used
the existing pinned browser image. Hosted CI is not reported as green.

Rollback: `harden-llm-web:rollback-37f39cf`, image
`sha256:9b0205e2cc15b339a177d6a4376882904cf67a12793a1a325b54e962fe5cc394`.

### Historical-data deletion boundary

No broad historical-data purge was performed in this checkpoint. Read-only
production inventory before the hosted smoke found `guest` with zero runs,
traces, or artifacts and eight cached outputs; `operator-local` had 25 runs,
25 traces, 25 artifacts, and 72 cached outputs. Twenty operator records were
pre-v2. The guest owner-deletion batch was completed. Delete all removes the
owner's executions/traces/artifacts through the existing journaled coordinator,
but not its operation cache or exported telemetry. The hosted smoke deletes its
own History entry but can add a cache entry.

The user was asked whether the purge should cover one account or all application
accounts while retaining configuration, sessions, and shared telemetry. That
choice remains unresolved. The old-data decoder remains until the retained
records' deletion scope is resolved; removing it now would break operator
History reads. No database volume, credentials, sessions, or telemetry was
deleted. This is a routing/layout clean cut, not a completed data/schema cutover.

## Approved execution-data purge and v2-only cutover (2026-09-10)

The user's subsequent approval resolved the preceding deletion boundary:
purge runs and caches for `guest` and `operator-local`, preserving profiles,
credentials, sessions, client settings, and telemetry.

The existing owner-history API and journaled artifact coordinator removed
25 operator runs, their 25 traces, and 25 Garage artifacts. Guest execution
history was already empty. A guarded, owner-scoped database transaction then
removed 81 operation-cache entries (8 guest, 73 operator). Independent database
counts confirmed zero runs, traces, artifacts, and cache entries for both
owners. `audit-artifacts` independently reported zero objects and metadata
references, no missing or unreferenced objects, and `healthy:true`.

Before/after configuration fingerprints matched, and every pre-existing API
session row was unchanged. Only the purge's temporary authenticated session
was logged out. No session-vault volume, telemetry store, backup, or unrelated
application was purged. The application cannot undo the deletion; recovery
would require a pre-existing backup. Completed deletion journals retain
operational metadata, not the deleted output bodies.

Application revision `2c0478c83c698af1524bc8c3525c0d792e6ab2be` removes retained-v1
execution decoders, projection aliases, OpenAPI schemas, and the obsolete
`reconcile-history` CLI/backend path. History and trace reads use the same
canonical v2 decoder as fresh results and reject inconsistent wrapper
identities/statuses. Current artifact audit, journal recovery, and execution
deletion remain. Historical SQL migrations are unchanged; current profile,
client-state, and semantic-operation version-1 contracts are unrelated and
remain supported. This removes 1,123 net lines without adding a new service,
adapter, dependency, or permanent purge endpoint.

Cheap regressions failed before implementation for retained-data acceptance,
the retired command, and retained OpenAPI schemas. After implementation, all
164 deterministic Phoenix tests passed (4 opt-in cases excluded), focused Go
tests passed, and the offline gate accepted all 8 tasks in
`tmp/v2-cutover-fast.json`. Relevant contracts include TEST-022/026/061 and
WEB-TEST-063 under their canonical specifications.

### Certification blocker: no application promotion

The exact application revision was pushed to `origin/main`. The full local
release selector in `tmp/v2-cutover-release.json` did **not** pass: 23 tasks
passed, `frontend-compose` failed, and the remaining packaging/baseline tasks
were not run (status 125). Cleanup completed without errors. The passing
tasks include backend Compose, integration/race/observability, native browser
canaries, deterministic frontend/client tests, and dependency checks.

The failure is at `ComposeSmokeTest.assert_telemetry!/2`: its 150-second Tempo
poll did not obtain a trace containing both `harden-llm-web` and
`harden-llm-gateway` plus the domain trace ID. The preceding browser run and
result-ID assertions passed. The final diagnostic is only `:retry`; retained
evidence cannot distinguish an absent search result from incomplete
cross-service spans. The test fixture was automatically removed. This is an
unresolved correlation failure, not a proven application regression or a
certified transient. No timeout/assertion was weakened and no unexplained
retry was used to obtain a pass. Diagnosing that boundary is the next release
entrypoint; application promotion remains blocked.

[GitHub run 34540870988](https://github.com/prls-co/harden-llm/actions/runs/34540870988)
passed fast and pooled integration. Browser/release jobs failed before tests
because `frontend/Dockerfile.browser` pins `curl=8.20.0-r0` while Alpine offers
`8.22.0-r0`, confirmed in this run's job log. Local certification used the
existing pinned browser image; that does not certify hosted cold builds.

No candidate container was deployed, and no hosted live-provider canary was
started. Production remains healthy at these unchanged identities:

| Service | Running image | Application revision |
| --- | --- | --- |
| Gateway | `sha256:924f573041275184ea12df883afd89610aa14d8163bfe1fae4badc0fdf10a20f` | `729804cdfab9ff5c2e9dd75c64609cddd6773557` |
| Frontend | `sha256:501316e3d70025744f536b94769bedc8f2b2c2ef4b1943bf91c50b56d60f0b63` | `1d7d7ede5b13cbb30a479dad8fd52ac1213a251a` |

Final independent database and Garage checks again confirmed zero runs,
traces, artifacts, cached outputs, and trace objects for the approved scope.
Public frontend health/login and API health/readiness returned 200;
`/workspace` and `/history` remained 404. The production worktree was restored
to its prior frontend revision after candidate preparation was abandoned.

Rollback preparation retained `harden-llm-web:rollback-1d7d7ed` at the running
frontend image. The running gateway's original image manifest was no longer
available locally, so `harden-llm-gateway:rollback-729804c` was rebuilt from its
exact prior source, producing
`sha256:7f0dc0671c229a546dd0023553f14d2eb9c39bb1cfa3949120e856ad8b433e25`.
Its binary version and OCI version label both matched the prior gateway SHA;
this is a rebuilt rollback image, not the original running image. The
temporary source worktree was removed, and mutable gateway/frontend image
tags were restored to the verified rollback images without restarting services.

## Paired result emoji controls and production release repair (2026-09-10)

Application candidate `effc8a27befb5a5d7cd9b394507185f77eb609bc` includes the
previously pending v2-only cutover and Clear Prompt rename. The shared Result
card now uses `📥` and `📤` buttons in place of Input/Output labels. Either
button toggles both text rows, with synchronized `aria-expanded` and scoped
`aria-controls`; the trailing expand button and unused label CSS are removed.
Current Result and History reuse the same component. Copy values, stats,
collection actions, transport ownership, and persisted data are unchanged.
The existing Phoenix JS commands own the local disclosure; no new client state,
hook, application dependency, or service is introduced for this interaction.

WEB-TEST-066's regression failed before implementation. The first targeted
Chromium gate passed all four tasks, including mouse and Enter/Space toggles,
paired state, History folding, clipboard, and existing responsive layout.
Reports: `tmp/result-emoji-fast.json`, `tmp/result-emoji-browser.json`.
Screenshot inspection exposed the test image's previously documented missing
emoji glyphs. The browser Dockerfile now pins `font-noto-emoji=2.048-r0`;
`fc-match emoji` resolves Noto Color Emoji. It also updates the unavailable
curl pin to `8.22.0-r0`, verified against the pinned image's Alpine repository.
An uncached build with the corrected curl pin succeeded, followed by the
font-inclusive image build:
`sha256:79123817638bf60b80b4997082326472d8b0b148f93c1939298e1f2d598b7f1b`.
These packages belong only to the test image, not the production frontend.

### Telemetry startup root cause and regression boundary

The instrumented diagnostic run in `tmp/result-emoji-compose-diagnostic.json`
passed, but direct inspection during startup captured
`OTLP exporter failed to initialize with exception :error::badarg` and no
frontend traces while gateway traces were present. The generated production
`start.script` conclusively started the SDK before `gproc`, `grpcbox`, and
`opentelemetry_exporter`. Correct ordering in `extra_applications` and the
compiled application's dependency list did not establish release boot order.
The SDK's batch processor disables span insertion when exporter initialization
fails and later attempts initialization again; early spans can be lost. The
diagnostic pass was therefore not treated as resolution of the earlier failure.

Mix now explicitly places the exporter before the SDK in release applications
and lists the exporter first among the telemetry dependencies. Existing
permanent application modes and the telemetry architecture are unchanged.
The new offline WEB-TEST-009 configuration regression failed before this fix
and passed afterward. The Compose boundary additionally reads the actual boot
script and rejects exporter initialization failures, while retaining its
frontend/gateway Tempo correlation, Loki, Prometheus, and Grafana checks and
their original deadlines. Failure diagnostics now distinguish absent Tempo
search results from missing services/domain correlation without logging bodies.
See the linked upstream startup-order guidance in `docs/self-hosting.md`.

Final fast gate: accepted all 8 tasks; 166 deterministic Phoenix tests passed,
4 opt-in tests excluded; `tmp/result-production-fast.json`. The candidate was
committed and pushed to `origin/main` before full release certification.

### Race-gate timeout fixture repair

The first full gate (`tmp/result-production-release.json`) rejected the
candidate in `go-integration-race`: TEST-025 returned the correct 504 but
reported `calls=0`. The test required profile/database I/O to finish within
the HTTP request's 10ms budget before its blocking caller could start. That
timing assumption is not a production contract.

The exact one-call/deadline-cancellation oracle now runs against the real
RunService and Postgres at the service boundary, where its existing caller
deadline starts after profile lookup. The real HTTP route separately retains
504, cancellation of any started caller, no retry, and rejection of a requested
timeout increase. Default-tag T1 cases cover deployment bounds, including the
unchanged 60-second maximum. No application code, timeout, assertion deadline,
or required service boundary was changed to address this test defect.

### Completed production promotion

Final source `3a721a1fcc72d524800aac27b95729be0b8ce342` was committed and pushed
to `origin/main`. The complete local release selector accepted all 26 tasks
with zero failures and cleanup errors in
`tmp/result-production-release-verified.json` (SHA-256
`88849abb17f787f7dd3a8751e8ec8d4ee4c742a76d723e184b1a9534351a7790`).
[GitHub run 34546660108](https://github.com/prls-co/harden-llm/actions/runs/34546660108)
also passed all four jobs for that exact SHA, including its cold browser-image
build and complete release gate. The earlier failed/cancelled runs are not
substituted for this evidence.

The font-inclusive Result and History screenshots were visually inspected:
both emoji controls render, copy actions remain at the right, and no trailing
expand control remains. Native browser checks passed at 900, 700, and 320px,
including click/keyboard disclosure, paired accessibility state, and patch
retention. Screenshot artifacts are disposable test fixtures, not production
data or committed screenshots.

Production deployment started at `2026-09-11T00:48:52Z` (September 10 local).
Images were built from the clean production worktree at the pushed source,
their version labels checked, then promoted with Compose
`up -d --no-build --no-deps --wait --wait-timeout 300` for only the gateway and
frontend. Both became healthy with matching source labels; the gateway binary
also reports the exact source SHA.

| Service | Live image ID | Retained release tag |
| --- | --- | --- |
| Gateway | `sha256:cd8a408899fe8dc478f799cd77e379ae11ae76a68b39ea777ab27018926c9736` | `harden-llm-gateway:release-3a721a1` |
| Frontend | `sha256:c02637b248639155e16a5664baa0dd97934bc2376939490131ef117bc6f0c7b0` | `harden-llm-web:release-3a721a1` |

Existing rollback tags documented above remain available. Database, Garage,
session-vault, and telemetry services/volumes were not replaced or purged.
The pending v2-only cleanup and Clear Prompt rename are now live as part of
this release; `reconcile-history` independently returns unknown command, while
the supported artifact audit remains healthy.

The authenticated deployed-browser canary accepted the exact live frontend
image using the existing CPA GPT-5.6 Luna profile. It checked Clear Prompt,
both emoji controls and synchronized disclosure, absence of the old expand
button, inline structured stats, History, and logout. Frontend health/login
and API health/readiness returned 200. Anonymous `/` redirects to `/login`;
`/workspace`, `/history` (also with a trace query), and public `/metrics` return
404. The application entry point is `https://harden-llm.prls.co/`.

Live release RPC confirmed boot order
`grpcbox,opentelemetry_exporter,opentelemetry`. Startup logs contained zero
exporter-initialization or observability-setup failures. Tempo trace
`9574ac2075eaf8c8e9f334f6af314eeb`, correlated to the successful canary's
frontend run log, contains both service names and `harden_llm.trace.id`.
Only correlation facts were recorded, not trace or request/response bodies.

The canary deleted its own History entry; a guarded transaction then removed
exactly its one cache entry, matching owner, version, operation hash, creation
timestamp, and smoke nonce. No broad historical purge was repeated. Before
and after this deployment, the new guest record remained at one run, trace,
artifact, and cache entry; operator-local returned to zero in each category.
Garage audit found the one referenced object, no missing/unreferenced objects,
and `healthy:true`. Profiles, credentials, sessions, telemetry, and backups
were preserved. The disposable smoke record/cache cannot be restored by the
application; no user record was removed in this promotion.

The durable, sanitized [production receipt](../plans/evidence/harden-llm/result-emoji-production-certification.json)
records each accepted local task, source/report identities, hosted workflow,
images, live probes, telemetry, and cleanup. `plans/implementation-status.json`
now points to this deployed application identity, so the default deployed
launcher no longer uses its older certification SHA. Subsequent documentation
commits do not change the certified application images.

### Existing dependency-alert qualification

The final push reported three open Dependabot alerts (#3/#4/#5) for indirect
`google.golang.org/grpc v1.83.0`: two high and one medium. The same dependency
was present in the prior deployed gateway revision; this release did not
introduce or upgrade it. The current release's `govulncheck ./...` passed with
zero detected affected calls/imported packages, while reporting four advisory
matches in required modules. That reachability result is not a claim that the
dependency version has no vulnerabilities.

The gateway uses gRPC as the OTLP client to its private Collector, not as a
public gRPC/xDS server; the production dependency graph has no xDS packages.
GitHub identifies 1.83.2 as covering all three reported alerts. Dependency
remediation remains a separate maintenance item; no alert was dismissed and
no dependency change was added to this certified UI promotion. This limitation
is included in the production receipt rather than silently treating the push
warning as resolved.
