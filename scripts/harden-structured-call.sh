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
email="$(dotenv_value HARDEN_LLM_LIVE_USER_EMAIL)"
password="$(dotenv_value HARDEN_LLM_LIVE_USER_PASSWORD)"

if [[ -z "$static_token" && ( -z "$email" || -z "$password" ) ]]; then
  printf 'HARDEN_LLM_LIVE_USER_EMAIL and HARDEN_LLM_LIVE_USER_PASSWORD are required in %s\n' "$env_file" >&2
  exit 1
fi

token="$static_token"
login_response=""
run_response=""
cleanup() {
  if [[ -n "$token" && -z "$static_token" ]]; then
    curl --fail --silent --show-error \
      -X POST \
      -H "Authorization: Bearer $token" \
      "$api/api/v1/auth/logout" >/dev/null || true
  fi
  [[ -z "$login_response" ]] || rm -f "$login_response"
  [[ -z "$run_response" ]] || rm -f "$run_response"
  unset email password static_token token login_body request_body login_response run_response
}
trap cleanup EXIT

if [[ -z "$token" ]]; then
  login_body="$(jq -cn \
    --arg email "$email" \
    --arg password "$password" \
    '{email:$email,password:$password}')"

  login_response="$(mktemp)"
  if ! curl --retry 3 --retry-delay 1 --fail --silent --show-error \
    -H 'Content-Type: application/json' \
    --data-binary "$login_body" \
    --output "$login_response" \
    "$api/api/v1/auth/login"; then
    printf 'Login failed; response:\n' >&2
    sed -n '1,80p' "$login_response" >&2
    exit 1
  fi
  token="$(jq -er '.result.accessToken' "$login_response")"
fi

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
  -H "Authorization: Bearer $token" \
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
