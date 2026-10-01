// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-062
// The shared .env is trusted host configuration, never branch-supplied input.
import { parseEnv } from 'node:util';
import { readFile } from 'node:fs/promises';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { command } from './host-command.mjs';

export function sharedProfiles(values) {
  let profiles, references;
  try {
    if (!path.isAbsolute(values.HARDEN_LLM_CONFIG_FILE ?? '')) throw new Error('absolute config path required');
    ({ profiles, credentialEnv: references } = JSON.parse(readFileSync(values.HARDEN_LLM_CONFIG_FILE, 'utf8')));
  } catch { throw new Error('invalid shared profile configuration'); }
  if (!profiles || typeof profiles !== 'object' || Array.isArray(profiles) || !Object.keys(profiles).length || !references || typeof references !== 'object' || Array.isArray(references)) throw new Error('invalid shared profile configuration');
  const credentials = [];
  for (const [name, variable] of Object.entries(references)) {
    if (!Object.hasOwn(profiles, name)) throw new Error('shared credential references an unknown profile');
    if (typeof variable !== 'string' || !/^[A-Z][A-Z0-9_]*_API_KEY$/.test(variable)) throw new Error('shared credentials must reference an explicit *_API_KEY variable');
    if (!values[variable]?.trim()) throw new Error(`shared credential variable ${variable} is missing`);
    credentials.push([name, { apiKey: values[variable] }]);
  }
  return { profiles, credentials: Object.fromEntries(credentials) };
}

// Profile synchronization is account-scoped through explicit Control Plane UUIDs.
export function profileAccountIDs(values) {
  const raw = values.HARDEN_LLM_PROFILE_ACCOUNT_IDS?.trim();
  if (!raw) throw new Error('HARDEN_LLM_PROFILE_ACCOUNT_IDS must name the Control Plane accounts to provision');
  const ids = raw.split(',').map(value => value.trim());
  if (ids.some(value => !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value)) || new Set(ids).size !== ids.length) {
    throw new Error('HARDEN_LLM_PROFILE_ACCOUNT_IDS must contain unique Control Plane account UUIDs');
  }
  return ids;
}

// Explicit application settings only. Never propagate deployment identities,
// databases, encryption keys, artifact credentials, or bearer/session secrets.
export function sharedApplicationVariables(values) {
  return Object.fromEntries([
    'HARDEN_LLM_MAX_RUN_DURATION_MS', 'HARDEN_LLM_PROVIDER_ALLOWED_HOSTS',
    'HARDEN_LLM_PROVIDER_PRIVATE_ALLOWLIST',
    'HARDEN_LLM_ARTIFACT_PRESIGN_TTL',
    'HARDEN_LLM_CONTROL_PLANE_URL', 'HARDEN_LLM_CONTROL_PLANE_INTERNAL_TOKEN',
    'JINA_API_KEY',
    'HARDEN_LLM_WEB_API_TIMEOUT_MS', 'HARDEN_LLM_WEB_RUN_TIMEOUT_MS',
    'HARDEN_LLM_WEB_LOG_MAX_BYTES', 'HARDEN_LLM_WEB_LOG_MAX_FILES',
  ].filter(key => values[key] !== undefined).map(key => [key, values[key]]));
}

export function syncSharedProfiles(container, image, values, run = command) {
  const config = sharedProfiles(values);
  const accounts = profileAccountIDs(values);
  const [info] = JSON.parse(run('docker', ['inspect', container]));
  const project = info.Config.Labels?.['com.docker.compose.project'];
  const service = info.Config.Labels?.['com.docker.compose.service'];
  if (!((project === 'harden-llm' && service === 'harden-llm-gateway') || (project?.startsWith('hllm-preview-') && service === 'gateway'))) throw new Error('Not a harden-llm gateway');
  const environment = Object.fromEntries(info.Config.Env.map(v => { const i = v.indexOf('='); return [v.slice(0,i),v.slice(i+1)]; }));
  const keys = ['HARDEN_LLM_DATABASE_URL', 'HARDEN_LLM_ENCRYPTION_KEYS', 'HARDEN_LLM_ACTIVE_ENCRYPTION_KEY_ID'];
  if (keys.some(k => !environment[k])) throw new Error('Gateway provisioning environment is incomplete');
  const env = { ...process.env, ...Object.fromEntries(keys.map(k => [k, environment[k]])) };
  let changed = false;
  for (const accountID of accounts) {
    const output = run('docker', ['run', '--rm', '-i', '--read-only', '--cap-drop=ALL', '--security-opt=no-new-privileges', '--network', `container:${info.Id}`, ...keys.flatMap(k => ['--env', k]), image, 'sync-profiles', '--account-id', accountID], { env, input: JSON.stringify(config) });
    try { changed ||= Boolean(JSON.parse(output).changed); } catch { throw new Error('Shared profile synchronization returned invalid status'); }
  }
  return { accounts: accounts.length, profiles: Object.keys(config.profiles).length, configured: Object.keys(config.credentials).length, changed };
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  try {
    const [envFile, container, image] = process.argv.slice(2);
    if (!envFile || !container || !/^sha256:[a-f0-9]{64}$/.test(image ?? '') || process.argv.length !== 5) throw new Error('Usage: node scripts/shared-profiles.mjs ENV_FILE GATEWAY_CONTAINER ADMIN_IMAGE_ID');
    console.log(JSON.stringify(syncSharedProfiles(container, image, parseEnv(await readFile(envFile, 'utf8')))));
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
