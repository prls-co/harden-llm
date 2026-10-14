defmodule HardenLlmUI.ProfileReasoningTest do
  use ExUnit.Case, async: true
  alias HardenLlmUI.ProfileWidgetState
  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-456
  # TEST-461 / WEB-TEST-129: finite capability domains and independent wire maps.

  @paths [
    ~w(jsonRepair initial),
    ~w(jsonRepair escalation),
    ~w(rerun target),
    ~w(rerun jsonRepair initial),
    ~w(rerun jsonRepair escalation)
  ]

  defp policy do
    %{
      "maxAttempts" => 6,
      "retryOn" => [],
      "backoff" => %{"baseDelayMs" => 0, "maxDelayMs" => 0},
      "jsonRepair" => %{
        "initial" => %{"source" => "generation"},
        "escalation" => %{"source" => "generation"}
      },
      "rerun" => %{
        "target" => %{
          "source" => "profile",
          "profileId" => "generation",
          "modelId" => "generation-model",
          "reasoningEffort" => "middle",
          "providerOptions" => %{"max_tokens" => 24}
        },
        "jsonRepair" => %{
          "initial" => %{"source" => "generation"},
          "escalation" => %{"source" => "generation"}
        }
      }
    }
  end

  test "every finite position preserves allowed values across full partial and absent maps" do
    generation = %{
      "profileId" => "generation",
      "modelId" => "generation-model",
      "reasoningEffort" => "middle",
      "providerOptions" => %{"max_tokens" => 24}
    }

    for path <- @paths,
        levels <- [["lowest", "middle", "highest"], ["middle", "highest"], []],
        requested <- [nil, "lowest", "middle", "highest"],
        options <- [%{}, %{"max_tokens" => 32, "stream" => false, "seed" => 0}] do
      mapping = Map.new(levels, &{&1, %{}})

      profiles = [
        %{"id" => "target", "definition" => %{"reasoningEffortMap" => mapping}},
        %{"id" => "generation", "definition" => %{"reasoningEffortMap" => %{"middle" => %{}}}}
      ]

      target = %{
        "source" => "profile",
        "profileId" => "target",
        "modelId" => "custom",
        "providerOptions" => options,
        "reasoningEffort" => requested
      }

      input = put_in(policy(), path, target)
      selected = if requested in levels, do: requested, else: List.first(levels)
      expected = Map.delete(target, "reasoningEffort")
      expected = if selected, do: Map.put(expected, "reasoningEffort", selected), else: expected
      result = ProfileWidgetState.normalize_recovery_policy(input, profiles, generation)
      assert get_in(result, path) == expected
      assert result["maxAttempts"] == 6
      assert result["retryOn"] == []
      assert result["backoff"] == %{"baseDelayMs" => 0, "maxDelayMs" => 0}
      assert ProfileWidgetState.normalize_recovery_policy(result, profiles, generation) == result
    end
  end

  test "inheritance retains repair semantics and fresh rerun compiles complete generation values" do
    for path <- @paths do
      generation = %{
        "profileId" => "generation",
        "modelId" => "generation-model",
        "reasoningEffort" => "middle",
        "providerOptions" => %{"max_tokens" => 24}
      }

      profiles = [
        %{"id" => "generation", "definition" => %{"reasoningEffortMap" => %{"middle" => %{}}}}
      ]

      input = put_in(policy(), path, %{"source" => "generation"})

      expected =
        if path == ~w(rerun target),
          do: Map.put(generation, "source", "profile"),
          else: %{"source" => "generation"}

      result = ProfileWidgetState.normalize_recovery_policy(input, profiles, generation)
      assert get_in(result, path) == expected
    end
  end

  test "unknown target references survive unchanged for canonical backend rejection" do
    for path <- @paths do
      target = %{
        "source" => "profile",
        "profileId" => "unknown-profile",
        "reasoningEffort" => "highest",
        "modelId" => "must-not-default",
        "providerOptions" => %{"stream" => false}
      }

      input = put_in(policy(), path, target)
      assert get_in(ProfileWidgetState.normalize_recovery_policy(input, [], %{}), path) == target
    end
  end

  test "supported reasoning has one capability owner and keeps explicit valid selections" do
    for {mapping, current, expected} <- [
          {nil, "lowest", ""},
          {%{}, "highest", ""},
          {%{"lowest" => %{}, "highest" => %{}}, "highest", "highest"},
          {%{"middle" => %{}, "highest" => %{}}, nil, "middle"}
        ] do
      profile = %{"definition" => %{"reasoningEffortMap" => mapping}}
      assert ProfileWidgetState.profile_reasoning_effort(profile, current) == expected
    end
  end

  test "persisted targets lose unsupported reasoning while inheritance and other settings remain" do
    profiles = [%{"id" => "unmapped", "definition" => %{}}]

    target = %{
      "source" => "profile",
      "profileId" => "unmapped",
      "reasoningEffort" => "lowest",
      "modelId" => "custom",
      "providerOptions" => %{"max_tokens" => 32}
    }

    policy = %{
      "maxAttempts" => 4,
      "jsonRepair" => %{"initial" => %{"source" => "generation"}, "escalation" => target},
      "rerun" => nil
    }

    normalized = ProfileWidgetState.normalize_recovery_policy(policy, profiles, %{})

    assert normalized ==
             update_in(policy, ~w(jsonRepair escalation), &Map.delete(&1, "reasoningEffort"))

    assert ProfileWidgetState.normalize_recovery_policy(normalized, profiles, %{}) == normalized
  end

  test "fresh rerun inherits the explicit generation setup through a valid profile target" do
    profiles = [
      %{"id" => "mapped", "definition" => %{"reasoningEffortMap" => %{"highest" => %{}}}}
    ]

    generation = %{
      "profileId" => "mapped",
      "modelId" => "custom-model",
      "reasoningEffort" => "highest",
      "providerOptions" => %{"max_tokens" => 16},
      "userPrompt" => "not a target field"
    }

    policy = %{"rerun" => %{"target" => %{"source" => "generation"}, "jsonRepair" => nil}}
    normalized = ProfileWidgetState.normalize_recovery_policy(policy, profiles, generation)

    assert normalized["rerun"]["target"] ==
             generation |> Map.delete("userPrompt") |> Map.put("source", "profile")
  end
end
