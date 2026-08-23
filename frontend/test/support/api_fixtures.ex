defmodule HardenLlmWeb.APIFixtures do
  @moduledoc false

  @token String.duplicate("t", 43)
  @expiry "2099-07-13T12:00:00Z"

  def token, do: @token
  def expiry, do: @expiry

  def principal do
    %{
      "ownerId" => "owner-test",
      "email" => "operator@example.test",
      "sessionId" => "session-test",
      "expiresAt" => @expiry
    }
  end

  def success(result, state \\ %{}), do: %{"state" => state, "result" => result, "error" => nil}

  def error(status, code \\ "request_failed", field_errors \\ %{}) do
    {status,
     %{
       "state" => %{},
       "result" => nil,
       "error" => %{
         "code" => code,
         "message" => "sensitive backend detail",
         "fieldErrors" => field_errors
       }
     }}
  end

  def login_result do
    %{"accessToken" => @token, "expiresAt" => @expiry, "principal" => principal()}
  end

  def state do
    %{
      "schemaVersion" => 1,
      "selectedProfileId" => "Primary",
      "modelId" => "model-test",
      "userPrompt" => "safe fixture prompt",
      "callType" => "text",
      "structuredRepair" => false,
      "cacheMode" => "cache"
    }
  end

  def profile_state do
    %{
      "profile" => %{
        "schemaVersion" => 1,
        "llmProfile" => "Primary",
        "provider" => "openai",
        "apiInferenceType" => "responses",
        "endpointCredentialScope" => "user",
        "baseUrl" => "https://provider.example.test/v1",
        "modelId" => "model-test",
        "pricing" => nil,
        "supportsTemperature" => true,
        "supportsContractedStructuredOutput" => true,
        "tokensParam" => nil,
        "responsesTokensParam" => "max_output_tokens",
        "defaultOptions" => %{},
        "reasoningEffortMap" => %{
          "lowest" => %{"reasoning" => %{"effort" => "low"}},
          "middle" => %{"reasoning" => %{"effort" => "medium"}},
          "highest" => %{"reasoning" => %{"effort" => "high"}}
        },
        "backupProfiles" => [],
        "models" => [%{"id" => "model-test", "label" => "Model Test"}]
      },
      "credential" => %{
        "schemaVersion" => 1,
        "credentialId" => "credential-test",
        "scope" => "user",
        "origin" => "https://provider.example.test",
        "apiInferenceTypes" => ["responses"],
        "configured" => true,
        "createdAt" => "2026-07-13T12:00:00Z"
      }
    }
  end

  def history_item do
    %{
      "runId" => "run-test",
      "profileId" => "Primary",
      "traceId" => "trace-test",
      "status" => "succeeded",
      "request" => %{
        "profileId" => "Primary",
        "userPrompt" => "safe restored prompt",
        "callType" => "text"
      },
      "result" => %{"output" => "ok"},
      "startedAt" => "2026-07-13T12:00:00Z",
      "completedAt" => "2026-07-13T12:00:01Z"
    }
  end

  def run_result do
    %{
      "runId" => "run-test",
      "callId" => "call-test",
      "traceId" => "trace-test",
      "output" => "fixture output",
      "usage" => %{
        "inputTokens" => 1,
        "cacheReadTokens" => 0,
        "cacheCreationTokens" => 0,
        "outputTokens" => 1,
        "reasoningTokens" => 0,
        "totalTokens" => 2
      },
      "cost" => %{"totalUsd" => 0.001, "known" => true, "source" => "fixture"},
      "attempts" => [],
      "cache" => %{"mode" => "off", "status" => "disabled", "served" => false, "written" => false},
      "artifacts" => []
    }
  end

  def trace do
    %{
      "traceId" => "trace-test",
      "record" => %{"status" => "succeeded"},
      "observations" => [
        %{
          "sequence" => 0,
          "type" => "result",
          "data" => %{"outcome" => "success"},
          "createdAt" => "2026-07-13T12:00:01Z"
        }
      ],
      "artifacts" => [
        %{
          "artifactId" => "artifact-test",
          "kind" => "trace",
          "sha256" => String.duplicate("a", 64),
          "sizeBytes" => 42,
          "contentType" => "application/json",
          "createdAt" => "2026-07-13T12:00:01Z"
        }
      ]
    }
  end

  def insert_session do
    {:ok, handle} = HardenLlmWeb.SessionVault.insert(@token, @expiry)
    handle
  end

  def session_map(handle) do
    %{
      "session_handle" => handle,
      "session_expiry" => @expiry,
      "identity" => %{"email" => "operator@example.test", "ownerId" => "owner-test"}
    }
  end
end
