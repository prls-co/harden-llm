defmodule HardenLlm.LlmTraceProjection do
  @moduledoc """
  Pure projection for reusable LLM trace details and aggregate statistics.

  The module has no LiveView, route, session, or transport dependency. Hosts
  supply their own public URLs while this module owns normalization, display
  semantics, and credential-free replay command generation.
  """

  @missing_request "Request payload is not available for this trace."
  @missing_response "Response payload is not available for this trace."

  def trace_available?(result) when is_map(result), do: present?(trace_id(result))
  def trace_available?(_result), do: false

  def summary(result) when is_map(result) do
    metrics = [
      %{"value" => "🔁 #{attempt_count(result)}", "title" => "Attempts"},
      %{"value" => "⏱️ #{duration(result)}", "title" => "Duration"},
      %{
        "value" => "💾",
        "id" => "run-cache-status",
        "class" => "ullm-cache-status ullm-cache-status-#{cache_status(result)}",
        "title" => cache_status_title(result),
        "aria_label" => "Harden-LLM cache: #{cache_status_label(result)}",
        "data_cache_status" => cache_status(result),
        "role" => "img"
      },
      %{
        "value" => "📥 #{number(get_in(result, ["usage", "inputTokens"]))}",
        "title" => "Input tokens"
      },
      %{"value" => "↺ #{number(cache_tokens(result))}", "title" => "Cache tokens"},
      %{"value" => "📤 #{number(completion_tokens(result))}", "title" => "Output tokens"},
      if(cost(result), do: %{"value" => cost(result), "title" => cost_title(result)})
    ]

    %{
      "status_icon" => if(success?(result), do: "✅", else: "❌"),
      "trace_id" => trace_id(result),
      "model_id" => text([result["modelId"]]) || "",
      "error_category" => if(success?(result), do: nil, else: error_category(result)),
      "metrics" => Enum.reject(metrics, &is_nil/1)
    }
  end

  def summary(_result), do: %{"status_icon" => "ℹ️", "metrics" => []}

  def details(result) when is_map(result) do
    %{
      "trace_id" => trace_id(result),
      "run_id" => text([result["runId"]]),
      "profile_id" => text([result["profileId"]]),
      "model_id" => text([result["modelId"]]),
      "provider" => text([result["provider"]]),
      "api_inference_type" => text([result["apiInferenceType"]]),
      "provider_base_url" => text([result["providerBaseUrl"]]),
      "status" => status_label(result),
      "cache_status" => cache_status_label(result),
      "used_repair" => used_repair?(result),
      "attempts" =>
        Enum.map(attempts(result), fn attempt ->
          %{
            "attempt" => attempt["attempt"],
            "category" => attempt["category"],
            "status_code" => attempt["statusCode"],
            "retryable" => attempt["retryable"],
            "delay_ms" => attempt["delayMs"],
            "duration_ms" => attempt["durationMs"]
          }
        end)
    }
  end

  def details(_result), do: %{"attempts" => []}

  def meta(result) when is_map(result) do
    inference_type = text([result["apiInferenceType"]]) || "—"
    base_url = text([result["providerBaseUrl"]]) || "—"
    "#{inference_type} · #{base_url}"
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
    resources = if is_map(trace["resources"]), do: trace["resources"], else: %{}
    request = normalize_resource(resources["request"], @missing_request)

    result =
      Map.put(
        result,
        "artifacts",
        if(is_list(trace["artifacts"]), do: trace["artifacts"], else: [])
      )

    %{
      "trace_url" => trace_url,
      "curl" => resource_curl(request, api_origin),
      "request" => request,
      "response" => normalize_resource(resources["response"], @missing_response),
      "artifacts" => artifact_links(result, id, artifact_url)
    }
  end

  def resources_from_trace(_trace, _result, _api_origin, _trace_url, _artifact_url), do: %{}

  def run_result(%{"record" => record} = trace) when is_map(record) do
    artifacts = if is_list(trace["artifacts"]), do: trace["artifacts"], else: []

    {:ok,
     record
     |> Map.put_new("traceId", trace["traceId"])
     |> Map.put("artifacts", artifacts)}
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

  def stats(api_stats) when is_map(api_stats) do
    total_count = integer(api_stats["totalCount"])
    total_duration = integer(api_stats["totalCallDurationMs"])
    total_cost = numeric(api_stats["totalCost"]) || 0
    cached_cost = numeric(api_stats["cachedCost"]) || 0

    %{
      runs: total_count,
      success: integer(api_stats["successCount"]),
      failed: integer(api_stats["failureCount"]),
      timeout: integer(api_stats["timeoutCount"]),
      prompt_tokens: integer(api_stats["totalPromptTokens"]),
      cache_read_tokens: integer(api_stats["cacheReadTokens"]),
      cache_creation_tokens: integer(api_stats["cacheCreationTokens"]),
      output_tokens: integer(api_stats["totalOutputTokens"]),
      reasoning_tokens: integer(api_stats["reasoningTokens"]),
      total_tokens: integer(api_stats["totalTokens"]),
      known_cost: "$" <> :erlang.float_to_binary(total_cost * 1.0, decimals: 4),
      cached_cost: "$" <> :erlang.float_to_binary(cached_cost * 1.0, decimals: 4),
      cached_count: integer(api_stats["cachedCount"]),
      known_cost_count: integer(api_stats["knownCostCount"]),
      unknown_cost_count: integer(api_stats["unknownCostCount"]),
      total_duration: total_duration,
      average_duration: if(total_count > 0, do: div(total_duration, total_count), else: nil),
      max_duration: integer(api_stats["maxCallDurationMs"]),
      over_budget_count: integer(api_stats["overBudgetCount"]),
      max_over_budget: integer(api_stats["maxOverBudgetMs"])
    }
  end

  def stats(_api_stats), do: stats(%{})

  def history_stats(item) when is_map(item) do
    usage = get_in(item, ["result", "usage"]) || %{}
    cost = get_in(item, ["result", "cost"]) || %{}
    attempts = get_in(item, ["result", "attempts"]) || []

    %{
      status: item["status"] || "unknown",
      profile: item["profileId"] || "",
      duration: history_duration_ms(item),
      input_tokens: integer(usage["inputTokens"]),
      cache_read_tokens: integer(usage["cacheReadTokens"]),
      cache_creation_tokens: integer(usage["cacheCreationTokens"]),
      output_tokens: integer(usage["outputTokens"]),
      reasoning_tokens: integer(usage["reasoningTokens"]),
      total_tokens: integer(usage["totalTokens"]),
      known_cost: if(cost["known"] == false, do: nil, else: numeric(cost["totalUsd"])),
      attempts: if(is_list(attempts), do: length(attempts), else: 0)
    }
  end

  def history_stats(_item), do: %{}

  def json_text(nil), do: ""
  def json_text(value) when is_binary(value), do: value
  def json_text(value), do: Jason.encode!(value, pretty: true)

  def trace_id(result) when is_map(result) do
    text([result["traceId"]])
  end

  def trace_id(_result), do: nil

  def attempt_count(result) when is_map(result) do
    case result["attempts"] do
      attempts when is_list(attempts) -> length(attempts)
      _ -> max(integer(result["totalAttempts"]), 0)
    end
  end

  def attempt_count(_result), do: 0

  def duration(result) when is_map(result) do
    duration_ms =
      numeric(
        first_present([
          result["totalCallDurationMs"],
          result["durationMs"],
          result["totalWaitMs"]
        ])
      ) ||
        0

    :erlang.float_to_binary(duration_ms / 1000, decimals: 2) <> "s"
  end

  def duration(_result), do: "0.00s"

  def cache_tokens(result) when is_map(result) do
    integer(get_in(result, ["usage", "cacheReadTokens"])) +
      integer(get_in(result, ["usage", "cacheCreationTokens"]))
  end

  def cache_tokens(_result), do: 0

  def completion_tokens(result) when is_map(result) do
    integer(get_in(result, ["usage", "outputTokens"])) +
      integer(get_in(result, ["usage", "reasoningTokens"]))
  end

  def completion_tokens(_result), do: 0

  def number(value) do
    value
    |> integer()
    |> Integer.to_string()
    |> group_digits()
  end

  def cost(result) when is_map(result) do
    case cost_value(result) do
      nil -> nil
      value -> if(cache_served?(result), do: "🗄️" <> value, else: value)
    end
  end

  def cost(_result), do: nil

  def cost_title(result) do
    case cost_value(result) do
      nil ->
        nil

      value ->
        "#{if(cache_served?(result), do: "Cached trace-attributed cost", else: "Trace-attributed cost")} #{value}"
    end
  end

  def cache_served?(result) when is_map(result) do
    cache = if is_map(result["cache"]), do: result["cache"], else: %{}
    truthy?(cache["served"] || cache["servedFromCache"])
  end

  def cache_served?(_result), do: false

  def cache_status(result) when is_map(result) do
    cache = if is_map(result["cache"]), do: result["cache"], else: %{}
    mode = text([cache["mode"]])
    status = text([cache["status"]])

    cond do
      cache_served?(result) or status == "hit" -> "hit"
      mode == "off" or status in [nil, "", "disabled", "skipped"] -> "disabled"
      status == "miss" -> "miss"
      status == "refresh" -> "refresh"
      truthy?(cache["written"]) -> "written"
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
    truthy?(result["usedRepair"]) or
      Enum.any?(raw_attempts(result), fn attempt ->
        is_map(attempt) and (truthy?(attempt["repair"]) or truthy?(attempt["usedRepair"]))
      end)
  end

  def used_repair?(_result), do: false

  def attempts(result) when is_map(result) do
    result
    |> raw_attempts()
    |> Enum.with_index(1)
    |> Enum.map(fn {attempt, index} -> normalize_attempt(attempt, index, result) end)
  end

  def attempts(_result), do: []

  defp success?(result) do
    category = category(result)
    status = status_value(result)
    status_text = String.downcase(to_string(result["status"] || ""))

    category in [nil, "", "success", "ok"] and
      status_text not in ["failed", "failure", "error", "timeout"] and
      (is_nil(status) or status < 400)
  end

  defp error_category(result), do: category(result) || "error"

  defp status_label(result) do
    label = if(success?(result), do: "Success", else: title_case(error_category(result)))
    status = status_value(result) || if(success?(result), do: 200, else: "")
    if status == "", do: label, else: "#{label} (#{status})"
  end

  defp category(result) do
    attempt = List.last(raw_attempts(result)) || %{}
    status = String.downcase(to_string(result["status"] || ""))

    text([
      result["lastErrorCategory"],
      result["category"],
      result["outcome"],
      attempt["category"],
      attempt["outcome"],
      if(status == "timeout", do: "timeout"),
      if(status in ["failed", "failure", "error"], do: "error")
    ])
  end

  defp status_value(result) do
    attempt = List.last(raw_attempts(result)) || %{}

    numeric(
      first_present([
        result["lastErrorStatus"],
        result["httpStatus"],
        result["statusCode"],
        attempt["httpStatus"],
        attempt["statusCode"]
      ])
    )
  end

  defp normalize_attempt(attempt, index, result) when is_map(attempt) do
    category = text([attempt["category"], attempt["outcome"]]) || category(result) || "success"

    attempt_number =
      positive_integer(first_present([attempt["number"], attempt["attempt"]])) || index

    status =
      case Enum.find(
             ["httpStatus", "status", "statusCode"],
             &(Map.has_key?(attempt, &1) and attempt[&1] != "")
           ) do
        nil -> status_value(result)
        key -> numeric(attempt[key])
      end

    %{
      "attempt" => attempt_number,
      "category" => category,
      "statusCode" => if(is_nil(status) and category in ["success", "ok"], do: 200, else: status),
      "retryable" => truthy?(attempt["retryable"]),
      "delayMs" => milliseconds(attempt["delayMs"] || attempt["waitMs"], attempt["wait"]),
      "durationMs" => milliseconds(attempt["durationMs"], attempt["duration"])
    }
  end

  defp normalize_attempt(_attempt, index, result), do: normalize_attempt(%{}, index, result)

  defp raw_attempts(result) do
    if is_list(result["attempts"]), do: result["attempts"], else: []
  end

  defp milliseconds(value, nanoseconds) do
    case numeric(value) do
      number when is_number(number) -> trunc(number)
      _ -> trunc((numeric(nanoseconds) || 0) / 1_000_000)
    end
  end

  defp cost_value(result) do
    cost = if is_map(result["cost"]), do: result["cost"], else: %{}

    if cost["known"] == false do
      nil
    else
      case numeric(cost["totalUsd"]) do
        nil -> nil
        value -> "$" <> :erlang.float_to_binary(value * 1.0, decimals: 4)
      end
    end
  end

  defp cache_written?(result), do: truthy?(get_in(result, ["cache", "written"]))

  defp local_resource(payload, _message) when is_map(payload),
    do: %{"available" => true, "payload" => payload}

  defp local_resource(_payload, message), do: %{"available" => false, "message" => message}

  defp normalize_resource(resource, message) when is_map(resource) do
    if resource["available"] == true,
      do: resource,
      else: %{"available" => false, "message" => text([resource["message"]]) || message}
  end

  defp normalize_resource(_resource, message), do: %{"available" => false, "message" => message}

  defp resource_curl(%{"available" => true, "payload" => payload}, origin),
    do: curl(payload, origin)

  defp resource_curl(_resource, _origin), do: nil

  defp artifact_links(result, id, artifact_url)
       when is_binary(id) and is_function(artifact_url, 2) do
    result
    |> Map.get("artifacts", [])
    |> then(&if(is_list(&1), do: &1, else: []))
    |> Enum.filter(&(is_map(&1) and present?(text([&1["artifactId"]]))))
    |> Enum.map(fn artifact ->
      artifact_id = artifact["artifactId"]
      kind = text([artifact["kind"]]) || "artifact"
      size = integer(artifact["sizeBytes"])
      %{"href" => artifact_url.(id, artifact_id), "label" => "#{kind} · #{size} bytes"}
    end)
  end

  defp artifact_links(_result, _id, _artifact_url), do: []

  defp history_duration_ms(item) do
    with {:ok, started, _} <- DateTime.from_iso8601(item["startedAt"] || ""),
         {:ok, completed, _} <- DateTime.from_iso8601(item["completedAt"] || "") do
      DateTime.diff(completed, started, :millisecond)
    else
      _ -> nil
    end
  end

  defp shell_quote(value), do: "'" <> String.replace(value, "'", "'\"'\"'") <> "'"

  defp first_present(values), do: Enum.find(values, &(not is_nil(&1) and &1 != ""))

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

  defp positive_integer(value) do
    case numeric(value) do
      number when is_number(number) and number > 0 -> trunc(number)
      _ -> nil
    end
  end

  defp integer(value), do: trunc(numeric(value) || 0)

  defp numeric(nil), do: nil
  defp numeric(value) when is_integer(value) or is_float(value), do: value

  defp numeric(value) when is_binary(value) do
    case Integer.parse(String.trim(value)) do
      {number, ""} ->
        number

      _ ->
        case Float.parse(String.trim(value)) do
          {number, ""} -> number
          _ -> nil
        end
    end
  end

  defp numeric(_value), do: nil

  defp group_digits(value) do
    {sign, digits} =
      if String.starts_with?(value, "-"),
        do: {"-", String.slice(value, 1..-1//1)},
        else: {"", value}

    grouped =
      digits
      |> String.reverse()
      |> String.graphemes()
      |> Enum.chunk_every(3)
      |> Enum.map(&Enum.join/1)
      |> Enum.join(",")
      |> String.reverse()

    sign <> grouped
  end

  defp present?(value) when is_binary(value), do: String.trim(value) != ""
  defp present?(value), do: not is_nil(value)

  defp truthy?(value), do: value in [true, "true", 1, "1", "on"]
end
