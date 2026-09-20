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

  @leaf_target_fields ~w(source profileId modelId reasoningEffort providerOptions)

  @recovery_node_descriptors %{
    "original" => %{
      "role" => "original_generation",
      "targetPath" => [],
      "repairPath" => ["recoveryPolicy", "jsonRepair"]
    },
    "original-repair-initial" => %{
      "role" => "json_repair",
      "targetPath" => ["recoveryPolicy", "jsonRepair", "initial"],
      "repairPath" => nil
    },
    "original-repair-escalation" => %{
      "role" => "json_repair",
      "targetPath" => ["recoveryPolicy", "jsonRepair", "escalation"],
      "repairPath" => nil
    },
    "rerun" => %{
      "role" => "rerun_generation",
      "targetPath" => ["recoveryPolicy", "rerun", "target"],
      "repairPath" => ["recoveryPolicy", "rerun", "jsonRepair"]
    },
    "rerun-repair-initial" => %{
      "role" => "json_repair",
      "targetPath" => ["recoveryPolicy", "rerun", "jsonRepair", "initial"],
      "repairPath" => nil
    },
    "rerun-repair-escalation" => %{
      "role" => "json_repair",
      "targetPath" => ["recoveryPolicy", "rerun", "jsonRepair", "escalation"],
      "repairPath" => nil
    }
  }

  @role_capabilities %{
    "original_generation" => %{
      "jsonRepair" => true,
      "rerun" => true,
      "retryPolicy" => true,
      "webSearch" => true,
      "cache" => true
    },
    "rerun_generation" => %{
      "jsonRepair" => true,
      "rerun" => false,
      "retryPolicy" => false,
      "webSearch" => true,
      "cache" => true
    },
    "json_repair" => %{
      "jsonRepair" => false,
      "rerun" => false,
      "retryPolicy" => false,
      "webSearch" => false,
      "cache" => false
    }
  }

  @doc "Returns the small built-in catalog used only when a host supplies none."
  def default_model_options, do: @default_models

  @doc "Returns the finite, server-owned recovery node identifiers."
  def recovery_node_ids, do: Map.keys(@recovery_node_descriptors)

  @doc "Returns a recovery node descriptor or nil for an unknown node."
  def recovery_node_descriptor(node_id) when is_binary(node_id),
    do: Map.get(@recovery_node_descriptors, node_id)

  def recovery_node_descriptor(_node_id), do: nil

  @doc "Returns role capabilities for a fixed recovery node and host context."
  def recovery_node_capabilities(node_id, host_context \\ "workspace")

  def recovery_node_capabilities(node_id, host_context) when is_binary(node_id) do
    with %{"role" => role} <- recovery_node_descriptor(node_id),
         capabilities when is_map(capabilities) <- capabilities(role, host_context) do
      capabilities
    else
      _ -> %{}
    end
  end

  def recovery_node_capabilities(_node_id, _host_context), do: %{}

  @doc "Returns capabilities for a server-owned role; unknown roles have none."
  def capabilities(role, host_context \\ "workspace")

  def capabilities(role, host_context) when is_binary(role) do
    case Map.get(@role_capabilities, role) do
      capabilities when is_map(capabilities) and host_context == "profile_definition" ->
        Map.drop(capabilities, ["webSearch", "cache"])

      capabilities when is_map(capabilities) ->
        capabilities

      _ ->
        %{}
    end
  end

  def capabilities(_role, _host_context), do: %{}

  @doc "Checks one capability without accepting arbitrary role input from a client."
  def recovery_capability?(node_id, capability)
      when is_binary(node_id) and is_binary(capability) do
    recovery_capability?(node_id, "workspace", capability)
  end

  def recovery_capability?(_node_id, _capability), do: false

  @doc "Checks one capability for a fixed node and host context."
  def recovery_capability?(node_id, host_context, capability)
      when is_binary(node_id) and is_binary(host_context) and is_binary(capability) do
    Map.get(recovery_node_capabilities(node_id, host_context), capability, false) == true
  end

  def recovery_capability?(_node_id, _host_context, _capability), do: false

  @doc "Returns the target path for a finite recovery node."
  def recovery_node_target_path(node_id) do
    case recovery_node_descriptor(node_id) do
      %{"targetPath" => path} -> path
      _ -> nil
    end
  end

  @doc "Alias used by renderers and host adapters for the fixed descriptor lookup."
  def node_descriptor(node_id), do: recovery_node_descriptor(node_id)

  @doc "Returns a fixed node's canonical target path."
  def node_path(node_id), do: recovery_node_target_path(node_id)

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

      {"jsonRepair", value} ->
        {"jsonRepair", serialize_repair_plan(value)}

      {"rerun", value} when is_map(value) ->
        {"rerun",
         value
         |> Map.update("target", %{}, &serialize_recovery_target/1)
         |> Map.update("jsonRepair", nil, &serialize_repair_plan/1)}

      {"repairInvalidOutput", "true"} ->
        {"repairInvalidOutput", true}

      {"repairInvalidOutput", "false"} ->
        {"repairInvalidOutput", false}

      entry ->
        entry
    end)
  end

  def serialize_recovery_policy(policy), do: policy

  @doc "Serializes the current v3 policy shape, mapping only historical boolean input."
  def serialize_current_recovery_policy(policy) when is_map(policy) do
    policy = serialize_recovery_policy(policy)

    if Map.has_key?(policy, "repairInvalidOutput") and
         not Map.has_key?(policy, "jsonRepair") and not Map.has_key?(policy, "rerun") do
      enabled = policy["repairInvalidOutput"] == true

      policy
      |> Map.delete("repairInvalidOutput")
      |> Map.put("jsonRepair", if(enabled, do: generation_repair_plan(), else: nil))
      |> Map.put("rerun", nil)
    else
      policy
    end
  end

  def serialize_current_recovery_policy(policy), do: policy

  @doc "Returns the safe generation-relative repair defaults used when a branch is enabled in the editor."
  def default_recovery_repair_plan do
    %{
      "initial" => %{"source" => "generation"},
      "escalation" => %{"source" => "generation"}
    }
  end

  @doc "Returns the safe fresh-rerun draft, including its shared repair shape."
  def default_recovery_rerun_plan do
    %{
      "target" => %{"source" => "generation"},
      "jsonRepair" => default_recovery_repair_plan()
    }
  end

  @doc "Serializes one leaf target without introducing a nested recovery policy."
  def serialize_recovery_target(target) when is_map(target) do
    provider_options = target_provider_options(target)

    target
    |> Map.take(@leaf_target_fields)
    |> Map.new(fn
      {"providerOptions", _value} -> {"providerOptions", provider_options}
      entry -> entry
    end)
    |> maybe_put_provider_options(provider_options, target)
  end

  def serialize_recovery_target(_target), do: %{}

  @doc "Patches one target while rejecting fields outside the leaf contract."
  def patch_recovery_target(target, incoming) when is_map(target) and is_map(incoming) do
    target
    |> merge_draft(incoming)
    |> serialize_recovery_target()
  end

  def patch_recovery_target(_target, _incoming), do: %{}

  @doc "Patches one fixed node in a full draft and rejects unknown nodes."
  def patch_node(draft, node_id, field_patch)
      when is_map(draft) and is_binary(node_id) and is_map(field_patch) do
    case recovery_node_target_path(node_id) do
      path when is_list(path) and path != [] ->
        current = get_in(draft, path) || %{}
        {:ok, put_in(draft, path, patch_recovery_target(current, field_patch))}

      _ ->
        {:error, :unknown_or_non_target_node}
    end
  end

  def patch_node(_draft, _node_id, _field_patch), do: {:error, :invalid_node_patch}

  @doc "Serializes a full profile/run draft without UI-only node state."
  def serialize_widget(draft) when is_map(draft) do
    draft = Map.drop(draft, ["ui", "nodeState", "recoveryTargetConfigOpen"])

    case Map.fetch(draft, "recoveryPolicy") do
      {:ok, policy} -> Map.put(draft, "recoveryPolicy", serialize_current_recovery_policy(policy))
      :error -> draft
    end
  end

  def serialize_widget(draft), do: draft

  @doc "Starts a selected profile target with no stale per-profile overrides."
  def reset_recovery_target_for_profile(target, profiles)
      when is_map(target) and is_list(profiles) do
    profile_id = normalize_text(target["profileId"])

    case Enum.find(profiles, &(get_in(&1, ["profile", "llmProfile"]) == profile_id)) do
      nil ->
        target

      profile_state ->
        %{
          "source" => "profile",
          "profileId" => profile_id,
          "reasoningEffort" => profile_default_reasoning(profile_state)
        }
    end
  end

  def reset_recovery_target_for_profile(target, _profiles), do: target

  @doc "Returns a policy branch in the shape consumed by the shared target picker."
  def recovery_branch(policy, key) when is_map(policy) and key in ["jsonRepair", "rerun"] do
    case policy[key] do
      value when is_map(value) -> value
      _ -> nil
    end
  end

  def recovery_branch(_policy, _key), do: nil

  @doc "Whether a target editor should hide cache, search, and recovery controls."
  def target_only?(assigns) when is_map(assigns),
    do: assigns[:target_only] == true or assigns["targetOnly"] == true

  def target_only?(_assigns), do: false

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

  defp serialize_repair_plan(nil), do: nil

  defp serialize_repair_plan(value) when is_map(value) do
    value
    |> Map.update("initial", %{}, &serialize_recovery_target/1)
    |> Map.update("escalation", nil, fn
      nil -> nil
      target -> serialize_recovery_target(target)
    end)
  end

  defp serialize_repair_plan(value), do: value

  defp generation_repair_plan, do: default_recovery_repair_plan()

  defp target_provider_options(target) do
    base = if is_map(target["providerOptions"]), do: target["providerOptions"], else: %{}

    base =
      case Map.get(target, "defaultOptionsJson") do
        value when is_binary(value) ->
          case Jason.decode(value) do
            {:ok, decoded} when is_map(decoded) -> decoded
            _ -> base
          end

        _ ->
          base
      end

    base
    |> patch_target_number("maxTokens", "max_tokens", :integer, target)
    |> patch_target_number("temperature", "temperature", :float, target)
    |> patch_target_number("topP", "top_p", :float, target)
    |> patch_target_number("topK", "top_k", :integer, target)
    |> patch_target_stop(target)
  end

  defp patch_target_number(options, field, option_key, kind, target) do
    if Map.has_key?(target, field) do
      value = target[field]

      if blank?(value) do
        Map.delete(options, option_key)
      else
        case parse_number(value, kind) do
          {:ok, parsed} -> Map.put(options, option_key, parsed)
          :error -> options
        end
      end
    else
      options
    end
  end

  defp patch_target_stop(options, target) do
    if Map.has_key?(target, "stopSequences") do
      stops =
        target["stopSequences"]
        |> to_string()
        |> String.split("\n")
        |> Enum.map(&String.trim/1)
        |> Enum.reject(&(&1 == ""))

      if stops == [], do: Map.delete(options, "stop"), else: Map.put(options, "stop", stops)
    else
      options
    end
  end

  defp maybe_put_provider_options(target, provider_options, original)
       when is_map(provider_options) do
    option_fields = ~w(defaultOptionsJson maxTokens temperature topP topK stopSequences)

    if Map.has_key?(original, "providerOptions") or
         Enum.any?(option_fields, &Map.has_key?(original, &1)),
       do: Map.put(target, "providerOptions", provider_options),
       else: target
  end

  defp profile_default_reasoning(profile_state) do
    map = get_in(profile_state, ["profile", "reasoningEffortMap"])

    cond do
      is_map(map) and Map.has_key?(map, "lowest") -> "lowest"
      is_map(map) -> map |> Map.keys() |> Enum.sort() |> List.first() || ""
      true -> ""
    end
  end
end
