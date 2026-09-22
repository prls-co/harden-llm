// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-271 TEST-272 TEST-273 TEST-274 TEST-280

import { createHash, randomBytes } from "node:crypto";
import { spawn, spawnSync } from "node:child_process";
import { constants as fsConstants, promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const SAFE_ID = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,190}$/;
const SAFE_SHA = /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/i;
const STATES = new Set(["registered", "creating", "running", "cleaning", "cleanup-pending", "cleaned"]);
const ACTIVE_DAEMON_LEASES = new Map();
const DAEMON_LOCK_WAIT_MS = 30_000;
const TRANSITIONS = Object.freeze({
  registered: new Set(["creating", "cleaning", "cleanup-pending"]),
  creating: new Set(["running", "cleaning", "cleanup-pending"]),
  running: new Set(["cleaning", "cleanup-pending"]),
  cleaning: new Set(["cleaned", "cleanup-pending"]),
  "cleanup-pending": new Set(["cleaning"]),
  cleaned: new Set(["cleanup-pending"]),
});

export function defaultResourceDirectory() {
  return process.env.HARDEN_LLM_TEST_RESOURCE_DIR
    ? path.resolve(process.env.HARDEN_LLM_TEST_RESOURCE_DIR)
    : path.join(os.homedir(), ".local", "state", "harden-llm", "test-resources");
}

export function validateResourceReceipt(receipt) {
  if (!receipt || typeof receipt !== "object" || Array.isArray(receipt)) throw new Error("resource receipt must be an object");
  if (receipt.schemaVersion !== 1) throw new Error("resource receipt schemaVersion must be 1");
  if (receipt.repository !== "harden-llm" || receipt.environment !== "test" || receipt.disposable !== true) {
    throw new Error("resource receipt must describe disposable Harden-LLM test resources");
  }
  for (const [field, value] of [["runId", receipt.runId], ["project", receipt.project], ["daemonId", receipt.daemonId], ["hostBootId", receipt.hostBootId], ["supervisorStart", receipt.supervisorStart]]) {
    if (typeof value !== "string" || !SAFE_ID.test(value)) throw new Error(`resource receipt ${field} is invalid`);
  }
  if (!receipt.project.startsWith("harden-llm-")) throw new Error("resource receipt project is outside the test namespace");
  if (typeof receipt.sourceSHA !== "string" || !SAFE_SHA.test(receipt.sourceSHA)) throw new Error("resource receipt sourceSHA is invalid");
  if (!Number.isSafeInteger(receipt.supervisorPid) || receipt.supervisorPid <= 0) throw new Error("resource receipt supervisorPid is invalid");
  if (typeof receipt.createdAt !== "string" || !Number.isFinite(Date.parse(receipt.createdAt))) throw new Error("resource receipt createdAt is invalid");
  if (!STATES.has(receipt.state)) throw new Error("resource receipt state is invalid");
  if (!Array.isArray(receipt.composeFiles) || receipt.composeFiles.length === 0 || receipt.composeFiles.some((file) => typeof file !== "string" || !path.isAbsolute(file) || file.includes("\0"))) {
    throw new Error("resource receipt composeFiles must contain absolute paths");
  }
  if (!receipt.resourceIds || typeof receipt.resourceIds !== "object") throw new Error("resource receipt resourceIds are required");
  for (const kind of ["containers", "volumes", "networks"]) {
    const values = receipt.resourceIds[kind];
    if (!Array.isArray(values) || values.some((id) => typeof id !== "string" || !SAFE_ID.test(id)) || new Set(values).size !== values.length) {
      throw new Error(`resource receipt ${kind} must contain unique safe IDs`);
    }
  }
  return receipt;
}

