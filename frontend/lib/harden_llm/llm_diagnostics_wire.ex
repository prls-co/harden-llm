defmodule HardenLlm.LlmDiagnosticsWire do
  @moduledoc """
  Strict, operation-specific decoding for execution diagnostics at the REST boundary.

  Run, history and trace records use schema v4; stats retain schema v2.
  Retired execution formats are rejected at this boundary before projection.
  """

  @run_keys ~w(schemaVersion runId status output callId traceId origin selectedTarget resultSource accounting attempts cache artifacts providerInvoked totalCallDurationMs totalWaitMs totalActualWaitMs overBudgetMs usedRepair generationTarget stopReason diagnostics)
  @run_required_keys ~w(schemaVersion runId status output callId traceId selectedTarget resultSource accounting attempts cache artifacts providerInvoked totalCallDurationMs totalWaitMs totalActualWaitMs overBudgetMs usedRepair)
  @attempt_keys ~w(number profileId target category httpStatus code type providerRequestId retryable wait duration repair providerUsed stage branch triggerAttemptNumber inputAttemptNumbers transportRetryOfAttempt startedAt finishedAt reasoningEffort dispatchObserved stream waitDiagnostics)
  @target_keys ~w(profileId provider protocol endpoint modelId)
  @usage_keys ~w(inputTokens cacheReadTokens cacheCreationTokens outputTokens reasoningTokens promptTokens completionTokens totalTokens status)
  @cost_keys ~w(knownSubtotalUsd status source knownObservations unknownObservations)
  @cache_keys ~w(mode status operationHash originalOperationHash rerunOperationHash version served written)
  @artifact_keys ~w(artifactId kind state sha256 sizeBytes contentType)
  @maximum_int64 9_223_372_036_854_775_807

  def decode("run", value), do: decode_run(value)
  def decode("getStats", value), do: decode_stats(value)
  def decode("getTrace", value), do: decode_trace(value)
  def decode("listHistory", value), do: decode_history(value)

  def decode(operation, value) when operation in ["runProgress", "runStream"],
    do: decode_progress(value)

  def decode(operation, value) when operation in ["listProfiles", "importProfileBundle"],
    do: decode_profiles(value)

  def decode(operation, value) when operation in ["saveProfile", "refreshProfileModels"] do
    case profile_state(value) do
      :ok -> {:ok, value}
      _ -> malformed()
    end
  end

  def decode(_operation, value), do: {:ok, value}

  @doc "Strictly decodes one request-bound REST SSE envelope."
  def decode_progress(value) when is_map(value) do
    with :ok <- subset_keys(value, ~w(schemaVersion sequence runId callId traceId type data)),
         :ok <- required_keys(value, ~w(schemaVersion sequence type data)),
         :ok <- enum(value["schemaVersion"], [1]),
         :ok <- positive_int64(value["sequence"]),
         :ok <- optional(value, "runId", &identifier/1),
         :ok <- optional(value, "callId", &identifier/1),
         :ok <- optional(value, "traceId", &identifier/1),
         :ok <- enum(value["type"], ~w(run.started run.progress run.completed run.failed)),
         :ok <- progress_data(value["type"], value["data"]) do
      {:ok, value}
    else
      _ -> malformed()
    end
  end

  def decode_progress(_value), do: malformed()

  defp progress_data(type, value) when type in ["run.started", "run.progress"],
    do: progress_snapshot(value)

  defp progress_data(_type, %{"state" => state, "result" => result, "error" => error} = value) do
    with :ok <- exact_keys(value, ~w(state result error)),
         true <- is_map(state),
         :ok <- decode_terminal_result(result),
         :ok <- terminal_error(error) do
      :ok
    else
      _ -> :error
    end
  end

  defp progress_data(_type, _value), do: :error

  defp progress_snapshot(value) when is_map(value) do
    keys =
      ~w(schemaVersion sequence runId callId traceId type stage branch profileId reasoningEffort attempt attemptsUsed attemptsRemaining elapsedMs deadlineRemainingMs receivedBytes eventCount outputBytes outputCodePoints lastActivity stopReason terminal maxAttempts effectiveTimeoutMs origin attempts accounting)

    with :ok <- subset_keys(value, keys),
         :ok <-
           required_keys(
             value,
             ~w(schemaVersion sequence callId traceId type attemptsUsed attemptsRemaining elapsedMs terminal)
           ),
         :ok <- enum(value["schemaVersion"], [1]),
         :ok <- positive_int64(value["sequence"]),
         :ok <- optional(value, "runId", &identifier/1),
         :ok <- identifier(value["callId"]),
         :ok <- identifier(value["traceId"]),
         :ok <- enum(value["type"], ~w(run.started run.progress run.terminal)),
         :ok <-
           optional_texts(
             value,
             ~w(stage branch profileId reasoningEffort stopReason lastActivity)
           ),
         :ok <- optional(value, "branch", &enum(&1, ~w(original rerun))),
         :ok <- optional(value, "attempt", &positive_integer/1),
         :ok <-
           nonnegative_integers(
             value,
             ~w(attemptsUsed attemptsRemaining elapsedMs receivedBytes eventCount outputBytes outputCodePoints)
           ),
         :ok <- optional(value, "deadlineRemainingMs", &nonnegative_integer/1),
         :ok <- optional(value, "maxAttempts", &positive_integer/1),
         :ok <- optional(value, "effectiveTimeoutMs", &nonnegative_integer/1),
         :ok <- optional(value, "origin", &origin/1),
         :ok <- optional(value, "attempts", &attempts/1),
         :ok <- optional(value, "accounting", &accounting/1),
         :ok <- boolean(value["terminal"]) do
      :ok
    else
      _ -> :error
    end
  end

  defp progress_snapshot(_value), do: :error

  defp decode_terminal_result(nil), do: :ok

  defp decode_terminal_result(value) when is_map(value) do
    case decode_run(value) do
      {:ok, _decoded} -> :ok
      _ -> :error
    end
  end

  defp decode_terminal_result(_value), do: :error

  defp terminal_error(nil), do: :ok

  defp terminal_error(%{"code" => code, "message" => message} = value)
       when is_binary(code) and is_binary(message),
       do: subset_keys(value, ~w(code message fieldErrors))

  defp terminal_error(_value), do: :error

  def decode("listHistory", value, :cursor), do: decode_cursor_history(value)
  def decode("listHistory", value, :numbered), do: decode_numbered_history(value)

  def decode_state(operation, value) when operation in ["getState", "saveState"] do
    with %{"schemaVersion" => version, "recoveryPolicy" => policy} <- value,
         true <- version in [2, 3],
         :ok <- recovery_policy(policy) do
      {:ok, value}
    else
      _ -> malformed()
    end
  end

  def decode_state(_operation, value), do: {:ok, value}

  @doc "Strictly decodes the small run identity state carried by run results."
  def decode_run_state(value) when is_map(value) do
    with :ok <- exact_keys(value, ~w(lastRunId lastTraceId)),
         :ok <- identifier(value["lastRunId"]),
         :ok <- identifier(value["lastTraceId"]) do
      {:ok, value}
    else
      _ -> malformed()
    end
  end

  def decode_run_state(_value), do: malformed()

  defp decode_profiles(%{"profiles" => profiles, "defaults" => defaults} = value)
       when is_list(profiles) and is_map(defaults) do
    with :ok <- exact_keys(value, ~w(profiles defaults)),
         :ok <- exact_keys(defaults, ~w(recoveryPolicy)),
         :ok <- recovery_policy(defaults["recoveryPolicy"]),
         :ok <- each(profiles, &profile_state/1) do
      {:ok, value}
    else
      _ -> malformed()
    end
  end

  defp decode_profiles(_value), do: malformed()

  defp profile_state(%{"profile" => %{"schemaVersion" => version, "recoveryPolicy" => policy}})
       when version in [2, 3],
       do: recovery_policy(policy)

  defp profile_state(_value), do: :error

  # Check the required wire shape only. Go owns policy semantics and defaults.
  defp recovery_policy(value) when is_map(value) do
    with :ok <- exact_keys(value, ~w(maxAttempts retryOn jsonRepair rerun backoff)),
         true <- is_integer(value["maxAttempts"]),
         categories when is_list(categories) <- value["retryOn"],
         true <- Enum.all?(categories, &is_binary/1),
         :ok <- json_repair(value["jsonRepair"]),
         :ok <- rerun(value["rerun"]),
         backoff when is_map(backoff) <- value["backoff"],
         :ok <- exact_keys(backoff, ~w(baseDelayMs maxDelayMs)),
         true <- is_integer(backoff["baseDelayMs"]),
         true <- is_integer(backoff["maxDelayMs"]) do
      :ok
    else
      _ -> :error
    end
  end

  defp recovery_policy(_value), do: :error

  defp json_repair(nil), do: :ok

  defp json_repair(value) when is_map(value) do
    with :ok <- exact_keys(value, ~w(initial escalation)),
         :ok <- recovery_target(value["initial"]),
         :ok <- optional(value, "escalation", &nullable_recovery_target/1) do
      :ok
    end
  end

  defp json_repair(_value), do: :error

  defp rerun(nil), do: :ok

  defp rerun(value) when is_map(value) do
    with :ok <- exact_keys(value, ~w(target jsonRepair)),
         :ok <- recovery_target(value["target"]),
         :ok <- optional(value, "jsonRepair", &json_repair/1) do
      :ok
    end
  end

  defp rerun(_value), do: :error

  defp nullable_recovery_target(nil), do: :ok
  defp nullable_recovery_target(value), do: recovery_target(value)

  defp recovery_target(value) when is_map(value) do
    case value["source"] do
      "profile" ->
        with :ok <-
               subset_keys(value, ~w(source profileId modelId reasoningEffort providerOptions)),
             :ok <- required_keys(value, ~w(source profileId)),
             :ok <- nonempty_text(value["profileId"]),
             :ok <- optional(value, "modelId", &nonempty_text/1),
             :ok <- optional(value, "reasoningEffort", &enum(&1, ~w(lowest middle highest))),
             :ok <-
               optional(value, "providerOptions", fn candidate ->
                 if is_map(candidate), do: :ok, else: :error
               end) do
          :ok
        end

      "generation" ->
        exact_keys(value, ["source"])

      _ ->
        :error
    end
  end

  defp recovery_target(_value), do: :error

  def decode_run(%{"schemaVersion" => 4} = value) do
    with :ok <- subset_keys(value, @run_keys ++ ~w(search)),
         :ok <- required_keys(value, @run_required_keys),
         :ok <- optional(value, "search", &search/1),
         :ok <- enum(value["status"], ~w(succeeded failed timeout)),
         :ok <- identifier(value["runId"]),
         :ok <- identifier(value["callId"]),
         :ok <- identifier(value["traceId"]),
         :ok <- target(value["selectedTarget"]),
         :ok <- optional(value, "origin", &origin/1),
         :ok <- result_source(value["resultSource"]),
         :ok <- accounting(value["accounting"]),
         :ok <- attempts(value["attempts"]),
         :ok <- cache(value["cache"]),
         :ok <- artifacts(value["artifacts"]),
         :ok <- boolean(value["providerInvoked"]),
         :ok <- nonnegative_integer(value["totalCallDurationMs"]),
         :ok <- nonnegative_integer(value["totalWaitMs"]),
         :ok <- optional(value, "totalActualWaitMs", &nullable_nonnegative_integer/1),
         :ok <- nonnegative_integer(value["overBudgetMs"]),
         :ok <- boolean(value["usedRepair"]),
         :ok <- optional(value, "generationTarget", &target/1),
         :ok <- optional_texts(value, ~w(stopReason)),
         :ok <- optional(value, "diagnostics", &diagnostics/1),
         :ok <- execution_invariants(value) do
      {:ok, value}
    else
      _ -> malformed()
    end
  end

  def decode_run(_value), do: malformed()

  defp diagnostics(value) when is_map(value) do
    with :ok <-
           subset_keys(
             value,
             ~w(stage branch stopReason attemptsUsed attemptsRemaining elapsedMs totalActualWaitMs receivedBytes eventCount outputBytes outputCodePoints effectiveTimeoutMs deadlineAt branchCaches)
           ),
         :ok <-
           required_keys(
             value,
             ~w(attemptsUsed attemptsRemaining elapsedMs totalActualWaitMs receivedBytes eventCount outputBytes outputCodePoints)
           ),
         :ok <- optional_texts(value, ~w(stage stopReason)),
         :ok <- optional(value, "branch", &enum(&1, ~w(original rerun))),
         :ok <- optional(value, "effectiveTimeoutMs", &nonnegative_integer/1),
         :ok <- optional(value, "deadlineAt", &iso8601/1),
         :ok <- optional(value, "branchCaches", &branch_caches/1),
         :ok <-
           nonnegative_integers(
             value,
             ~w(attemptsUsed attemptsRemaining elapsedMs totalActualWaitMs receivedBytes eventCount outputBytes outputCodePoints)
           ) do
      :ok
    else
      _ -> :error
    end
  end

  defp diagnostics(_value), do: :error

  defp branch_caches(values) when is_list(values) and length(values) <= 2 do
    each(values, fn value ->
      with :ok <- exact_keys(value, ~w(branch generationTarget cache)),
           :ok <- enum(value["branch"], ~w(original rerun)),
           :ok <- target(value["generationTarget"]),
           :ok <- cache(value["cache"]) do
        :ok
      end
    end)
  end

  defp branch_caches(_value), do: :error

  defp origin(value) when is_map(value) do
    keys = ~w(client component operationId parentRunId jobId testRunId testId sourceRevision)

    with :ok <- subset_keys(value, keys),
         :ok <- each(keys, fn key -> optional(value, key, &origin_text/1) end),
         true <- byte_size(Jason.encode!(value)) <= 2_048 do
      :ok
    else
      _ -> :error
    end
  end

  defp origin(_value), do: :error

  defp origin_text(value) when is_binary(value) and byte_size(value) <= 256, do: :ok
  defp origin_text(_value), do: :error

  # Optional OpenAPI WebSearchResult, shared by live runs, history and traces.
  # executed describes the original answer, including when served from cache.
  defp search(value) when is_map(value) do
    with :ok <- subset_keys(value, ~w(mode executed sources costStatus citations entryPointHtml)),
         :ok <- required_keys(value, ~w(mode executed sources costStatus)),
         :ok <- enum(value["mode"], ~w(native jina)),
         :ok <- boolean(value["executed"]),
         :ok <- enum(value["costStatus"], ~w(unavailable)),
         sources when is_list(sources) and length(sources) <= 50 <- value["sources"],
         :ok <- each(sources, &search_source/1),
         :ok <- optional(value, "citations", &search_citations/1),
         :ok <- optional(value, "entryPointHtml", &search_entry_point/1) do
      :ok
    else
      _ -> :error
    end
  end

  defp search(_value), do: :error

  defp search_source(value) when is_map(value) do
    with :ok <- exact_keys(value, ~w(url title)),
         :ok <- search_url(value["url"]),
         true <- is_binary(value["title"]),
         do: :ok,
         else: (_ -> :error)
  end

  defp search_source(_value), do: :error

  defp search_citations(values) when is_list(values), do: each(values, &search_citation/1)
  defp search_citations(_value), do: :error

  defp search_citation(value) when is_map(value) do
    with :ok <- exact_keys(value, ~w(url title startIndex endIndex)),
         :ok <- search_source(Map.take(value, ~w(url title))),
         :ok <- nonnegative_integer(value["startIndex"]),
         :ok <- positive_integer(value["endIndex"]),
         true <- value["endIndex"] > value["startIndex"],
         do: :ok,
         else: (_ -> :error)
  end

  defp search_citation(_value), do: :error

  defp search_url(value) when is_binary(value) do
    case URI.parse(value) do
      %URI{scheme: scheme, host: host, userinfo: nil}
      when scheme in ["http", "https"] and is_binary(host) and host != "" ->
        :ok

      _ ->
        :error
    end
  end

  defp search_url(_value), do: :error

  defp search_entry_point(value) when is_binary(value) do
    if length(String.codepoints(value)) <= 32_768, do: :ok, else: :error
  end

  defp search_entry_point(_value), do: :error

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

  def decode_history(%{"pagination" => _pagination} = value), do: decode_numbered_history(value)
  def decode_history(value), do: decode_cursor_history(value)

  defp decode_cursor_history(%{"items" => items} = value)
       when is_list(items) and length(items) <= 100 do
    with :ok <- subset_keys(value, ~w(items nextCursor)),
         :ok <- optional(value, "nextCursor", &nullable_cursor/1),
         :ok <- each(items, &history_item/1) do
      {:ok, value}
    else
      _ -> malformed()
    end
  end

  defp decode_cursor_history(_value), do: malformed()

  defp decode_numbered_history(%{"items" => items, "pagination" => pagination} = value)
       when is_list(items) and length(items) <= 100 and is_map(pagination) do
    with :ok <- exact_keys(value, ~w(items pagination)),
         :ok <- numbered_pagination(pagination),
         true <- exact_numbered_item_count?(items, pagination),
         :ok <- each(items, &history_item/1) do
      {:ok, value}
    else
      _ -> malformed()
    end
  end

  defp decode_numbered_history(_value), do: malformed()

  defp numbered_pagination(
         %{"page" => page, "pageSize" => page_size, "totalCount" => total_count} = value
       ) do
    with :ok <- exact_keys(value, ~w(page pageSize totalCount)),
         :ok <- positive_int64(page),
         :ok <- positive_integer(page_size),
         true <- page_size <= 100,
         :ok <- nonnegative_int64(total_count),
         total_pages <- div(total_count + page_size - 1, page_size),
         true <- page <= max(total_pages, 1) do
      :ok
    else
      _ -> :error
    end
  end

  defp numbered_pagination(_value), do: :error

  defp exact_numbered_item_count?(items, %{
         "page" => page,
         "pageSize" => page_size,
         "totalCount" => total_count
       }) do
    expected_count =
      case total_count do
        0 -> 0
        _ -> min(page_size, total_count - (page - 1) * page_size)
      end

    length(items) == expected_count
  end

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
         {:ok, _record} <- decode_run(record),
         true <- record["traceId"] == trace_id,
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
         {:ok, _result} <- decode_run(value["result"]),
         true <- value["result"]["runId"] == value["runId"],
         true <- value["result"]["traceId"] == value["traceId"],
         true <- get_in(value, ["result", "selectedTarget", "profileId"]) == value["profileId"],
         true <- value["result"]["status"] == value["status"],
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
             ~w(number profileId target retryable wait duration repair providerUsed)
           ),
         true <- value["number"] == expected,
         :ok <- nonempty_text(value["profileId"]),
         :ok <- target(value["target"]),
         :ok <- optional_texts(value, ~w(category code type providerRequestId)),
         :ok <- optional_texts(value, ~w(stage)),
         :ok <- optional(value, "branch", &enum(&1, ~w(original rerun))),
         :ok <- optional(value, "triggerAttemptNumber", &positive_integer/1),
         :ok <-
           optional(value, "inputAttemptNumbers", fn values ->
             if is_list(values), do: each(values, &positive_integer/1), else: :error
           end),
         :ok <- optional(value, "transportRetryOfAttempt", &positive_integer/1),
         :ok <- optional_texts(value, ~w(startedAt finishedAt reasoningEffort)),
         :ok <- optional(value, "dispatchObserved", &boolean/1),
         :ok <- optional(value, "stream", &stream/1),
         :ok <- optional(value, "waitDiagnostics", &wait_diagnostics/1),
         :ok <- optional(value, "httpStatus", &http_status/1),
         :ok <- booleans(value, ~w(retryable repair providerUsed)),
         :ok <- nonnegative_integers(value, ~w(wait duration)) do
      :ok
    else
      _ -> :error
    end
  end

  defp attempt(_value, _expected), do: :error

  defp stream(value) when is_map(value) do
    with :ok <-
           subset_keys(
             value,
             ~w(receivedBytes eventCount outputBytes outputCodePoints firstEventMs firstOutputMs lastEventAt lastOutputAt terminalState)
           ),
         :ok <- required_keys(value, ~w(receivedBytes eventCount outputBytes outputCodePoints)),
         :ok <-
           nonnegative_integers(value, ~w(receivedBytes eventCount outputBytes outputCodePoints)),
         :ok <- optional(value, "firstEventMs", &nonnegative_integer/1),
         :ok <- optional(value, "firstOutputMs", &nonnegative_integer/1),
         :ok <- optional(value, "lastEventAt", &iso8601/1),
         :ok <- optional(value, "lastOutputAt", &iso8601/1),
         :ok <-
           optional(
             value,
             "terminalState",
             &enum(&1, ~w(not_streaming awaiting_terminal completed incomplete failed missing))
           ) do
      :ok
    else
      _ -> :error
    end
  end

  defp stream(_value), do: :error

  defp wait_diagnostics(value) when is_map(value) do
    with :ok <- subset_keys(value, ~w(reason plannedMs actualMs retryAfterMs)),
         :ok <- required_keys(value, ~w(plannedMs actualMs)),
         :ok <- optional_texts(value, ~w(reason)),
         :ok <- nonnegative_integers(value, ~w(plannedMs actualMs)),
         :ok <- optional(value, "retryAfterMs", &nonnegative_integer/1) do
      :ok
    else
      _ -> :error
    end
  end

  defp wait_diagnostics(_value), do: :error

  defp cache(value) when is_map(value) do
    with :ok <- subset_keys(value, @cache_keys),
         :ok <- required_keys(value, ~w(mode status served written)),
         :ok <- enum(value["mode"], ~w(off cache refresh)),
         :ok <- nonempty_text(value["status"]),
         :ok <- booleans(value, ~w(served written)),
         :ok <-
           optional_texts(
             value,
             ~w(operationHash originalOperationHash rerunOperationHash version)
           ) do
      :ok
    else
      _ -> :error
    end
  end

  defp cache(_value), do: :error

  defp artifacts(values) when is_list(values) do
    with :ok <- each(values, &run_artifact/1),
         true <- Enum.all?(values, &(&1["state"] == "available")) do
      :ok
    else
      _ -> :error
    end
  end

  defp artifacts(_values), do: :error

  defp run_artifact(value) when is_map(value) do
    with :ok <- exact_keys(value, @artifact_keys),
         :ok <- identifier(value["artifactId"]),
         :ok <- enum(value["kind"], ~w(trace parse-failure-response diagnostic-event)),
         :ok <- enum(value["state"], ~w(available)),
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
  defp nullable_nonnegative_integer(nil), do: :ok
  defp nullable_nonnegative_integer(value), do: nonnegative_integer(value)
  defp positive_integer(value) when is_integer(value) and value > 0, do: :ok
  defp positive_integer(_value), do: :error
  defp nonnegative_int64(value) when is_integer(value) and value in 0..@maximum_int64, do: :ok
  defp nonnegative_int64(_value), do: :error
  defp positive_int64(value) when is_integer(value) and value in 1..@maximum_int64, do: :ok
  defp positive_int64(_value), do: :error
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
