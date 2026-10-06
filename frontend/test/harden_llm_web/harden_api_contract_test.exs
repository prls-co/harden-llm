defmodule HardenLlmWeb.HardenAPIContractTest do
  use ExUnit.Case, async: true

  alias HardenLlmWeb.HardenAPI

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-002

  test "reference client methods map to the standard OpenAI routes in OpenAPI" do
    source = File.read!(Path.expand("../../../api/openapi.yaml", __DIR__))

    assert source =~ ~r{^  /v1/models:\n    get:.*?operationId: listModels}ms
    assert source =~ ~r{^  /v1/responses:\n    post:.*?operationId: createResponse}ms
    assert function_exported?(HardenAPI, :models, 0)
    assert function_exported?(HardenAPI, :responses, 1)
    refute function_exported?(HardenAPI, :list_history, 1)
    refute function_exported?(HardenAPI, :list_profiles, 1)
  end

  test "reference client has one bearer configuration and no human-session forwarding" do
    source = File.read!(Path.expand("../../lib/harden_llm_web/harden_api.ex", __DIR__))

    assert source =~ "HARDEN_LLM_TOKEN"
    refute source =~ "x-prls-session-reference"
    refute source =~ "/api/v1/"
  end
end
