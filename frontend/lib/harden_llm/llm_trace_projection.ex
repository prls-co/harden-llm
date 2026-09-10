defmodule HardenLlm.LlmTraceProjection do
  @moduledoc """
  Pure display and resource projection for validated LLM execution diagnostics.

  Consumes schema v2 validated by `LlmDiagnosticsWire`; it does not adapt retired
  execution formats or reconstruct diagnostics from mutable configuration.
  """

  alias HardenLlm.LlmCostFormatter

  @missing_request "Request payload is not available for this trace."
  @missing_response "Response payload is not available for this trace."

  def trace_available?(result) when is_map(result), do: present?(trace_id(result))
  def trace_available?(_result), do: false

  def summary(result) when is_map(result) do
    usage = result_usage(result)

    metrics = [
      %{"key" => "attempts", "value" => "🔁 #{attempt_count(result)}", "title" => "Attempts"},
      %{"key" => "duration", "value" => "⏱️ #{duration(result)}", "title" => "Duration"},
      %{
        "key" => "cache-status",
        "value" => "💾",
        "class" => "ullm-cache-status ullm-cache-status-#{cache_status(result)}",
        "title" => cache_status_title(result),
        "aria_label" => "Harden-LLM cache: #{cache_status_label(result)}",
        "data_cache_status" => cache_status(result),
        "role" => "img"
      },
      %{
        "key" => "input-tokens",
        "value" => "📥 #{usage_number(usage, "inputTokens")}",
        "title" => usage_title(usage, "Input tokens")
      },
      %{
        "key" => "cache-tokens",
        "value" => "↺ #{usage_sum(usage, ~w(cacheReadTokens cacheCreationTokens))}",
        "title" => usage_title(usage, "Cache tokens")
      },
      %{
        "key" => "completion-tokens",
        "value" => "📤 #{usage_sum(usage, ~w(outputTokens reasoningTokens))}",
        "title" => usage_title(usage, "Completion tokens")
      },
      %{"key" => "cost", "value" => cost(result), "title" => cost_title(result)}
    ]

    target = selected_target(result)

    %{
      "status_icon" => if(success?(result), do: "✅", else: "❌"),
      "trace_id" => trace_id(result),
      "model_id" => target["modelId"] || "",
      "error_category" => if(success?(result), do: nil, else: error_category(result)),
      "metrics" => metrics
    }
  end

  def summary(_result), do: %{"status_icon" => "ℹ️", "metrics" => []}

  def details(result) when is_map(result) do
    selected = selected_target(result)
    producer = producer_target(result)
    accounting = result["accounting"] || %{}

    %{
      "trace_id" => trace_id(result),
      "run_id" => text([result["runId"]]),
      "schema_label" => "v2",
      "profile_id" => text([selected["profileId"]]),
      "model_id" => text([selected["modelId"]]),
      "provider" => text([selected["provider"]]),
      "api_inference_type" => text([selected["protocol"]]),
      "provider_base_url" => text([selected["endpoint"]]),
      "result_source" => result_source_label(result),
      "producer_profile_id" => text([producer["profileId"]]),
      "producer_model_id" => text([producer["modelId"]]),
      "producer_provider" => text([producer["provider"]]),
      "producer_protocol" => text([producer["protocol"]]),
      "producer_endpoint" => text([producer["endpoint"]]),
      "provider_invoked" => captured_boolean(result["providerInvoked"]),
      "result_usage_status" => get_in(accounting, ["result", "usage", "status"]),
      "provider_usage_status" => get_in(accounting, ["provider", "usage", "status"]),
      "result_cost_status" => get_in(accounting, ["result", "cost", "status"]),
      "provider_cost_status" => get_in(accounting, ["provider", "cost", "status"]),
      "status" => status_label(result),
      "cache_status" => cache_status_label(result),
      "used_repair" => used_repair?(result),
      "attempts" => attempts(result)
    }
  end

  def details(_result), do: %{"attempts" => []}

  def meta(result) when is_map(result) do
    target = selected_target(result)
    "#{target["protocol"] || "—"} · #{target["endpoint"] || "—"}"
  end

  def meta(_result), do: "— · —"

  def resources_from_run(result, request, api_origin, trace_url, artifact_url)
      when is_map(result) do
    id = trace_id(result)

    %{
      "trace_url" => trace_url,
      "curl" => curl(request, api_origin),
      "request" => local_resource(request, @missing_request),
      "response" => local_resource(result, @missing_response),
      "artifacts" => artifact_links(result, id, artifact_url)
    }
  end

  def resources_from_run(_result, _request, _api_origin, _trace_url, _artifact_url), do: %{}

  def resources_from_trace(trace, result, api_origin, trace_url, artifact_url)
      when is_map(trace) and is_map(result) do
    id = trace_id(result)
    resources = trace["resources"]
    request = normalize_resource(resources["request"], @missing_request)
    result = Map.put(result, "artifacts", trace["artifacts"])

    %{
      "trace_url" => trace_url,
      "curl" => resource_curl(request, api_origin),
      "request" => request,
      "response" => normalize_resource(resources["response"], @missing_response),
      "artifacts" => artifact_links(result, id, artifact_url)
    }
  end

  def resources_from_trace(_trace, _result, _api_origin, _trace_url, _artifact_url), do: %{}

  def run_result(%{"record" => record, "artifacts" => artifacts, "traceId" => trace_id})
      when is_map(record) and is_list(artifacts) do
    {:ok, record |> Map.put_new("traceId", trace_id) |> Map.put("artifacts", artifacts)}
  end

  def run_result(_trace), do: :error

  def curl(payload, api_origin) when is_map(payload) and is_binary(api_origin) do
    endpoint = String.trim_trailing(api_origin, "/") <> "/api/v1/run"
    body = payload |> Jason.encode!() |> shell_quote()

    "curl --fail-with-body --request POST #{shell_quote(endpoint)} " <>
      "--header 'accept: application/json' " <>
      "--header \"authorization: Bearer ${HARDEN_LLM_TOKEN}\" " <>
      "--header 'content-type: application/json' --data-raw #{body}"
  end

  def curl(_payload, _api_origin), do: nil

  def json_text(nil), do: ""
  def json_text(value) when is_binary(value), do: value
  def json_text(value), do: Jason.encode!(value, pretty: true)

  def trace_id(result) when is_map(result), do: text([result["traceId"]])
  def trace_id(_result), do: nil

  def attempt_count(result) when is_map(result), do: length(raw_attempts(result))

  def attempt_count(_result), do: 0

  def duration(result) when is_map(result) do
    duration_ms =
      number_value(result["totalCallDurationMs"])

    if is_number(duration_ms),
      do: :erlang.float_to_binary(duration_ms / 1000, decimals: 2) <> "s",
      else: "—"
  end

  def duration(_result), do: "—"

  def cache_tokens(result) when is_map(result) do
    result |> result_usage() |> sum_if_captured(~w(cacheReadTokens cacheCreationTokens))
  end

  def cache_tokens(_result), do: nil

  def completion_tokens(result) when is_map(result) do
    result |> result_usage() |> sum_if_captured(~w(outputTokens reasoningTokens))
  end

  def completion_tokens(_result), do: nil

  def number(value) when is_integer(value), do: value |> Integer.to_string() |> group_digits()
  def number(_value), do: "—"

  def cost(result) when is_map(result) do
    value = cost_value(result_cost(result))
    if(cache_served?(result), do: "🗄️" <> value, else: value)
  end

  def cost(_result), do: "$—"

  def cost_title(result) do
    cost = result_cost(result)
    prefix = if(cache_served?(result), do: "Cached result", else: "Result")

    case cost_status(cost) do
      "exact" -> "#{prefix} exact trace-attributed cost #{cost_value(cost)}"
      "partial" -> "#{prefix} known cost subtotal #{cost_value(cost)}; additional cost is unknown"
      "unknown" -> "#{prefix} cost is unknown"
      _ -> "#{prefix} cost was not captured"
    end
  end

  def cache_served?(result) when is_map(result), do: get_in(result, ["cache", "served"]) == true
  def cache_served?(_result), do: false

  def cache_status(result) when is_map(result) do
    cache = result["cache"] || %{}

    cond do
      cache["served"] == true or cache["status"] == "hit" -> "hit"
      cache["mode"] == "off" or cache["status"] in ["disabled", "skipped"] -> "disabled"
      cache["status"] == "miss" -> "miss"
      cache["status"] == "refresh" -> "refresh"
      cache["written"] == true -> "written"
      true -> "unknown"
    end
  end

  def cache_status(_result), do: "unknown"

  def cache_status_label(result) do
    case cache_status(result) do
      "hit" -> "Hit"
      "miss" -> if(cache_written?(result), do: "Miss · saved", else: "Miss")
      "refresh" -> if(cache_written?(result), do: "Fresh run · saved", else: "Fresh run")
      "written" -> "Saved"
      "disabled" -> "Disabled"
      _ -> "Unknown"
    end
  end

  def cache_status_title(result) do
    case {cache_status(result), cache_written?(result)} do
      {"hit", _} ->
        "Harden-LLM cache hit: reused the saved response without a provider call."

      {"miss", true} ->
        "Harden-LLM cache miss: ran the provider and saved the successful response."

      {"miss", false} ->
        "Harden-LLM cache miss: no saved response was available."

      {"refresh", true} ->
        "Harden-LLM fresh run: skipped the old cache and saved the successful response."

      {"refresh", false} ->
        "Harden-LLM fresh run: skipped the old cache."

      {"disabled", _} ->
        "Harden-LLM cache was disabled for this run."

      {"written", _} ->
        "The successful response was saved to the Harden-LLM cache."

      _ ->
        "The Harden-LLM cache result did not include a recognized status."
    end
  end

  def used_repair?(result) when is_map(result) do
    result["usedRepair"] == true or
      Enum.any?(raw_attempts(result), &(&1["repair"] == true))
  end

  def used_repair?(_result), do: false

  def attempts(result) when is_map(result) do
    result
    |> raw_attempts()
    |> Enum.map(fn attempt ->
      target = attempt["target"] || %{}

      %{
        "attempt" => attempt["number"],
        "retry_local_attempt" => attempt["retryLocalNumber"],
        "profile_id" => attempt["profileId"],
        "provider" => target["provider"],
        "protocol" => target["protocol"],
        "model_id" => target["modelId"],
        "category" => text([attempt["category"]]) || "not captured",
        "status_code" => attempt["httpStatus"],
        "retryable" => attempt["retryable"] == true,
        "delay_ms" => milliseconds(attempt["wait"]),
        "duration_ms" => milliseconds(attempt["duration"]),
        "provider_used" => attempt["providerUsed"],
        "repair" => attempt["repair"]
      }
    end)
  end

  def attempts(_result), do: []

  defp selected_target(result), do: result["selectedTarget"] || %{}

  defp producer_target(result), do: get_in(result, ["resultSource", "producer"]) || %{}

  defp result_source_label(%{"resultSource" => source}) do
    case source["kind"] do
      "provider" -> "Provider attempt #{source["attemptNumber"]}"
      "cache" -> "Cache"
      _ -> "No result"
    end
  end

  defp result_source_label(_result), do: "No result"

  defp result_usage(result), do: get_in(result, ["accounting", "result", "usage"]) || %{}

  defp result_cost(result), do: get_in(result, ["accounting", "result", "cost"]) || %{}

  defp usage_number(usage, key) do
    case captured_usage_value(usage, key) do
      value when is_integer(value) -> number(value)
      _ -> "—"
    end
  end

  defp usage_sum(usage, keys) do
    case sum_if_captured(usage, keys) do
      value when is_integer(value) -> number(value)
      _ -> "—"
    end
  end

  defp sum_if_captured(usage, keys) do
    values = Enum.map(keys, &captured_usage_value(usage, &1))
    if Enum.all?(values, &is_integer/1), do: Enum.sum(values), else: nil
  end

  defp captured_usage_value(%{"status" => status}, _key)
       when status in ~w(unavailable inconsistent), do: nil

  defp captured_usage_value(usage, key), do: if(is_integer(usage[key]), do: usage[key], else: nil)

  defp usage_title(%{"status" => "partial"}, label), do: "#{label} (partial accounting)"

  defp usage_title(%{"status" => status}, label) when status in ~w(unavailable inconsistent),
    do: "#{label} (#{status})"

  defp usage_title(_usage, label), do: label

  defp cost_value(cost) do
    case cost_status(cost) do
      "exact" -> LlmCostFormatter.format(cost["knownSubtotalUsd"])
      "partial" -> "≥" <> LlmCostFormatter.format(cost["knownSubtotalUsd"])
      _ -> "$—"
    end
  end

  defp cost_status(%{"status" => status}), do: status
  defp cost_status(_cost), do: "not captured"

  defp success?(result), do: result["status"] == "succeeded"

  defp error_category(result) do
    attempt = List.last(raw_attempts(result)) || %{}

    text([attempt["category"], result["status"]]) ||
      "error"
  end

  defp status_label(result) do
    attempt = List.last(raw_attempts(result)) || %{}

    status_code = attempt["httpStatus"]

    label = if(success?(result), do: "Success", else: title_case(error_category(result)))
    if is_integer(status_code), do: "#{label} (#{status_code})", else: label
  end

  defp raw_attempts(result), do: if(is_list(result["attempts"]), do: result["attempts"], else: [])

  defp captured_boolean(value) when is_boolean(value), do: value
  defp captured_boolean(_value), do: nil

  defp milliseconds(nanoseconds) when is_integer(nanoseconds), do: div(nanoseconds, 1_000_000)
  defp milliseconds(_nanoseconds), do: nil

  defp cache_written?(result), do: get_in(result, ["cache", "written"]) == true

  defp local_resource(payload, _message) when is_map(payload),
    do: %{"available" => true, "payload" => payload}

  defp local_resource(_payload, message), do: %{"available" => false, "message" => message}

  defp normalize_resource(%{"available" => true, "payload" => _payload} = resource, _message),
    do: resource

  defp normalize_resource(%{"available" => false, "message" => message}, _default),
    do: %{"available" => false, "message" => message}

  defp normalize_resource(_resource, message), do: %{"available" => false, "message" => message}

  defp resource_curl(%{"available" => true, "payload" => payload}, origin),
    do: curl(payload, origin)

  defp resource_curl(_resource, _origin), do: nil

  defp artifact_links(result, id, artifact_url)
       when is_binary(id) and is_function(artifact_url, 2) do
    Enum.map(result["artifacts"] || [], fn artifact ->
      available? = artifact["state"] == "available"

      %{
        "available" => available?,
        "href" => if(available?, do: artifact_url.(id, artifact["artifactId"]), else: nil),
        "label" =>
          "#{artifact["kind"]} · #{artifact["sizeBytes"]} bytes" <>
            if(available?, do: "", else: " · #{artifact["state"]}")
      }
    end)
  end

  defp artifact_links(_result, _id, _artifact_url), do: []

  defp shell_quote(value), do: "'" <> String.replace(value, "'", "'\"'\"'") <> "'"

  defp text(values) do
    Enum.find_value(values, fn
      value when value in [nil, false] ->
        nil

      value when is_binary(value) ->
        if(String.trim(value) == "", do: nil, else: String.trim(value))

      value when is_atom(value) or is_number(value) ->
        to_string(value)

      _ ->
        nil
    end)
  end

  defp title_case(value) do
    value
    |> to_string()
    |> String.split(~r/[\s_-]+/)
    |> Enum.reject(&(&1 == ""))
    |> Enum.map(&String.capitalize/1)
    |> Enum.join(" ")
  end

  defp number_value(value) when is_integer(value) or is_float(value), do: value
  defp number_value(_value), do: nil

  defp group_digits(value) do
    value |> String.reverse() |> String.replace(~r/(\d{3})(?=\d)/, "\\1,") |> String.reverse()
  end

  defp present?(value) when is_binary(value), do: String.trim(value) != ""
  defp present?(value), do: not is_nil(value)
end
