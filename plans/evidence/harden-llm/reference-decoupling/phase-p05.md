# P05 shared deployment topology checkpoint

P05.S01–S06 are complete. Canonical plan: `PLAN-HLLM-REFERENCE-DECOUPLING-002`
v5.3. Resume P06.S01. Production has not changed.

## Implemented boundary

Production runs one Go gateway and one Phoenix reference host on one public
hostname. Both REST surfaces use the configured bearer validator; Portal
admission is confined to Phoenix. The gateway uses an isolated HLLM database/
role on existing shared PostgreSQL and HLLM-scoped Garage credentials. Existing
`runApply` invokes `gateway-schema` from the exact accepted image before
application startup. No extra daemon, key issuer, alias or legacy data path.

Kirill approved existing PostgreSQL `compose-postgres-1`/`compose_default` on
2026-10-13. Concrete owner commands are in `docs/reference-production-cutover.md`.
No database/key/bucket was provisioned. Actual permission checks remain P06.
Caddy owner source is `feat/hllm-reference-routing` at `2511b74`, based on the
already-deployed Portal owner branch `b98dbc8`; its main is older `b611cfd`.
Preserve that Portal work instead of resetting the owner checkout to main.
The separate artifact hostname serves other products and is retained.

## Verified evidence

| Gate | Evidence | Result |
| --- | --- | --- |
| Shared topology / schema apply | S01 RED; `p05-s02-topology-green.log`, `p05-s05-schema-final.log` | pass; 25 production apply cases |
| Native deployment contracts | `p05-s05-compose-contracts.log` | pass |
| Same-origin Caddy owner routes | `p05-caddy-owner-final.log`, `p05-caddy-routes-final.log` | pass, including actual isolated HTTPS |
| Composed workflows | `p05-s03-compose-red.json`, `p05-s05-compose-final.json` | RED then full pass; 11/11 ready, 4/4 telemetry correlations |
| Fast gate | `p05-s05-fast-final.json` | all 10 tasks pass |
| Docs contract | `p05-s05-doc-contract.log` | pass |

Composed startup: 42.838 s; test: 140.919 s; runner wall: 141686 ms; command
peak RSS: 257.363 MiB (not total container RAM). Added long-lived production
services: 0. Test cleanup errors/warnings: 0. Browsers/real-provider calls: 0.
P05.S05's final composed run also supplies S06 measurements without repeating
the unchanged suite. The chronological log retains all failed attempts and
fixture corrections; no skipped/cancelled task is claimed as passing.

## Remaining release gates

P06 must qualify the exact merged SHA with the explicit browser-free release
workflow, provision only the approved HLLM database/role/bucket/key, build the
accepted images, apply schema before apps, publish Caddy/tunnel routing and
verify public HTTP/configuration identity. Preserve unrelated shared services
and remove only the obsolete HLLM container/volume after acceptance. Browser
layout, interactive signed-in production behavior and paid inference remain
unverified without explicit separate authorization.

Text-log indentation/trailing whitespace is normalized for Git; runner JSON
reports retain the exact redacted results. Assertion messages/outcomes are unchanged.
