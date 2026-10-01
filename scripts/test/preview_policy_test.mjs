// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-055 TEST-062
import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { branchIdentity, ciMode, changedServices, deploymentAllowed } from "../preview-policy.mjs";
import { loadManifest, selectTasks, runSelection } from "../run-test-tier.mjs";
import { dotenv, routeFor, stateFor, destroyEnvironment, syncControl, writePrivateIfChanged, reusableImage, initialPreviewCloudflareToken } from "../preview-environment.mjs";
import { mkdtemp, mkdir, writeFile, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";

test("initial preview bootstrap requires its explicit Cloudflare token", () => {
  const message = /HARDEN_LLM_PREVIEW_CLOUDFLARE_API_TOKEN is required for initial preview host setup/;
  assert.throws(() => initialPreviewCloudflareToken({}), message);
  assert.throws(() => initialPreviewCloudflareToken({ CLOUDFLARE_API_TOKEN: "generic-token" }), message);
  assert.throws(() => initialPreviewCloudflareToken({
    HARDEN_LLM_PREVIEW_CLOUDFLARE_API_TOKEN: " \t\n",
    CLOUDFLARE_API_TOKEN: "generic-token",
  }), message);
});

test("initial preview bootstrap trims and selects only its explicit Cloudflare token", () => {
  assert.equal(initialPreviewCloudflareToken({
    HARDEN_LLM_PREVIEW_CLOUDFLARE_API_TOKEN: " \t synthetic-preview-token \n",
    CLOUDFLARE_API_TOKEN: "unrelated-generic-token",
    SHAMAN_PUBLIC_SSH_CLOUDFLARE_TOKEN: "unrelated-ssh-token",
  }), "synthetic-preview-token");
});

test("preview bootstrap is wired to the explicit token helper and has no SSH credential-file dependency", async () => {
  const bootstrap = await readFile(new URL("../setup-preview-host.mjs", import.meta.url), "utf8");
  assert(bootstrap.includes("initialPreviewCloudflareToken(process.env)"));
  assert.doesNotMatch(bootstrap, /shaman-public-ssh/);
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
  assert.deepEqual(ciMode("push"), { fast: true, integration: false, lifecycle: false, capacity: false, release: false, browser: false, browserCompose: false });
  assert.equal(ciMode("workflow_dispatch", "lifecycle").lifecycle, true);
  assert.equal(ciMode("workflow_dispatch", "lifecycle").fast, false);
  assert.equal(ciMode("workflow_dispatch", "lifecycle").release, false);
  assert.equal(ciMode("workflow_dispatch", "lifecycle").browser, false);
  assert.equal(ciMode("workflow_dispatch", "release").browser, false);
  assert.equal(ciMode("workflow_dispatch", "capacity").capacity, true);
  assert.equal(ciMode("workflow_dispatch", "capacity").release, false);
  assert.equal(ciMode("workflow_dispatch", "capacity").browser, false);
  assert.equal(ciMode("workflow_dispatch", "browser").browser, true);
  assert.equal(ciMode("workflow_dispatch", "full-with-browser").browserCompose, true);
  assert.throws(() => ciMode("workflow_dispatch", "typo"));
});

test("image rebuilds follow application inputs, not docs or test-only changes", () => {
  assert.deepEqual(changedServices(["docs/preview-environments.md", "AGENTS.md", "frontend/test/example_test.exs", "internal/gateway/run_test.go"]), []);
  assert.deepEqual(changedServices(["frontend/lib/widget.ex", "frontend/assets/app.css"]), ["web"]);
  assert.deepEqual(changedServices(["internal/gateway/run.go", "go.mod"]), ["gateway"]);
  assert.deepEqual(changedServices(["frontend/mix.lock", "run.go"]), ["gateway", "web"]);
  assert.deepEqual(changedServices(["deploy/preview/compose.yml"]), []);
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

test("preview control files and image reuse are idempotent", async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), "hllm-preview-idempotence-"));
  try {
    const repositoryRoot = path.join(root, "repository");
    await mkdir(path.join(repositoryRoot, "deploy/preview"), { recursive: true });
    await mkdir(path.join(repositoryRoot, "deploy/test"), { recursive: true });
    for (const [relative, contents] of [
      ["deploy/preview/compose.yml", "compose\n"],
      ["deploy/preview/host.compose.yml", "host\n"],
      ["deploy/preview/Caddyfile", "caddy\n"],
      ["deploy/test/garage.toml", "garage\n"],
    ]) await writeFile(path.join(repositoryRoot, relative), contents);
    const c = { root };
    assert.deepEqual(await syncControl(c, repositoryRoot), ["compose.yml", "host.compose.yml", "Caddyfile", "garage.toml"]);
    assert.deepEqual(await syncControl(c, repositoryRoot), []);
    const target = path.join(root, "nested", "value");
    assert.equal(await writePrivateIfChanged(target, "same\n"), true);
    assert.equal(await writePrivateIfChanged(target, "same\n"), false);

    const image = { Id: "sha256:image", Config: { Labels: { "org.opencontainers.image.version": "a".repeat(40), "co.prls.harden.preview-image": "gateway" } } };
    const inspect = () => JSON.stringify([image]);
    assert.deepEqual(reusableImage("preview", "gateway", "a".repeat(40), inspect), image);
    assert.equal(reusableImage("preview", "gateway", "b".repeat(40), inspect), null);
    assert.equal(reusableImage("preview", "web", "a".repeat(40), inspect), null);
    assert.equal(reusableImage("missing", "gateway", "a".repeat(40), () => { throw new Error("not found"); }), null);
  } finally { await rm(root, { recursive: true }); }
});

