defmodule HardenLlmWeb.ProfileDefaultsTest do
  use ExUnit.Case, async: true

  alias HardenLlmWeb.ProfileDefaults

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-052
  test "profile defaults match utility-llm's model and editor fallbacks" do
    form = ProfileDefaults.empty_form()

    assert ProfileDefaults.default_options() == %{"max_tokens" => 16_000}
    assert form["defaultOptionsJson"] == ~s({"max_tokens":16000})
    assert form["maxTokens"] == "16000"
    assert form["retryMaxAttempts"] == "4"
    assert form["retryBaseDelayMs"] == "500"
    assert form["retryMaxDelayMs"] == "8000"
    assert form["escalationAttempt"] == "3"
    assert form["escalationProfile"] == "CPA GPT-5.6 Sol"
    assert form["apiInferenceType"] == "chat-completions"
    assert form["escalationReasoning"] == "highest"

    assert ProfileDefaults.option_placeholder("temperature") == "0.2"
    assert ProfileDefaults.option_placeholder("topP") == "0.95"
    assert ProfileDefaults.option_placeholder("topK") == "40"
    assert ProfileDefaults.option_placeholder("stopSequences") == "one sequence per line"

    assert ProfileDefaults.option_placeholder("defaultOptionsJson") ==
             ~s({"temperature":0,"max_tokens":16000})

    assert ProfileDefaults.retry_placeholder("retryMaxAttempts") == "4"
    assert ProfileDefaults.retry_placeholder("retryBaseDelayMs") == "500"
    assert ProfileDefaults.retry_placeholder("retryMaxDelayMs") == "8000"
    assert ProfileDefaults.retry_placeholder("escalationAttempt") == "3"
    assert ProfileDefaults.pricing_placeholder() == "n/a"
    assert ProfileDefaults.profile_placeholder() == "OpenRouter DeepSeek V4 Flash"
    assert ProfileDefaults.base_url_placeholder() == "https://openrouter.ai/api/v1"
    assert ProfileDefaults.model_placeholder("main") == "gpt-5.6-luna"
    assert ProfileDefaults.model_placeholder("escalation") == "gpt-5.6-sol"
    assert ProfileDefaults.reasoning_default() == "lowest"
    assert ProfileDefaults.repair_reasoning_default() == "highest"
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
    assert ProfileDefaults.default_escalation_profile_id() == "CPA GPT-5.6 Sol"
    assert ProfileDefaults.default_escalation_model_id() == "gpt-5.6-sol"

    assert ProfileDefaults.field_info("maxAttempts") ==
             "Total attempts for the utility call, including initial, ordinary retry, repair, and escalation attempts."

    assert ProfileDefaults.field_info("pricingCacheWrite") =~ "Cache write applies"
    assert ProfileDefaults.field_info("unknown") == nil
  end
end
