# Upstream Langfuse Compose provenance

- Release: `v3.225.5`
- Commit: `a914a47f357f5d1cf5611e1387ea68678410c671`
- Source: `https://raw.githubusercontent.com/langfuse/langfuse/a914a47f357f5d1cf5611e1387ea68678410c671/docker-compose.yml`
- SHA-256: `26510ab5cc9163bf2212d5dfb991b3a71e1ce5cf7d032b595e7eee122bec1687`
- License: `Apache-2.0`
- Image resolution checkpoint: `2026-09-01`; resolved manifest digests are recorded in `../images.lock.json`.

`docker-compose.upstream.yml` is copied byte-for-byte from the released commit. It owns six services: Langfuse web and worker plus their Postgres, Redis, ClickHouse, and MinIO dependencies. `compose.private.yml` overlays the resolved digests from `../images.lock.json` so an ordinary Compose pull cannot drift from this checkpoint. Update the upstream fragment only by selecting another released commit, replacing the whole file, recalculating the hash, refreshing both the image lock and digest overlay, and rerunning the complete Compose smoke test.
