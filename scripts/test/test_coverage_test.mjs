// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-460
import assert from "node:assert/strict";
import { test } from "node:test";
import { readFileSync } from "node:fs";
import { verifyInteractions, verifyCaseCoverage } from "../test-coverage.mjs";

test("retry fixture retains every declared pair and required triple", () => {
  const model = JSON.parse(readFileSync(new URL("../../fixtures/parity/source/combinatorial/retry-decision-matrix.json", import.meta.url)));
  assert.equal(verifyInteractions(model).admissibleAssignments, 36);
  assert.throws(() => verifyInteractions({ ...model, rows: model.rows.slice(1) }), /missing interaction/);
});

test("coverage uses feasible domain tuples and permanent regression IDs", () => {
  const model = { modelId: "small", strength: 2, parameters: { source: ["explicit", "inherited"], effort: ["none", "high"] }, constraints: [{ source: "inherited", effort: "high" }], requiredRows: ["incident"], rows: [{ id: "incident", source: "explicit", effort: "high" }, { id: "b", source: "explicit", effort: "none" }, { id: "c", source: "inherited", effort: "none" }] };
  assert.equal(verifyInteractions(model).admissibleAssignments, 3);
  assert.throws(() => verifyInteractions({ ...model, rows: model.rows.filter(r => r.id !== "incident") }), /missing regression/);
  assert.throws(() => verifyInteractions({ ...model, rows: [...model.rows, model.rows[0]] }), /duplicate/);
  assert.throws(() => verifyInteractions({ ...model, constraints: [{ missing: true }] }), /invalid constraint/);
  assert.throws(() => verifyInteractions({ ...model, rows: [...model.rows, { id: "bad", source: "inherited", effort: "high" }] }), /invalid row/);
});

function registration() {
  return { schemaVersion: 1, documentId: "fixture", resourceClasses: { cpu: { slots: 1 } }, tasks: [{ id: "cases", testIds: ["TEST-460", "WEB-TEST-128"], tier: "T1", resourceClass: "cpu", command: ["mix", "test"], workingDirectory: "frontend", dependsOn: [], timeoutMs: 1000, cleanupOwner: "runner", network: "forbidden", credentialKeys: [], requiredFor: ["fast"], pathSelectors: [] }] };
}
const spec = "### TEST-460: fixture\n- Coverage gate: `fast`.\n- Frontend cases: WEB-TEST-128.\n- Location: `frontend/test/case_test.exs`.\n";
const files = new Map([["frontend/test/case_test.exs", '# TEST-460 WEB-TEST-128\ntest "actual" do\nassert result\nend']]);
test("case closure rejects lost registration, gate and executable consumer", () => {
  assert.equal(verifyCaseCoverage(registration(), spec, "WEB-TEST-128", files), 2);
  const missing = registration(); missing.tasks[0].testIds = ["WEB-TEST-128"];
  assert.throws(() => verifyCaseCoverage(missing, spec, "WEB-TEST-128", files), /registration/);
  const excluded = registration(); excluded.tasks.push({ ...excluded.tasks[0], id: "control", testIds: ["TEST-050"] }); excluded.tasks[0].requiredFor = [];
  assert.throws(() => verifyCaseCoverage(excluded, spec, "WEB-TEST-128", files), /excluded/);
  assert.throws(() => verifyCaseCoverage(registration(), spec, "WEB-TEST-128", new Map()), /consumer/);
  assert.throws(() => verifyCaseCoverage(registration(), spec, "WEB-TEST-128", new Map([["frontend/test/case_test.exs", "# TEST-460 WEB-TEST-128\n"]])), /consumer/);
  const excludedModule = new Map(files);
  excludedModule.set("frontend/test/test_helper.exs", "ExUnit.start(exclude: [:database])");
  excludedModule.set("frontend/test/case_test.exs", '@moduletag :database\n' + files.get("frontend/test/case_test.exs"));
  assert.throws(() => verifyCaseCoverage(registration(), spec, "WEB-TEST-128", excludedModule), /consumer/);
  assert.throws(() => verifyCaseCoverage(registration(), spec.replace("`fast`", "`manual`"), "WEB-TEST-128", files), /automatically registered/);
});
