defmodule HardenLlmWeb.HardenAPI do
  @moduledoc "The server-side OpenAI-compatible API client used by the reference app."

  alias HardenLlmWeb.APIError
  require Logger
  require OpenTelemetry.Tracer, as: Tracer

  @stream_limit 16 * 1024 * 1024 + 64 * 1024

  def validate_config!, do: context() |> validate_context!()

  def context, do: config()

  def context(options) when is_list(options) do
    context = Map.new(options)
    :ok = validate_context!(context)
    context
  end

  defp validate_context!(config) do
    uri = URI.parse(config.base_url)

    unless uri.scheme in ["http", "https"] and is_binary(uri.host) and uri.host != "" and
             is_nil(uri.userinfo) and uri.query == nil and uri.fragment == nil and
             uri.path in [nil, "", "/"] do
      raise "HARDEN_LLM_API_BASE_URL must be an absolute HTTP(S) origin"
    end

    unless is_binary(config.token) and byte_size(config.token) in 32..512 and
             String.trim(config.token) == config.token and
             String.match?(config.token, ~r/^[!-~]+$/) do
      raise "HARDEN_LLM_TOKEN must be configured for the server-to-server boundary"
    end

    unless is_integer(config.api_timeout_ms) and config.api_timeout_ms in 1..120_000 do
      raise "HARDEN_LLM_WEB_API_TIMEOUT_MS must be between 1 and 120000"
    end

    :ok
  end

  def models, do: request(context(), :get, "/v1/models")

  def responses(payload) when is_map(payload),
    do: request(context(), :post, "/v1/responses", payload)

  def public_base_url, do: context().base_url
  def status(context), do: request(context, :get, "/api/v1/status")
  def list_profiles(context), do: request(context, :get, "/api/v1/profiles")
  def get_profile(context, id), do: request(context, :get, "/api/v1/profiles/" <> segment(id))
  def save_profile(context, payload), do: request(context, :post, "/api/v1/profiles", payload)

  def delete_profile(context, id),
    do: request(context, :delete, "/api/v1/profiles/" <> segment(id))

  def refresh_profile_models(context, id),
    do: request(context, :post, "/api/v1/profiles/" <> segment(id) <> "/models:refresh")

  def export_profile_bundle(context), do: request(context, :get, "/api/v1/profiles/bundle")

  def import_profile_bundle(context, bundle),
    do: request(context, :put, "/api/v1/profiles/bundle", bundle)

  def get_state(context, draft_key),
    do: request(context, :get, "/api/v1/state?" <> URI.encode_query(%{"draftKey" => draft_key}))

  def save_state(context, draft_key, revision, payload),
    do:
      request(context, :post, "/api/v1/state", %{
        "draftKey" => draft_key,
        "revision" => revision,
        "payload" => payload
      })

  def list_history(context, options \\ []),
    do: request(context, :get, query("/api/v1/history", options))

  def get_history(context, id), do: request(context, :get, "/api/v1/history/" <> segment(id))

  def delete_history(context, id),
    do: request(context, :delete, "/api/v1/history/" <> segment(id))

  def clear_history(context, options \\ []),
    do: request(context, :delete, query("/api/v1/history", options))

  def get_stats(context, options \\ []),
    do: request(context, :get, query("/api/v1/stats", options))

  def run(context, payload), do: request(context, :post, "/api/v1/run", payload)

  def run_stream(context, payload, receiver) when is_function(receiver, 1) do
    key = {__MODULE__, make_ref()}

    Process.put(key, %{
      buffer: "",
      bytes: 0,
      sequence: 0,
      run_id: nil,
      terminal: nil,
      recording: nil,
      error_body: "",
      reason: nil
    })

    started = System.monotonic_time()

    into = fn {:data, bytes}, {request, response} ->
      state = Process.get(key)
      chunk = IO.iodata_to_binary(bytes)
      next = receive_stream_chunk(state, response, chunk, receiver)
      Process.put(key, next)
      {if(next.reason, do: :halt, else: :cont), {request, response}}
    end

    try do
      options = request_options(context, :post, "/api/v1/run", payload, "text/event-stream")

      result =
        case Req.request(Keyword.put(options, :into, into)) do
          {:ok, response} -> finish_stream(response, Process.get(key))
          {:error, _reason} -> {:error, transport_error()}
        end

      record_result("/api/v1/run", result, System.monotonic_time() - started)
      result
    after
      Process.delete(key)
    end
  end

  defp receive_stream_chunk(state, response, chunk, receiver) do
    cond do
      state.bytes + byte_size(chunk) > @stream_limit ->
        %{state | reason: :oversize}

      response.status not in 200..299 ->
        %{state | error_body: state.error_body <> chunk, bytes: state.bytes + byte_size(chunk)}

      not stream_content_type?(response) ->
        %{state | reason: :content_type}

      true ->
        %{state | buffer: state.buffer <> chunk, bytes: state.bytes + byte_size(chunk)}
        |> consume_stream_blocks(receiver)
    end
  end

  defp consume_stream_blocks(state, receiver) do
    {body, suffix} =
      if String.ends_with?(state.buffer, "\r"),
        do: {binary_part(state.buffer, 0, byte_size(state.buffer) - 1), "\r"},
        else: {state.buffer, ""}

    buffer =
      body
      |> :binary.replace("\r\n", "\n", [:global])
      |> :binary.replace("\r", "\n", [:global])
      |> Kernel.<>(suffix)

    case :binary.match(buffer, "\n\n") do
      {index, 2} ->
        block = binary_part(buffer, 0, index)
        rest = binary_part(buffer, index + 2, byte_size(buffer) - index - 2)
        next = consume_stream_block(block, %{state | buffer: rest}, receiver)
        if next.reason, do: next, else: consume_stream_blocks(next, receiver)

      :nomatch ->
        %{state | buffer: buffer}
    end
  end

  defp consume_stream_block(block, state, receiver) do
    lines = String.split(block, "\n")

    event_name =
      Enum.find_value(lines, fn
        "event:" <> name -> String.trim(name)
        _ -> nil
      end)

    data =
      lines
      |> Enum.flat_map(fn
        "data:" <> value -> [String.trim_leading(value)]
        _ -> []
      end)
      |> Enum.join("\n")

    if data == "" do
      state
    else
      with {:ok, event} when is_map(event) <- Jason.decode(data),
           true <- valid_stream_event?(event_name, event, state),
           value when value in [:ok, :cont] <- receiver.(event) do
        state = %{state | sequence: event["sequence"], run_id: event["runId"]}

        case event["type"] do
          type when type in ["run.completed", "run.failed"] -> %{state | terminal: event}
          "history.recorded" -> %{state | recording: event["data"]}
          _ -> state
        end
      else
        _ -> %{state | reason: :malformed}
      end
    end
  end

  defp valid_stream_event?(name, event, state) do
    event["schemaVersion"] == 1 and event["type"] == name and
      event["sequence"] == state.sequence + 1 and is_binary(event["runId"]) and
      (is_nil(state.run_id) or event["runId"] == state.run_id) and
      is_map(event["data"]) and
      Enum.all?(
        Map.keys(event),
        &(&1 in ~w(schemaVersion sequence runId callId traceId type data))
      ) and
      valid_stream_data?(event["type"], event["data"], state) and correlated_stream_data?(event)
  end

  defp correlated_stream_data?(%{"type" => type, "data" => data} = event)
       when type in ["run.completed", "run.failed"] do
    result = data["result"]
    run_state = data["state"]

    result["runId"] == event["runId"] and result["callId"] == event["callId"] and
      result["traceId"] == event["traceId"] and run_state["lastRunId"] == event["runId"] and
      run_state["lastTraceId"] == event["traceId"] and
      ((type == "run.completed" and result["status"] == "succeeded" and is_nil(data["error"])) or
         (type == "run.failed" and result["status"] in ["failed", "timeout"] and
            is_map(data["error"])))
  end

  defp correlated_stream_data?(_event), do: true

  defp valid_stream_data?(type, data, %{terminal: nil})
       when type in ["run.started", "run.progress"],
       do: data["schemaVersion"] == 1 and is_binary(data["type"])

  defp valid_stream_data?(type, data, %{terminal: nil})
       when type in ["run.completed", "run.failed"],
       do: is_map(data["state"]) and valid_recorded_result?(data["result"])

  defp valid_stream_data?("history.recorded", data, %{
         terminal: terminal,
         recording: nil,
         run_id: run_id
       })
       when not is_nil(terminal),
       do:
         data["runId"] == run_id and data["mode"] in ["enabled", "disabled"] and
           data["status"] in ~w(disabled configuration_error saved not_saved unconfirmed)

  defp valid_stream_data?(_type, _data, _state), do: false

  defp valid_recorded_result?(result) when is_map(result),
    do:
      result["schemaVersion"] == 4 and is_binary(result["runId"]) and
        result["status"] in ~w(succeeded failed timeout) and is_map(result["accounting"]) and
        is_list(result["attempts"]) and is_map(result["cache"]) and is_list(result["artifacts"])

  defp valid_recorded_result?(_result), do: false

  defp finish_stream(response, state) do
    cond do
      state.reason ->
        {:error, protocol_error()}

      response.status not in 200..299 ->
        case Jason.decode(state.error_body) do
          {:ok, body} -> decode_response("/api/v1/run", %{response | body: body})
          _ -> {:error, protocol_error()}
        end

      not stream_content_type?(response) or state.buffer != "" or
        is_nil(state.terminal) or is_nil(state.recording) ->
        {:error, protocol_error()}

      state.terminal["type"] == "run.failed" ->
        {:error,
         %APIError{
           category: :backend,
           message: "The run failed.",
           trace_id: state.terminal["traceId"],
           ambiguous?: false
         }}

      true ->
        data = state.terminal["data"]

        {:ok,
         %{
           "result" => Map.put(data["result"], "history", state.recording),
           "state" => data["state"]
         }}
    end
  end

  defp stream_content_type?(response),
    do:
      Enum.any?(
        Req.Response.get_header(response, "content-type"),
        &String.starts_with?(String.downcase(&1), "text/event-stream")
      )

  def get_trace(context, id), do: request(context, :get, "/api/v1/traces/" <> segment(id))

  def get_artifact(context, run_id, artifact_id),
    do:
      request(
        context,
        :get,
        "/api/v1/traces/" <> segment(run_id) <> "/artifacts/" <> segment(artifact_id)
      )

  defp segment(value), do: URI.encode(value, &URI.char_unreserved?/1)
  defp query(path, []), do: path
  defp query(path, options), do: path <> "?" <> URI.encode_query(options)

  defp request(context, method, path, payload \\ nil) do
    started = System.monotonic_time()

    result =
      Tracer.with_span "harden_llm.openai.request",
                       %{
                         attributes: %{
                           "http.request.method" => method |> Atom.to_string() |> String.upcase(),
                           "http.route" => path
                         }
                       } do
        options = request_options(context, method, path, payload, "application/json")

        case Req.request(options) do
          {:ok, response} -> decode_response(path, response)
          {:error, _reason} -> {:error, transport_error()}
        end
      end

    record_result(path, result, System.monotonic_time() - started)
    result
  end

  defp request_options(context, method, path, payload, accept) do
    options = [
      method: method,
      base_url: context.base_url,
      url: path,
      headers:
        [
          {"accept", accept},
          {"authorization", "Bearer " <> context.token}
        ] ++ trace_headers(),
      retry: false,
      redirect: false,
      receive_timeout: context.api_timeout_ms,
      pool_timeout: min(context.api_timeout_ms, 1_000)
    ]

    options = if is_map(payload), do: Keyword.put(options, :json, payload), else: options
    Keyword.merge(options, request_adapter_options())
  end

  defp decode_response(_path, %{status: 204}), do: {:ok, nil}

  defp decode_response(path, %{status: status, body: body} = response) when status in 200..299 do
    with :ok <- require_json(response),
         true <- valid_success_body?(path, body) do
      {:ok, body}
    else
      _ -> {:error, protocol_error()}
    end
  end

  defp decode_response(_path, %{status: status, body: body} = response) do
    with :ok <- require_json(response),
         %{"error" => error} when is_map(error) <- body do
      code = error["code"]

      {:error,
       %APIError{
         category: status_category(status),
         status: status,
         code: if(is_binary(code), do: String.slice(code, 0, 64)),
         message: safe_error_message(status, code),
         ambiguous?: status >= 500
       }}
    else
      _ -> {:error, protocol_error()}
    end
  end

  defp valid_success_body?("/v1/models", %{"object" => "list", "data" => models})
       when is_list(models) do
    Enum.all?(models, fn
      %{"id" => id, "object" => "model", "created" => 0, "owned_by" => owner}
      when is_binary(id) and is_binary(owner) ->
        true

      _ ->
        false
    end)
  end

  defp valid_success_body?("/v1/responses", body) when is_map(body) do
    is_binary(body["id"]) and body["object"] == "response" and
      is_integer(body["created_at"]) and body["status"] == "completed" and
      is_binary(body["model"]) and is_list(body["output"]) and
      is_binary(body["output_text"]) and is_boolean(body["parallel_tool_calls"])
  end

  defp valid_success_body?("/api/v1/profiles", %{"profiles" => profiles}), do: is_list(profiles)
  defp valid_success_body?("/api/v1/profiles", %{"profile" => profile}), do: is_map(profile)

  defp valid_success_body?("/api/v1/run", %{"result" => result, "state" => state}),
    do:
      valid_recorded_result?(result) and is_map(state) and
        is_map(result["history"])

  defp valid_success_body?("/api/v1/history" <> _query, %{
         "items" => items,
         "pagination" => pagination,
         "window" => window,
         "history" => history
       }),
       do: is_list(items) and is_map(pagination) and is_map(window) and is_map(history)

  defp valid_success_body?("/api/v1/state" <> _query, %{
         "draftKey" => key,
         "revision" => revision,
         "payload" => payload
       }),
       do: is_binary(key) and is_integer(revision) and is_map(payload)

  defp valid_success_body?("/api/v1/stats" <> _query, %{
         "scope" => "available_records",
         "accounting" => accounting
       }),
       do: is_map(accounting)

  defp valid_success_body?("/api/v1/status", %{"storage" => storage, "historyStatus" => history}),
    do: is_map(storage) and is_map(history)

  defp valid_success_body?("/api/v1/profiles/bundle", %{
         "schemaVersion" => 4,
         "profiles" => profiles
       }),
       do: is_list(profiles)

  defp valid_success_body?("/api/v1/profiles/bundle", %{"profiles" => profiles}),
    do: is_list(profiles)

  defp valid_success_body?("/api/v1/profiles/" <> _id, %{
         "definition" => definition,
         "inputs" => inputs
       }),
       do: is_map(definition) and is_map(inputs)

  defp valid_success_body?("/api/v1/history/" <> _id, %{
         "schemaVersion" => 1,
         "request" => request,
         "result" => result
       }),
       do: is_map(request) and is_map(result)

  defp valid_success_body?("/api/v1/history" <> _query, %{
         "deleted" => count,
         "complete" => complete
       }),
       do: is_integer(count) and is_boolean(complete)

  defp valid_success_body?("/api/v1/traces/" <> _id, body), do: is_map(body)
  defp valid_success_body?(_, _), do: false

  defp require_json(response) do
    case Req.Response.get_header(response, "content-type") do
      [content_type | _] ->
        if String.starts_with?(String.downcase(content_type), "application/json"),
          do: :ok,
          else: {:error, :content_type}

      _ ->
        {:error, :content_type}
    end
  end

  defp trace_headers do
    :otel_propagator_text_map.inject([])
  rescue
    _ -> []
  end

  defp request_adapter_options do
    Application.get_env(:harden_llm, :harden_api_req_options, [])
  end

  defp config do
    config = Application.fetch_env!(:harden_llm, :harden_api)

    %{
      base_url: Keyword.fetch!(config, :base_url),
      api_timeout_ms: Keyword.fetch!(config, :api_timeout_ms),
      token: Keyword.fetch!(config, :token)
    }
  end

  defp status_category(401), do: :unauthorized
  defp status_category(403), do: :forbidden
  defp status_category(429), do: :rate_limited
  defp status_category(502), do: :backend
  defp status_category(503), do: :unavailable
  defp status_category(504), do: :timeout
  defp status_category(status) when status >= 500, do: :backend
  defp status_category(400), do: :validation
  defp status_category(_), do: :request

  defp safe_error_message(401, _), do: "The proxy rejected its configured API token."
  defp safe_error_message(403, _), do: "The request is not authorized."
  defp safe_error_message(404, _), do: "The requested model or operation was not found."
  defp safe_error_message(429, _), do: "The service is busy. Try again later."

  defp safe_error_message(status, _) when status >= 500,
    do: "The proxy could not complete the request."

  defp safe_error_message(_, _), do: "The proxy rejected the request."

  defp transport_error do
    %APIError{category: :transport, message: "The proxy could not be reached.", ambiguous?: true}
  end

  defp protocol_error do
    %APIError{category: :protocol, message: "The proxy returned a malformed response."}
  end

  defp record_result(path, result, duration) do
    outcome = if match?({:ok, _}, result), do: "success", else: "error"

    Tracer.set_attributes(%{
      "harden_llm.outcome" => outcome,
      "http.route" => path
    })

    :telemetry.execute([:harden_llm_web, :api, :stop], %{duration: duration}, %{
      operation: path,
      outcome: outcome
    })

    Logger.info("OpenAI request completed", operation: path, outcome: outcome)
  end
end
