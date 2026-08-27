defmodule HardenLlmWeb.ProfileWidgetStateTest do
  use ExUnit.Case, async: true

  alias HardenLlmWeb.ProfileWidgetState

  # PLAN-HLLM-WIDGET-PARITY-001 TEST-105 TEST-110

  test "options patches preserve unknown keys and canonicalize utility aliases" do
    options = %{
      "provider_option" => %{"keep" => true},
      "topP" => 0.4,
      "structuredRepairRetry" => %{
        "enabled" => true,
        "customRepairKey" => "keep",
        "escalation" => %{"customEscalationKey" => "keep", "attempt" => 2}
      },
      "enableRetryOn429" => true,
      "enableRetryOn5xx" => false
    }

    patched =
      ProfileWidgetState.patch_options(options, %{
        "topP" => "",
        "topK" => "40",
        "stopSequences" => "DONE\n\nSTOP",
        "enableRetryOn429" => "true",
        "enableRetryOn5xx" => "false",
        "enableRetryOnNetworkError" => "true",
        "enableRetryOnParseError" => "true",
        "structuredRepairRetryEnabled" => "true",
        "retryMaxAttempts" => "4",
        "retryBaseDelayMs" => "500",
        "retryMaxDelayMs" => "8000",
        "escalationAttempt" => "3",
        "escalationProfile" => "Repair",
        "escalationReasoning" => "highest"
      })

    assert patched["provider_option"] == %{"keep" => true}
    assert patched["top_k"] == 40
    refute Map.has_key?(patched, "topP")
    refute Map.has_key?(patched, "top_p")
    assert patched["stop"] == ["DONE", "STOP"]
    refute Map.has_key?(patched, "enableRetryOn429")
    assert patched["enableRetryOn5xx"] == false
    refute Map.has_key?(patched, "enableRetryOnNetworkError")
    assert patched["structuredRepairRetry"]["customRepairKey"] == "keep"
    assert patched["structuredRepairRetry"]["escalation"]["customEscalationKey"] == "keep"
    assert patched["structuredRepairRetry"]["escalation"]["llmProfile"] == "Repair"
    refute Map.has_key?(patched, "enableRetryOnParseError")
  end

  test "disabling repair preserves unrelated options and explicitly disables repair" do
    options = %{
      "provider_option" => "keep",
      "structuredRepairRetry" => %{
        "enabled" => true,
        "custom" => "keep",
        "escalation" => %{"custom" => "keep"}
      },
      "enableRetryOnParseError" => false
    }

    patched =
      ProfileWidgetState.patch_options(options, %{
        "structuredRepairRetryEnabled" => "false",
        "enableRetryOnParseError" => "true"
      })

    assert patched["provider_option"] == "keep"
    assert patched["structuredRepairRetry"] == false
    refute Map.has_key?(patched, "enableRetryOnParseError")
  end

  test "blank visible retry and escalation fields delete only their canonical keys" do
    options = %{
      "maxAttempts" => 4,
      "baseDelayMs" => 500,
      "maxDelayMs" => 8_000,
      "structuredRepairRetry" => %{
        "enabled" => true,
        "custom" => "keep",
        "escalation" => %{
          "attempt" => 3,
          "llmProfile" => "Repair",
          "reasoningEffort" => "highest"
        }
      },
      "provider_option" => "keep"
    }

    patched =
      ProfileWidgetState.patch_options(options, %{
        "retryMaxAttempts" => "",
        "retryBaseDelayMs" => "",
        "retryMaxDelayMs" => "",
        "structuredRepairRetryEnabled" => "true",
        "escalationAttempt" => "",
        "escalationProfile" => "",
        "escalationReasoning" => ""
      })

    refute Map.has_key?(patched, "maxAttempts")
    refute Map.has_key?(patched, "baseDelayMs")
    refute Map.has_key?(patched, "maxDelayMs")
    assert patched["structuredRepairRetry"]["custom"] == "keep"
    refute Map.has_key?(patched["structuredRepairRetry"]["escalation"], "attempt")
    refute Map.has_key?(patched["structuredRepairRetry"]["escalation"], "llmProfile")
    refute Map.has_key?(patched["structuredRepairRetry"]["escalation"], "reasoningEffort")
    assert patched["provider_option"] == "keep"
  end

  test "model catalog uses host values, default values, and current-value retention" do
    host = [%{"id" => "host-model", "label" => "Host label"}, %{"id" => "duplicate"}]
    duplicate = [%{"id" => "duplicate", "label" => "Profile label"}]

    assert ProfileWidgetState.model_options(host, duplicate, "omitted") == [
             %{"id" => "host-model", "label" => "Host label"},
             %{"id" => "duplicate", "label" => ""},
             %{"id" => "omitted", "label" => ""}
           ]

    assert "gpt-5.6-luna" in Enum.map(
             ProfileWidgetState.model_options(nil, [], "gpt-5.6-luna"),
             & &1["id"]
           )
  end

  test "selects the seeded utility preset when the saved workspace has no selection" do
    profiles = [
      %{"profile" => %{"llmProfile" => "Custom", "modelId" => "custom-model"}},
      %{
        "profile" => %{
          "llmProfile" => "CPA GPT-5.6 Luna",
          "modelId" => "gpt-5.6-luna"
        }
      }
    ]

    assert ProfileWidgetState.resolve_selected_profile_id(profiles, "") ==
             "CPA GPT-5.6 Luna"

    assert ProfileWidgetState.resolve_selected_profile_id(profiles, "Custom") == "Custom"

    assert ProfileWidgetState.resolve_selected_model_id(
             profiles,
             "CPA GPT-5.6 Luna",
             ""
           ) == "gpt-5.6-luna"
  end

  test "selects the seeded CPA Sol profile for escalation when it is available" do
    profiles = [
      %{"profile" => %{"llmProfile" => "Primary", "modelId" => "primary-model"}},
      %{"profile" => %{"llmProfile" => "CPA GPT-5.6 Sol", "modelId" => "gpt-5.6-sol"}}
    ]

    assert ProfileWidgetState.resolve_escalation_profile_id(profiles, "Primary") ==
             "CPA GPT-5.6 Sol"

    assert ProfileWidgetState.resolve_escalation_profile_id(
             [%{"profile" => %{"llmProfile" => "Primary"}}],
             "Primary"
           ) == "Primary"
  end

  test "fallback movement is bounded and cache values normalize to two states" do
    assert ProfileWidgetState.move_fallback(["A", "B"], 0, "up") == ["A", "B"]
    assert ProfileWidgetState.move_fallback(["A", "B"], 1, "down") == ["A", "B"]
    assert ProfileWidgetState.move_fallback(["A", "B"], 0, "down") == ["B", "A"]
    assert ProfileWidgetState.normalize_cache_mode("off") == "cache"
    assert ProfileWidgetState.normalize_cache_mode("refresh") == "refresh"
    assert ProfileWidgetState.normalize_cache_mode("unexpected") == "cache"
  end

  test "dirty fields compare persisted identity values across form and API shapes" do
    original = %{
      "profileId" => "Primary",
      "provider" => "openai",
      "apiInferenceType" => "responses",
      "baseUrl" => "https://example.test/v1",
      "endpointCredentialScope" => "user",
      "credentialId" => "credential-test",
      "backupProfiles" => ["Backup"]
    }

    current = %{
      "profileId" => "Primary",
      "provider" => "openai",
      "apiInferenceType" => "responses",
      "baseUrl" => "https://example.test/v1/",
      "endpointCredentialScope" => "user",
      "credentialId" => "credential-test",
      "backupProfiles" => " Backup "
    }

    assert ProfileWidgetState.dirty_fields(original, current) == MapSet.new()

    assert "baseUrl" in ProfileWidgetState.dirty_fields(original, %{
             current
             | "baseUrl" => "https://other.test/v1"
           })
  end
end
