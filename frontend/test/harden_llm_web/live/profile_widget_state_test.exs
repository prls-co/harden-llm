defmodule HardenLlmWeb.ProfileWidgetStateTest do
  use ExUnit.Case, async: true

  alias HardenLlmWeb.ProfileWidgetState

  # PLAN-HLLM-WIDGET-PARITY-001 TEST-105 TEST-110

  test "options patches preserve unknown keys and canonicalize utility aliases" do
    options = %{"provider_option" => %{"keep" => true}, "topP" => 0.4}

    patched =
      ProfileWidgetState.patch_options(options, %{
        "topP" => "",
        "topK" => "40",
        "stopSequences" => "DONE\n\nSTOP"
      })

    assert patched["provider_option"] == %{"keep" => true}
    assert patched["top_k"] == 40
    refute Map.has_key?(patched, "topP")
    refute Map.has_key?(patched, "top_p")
    assert patched["stop"] == ["DONE", "STOP"]
  end

  @tag :recovery
  test "partial recovery edits preserve unrelated fields and blank input" do
    current = %{
      "modelId" => "fixture",
      "recoveryPolicy" => %{
        "maxAttempts" => 4,
        "retryOn" => [],
        "repairInvalidOutput" => true,
        "backoff" => %{"baseDelayMs" => 0, "maxDelayMs" => 8000}
      }
    }

    changed =
      ProfileWidgetState.merge_draft(current, %{
        "recoveryPolicy" => %{
          "repairInvalidOutput" => "false",
          "backoff" => %{"baseDelayMs" => ""}
        }
      })

    assert changed["modelId"] == "fixture"
    assert changed["recoveryPolicy"]["maxAttempts"] == 4
    assert changed["recoveryPolicy"]["retryOn"] == []
    policy = ProfileWidgetState.serialize_recovery_policy(changed["recoveryPolicy"])
    assert policy["repairInvalidOutput"] == false
    assert policy["backoff"] == %{"baseDelayMs" => "", "maxDelayMs" => 8000}
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

  test "cache values normalize to two states" do
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
      "credentialId" => "credential-test"
    }

    current = %{
      "profileId" => "Primary",
      "provider" => "openai",
      "apiInferenceType" => "responses",
      "baseUrl" => "https://example.test/v1/",
      "endpointCredentialScope" => "user",
      "credentialId" => "credential-test"
    }

    assert ProfileWidgetState.dirty_fields(original, current) == MapSet.new()

    assert "baseUrl" in ProfileWidgetState.dirty_fields(original, %{
             current
             | "baseUrl" => "https://other.test/v1"
           })
  end

  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-209
  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-071
  @tag :recovery
  test "complete policy serialization preserves false empty and zero without defaults" do
    draft = %{
      "maxAttempts" => "1",
      "retryOn" => [""],
      "repairInvalidOutput" => "false",
      "backoff" => %{"baseDelayMs" => "0", "maxDelayMs" => "0"}
    }

    expected = %{
      "maxAttempts" => 1,
      "retryOn" => [],
      "repairInvalidOutput" => false,
      "backoff" => %{"baseDelayMs" => 0, "maxDelayMs" => 0}
    }

    assert ProfileWidgetState.serialize_recovery_policy(draft) == expected
    assert ProfileWidgetState.serialize_recovery_policy(expected) == expected
    assert ProfileWidgetState.serialize_recovery_policy(%{}) == %{}
    invalid = put_in(draft, ["backoff", "baseDelayMs"], "")

    assert get_in(ProfileWidgetState.serialize_recovery_policy(invalid), [
             "backoff",
             "baseDelayMs"
           ]) == ""

    assert ProfileWidgetState.serialize_recovery_policy(%{"maxAttempts" => "11"}) == %{
             "maxAttempts" => 11
           }
  end
end
