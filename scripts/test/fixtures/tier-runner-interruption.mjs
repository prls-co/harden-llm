// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-532: process-owned fixture only.
import { promises as fs, existsSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { runTasks, watchProcessSignals } from "../../run-test-tier.mjs";

const [root, mode, id] = process.argv.slice(2);
const filename = fileURLToPath(import.meta.url);
if (id) {
  const identity = async pid => {
    const stat = await fs.readFile(`/proc/${pid}/stat`, "utf8");
    return { pid, start: stat.slice(stat.lastIndexOf(")") + 2).split(" ")[19] };
  };
  if (mode === "abort-resistant") {
    process.on("SIGTERM", () => {});
    process.on("SIGINT", () => {});
  }
  await fs.appendFile(path.join(root, "events.jsonl"), JSON.stringify({ id, identities: await Promise.all([identity(process.pid), identity(process.ppid)]) }) + "\n");
  const wait = () => existsSync(path.join(root, "release")) ? process.exit(0) : setTimeout(wait, 10);
  wait();
} else {
  const cancellation = watchProcessSignals();
  try {
    const tasks = ["a", "b", "c"].map(id => ({
      id, tier: "T0", resourceClass: "cpu", cleanupOwner: "runner-test", network: "forbidden",
      command: [process.execPath, filename, root, mode, id], workingDirectory: ".", dependsOn: [], timeoutMs: 30_000,
    }));
    const report = await runTasks(tasks, { root, runDirectory: path.join(root, "run"), resourceClasses: { cpu: { slots: 3 } }, signal: cancellation.signal });
    await fs.writeFile(path.join(root, "result.json"), JSON.stringify(report));
    process.exitCode = report.accepted ? 0 : 1;
  } finally {
    cancellation.dispose();
  }
}