export async function createResourceReceipt({
  directory = defaultResourceDirectory(),
  runId,
  project,
  daemonId,
  composeFiles,
  sourceSHA = currentSourceSHA(),
  now = () => new Date(),
  pid = process.pid,
  hostBootId,
  supervisorStart,
}) {
  const receipt = {
    schemaVersion: 1,
    repository: "harden-llm",
    environment: "test",
    disposable: true,
    runId,
    project,
    sourceSHA,
    daemonId,
    hostBootId: hostBootId ?? await readHostBootID(),
    supervisorPid: pid,
    supervisorStart: supervisorStart ?? await processStartIdentity(pid),
    state: "registered",
    createdAt: now().toISOString(),
    composeFiles: composeFiles.map((file) => path.resolve(file)),
    resourceIds: { containers: [], volumes: [], networks: [] },
  };
  validateResourceReceipt(receipt);
  const receiptPath = receiptPathFor(directory, receipt);
  await writeAtomic(receiptPath, receipt, { exclusive: true });
  return { receiptPath, receipt };
}

export async function updateResourceReceipt(receiptPath, state, patch = {}) {
  const previous = await readResourceReceipt(receiptPath);
  if (state !== previous.state && !TRANSITIONS[previous.state]?.has(state)) throw new Error(`invalid resource receipt transition ${previous.state} -> ${state}`);
  if (Object.keys(patch).some((key) => key !== "resourceIds")) throw new Error("resource receipt identity fields are immutable");
  const next = { ...previous, ...patch, state };
  validateResourceReceipt(next);
  await writeAtomic(receiptPath, next, { exclusive: false });
  return next;
}

export async function readResourceReceipt(receiptPath) {
  await assertPrivateDirectory(path.dirname(receiptPath));
  const metadata = await fs.lstat(receiptPath);
  if (!metadata.isFile() || metadata.isSymbolicLink()) throw new Error("resource receipt must be a regular file");
  if ((metadata.mode & 0o077) !== 0 || (typeof process.getuid === "function" && metadata.uid !== process.getuid())) {
    throw new Error("resource receipt permissions or ownership are not private");
  }
  return validateResourceReceipt(JSON.parse(await fs.readFile(receiptPath, "utf8")));
}

function receiptPathFor(directory, receipt) {
  if (typeof receipt.runId !== "string" || !SAFE_ID.test(receipt.runId)) throw new Error("resource receipt runId is invalid");
  if (typeof receipt.project !== "string" || !SAFE_ID.test(receipt.project)) throw new Error("resource receipt project is invalid");
  return path.join(path.resolve(directory), receipt.runId, `resource-${receipt.project}.json`);
}

async function writeAtomic(receiptPath, receipt, { exclusive }) {
  const directory = path.dirname(receiptPath);
  await assertPrivateDirectory(directory, true);
  try {
    const existing = await fs.lstat(receiptPath);
    if (existing.isSymbolicLink() || !existing.isFile()) throw new Error("resource receipt target is not a regular file");
    if (exclusive) throw new Error("resource receipt already exists");
    if ((existing.mode & 0o077) !== 0 || (typeof process.getuid === "function" && existing.uid !== process.getuid())) {
      throw new Error("resource receipt permissions or ownership are not private");
    }
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
    if (!exclusive) throw new Error("resource receipt disappeared before update");
  }
  const temporary = path.join(directory, `.resource-${process.pid}-${randomBytes(12).toString("hex")}.tmp`);
  const handle = await fs.open(temporary, fsConstants.O_CREAT | fsConstants.O_EXCL | fsConstants.O_WRONLY, 0o600);
  try {
    await handle.writeFile(`${JSON.stringify(receipt)}\n`, "utf8");
    await handle.sync();
  } finally {
    await handle.close();
  }
  try {
    if (exclusive) {
      await fs.link(temporary, receiptPath);
      await fs.unlink(temporary);
    } else {
      await fs.rename(temporary, receiptPath);
    }
    const directoryHandle = await fs.open(directory, "r");
    try { await directoryHandle.sync(); } finally { await directoryHandle.close(); }
  } catch (error) {
    await fs.unlink(temporary).catch(() => {});
    throw error;
  }
}

