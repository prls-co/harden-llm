# Issue #139: RCA and solution options

## 1. Scope and evidence

Analysis on 2026-11-07 against Harden `3f8a5173086ea245841551fe988f6a3b03cff7f8`. This is analysis only. No application fix, main-branch change, deployment, browser test or paid inference is claimed. Retained incident evidence and fresh observations are identified separately.

## 2. Causal chain and evidence limits

The retained consumer measurement is unusually discriminating: a 597-byte stored HTML artifact downloaded as 964 bytes; removing only the added Cloudflare Insights script and newline recovered the exact expected SHA-256 `8aed43f1bcf08a940911616ab9f4815ec08dff6e4f6812e9d4e749023af59a28`. JSON/CSV checks passed. [Committed consumer evidence, lines 25–33](https://github.com/prls-co/query-analytics/blob/1eba67fb39c1e5c3fe44d7bf8ecf2e23519a359a/docs/QUERY-ANALYTICS-LOCAL-GATES.md#L25-L33) records the public-path transformation and separately qualified private route.

The source path is:

1. [cloudflared ingress lines 11–16](https://github.com/prls-co/caddy-shared/blob/16568645215339c81000f4765bef30ea1e4dc3a6/cloudflared/config.yml#L11-L16) routes the artifact hostname to shared Caddy with that host preserved.
2. [Caddy artifact route lines 36–44](https://github.com/prls-co/caddy-shared/blob/16568645215339c81000f4765bef30ea1e4dc3a6/routes/harden-llm.caddy#L36-L44) applies security headers and proxies Garage. It contains no HTML script injection and no explicit no-transform policy. The CSP/sandbox controls script execution; they do not prevent an edge service from changing response bytes.
3. The expected-versus-served byte difference identifies an HTML response transformation after storage. A signed URL authorizes access but does not make edge transformations byte-preserving. Neither a new PUT nor checksum normalization addresses this path.

[Cloudflare's official setup documentation](https://developers.cloudflare.com/web-analytics/get-started/) describes automatic beacon injection and disabling it. Its [Rules documentation](https://developers.cloudflare.com/web-analytics/configuration-options/rules/) describes hostname/path-scoped control and configuration-rule precedence.

**Attribution:** Cloudflare Insights injection is demonstrated by retained bytes, consistent with the documented mechanism. The exact account/site/rule ID and effective live setting are **not yet retrieved**. No live signed artifact was fetched or new object uploaded in this analysis. There is no honest Harden Go “faulty line” to name for an external edge policy; the code/config links identify the route and the missing byte-preservation boundary, not Cloudflare's private implementation.

The HLLM-owned [stateful/artifact.go:111–117](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/stateful/artifact.go#L111-L117) stores JSON diagnostic objects, not arbitrary consumer HTML. Altering that function's hash behavior would target the wrong owner. The caddy-shared checkout contains unrelated local edits; immutable committed route lines above were checked separately and no edits were made there.

## 3. Options and recommendation

1. **Recommended:** the existing ingress/Cloudflare owner exports the effective Web Analytics and overriding configuration rules for this exact hostname, disables automatic injection/HTML transformations only on the artifact origin, and records the setting/rule IDs plus before/after signed-byte verification. Keep bucket, credentials, paths and hostname.
2. Add a scoped `Cache-Control: no-transform` directive at the origin/ingress as additional defense, after checking existing signed/private cache directives. Cloudflare documents its no-transform behavior, but live verification is still required. Do not copy its example's `public` directive onto private/signed artifacts or overwrite existing cache policy carelessly.
3. Private Garage access is a valid server-side workaround already recorded by the consumer. It does not repair public signed downloads or satisfy this issue. New storage/proxy services or stripping the script before hashing are not recommended.

## 4. Verification and blockers

Use isolated synthetic HTML, JSON and CSV, compare exact stored/read/signed-download bytes and SHA-256, content type and existing auth/expiry behavior across private origin and public edge; check the injected module is absent. Verify unsigned denial and expired/tampered URL denial. Signed URLs and credentials must stay out of public comments. No browser or provider is required.

**Blockers:** effective Cloudflare setting/rule readback, access through the ingress owner to change it when separately authorized, and an owned signed-fixture round trip. The connected tools available in this session do not include a Cloudflare settings API. No user action is required to complete this analysis; implementation may need the account/zone owner or scoped existing access. Highest-priority bounded integrity fix, but “one setting” effort remains conditional on that inspection.
