defmodule HardenLlm.ReferenceTest do
  use ExUnit.Case, async: true

  alias HardenLlm.Reference
  alias HardenLlm.Reference.Call
  alias HardenLlmWeb.WorkspaceRequest

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-115
  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-406

  test "one workspace request maps directly to stateless OpenAI Responses JSON" do
    assert {:ok, payload} =
             WorkspaceRequest.build(%{
               "model" => "native-model-id",
               "userPrompt" => "Write one line.",
               "systemPrompt" => "Be concise.",
               "structured" => "false",
               "reasoningEffort" => "medium"
             })

    assert payload["model"] == "native-model-id"
    assert payload["input"] == "Write one line."
    assert payload["instructions"] == "Be concise."
    assert payload["reasoning"] == %{"effort" => "medium"}
    assert payload["store"] == false
    assert payload["harden"] == %{"cache" => "off", "diagnostics" => true}
    refute Map.has_key?(payload, "profileId")
    refute Map.has_key?(payload, "session_ref")
    refute Map.has_key?(payload, "authorization")
  end

  test "structured output and explicit recovery policy use documented request fields" do
    schema = %{
      "type" => "object",
      "properties" => %{"answer" => %{"type" => "string"}},
      "required" => ["answer"],
      "additionalProperties" => false
    }

    recovery = %{
      "maxAttempts" => 2,
      "retryOn" => ["network"],
      "backoff" => %{"baseDelayMs" => 100, "maxDelayMs" => 200},
      "jsonRepair" => nil,
      "rerun" => nil
    }

    assert {:ok, payload} =
             WorkspaceRequest.build(%{
               "model" => "native-model-id",
               "userPrompt" => "Return an object.",
               "structured" => "true",
               "schema" => Jason.encode!(schema),
               "recoveryJson" => Jason.encode!(recovery)
             })

    assert payload["text"]["format"] == %{
             "type" => "json_schema",
             "name" => "structured_response",
             "schema" => schema,
             "strict" => true
           }

    assert payload["harden"]["recovery"] == recovery
  end

  test "request validation rejects missing models, blank prompts, and invalid structured schemas" do
    assert {:error, "Choose a model or enter its ID."} =
             WorkspaceRequest.build(%{"model" => "", "userPrompt" => "question"})

    assert {:error, "Enter a prompt before submitting."} =
             WorkspaceRequest.build(%{"model" => "model", "userPrompt" => "  "})

    assert {:error, _} =
             WorkspaceRequest.build(%{
               "model" => "model",
               "userPrompt" => "question",
               "structured" => "true",
               "schema" => "{bad json"
             })
  end

  test "restoring a saved request fills the form without executing it" do
    form =
      WorkspaceRequest.restore(%{
        "request" => %{
          "model" => "model-test",
          "input" => "original prompt",
          "instructions" => "original instructions",
          "text" => %{
            "format" => %{
              "type" => "json_schema",
              "schema" => %{"type" => "object"}
            }
          }
        }
      })

    assert form["model"] == "model-test"
    assert form["userPrompt"] == "original prompt"
    assert form["systemPrompt"] == "original instructions"
    assert form["structured"] == "true"
    assert form["schema"] == ~s({\n  "type": "object"\n})
  end

  test "history is global to the reference app and the namespace is fixed" do
    assert Reference.namespace() == "workspace"
    assert {:error, :unavailable} = Reference.list_calls(1, 10)
    assert {:error, :unavailable} = Reference.statistics()
  end

  test "draft size and identity are checked before storage is attempted" do
    assert {:error, :invalid_user} = Reference.get_draft("")
    assert {:error, :invalid_draft} = Reference.save_draft("user", 0, %{})

    too_large = %{"value" => String.duplicate("x", 256 * 1024)}
    assert {:error, :too_large} = Reference.save_draft("user", 1, too_large)
  end

  test "download derives one canonical record and preserves the outcome category" do
    call = %Call{
      id: "8b417351-53f2-4ec9-b2d5-8d846790de61",
      inserted_at: ~U[2026-10-06 12:00:00.000000Z],
      endpoint: "/v1/responses",
      request: %{"model" => "model-test", "input" => "hello"},
      outcome: %{"error" => %{"message" => "safe error"}},
      outcome_kind: "unknown"
    }

    assert Reference.download(call) == %{
             "id" => call.id,
             "createdAt" => "2026-10-06T12:00:00.000000Z",
             "endpoint" => "/v1/responses",
             "request" => call.request,
             "outcome" => call.outcome,
             "outcomeKind" => "unknown"
           }
  end
end
