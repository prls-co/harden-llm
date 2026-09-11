// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-062
// Host-side deployment primitives. Invoked only by trusted main-branch orchestration.
import { spawnSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import { promises as fs } from "node:fs";
import path from "node:path";
import os from "node:os";
import { parseEnv } from "node:util";
import { branchIdentity, changedServices } from "./preview-policy.mjs";

export const configPath = path.join(os.homedir(), ".config/harden-llm-preview/host.json");
export const repo = "prls-co/harden-llm";
export const delay = ms => new Promise(resolve => setTimeout(resolve, ms));

export async function writePrivate(file, value) {
  await fs.mkdir(path.dirname(file), { recursive: true, mode: 0o700 });
  await fs.writeFile(`${file}.writing`, value, { mode: 0o600 });
  await fs.rename(`${file}.writing`, file);
}

export async function readJSON(file, fallback) {
  try { return JSON.parse(await fs.readFile(file, "utf8")); }
  catch (error) { if (error.code === "ENOENT" && fallback !== undefined) return fallback; throw error; }
}

export function command(bin, args, options = {}) {
  const result = spawnSync(bin, args, { encoding: "utf8", timeout: 900_000, maxBuffer: 8 * 1024 * 1024, ...options });
  if (result.status !== 0 || result.error) {
    // Do not include subprocess output: connection errors may include credentials.
    throw new Error(`${bin} ${args[0] ?? ""} failed (exit ${result.status ?? result.error?.code}); inspect the scoped service locally`);
  }
  return (result.stdout ?? "").trim();
}

export async function loadConfig() {
  const c = await readJSON(configPath);
  if (c.repository !== repo || path.basename(c.root) !== "harden-llm-previews" || !path.isAbsolute(c.root)) throw new Error("Invalid preview host configuration");
  return c;
}

export async function cf(c, endpoint, method = "GET", body) {
  const response = await fetch(`https://api.cloudflare.com/client/v4/${endpoint}`, {
    method, headers: { Authorization: `Bearer ${c.cloudflareToken}`, "Content-Type": "application/json" },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }), signal: AbortSignal.timeout(30_000),
  });
  const data = await response.json();
  if (!response.ok || !data.success) throw new Error(`Cloudflare ${method} failed: HTTP ${response.status}, codes ${(data.errors ?? []).map(e => e.code).join(",")}`);
  return data.result;
}

export function environmentDirectory(c, branch) {
  return path.join(c.root, "environments", branchIdentity(branch).id);
}

export async function stateFor(c, branch) {
  const identity = branchIdentity(branch);
  const state = await readJSON(path.join(environmentDirectory(c, branch), "state.json"), null);
  if (state && Object.entries(identity).some(([key, value]) => state[key] !== value)) throw new Error("Preview ownership mismatch");
  return state;
}

export async function saveState(c, state) {
  await writePrivate(path.join(environmentDirectory(c, state.branch), "state.json"), JSON.stringify(state, null, 2) + "\n");
}

export async function enableEnvironment(c, branch) {
  const state = await stateFor(c, branch) ?? { ...branchIdentity(branch), components: {} };
  state.enabled = true;
  await saveState(c, state);
  return state;
}

export function hostCompose(c, args) {
  return command("docker", ["compose", "--env-file", path.join(c.root, "host.env"), "-p", "hllm-preview-host", "-f", path.join(c.root, "control/host.compose.yml"), ...args]);
}

function compose(c, state, args, extraEnv = {}) {
  return command("docker", ["compose", "--env-file", path.join(environmentDirectory(c, state.branch), ".env"), "-p", state.project, "-f", path.join(c.root, "control/compose.yml"), ...args], { env: { ...process.env, ...extraEnv } });
}

export function dotenv(values) {
  return Object.entries(values).map(([k, v]) => `${k}=${JSON.stringify(String(v)).replaceAll("$", () => "$$")}\n`).join("");
}

