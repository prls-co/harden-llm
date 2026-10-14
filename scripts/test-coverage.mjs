import { selectTasks } from "./run-test-tier.mjs";

// A fixture's input model, rather than its current rows, defines coverage.
export function verifyInteractions(model) {
  const entries = Object.entries(model.parameters ?? {});
  if (!entries.length || !Number.isInteger(model.strength) || model.strength < 1 || model.strength > entries.length) throw new Error("invalid interaction model");
  for (const [name, values] of entries) {
    if (!Array.isArray(values) || !values.length || new Set(values.map(JSON.stringify)).size !== values.length || values.some(v => v !== null && !["string", "number", "boolean"].includes(typeof v))) throw new Error(`invalid domain ${name}`);
  }
  const domains = Object.fromEntries(entries);
  const validAssignment = assignment => Object.entries(assignment).every(([key, value]) => domains[key]?.some(v => JSON.stringify(v) === JSON.stringify(value)));
  const constraints = model.constraints ?? [];
  for (const constraint of constraints) if (!Object.keys(constraint).length || !validAssignment(constraint)) throw new Error("invalid constraint");
  const admissible = row => !constraints.some(c => Object.entries(c).every(([k, v]) => row[k] === v));
  const ids = new Set();
  const rows = model.rows ?? [];
  for (const row of rows) {
    if (typeof row.id !== "string" || !row.id || ids.has(row.id)) throw new Error("duplicate or missing row ID");
    ids.add(row.id);
    const assignment = Object.fromEntries(Object.keys(domains).map(k => [k, row[k]]));
    if (!validAssignment(assignment) || !admissible(assignment) || Object.keys(row).some(k => k !== "id" && k !== "expected" && !Object.hasOwn(domains, k))) throw new Error(`invalid row ${row.id}`);
  }
  for (const id of model.requiredRows ?? []) if (!ids.has(id)) throw new Error(`missing regression row ${id}`);
  const groups = [];
  function choose(names, selected = []) {
    if (selected.length === model.strength) { groups.push(selected); return; }
    names.forEach((name, index) => choose(names.slice(index + 1), [...selected, name]));
  }
  choose(Object.keys(domains));
  for (const group of model.requiredInteractions ?? []) {
    if (!Array.isArray(group) || !group.length || new Set(group).size !== group.length || group.some(k => !Object.hasOwn(domains, k))) throw new Error("invalid required interaction");
    groups.push(group);
  }
  let assignments = [{}];
  for (const [key, values] of entries) assignments = assignments.flatMap(row => values.map(v => ({ ...row, [key]: v })));
  assignments = assignments.filter(admissible);
  if (!assignments.length) throw new Error("interaction model admits no assignments");
  let tuples = 0;
  for (const group of groups) {
    const signature = row => JSON.stringify(group.map(k => row[k]));
    const required = new Set(assignments.map(signature));
    const covered = new Set(rows.map(signature));
    for (const tuple of required) if (!covered.has(tuple)) throw new Error(`missing interaction ${group.join(" × ")}: ${tuple}`);
    tuples += required.size;
  }
  return { modelId: model.modelId, rows: rows.length, admissibleAssignments: assignments.length, requiredTuples: tuples };
}

// The reviewed reference cases start at TEST-452. New cases must declare their
// gate in the canonical catalog; task registration stays solely in the manifest.
// This checks source ownership and selection, not observed runtime execution.
export function verifyCaseCoverage(manifest, backendSpec, frontendSpec, files) {
  const sections = [...backendSpec.matchAll(/^### (TEST-(\d+)):([^]*?)(?=^### |^## |$(?![^]))/gm)];
  let checked = 0;
  for (const [, id, number, section] of sections) {
    if (Number(number) < 452) continue;
    const gate = section.match(/^- Coverage gate: `([^`]+)`/m)?.[1];
    if (!gate) throw new Error(`${id} has no coverage gate`);
    const webIDs = [...(section.match(/^- Frontend cases: (.+)$/m)?.[1] ?? "").matchAll(/WEB-TEST-\d+/g)].map(m => m[0]);
    for (const caseID of [id, ...webIDs]) {
      if (caseID.startsWith("WEB") && !frontendSpec.includes(caseID)) throw new Error(`${caseID} is missing from frontend specification`);
      const tasks = manifest.tasks.filter(t => t.testIds.includes(caseID));
      if (gate === "manual") {
        if (tasks.length) throw new Error(`${caseID} manual case is automatically registered`);
        continue;
      }
      if (tasks.length !== 1) throw new Error(`${caseID} has no unique task registration`);
      const task = tasks[0];
      if (!selectTasks(manifest, gate).some(t => t.id === task.id)) throw new Error(`${caseID} task is excluded from ${gate}`);
      const marker = new RegExp(`\\b${caseID}\\b`);
      const consumers = [...files].filter(([file, text]) => section.includes(file) && marker.test(text));
      const owns = ([file, text]) => {
        const command = task.command;
        const directory = task.workingDirectory ?? "";
        if (command[0] === "mix" && command[1] === "test") {
          if (!/^\s*test\s+"[^"\n]+"/m.test(text)) return false;
          const excludes = files.get(`${directory}/test/test_helper.exs`)?.match(/ExUnit\.start\(exclude:\s*\[([^\]]+)\]/)?.[1] ?? "";
          const excludedTags = [...excludes.matchAll(/:(\w+)/g)].map(m => m[1]);
          const moduleTags = [...text.matchAll(/^\s*@moduletag\s+(:\w+|\[[^\]\n]*\])/gm)].flatMap(m => [...m[1].matchAll(/:(\w+)/g)].map(v => v[1]));
          const included = command.flatMap((a, i) => a === "--include" || a === "--only" ? [command[i + 1]] : a.match(/^--(?:include|only)=(.+)$/)?.slice(1) ?? []);
          if (moduleTags.some(tag => excludedTags.includes(tag) && !included.includes(tag))) return false;
          return file.startsWith(`${directory}/test/`) && (command.length === 2 || command.some(a => file === `${directory}/${a}`));
        }
        if (command[0] === "node" && command[1] === "--test") return command.includes(file) && /^\s*test\s*\(/m.test(text);
        const goTests = [...text.matchAll(/^func (Test\w+)\(t \*testing\.T\)/gm)].map(m => m[1]);
        if (!goTests.length) return false;
        if (command.join(" ") === "make test-unit") return file.endsWith("_test.go") && !text.startsWith("//go:build");
        if (command[0] === "go" && command.includes("test")) {
          const run = command.indexOf("-run");
          if (run !== -1 && !goTests.some(name => new RegExp(command[run + 1]).test(name))) return false;
          return file.endsWith("_test.go") && command.some(a => a === "./..." || file.startsWith(a.replace(/^\.\//, "") + "/"));
        }
        return false;
      };
      if (!consumers.some(owns)) throw new Error(`${caseID} has no executable consumer owned by ${task.id}`);
      checked++;
    }
  }
  if (!checked) throw new Error("no reviewed cases found");
  return checked;
}
