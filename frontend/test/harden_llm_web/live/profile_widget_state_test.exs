defmodule HardenLlmWeb.ProfileWidgetStateTest do
  use ExUnit.Case, async: true

  alias HardenLlmWeb.ProfileWidgetState

  # PLAN-HLLM-WIDGET-PARITY-001 TEST-105 TEST-110 WEB-TEST-083 WEB-TEST-085

  # WEB-TEST-091: recovery composition is finite and server-owned.
  test "exposes exactly the six recovery nodes and role capability matrix" do
    assert Enum.sort(ProfileWidgetState.recovery_node_ids()) ==
             Enum.sort([
               "original",
               "original-repair-initial",
               "original-repair-escalation",
               "rerun",
               "rerun-repair-initial",
               "rerun-repair-escalation"
             ])

    assert ProfileWidgetState.recovery_capability?("original", "jsonRepair")
    assert ProfileWidgetState.recovery_capability?("rerun", "jsonRepair")
    refute ProfileWidgetState.recovery_capability?("rerun", "rerun")
    refute ProfileWidgetState.recovery_capability?("rerun-repair-initial", "webSearch")

    refute ProfileWidgetState.recovery_capability?(
             "original",
             "profile_definition",
             "webSearch"
           )

    refute ProfileWidgetState.recovery_capability?("unknown", "jsonRepair")

    assert ProfileWidgetState.recovery_node_target_path("rerun-repair-initial") ==
             ["recoveryPolicy", "rerun", "jsonRepair", "initial"]
  end

  # WEB-TEST-092: target option edits remain invocation-local provider options.
  test "target option patches serialize into the leaf without nested recovery" do
    target = %{
      "source" => "profile",
      "profileId" => "Luna",
      "reasoningEffort" => "lowest",
      "providerOptions" => %{"keep" => true}
    }

    patched =
      ProfileWidgetState.patch_recovery_target(target, %{
        "maxTokens" => "2048",
        "temperature" => "0.2",
        "topP" => "0.95",
        "topK" => "40",
        "stopSequences" => "DONE\nSTOP"
      })

    assert patched == %{
             "source" => "profile",
             "profileId" => "Luna",
             "reasoningEffort" => "lowest",
             "providerOptions" => %{
               "keep" => true,
               "max_tokens" => 2048,
               "temperature" => 0.2,
               "top_p" => 0.95,
               "top_k" => 40,
               "stop" => ["DONE", "STOP"]
             }
           }

    refute Map.has_key?(patched, "jsonRepair")
    refute Map.has_key?(patched, "rerun")
  end

  test "fixed node patches use the descriptor path and reject unknown nodes" do
    draft = %{
      "recoveryPolicy" => %{
        "rerun" => %{
          "target" => %{"source" => "profile", "profileId" => "A"},
          "jsonRepair" => %{"initial" => %{"source" => "profile", "profileId" => "B"}}
        }
      }
    }

    assert {:ok, patched} =
             ProfileWidgetState.patch_node(
               draft,
               "rerun-repair-initial",
               %{"profileId" => "C", "jsonRepair" => %{"unexpected" => true}}
             )

    assert get_in(patched, ["recoveryPolicy", "rerun", "jsonRepair", "initial", "profileId"]) ==
             "C"

    refute get_in(patched, ["recoveryPolicy", "rerun", "jsonRepair", "initial", "jsonRepair"])

    assert {:error, :unknown_or_non_target_node} =
             ProfileWidgetState.patch_node(draft, "original", %{"profileId" => "A"})

    assert {:error, :unknown_or_non_target_node} =
             ProfileWidgetState.patch_node(draft, "not-a-node", %{"profileId" => "A"})
  end

  test "full draft serialization keeps the REST policy and strips UI-only target nesting" do
    draft = %{
      "ui" => %{"rerunConfigOpen" => true},
      "recoveryPolicy" => %{
        "rerun" => %{
          "target" => %{
            "source" => "profile",
            "profileId" => "A",
            "jsonRepair" => %{"initial" => %{"profileId" => "wrong"}}
          }
        }
      }
    }

    serialized = ProfileWidgetState.serialize_widget(draft)
    refute Map.has_key?(serialized, "ui")
    refute get_in(serialized, ["recoveryPolicy", "rerun", "target", "jsonRepair"])
  end

  test "switching a recovery target profile clears stale leaf overrides" do
    target = %{
      "source" => "profile",
      "profileId" => "Luna",
      "modelId" => "old-model",
      "reasoningEffort" => "highest",
      "providerOptions" => %{"max_tokens" => 99}
    }

    profiles = [
      %{
        "profile" => %{
          "llmProfile" => "Astra",
          "reasoningEffortMap" => %{"lowest" => %{}, "highest" => %{}}
        }
      }
    ]

    assert ProfileWidgetState.reset_recovery_target_for_profile(
             Map.put(target, "profileId", "Astra"),
             profiles
           ) == %{
             "source" => "profile",
             "profileId" => "Astra",
             "reasoningEffort" => "lowest"
           }
  end

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

  test "capability and node descriptors do not accept browser-supplied roles" do
    assert ProfileWidgetState.capabilities("json_repair", "workspace")["webSearch"] == false
    assert ProfileWidgetState.capabilities("rerun_generation", "workspace")["rerun"] == false
    assert ProfileWidgetState.capabilities("not-a-role", "workspace") == %{}
    assert ProfileWidgetState.node_descriptor("rerun")["role"] == "rerun_generation"
    assert ProfileWidgetState.node_descriptor("not-a-node") == nil
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

  @tag :recovery
  test "explicit repair and rerun targets round-trip without recursive policy fields" do
    policy = %{
      "maxAttempts" => "6",
      "retryOn" => ["network", ""],
      "backoff" => %{"baseDelayMs" => "0", "maxDelayMs" => "8000"},
      "jsonRepair" => %{
        "initial" => %{
          "source" => "profile",
          "profileId" => "CPA GPT-5.6 Luna",
          "reasoningEffort" => "lowest",
          "providerOptions" => %{"max_tokens" => 256},
          "rerun" => %{"should" => "not survive"}
        },
        "escalation" => nil
      },
      "rerun" => %{
        "target" => %{"source" => "generation"},
        "jsonRepair" => nil
      }
    }

    assert ProfileWidgetState.serialize_recovery_policy(policy) == %{
             "maxAttempts" => 6,
             "retryOn" => ["network"],
             "backoff" => %{"baseDelayMs" => 0, "maxDelayMs" => 8000},
             "jsonRepair" => %{
               "initial" => %{
                 "source" => "profile",
                 "profileId" => "CPA GPT-5.6 Luna",
                 "reasoningEffort" => "lowest",
                 "providerOptions" => %{"max_tokens" => 256}
               },
               "escalation" => nil
             },
             "rerun" => %{
               "target" => %{"source" => "generation"},
               "jsonRepair" => nil
             }
           }
  end

  test "current policy conversion keeps disabled branches explicit and supplies bounded editor drafts" do
    legacy = %{
      "maxAttempts" => 4,
      "retryOn" => [],
      "repairInvalidOutput" => false,
      "backoff" => %{"baseDelayMs" => 0, "maxDelayMs" => 0}
    }

    current = ProfileWidgetState.serialize_current_recovery_policy(legacy)
    assert current["jsonRepair"] == nil
    assert current["rerun"] == nil
    assert current["repairInvalidOutput"] == nil

    assert ProfileWidgetState.default_recovery_repair_plan() == %{
             "initial" => %{"source" => "generation"},
             "escalation" => %{"source" => "generation"}
           }

    assert ProfileWidgetState.default_recovery_rerun_plan() == %{
             "target" => %{"source" => "generation"},
             "jsonRepair" => ProfileWidgetState.default_recovery_repair_plan()
           }
  end
end
