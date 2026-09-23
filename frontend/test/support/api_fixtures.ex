defmodule HardenLlmWeb.APIFixtures do
  @moduledoc false

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-044 WEB-TEST-045 TEST-044 TEST-045

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

  def history_page(items, page \\ 1, page_size \\ 10, total_count \\ nil) do
    total_count = if is_nil(total_count), do: length(items), else: total_count

    success(%{
      "items" => items,
      "pagination" => %{"page" => page, "pageSize" => page_size, "totalCount" => total_count}
    })
  end

  def history_items(count, prefix \\ "run")
  def history_items(0, _prefix), do: []

  def history_items(count, prefix) when is_integer(count) and count > 0 do
    Enum.map(1..count, fn number ->
      history_item("#{prefix}-#{number}", "#{prefix}-trace-#{number}")
    end)
  end

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

  def recovery_policy do
    %{
      "maxAttempts" => 4,
      "retryOn" => ["network", "rate_limit", "server_error", "empty_response", "provider_retry"],
      "repairInvalidOutput" => true,
      "backoff" => %{"baseDelayMs" => 500, "maxDelayMs" => 8000}
    }
  end

  def profiles(profiles),
    do: success(%{"profiles" => profiles, "defaults" => %{"recoveryPolicy" => recovery_policy()}})

  def state do
    %{
      "schemaVersion" => 2,
      "selectedProfileId" => "Primary",
      "modelId" => "model-test",
      "userPrompt" => "safe fixture prompt",
      "callType" => "text",
      "recoveryPolicy" => Map.put(recovery_policy(), "repairInvalidOutput", false),
      "cacheMode" => "cache"
    }
  end

  def profile_state do
    %{
      "profile" => %{
        "schemaVersion" => 2,
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
        "recoveryPolicy" => recovery_policy(),
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

  def profile(profile_id, model_id) do
    profile_state()
    |> put_in(["profile", "llmProfile"], profile_id)
    |> put_in(["profile", "modelId"], model_id)
    |> put_in(["profile", "models"], [%{"id" => model_id, "label" => model_id}])
    |> put_in(
      ["profile", "defaultOptions"],
      %{
        "max_tokens" => 16_000
      }
    )
    |> put_in(["credential", "credentialId"], "credential-#{profile_id}")
  end

  def history_item(run_id \\ "run-test", trace_id \\ "trace-test", profile_id \\ "Primary") do
    result =
      run_result()
      |> Map.put("runId", run_id)
      |> Map.put("traceId", trace_id)
      |> put_in(["selectedTarget", "profileId"], profile_id)
      |> put_in(["resultSource", "producer", "profileId"], profile_id)
      |> put_in(["attempts", Access.at(0), "profileId"], profile_id)
      |> put_in(["attempts", Access.at(0), "target", "profileId"], profile_id)

    %{
      "runId" => run_id,
      "profileId" => profile_id,
      "traceId" => trace_id,
      "status" => "succeeded",
      "request" => %{
        "profileId" => "Primary",
        "userPrompt" => "safe restored prompt",
        "callType" => "text",
        "recoveryPolicy" => recovery_policy()
      },
      "result" => result,
      "startedAt" => "2026-07-13T12:00:00Z",
      "completedAt" => "2026-07-13T12:00:01Z"
    }
  end

  def run_result do
    selected_target = %{
      "profileId" => "Primary",
      "provider" => "openai",
      "protocol" => "responses",
      "endpoint" => "https://provider.example.test/v1",
      "modelId" => "model-test"
    }

    producer = Map.put(selected_target, "protocol", "openai.responses")

    usage = %{
      "inputTokens" => 1,
      "cacheReadTokens" => 0,
      "cacheCreationTokens" => 0,
      "outputTokens" => 1,
      "reasoningTokens" => 0,
      "promptTokens" => 1,
      "completionTokens" => 1,
      "totalTokens" => 2,
      "status" => "complete"
    }

    cost = %{
      "knownSubtotalUsd" => 0.001,
      "status" => "exact",
      "source" => "fixture",
      "knownObservations" => 1,
      "unknownObservations" => 0
    }

    %{
      "schemaVersion" => 4,
      "runId" => "run-test",
      "status" => "succeeded",
      "callId" => "call-test",
      "traceId" => "trace-test",
      "output" => "fixture output",
      "selectedTarget" => selected_target,
      "resultSource" => %{"kind" => "provider", "attemptNumber" => 1, "producer" => producer},
      "accounting" => %{
        "result" => %{"usage" => usage, "cost" => cost},
        "provider" => %{"usage" => usage, "cost" => cost}
      },
      "attempts" => [
        %{
          "number" => 1,
          "profileId" => "Primary",
          "target" => producer,
          "category" => "success",
          "httpStatus" => 200,
          "retryable" => false,
          "wait" => 0,
          "duration" => 120_000_000,
          "repair" => false,
          "providerUsed" => true
        }
      ],
      "cache" => %{"mode" => "off", "status" => "disabled", "served" => false, "written" => false},
      "artifacts" => [],
      "providerInvoked" => true,
      "totalCallDurationMs" => 120,
      "totalWaitMs" => 0,
      "totalActualWaitMs" => 0,
      "overBudgetMs" => 0,
      "usedRepair" => false
    }
  end

  def stats do
    %{
      "schemaVersion" => 2,
      "totalCount" => 3,
      "successCount" => 2,
      "failureCount" => 1,
      "timeoutCount" => 0,
      "resultAccounting" => %{
        "usage" => %{
          "promptTokens" => 24,
          "cacheReadTokens" => 8,
          "cacheCreationTokens" => 2,
          "outputTokens" => 12,
          "reasoningTokens" => 4,
          "totalTokens" => 40,
          "coverage" => %{
            "complete" => 2,
            "partial" => 0,
            "unavailable" => 1,
            "inconsistent" => 0
          }
        },
        "cost" => %{
          "knownSubtotalUsd" => 0.00042,
          "coverage" => %{"exact" => 1, "partial" => 1, "unknown" => 1, "unavailable" => 0}
        }
      },
      "providerAccounting" => %{
        "usage" => %{
          "promptTokens" => 16,
          "cacheReadTokens" => 0,
          "cacheCreationTokens" => 0,
          "outputTokens" => 9,
          "reasoningTokens" => 3,
          "totalTokens" => 28,
          "coverage" => %{
            "complete" => 1,
            "partial" => 1,
            "unavailable" => 1,
            "inconsistent" => 0
          }
        },
        "cost" => %{
          "knownSubtotalUsd" => 0.00032,
          "coverage" => %{"exact" => 1, "partial" => 1, "unknown" => 0, "unavailable" => 1}
        }
      },
      "cached" => %{
        "count" => 1,
        "cost" => %{
          "knownSubtotalUsd" => 0.0001,
          "coverage" => %{"exact" => 1, "partial" => 0, "unknown" => 0, "unavailable" => 0}
        }
      },
      "totalCallDurationMs" => 2_580,
      "maxCallDurationMs" => 1_200,
      "overBudgetCount" => 1,
      "maxOverBudgetMs" => 50
    }
  end

  def trace do
    %{
      "traceId" => "trace-test",
      "record" => Map.put(run_result(), "status", "succeeded"),
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
          "state" => "available",
          "sha256" => String.duplicate("a", 64),
          "sizeBytes" => 42,
          "contentType" => "application/json",
          "createdAt" => "2026-07-13T12:00:01Z"
        }
      ],
      "resources" => %{
        "request" => %{
          "available" => true,
          "payload" => %{
            "profileId" => "Primary",
            "userPrompt" => "safe restored prompt",
            "callType" => "text",
            "recoveryPolicy" => recovery_policy()
          }
        },
        "response" => %{"available" => true, "payload" => run_result()}
      }
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
