// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-062
import { createHash } from "node:crypto";
import { appendFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";

export function branchIdentity(branch) {
  if (typeof branch !== "string" || !branch || branch === "main" || branch.startsWith("-") ||
      /[\s\x00-\x1f~^:?*\[\\]/.test(branch) || branch.includes("..") || branch.includes("@{") ||
      branch.startsWith("/") || branch.endsWith("/") || branch.includes("//") ||
      branch.split("/").some(s => s.startsWith(".") || s.endsWith(".") || s.endsWith(".lock"))) {
    throw new Error("Expected a non-production Git branch name");
  }
  const hash = createHash("sha256").update(branch).digest("hex").slice(0, 10);
  const slug = branch.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "").slice(0, 24).replace(/-$/, "") || "branch";
  const id = branch === "dev" ? "dev" : `${slug}-${hash}`;
  return { branch, id, project: `hllm-preview-${id}`, host: `harden-llm-${id}.prls.co`, url: `https://harden-llm-${id}.prls.co` };
}

export function ciMode(event, suite = "fast") {
  const manual = event === "workflow_dispatch";
  if (manual && !["fast", "integration", "release", "browser", "full-with-browser"].includes(suite)) throw new Error("Unknown CI suite");
  return {
    fast: ["push", "pull_request"].includes(event) || (manual && suite === "fast"),
    integration: manual && suite === "integration",
    release: event === "schedule" || (manual && ["release", "full-with-browser"].includes(suite)),
    browser: manual && ["browser", "full-with-browser"].includes(suite),
    browserCompose: manual && suite === "full-with-browser",
  };
}

export function changedServices(paths) {
  const selected = new Set();
  for (const p of paths) {
    if (/^deploy\/preview\//.test(p)) { selected.add("gateway"); selected.add("web"); }
    if (/^frontend\/(lib\/|assets\/|config\/|priv\/|mix\.(exs|lock)$|Dockerfile$|\.dockerignore$)/.test(p)) selected.add("web");
    if ((/^(Dockerfile|\.dockerignore|go\.mod|go\.sum)$/.test(p) || /^(internal\/|cmd\/|[^/]+\.go$)/.test(p)) &&
        !p.endsWith("_test.go") && !/^internal\/(testkit|integrationtest|smoke|deploytest)\//.test(p)) selected.add("gateway");
  }
  return ["gateway", "web"].filter(s => selected.has(s));
}

export function deploymentAllowed({ branch, sameRepository, enabled, success, sha, tip }) {
  try { branchIdentity(branch); } catch { return false; }
  return Boolean(sameRepository && enabled && success && /^[a-f0-9]{40}$/.test(sha) && sha === tip);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  if (process.argv[2] !== "ci") throw new Error("Usage: node scripts/preview-policy.mjs ci");
  const mode = ciMode(process.env.GITHUB_EVENT_NAME, process.env.SUITE || "fast");
  if (process.env.GITHUB_OUTPUT) await appendFile(process.env.GITHUB_OUTPUT, Object.entries(mode).map(([k, v]) => `${k}=${v}\n`).join(""));
  console.log(JSON.stringify(mode));
}
