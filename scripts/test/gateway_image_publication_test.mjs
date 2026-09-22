// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-283
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");

function readRepositoryFile(relativePath) {
  return readFileSync(path.join(repositoryRoot, relativePath), "utf8");
}

function jobBody(workflow, name) {
  const header = `\n  ${name}:\n`;
  const headerOffset = workflow.indexOf(header);
  assert.notEqual(headerOffset, -1, `publisher workflow must define the ${name} job`);
  const bodyStart = workflow.indexOf("\n", headerOffset + 1) + 1;
  const remaining = workflow.slice(bodyStart);
  const nextJobOffset = remaining.search(/^  [A-Za-z][A-Za-z0-9_-]*:\s*$/m);
  return remaining.slice(0, nextJobOffset === -1 ? undefined : nextJobOffset);
}

test("TEST-283 publishing is manual, main-only, and follows exact-source release certification", () => {
  const workflow = readRepositoryFile(".github/workflows/publish-gateway-image.yml");
  const header = workflow.slice(0, workflow.indexOf("\njobs:\n"));
  assert.match(header, /^on:\n  workflow_dispatch:\s*$/m);
  assert.doesNotMatch(header, /^\s{2}(push|pull_request|schedule):/m);
  assert.match(header, /^permissions:\n  contents: read\s*$/m);
  assert.doesNotMatch(header, /packages:\s*write/);

  const certify = jobBody(workflow, "certify");
  assert.match(certify, /if: github\.ref == 'refs\/heads\/main'/);
  assert.match(header, /GO_VERSION: "1\.26\.6"/);
  assert.match(header, /NODE_VERSION: "22\.22\.1"/);
  assert.match(header, /ELIXIR_VERSION: "1\.20\.2"/);
  assert.match(header, /OTP_VERSION: "28\.4\.3"/);
  assert.match(certify, /go-version:\s*\$\{\{\s*env\.GO_VERSION\s*\}\}/);
  assert.match(certify, /node-version:\s*\$\{\{\s*env\.NODE_VERSION\s*\}\}/);
  assert.match(certify, /elixir-version:\s*\$\{\{\s*env\.ELIXIR_VERSION\s*\}\}/);
  assert.match(certify, /otp-version:\s*\$\{\{\s*env\.OTP_VERSION\s*\}\}/);
  assert.match(certify, /make test-release/);
  assert.match(certify, /ref:\s*\$\{\{\s*github\.sha\s*\}\}/);
  assert.match(certify, /persist-credentials:\s*false/);
  assert.doesNotMatch(certify, /packages:\s*write/);
  assert.doesNotMatch(certify, /make test-browser|make test-browser-compose|--allow-browser/);
});

test("TEST-283 publishes a private, provenance-bearing digest with narrowly scoped credentials", () => {
  const workflow = readRepositoryFile(".github/workflows/publish-gateway-image.yml");
  const publish = jobBody(workflow, "publish");
  const actions = workflow.match(/^\s+uses:\s+[^\s]+$/gm) ?? [];
  const pinnedActions = workflow.match(/^\s+uses:\s+[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+@[0-9a-f]{40}\s*$/gm) ?? [];

  assert.equal(actions.length, pinnedActions.length, "every third-party action must use a full commit SHA");
  assert.ok(actions.length > 0, "publisher workflow must use the pinned test/build/report actions");
  assert.match(publish, /needs:\s*certify/);
  assert.match(publish, /if: github\.ref == 'refs\/heads\/main'/);
  assert.match(publish, /permissions:\n\s+contents: read\n\s+packages: write/);
  assert.equal((workflow.match(/packages:\s*write/g) ?? []).length, 1, "package write permission must exist only in the publisher job");
  assert.match(publish, /secrets\.GITHUB_TOKEN/);
  assert.doesNotMatch(publish, /PERSONAL_ACCESS_TOKEN|GHCR_PAT|REGISTRY_PASSWORD/);
  assert.match(publish, /ghcr\.io\/prls-co\/harden-llm-gateway/);
  assert.match(publish, /IMAGE_TAG=.*GITHUB_SHA.*GITHUB_RUN_ID.*GITHUB_RUN_ATTEMPT/);
  assert.match(publish, /--platform linux\/amd64/);
  assert.match(publish, /--build-arg VERSION=.*GITHUB_SHA/);
  assert.match(publish, /--build-arg REVISION=.*GITHUB_SHA/);
  assert.match(publish, /--provenance=mode=max/);
  assert.match(publish, /--metadata-file/);
  assert.match(publish, /--push/);
  assert.match(publish, /jq -n/);
  for (const reportField of ["schemaVersion", "sourceSHA", "image", "tag", "digest", "platform", "provenance", "workflowRunID", "workflowRunAttempt", "packageVisibility"]) {
    assert.match(publish, new RegExp(`\\b${reportField}\\s*:`), `publication report must include ${reportField}`);
  }
  assert.match(publish, /chmod 600 \"\$REPORT_FILE\"/);
  assert.match(publish, /gh api orgs\/prls-co\/packages\/container\/harden-llm-gateway/);
  assert.match(publish, /visibility.*private|private.*visibility/s);
  assert.match(publish, /gateway-image-publication\.json/);
  assert.match(publish, /retention-days: 90/);
  assert.doesNotMatch(publish, /:latest\b/);

  const dockerfile = readRepositoryFile("Dockerfile");
  assert.match(dockerfile, /org\.opencontainers\.image\.source="https:\/\/github\.com\/prls-co\/harden-llm"/);
  assert.match(dockerfile, /org\.opencontainers\.image\.revision="\$\{REVISION\}"/);
  assert.match(dockerfile, /org\.opencontainers\.image\.version="\$\{VERSION\}"/);
});