export function operatorCredentials(contents) {
  const values = parseEnv(contents);
  const email = values.HARDEN_LLM_LOCAL_OPERATOR_EMAIL?.trim().toLowerCase();
  const password = values.HARDEN_LLM_LOCAL_OPERATOR_PASSWORD;
  if (!email || !password || /[\r\n]/.test(password)) throw new Error("Shared operator email/password are missing or invalid");
  return { OPERATOR_EMAIL: email, OPERATOR_PASSWORD: password };
}

export function testCredentials(contents) {
  const values = parseEnv(contents);
  const email = values.TEST_LOGIN?.trim().toLowerCase();
  const password = values.TEST_PASSWORD;
  if (!email || !password || /[\r\n]/.test(password)) throw new Error("TEST_LOGIN/TEST_PASSWORD are missing or invalid");
  return { email, password };
}

export function ensureTestLogin(c, state, guest, run = command) {
  if (state.project !== branchIdentity(state.branch).project) throw new Error("Preview ownership mismatch");
  const base = ["compose", "--env-file", path.join(environmentDirectory(c, state.branch), ".env"), "-p", state.project, "-f", path.join(c.root, "control/compose.yml"), "exec", "-T"];
  const email = run("docker", [...base, "postgres", "sh", "-c", 'PGPASSWORD="$POSTGRES_PASSWORD" psql -X -qAt -v ON_ERROR_STOP=1 -U harden_llm -d harden_llm -c "SELECT email FROM users WHERE id=\'guest\'"']);
  if (email === guest.email) return false;
  if (email) throw new Error("Guest owner ID belongs to a different account; refusing overwrite");
  run("docker", [...base, "gateway", "/harden-llm-gateway", "bootstrap-user", "--owner-id", "guest", "--email", guest.email, "--password-file", "-"], { input: guest.password + "\n" });
  return true;
}

function secrets() {
  const secret = n => randomBytes(n).toString("base64url");
  return {
    POSTGRES_PASSWORD: secret(32), GARAGE_RPC_SECRET: randomBytes(32).toString("hex"),
    ARTIFACT_ACCESS_KEY: `GK${randomBytes(16).toString("hex")}`, ARTIFACT_SECRET_KEY: randomBytes(32).toString("hex"),
    ENCRYPTION_KEYS: JSON.stringify({ preview: secret(32) }), WEB_SECRET: secret(64),
    WEB_SIGNING_SALT: secret(16), WEB_ENCRYPTION_SALT: secret(16),
  };
}

export function routeFor(state) {
  const expected = branchIdentity(state.branch);
  if (state.host !== expected.host || state.project !== expected.project) throw new Error("Invalid preview route ownership");
  return `http://${state.host}:8080 {\n` +
    `\theader X-Robots-Tag "noindex, nofollow"\n` +
    `\t@private path /metrics\n\thandle @private {\n\t\trespond 404\n\t}\n` +
    `\t@api path /api/* /readyz\n\thandle @api {\n\t\treverse_proxy ${state.project}-gateway:8080\n\t}\n` +
    `\thandle /harden-llm-artifacts/* {\n\t\treverse_proxy ${state.project}-garage:3900\n\t}\n` +
    `\thandle {\n\t\treverse_proxy ${state.project}-web:4000 {\n\t\t\theader_up X-Forwarded-Proto https\n\t\t}\n\t}\n}\n`;
}

async function ensureDNS(c, state) {
  const records = await cf(c, `zones/${c.zoneID}/dns_records?name=${encodeURIComponent(state.host)}`);
  const desired = { type: "CNAME", name: state.host, content: `${c.tunnelID}.cfargotunnel.com`, proxied: true, ttl: 1, comment: `harden-llm-preview:${state.id}` };
  if (records.length) {
    if (records.length !== 1 || records[0].type !== desired.type || records[0].content !== desired.content || records[0].comment !== desired.comment || !records[0].proxied) throw new Error("Preview DNS name belongs to another configuration; refusing overwrite");
    return records[0].id;
  }
  return (await cf(c, `zones/${c.zoneID}/dns_records`, "POST", desired)).id;
}

