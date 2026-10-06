// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-408
import test from "node:test";
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const read = (relative) => readFileSync(path.join(root, relative), "utf8");

test("one connection-only CPA configuration is selected without embedded credentials", () => {
  const document = JSON.parse(read("config/upstreams.example.json"));
  assert.deepEqual(Object.keys(document).sort(), ["default_upstream", "upstreams"]);
  assert.equal(document.default_upstream, "cpa");
  assert.deepEqual(document.upstreams, [
    {
      id: "cpa",
      provider: "cpa",
      protocol: "responses",
      base_url: "https://cpa.prls.co/v1",
      api_key_env: "CPA_API_KEY",
      cache_domain: "cpa-production",
      supports_web_search: true,
    },
  ]);
  assert.doesNotMatch(JSON.stringify(document), /api[_-]?key\s*[:=]\s*["'][^"']+/i);

  const example = read(".env.example");
  assert.match(example, /^HARDEN_LLM_CONFIG_FILE=\/absolute\/path\/to\/config\/upstreams\.local\.json$/m);
  assert.match(example, /^HARDEN_LLM_TOKEN=replace-with-at-least-32-random-printable-bytes$/m);
  assert.match(example, /^CPA_API_KEY=replace-with-cpa-api-key$/m);
});

test("Compose gives the same bearer to the public gateway and reference client", () => {
  const gateway = read("docker-compose.yml");
  const frontend = read("deploy/frontend/compose.frontend.yml");
  assert.match(gateway, /^      HARDEN_LLM_TOKEN: \$\{HARDEN_LLM_TOKEN:\?[^\n]+\}$/m);
  assert.match(frontend, /^      HARDEN_LLM_TOKEN: \$\{HARDEN_LLM_TOKEN:\?[^\n]+\}$/m);
  assert.match(gateway, /^      HARDEN_LLM_CONFIG_FILE: \/etc\/harden-llm\/upstreams\.json$/m);
  assert.match(gateway, /^      - \$\{HARDEN_LLM_CONFIG_FILE:\?[^\n]+\}:\/etc\/harden-llm\/upstreams\.json:ro$/m);
  assert.match(gateway, /^      CPA_API_KEY: \$\{CPA_API_KEY:\?[^\n]+\}$/m);
  assert.doesNotMatch(frontend, /CPA_API_KEY|HARDEN_LLM_STATIC_TOKEN|HARDEN_LLM_PROFILE_USER_IDS/);
  assert.doesNotMatch(gateway, /HARDEN_LLM_STATIC_TOKEN|HARDEN_LLM_STATIC_TOKEN_USER_ID|HARDEN_LLM_CONTROL_PLANE_URL/);
  assert.match(frontend, /HARDEN_LLM_DATABASE_URL:/);
  assert.doesNotMatch(gateway, /^  harden-postgres:/m);
  assert.match(frontend, /^  harden-postgres:/m);
  assert.doesNotMatch(frontend, /harden-postgres:\n\s+condition: service_healthy/);
});

test("active setup and client documentation have no profile API or synchronization path", () => {
  const activeFiles = [
    ".env.example",
    "README.md",
    "frontend/README.md",
    "AGENTS.md",
    "docs/environment.md",
    "docs/shared-llm-configuration.md",
    "docs/self-hosting.md",
    "docs/preview-environments.md",
    "docs/api-and-library.md",
    "docker-compose.yml",
    "deploy/frontend/compose.frontend.yml",
    "deploy/preview/compose.yml",
    "scripts/preview-environment.mjs",
    "scripts/harden-structured-call.sh",
  ];

  for (const file of activeFiles) {
    const source = read(file);
    assert.doesNotMatch(source, /HARDEN_LLM_STATIC_TOKEN|HARDEN_LLM_STATIC_TOKEN_USER_ID/);
    assert.doesNotMatch(source, /sync-profiles|HARDEN_LLM_PROFILE_USER_IDS/);
    assert.doesNotMatch(source, /\/api\/v1\/(?:run|profiles|history|traces|artifacts)/);
  }

  for (const retiredPath of [
    "scripts/shared-profiles.mjs",
    "scripts/test/shared_profiles_test.mjs",
    "config/llm-profiles.example.json",
    "cmd/harden-llm-gateway/shared_profiles.go",
  ]) {
    assert.equal(existsSync(path.join(root, retiredPath)), false, `${retiredPath} remains active`);
  }
});
