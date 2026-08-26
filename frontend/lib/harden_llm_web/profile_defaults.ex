defmodule HardenLlmWeb.ProfileDefaults do
  @moduledoc """
  Shared profile-editor defaults mirrored from utility-llm's profile editor.

  Only `max_tokens` is an actual model-option default. The temperature, top-p,
  and top-k values are intentionally placeholders: leaving those fields empty
  keeps provider-specific behavior unchanged.
  """

  @default_max_output_tokens 16_000
  @default_options %{"max_tokens" => @default_max_output_tokens}
  @default_options_json ~s({"max_tokens":16000})
  @default_api_inference_type "chat-completions"
  @default_profile_id "CPA GPT-5.6 Luna"
  @default_model_id "gpt-5.6-luna"
  @default_reasoning "lowest"
  @default_repair_reasoning "highest"
  @default_cache_mode "cache"

  @field_info %{
    "structuredRepairRetry" =>
      "Runs semantic JSON repair for structured-output parse or schema failures. Missing settings default to enabled in Trace Studio; set this off to store structuredRepairRetry: false.",
    "enableRetryOn429" =>
      "When enabled, provider rate-limit responses can consume another attempt from the shared max-attempt retry budget.",
    "enableRetryOn5xx" =>
      "When enabled, upstream server errors can consume another attempt from the shared max-attempt retry budget.",
    "enableRetryOnNetworkError" =>
      "When enabled, transient network failures can consume another attempt from the shared max-attempt retry budget.",
    "enableRetryOnParseError" =>
      "When enabled, JSON parse and schema failures can consume another attempt. Structured Repair requires this and locks it on.",
    "maxAttempts" =>
      "Total attempts for the utility call, including initial, ordinary retry, repair, and escalation attempts.",
    "baseDelayMs" =>
      "Initial retry delay before the next attempt. Backoff grows from this value.",
    "maxDelayMs" => "Upper bound for retry backoff delay between attempts.",
    "escalationAttempt" =>
      "First attempt number that should use the escalation profile for structured repair.",
    "pricingCacheWrite" =>
      "Cache write applies to prompt tokens used to create cache entries. Some providers only expose cache read pricing, so this is optional.",
    "pricingReasoning" =>
      "Reasoning output applies only to reasoning tokens returned separately in usage. Leave empty if your provider does not report reasoning output pricing."
  }

  @option_placeholders %{
    "maxTokens" => Integer.to_string(@default_max_output_tokens),
    "temperature" => "0.2",
    "topP" => "0.95",
    "topK" => "40",
    "stopSequences" => "one sequence per line",
    "defaultOptionsJson" => ~s({"temperature":0,"max_tokens":16000})
  }

  @retry_defaults %{
    "maxAttempts" => 4,
    "baseDelayMs" => 500,
    "maxDelayMs" => 8_000,
    "escalationAttempt" => 3
  }

  @retry_form_defaults %{
    "retryMaxAttempts" => Integer.to_string(@retry_defaults["maxAttempts"]),
    "retryBaseDelayMs" => Integer.to_string(@retry_defaults["baseDelayMs"]),
    "retryMaxDelayMs" => Integer.to_string(@retry_defaults["maxDelayMs"]),
    "escalationAttempt" => Integer.to_string(@retry_defaults["escalationAttempt"])
  }

  @retry_placeholders @retry_form_defaults

  @empty_form_base %{
    "profileId" => "",
    "provider" => "",
    "apiInferenceType" => @default_api_inference_type,
    "baseUrl" => "",
    "modelId" => "",
    "credentialId" => "",
    "credentialConfigured" => "false",
    "endpointCredentialScope" => "user",
    "apiKey" => "",
    "backupProfiles" => "",
    "supportsTemperature" => "true",
    "supportsContractedStructuredOutput" => "true",
    "maxTokens" => Integer.to_string(@default_max_output_tokens),
    "temperature" => "",
    "topP" => "",
    "topK" => "",
    "stopSequences" => "",
    "defaultOptionsJson" => @default_options_json,
    "structuredRepairRetryEnabled" => "true",
    "enableRetryOn429" => "true",
    "enableRetryOn5xx" => "true",
    "enableRetryOnNetworkError" => "true",
    "enableRetryOnParseError" => "true",
    "escalationProfile" => "",
    "escalationReasoning" => @default_repair_reasoning,
    "pricingInput" => "",
    "pricingOutput" => "",
    "pricingCacheRead" => "",
    "pricingCacheWrite" => "",
    "pricingReasoning" => ""
  }

  @empty_form Map.merge(@empty_form_base, @retry_form_defaults)

  @doc "Returns the blank profile editor shape used by all profile surfaces."
  def empty_form, do: @empty_form

  @doc "Returns the actual default model options used when no options are stored."
  def default_options, do: @default_options

  @doc "Returns the compact JSON form of the default model options."
  def default_options_json, do: @default_options_json

  @doc "Returns the default inference transport for a new profile."
  def api_inference_type_default, do: @default_api_inference_type

  @doc "Returns the utility-llm profile selected for a new workspace."
  def default_profile_id, do: @default_profile_id

  @doc "Returns the model ID paired with the default workspace profile."
  def default_model_id, do: @default_model_id

  @doc "Returns the default reasoning level for an ordinary model run."
  def reasoning_default, do: @default_reasoning

  @doc "Returns the default reasoning level for structured-repair escalation."
  def repair_reasoning_default, do: @default_repair_reasoning

  @doc "Returns the cache-first mode used by the utility-llm UI."
  def cache_mode_default, do: @default_cache_mode

  @doc "Returns utility-llm's contextual help text for a field, when defined."
  def field_info(key), do: Map.get(@field_info, to_string(key))

  @doc "Adds the canonical model-option defaults to a profile options map."
  def normalize_options(options) when is_map(options),
    do: Map.put_new(options, "max_tokens", @default_max_output_tokens)

  def normalize_options(_options), do: @default_options

  @doc "Returns the utility-llm placeholder for one option editor field."
  def option_placeholder(field), do: Map.get(@option_placeholders, field)

  @doc "Returns the utility-llm runtime fallback for one retry control."
  def retry_default(field), do: Map.get(@retry_defaults, field)

  @doc "Returns the utility-llm placeholder for one retry editor field."
  def retry_placeholder(field), do: Map.get(@retry_placeholders, field)

  @doc "Returns the utility-llm empty-pricing placeholder."
  def pricing_placeholder, do: "n/a"

  @doc "Returns the default profile-picker placeholder used by utility-llm."
  def profile_placeholder, do: "OpenRouter DeepSeek V4 Flash"

  @doc "Returns the default endpoint placeholder used by utility-llm."
  def base_url_placeholder, do: "https://openrouter.ai/api/v1"

  @doc "Returns the model-slot placeholder for the main or repair editor."
  def model_placeholder("escalation"), do: "gpt-5.4"
  def model_placeholder(_kind), do: "gpt-5.6-luna"
end
