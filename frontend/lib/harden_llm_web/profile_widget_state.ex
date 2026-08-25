defmodule HardenLlmWeb.ProfileWidgetState do
  @moduledoc """
  Pure transformations shared by the reusable profile widget.

  The widget keeps the editable form local, but its options, retry, fallback,
  cache, and model-list decisions must have one deterministic implementation.
  This module deliberately has no LiveView, browser, provider, or persistence
  dependencies.
  """

  @default_models [
    %{"id" => "gpt-5.6-luna", "label" => "GPT-5.6 Luna"},
    %{"id" => "gpt-5.6-sol", "label" => "GPT-5.6 Sol"},
    %{"id" => "gpt-5.6-terra", "label" => "GPT-5.6 Terra"}
  ]

  @retry_booleans ~w(enableRetryOn429 enableRetryOn5xx enableRetryOnNetworkError enableRetryOnParseError)
  @retry_numbers %{
    "retryMaxAttempts" => {"maxAttempts", :integer},
    "retryBaseDelayMs" => {"baseDelayMs", :integer},
    "retryMaxDelayMs" => {"maxDelayMs", :integer}
  }
  @scalar_options %{
    "maxTokens" => {"max_tokens", :integer},
    "temperature" => {"temperature", :float},
    "topP" => {"top_p", :float},
    "topK" => {"top_k", :integer}
  }

  @doc "Returns the small built-in catalog used only when a host supplies none."
  def default_model_options, do: @default_models

  @doc "Normalizes a host-owned model catalog without adding widget defaults."
  def normalize_model_catalog(models), do: normalize_models(models)

  @doc "Normalizes legacy cache values to the two supported widget states."
  def normalize_cache_mode("refresh"), do: "refresh"
  def normalize_cache_mode(_), do: "cache"

  @doc "Moves one fallback row while preserving order at list boundaries."
  def move_fallback(rows, index, direction) when is_list(rows) and is_integer(index) do
    target = if direction == "up", do: index - 1, else: index + 1

    if index < 0 or target < 0 or index >= length(rows) or target >= length(rows) do
      rows
    else
      current = Enum.at(rows, index)
      other = Enum.at(rows, target)
      rows |> List.replace_at(index, other) |> List.replace_at(target, current)
    end
  end

  def move_fallback(rows, _index, _direction), do: rows

  @doc "Builds the model combobox catalog with stable ID ownership."
  def model_options(host_catalog, profile_models, current_id) do
    source =
      if is_nil(host_catalog) do
        @default_models ++ normalize_models(profile_models)
      else
        normalize_models(host_catalog)
      end

    source
    |> Kernel.++([%{"id" => normalize_text(current_id), "label" => ""}])
    |> Enum.reject(&(&1["id"] == ""))
    |> Enum.reduce([], fn model, acc ->
      if Enum.any?(acc, &(&1["id"] == model["id"])), do: acc, else: acc ++ [model]
    end)
  end

  @doc "Applies widget field edits to one canonical provider-options map."
  def patch_options(options, params) when is_map(options) and is_map(params) do
    options
    |> canonicalize_alias("top_p", "topP")
    |> canonicalize_alias("top_k", "topK")
    |> patch_scalar_options(params)
    |> patch_stop_sequences(params)
    |> patch_retry_numbers(params)
    |> patch_retry_booleans(params)
    |> patch_structured_repair(params)
    |> remove_parse_retry_when_repair_enabled()
  end

  def patch_options(_options, _params), do: %{}

  @doc "Returns the field names whose persisted profile values differ."
  def dirty_fields(original, current) when is_map(original) and is_map(current) do
    fields =
      ~w(profileId provider apiInferenceType baseUrl endpointCredentialScope credentialId backupProfiles)

    fields
    |> Enum.filter(fn key ->
      normalize_field(key, original[key]) != normalize_field(key, current[key])
    end)
    |> MapSet.new()
  end

  def dirty_fields(_original, _current), do: MapSet.new()

  defp patch_scalar_options(options, params) do
    Enum.reduce(@scalar_options, options, fn {field, {key, kind}}, acc ->
      if Map.has_key?(params, field) do
        value = params[field]

        if blank?(value) do
          acc |> Map.delete(key) |> delete_alias(key)
        else
          case parse_number(value, kind) do
            {:ok, parsed} -> acc |> Map.put(key, parsed) |> delete_alias(key)
            :error -> acc
          end
        end
      else
        acc
      end
    end)
  end

  defp patch_stop_sequences(options, params) do
    if Map.has_key?(params, "stopSequences") do
      if blank?(params["stopSequences"]) do
        Map.delete(options, "stop")
      else
        stops =
          params["stopSequences"]
          |> to_string()
          |> String.split("\n")
          |> Enum.map(&String.trim/1)
          |> Enum.reject(&(&1 == ""))

        Map.put(options, "stop", stops)
      end
    else
      options
    end
  end

  defp patch_retry_numbers(options, params) do
    Enum.reduce(@retry_numbers, options, fn {field, {key, kind}}, acc ->
      if Map.has_key?(params, field) do
        if blank?(params[field]) do
          Map.delete(acc, key)
        else
          case parse_number(params[field], kind) do
            {:ok, value} -> Map.put(acc, key, value)
            :error -> acc
          end
        end
      else
        acc
      end
    end)
  end

  defp patch_retry_booleans(options, params) do
    Enum.reduce(@retry_booleans, options, fn key, acc ->
      value = if Map.has_key?(params, key), do: truthy?(params[key]), else: Map.get(acc, key)

      case value do
        true -> Map.delete(acc, key)
        false -> Map.put(acc, key, false)
        _ -> acc
      end
    end)
  end

  defp patch_structured_repair(options, params) do
    if Map.has_key?(params, "structuredRepairRetryEnabled") do
      if truthy?(params["structuredRepairRetryEnabled"]) do
        repair =
          case options["structuredRepairRetry"] do
            value when is_map(value) -> value
            _ -> %{}
          end

        repair =
          repair
          |> Map.put("enabled", true)
          |> patch_escalation(params)

        Map.put(options, "structuredRepairRetry", repair)
      else
        Map.put(options, "structuredRepairRetry", false)
      end
    else
      options
    end
  end

  defp patch_escalation(repair, params) do
    if Enum.any?(
         ~w(escalationAttempt escalationProfile escalationReasoning),
         &Map.has_key?(params, &1)
       ) do
      escalation = if is_map(repair["escalation"]), do: repair["escalation"], else: %{}

      escalation =
        escalation
        |> put_number_if_present("attempt", params["escalationAttempt"], :integer)
        |> put_text_if_present("llmProfile", params["escalationProfile"])
        |> put_text_if_present("reasoningEffort", params["escalationReasoning"])

      Map.put(repair, "escalation", escalation)
    else
      repair
    end
  end

  defp remove_parse_retry_when_repair_enabled(options) do
    case options["structuredRepairRetry"] do
      %{"enabled" => true} -> Map.delete(options, "enableRetryOnParseError")
      _ -> options
    end
  end

  defp put_number_if_present(map, _key, nil, _kind), do: map

  defp put_number_if_present(map, key, value, kind) do
    if blank?(value) do
      Map.delete(map, key)
    else
      case parse_number(value, kind) do
        {:ok, parsed} -> Map.put(map, key, parsed)
        :error -> map
      end
    end
  end

  defp put_text_if_present(map, _key, nil), do: map

  defp put_text_if_present(map, key, value) do
    if blank?(value), do: Map.delete(map, key), else: Map.put(map, key, normalize_text(value))
  end

  defp canonicalize_alias(options, key, alias_key) do
    cond do
      Map.has_key?(options, key) ->
        Map.delete(options, alias_key)

      Map.has_key?(options, alias_key) ->
        options |> Map.put(key, options[alias_key]) |> Map.delete(alias_key)

      true ->
        options
    end
  end

  defp delete_alias(options, "top_p"), do: Map.delete(options, "topP")
  defp delete_alias(options, "top_k"), do: Map.delete(options, "topK")
  defp delete_alias(options, _key), do: options

  defp normalize_models(models) when is_list(models) do
    Enum.reduce(models, [], fn model, acc ->
      model = normalize_model(model)

      if model["id"] == "" or Enum.any?(acc, &(&1["id"] == model["id"])),
        do: acc,
        else: acc ++ [model]
    end)
  end

  defp normalize_models(_), do: []

  defp normalize_model(%{"id" => id} = model),
    do: %{"id" => normalize_text(id), "label" => normalize_text(model["label"])}

  defp normalize_model(%{id: id} = model),
    do: %{"id" => normalize_text(id), "label" => normalize_text(model[:label])}

  defp normalize_model(id), do: %{"id" => normalize_text(id), "label" => ""}

  defp normalize_field("baseUrl", value),
    do: value |> normalize_text() |> String.trim_trailing("/")

  defp normalize_field("backupProfiles", value), do: normalize_backups(value)
  defp normalize_field(_key, value), do: normalize_text(value)

  defp normalize_backups(value) when is_list(value),
    do: Enum.map(value, &normalize_text/1) |> Enum.reject(&(&1 == ""))

  defp normalize_backups(value),
    do:
      value
      |> normalize_text()
      |> String.split(",")
      |> Enum.map(&String.trim/1)
      |> Enum.reject(&(&1 == ""))

  defp parse_number(value, :integer) do
    case Integer.parse(normalize_text(value)) do
      {number, ""} -> {:ok, number}
      _ -> :error
    end
  end

  defp parse_number(value, :float) do
    case Float.parse(normalize_text(value)) do
      {number, ""} -> {:ok, number}
      _ -> :error
    end
  end

  defp blank?(value), do: normalize_text(value) == ""
  defp normalize_text(value), do: String.trim(to_string(value || ""))
  defp truthy?(value), do: value in [true, "true", "on", "1"]
end