export async function healthCheck(url, timeoutMs = 90_000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const results = await Promise.all(["/healthz", "/readyz", "/login"].map(async route => {
        const r = await fetch(url + route, { redirect: "manual", signal: AbortSignal.timeout(5_000) });
        await r.body?.cancel(); return r.status;
      }));
      if (results.every(s => s === 200)) return results;
    } catch { /* DNS and container readiness are asynchronous; bounded wait only. */ }
    await delay(1000);
  }
  throw new Error("Preview HTTP health/login/readiness did not become ready within the deployment budget");
}

export async function authCheck(url, credentials) {
  const login = await fetch(`${url}/api/v1/auth/login`, {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email: credentials.OPERATOR_EMAIL, password: credentials.OPERATOR_PASSWORD }), signal: AbortSignal.timeout(15_000),
  });
  const body = await login.json();
  const token = body.result?.accessToken;
  if (login.status !== 200 || !token) throw new Error("Preview API login failed");
  const headers = { Authorization: `Bearer ${token}` };
  try {
    for (const endpoint of ["/api/v1/auth/session", "/api/v1/profiles", "/api/v1/history"]) {
      const response = await fetch(url + endpoint, { headers, signal: AbortSignal.timeout(15_000) });
      await response.body?.cancel();
      if (response.status !== 200) throw new Error(`Preview authenticated ${endpoint} failed`);
    }
  } finally {
    const response = await fetch(`${url}/api/v1/auth/logout`, { method: "POST", headers, signal: AbortSignal.timeout(15_000) });
    await response.body?.cancel();
    if (response.status !== 200) throw new Error("Preview smoke session logout failed");
  }
}

export async function syncControl(c, repositoryRoot) {
  for (const name of ["compose.yml", "host.compose.yml", "Caddyfile"]) {
    await writePrivate(path.join(c.root, "control", name), await fs.readFile(path.join(repositoryRoot, "deploy/preview", name)));
  }
  await writePrivate(path.join(c.root, "control/garage.toml"), await fs.readFile(path.join(repositoryRoot, "deploy/garage/garage.toml")));
}

function fetchBranch(c, branch) {
  const env = { ...process.env };
  let remote = "origin";
  if (env.GH_TOKEN) {
    remote = `https://github.com/${repo}.git`;
    env.GIT_CONFIG_COUNT = "1";
    env.GIT_CONFIG_KEY_0 = "http.https://github.com/.extraheader";
    env.GIT_CONFIG_VALUE_0 = `AUTHORIZATION: basic ${Buffer.from(`x-access-token:${env.GH_TOKEN}`).toString("base64")}`;
  }
  command("git", ["-C", c.sourceRepository, "fetch", "--no-tags", remote, `+refs/heads/${branch}:refs/remotes/origin/${branch}`], { env });
}

