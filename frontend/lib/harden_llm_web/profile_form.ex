defmodule HardenLlmWeb.ProfileForm do
  @moduledoc """
  Pure conversion and validation for a saved LLM profile.

  The workspace widget and the Profiles page use the same form contract.  This
  module deliberately has no LiveView state or HTTP calls; callers own the
  draft and decide which profile-save operation to invoke.
  """

  alias HardenLlmWeb.{ProfileDefaults, ProfileWidgetState}

  def empty_form(policy), do: ProfileDefaults.empty_form(policy)

  def field_error(errors, name) do
    errors[name] ||
      Enum.find_value(errors, fn {field, message} ->
        if String.ends_with?(field, "." <> name), do: message
      end)
  end

  def options_valid?(value) do
    case decode_object(value, "Default options JSON") do
      {:ok, _options} -> true
      {:error, _message} -> false
    end
  end

  @doc "Converts a backend profile state into the editable profile form shape."
  def profile_form(profile_state) do
    profile = profile_state["profile"] || %{}
    credential = profile_state["credential"] || %{}
    options = ProfileDefaults.normalize_options(profile["defaultOptions"])
    pricing = profile["pricing"] || %{}

    Map.merge(ProfileDefaults.empty_form(Map.fetch!(profile, "recoveryPolicy")), %{
      "profileId" => profile["llmProfile"] || "",
      "provider" => profile["provider"] || "",
      "apiInferenceType" =>
        profile["apiInferenceType"] || ProfileDefaults.api_inference_type_default(),
      "baseUrl" => normalize_base_url(profile["baseUrl"] || ""),
      "modelId" => profile["modelId"] || "",
      "credentialId" => credential["credentialId"] || "",
      "credentialConfigured" => to_string(credential["configured"] || false),
      "endpointCredentialScope" => profile["endpointCredentialScope"] || "user",
      "apiKey" => "",
      "supportsTemperature" => to_string(profile["supportsTemperature"] || false),
      "supportsContractedStructuredOutput" =>
        to_string(profile["supportsContractedStructuredOutput"] || false),
      "supportsWebSearch" => to_string(supports_web_search?(profile)),
      "maxTokens" =>
        option_text(options["max_tokens"] || ProfileDefaults.default_options()["max_tokens"]),
      "temperature" => option_text(options["temperature"]),
      "topP" => option_text(options["top_p"] || options["topP"]),
      "topK" => option_text(options["top_k"] || options["topK"]),
      "stopSequences" => stop_text(options["stop"]),
      "defaultOptionsJson" => Jason.encode!(options, pretty: true),
      "pricingInput" => pricing_text(pricing["input_cost_per_token"]),
      "pricingOutput" => pricing_text(pricing["output_cost_per_token"]),
      "pricingCacheRead" => pricing_text(pricing["cache_read_input_token_cost"]),
      "pricingCacheWrite" => pricing_text(pricing["cache_creation_input_token_cost"]),
      "pricingReasoning" => pricing_text(pricing["output_cost_per_reasoning_token"])
    })
  end

  @doc "Builds the backend profile payload while preserving credential safety."
  def profile_payload(params) do
    with {:ok, options} <- options_payload(params),
         {:ok, pricing} <- pricing_payload(params) do
      credential = String.trim(params["apiKey"] || "")

      payload = %{
        "profile" => %{
          "schemaVersion" => 3,
          "llmProfile" => params["profileId"] || "",
          "provider" => params["provider"] || "",
          "apiInferenceType" =>
            params["apiInferenceType"] || ProfileDefaults.api_inference_type_default(),
          "endpointCredentialScope" => params["endpointCredentialScope"] || "user",
          "baseUrl" => normalize_base_url(params["baseUrl"] || ""),
          "modelId" => params["modelId"] || "",
          "pricing" => pricing,
          "supportsTemperature" => truthy?(params["supportsTemperature"]),
          "supportsContractedStructuredOutput" =>
            truthy?(params["supportsContractedStructuredOutput"]),
          "supportsWebSearch" => truthy?(params["supportsWebSearch"]),
          "tokensParam" => nil,
          "responsesTokensParam" => nil,
          "defaultOptions" => options,
          "recoveryPolicy" =>
            ProfileWidgetState.serialize_current_recovery_policy(params["recoveryPolicy"])
        },
        "credentialId" => params["credentialId"] || ""
      }

      {:ok,
       if(credential == "",
         do: payload,
         else: Map.put(payload, "credential", %{"apiKey" => credential})
       )}
    end
  end

  defp options_payload(params) do
    with {:ok, options} <- decode_object(params["defaultOptionsJson"], "Default options JSON"),
         {:ok, options} <-
           put_number_option(
             options,
             "max_tokens",
             params["maxTokens"],
             "Max Output Tokens",
             :integer
           ),
         {:ok, options} <-
           put_number_option(options, "temperature", params["temperature"], "Temperature", :float),
         {:ok, options} <- put_number_option(options, "top_p", params["topP"], "Top P", :float),
         {:ok, options} <- put_number_option(options, "top_k", params["topK"], "Top K", :integer),
         {:ok, options} <- put_stop_option(options, params["stopSequences"]) do
      {:ok, options}
    end
  end

  defp pricing_payload(params) do
    fields = %{
      "input_cost_per_token" => params["pricingInput"],
      "output_cost_per_token" => params["pricingOutput"],
      "cache_read_input_token_cost" => params["pricingCacheRead"],
      "cache_creation_input_token_cost" => params["pricingCacheWrite"],
      "output_cost_per_reasoning_token" => params["pricingReasoning"]
    }

    with {:ok, values} <-
           Enum.reduce_while(fields, {:ok, %{}}, fn {key, value}, {:ok, acc} ->
             case rate_value(value, key) do
               {:ok, nil} -> {:cont, {:ok, Map.put(acc, key, nil)}}
               {:ok, number} -> {:cont, {:ok, Map.put(acc, key, number / 1_000_000)}}
               {:error, message} -> {:halt, {:error, message}}
             end
           end) do
      if Enum.any?(values, fn {_key, value} -> not is_nil(value) end),
        do: {:ok, values},
        else: {:ok, nil}
    end
  end

  defp put_number_option(options, key, value, _label, _kind) when value in [nil, ""],
    do: {:ok, Map.delete(options, option_alias(key))}

  defp put_number_option(options, key, value, label, kind) do
    case number_value(value, label, kind) do
      {:ok, number} -> {:ok, options |> Map.put(key, number) |> Map.delete(option_alias(key))}
      {:error, message} -> {:error, message}
    end
  end

  defp option_alias("top_p"), do: "topP"
  defp option_alias("top_k"), do: "topK"
  defp option_alias(_key), do: nil

  defp put_stop_option(options, value) when value in [nil, ""], do: {:ok, options}

  defp put_stop_option(options, value) do
    stops = value |> String.split("\n") |> Enum.map(&String.trim/1) |> Enum.reject(&(&1 == ""))
    {:ok, Map.put(options, "stop", stops)}
  end

  defp decode_object(value, label) do
    text = String.trim(value || "")

    if text == "" do
      {:ok, %{}}
    else
      case Jason.decode(text) do
        {:ok, object} when is_map(object) -> {:ok, object}
        {:ok, _} -> {:error, "#{label} must be a JSON object."}
        {:error, _} -> {:error, "#{label} must be valid JSON."}
      end
    end
  end

  defp rate_value(value, _label) when value in [nil, ""], do: {:ok, nil}

  defp rate_value(value, label) do
    case Float.parse(String.trim(to_string(value))) do
      {number, ""} when number >= 0 -> {:ok, number}
      _ -> {:error, "#{label} must be a non-negative number."}
    end
  end

  defp number_value(value, label, :integer) do
    case Integer.parse(String.trim(to_string(value))) do
      {number, ""} when number >= 0 -> {:ok, number}
      _ -> {:error, "#{label} must be a non-negative integer."}
    end
  end

  defp number_value(value, label, :float) do
    case Float.parse(String.trim(to_string(value))) do
      {number, ""} when number >= 0 -> {:ok, number}
      _ -> {:error, "#{label} must be a non-negative number."}
    end
  end

  defp option_text(value) when is_nil(value), do: ""
  defp option_text(value), do: to_string(value)

  defp normalize_base_url(value),
    do: value |> to_string() |> String.trim() |> String.trim_trailing("/")

  defp stop_text(value) when is_list(value), do: Enum.join(value, "\n")
  defp stop_text(_value), do: ""

  defp pricing_text(value) when is_number(value),
    do:
      :erlang.float_to_binary(value * 1.0 * 1_000_000, decimals: 12)
      |> String.trim_trailing("0")
      |> String.trim_trailing(".")

  defp pricing_text(_value), do: ""
  defp truthy?(value), do: value in [true, "true", "on", "1"]

  defp supports_web_search?(profile) do
    case Map.fetch(profile, "supportsWebSearch") do
      {:ok, value} -> truthy?(value)
      :error -> false
    end
  end
end
