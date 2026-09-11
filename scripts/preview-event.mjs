// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-062
// Only trusted main executes this file; branch code is a Docker build context.
import { promises as fs } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import { branchIdentity, deploymentAllowed } from "./preview-policy.mjs";
import { deployEnvironment, destroyEnvironment, enableEnvironment, loadConfig, repo, saveState, stateFor, syncControl } from "./preview-environment.mjs";

const api = async (endpoint, method = "GET", body) => {
  const response = await fetch(`https://api.github.com/repos/${repo}/${endpoint}`, {
    method, headers: { Authorization: `Bearer ${process.env.GH_TOKEN}`, Accept: "application/vnd.github+json", "Content-Type": "application/json", "X-GitHub-Api-Version": "2022-11-28" },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }), signal: AbortSignal.timeout(30_000),
  });
  if (!response.ok) throw new Error(`GitHub ${method} ${endpoint.split("?")[0]} failed: HTTP ${response.status}`);
  return response.status === 204 ? null : response.json();
};

async function passedFast(sha) {
  const { workflow_runs: runs } = await api(`actions/workflows/test-hierarchy.yml/runs?head_sha=${sha}&per_page=30`);
  for (const run of runs) {
    if (run.head_sha !== sha || run.head_repository.full_name !== repo || run.conclusion !== "success") continue;
    const { jobs } = await api(`actions/runs/${run.id}/jobs?per_page=100`);
    if (jobs.some(job => ["fast T0-T2", "browser-free release"].includes(job.name) && job.conclusion === "success")) return true;
  }
  return false;
}

async function requestFast(branch, sha) {
  // Old branches may contain automatic browser workflows. Never dispatch those.
  const content = await api(`contents/scripts/preview-policy.mjs?ref=${sha}`);
  const source = Buffer.from(content.content, "base64").toString("utf8");
  const trusted = await fs.readFile(new URL("./preview-policy.mjs", import.meta.url), "utf8");
  if (source !== trusted) throw new Error("Merge current main preview policy into the branch before requesting checks");
  const workflow = await api(`contents/.github/workflows/test-hierarchy.yml?ref=${sha}`);
  if (Buffer.from(workflow.content, "base64").toString("utf8") !== await fs.readFile(new URL("../.github/workflows/test-hierarchy.yml", import.meta.url), "utf8")) {
    throw new Error("Branch CI differs from trusted browser-free workflow; merge current main first");
  }
  const { workflow_runs: runs } = await api(`actions/workflows/test-hierarchy.yml/runs?head_sha=${sha}&per_page=30`);
  if (!runs.some(run => run.head_branch === branch && run.head_sha === sha && ["queued", "in_progress", "waiting", "requested", "pending"].includes(run.status))) {
    await api("actions/workflows/test-hierarchy.yml/dispatches", "POST", { ref: branch, inputs: { suite: "fast" } });
  }
  console.log(`Preview enabled for ${branch}; waiting for browser-free checks at ${sha}`);
}

async function remove(c, branch) {
  if (["main", "dev"].includes(branch)) return;
  const state = await stateFor(c, branch);
  await destroyEnvironment(c, branch);
  if (state?.deploymentID) await api(`deployments/${state.deploymentID}/statuses`, "POST", { state: "inactive", environment: `preview-${state.id}` });
}

