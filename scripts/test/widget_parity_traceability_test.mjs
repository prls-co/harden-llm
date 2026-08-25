import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import {fileURLToPath} from "node:url";

// PLAN-HLLM-WIDGET-PARITY-001 TEST-116

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const planPath = path.join(repositoryRoot, "plans/llm-widget-utility-parity-implementation-plan.md");

const traceCases = [
  ["TEST-101", ["frontend/test/harden_llm_web/live/profile_widget_component_test.exs", "frontend/test/harden_llm_web/live/workspace_live_test.exs"]],
  ["TEST-102", ["frontend/test/harden_llm_web/live/profile_widget_component_test.exs", "frontend/test/harden_llm_web/live/workspace_live_test.exs"]],
  ["TEST-103", ["frontend/test/harden_llm_web/live/profile_widget_component_test.exs", "frontend/test/harden_llm_web/live/workspace_live_test.exs"]],
  ["TEST-104", ["frontend/test/harden_llm_web/live/profile_widget_component_test.exs", "frontend/test/harden_llm_web/live/workspace_live_test.exs"]],
  ["TEST-105", ["frontend/test/harden_llm_web/live/profile_widget_state_test.exs"]],
  ["TEST-106", ["frontend/test/harden_llm_web/live/workspace_live_test.exs"]],
  ["TEST-107", ["frontend/test/harden_llm_web/live/workspace_live_test.exs"]],
  ["TEST-108", ["internal/gateway/resource_routes_test.go"]],
  ["TEST-109", ["frontend/test/harden_llm_web/harden_api_test.exs", "frontend/test/harden_llm_web/live/workspace_live_test.exs"]],
  ["TEST-110", ["frontend/test/harden_llm_web/live/profile_widget_state_test.exs", "frontend/test/harden_llm_web/live/embedding_live_test.exs"]],
  ["TEST-111", ["frontend/assets/test/client_core.test.mjs"]],
  ["TEST-112", ["frontend/test/harden_llm_web/live/profile_widget_component_test.exs"]],
  ["TEST-113", ["frontend/test/harden_llm_web/live/embedding_live_test.exs", "frontend/test/harden_llm_web/live/workspace_live_test.exs"]],
  ["TEST-114", ["frontend/test/browser/widget_canary_test.exs"]],
  ["TEST-115", ["frontend/test/harden_llm_web/boundary_test.exs"]],
  ["TEST-116", ["scripts/test/widget_parity_traceability_test.mjs"]],
  ["TEST-117", ["scripts/verify-test-tiers.mjs"]],
  ["TEST-118", ["frontend/test/browser/deployed_canary_test.exs", "scripts/run-deployed-browser-test.mjs"]],
];

function read(relativePath) {
  const absolutePath = path.join(repositoryRoot, relativePath);
  assert.ok(fs.existsSync(absolutePath), `traceability path is missing: ${relativePath}`);
  return fs.readFileSync(absolutePath, "utf8");
}

test("widget plan requirements, tests, paths, and trace tags agree", () => {
  const plan = read("plans/llm-widget-utility-parity-implementation-plan.md");

  for (let number = 1; number <= 18; number += 1) {
    const requirement = `REQ-${String(number).padStart(3, "0")}`;
    assert.match(plan, new RegExp(`\\b${requirement}\\b`), `missing ${requirement} in plan`);
  }

  for (const [testId, paths] of traceCases) {
    assert.match(plan, new RegExp(`\\b${testId}\\b`), `missing ${testId} in plan`);
    for (const relativePath of paths) {
      const source = read(relativePath);
      assert.match(source, new RegExp(`\\b${testId}\\b`), `${relativePath} is not tagged ${testId}`);
    }
  }

  for (const evaluation of ["EVAL-101", "EVAL-102", "EVAL-103", "EVAL-104"]) {
    assert.match(plan, new RegExp(`\\b${evaluation}\\b`), `missing ${evaluation} in plan`);
  }

  for (const documentation of [
    "docs/utility-llm-frontend-parity-inventory.md",
    "docs/adr/ADR-HLLM-014-embedded-widget-runtime-parity.md",
    "docs/adr/ADR-HLLM-016-widget-draft-and-data-contract.md",
    "ker/widget-parity/README.md",
    "ker/widget-parity/baseline.json",
    "plans/evidence/harden-llm/widget-parity-eval.json",
  ]) {
    read(documentation);
  }

  console.log(JSON.stringify({accepted: true, testCases: traceCases.length, requirements: 18}));
});
