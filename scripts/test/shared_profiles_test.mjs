// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-062
import test from 'node:test';
import assert from 'node:assert/strict';
import { sharedProfiles, profileUserIDs, sharedApplicationVariables, syncSharedProfiles } from '../shared-profiles.mjs';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';

const example = JSON.parse(readFileSync(new URL('../../config/llm-profiles.example.json', import.meta.url), 'utf8')).profiles.Example;

function configFile(t, profiles, credentialEnv = {Example:'EXAMPLE_API_KEY'}) {
  const dir = mkdtempSync(path.join(tmpdir(),'hllm-config-test-'));
  t.after(()=>rmSync(dir,{recursive:true}));
  const file=path.join(dir,'profiles.json');
  writeFileSync(file, JSON.stringify({profiles,credentialEnv}));
  return file;
}

test('shared configuration resolves only explicitly bound env keys, without interpolation', t => {
  const catalog = { Example: example };
  const values = {
    HARDEN_LLM_CONFIG_FILE: configFile(t,catalog),
    EXAMPLE_API_KEY: 'fixture$not-expanded',
    HARDEN_LLM_WEB_SECRET_KEY_BASE: 'never-shared',
  };
  assert.deepEqual(sharedProfiles(values), { profiles: catalog, credentials: { Example: { apiKey: 'fixture$not-expanded' } } });
  assert.throws(() => sharedProfiles({ ...values, EXAMPLE_API_KEY: '' }), /missing/);
  assert.throws(() => sharedProfiles({ ...values, HARDEN_LLM_CONFIG_FILE: configFile(t,catalog,{Unknown:'EXAMPLE_API_KEY'}) }), /unknown profile/);
  assert.throws(() => sharedProfiles({ ...values, HARDEN_LLM_CONFIG_FILE: configFile(t,catalog,{Example:'HARDEN_LLM_WEB_SECRET_KEY_BASE'}) }), /API_KEY/);
  assert.throws(() => sharedProfiles({ ...values, HARDEN_LLM_CONFIG_FILE: 'secret malformed value' }), /invalid shared/);
  assert.throws(() => sharedProfiles({ ...values, HARDEN_LLM_CONFIG_FILE: configFile(t,catalog,42) }), /invalid shared/);
});

test('only portable application variables are shared', () => {
  assert.deepEqual(sharedApplicationVariables({ HARDEN_LLM_MAX_RUN_DURATION_MS:'45000', HARDEN_LLM_PROVIDER_ALLOWED_HOSTS:'example.test', HARDEN_LLM_CONTROL_PLANE_URL:'http://control-plane:4310', HARDEN_LLM_CONTROL_PLANE_INTERNAL_TOKEN:'fixture-internal-token', PRLS_PORTAL_URL:'https://a.prls.co', JINA_API_KEY:'fixture-jina-key', HARDEN_LLM_DATABASE_URL:'not-shared', HARDEN_LLM_STATIC_TOKEN:'not-shared', HARDEN_LLM_WEB_SECRET_KEY_BASE:'not-shared' }), { HARDEN_LLM_MAX_RUN_DURATION_MS:'45000', HARDEN_LLM_PROVIDER_ALLOWED_HOSTS:'example.test', HARDEN_LLM_CONTROL_PLANE_URL:'http://control-plane:4310', HARDEN_LLM_CONTROL_PLANE_INTERNAL_TOKEN:'fixture-internal-token', PRLS_PORTAL_URL:'https://a.prls.co', JINA_API_KEY:'fixture-jina-key' });
});

test('profile provisioning uses only explicit Control Plane user IDs', () => {
  const first='User_Alpha';
  const second='User_Beta';
  assert.deepEqual(profileUserIDs({HARDEN_LLM_PROFILE_USER_IDS:`${first}, ${second}`}),[first,second]);
  for(const value of [undefined,'', 'user\nunsafe', 'x'.repeat(129), `${first},${first}`, `${first},`, `${first},,${second}`]) {
    assert.throws(()=>profileUserIDs({HARDEN_LLM_PROFILE_USER_IDS:value}),/HARDEN_LLM_PROFILE_USER_IDS/);
  }
});

test('sync uses local encryption and DB, stdin keys, explicit users and no provider call', t => {
  const first='User_Alpha';
  const second='User_Beta';
  const values = { HARDEN_LLM_CONFIG_FILE:configFile(t,{Example:example}), EXAMPLE_API_KEY:'fixture-private', HARDEN_LLM_PROFILE_USER_IDS:`${first},${second}` };
  const calls=[];
  const run=(bin,args,options)=>{calls.push({bin,args,options});return args[0]==='inspect'?JSON.stringify([{Id:'target-id',Config:{Labels:{'com.docker.compose.project':'hllm-preview-dev','com.docker.compose.service':'gateway'},Env:['HARDEN_LLM_DATABASE_URL=local-db','HARDEN_LLM_ENCRYPTION_KEYS=local-keys','HARDEN_LLM_ACTIVE_ENCRYPTION_KEY_ID=local','UNRELATED_SECRET=excluded']}}]):'{"changed":false}';};
  assert.deepEqual(syncSharedProfiles('target','image',values,run),{users:2,profiles:1,configured:1,changed:false});
  assert.equal(calls.length,3);
  for(const [index,call] of calls.slice(1).entries()){
    assert(call.args.includes('container:target-id'));
    assert(call.args.includes('sync-profiles'));
    assert(call.args.includes(index===0?first:second));
    assert(!call.args.join(' ').includes('fixture-private'));
    assert.equal(JSON.parse(call.options.input).credentials.Example.apiKey,'fixture-private');
    assert.equal(call.options.env.HARDEN_LLM_ENCRYPTION_KEYS,'local-keys');
    assert.equal(call.options.env.UNRELATED_SECRET,undefined);
  }
  assert.throws(()=>syncSharedProfiles('wrong','image',values,()=>JSON.stringify([{Config:{Labels:{}}}])),/Not a harden-llm gateway/);
});