async function assertPrivateDirectory(directory, create = false) {
  if (create) await fs.mkdir(directory, { recursive: true, mode: 0o700 });
  const metadata = await fs.lstat(directory);
  if (!metadata.isDirectory() || metadata.isSymbolicLink()) throw new Error("resource receipt directory must be a real directory");
  if ((metadata.mode & 0o077) !== 0 || (typeof process.getuid === "function" && metadata.uid !== process.getuid())) {
    throw new Error("resource receipt directory permissions or ownership are not private");
  }
}

function currentSourceSHA() {
  const result = spawnSync("git", ["rev-parse", "HEAD"], { cwd: path.resolve(path.dirname(fileURLToPath(import.meta.url)), ".."), encoding: "utf8" });
  if (result.status !== 0) throw new Error("cannot identify source revision for resource receipt");
  return result.stdout.trim();
}

export async function readHostBootID() {
  const value = (await fs.readFile("/proc/sys/kernel/random/boot_id", "utf8")).trim();
  if (!SAFE_ID.test(value)) throw new Error("cannot establish host boot identity for resource receipt");
  return value;
}

export async function processStartIdentity(pid = process.pid) {
  const stat = await fs.readFile(`/proc/${pid}/stat`, "utf8");
  const closingParenthesis = stat.lastIndexOf(")");
  if (closingParenthesis < 0) throw new Error("cannot parse supervisor process identity");
  const fields = stat.slice(closingParenthesis + 1).trim().split(/\s+/);
  const start = fields[19]; // /proc stat field 22, relative to field 3 after comm.
  if (!start || !/^\d+$/.test(start)) throw new Error("cannot establish supervisor process start identity");
  return start;
}

export async function classifyReceiptOwner(receipt) {
  const currentBootID = await readHostBootID();
  if (receipt.hostBootId !== currentBootID) return { status: "dead", reason: "host boot identity changed" };
  let currentStart;
  try {
    currentStart = await processStartIdentity(receipt.supervisorPid);
  } catch (error) {
    if (error.code === "ENOENT" || error.code === "ESRCH") return { status: "dead", reason: "supervisor process no longer exists" };
    return { status: "ambiguous", reason: `cannot establish supervisor identity: ${error.message}` };
  }
  if (currentStart === receipt.supervisorStart) return { status: "active", reason: "supervisor PID and start identity still match" };
  return { status: "dead", reason: "supervisor PID was reused with a different process-start identity" };
}

