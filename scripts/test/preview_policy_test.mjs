// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-055 TEST-062
import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { branchIdentity, ciMode, changedServices, deploymentAllowed } from "../preview-policy.mjs";
import { loadManifest, selectTasks, runSelection } from "../run-test-tier.mjs";
import { dotenv, routeFor, stateFor, destroyEnvironment, operatorCredentials } from "../preview-environment.mjs";
import { mkdtemp, mkdir, writeFile, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";

test("new previews share only the approved operator login, never service secrets", () => {
  assert.deepEqual(operatorCredentials('HARDEN_LLM_LOCAL_OPERATOR_EMAIL="Operator@Example.test"\nHARDEN_LLM_LOCAL_OPERATOR_PASSWORD=\'fixture$only\'\nPROVIDER_API_KEY=must-not-copy\nHARDEN_LLM_WEB_SECRET_KEY_BASE=must-not-copy'), {
    OPERATOR_EMAIL: "operator@example.test", OPERATOR_PASSWORD: "fixture$only",
  });
  assert.throws(() => operatorCredentials("HARDEN_LLM_LOCAL_OPERATOR_EMAIL=operator@example.test"), /missing/);
  assert.throws(() => operatorCredentials('HARDEN_LLM_LOCAL_OPERATOR_EMAIL=a@b.test\nHARDEN_LLM_LOCAL_OPERATOR_PASSWORD="line\nbreak"'), /invalid/);
});

test("branch identities are stable, bounded, distinct and cannot target production", () => {
  assert.equal(branchIdentity("dev").host, "harden-llm-dev.prls.co");
  const a = branchIdentity("feat/Better_Icons");
  assert.match(a.host, /^harden-llm-feat-better-icons-[a-f0-9]{10}\.prls\.co$/);
  assert.notEqual(a.id, branchIdentity("feat-better-icons").id);
  assert.deepEqual(a, branchIdentity("feat/Better_Icons"));
  assert(branchIdentity("x".repeat(200)).host.split(".")[0].length <= 63);
  for (const branch of ["main", "", "../main", "--help", "a\nb", "a@{1}"]) {
    assert.throws(() => branchIdentity(branch));
  }
});

test("pushes, PRs and schedules never authorize browser tests", () => {
  for (const event of ["push", "pull_request", "schedule"]) {
    for (const suite of ["fast", "browser", "full-with-browser"]) {
      assert.equal(ciMode(event, suite).browser, false);
      assert.equal(ciMode(event, suite).browserCompose, false);
    }
  }
  assert.deepEqual(ciMode("push"), { fast: true, integration: false, release: false, browser: false, browserCompose: false });
  assert.equal(ciMode("workflow_dispatch", "release").browser, false);
  assert.equal(ciMode("workflow_dispatch", "browser").browser, true);
  assert.equal(ciMode("workflow_dispatch", "full-with-browser").browserCompose, true);
  assert.throws(() => ciMode("workflow_dispatch", "typo"));
});

test("image rebuilds follow application inputs, not docs or test-only changes", () => {
  assert.deepEqual(changedServices(["docs/preview-environments.md", "AGENTS.md", "frontend/test/example_test.exs", "internal/gateway/run_test.go"]), []);
  assert.deepEqual(changedServices(["frontend/lib/widget.ex", "frontend/assets/app.css"]), ["web"]);
  assert.deepEqual(changedServices(["internal/gateway/run.go", "go.mod"]), ["gateway"]);
  assert.deepEqual(changedServices(["frontend/mix.lock", "run.go"]), ["gateway", "web"]);
  assert.deepEqual(changedServices(["deploy/preview/compose.yml"]), ["gateway", "web"]);
});

test("deployments require an enabled same-repository branch and a passing current revision", () => {
  const ok = { branch: "dev", sameRepository: true, enabled: true, success: true, sha: "a".repeat(40), tip: "a".repeat(40) };
  assert(deploymentAllowed(ok));
  for (const change of [{ branch: "main" }, { sameRepository: false }, { enabled: false }, { success: false }, { tip: "b".repeat(40) }]) {
    assert.equal(deploymentAllowed({ ...ok, ...change }), false);
  }
});

test("automatic task graphs are browser-free; browser tasks require explicit authorization", async () => {
  const manifest = await loadManifest(new URL("../../test/test-tiers.json", import.meta.url));
  for (const selector of ["fast", "release", "baseline"]) {
    assert(selectTasks(manifest, selector).every(t => !t.requiresBrowser));
    assert(selectTasks(manifest, selector).every(t => !["frontend-browser", "frontend-compose", "frontend-deployed"].includes(t.id)));
  }
  for (const selector of ["browser", "frontend-compose", "deployed"]) {
    assert(selectTasks(manifest, selector).some(t => t.requiresBrowser));
    await assert.rejects(runSelection({ manifest, selector }), /explicit.*browser/i);
  }
});

test("preview workflows use the policy and never check out fork code on the deployment runner", async () => {
  const workflow = await readFile(new URL("../../.github/workflows/preview-environments.yml", import.meta.url), "utf8");
  assert.match(workflow, /workflow_run:/);
  assert.match(workflow, /ref: main/);
  assert.match(workflow, /persist-credentials: false/);
  assert.match(workflow, /head\.repo\.full_name == github\.repository/);
  assert.match(workflow, /cancel-in-progress: false/);
  assert.doesNotMatch(workflow, /run-deployed-browser|make test-browser|Dockerfile\.browser/);
});

test("routes and state enforce exact environment ownership before writes", async () => {
  const state = branchIdentity("feat/test");
  const route = routeFor(state);
  assert.match(route, /@api path \/api\/\* \/readyz/);
  assert(route.includes(`${state.project}-web:4000`));
  assert.match(route, /header_up X-Forwarded-Proto https/);
  assert(route.includes(`${state.project}-garage:3900`));
  assert.doesNotMatch(route, /harden-llm\.prls\.co|otel-collector/);
  assert.throws(() => routeFor({ ...state, host: "harden-llm.prls.co" }));
  assert.throws(() => routeFor({ ...state, project: "harden-llm" }));
  await assert.rejects(destroyEnvironment({}, "dev"), /cannot.*destroyed/);
  await assert.rejects(destroyEnvironment({}, "main"), /non-production/);
  const root = await mkdtemp(path.join(os.tmpdir(), "hllm-preview-policy-"));
  try {
    const c = { root };
    assert.equal(await stateFor(c, state.branch), null);
    const dir = path.join(root, "environments", state.id);
    await mkdir(dir, { recursive: true });
    await writeFile(path.join(dir, "state.json"), JSON.stringify({ ...state, project: "harden-llm" }));
    await assert.rejects(stateFor(c, state.branch), /ownership/);
  } finally { await rm(root, { recursive: true }); }
});

test("preview templates expose no host ports or production telemetry and protect generated dotenv", async () => {
  const compose = await readFile(new URL("../../deploy/preview/compose.yml", import.meta.url), "utf8");
  assert.doesNotMatch(compose, /\bports:|external: true|docker\.sock/);
  assert.match(compose, /OTEL_SDK_DISABLED: 'true'/);
  assert.match(compose, /HARDEN_LLM_OTEL_EXPORTER_OTLP_ENDPOINT: ''/);
  assert.match(compose, /HARDEN_LLM_ENVIRONMENT: development/);
  assert.match(compose, /name: \$\{PREVIEW_PROJECT\}-private/);
  assert.match(compose, /tmpfs: \['\/tmp:size=16m,mode=1777'\]/);
  assert.match(compose, /tmpfs: \['\/tmp:size=32m,mode=1777'\]/);
  assert.equal(dotenv({ KEY: 'a$b"c' }), 'KEY="a$$b\\"c"\n');
  const launcher = await readFile(new URL("../preview-environment.mjs", import.meta.url), "utf8");
  assert.match(launcher, /GATEWAY_IMAGE: components\.gateway\.imageID/);
  assert.match(launcher, /WEB_IMAGE: components\.web\.imageID/);
});
