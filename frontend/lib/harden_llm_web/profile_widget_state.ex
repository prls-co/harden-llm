defmodule HardenLlmWeb.ProfileWidgetState do
  @moduledoc """
  Pure transformations shared by the reusable profile widget.

  The widget keeps the editable form local, but its options, recovery,
  cache, and model-list decisions must have one deterministic implementation.
  This module deliberately has no LiveView, browser, provider, or persistence
  dependencies.
  """

  alias HardenLlmWeb.ProfileDefaults

  @default_models [
    %{"id" => "gpt-5.6-luna", "label" => "GPT-5.6 Luna"},
    %{"id" => "gpt-5.6-sol", "label" => "GPT-5.6 Sol"},
    %{"id" => "gpt-5.6-terra", "label" => "GPT-5.6 Terra"}
  ]

  @scalar_options %{
    "maxTokens" => {"max_tokens", :integer},
    "temperature" => {"temperature", :float},
    "topP" => {"top_p", :float},
    "topK" => {"top_k", :integer}
  }

  @doc "Returns the small built-in catalog used only when a host supplies none."
  def default_model_options, do: @default_models

  @doc "Resolves the initial profile without hiding the backend-owned presets."
  def resolve_selected_profile_id(profiles, selected_id) when is_list(profiles) do
    selected_id = normalize_text(selected_id)

    if selected_id != "" do
      selected_id
    else
      profile_ids = Enum.map(profiles, &get_in(&1, ["profile", "llmProfile"]))

      cond do
        ProfileDefaults.default_profile_id() in profile_ids ->
          ProfileDefaults.default_profile_id()

        true ->
          Enum.find(profile_ids, &(is_binary(&1) and &1 != "")) || ""
      end
    end
  end

  def resolve_selected_profile_id(_profiles, selected_id), do: normalize_text(selected_id)

  @doc "Resolves the initial model from the selected backend-owned profile."
  def resolve_selected_model_id(profiles, profile_id, model_id) when is_list(profiles) do
    model_id = normalize_text(model_id)

    if model_id != "" do
      model_id
    else
      profiles
      |> Enum.find(&(get_in(&1, ["profile", "llmProfile"]) == profile_id))
      |> get_in(["profile", "modelId"])
      |> normalize_text()
    end
  end

  def resolve_selected_model_id(_profiles, _profile_id, model_id), do: normalize_text(model_id)

  @doc "Normalizes a host-owned model catalog without adding widget defaults."
  def normalize_model_catalog(models), do: normalize_models(models)

  @doc "Normalizes legacy cache values to the two supported widget states."
  def normalize_cache_mode("refresh"), do: "refresh"
  def normalize_cache_mode(_), do: "cache"

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
  end

  def patch_options(_options, _params), do: %{}

  @doc "Returns the field names whose persisted profile values differ."
  def dirty_fields(original, current) when is_map(original) and is_map(current) do
    fields =
      ~w(profileId provider apiInferenceType baseUrl endpointCredentialScope credentialId)

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

  defp normalize_field(_key, value), do: normalize_text(value)

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
  @doc "Serializes form values without supplying defaults or validating backend policy semantics."
  def serialize_recovery_policy(policy) when is_map(policy) do
    Map.new(policy, fn
      {"maxAttempts", value} ->
        {"maxAttempts", form_integer(value)}

      {"backoff", values} when is_map(values) ->
        {"backoff", Map.new(values, fn {key, value} -> {key, form_integer(value)} end)}

      {"retryOn", values} when is_list(values) ->
        {"retryOn", Enum.reject(values, &(&1 == ""))}

      {"repairInvalidOutput", "true"} ->
        {"repairInvalidOutput", true}

      {"repairInvalidOutput", "false"} ->
        {"repairInvalidOutput", false}

      entry ->
        entry
    end)
  end

  def serialize_recovery_policy(policy), do: policy

  @doc "Applies one recovery-policy edit to the supplied host-owned policy."
  def merge_recovery_policy(current, incoming) when is_map(current) and is_map(incoming) do
    current
    |> merge_draft(incoming)
    |> serialize_recovery_policy()
  end

  def merge_recovery_policy(_current, incoming), do: serialize_recovery_policy(incoming)

  @doc "Applies a partial form event while retaining unrelated draft fields."
  def merge_draft(current, incoming) do
    Map.merge(current, incoming, fn
      _key, old, new when is_map(old) and is_map(new) -> merge_draft(old, new)
      _key, _old, new -> new
    end)
  end

  defp form_integer(value) when is_binary(value) do
    case Integer.parse(value) do
      {number, ""} -> number
      _ -> value
    end
  end

  defp form_integer(value), do: value
end
