defmodule HardenLlmUI.ClientContractTest do
  use ExUnit.Case, async: true

  alias HardenLlmUI.Client

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-002

  test "reference client methods map to the standard OpenAI routes in OpenAPI" do
    source = File.read!(Path.expand("../../../api/openapi.yaml", __DIR__))

    assert source =~ ~r{^  /v1/models:\n    get:.*?operationId: listModels}ms
    assert source =~ ~r{^  /v1/responses:\n    post:.*?operationId: createResponse}ms
    Code.ensure_loaded!(Client)
    assert function_exported?(Client, :models, 1)
    assert function_exported?(Client, :responses, 2)
    assert function_exported?(Client, :list_history, 1)
    assert function_exported?(Client, :list_profiles, 1)
  end

  test "reference client has one bearer configuration and no human-session forwarding" do
    source =
      File.read!(Path.expand("../../packages/harden_llm_ui/lib/harden_llm_ui/client.ex", __DIR__))

    assert source =~ "context.api_key"
    refute source =~ "x-prls-session-reference"
    assert source =~ "/api/v1/"
    refute source =~ "session_ref"
  end
end