export async function acquireDaemonLock({ daemonId, resourceDirectory = defaultResourceDirectory(), waitMs = DAEMON_LOCK_WAIT_MS, environment = process.env, signal }) {
  if (process.platform !== "linux") throw new Error("managed Docker tasks require Linux util-linux flock; unlocked fallback is not allowed");
  if (typeof daemonId !== "string" || !SAFE_ID.test(daemonId)) throw new Error("cannot lock an invalid Docker daemon identity");
  if (!Number.isSafeInteger(waitMs) || waitMs <= 0 || waitMs > DAEMON_LOCK_WAIT_MS) throw new Error(`Docker daemon lock wait must be between 1 and ${DAEMON_LOCK_WAIT_MS} ms`);
  const root = path.resolve(resourceDirectory);
  await fs.mkdir(root, { recursive: true, mode: 0o700 });
  await assertPrivateDirectory(root);
  const lockDirectory = path.join(root, "locks");
  await fs.mkdir(lockDirectory, { recursive: true, mode: 0o700 });
  await assertPrivateDirectory(lockDirectory);
  const daemonKey = createHash("sha256").update(daemonId, "utf8").digest("hex");
  const lockPath = path.join(lockDirectory, `${daemonKey}.lock`);
  const noFollow = fsConstants.O_NOFOLLOW ?? 0;
  const lockFile = await fs.open(lockPath, fsConstants.O_CREAT | fsConstants.O_RDWR | noFollow, 0o600);
  try {
    const metadata = await lockFile.stat();
    if (!metadata.isFile() || (metadata.mode & 0o077) !== 0 || (typeof process.getuid === "function" && metadata.uid !== process.getuid())) {
      throw new Error("Docker daemon lock file permissions, ownership, or file type are not private");
    }
  } finally {
    await lockFile.close();
  }

  const ownerPid = process.pid;
  const ownerStart = await processStartIdentity(ownerPid);
  const hostBootId = await readHostBootID();
  const token = randomBytes(32).toString("hex");
  const holderCode = "process.stdout.write('HARDEN_LLM_LOCKED\\n'); process.stdin.resume();";
  const child = spawn("flock", ["--exclusive", "--wait", String(Math.max(1, Math.ceil(waitMs / 1000))), lockPath, process.execPath, "-e", holderCode], {
    stdio: ["pipe", "pipe", "pipe"],
    env: environment,
  });
  let stdout = "";
  let stderr = "";
  child.stdout.setEncoding("utf8");
  child.stderr.setEncoding("utf8");
  const ready = await new Promise((resolve, reject) => {
    let settled = false;
    const timeout = setTimeout(() => finish(new Error("timed out waiting for the local Docker daemon lock")), waitMs + 2_000);
    const finish = (error) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      signal?.removeEventListener("abort", abortHandler);
      if (error) reject(error);
      else resolve(true);
    };
    const abortHandler = () => {
      child.kill("SIGTERM");
      finish(new Error("cancelled while waiting for the local Docker daemon lock"));
    };
    if (signal?.aborted) abortHandler();
    else signal?.addEventListener("abort", abortHandler, { once: true });
    child.stdout.on("data", (chunk) => {
      stdout += chunk;
      if (stdout.includes("HARDEN_LLM_LOCKED\n")) finish(null);
    });
    child.stderr.on("data", (chunk) => { stderr = (stderr + chunk).slice(-4096); });
    child.once("error", (error) => finish(new Error(`cannot start util-linux flock: ${error.message}`)));
    child.once("close", (code) => {
      if (!settled) finish(new Error(`cannot acquire local Docker daemon lock (flock exit ${code ?? "unknown"}): ${stderr.trim() || "lock wait expired"}`));
    });
  }).catch(async (error) => {
    if (child.exitCode === null && child.signalCode === null) child.kill("SIGTERM");
    if (child.exitCode === null && child.signalCode === null) await new Promise((resolve) => child.once("close", resolve)).catch(() => {});
    throw error;
  });
  if (!ready) throw new Error("cannot acquire local Docker daemon lock");
  let holderStart;
  try { holderStart = await processStartIdentity(child.pid); }
  catch (error) {
    child.kill("SIGTERM");
    throw new Error(`cannot verify local Docker daemon lock holder: ${error.message}`);
  }
  const lease = { daemonId, lockPath, ownerPid, ownerStart, hostBootId, holderPid: child.pid, holderStart, token, child, delegated: false };
  ACTIVE_DAEMON_LEASES.set(token, lease);
  return lease;
}

