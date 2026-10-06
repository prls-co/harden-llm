// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-408
import test from "node:test";
import assert from "node:assert/strict";
import { applicationEnvironment } from "../deployment-configuration.mjs";

const fixture = {
  HARDEN_LLM_CONFIG_FILE: "/fixture/upstreams.json",
  HARDEN_LLM_TOKEN: "synthetic-incoming-token-not-a-secret",
  CPA_API_KEY: "synthetic-upstream-token-not-a-secret",
  HARDEN_LLM_CONTROL_PLANE_URL: "http://control-plane.test:4310",
  HARDEN_LLM_CONTROL_PLANE_INTERNAL_TOKEN: "synthetic-identity-token",
  PRLS_PORTAL_URL: "https://portal.test",
  HARDEN_LLM_STATIC_TOKEN: "retired-key-must-not-be-propagated",
  HARDEN_LLM_PROFILE_USER_IDS: "retired-users-must-not-be-propagated",
  UNRELATED_SECRET: "must-not-be-propagated",
};

test("application environment contains only the one connection and incoming bearer path", () => {
  assert.deepEqual(applicationEnvironment(fixture), {
    HARDEN_LLM_CONFIG_FILE: "/fixture/upstreams.json",
    HARDEN_LLM_TOKEN: "synthetic-incoming-token-not-a-secret",
    CPA_API_KEY: "synthetic-upstream-token-not-a-secret",
    HARDEN_LLM_CONTROL_PLANE_URL: "http://control-plane.test:4310",
    HARDEN_LLM_CONTROL_PLANE_INTERNAL_TOKEN: "synthetic-identity-token",
    PRLS_PORTAL_URL: "https://portal.test",
  });
});

test("required connection and bearer inputs fail explicitly", () => {
  for (const key of [
    "HARDEN_LLM_CONFIG_FILE",
    "HARDEN_LLM_TOKEN",
    "CPA_API_KEY",
    "HARDEN_LLM_CONTROL_PLANE_URL",
    "HARDEN_LLM_CONTROL_PLANE_INTERNAL_TOKEN",
    "PRLS_PORTAL_URL",
  ]) {
    const values = { ...fixture };
    delete values[key];
    assert.throws(() => applicationEnvironment(values), new RegExp(`${key} is required`));
  }

  assert.throws(
    () => applicationEnvironment({ ...fixture, HARDEN_LLM_CONFIG_FILE: "relative/upstreams.json" }),
    /HARDEN_LLM_CONFIG_FILE must be an absolute path/,
  );
});
