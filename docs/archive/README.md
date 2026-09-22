# Archived Gateway Image Publisher Reference

The active project now builds and deploys the gateway image on its existing
Docker host. This archive keeps a compact, non-executable reference to the one
historical GHCR publisher implementation for possible future reconsideration.
It is not extracted or executed by CI, Make, Compose, production-config, or a
deployment hook.

## Bundle

- File: `harden-llm-ghcr-publisher-reference-6887fcd.tar.gz`.
- Source commit: `6887fcd8146961dc64598dd7a236e7a9fc522c9c`.
- Contents:
  - `.github/workflows/publish-gateway-image.yml`
  - `scripts/test/gateway_image_publication_test.mjs`
  - `Dockerfile`
- SHA-256: `60b771ebdb807f306557f383208d6c2c30e6b6cb87ebfc0337f7f8001bb6c785`.
- Purpose: source reference only. The workflow requires a deliberate current
  security/release review before reuse; it must not be enabled directly from
  this archive.

The Dockerfile remains active in the root of the repository. Its copy in the
archive preserves the exact build metadata and OCI-label context used by the
historical publisher. Retaining the source/revision/version OCI labels in the
active Dockerfile is intentional because local deployment checks use the
version identity; these labels do not publish an image.

The lifecycle specification and decision are
[`gateway-image-build-deployment-spec.md`](../../plans/gateway-image-build-deployment-spec.md),
[`gateway-image-build-deployment-kers.md`](../../plans/gateway-image-build-deployment-kers.md),
and [ADR-HLLM-028](../adr/ADR-HLLM-028-local-image-build-deployment.md).
