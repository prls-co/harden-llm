# ADR-HLLM-010: Reserve Overlay-Specific Read-Only Mount Points

- Status: Accepted
- Date: 2026-07-13
- Requirements: REQ-016, REQ-017 and SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 section 14
- Verification: TEST-033, TEST-034, WEB-TEST-009, WEB-TEST-012

## Context

The frontend specification placed its Caddy fragment below the base
`/etc/caddy/conf.d` bind mount and its dashboard below the base Grafana
dashboard bind mount. Docker cannot portably add a child bind mount beneath an
already read-only parent mount. The effective Compose project failed before
service startup on the certified Linux host.

## Decision

Reserve two independent, read-only overlay paths. Caddy imports
`/etc/caddy/overlays/*.frontend`, mounted from `deploy/frontend/`. Grafana uses
the separately provisioned `/var/lib/grafana/frontend-dashboards` directory.
The base configuration remains read-only and unchanged by the overlay; Caddy
still owns every public port, and Grafana still provisions the same three
backend datasources.

## Consequences

The filesystem paths differ from the illustrative paths in the frontend spec,
but ownership, routing, and service topology do not. Static Compose tests must
prove the mounts are read-only and non-overlapping. WEB-TEST-012 must validate
the effective sixteen-service project from a clean startup.

Rollback means removing the frontend overlay and its dedicated mounts. Moving
them back below a base read-only mount is prohibited unless the container
runtime demonstrates portable nested-mount support and the full Compose suite
passes.
