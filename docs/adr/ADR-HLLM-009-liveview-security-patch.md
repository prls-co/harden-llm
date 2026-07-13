# ADR-HLLM-009: Patch the LiveView Version Pin

- Status: Accepted
- Date: 2026-07-13
- Requirements: SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 sections 3, 12, and 15
- Verification: WEB-TEST-001 through WEB-TEST-012, `mix deps.audit`, `mix hex.audit`

## Context

The frontend specification selected Phoenix LiveView 1.2.6 as its initial
exact pin. While implementation was being certified, EEF advisory
[CVE-2026-58228](https://cna.erlef.org/cves/CVE-2026-58228.html) identified a
URL-scheme validation bypass in versions 1.2.2 through 1.2.6. A leading ASCII
control or space byte could cause `<.link>` to accept a browser-normalized
`javascript:` URL. Version 1.2.7 is the upstream security release.

## Decision

Pin `phoenix_live_view` to 1.2.7 instead of the specified 1.2.6. Keep every
other frontend runtime and architectural boundary unchanged. WEB-TEST-001
also executes the advisory's leading-space regression so an unsafe downgrade
fails independently of the package audit feed.

## Consequences

The lock file intentionally differs from the specification by one security
patch release. The full controller, LiveView, real-browser, and Compose suite
must pass against that release, and both Hex audit gates must remain clean.

Rolling back to 1.2.6 is prohibited while the advisory applies. A future
upgrade follows the same exact-pin, audit, and full-certification process; it
does not require an application data migration or REST contract change.