export async function inheritedDaemonLockLease({ daemonId, resourceDirectory = defaultResourceDirectory(), environment = process.env }) {
  const token = environment.HARDEN_LLM_TEST_DAEMON_LOCK_TOKEN;
  if (!token) return null;
  const expectedPath = path.join(path.resolve(resourceDirectory), "locks", `${createHash("sha256").update(daemonId, "utf8").digest("hex")}.lock`);
  const lockPath = environment.HARDEN_LLM_TEST_DAEMON_LOCK_PATH;
  const inheritedDaemonId = environment.HARDEN_LLM_TEST_DAEMON_LOCK_DAEMON_ID;
  const ownerPid = Number(environment.HARDEN_LLM_TEST_DAEMON_LOCK_OWNER_PID);
  const ownerStart = environment.HARDEN_LLM_TEST_DAEMON_LOCK_OWNER_START;
  const hostBootId = environment.HARDEN_LLM_TEST_DAEMON_LOCK_HOST_BOOT_ID;
  const holderPid = Number(environment.HARDEN_LLM_TEST_DAEMON_LOCK_HOLDER_PID);
  const holderStart = environment.HARDEN_LLM_TEST_DAEMON_LOCK_HOLDER_START;
  if (inheritedDaemonId !== daemonId || lockPath !== expectedPath || !/^[a-f0-9]{64}$/.test(token) || !Number.isSafeInteger(ownerPid) || ownerPid <= 0 || !/^[0-9]+$/.test(ownerStart ?? "") || !Number.isSafeInteger(holderPid) || holderPid <= 0 || !/^[0-9]+$/.test(holderStart ?? "")) {
    throw new Error("inherited Docker daemon lock lease does not match this daemon or private lock path");
  }
  const currentBootID = await readHostBootID();
  if (hostBootId !== currentBootID) return null;
  let currentHolderStart;
  try { currentHolderStart = await processStartIdentity(holderPid); }
  catch (error) {
    if (error.code === "ENOENT" || error.code === "ESRCH") return null;
    throw new Error(`cannot validate inherited Docker daemon lock holder: ${error.message}`);
  }
  if (currentHolderStart !== holderStart) return null;
  if (ownerPid === process.pid) {
    const active = ACTIVE_DAEMON_LEASES.get(token);
    return active && active.daemonId === daemonId && active.ownerStart === ownerStart && active.hostBootId === hostBootId && active.holderPid === holderPid && active.holderStart === holderStart ? { ...active, delegated: true } : null;
  }
  let currentStart;
  try { currentStart = await processStartIdentity(ownerPid); }
  catch (error) {
    if (error.code === "ENOENT" || error.code === "ESRCH") return null;
    throw new Error(`cannot validate inherited Docker daemon lock owner: ${error.message}`);
  }
  if (currentStart !== ownerStart) return null;
  return { daemonId, lockPath, ownerPid, ownerStart, hostBootId, holderPid, holderStart, token, delegated: true };
}

export function daemonLockEnvironment(lease) {
  if (!lease) return {};
  return {
    HARDEN_LLM_TEST_DAEMON_LOCK_TOKEN: lease.token,
    HARDEN_LLM_TEST_DAEMON_LOCK_DAEMON_ID: lease.daemonId,
    HARDEN_LLM_TEST_DAEMON_LOCK_PATH: lease.lockPath,
    HARDEN_LLM_TEST_DAEMON_LOCK_OWNER_PID: String(lease.ownerPid),
    HARDEN_LLM_TEST_DAEMON_LOCK_OWNER_START: lease.ownerStart,
    HARDEN_LLM_TEST_DAEMON_LOCK_HOST_BOOT_ID: lease.hostBootId,
    HARDEN_LLM_TEST_DAEMON_LOCK_HOLDER_PID: String(lease.holderPid),
    HARDEN_LLM_TEST_DAEMON_LOCK_HOLDER_START: lease.holderStart,
  };
}

export async function releaseDaemonLock(lease) {
  if (!lease || lease.delegated || !lease.child) return;
  ACTIVE_DAEMON_LEASES.delete(lease.token);
  if (lease.child.exitCode !== null || lease.child.signalCode !== null) return;
  const closed = new Promise((resolve) => lease.child.once("close", resolve));
  lease.child.stdin.end();
  const waitForClose = async (milliseconds) => Promise.race([
    closed.then(() => true),
    new Promise((resolve) => setTimeout(() => resolve(false), milliseconds)),
  ]);
  if (await waitForClose(2_000)) return;
  lease.child.kill("SIGTERM");
  if (await waitForClose(2_000)) return;
  lease.child.kill("SIGKILL");
  await closed;
}
