defmodule HardenLlmWeb.APIFixtures do
  @moduledoc false

  def token, do: "harden-llm-test-token-0123456789abcdef"
  def cookie, do: HardenLlmWeb.AccessFixtures.cookie()

  def models do
    %{
      "object" => "list",
      "data" => [
        %{"id" => "model-test", "object" => "model", "created" => 0, "owned_by" => "fixture"}
      ]
    }
  end

  def response(text \\ "fixture response") do
    %{
      "id" => "resp_fixture",
      "object" => "response",
      "created_at" => 0,
      "status" => "completed",
      "model" => "model-test",
      "output" => [
        %{
          "id" => "msg_fixture",
          "type" => "message",
          "status" => "completed",
          "role" => "assistant",
          "content" => [%{"type" => "output_text", "text" => text, "annotations" => []}]
        }
      ],
      "output_text" => text,
      "parallel_tool_calls" => false,
      "usage" => %{
        "input_tokens" => 4,
        "output_tokens" => 3,
        "total_tokens" => 7,
        "input_tokens_details" => %{"cached_tokens" => 0},
        "output_tokens_details" => %{"reasoning_tokens" => 0}
      },
      "harden" => %{"execution_id" => "exec_fixture"}
    }
  end

  def error(code \\ "invalid_request_error") do
    %{
      "error" => %{
        "message" => "sensitive upstream detail",
        "type" => "invalid_request_error",
        "param" => "model",
        "code" => code
      }
    }
  end
end