async function reconcile(c) {
  if (process.env.GITHUB_REPOSITORY !== repo) throw new Error("Unexpected repository");
  const event = JSON.parse(await fs.readFile(process.env.GITHUB_EVENT_PATH, "utf8"));
  const kind = process.env.GITHUB_EVENT_NAME;
  let branch, expectedSHA, enableSource;
  if (kind === "workflow_dispatch") {
    if (process.env.GITHUB_REF !== "refs/heads/main") throw new Error("Run deployment workflow from main only");
    branch = process.env.PREVIEW_BRANCH;
    branchIdentity(branch);
    if (process.env.PREVIEW_ACTION === "destroy") return remove(c, branch);
    if (process.env.PREVIEW_ACTION !== "deploy") throw new Error("Unknown preview action");
    enableSource = "manual";
  } else if (kind === "delete") {
    if (event.ref_type === "branch") await remove(c, event.ref);
    return;
  } else if (kind === "pull_request_target") {
    if (event.pull_request.head.repo.full_name !== repo) return;
    branch = event.pull_request.head.ref;
    if (["dev", "main"].includes(branch)) return;
    if (event.action === "closed") return remove(c, branch);
    if (event.label?.name !== "deploy:preview") return;
    const current = await api(`pulls/${event.pull_request.number}`);
    if (current.state !== "open" || !current.labels.some(label => label.name === "deploy:preview")) {
      const existing = await stateFor(c, branch);
      if (existing?.enableSource !== "manual") await remove(c, branch);
      return;
    }
    enableSource = "label";
  } else if (kind === "workflow_run") {
    const run = event.workflow_run;
    if (run.conclusion !== "success" || run.head_repository.full_name !== repo) return;
    branch = run.head_branch;
    expectedSHA = run.head_sha;
  } else throw new Error("Unsupported preview event");
  if (!branch || branch === "main") return;
  const identity = branchIdentity(branch);
  let state = await stateFor(c, branch);
  if (branch === "dev" || enableSource) {
    state = await enableEnvironment(c, branch);
    state.enableSource = branch === "dev" ? "dev" : (state.enableSource === "manual" ? "manual" : enableSource);
    await saveState(c, state);
  }
  if (!state?.enabled) return;
  if (state.enableSource === "label") {
    const prs = await api(`pulls?state=open&head=prls-co:${encodeURIComponent(branch)}`);
    if (!prs.some(pr => pr.labels.some(label => label.name === "deploy:preview"))) return remove(c, branch);
  }
  const current = await api(`branches/${encodeURIComponent(branch)}`);
  const sha = current.commit.sha;
  if (expectedSHA && expectedSHA !== sha) { console.log("Ignoring stale CI revision"); return; }
  const success = await passedFast(sha);
  if (!success) {
    if (enableSource) await requestFast(branch, sha);
    return;
  }
  if (!deploymentAllowed({ branch, sameRepository: true, enabled: state.enabled, success, sha, tip: current.commit.sha })) throw new Error("Preview deployment policy rejected revision");
  await syncControl(c, path.resolve(path.dirname(fileURLToPath(import.meta.url)), ".."));
  const deployment = await api("deployments", "POST", { ref: sha, environment: `preview-${identity.id}`, transient_environment: branch !== "dev", production_environment: false, auto_merge: false, required_contexts: [] });
  const status = value => api(`deployments/${deployment.id}/statuses`, "POST", {
    state: value, environment: `preview-${identity.id}`, environment_url: identity.url,
    log_url: `https://github.com/${repo}/actions/runs/${process.env.GITHUB_RUN_ID}`, auto_inactive: true,
  });
  await status("in_progress");
  try {
    const result = await deployEnvironment(c, branch, sha);
    await saveState(c, { ...result, deploymentID: deployment.id });
    await status("success");
    if (process.env.GITHUB_STEP_SUMMARY) await fs.appendFile(process.env.GITHUB_STEP_SUMMARY,
      `Preview: ${identity.url}\n\nSource: \`${sha}\`\n\nRebuilt: ${result.rebuiltServices.join(", ") || "none"}\n\nHTTP health and container image identity verified. No browser or provider call.\n`);
  } catch (error) { await status("failure"); throw error; }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  try {
    const c = await loadConfig();
    if (!process.env.PREVIEW_LOCKED) {
      const child = spawnSync("flock", ["--wait", "1800", path.join(c.root, "deploy.lock"), process.execPath, fileURLToPath(import.meta.url)], {
        stdio: "inherit", env: { ...process.env, PREVIEW_LOCKED: "1" },
      });
      process.exitCode = child.status ?? 1;
    } else await reconcile(c);
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