test("preview gateway bounds Go memory and uses the shared identity contract", async () => {
  const compose = await readFile(new URL("../../deploy/preview/compose.yml", import.meta.url), "utf8");
  const gateway = compose.split("\n  gateway:\n")[1]?.split("\n  web:\n")[0];
  assert.ok(gateway, "gateway service must exist");
  assert.match(gateway, /mem_limit: 256m\n/);
  assert.match(gateway, /GOMEMLIMIT: 192MiB\n/);
  const launcher = await readFile(new URL("../preview-environment.mjs", import.meta.url), "utf8");
  assert.match(launcher, /profileAccountIDs\(sharedValues\)/);
  assert.doesNotMatch(launcher, /bootstrap-user|TEST_PASSWORD|api\/v1\/auth\/login/);
  assert.match(launcher, /delete credentials\.OPERATOR_PASSWORD/); // Remove credentials from preview state written by the retired flow.
});

test("preview templates expose no host ports or production telemetry and protect generated dotenv", async () => {
  const compose = await readFile(new URL("../../deploy/preview/compose.yml", import.meta.url), "utf8");
  assert.doesNotMatch(compose, /\bports:|docker\.sock/);
  const externalNetworks = [...compose.matchAll(/^  ([a-z0-9-]+):\n    external: true$/gm)].map(match => match[1]);
  assert.deepEqual(externalNetworks, ["prls-observability"]);
  assert.match(compose, /OTEL_SDK_DISABLED: 'true'/);
  assert.match(compose, /HARDEN_LLM_OTEL_EXPORTER_OTLP_ENDPOINT: ''/);
  assert.match(compose, /HARDEN_LLM_ENVIRONMENT: development/);
  assert.match(compose, /name: \$\{PREVIEW_PROJECT\}-private/);
  assert.match(compose, /tmpfs: \['\/tmp:size=16m,mode=1777'\]/);
  assert.match(compose, /tmpfs: \['\/tmp:size=32m,mode=1777'\]/);
  assert.match(compose, /mem_limit: 512m/);
  assert.match(compose, /start_interval: 3s/);
  assert.match(compose, /start_interval: 2s/);
  assert.match(compose, /start_period: 10s/);
  assert.match(compose, /interval: 20s/);
  assert.equal(dotenv({ KEY: 'a$b"c' }), 'KEY="a$$b\\"c"\n');
  const launcher = await readFile(new URL("../preview-environment.mjs", import.meta.url), "utf8");
  assert.match(launcher, /GATEWAY_IMAGE: components\.gateway\.imageID/);
  assert.match(launcher, /WEB_IMAGE: components\.web\.imageID/);
  assert.match(launcher, /Reusing preview/);
  assert.match(launcher, /const runtimeServices = \["postgres", "garage", "gateway", "web"\]/);
  assert.match(launcher, /"postgres", "garage", "gateway", "web"/);
  const workflow = await readFile(new URL("../../.github/workflows/test-hierarchy.yml", import.meta.url), "utf8");
  assert.match(workflow, /actions\/cache@v4/);
  const previewWorkflow = await readFile(new URL("../../.github/workflows/preview-environments.yml", import.meta.url), "utf8");
  for (const configuredWorkflow of [workflow, previewWorkflow]) {
    assert.match(configuredWorkflow, /actions\/create-github-app-token@bcd2ba49218906704ab6c1aa796996da409d3eb1/);
    assert.match(configuredWorkflow, /secrets\.CI_APP_PRIVATE_KEY/);
    assert.match(configuredWorkflow, /repositories: prls-control-plane,prls-web[\s\S]*permission-contents: read/);
    assert.match(configuredWorkflow, /PRIVATE_MODULE_TOKEN: \$\{\{ steps\.cp_module\.outputs\.token \}\}/);
    assert.doesNotMatch(configuredWorkflow, /secrets\.PRIVATE_MODULE_TOKEN/);
  }
  const gatewayDockerfile = await readFile(new URL("../../Dockerfile", import.meta.url), "utf8");
  const frontendDockerfile = await readFile(new URL("../../frontend/Dockerfile", import.meta.url), "utf8");
  assert.match(gatewayDockerfile, /RUN --mount=type=cache,target=\/go\/pkg\/mod/);
  assert.match(gatewayDockerfile, /type=secret,id=private_module_token,required=true/);
  assert.match(frontendDockerfile, /type=secret,id=private_module_token,required=true/);
  assert.match(launcher, /--secret=id=private_module_token,env=PRIVATE_MODULE_TOKEN/);
  assert.match(gatewayDockerfile, /target=\/root\/.cache\/go-build/);
});

test("release CI builds the Phoenix production image through its private BuildKit secret", async () => {
  const workflow = await readFile(new URL("../../.github/workflows/test-hierarchy.yml", import.meta.url), "utf8");
  assert.match(workflow, /Build Phoenix production image with the private dependency secret/);
  assert.match(workflow, /docker build[\s\S]*--secret=id=private_module_token,env=PRIVATE_MODULE_TOKEN[\s\S]*frontend\/Dockerfile frontend/);
  assert.match(workflow, /Remove Phoenix validation image[\s\S]*docker image inspect harden-llm-web-buildcheck[\s\S]*docker image rm --force harden-llm-web-buildcheck/);
});
