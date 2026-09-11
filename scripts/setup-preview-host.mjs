// One-time operator command. No production resources are modified.
import { promises as fs } from "node:fs";
import path from "node:path";
import os from "node:os";
import { createHash } from "node:crypto";
import { fileURLToPath } from "node:url";
import { cf, command, configPath, dotenv, hostCompose, readJSON, repo, syncControl, writePrivate } from "./preview-environment.mjs";

const root = path.join(os.homedir(), ".local/share/harden-llm-previews");
const sourceRepository = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
let config = await readJSON(configPath, null);
if (!config) {
  const tokenFile = await fs.readFile(path.join(os.homedir(), ".config/shaman-public-ssh/cloudflare.env"), "utf8");
  const match = tokenFile.match(/^(?:export )?CLOUDFLARE_API_TOKEN=(.+)$/m);
  if (!match) throw new Error("Cloudflare operator token is missing");
  const cloudflareToken = match[1].trim().replace(/^['"]|['"]$/g, "");
  const [zone] = await cf({ cloudflareToken }, "zones?name=prls.co");
  if (!zone || zone.name !== "prls.co") throw new Error("Expected prls.co zone");
  config = { repository: repo, root, sourceRepository, cloudflareToken, zoneID: zone.id, accountID: zone.account.id };
  const existing = await cf(config, `accounts/${config.accountID}/cfd_tunnel?name=shaman-harden-llm-preview&is_deleted=false`);
  if (existing.length) throw new Error("Preview tunnel already exists without local ownership record; reconcile before setup");
  const tunnel = await cf(config, `accounts/${config.accountID}/cfd_tunnel`, "POST", { name: "shaman-harden-llm-preview", config_src: "cloudflare" });
  config.tunnelID = tunnel.id;
  await writePrivate(configPath, JSON.stringify(config, null, 2) + "\n");
}
if (config.repository !== repo || config.root !== root || config.sourceRepository !== sourceRepository) throw new Error("Host configuration ownership mismatch");
// One operator-managed source for shared logins, provider keys and model setup.
// Infrastructure/session secrets are still generated per environment.
config.sharedEnvFile ??= path.join(os.homedir(), "p/harden-llm/.env");
await writePrivate(configPath, JSON.stringify(config, null, 2) + "\n");
await cf(config, `accounts/${config.accountID}/cfd_tunnel/${config.tunnelID}/configurations`, "PUT", {
  config: { ingress: [{ service: "http://edge:8080" }] },
});
const tunnelToken = await cf(config, `accounts/${config.accountID}/cfd_tunnel/${config.tunnelID}/token`);
await writePrivate(path.join(root, "tunnel-token"), tunnelToken);
await syncControl(config, sourceRepository);
await fs.mkdir(path.join(root, "routes"), { recursive: true, mode: 0o700 });
await writePrivate(path.join(root, "host.env"), dotenv({
  PREVIEW_ROOT: root, PREVIEW_UID: process.getuid(),
  PREVIEW_TUNNEL_IMAGE: "cloudflare/cloudflared:2026.7.3@sha256:e39ee8da81ad5e05d77f38d2f51c60ca51bf2a8450ac3abab50c17fdb91d91bf",
}));
hostCompose(config, ["up", "-d"]);

const runner = path.join(os.homedir(), ".local/share/github-actions/harden-llm-preview");
await fs.mkdir(runner, { recursive: true, mode: 0o700 });
const registered = await fs.access(path.join(runner, ".runner")).then(() => true, e => { if (e.code === "ENOENT") return false; throw e; });
if (!registered) {
  const version = "2.337.0";
  const response = await fetch(`https://github.com/actions/runner/releases/download/v${version}/actions-runner-linux-x64-${version}.tar.gz`, { signal: AbortSignal.timeout(120_000) });
  if (!response.ok) throw new Error("Runner download failed");
  const archive = Buffer.from(await response.arrayBuffer());
  if (createHash("sha256").update(archive).digest("hex") !== "70920811a4f8ad4328818682bca5c6469c1c942fab52448868071d0063816613") throw new Error("Runner checksum mismatch");
  const archivePath = path.join(runner, "runner.tar.gz");
  await writePrivate(archivePath, archive);
  command("tar", ["xzf", archivePath, "-C", runner]);
  await fs.rm(archivePath);
  const { token } = JSON.parse(command("gh", ["api", "--method", "POST", `repos/${repo}/actions/runners/registration-token`]));
  command(path.join(runner, "config.sh"), ["--unattended", "--url", `https://github.com/${repo}`, "--token", token,
    "--name", "harden-llm-preview", "--labels", "harden-llm-preview", "--work", "_work"], { cwd: runner });
}
const unit = `[Unit]\nDescription=Harden-LLM trusted branch preview runner\nAfter=network-online.target\n\n[Service]\nWorkingDirectory=${runner}\nExecStart=/usr/bin/sg docker -c ${runner}/run.sh\nRestart=always\nRestartSec=5\nEnvironment=PATH=/home/kirill/.local/elixir-1.20.2/bin:/home/kirill/.local/otp-28.4.3/bin:/usr/local/bin:/usr/bin:/bin\n\n[Install]\nWantedBy=default.target\n`;
await writePrivate(path.join(os.homedir(), ".config/systemd/user/github-actions-harden-llm-preview.service"), unit);
command("systemctl", ["--user", "daemon-reload"]);
command("systemctl", ["--user", "enable", "--now", "github-actions-harden-llm-preview.service"]);
console.log(JSON.stringify({ configured: true, tunnelID: config.tunnelID, root, runner: "harden-llm-preview", productionChanged: false }));