export async function deployEnvironment(c, branch, sha) {
  if (!/^[a-f0-9]{40}$/.test(sha)) throw new Error("Expected an exact commit SHA");
  const state = await stateFor(c, branch);
  if (!state?.enabled) throw new Error("Preview is not enabled");
  fetchBranch(c, branch);
  const tip = command("git", ["-C", c.sourceRepository, "rev-parse", `refs/remotes/origin/${branch}`]);
  if (tip !== sha) throw new Error("Branch advanced before deployment; wait for its latest passing CI");
  const directory = environmentDirectory(c, branch);
  const source = path.join(directory, "source");
  const changes = state.sha ? command("git", ["-C", c.sourceRepository, "diff", "--name-only", state.sha, sha]).split("\n") : [];
  const services = state.sha ? changedServices(changes) : ["gateway", "web"];
  const sourceExists = await fs.access(path.join(source, ".git")).then(() => true, e => { if (e.code === "ENOENT") return false; throw e; });
  if (!sourceExists) command("git", ["-C", c.sourceRepository, "worktree", "add", "--detach", source, sha]);
  else {
    if (command("git", ["-C", source, "status", "--porcelain"])) throw new Error("Preview worktree has local changes; refusing overwrite");
    command("git", ["-C", source, "checkout", "--detach", sha]);
  }
  const sharedEnv = await fs.readFile(c.operatorEnvFile, "utf8");
  const guest = testCredentials(sharedEnv);
  const credentials = await readJSON(path.join(directory, "secrets.json"), null) ?? {
    ...secrets(), ...operatorCredentials(sharedEnv),
  };
  await writePrivate(path.join(directory, "secrets.json"), JSON.stringify(credentials, null, 2) + "\n");
  await writePrivate(path.join(directory, "login.txt"), `URL: ${state.url}\nGuest email: ${guest.email}\nGuest password: ${guest.password}\nOperator email: ${credentials.OPERATOR_EMAIL}\nOperator password: ${credentials.OPERATOR_PASSWORD}\n`);
  const components = structuredClone(state.components);
  for (const service of services) {
    const image = `harden-llm-preview-${service}:${sha}`;
    console.log(`Building preview ${service} at ${sha.slice(0, 12)}`);
    command("docker", ["build", "--label", `co.prls.harden.preview-image=${service}`, "--build-arg", `VERSION=${sha}`, "--tag", image, "--file", path.join(source, service === "web" ? "frontend/Dockerfile" : "Dockerfile"), service === "web" ? path.join(source, "frontend") : source]);
    const [info] = JSON.parse(command("docker", ["image", "inspect", image]));
    if (info.Config.Labels["org.opencontainers.image.version"] !== sha) throw new Error("Preview image release label mismatch");
    components[service] = { image, imageID: info.Id, release: sha };
  }
  const envPath = path.join(directory, ".env");
  fetchBranch(c, branch);
  if (command("git", ["-C", c.sourceRepository, "rev-parse", `refs/remotes/origin/${branch}`]) !== sha) throw new Error("Branch advanced during build; nothing promoted");
  let previousEnv = null;
  try { previousEnv = await fs.readFile(envPath); } catch (e) { if (e.code !== "ENOENT") throw e; }
  const values = { ...credentials, PREVIEW_ID: state.id, PREVIEW_PROJECT: state.project, PREVIEW_HOST: state.host,
    PREVIEW_CONTROL: path.join(c.root, "control"), GATEWAY_IMAGE: components.gateway.imageID, WEB_IMAGE: components.web.imageID,
    GATEWAY_RELEASE: components.gateway.release, WEB_RELEASE: components.web.release };
  await writePrivate(envPath, dotenv(values));
  try {
    if (!state.initialized) {
      console.log("Initializing isolated preview data services");
      compose(c, state, ["up", "-d", "--wait", "--wait-timeout", "180", "postgres", "garage"], { PREVIEW_GARAGE_COMMAND: "/garage server --single-node --default-bucket" });
      // Bootstrap flags never remain on a retained Garage layout.
      compose(c, state, ["up", "-d", "--wait", "--wait-timeout", "180", "garage"]);
      compose(c, state, ["up", "-d", "--wait", "--wait-timeout", "180", "gateway"]);
      const exists = compose(c, state, ["exec", "-T", "postgres", "sh", "-c", 'PGPASSWORD="$POSTGRES_PASSWORD" psql -U harden_llm -d harden_llm -tAc "SELECT count(*) FROM users WHERE id=\'preview-local\'"']);
      if (exists !== "1") {
        const args = ["compose", "--env-file", envPath, "-p", state.project, "-f", path.join(c.root, "control/compose.yml"), "exec", "-T", "gateway", "/harden-llm-gateway", "bootstrap-user", "--owner-id", "preview-local", "--email", credentials.OPERATOR_EMAIL, "--password-file", "-"];
        command("docker", args, { input: credentials.OPERATOR_PASSWORD + "\n" });
      }
    }
    if (!state.initialized || services.length) compose(c, state, ["up", "-d", "--no-build", "--no-deps", "--wait", "--wait-timeout", "180", ...(!state.initialized ? ["gateway", "web"] : services)]);
    const guestCreated = ensureTestLogin(c, state, guest);
    const edge = hostCompose(c, ["ps", "-q", "edge"]);
    const [edgeInfo] = JSON.parse(command("docker", ["inspect", edge]));
    if (!edgeInfo.NetworkSettings.Networks[`${state.project}-private`]) command("docker", ["network", "connect", `${state.project}-private`, edge]);
    const route = path.join(c.root, "routes", `${state.id}.caddy`);
    await writePrivate(route, routeFor(state));
    hostCompose(c, ["exec", "-T", "edge", "caddy", "reload", "--config", "/etc/caddy/control/Caddyfile"]);
    const dnsID = await ensureDNS(c, state);
    // New Cloudflare hostnames can take several minutes to reach every edge.
    await healthCheck(state.url, state.initialized ? 90_000 : 300_000);
    if (!state.initialized) await authCheck(state.url, credentials);
    if (!state.initialized || guestCreated) await authCheck(state.url, { OPERATOR_EMAIL: guest.email, OPERATOR_PASSWORD: guest.password });
    for (const service of ["gateway", "web"]) {
      const id = compose(c, state, ["ps", "-q", service]);
      const [info] = JSON.parse(command("docker", ["inspect", id]));
      if (info.Image !== components[service].imageID || info.State.Health?.Status !== "healthy") throw new Error("Running preview image/health mismatch");
    }
    const result = { ...state, components, sha, dnsID, initialized: true, deployedAt: new Date().toISOString(), rebuiltServices: services };
    await saveState(c, result);
    console.log(JSON.stringify({ deployed: true, branch, sha, url: state.url, rebuiltServices: services, loginFile: path.join(directory, "login.txt") }));
    return result;
  } catch (error) {
    if (state.initialized && previousEnv) {
      await writePrivate(envPath, previousEnv);
      compose(c, state, ["up", "-d", "--no-build", "--no-deps", "--wait", "--wait-timeout", "180", "gateway", "web"]);
      console.error("Previous preview application images restored; branch data retained");
    }
    throw error;
  }
}

