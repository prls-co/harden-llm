defmodule HardenLlm.LlmDiagnosticsWire do
  @moduledoc """
  Strict, operation-specific decoding for execution diagnostics at the REST boundary.

  Current run and stats responses must use schema v2. History and trace reads also
  accept the bounded retained v1 shape so old rows remain inspectable without
  inventing identity, usage, or cost facts that were never captured.
  """

  @run_keys ~w(schemaVersion runId status output callId traceId selectedTarget resultSource accounting attempts cache artifacts providerInvoked totalCallDurationMs totalWaitMs overBudgetMs usedRepair)
  @legacy_run_keys ~w(schemaVersion runId profileId modelId provider apiInferenceType providerBaseUrl callId traceId output usage cost attempts cache artifacts totalCallDurationMs totalWaitMs overBudgetMs usedRepair status category statusCode lastErrorCategory lastErrorStatus httpStatus outcome totalAttempts)
  @attempt_keys ~w(number retryLocalNumber profileId target category httpStatus code type providerRequestId retryable wait duration repair backupIndex providerUsed)
  @legacy_attempt_keys ~w(number attempt profileId category outcome httpStatus status statusCode code type providerRequestId retryable delayMs waitMs wait durationMs duration repair usedRepair backupIndex providerUsed)
  @target_keys ~w(profileId provider protocol endpoint modelId)
  @usage_keys ~w(inputTokens cacheReadTokens cacheCreationTokens outputTokens reasoningTokens promptTokens completionTokens totalTokens status)
  @cost_keys ~w(knownSubtotalUsd status source knownObservations unknownObservations)
  @cache_keys ~w(mode status operationHash version served written)
  @artifact_keys ~w(artifactId kind sha256 sizeBytes contentType)

  def decode("run", value), do: decode_run(value, false)
  def decode("getStats", value), do: decode_stats(value)
  def decode("getTrace", value), do: decode_trace(value)
  def decode("listHistory", value), do: decode_history(value)
  def decode(_operation, value), do: {:ok, value}

  def decode_run(value, allow_legacy? \\ false)

  def decode_run(%{"schemaVersion" => 2} = value, _allow_legacy?) do
    with :ok <- exact_keys(value, @run_keys),
         :ok <- enum(value["status"], ~w(succeeded failed timeout)),
         :ok <- identifier(value["runId"]),
         :ok <- identifier(value["callId"]),
         :ok <- identifier(value["traceId"]),
         :ok <- target(value["selectedTarget"]),
         :ok <- result_source(value["resultSource"]),
         :ok <- accounting(value["accounting"]),
         :ok <- attempts(value["attempts"]),
         :ok <- cache(value["cache"]),
         :ok <- artifacts(value["artifacts"]),
         :ok <- boolean(value["providerInvoked"]),
         :ok <- nonnegative_integer(value["totalCallDurationMs"]),
         :ok <- nonnegative_integer(value["totalWaitMs"]),
         :ok <- nonnegative_integer(value["overBudgetMs"]),
         :ok <- boolean(value["usedRepair"]),
         :ok <- execution_invariants(value) do
      {:ok, value}
    else
      _ -> malformed()
    end
  end

  def decode_run(value, true) when is_map(value) do
    with :ok <- subset_keys(value, @legacy_run_keys),
         :ok <- optional(value, "schemaVersion", &enum(&1, [1])),
         :ok <- optional(value, "runId", &identifier/1),
         :ok <- optional(value, "callId", &identifier/1),
         :ok <- optional(value, "traceId", &identifier/1),
         :ok <-
           optional(
             value,
             "status",
             &enum(&1, ~w(succeeded failed timeout success failed failure error))
           ),
         :ok <-
           optional_texts(
             value,
             ~w(profileId modelId provider apiInferenceType providerBaseUrl category lastErrorCategory outcome)
           ),
         :ok <-
           optional_nonnegative_integers(
             value,
             ~w(statusCode lastErrorStatus httpStatus totalAttempts totalCallDurationMs totalWaitMs overBudgetMs)
           ),
         :ok <- optional(value, "usedRepair", &boolean/1),
         :ok <- optional(value, "usage", &legacy_usage/1),
         :ok <- optional(value, "cost", &legacy_cost/1),
         :ok <- optional(value, "attempts", &legacy_attempts/1),
         :ok <- optional(value, "cache", &cache/1),
         :ok <- optional(value, "artifacts", &artifacts/1) do
      {:ok, value}
    else
      _ -> malformed()
    end
  end

  def decode_run(_value, _allow_legacy?), do: malformed()

  def decode_stats(value) when is_map(value) do
    keys =
      ~w(schemaVersion totalCount successCount failureCount timeoutCount resultAccounting providerAccounting cached totalCallDurationMs maxCallDurationMs overBudgetCount maxOverBudgetMs)

    with :ok <- exact_keys(value, keys),
         :ok <- enum(value["schemaVersion"], [2]),
         :ok <-
           nonnegative_integers(
             value,
             ~w(totalCount successCount failureCount timeoutCount totalCallDurationMs maxCallDurationMs overBudgetCount maxOverBudgetMs)
           ),
         :ok <- accounting_stats(value["resultAccounting"], value["totalCount"]),
         :ok <- accounting_stats(value["providerAccounting"], value["totalCount"]),
         :ok <- cached_stats(value["cached"], value["totalCount"]),
         true <-
           value["successCount"] + value["failureCount"] + value["timeoutCount"] ==
             value["totalCount"] do
      {:ok, value}
    else
      _ -> malformed()
    end
  end

  def decode_stats(_value), do: malformed()

  def decode_history(%{"items" => items} = value) when is_list(items) and length(items) <= 100 do
    with :ok <- subset_keys(value, ~w(items nextCursor)),
         :ok <- optional(value, "nextCursor", &nullable_cursor/1),
         :ok <- each(items, &history_item/1) do
      {:ok, value}
    else
      _ -> malformed()
    end
  end

  def decode_history(_value), do: malformed()

  def decode_trace(
        %{
          "traceId" => trace_id,
          "record" => record,
          "observations" => observations,
          "artifacts" => artifacts,
          "resources" => resources
        } = value
      ) do
    with :ok <- exact_keys(value, ~w(traceId record observations artifacts resources)),
         :ok <- identifier(trace_id),
         {:ok, _record} <- decode_run(record, true),
         true <- record["traceId"] in [nil, trace_id],
         :ok <- trace_observations(observations),
         :ok <- trace_artifacts(artifacts),
         :ok <- trace_resources(resources) do
      {:ok, value}
    else
      _ -> malformed()
    end
  end

  def decode_trace(_value), do: malformed()

  defp history_item(value) when is_map(value) do
    with :ok <-
           exact_keys(
             value,
             ~w(runId profileId traceId status request result startedAt completedAt)
           ),
         :ok <- identifier(value["runId"]),
         :ok <- nonempty_text(value["profileId"]),
         :ok <- identifier(value["traceId"]),
         :ok <- enum(value["status"], ~w(succeeded failed timeout)),
         true <- is_map(value["request"]),
         {:ok, _result} <- decode_run(value["result"], true),
         true <- value["result"]["runId"] in [nil, value["runId"]],
         true <- value["result"]["traceId"] in [nil, value["traceId"]],
         true <-
           value["result"]["schemaVersion"] != 2 or
             get_in(value, ["result", "selectedTarget", "profileId"]) == value["profileId"],
         :ok <- iso8601(value["startedAt"]),
         :ok <- iso8601(value["completedAt"]) do
      :ok
    else
      _ -> :error
    end
  end

  defp history_item(_value), do: :error

  defp target(value) when is_map(value) do
    with :ok <- exact_keys(value, @target_keys),
         :ok <- nonempty_text(value["profileId"]),
         :ok <- nonempty_text(value["provider"]),
         :ok <- nonempty_text(value["protocol"]),
         :ok <- https_url(value["endpoint"]),
         :ok <- nonempty_text(value["modelId"]) do
      :ok
    end
  end

  defp target(_value), do: :error

  defp result_source(%{"kind" => "none"} = value), do: exact_keys(value, ~w(kind))

  defp result_source(%{"kind" => "provider"} = value) do
    with :ok <- exact_keys(value, ~w(kind attemptNumber producer)),
         :ok <- positive_integer(value["attemptNumber"]),
         :ok <- target(value["producer"]) do
      :ok
    end
  end

  defp result_source(%{"kind" => "cache"} = value) do
    with :ok <- exact_keys(value, ~w(kind producer)), :ok <- target(value["producer"]), do: :ok
  end

  defp result_source(_value), do: :error

  defp accounting(%{"result" => result, "provider" => provider} = value) do
    with :ok <- exact_keys(value, ~w(result provider)),
         :ok <- ledger(result),
         :ok <- ledger(provider) do
      :ok
    end
  end

  defp accounting(_value), do: :error

  defp ledger(%{"usage" => usage, "cost" => cost} = value) do
    with :ok <- exact_keys(value, ~w(usage cost)), :ok <- usage(usage), :ok <- cost(cost), do: :ok
  end

  defp ledger(_value), do: :error

  defp usage(value) when is_map(value) do
    with :ok <- exact_keys(value, @usage_keys),
         :ok <-
           nonnegative_integers(
             value,
             ~w(inputTokens cacheReadTokens cacheCreationTokens outputTokens reasoningTokens promptTokens completionTokens totalTokens)
           ),
         :ok <- enum(value["status"], ~w(complete partial unavailable inconsistent)),
         true <-
           value["promptTokens"] ==
             value["inputTokens"] + value["cacheReadTokens"] + value["cacheCreationTokens"],
         true <- value["completionTokens"] == value["outputTokens"] + value["reasoningTokens"],
         true <- value["totalTokens"] == value["promptTokens"] + value["completionTokens"],
         true <- value["status"] != "unavailable" or value["totalTokens"] == 0 do
      :ok
    else
      _ -> :error
    end
  end

  defp usage(_value), do: :error

  defp cost(value) when is_map(value) do
    with :ok <- exact_keys(value, @cost_keys),
         :ok <- nonnegative_number(value["knownSubtotalUsd"]),
         :ok <- enum(value["status"], ~w(exact partial unknown unavailable)),
         true <- is_binary(value["source"]),
         :ok <- nonnegative_integer(value["knownObservations"]),
         :ok <- nonnegative_integer(value["unknownObservations"]),
         :ok <- cost_invariants(value) do
      :ok
    else
      _ -> :error
    end
  end

  defp cost(_value), do: :error

  defp cost_invariants(%{
         "status" => "exact",
         "knownObservations" => known,
         "unknownObservations" => 0
       })
       when known > 0, do: :ok

  defp cost_invariants(%{
         "status" => "partial",
         "knownObservations" => known,
         "unknownObservations" => unknown
       })
       when known > 0 and unknown > 0, do: :ok

  defp cost_invariants(%{
         "status" => "unknown",
         "knownSubtotalUsd" => subtotal,
         "knownObservations" => 0,
         "unknownObservations" => unknown
       })
       when subtotal == 0 and unknown > 0, do: :ok

  defp cost_invariants(%{
         "status" => "unavailable",
         "knownSubtotalUsd" => subtotal,
         "knownObservations" => 0,
         "unknownObservations" => 0
       })
       when subtotal == 0,
       do: :ok

  defp cost_invariants(_value), do: :error

  defp attempts(values) when is_list(values) and length(values) <= 10 do
    values
    |> Enum.with_index(1)
    |> each(fn {value, expected} -> attempt(value, expected) end)
  end

  defp attempts(_values), do: :error

  defp attempt(value, expected) when is_map(value) do
    with :ok <- subset_keys(value, @attempt_keys),
         :ok <-
           required_keys(
             value,
             ~w(number retryLocalNumber profileId target retryable wait duration repair backupIndex providerUsed)
           ),
         true <- value["number"] == expected,
         :ok <- positive_integer(value["retryLocalNumber"]),
         :ok <- nonempty_text(value["profileId"]),
         :ok <- target(value["target"]),
         :ok <- optional_texts(value, ~w(category code type providerRequestId)),
         :ok <- optional(value, "httpStatus", &http_status/1),
         :ok <- booleans(value, ~w(retryable repair providerUsed)),
         :ok <- nonnegative_integers(value, ~w(wait duration backupIndex)) do
      :ok
    else
      _ -> :error
    end
  end

  defp attempt(_value, _expected), do: :error

  defp cache(value) when is_map(value) do
    with :ok <- subset_keys(value, @cache_keys),
         :ok <- required_keys(value, ~w(mode status served written)),
         :ok <- enum(value["mode"], ~w(off cache refresh)),
         :ok <- nonempty_text(value["status"]),
         :ok <- booleans(value, ~w(served written)),
         :ok <- optional_texts(value, ~w(operationHash version)) do
      :ok
    else
      _ -> :error
    end
  end

  defp cache(_value), do: :error

  defp artifacts(values) when is_list(values), do: each(values, &run_artifact/1)
  defp artifacts(_values), do: :error

  defp run_artifact(value) when is_map(value) do
    with :ok <- exact_keys(value, @artifact_keys),
         :ok <- identifier(value["artifactId"]),
         :ok <- enum(value["kind"], ~w(trace parse-failure-response diagnostic-event)),
         true <- is_binary(value["sha256"]) and Regex.match?(~r/^[0-9a-f]{64}$/, value["sha256"]),
         :ok <- positive_integer(value["sizeBytes"]),
         :ok <- enum(value["contentType"], ["application/json"]) do
      :ok
    else
      _ -> :error
    end
  end

  defp run_artifact(_value), do: :error

  defp execution_invariants(value) do
    attempts = value["attempts"]
    provider_invoked? = Enum.any?(attempts, &(&1["providerUsed"] == true))

    with true <- provider_invoked? == value["providerInvoked"],
         :ok <- source_invariants(value["resultSource"], attempts),
         true <- value["cache"]["served"] == (value["resultSource"]["kind"] == "cache") do
      :ok
    else
      _ -> :error
    end
  end

  defp source_invariants(
         %{"kind" => "provider", "attemptNumber" => number, "producer" => producer},
         attempts
       ) do
    case Enum.find(attempts, &(&1["number"] == number)) do
      %{"providerUsed" => true, "target" => ^producer} -> :ok
      _ -> :error
    end
  end

  defp source_invariants(%{"kind" => "cache"}, _attempts), do: :ok
  defp source_invariants(%{"kind" => "none"}, _attempts), do: :ok
  defp source_invariants(_source, _attempts), do: :error

  defp accounting_stats(%{"usage" => usage, "cost" => cost} = value, total_count) do
    with :ok <- exact_keys(value, ~w(usage cost)),
         :ok <- usage_stats(usage, total_count),
         :ok <- cost_stats(cost, total_count) do
      :ok
    end
  end

  defp accounting_stats(_value, _total_count), do: :error

  defp usage_stats(%{"coverage" => coverage} = value, total_count) do
    with :ok <-
           exact_keys(
             value,
             ~w(promptTokens cacheReadTokens cacheCreationTokens outputTokens reasoningTokens totalTokens coverage)
           ),
         :ok <-
           nonnegative_integers(
             value,
             ~w(promptTokens cacheReadTokens cacheCreationTokens outputTokens reasoningTokens totalTokens)
           ),
         true <-
           value["totalTokens"] ==
             value["promptTokens"] + value["outputTokens"] + value["reasoningTokens"],
         :ok <- coverage(coverage, ~w(complete partial unavailable inconsistent), total_count) do
      :ok
    else
      _ -> :error
    end
  end

  defp usage_stats(_value, _total_count), do: :error

  defp cost_stats(%{"knownSubtotalUsd" => subtotal, "coverage" => coverage} = value, total_count) do
    with :ok <- exact_keys(value, ~w(knownSubtotalUsd coverage)),
         :ok <- nonnegative_number(subtotal),
         :ok <- coverage(coverage, ~w(exact partial unknown unavailable), total_count) do
      :ok
    end
  end

  defp cost_stats(_value, _total_count), do: :error

  defp cached_stats(%{"count" => count, "cost" => cost} = value, total_count) do
    with :ok <- exact_keys(value, ~w(count cost)),
         :ok <- nonnegative_integer(count),
         true <- count <= total_count,
         :ok <- cost_stats(cost, count) do
      :ok
    else
      _ -> :error
    end
  end

  defp cached_stats(_value, _total_count), do: :error

  defp coverage(value, keys, total_count) when is_map(value) do
    with :ok <- exact_keys(value, keys),
         :ok <- nonnegative_integers(value, keys),
         true <- Enum.sum(Enum.map(keys, &value[&1])) == total_count do
      :ok
    else
      _ -> :error
    end
  end

  defp coverage(_value, _keys, _total_count), do: :error

  defp legacy_usage(value) when is_map(value) do
    keys =
      ~w(inputTokens cacheReadTokens cacheCreationTokens outputTokens reasoningTokens totalTokens)

    with :ok <- subset_keys(value, keys),
         :ok <- optional_nonnegative_integers(value, keys) do
      :ok
    end
  end

  defp legacy_usage(_value), do: :error

  defp legacy_cost(value) when is_map(value) do
    with :ok <- subset_keys(value, ~w(totalUsd known source)),
         :ok <- optional(value, "totalUsd", &nonnegative_number/1),
         :ok <- optional(value, "known", &boolean/1),
         :ok <-
           optional(value, "source", fn candidate ->
             if is_binary(candidate), do: :ok, else: :error
           end) do
      :ok
    end
  end

  defp legacy_cost(_value), do: :error

  defp legacy_attempts(values) when is_list(values) and length(values) <= 10 do
    each(values, fn value ->
      with true <- is_map(value),
           :ok <- subset_keys(value, @legacy_attempt_keys),
           :ok <-
             optional_texts(value, ~w(profileId category outcome code type providerRequestId)),
           :ok <-
             optional_nonnegative_integers(
               value,
               ~w(number attempt httpStatus status statusCode delayMs waitMs wait durationMs duration backupIndex)
             ),
           :ok <- optional_booleans(value, ~w(retryable repair usedRepair providerUsed)) do
        :ok
      else
        _ -> :error
      end
    end)
  end

  defp legacy_attempts(_values), do: :error

  defp trace_observations(values) when is_list(values) do
    values
    |> Enum.with_index()
    |> each(fn {value, expected} ->
      with true <- is_map(value),
           :ok <- exact_keys(value, ~w(sequence type data createdAt)),
           true <- value["sequence"] == expected,
           :ok <- nonempty_text(value["type"]),
           true <- is_map(value["data"]),
           :ok <- iso8601(value["createdAt"]) do
        :ok
      else
        _ -> :error
      end
    end)
  end

  defp trace_observations(_values), do: :error

  defp trace_artifacts(values) when is_list(values) do
    each(values, fn value ->
      with true <- is_map(value),
           :ok <- exact_keys(value, @artifact_keys ++ ["createdAt"]),
           :ok <- run_artifact(Map.delete(value, "createdAt")),
           :ok <- iso8601(value["createdAt"]) do
        :ok
      else
        _ -> :error
      end
    end)
  end

  defp trace_artifacts(_values), do: :error

  defp trace_resources(%{"request" => request, "response" => response} = value) do
    with :ok <- exact_keys(value, ~w(request response)),
         :ok <- trace_resource(request),
         :ok <- trace_resource(response) do
      :ok
    end
  end

  defp trace_resources(_value), do: :error

  defp trace_resource(%{"available" => true, "payload" => payload} = value) when is_map(payload),
    do: exact_keys(value, ~w(available payload))

  defp trace_resource(%{"available" => false, "message" => message} = value) do
    with :ok <- exact_keys(value, ~w(available message)), :ok <- nonempty_text(message), do: :ok
  end

  defp trace_resource(_value), do: :error

  defp exact_keys(value, keys) when is_map(value) do
    if MapSet.new(Map.keys(value)) == MapSet.new(keys), do: :ok, else: :error
  end

  defp subset_keys(value, keys) when is_map(value) do
    if MapSet.subset?(MapSet.new(Map.keys(value)), MapSet.new(keys)), do: :ok, else: :error
  end

  defp required_keys(value, keys) when is_map(value) do
    if Enum.all?(keys, &Map.has_key?(value, &1)), do: :ok, else: :error
  end

  defp optional(value, key, validator) do
    if Map.has_key?(value, key), do: validator.(value[key]), else: :ok
  end

  defp optional_texts(value, keys),
    do:
      each(
        keys,
        &optional(value, &1, fn candidate -> if is_binary(candidate), do: :ok, else: :error end)
      )

  defp optional_booleans(value, keys),
    do: each(keys, &optional(value, &1, fn candidate -> boolean(candidate) end))

  defp optional_nonnegative_integers(value, keys),
    do: each(keys, &optional(value, &1, fn candidate -> nonnegative_integer(candidate) end))

  defp nonnegative_integers(value, keys), do: each(keys, &nonnegative_integer(value[&1]))
  defp booleans(value, keys), do: each(keys, &boolean(value[&1]))

  defp each(values, validator) do
    if Enum.all?(values, &(validator.(&1) == :ok)), do: :ok, else: :error
  end

  defp identifier(value) when is_binary(value) and byte_size(value) in 1..256, do: :ok
  defp identifier(_value), do: :error
  defp nullable_cursor(nil), do: :ok
  defp nullable_cursor(value), do: nonempty_text(value)
  defp nonempty_text(value) when is_binary(value) and byte_size(value) in 1..2048, do: :ok
  defp nonempty_text(_value), do: :error
  defp boolean(value) when is_boolean(value), do: :ok
  defp boolean(_value), do: :error
  defp nonnegative_integer(value) when is_integer(value) and value >= 0, do: :ok
  defp nonnegative_integer(_value), do: :error
  defp positive_integer(value) when is_integer(value) and value > 0, do: :ok
  defp positive_integer(_value), do: :error
  defp nonnegative_number(value) when is_integer(value) and value >= 0, do: :ok
  defp nonnegative_number(value) when is_float(value) and value >= 0, do: :ok
  defp nonnegative_number(_value), do: :error
  defp http_status(value) when is_integer(value) and value in 100..599, do: :ok
  defp http_status(_value), do: :error

  defp enum(value, values), do: if(value in values, do: :ok, else: :error)

  defp https_url(value) when is_binary(value) do
    case URI.parse(value) do
      %URI{scheme: "https", host: host} when is_binary(host) and host != "" -> :ok
      _ -> :error
    end
  end

  defp https_url(_value), do: :error

  defp iso8601(value) when is_binary(value) do
    case DateTime.from_iso8601(value) do
      {:ok, _datetime, _offset} -> :ok
      _ -> :error
    end
  end

  defp iso8601(_value), do: :error
  defp malformed, do: {:error, :malformed_diagnostics}
end
