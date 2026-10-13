# ADR-HLLM-011: Patch the Go Toolchain

- Status: Accepted
- Date: 2026-07-13
- Requirements: REQ-005, REQ-019 and WEB-TEST-012
- Verification: TEST-014, TEST-034, TEST-036, `go tool govulncheck ./...`

## Context

The implementation specifications selected Go 1.26.0. During P07,
`govulncheck` found reachable standard-library vulnerabilities in TLS, X.509,
HTTP, URL, and MIME-header paths. The newest finding, GO-2026-5856, required Go
1.26.5. The certified build subsequently advanced to the 1.26.6 patch release
with the same language and application contracts. These paths are relevant to
the public gateway and provider egress
boundary; suppressing the findings would not be a production-safe option.

## Decision

Require Go 1.26.9 in local certification, the gateway and fake-provider
builders, and the frontend browser-test image. Pin the official
`golang:1.26.9-alpine3.23` image by manifest digest. Keep the language/runtime
minor version and every application contract unchanged.

## Consequences

Builders may use Go's authenticated toolchain download when the host has an
older 1.26 patch. Release certification must report the selected toolchain and
must fail on any reachable vulnerability. All deterministic, race, integration,
and browser-free Compose gates are rerun after the patch. Browser checks remain
explicit user opt-ins under ADR-HLLM-015.

Rolling back to Go 1.26.0 is prohibited while the advisories apply. A future
patch upgrade follows the same image-digest, vulnerability, and full-test flow.

## 2026-10-13 security refresh

P06 release run 38373119904 found 11 reachable advisories in the Go 1.26.6
standard library, including GO-2026-6617 in `net/http` and `x/net/http2`.
[Go 1.26.9](https://go.dev/doc/devel/release#go1.26.9) and
[`golang.org/x/net` v0.60.0](https://pkg.go.dev/vuln/GO-2026-6617) provide the
corresponding security fixes. Update module/CI/image pins together, retain the
1.26 language/runtime series, and require a fresh full release certification.
Historical evidence remains bound to its original toolchain and SHA.
