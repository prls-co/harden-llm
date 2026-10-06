#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
env_file="${HARDEN_ENV_FILE:-$repo_root/.env}"
api="${HARDEN_API_URL:-https://harden-llm-api.prls.co}"
model="${HARDEN_LLM_SMOKE_MODEL:-gpt-5.6-luna}"

if [[ ! -r "$env_file" ]]; then
  printf 'Missing readable environment file: %s\n' "$env_file" >&2
  exit 1
fi

api_token="$(awk -v key='HARDEN_LLM_TOKEN' 'index($0, key "=") == 1 {
  print substr($0, length(key) + 2)
  exit
}' "$env_file")"

if [[ -z "$api_token" ]]; then
  printf 'HARDEN_LLM_TOKEN is required in %s\n' "$env_file" >&2
  exit 1
fi

response_file="$(mktemp)"
cleanup() {
  rm -f "$response_file"
  unset api_token request_body response_file
}
trap cleanup EXIT

request_body="$(jq -cn --arg model "$model" '
  {
    model: $model,
    input: "Tell me a joke about yourself.",
    store: false,
    text: {
      format: {
        type: "json_schema",
        name: "joke",
        strict: true,
        schema: {
          type: "object",
          required: ["setup", "punchline"],
          properties: {setup: {type: "string"}, punchline: {type: "string"}},
          additionalProperties: false
        }
      }
    }
  }
')"

if curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer $api_token" \
  -H 'Content-Type: application/json' \
  --data-binary "$request_body" \
  --output "$response_file" \
  "$api/v1/responses"; then
  jq . "$response_file"
else
  status="$?"
  printf 'Responses request failed (curl exit %s); response:\n' "$status" >&2
  sed -n '1,80p' "$response_file" >&2
  exit "$status"
fi
