# P06 release qualification and deployment log

## 1. Candidate review

P00–P05 are pushed through `b4d270d11b64056074788b92eb8ec68807578164`.
`ab8f8963c972ae2ce709011196b93f7189d32bf9` adds TEST-435, the explicit
browser-free deployed acceptance command. It was compiled, not run against
production before deployment. PR #101 targets main; production is unchanged.

PR CodeQL review found two capacity-arithmetic allocation alerts (6 and 7)
in provider payload and response chunk construction. Removed the optional
preallocation arithmetic, preserving the existing append loops and wire output.
Focused provider/HTTP tests and the ten-task fast gate pass; see
`p06-allocation-tests.log` and `p06-allocation-fast.json` (accepted, zero cleanup
errors or warnings). Hosted checks will rerun on the resulting commit.

Alert 8 (`go/weak-sensitive-data-hashing`) was reviewed and dismissed as a
false positive on the specific alert, without suppressing the query. SHA-256
only derives a storage namespace after `auth.MatchBearer` validates the raw
bearer; the digest is not an authentication verifier or password hash. TEST-425
proves a digest cannot authenticate, consistent with ADR-HLLM-033. The review
comment is recorded on GitHub alert 8.

The release gate remains an explicit successful main-branch `suite=release`
workflow attempt at the exact accepted SHA. No browser or paid inference check
is authorized. Shared service provisioning and production apply remain pending.
