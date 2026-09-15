defmodule HardenLlmWeb.ProfileDefaultsTest do
  use ExUnit.Case, async: true

  alias HardenLlmWeb.ProfileDefaults

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-052
  test "profile creation retains supplied backend recovery defaults and model options" do
    form = ProfileDefaults.empty_form(HardenLlmWeb.APIFixtures.recovery_policy())

    assert ProfileDefaults.default_options() == %{"max_tokens" => 16_000}
    assert form["defaultOptionsJson"] == ~s({"max_tokens":16000})
    assert form["maxTokens"] == "16000"
    assert form["recoveryPolicy"] == HardenLlmWeb.APIFixtures.recovery_policy()
    refute Map.has_key?(ProfileDefaults.empty_form(%{})["recoveryPolicy"], "maxAttempts")
    assert form["apiInferenceType"] == "chat-completions"

    assert ProfileDefaults.option_placeholder("temperature") == "0.2"
    assert ProfileDefaults.option_placeholder("topP") == "0.95"
    assert ProfileDefaults.option_placeholder("topK") == "40"
    assert ProfileDefaults.option_placeholder("stopSequences") == "one sequence per line"

    assert ProfileDefaults.option_placeholder("defaultOptionsJson") ==
             ~s({"temperature":0,"max_tokens":16000})

    assert ProfileDefaults.pricing_placeholder() == "n/a"
    assert ProfileDefaults.profile_placeholder() == "OpenRouter DeepSeek V4 Flash"
    assert ProfileDefaults.base_url_placeholder() == "https://openrouter.ai/api/v1"
    assert ProfileDefaults.model_placeholder("main") == "gpt-5.6-luna"
    assert ProfileDefaults.reasoning_default() == "lowest"
    assert ProfileDefaults.cache_mode_default() == "cache"
  end

  test "normalizing options adds only the actual max-token default" do
    assert ProfileDefaults.normalize_options(%{"temperature" => 0.3}) == %{
             "temperature" => 0.3,
             "max_tokens" => 16_000
           }

    assert ProfileDefaults.normalize_options(%{"max_tokens" => 256}) == %{
             "max_tokens" => 256
           }
  end

  test "exposes the utility workspace preset and contextual help text" do
    assert ProfileDefaults.default_profile_id() == "CPA GPT-5.6 Luna"
    assert ProfileDefaults.default_model_id() == "gpt-5.6-luna"

    assert ProfileDefaults.field_info("pricingCacheWrite") =~ "Cache write applies"
    assert ProfileDefaults.field_info("unknown") == nil
  end
end
