// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-062
import test from 'node:test';
import assert from 'node:assert/strict';
import { sharedProfiles, sharedApplicationVariables, syncSharedProfiles, verifySharedProfiles } from '../shared-profiles.mjs';

test('shared configuration resolves only explicitly bound env keys, without interpolation', () => {
  const catalog = { Example: { llmProfile: 'Example', baseUrl: 'https://example.test/v1' } };
  const values = {
    HARDEN_LLM_SHARED_PROFILES: JSON.stringify(catalog),
    HARDEN_LLM_SHARED_CREDENTIALS: JSON.stringify({ Example: 'EXAMPLE_API_KEY' }),
    EXAMPLE_API_KEY: 'fixture$not-expanded',
    HARDEN_LLM_WEB_SECRET_KEY_BASE: 'never-shared',
  };
  assert.deepEqual(sharedProfiles(values), { profiles: catalog, credentials: { Example: { apiKey: 'fixture$not-expanded' } } });
  assert.throws(() => sharedProfiles({ ...values, EXAMPLE_API_KEY: '' }), /missing/);
  assert.throws(() => sharedProfiles({ ...values, HARDEN_LLM_SHARED_CREDENTIALS: '{"Unknown":"EXAMPLE_API_KEY"}' }), /unknown profile/);
  assert.throws(() => sharedProfiles({ ...values, HARDEN_LLM_SHARED_CREDENTIALS: '{"Example":"HARDEN_LLM_WEB_SECRET_KEY_BASE"}' }), /API_KEY/);
  assert.throws(() => sharedProfiles({ ...values, HARDEN_LLM_SHARED_PROFILES: 'secret malformed value' }), /invalid shared/);
  assert.throws(() => sharedProfiles({ ...values, HARDEN_LLM_SHARED_CREDENTIALS: '42' }), /invalid shared/);
});

test('only portable application variables are shared', () => {
  assert.deepEqual(sharedApplicationVariables({ HARDEN_LLM_MAX_RUN_DURATION_MS:'45000', HARDEN_LLM_PROVIDER_ALLOWED_HOSTS:'example.test', HARDEN_LLM_DATABASE_URL:'not-shared', HARDEN_LLM_STATIC_TOKEN:'not-shared', HARDEN_LLM_WEB_SECRET_KEY_BASE:'not-shared' }), { HARDEN_LLM_MAX_RUN_DURATION_MS:'45000', HARDEN_LLM_PROVIDER_ALLOWED_HOSTS:'example.test' });
});

test('sync uses local encryption and DB, stdin keys, both accounts and no provider call', () => {
  const values = { HARDEN_LLM_SHARED_PROFILES:'{"Example":{"llmProfile":"Example"}}', HARDEN_LLM_SHARED_CREDENTIALS:'{"Example":"EXAMPLE_API_KEY"}', EXAMPLE_API_KEY:'fixture-private', TEST_LOGIN:'guest@example.test', HARDEN_LLM_LOCAL_OPERATOR_EMAIL:'operator@example.test' };
  const calls=[];
  const run=(bin,args,options)=>{calls.push({bin,args,options});return args[0]==='inspect'?JSON.stringify([{Id:'target-id',Config:{Labels:{'com.docker.compose.project':'hllm-preview-dev','com.docker.compose.service':'gateway'},Env:['HARDEN_LLM_DATABASE_URL=local-db','HARDEN_LLM_ENCRYPTION_KEYS=local-keys','HARDEN_LLM_ACTIVE_ENCRYPTION_KEY_ID=local','UNRELATED_SECRET=excluded']}}]):'{"changed":false}';};
  assert.deepEqual(syncSharedProfiles('target','image',values,run),{accounts:2,profiles:1,configured:1,changed:false});
  assert.equal(calls.length,3);
  for(const call of calls.slice(1)){
    assert(call.args.includes('container:target-id'));
    assert(call.args.includes('sync-profiles'));
    assert(!call.args.join(' ').includes('fixture-private'));
    assert.equal(JSON.parse(call.options.input).credentials.Example.apiKey,'fixture-private');
    assert.equal(call.options.env.HARDEN_LLM_ENCRYPTION_KEYS,'local-keys');
    assert.equal(call.options.env.UNRELATED_SECRET,undefined);
  }
  assert.throws(()=>syncSharedProfiles('wrong','image',values,()=>JSON.stringify([{Config:{Labels:{}}}])),/Not a harden-llm gateway/);
});

test('readback verifies model settings and key availability, and logs out on mismatch', async () => {
  const profile={llmProfile:'Example',modelId:'model'};
  const values={HARDEN_LLM_SHARED_PROFILES:JSON.stringify({Example:profile}),HARDEN_LLM_SHARED_CREDENTIALS:'{"Example":"EXAMPLE_API_KEY"}',EXAMPLE_API_KEY:'fixture-only',TEST_LOGIN:'guest',TEST_PASSWORD:'guest-password',HARDEN_LLM_LOCAL_OPERATOR_EMAIL:'operator',HARDEN_LLM_LOCAL_OPERATOR_PASSWORD:'operator-password'};
  for(const mismatch of [false,true]) {
    const calls=[];
    const request=async(url,options)=>{
      calls.push({url,options});
      if(url.endsWith('/login'))return Response.json({result:{accessToken:'fixture-session'}});
      if(url.endsWith('/logout'))return Response.json({});
      return Response.json({result:{profiles:[{profile:{...profile,modelId:mismatch?'wrong':'model'},credential:{configured:true}}]}});
    };
    if(mismatch)await assert.rejects(verifySharedProfiles('https://example.test',values,request),/mismatch/);
    else assert.deepEqual(await verifySharedProfiles('https://example.test',values,request),{accounts:2,profiles:1,configured:1});
    assert(calls.at(-1).url.endsWith('/logout'));
    assert(!calls.some(c=>c.url.endsWith('/run')));
    assert(!JSON.stringify(calls).includes('fixture-only'));
  }
});
