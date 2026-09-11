// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-062
// The shared .env is trusted host configuration, never branch-supplied input.
import { parseEnv, isDeepStrictEqual } from 'node:util';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { command } from './host-command.mjs';

export function sharedProfiles(values) {
  let profiles, references;
  try {
    profiles = JSON.parse(values.HARDEN_LLM_SHARED_PROFILES);
    references = JSON.parse(values.HARDEN_LLM_SHARED_CREDENTIALS);
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

// Explicit application settings only. Never propagate deployment identities,
// databases, encryption keys, artifact credentials, or bearer/session secrets.
export function sharedApplicationVariables(values) {
  return Object.fromEntries([
    'HARDEN_LLM_MAX_RUN_DURATION_MS', 'HARDEN_LLM_PROVIDER_ALLOWED_HOSTS',
    'HARDEN_LLM_PROVIDER_PRIVATE_ALLOWLIST',
    'HARDEN_LLM_ARTIFACT_PRESIGN_TTL', 'HARDEN_LLM_SESSION_TTL',
    'HARDEN_LLM_WEB_API_TIMEOUT_MS', 'HARDEN_LLM_WEB_RUN_TIMEOUT_MS',
    'HARDEN_LLM_WEB_LOG_MAX_BYTES', 'HARDEN_LLM_WEB_LOG_MAX_FILES',
  ].filter(key => values[key] !== undefined).map(key => [key, values[key]]));
}

export async function verifySharedProfiles(url, values, request = fetch) {
  const expected = sharedProfiles(values);
  const accounts = [[values.TEST_LOGIN, values.TEST_PASSWORD], [values.HARDEN_LLM_LOCAL_OPERATOR_EMAIL, values.HARDEN_LLM_LOCAL_OPERATOR_PASSWORD]];
  for (const [email,password] of accounts) {
    const login = await request(url + '/api/v1/auth/login', { method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({email,password}), signal:AbortSignal.timeout(15000) });
    const token = (await login.json()).result?.accessToken;
    if (login.status !== 200 || !token) throw new Error('Shared profile verification login failed');
    const headers = {Authorization:`Bearer ${token}`};
    try {
      const response = await request(url + '/api/v1/profiles', {headers,signal:AbortSignal.timeout(15000)});
      const body = await response.json();
      if (response.status !== 200 || !Array.isArray(body.result?.profiles)) throw new Error('Shared profile readback failed');
      const actual = new Map(body.result.profiles.map(p => [p.profile.llmProfile,p]));
      for (const [name,profile] of Object.entries(expected.profiles)) {
        const stored=actual.get(name);
        if (!stored || !isDeepStrictEqual(stored.profile,profile) || stored.credential.configured !== Object.hasOwn(expected.credentials,name)) throw new Error('Shared profile/model/credential readback mismatch');
      }
    } finally {
      const logout = await request(url + '/api/v1/auth/logout', {method:'POST',headers,signal:AbortSignal.timeout(15000)});
      await logout.body?.cancel();
      if (logout.status !== 200) throw new Error('Shared verification session logout failed');
    }
  }
  return {accounts:accounts.length, profiles:Object.keys(expected.profiles).length, configured:Object.keys(expected.credentials).length};
}

export function syncSharedProfiles(container, image, values, run = command) {
  const config = sharedProfiles(values);
  const emails = [...new Set([values.TEST_LOGIN, values.HARDEN_LLM_LOCAL_OPERATOR_EMAIL].map(v => v?.trim().toLowerCase()))];
  if (emails.some(v => !v)) throw new Error('Shared guest and operator emails are required');
  const [info] = JSON.parse(run('docker', ['inspect', container]));
  const project = info.Config.Labels?.['com.docker.compose.project'];
  const service = info.Config.Labels?.['com.docker.compose.service'];
  if (!((project === 'harden-llm' && service === 'harden-llm-gateway') || (project?.startsWith('hllm-preview-') && service === 'gateway'))) throw new Error('Not a harden-llm gateway');
  const environment = Object.fromEntries(info.Config.Env.map(v => { const i = v.indexOf('='); return [v.slice(0,i),v.slice(i+1)]; }));
  const keys = ['HARDEN_LLM_DATABASE_URL', 'HARDEN_LLM_ENCRYPTION_KEYS', 'HARDEN_LLM_ACTIVE_ENCRYPTION_KEY_ID'];
  if (keys.some(k => !environment[k])) throw new Error('Gateway provisioning environment is incomplete');
  const env = { ...process.env, ...Object.fromEntries(keys.map(k => [k, environment[k]])) };
  let changed = false;
  for (const email of emails) {
    const output = run('docker', ['run', '--rm', '-i', '--read-only', '--cap-drop=ALL', '--security-opt=no-new-privileges', '--network', `container:${info.Id}`, ...keys.flatMap(k => ['--env', k]), image, 'sync-profiles', '--email', email], { env, input: JSON.stringify(config) });
    try { changed ||= Boolean(JSON.parse(output).changed); } catch { throw new Error('Shared profile synchronization returned invalid status'); }
  }
  return { accounts: emails.length, profiles: Object.keys(config.profiles).length, configured: Object.keys(config.credentials).length, changed };
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  try {
    const [envFile, container, image] = process.argv.slice(2);
    if (!envFile || !container || !/^sha256:[a-f0-9]{64}$/.test(image ?? '') || process.argv.length !== 5) throw new Error('Usage: node scripts/shared-profiles.mjs ENV_FILE GATEWAY_CONTAINER ADMIN_IMAGE_ID');
    console.log(JSON.stringify(syncSharedProfiles(container, image, parseEnv(await readFile(envFile, 'utf8')))));
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
