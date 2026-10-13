defmodule HardenLlmUI.ProfileReasoningTest do
  use ExUnit.Case, async: true
  alias HardenLlmUI.ProfileWidgetState
  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-456

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
