#!/usr/bin/env node

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-004

import { createHash } from "node:crypto";
import { readdir, readFile } from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const fixtureRoot = path.join(repositoryRoot, "fixtures", "parity");
const manifestPath = path.join(fixtureRoot, "manifest.json");
const supportedManifestVersions = new Set([1]);
const supportedFixtureVersions = new Set([1]);
const secretPatterns = [
  /-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----/i,
  /\b(?:sk|pk|rk|api)[-_][a-z0-9]{20,}\b/i,
  /\bAIza[0-9A-Za-z_-]{30,}\b/,
  /\bgh[pousr]_[A-Za-z0-9]{30,}\b/,
  /\bAKIA[0-9A-Z]{16}\b/,
];

function fail(message) {
  console.error(`fixture verification failed: ${message}`);
  process.exitCode = 1;
}

async function listFiles(directory) {
  const result = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    if (entry.name === "manifest.json") continue;
    const absolute = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      result.push(...await listFiles(absolute));
    } else if (entry.isFile()) {
      result.push(path.relative(fixtureRoot, absolute).split(path.sep).join("/"));
    }
  }
  return result.sort();
}

let manifest;
try {
  manifest = JSON.parse(await readFile(manifestPath, "utf8"));
} catch (error) {
  fail(`cannot read ${path.relative(repositoryRoot, manifestPath)}: ${error.message}`);
  process.exit();
}

if (!supportedManifestVersions.has(manifest.manifestSchemaVersion)) {
  fail(`unsupported manifest schema version ${manifest.manifestSchemaVersion}`);
}
if (!/^[0-9a-f]{40}$/.test(manifest.source?.gitSHA ?? "")) {
  fail("source.gitSHA must be a full lowercase Git commit");
}
if (!Array.isArray(manifest.fixtures) || manifest.fixtures.length === 0) {
  fail("manifest must contain at least one fixture");
}
for (const difference of manifest.intentionalDifferences ?? []) {
  if (difference.mode !== "intentional-difference" || !/^ADR-HLLM-\d{3}$/.test(difference.adr ?? "") || !String(difference.note ?? "").trim()) {
    fail(`intentional difference ${String(difference.id)} must name its mode, ADR, and note`);
  }
}

const manifestPaths = new Set();
const classes = new Map();
for (const fixture of manifest.fixtures ?? []) {
  if (typeof fixture.path !== "string" || fixture.path.startsWith("/") || fixture.path.includes("..")) {
    fail(`unsafe fixture path ${String(fixture.path)}`);
    continue;
  }
  if (manifestPaths.has(fixture.path)) fail(`duplicate fixture path ${fixture.path}`);
  manifestPaths.add(fixture.path);
  if (!supportedFixtureVersions.has(fixture.schemaVersion)) {
    fail(`unsupported fixture schema version for ${fixture.path}`);
  }
  const absolute = path.join(fixtureRoot, fixture.path);
  try {
    const bytes = await readFile(absolute);
    const digest = createHash("sha256").update(bytes).digest("hex");
    if (digest !== fixture.sha256) fail(`SHA-256 mismatch for ${fixture.path}`);
    const text = bytes.toString("utf8");
    for (const pattern of secretPatterns) {
      if (pattern.test(text)) fail(`possible secret in ${fixture.path}`);
    }
    classes.set(fixture.class, (classes.get(fixture.class) ?? 0) + 1);
  } catch (error) {
    fail(`cannot verify ${fixture.path}: ${error.message}`);
  }
}

for (const actualPath of await listFiles(fixtureRoot)) {
  if (!manifestPaths.has(actualPath)) fail(`untracked fixture ${actualPath}`);
}
for (const manifestFixture of manifestPaths) {
  if (!(await listFiles(fixtureRoot)).includes(manifestFixture)) fail(`missing fixture ${manifestFixture}`);
}

if (!process.exitCode) {
  const summary = [...classes].sort(([left], [right]) => left.localeCompare(right))
    .map(([name, count]) => `${name}=${count}`).join(" ");
  console.log(`verified ${manifestPaths.size} parity fixtures at ${manifest.source.gitSHA}; ${summary}`);
}
