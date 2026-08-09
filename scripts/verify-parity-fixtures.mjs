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
const supportedManifestVersions = new Set([2]);
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

const testSpecification = await readFile(path.join(repositoryRoot, "plans", "from_utility-llm", "harden-llm-self-hosted-test-spec.md"), "utf8");
const canonicalTestIDs = new Set([...testSpecification.matchAll(/^### (TEST-\d{3}):/gm)].map((match) => match[1]));
const adrFiles = await readdir(path.join(repositoryRoot, "docs", "adr"));

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
  if (!/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(difference.id ?? "") || difference.mode !== "intentional-difference" || !/^ADR-HLLM-\d{3}$/.test(difference.adr ?? "") || !String(difference.note ?? "").trim()) {
    fail(`intentional difference ${String(difference.id)} must name its mode, ADR, and note`);
  }
  if (!Array.isArray(difference.fixtures) || !Array.isArray(difference.tests) || difference.tests.length === 0 || difference.tests.some((id) => !/^TEST-\d{3}$/.test(id))) {
    fail(`intentional difference ${String(difference.id)} must list fixtures and canonical tests`);
  }
  for (const testID of difference.tests ?? []) {
    if (!canonicalTestIDs.has(testID)) fail(`intentional difference ${String(difference.id)} references undefined test ${testID}`);
  }
  if (!adrFiles.some((file) => file.startsWith(`${difference.adr}-`) && file.endsWith(".md"))) {
    fail(`intentional difference ${String(difference.id)} references missing ${String(difference.adr)}`);
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

const actualPaths = await listFiles(fixtureRoot);
const actualPathSet = new Set(actualPaths);
for (const actualPath of actualPaths) {
  if (!manifestPaths.has(actualPath)) fail(`untracked fixture ${actualPath}`);
}
for (const manifestFixture of manifestPaths) {
  if (!actualPathSet.has(manifestFixture)) fail(`missing fixture ${manifestFixture}`);
}
const classifiedFixtures = new Set();
for (const difference of manifest.intentionalDifferences ?? []) {
  for (const fixture of difference.fixtures ?? []) {
    if (!manifestPaths.has(fixture)) fail(`intentional difference ${difference.id} references unknown fixture ${fixture}`);
    classifiedFixtures.add(fixture);
  }
}

if (!manifest.fixtureConsumers || typeof manifest.fixtureConsumers !== "object" || Array.isArray(manifest.fixtureConsumers)) {
  fail("manifest must contain a fixtureConsumers object");
}
for (const [fixture, consumers] of Object.entries(manifest.fixtureConsumers ?? {})) {
  if (!manifestPaths.has(fixture)) {
    fail(`fixture consumer references unknown fixture ${fixture}`);
    continue;
  }
  classifiedFixtures.add(fixture);
  if (!Array.isArray(consumers) || consumers.length === 0) {
    fail(`fixture ${fixture} must name at least one semantic test consumer`);
    continue;
  }
  for (const consumer of consumers) {
    const target = String(consumer.target ?? "");
    const testFunction = String(consumer.testFunction ?? "");
    const tests = consumer.tests;
    const evidence = String(consumer.evidence ?? "");
    if (!target || path.isAbsolute(target) || target.split("/").includes("..") || !/_test\.(?:go|exs)$/.test(target)) {
      fail(`fixture ${fixture} has unsafe semantic consumer target ${target}`);
      continue;
    }
    if (!Array.isArray(tests) || tests.length === 0 || tests.some((id) => !/^TEST-\d{3}$/.test(id))) {
      fail(`fixture ${fixture} consumer ${target} must list canonical tests`);
      continue;
    }
    if (!/^Test[A-Za-z0-9_]+$/.test(testFunction) || !/(?:Parity|Contract|Identity|Replay)/.test(testFunction)) {
      fail(`fixture ${fixture} consumer ${target} must name a test selected by make test-parity`);
      continue;
    }
    for (const testID of tests) {
      if (!canonicalTestIDs.has(testID)) fail(`fixture ${fixture} consumer ${target} references undefined test ${testID}`);
    }
    if (!evidence.trim()) {
      fail(`fixture ${fixture} consumer ${target} must name fixture-read evidence`);
      continue;
    }
    try {
      const targetText = await readFile(path.join(repositoryRoot, target), "utf8");
      if (!targetText.includes("SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001")) {
        fail(`fixture ${fixture} consumer ${target} lacks the canonical test specification marker`);
      }
      if (!targetText.includes(`func ${testFunction}(`)) {
        fail(`fixture ${fixture} consumer ${target} lacks ${testFunction}`);
      }
      for (const testID of tests) {
        if (!targetText.includes(testID)) fail(`fixture ${fixture} consumer ${target} lacks ${testID}`);
      }
      if (!targetText.includes(evidence)) {
        fail(`fixture ${fixture} consumer ${target} lacks fixture-read evidence ${JSON.stringify(evidence)}`);
      }
    } catch (error) {
      fail(`cannot verify fixture ${fixture} consumer ${target}: ${error.message}`);
    }
  }
}
for (const fixture of manifestPaths) {
  if (!classifiedFixtures.has(fixture)) {
    fail(`fixture ${fixture} has neither a semantic test consumer nor an intentional-difference classification`);
  }
}

if (!process.exitCode) {
  const summary = [...classes].sort(([left], [right]) => left.localeCompare(right))
    .map(([name, count]) => `${name}=${count}`).join(" ");
  console.log(`verified ${manifestPaths.size} parity fixtures at ${manifest.source.gitSHA}; ${summary}`);
}