export async function destroyEnvironment(c, branch) {
  if (branch === "dev") throw new Error("The persistent dev environment cannot be automatically destroyed");
  const state = await stateFor(c, branch);
  if (!state) return;
  const directory = environmentDirectory(c, branch);
  const records = await cf(c, `zones/${c.zoneID}/dns_records?name=${encodeURIComponent(state.host)}`);
  for (const record of records) {
    if (record.type !== "CNAME" || record.content !== `${c.tunnelID}.cfargotunnel.com` || record.comment !== `harden-llm-preview:${state.id}`) throw new Error("Refusing to delete DNS not owned by this preview");
    await cf(c, `zones/${c.zoneID}/dns_records/${record.id}`, "DELETE");
  }
  await fs.rm(path.join(c.root, "routes", `${state.id}.caddy`), { force: true });
  hostCompose(c, ["exec", "-T", "edge", "caddy", "reload", "--config", "/etc/caddy/control/Caddyfile"]);
  const edge = hostCompose(c, ["ps", "-q", "edge"]);
  const [edgeInfo] = JSON.parse(command("docker", ["inspect", edge]));
  if (edgeInfo.NetworkSettings.Networks[`${state.project}-private`]) command("docker", ["network", "disconnect", `${state.project}-private`, edge]);
  try { await fs.access(path.join(directory, ".env")); compose(c, state, ["down", "--volumes", "--remove-orphans"]); }
  catch (e) { if (e.code !== "ENOENT") throw e; }
  try {
    await fs.access(path.join(directory, "source/.git"));
    if (command("git", ["-C", path.join(directory, "source"), "status", "--porcelain"])) throw new Error("Preview worktree has local changes; preserve it for inspection");
    command("git", ["-C", c.sourceRepository, "worktree", "remove", path.join(directory, "source")]);
  } catch (e) { if (e.code !== "ENOENT") throw e; }
  // directory is derived from a validated non-main branch and its matching owned state.
  await fs.rm(directory, { recursive: true });
  console.log(JSON.stringify({ removed: true, branch, url: state.url, dataRecovery: "No automatic recovery; preview data is disposable" }));
}
