// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-408
import path from "node:path";

const APPLICATION_ENVIRONMENT_KEYS = Object.freeze([
  "HARDEN_LLM_CONFIG_FILE",
  "HARDEN_LLM_TOKEN",
  "CPA_API_KEY",
  "HARDEN_LLM_MAX_RUN_DURATION_MS",
  "HARDEN_LLM_PROVIDER_ALLOWED_HOSTS",
  "HARDEN_LLM_PROVIDER_PRIVATE_ALLOWLIST",
  "HARDEN_LLM_CONTROL_PLANE_URL",
  "HARDEN_LLM_CONTROL_PLANE_INTERNAL_TOKEN",
  "PRLS_PORTAL_URL",
  "JINA_API_KEY",
  "HARDEN_LLM_WEB_API_TIMEOUT_MS",
  "HARDEN_LLM_WEB_LOG_MAX_BYTES",
  "HARDEN_LLM_WEB_LOG_MAX_FILES",
]);

const REQUIRED_APPLICATION_KEYS = Object.freeze([
  "HARDEN_LLM_CONFIG_FILE",
  "HARDEN_LLM_TOKEN",
  "CPA_API_KEY",
  "HARDEN_LLM_CONTROL_PLANE_URL",
  "HARDEN_LLM_CONTROL_PLANE_INTERNAL_TOKEN",
  "PRLS_PORTAL_URL",
]);

export function applicationEnvironment(values) {
  for (const key of REQUIRED_APPLICATION_KEYS) {
    if (typeof values[key] !== "string" || values[key].trim() === "") {
      throw new Error(`${key} is required in the shared application environment`);
    }
  }

  if (!path.isAbsolute(values.HARDEN_LLM_CONFIG_FILE)) {
    throw new Error("HARDEN_LLM_CONFIG_FILE must be an absolute path");
  }

  return Object.fromEntries(
    APPLICATION_ENVIRONMENT_KEYS
      .filter((key) => values[key] !== undefined)
      .map((key) => [key, values[key]]),
  );
}
