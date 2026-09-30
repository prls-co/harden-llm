#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
env_file="${HARDEN_ENV_FILE:-$repo_root/.env}"
api="${HARDEN_API_URL:-https://harden-llm-api.prls.co}"

if [[ ! -r "$env_file" ]]; then
  printf 'Missing readable environment file: %s\n' "$env_file" >&2
  exit 1
fi

dotenv_value() {
  local key="$1"
  awk -v key="$key" 'index($0, key "=") == 1 {
    print substr($0, length(key) + 2)
    exit
  }' "$env_file"
}

static_token="$(dotenv_value HARDEN_LLM_STATIC_TOKEN)"
account_id="$(dotenv_value HARDEN_LLM_STATIC_TOKEN_ACCOUNT_ID)"

if [[ -z "$static_token" || ! "$account_id" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$ ]]; then
  printf 'HARDEN_LLM_STATIC_TOKEN and HARDEN_LLM_STATIC_TOKEN_ACCOUNT_ID are required in %s\n' "$env_file" >&2
  exit 1
fi

run_response=""
cleanup() {
  [[ -z "$run_response" ]] || rm -f "$run_response"
  unset static_token account_id request_body run_response
}
trap cleanup EXIT

request_body="$(jq -cn '
  {
    profileId: "CurlStructured",
    userPrompt: "Tell me a joke about yourself.",
    callType: "structured",
    schema: {
      type: "object",
      required: ["setup", "punchline"],
      properties: {
        setup: {type: "string"},
        punchline: {type: "string"}
      },
      additionalProperties: false
    }
  }
')"

run_response="$(mktemp)"
if curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer $static_token" \
  -H 'Content-Type: application/json' \
  --data-binary "$request_body" \
  --output "$run_response" \
  "$api/api/v1/run"; then
  jq . "$run_response"
else
  status="$?"
  printf 'Run failed (curl exit %s); response:\n' "$status" >&2
  sed -n '1,80p' "$run_response" >&2
  exit "$status"
fi
