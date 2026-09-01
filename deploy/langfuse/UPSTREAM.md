# Upstream Langfuse Compose provenance

- Release: `v3.212.0`
- Commit: `3a572984276dd2dc2f8f77f1b2aadb799aa17fdf`
- Source: `https://raw.githubusercontent.com/langfuse/langfuse/3a572984276dd2dc2f8f77f1b2aadb799aa17fdf/docker-compose.yml`
- SHA-256: `f4502f5240857cf9189113fe6c32837ec28f46699415f7efb4b59a6f16423741`
- License: `Apache-2.0`
- Image resolution checkpoint: `2026-07-13`; resolved manifest digests are recorded in `../images.lock.json`.

`docker-compose.upstream.yml` is copied byte-for-byte from the released commit. It owns six services: Langfuse web and worker plus their Postgres, Redis, ClickHouse, and MinIO dependencies. `compose.private.yml` overlays the resolved digests from `../images.lock.json` so an ordinary Compose pull cannot drift from this checkpoint. Update the upstream fragment only by selecting another released commit, replacing the whole file, recalculating the hash, refreshing both the image lock and digest overlay, and rerunning the complete Compose smoke test.
