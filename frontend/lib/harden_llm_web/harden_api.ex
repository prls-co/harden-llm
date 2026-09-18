defmodule HardenLlmWeb.HardenAPI do
  @moduledoc """
  The only Phoenix-to-Go REST boundary.

  It owns bearer resolution, trace propagation, timeout policy, envelope
  validation, and safe error normalization. Callers never receive Req structs.
  """

  alias HardenLlm.LlmDiagnosticsWire
  alias HardenLlmWeb.{APIError, SessionVault}
  require Logger
  require OpenTelemetry.Tracer, as: Tracer

  @operations [
    %{id: "login", function: :login, method: :post, path: "/api/v1/auth/login", auth: false},
    %{id: "logout", function: :logout, method: :post, path: "/api/v1/auth/logout", auth: true},
    %{
      id: "getSession",
      function: :get_session,
      method: :get,
      path: "/api/v1/auth/session",
      auth: true
    },
    %{id: "getState", function: :get_state, method: :get, path: "/api/v1/state", auth: true},
    %{id: "saveState", function: :save_state, method: :post, path: "/api/v1/state", auth: true},
    %{
      id: "listProfiles",
      function: :list_profiles,
      method: :get,
      path: "/api/v1/profiles",
      auth: true
    },
    %{
      id: "listHistory",
      function: :list_history,
      method: :get,
      path: "/api/v1/history",
      auth: true
    },
    %{id: "getStats", function: :get_stats, method: :get, path: "/api/v1/stats", auth: true},
    %{
      id: "clearHistory",
      function: :clear_history,
      method: :delete,
      path: "/api/v1/history",
      auth: true
    },
    %{
      id: "deleteHistory",
      function: :delete_history,
      method: :delete,
      path: "/api/v1/history/{historyID}",
      auth: true
    },
    %{
      id: "exportProfileBundle",
      function: :export_profile_bundle,
      method: :get,
      path: "/api/v1/profiles/bundle",
      auth: true
    },
    %{
      id: "importProfileBundle",
      function: :import_profile_bundle,
      method: :put,
      path: "/api/v1/profiles/bundle",
      auth: true
    },
    %{
      id: "saveProfile",
      function: :save_profile,
      method: :put,
      path: "/api/v1/profiles/{profileID}",
      auth: true
    },
    %{
      id: "deleteProfile",
      function: :delete_profile,
      method: :delete,
      path: "/api/v1/profiles/{profileID}",
      auth: true
    },
    %{
      id: "refreshProfileModels",
      function: :refresh_profile_models,
      method: :post,
      path: "/api/v1/profiles/{profileID}/models:refresh",
      auth: true
    },
    %{id: "run", function: :run, method: :post, path: "/api/v1/run", auth: true},
    %{
      id: "getTrace",
      function: :get_trace,
      method: :get,
      path: "/api/v1/traces/{traceID}",
      auth: true
    },
    %{
      id: "getArtifact",
      function: :get_artifact,
      method: :get,
      path: "/api/v1/traces/{traceID}/artifacts/{artifactID}",
      auth: true,
      redirect: true
    }
  ]

  @operations_by_id Map.new(@operations, &{&1.id, &1})
  @max_stream_bytes 2 * 1024 * 1024

  def operations, do: @operations
  def backend_only_operations, do: ["getHealth", "getReadiness"]

  def validate_config! do
    config = config()
    uri = URI.parse(config.base_url)
    public_uri = URI.parse(config.public_base_url)

    unless uri.scheme in ["http", "https"] and is_binary(uri.host) and uri.host != "" and
             is_nil(uri.userinfo) and is_nil(uri.query) and is_nil(uri.fragment) do
      raise "HARDEN_LLM_API_BASE_URL must be an absolute HTTP(S) origin"
    end

    unless public_uri.scheme in ["http", "https"] and is_binary(public_uri.host) and
             public_uri.host != "" and is_nil(public_uri.userinfo) and is_nil(public_uri.query) and
             is_nil(public_uri.fragment) and public_uri.path in [nil, "", "/"] do
      raise "HARDEN_LLM_PUBLIC_API_BASE_URL must be an absolute HTTP(S) origin"
    end

    unless config.api_timeout_ms > 0 do
      raise "HARDEN_LLM_WEB_API_TIMEOUT_MS must be positive"
    end

    unless config.max_run_duration_ms > 0 and config.run_timeout_ms > config.max_run_duration_ms do
      raise "HARDEN_LLM_WEB_RUN_TIMEOUT_MS must exceed HARDEN_LLM_MAX_RUN_DURATION_MS"
    end

    :ok
  end

  def login(email, password) do
    request("login", nil, json: %{"email" => email, "password" => password})
  end

  def logout(handle), do: request("logout", handle)
  def get_session(handle), do: request("getSession", handle)
  def get_state(handle), do: request("getState", handle)
  def save_state(handle, state), do: request("saveState", handle, json: state)
  def list_profiles(handle), do: request("listProfiles", handle)
  def get_stats(handle), do: request("getStats", handle)
  def public_base_url, do: config().public_base_url

  def list_history(handle, options \\ []) do
    params =
      options
      |> Keyword.take([:cursor, :limit, :page])
      |> Enum.reject(fn {_key, value} -> is_nil(value) end)

    history_mode = if Keyword.get(options, :page) != nil, do: :numbered, else: :cursor
    request("listHistory", handle, params: params, history_mode: history_mode)
  end

  def clear_history(handle), do: request("clearHistory", handle)

  def delete_history(handle, history_id) do
    request("deleteHistory", handle, path: %{"historyID" => history_id})
  end

  def export_profile_bundle(handle), do: request("exportProfileBundle", handle)

  def import_profile_bundle(handle, bundle),
    do: request("importProfileBundle", handle, json: bundle)

  def save_profile(handle, profile_id, payload) do
    request("saveProfile", handle, path: %{"profileID" => profile_id}, json: payload)
  end

  def delete_profile(handle, profile_id) do
    request("deleteProfile", handle, path: %{"profileID" => profile_id})
  end

  def refresh_profile_models(handle, profile_id) do
    request("refreshProfileModels", handle, path: %{"profileID" => profile_id})
  end

  def run(handle, payload), do: request("run", handle, json: payload, timeout: :run)

  @doc """
  Executes one authenticated request-bound SSE run.

  `receiver` is called synchronously with each strictly decoded envelope. It
  may return `:cont` (or `:ok`) to continue, or `:halt` to cancel the one POST.
  The adapter never retries or reconnects. A completed stream has the same
  `{:ok, result, state}` shape as `run/2`; a terminal failure is returned as a
  redacted `APIError` after its final event has been delivered.
  """
  def run_stream(handle, payload, receiver) when is_function(receiver, 1) do
    request_stream("run", handle, payload, receiver)
  end

  def get_trace(handle, trace_id) do
    request("getTrace", handle, path: %{"traceID" => trace_id})
  end

  def get_artifact(handle, trace_id, artifact_id) do
    request("getArtifact", handle, path: %{"traceID" => trace_id, "artifactID" => artifact_id})
  end

  defp request(operation_id, handle, options \\ []) do
    operation = Map.fetch!(@operations_by_id, operation_id)

    with {:ok, token} <- resolve_token(operation, handle) do
      path = expand_path(operation.path, Keyword.get(options, :path, %{}))
      timeout = timeout_for(Keyword.get(options, :timeout, :normal))
      started = System.monotonic_time()

      Tracer.with_span "harden_llm.api.request",
                       %{attributes: api_attributes(operation)} do
        result = perform_request(operation, path, token, timeout, options)
        record_result(operation, result, started)
        result
      end
    end
  end

  defp perform_request(operation, path, token, timeout, options) do
    headers = [{"accept", "application/json"}] ++ authorization_header(token) ++ trace_headers()

    request_options = [
      method: operation.method,
      base_url: config().base_url,
      url: path,
      headers: headers,
      params: Keyword.get(options, :params, []),
      retry: false,
      redirect: false,
      receive_timeout: timeout,
      pool_timeout: min(timeout, 5_000)
    ]

    request_options =
      case Keyword.fetch(options, :json) do
        {:ok, body} -> Keyword.put(request_options, :json, body)
        :error -> request_options
      end

    request_options = Keyword.merge(request_options, request_adapter_options())

    case Req.request(request_options) do
      {:ok, response} -> decode_response(operation, response, Keyword.get(options, :history_mode))
      {:error, _reason} -> transport_error(operation)
    end
  rescue
    _exception -> protocol_error(operation, "The backend response could not be processed.")
  end

  defp request_stream(operation_id, handle, payload, receiver) do
    operation = Map.fetch!(@operations_by_id, operation_id)

    with {:ok, token} <- resolve_token(operation, handle) do
      path = operation.path
      timeout = timeout_for(:run)
      started = System.monotonic_time()

      Tracer.with_span "harden_llm.api.request",
                       %{attributes: api_attributes(operation)} do
        result = perform_stream_request(operation, path, token, timeout, payload, receiver)
        record_result(operation, result, started)
        result
      end
    end
  end

  defp perform_stream_request(operation, path, token, timeout, payload, receiver) do
    key = {__MODULE__, make_ref()}
    Process.put(key, %{buffer: <<>>, status: nil, terminal: nil, error_body: <<>>, reason: nil})

    callback = fn {:data, data}, {request, response} ->
      state = Process.get(key)
      state = %{state | status: response.status}

      case stream_data(IO.iodata_to_binary(data), state, receiver) do
        {:cont, next_state} ->
          Process.put(key, next_state)
          {:cont, {request, response}}

        {:halt, next_state} ->
          Process.put(key, next_state)
          {:halt, {request, response}}
      end
    end

    headers = [{"accept", "text/event-stream"}] ++ authorization_header(token) ++ trace_headers()

    request_options = [
      method: operation.method,
      base_url: config().base_url,
      url: path,
      headers: headers,
      retry: false,
      redirect: false,
      receive_timeout: timeout,
      pool_timeout: min(timeout, 5_000),
      json: payload,
      into: callback
    ]

    try do
      case Req.request(Keyword.merge(request_options, request_adapter_options())) do
        {:ok, response} ->
          state = Process.get(key)
          finish_stream(operation, response, state)

        {:error, _reason} ->
          transport_error(operation)
      end
    rescue
      _exception -> protocol_error(operation, "The backend stream could not be processed.")
    after
      Process.delete(key)
    end
  end

  defp stream_data(data, %{status: status} = state, _receiver) when status not in 200..299 do
    if byte_size(state.error_body) + byte_size(data) > @max_stream_bytes do
      {:halt, %{state | reason: :response_too_large}}
    else
      {:cont, %{state | error_body: state.error_body <> data}}
    end
  end

  defp stream_data(data, state, receiver) do
    buffer = state.buffer <> data

    if byte_size(buffer) > @max_stream_bytes do
      {:halt, %{state | buffer: <<>>, reason: :response_too_large}}
    else
      consume_sse_blocks(%{state | buffer: buffer}, receiver)
    end
  end

  defp consume_sse_blocks(state, receiver) do
    # Keep a trailing CR until the next Req chunk arrives. Normalizing it to a
    # newline immediately would turn a CRLF split across chunks into an empty
    # event and could deliver a truncated JSON envelope.
    buffer = normalize_sse_line_endings(state.buffer)

    case :binary.match(buffer, "\n\n") do
      {index, 2} ->
        block = binary_part(buffer, 0, index)
        rest = binary_part(buffer, index + 2, byte_size(buffer) - index - 2)

        case consume_sse_block(block, %{state | buffer: rest}, receiver) do
          {:cont, next_state} -> consume_sse_blocks(next_state, receiver)
          {:halt, next_state} -> {:halt, next_state}
        end

      :nomatch ->
        {:cont, %{state | buffer: buffer}}
    end
  end

  defp normalize_sse_line_endings(buffer) when is_binary(buffer) do
    {body, suffix} =
      if String.ends_with?(buffer, "\r") do
        {binary_part(buffer, 0, byte_size(buffer) - 1), "\r"}
      else
        {buffer, ""}
      end

    :binary.replace(body, "\r\n", "\n", [:global])
    |> :binary.replace("\r", "\n", [:global])
    |> Kernel.<>(suffix)
  end

  defp consume_sse_block(block, state, receiver) do
    lines = String.split(block, "\n")

    event_name =
      lines
      |> Enum.find_value(fn
        "event:" <> value -> String.trim(value)
        _ -> nil
      end)

    data =
      lines
      |> Enum.filter(&String.starts_with?(&1, "data:"))
      |> Enum.map(&(String.trim_leading(&1, "data:") |> String.trim_leading()))
      |> Enum.join("\n")

    if data == "" do
      {:cont, state}
    else
      with {:ok, envelope} <- Jason.decode(data),
           {:ok, ^envelope} <- LlmDiagnosticsWire.decode_progress(envelope),
           :ok <- matching_event_name(event_name, envelope["type"]),
           {:ok, next_state} <- deliver_stream_event(envelope, state, receiver) do
        {:cont, next_state}
      else
        {:halt, next_state} -> {:halt, next_state}
        _ -> {:halt, %{state | reason: :malformed}}
      end
    end
  end

  defp matching_event_name(nil, _type), do: :ok
  defp matching_event_name(event_name, event_name), do: :ok
  defp matching_event_name(_event_name, _type), do: :error

  defp deliver_stream_event(envelope, state, receiver) do
    case receiver.(envelope) do
      value when value in [:ok, :cont] ->
        next_state = %{
          state
          | terminal:
              if(envelope["type"] in ["run.completed", "run.failed"],
                do: envelope,
                else: state.terminal
              )
        }

        if next_state.terminal, do: {:halt, next_state}, else: {:ok, next_state}

      :halt ->
        {:halt, %{state | reason: :canceled}}

      {:error, _reason} ->
        {:halt, %{state | reason: :receiver}}

      _other ->
        {:halt, %{state | reason: :receiver}}
    end
  rescue
    _exception -> {:halt, %{state | reason: :receiver}}
  end

  defp finish_stream(operation, response, state) do
    cond do
      state.reason == :canceled ->
        {:error,
         %APIError{category: :canceled, message: "The run stream was canceled.", ambiguous?: true}}

      state.reason == :response_too_large ->
        protocol_error(operation, "The backend stream exceeded the response limit.")

      response.status not in 200..299 ->
        decode_stream_error(operation, response.status, state.error_body)

      state.reason != nil ->
        protocol_error(operation, "The backend stream was malformed.")

      state.terminal == nil ->
        protocol_error(operation, "The backend stream ended without a terminal event.")

      true ->
        finish_terminal(state.terminal)
    end
  end

  defp finish_terminal(%{"type" => "run.completed", "data" => data}) do
    with %{"state" => state, "result" => result, "error" => nil} <- data,
         {:ok, decoded_result} <- LlmDiagnosticsWire.decode("run", result),
         {:ok, decoded_state} <- LlmDiagnosticsWire.decode_run_state(state) do
      {:ok, decoded_result, decoded_state}
    else
      _ -> protocol_error(nil, "The backend completed stream was malformed.")
    end
  end

  defp finish_terminal(%{
         "type" => "run.failed",
         "data" => %{"error" => error, "state" => state, "result" => result}
       })
       when is_map(error) and is_map(state) do
    with {:ok, _decoded_result} <- LlmDiagnosticsWire.decode("run", result),
         {:ok, decoded_state} <- LlmDiagnosticsWire.decode_run_state(state) do
      {:error,
       %APIError{
         category: :backend,
         code: safe_code(error["code"]),
         message: "The run failed.",
         trace_id: safe_trace_id(decoded_state),
         ambiguous?: false
       }}
    else
      _ -> protocol_error(nil, "The backend failed stream was malformed.")
    end
  end

  defp finish_terminal(_terminal),
    do: protocol_error(nil, "The backend terminal event was malformed.")

  defp decode_stream_error(operation, status, body) do
    case Jason.decode(body) do
      {:ok, decoded} ->
        case decode_envelope(operation, status, decoded, nil) do
          {:ok, {:error, error}} -> {:error, error}
          _ -> protocol_error(operation, "The backend response could not be processed.")
        end

      _ ->
        protocol_error(operation, "The backend response could not be processed.")
    end
  end

  defp decode_response(%{redirect: true}, %{status: 303} = response, _history_mode) do
    case Req.Response.get_header(response, "location") do
      [location] when is_binary(location) and location != "" -> {:ok, %{location: location}, %{}}
      _ -> protocol_error(nil, "The artifact response was malformed.")
    end
  end

  defp decode_response(operation, response, history_mode) do
    with :ok <- require_json(response),
         {:ok, result} <- decode_envelope(operation, response.status, response.body, history_mode) do
      result
    else
      {:error, %APIError{} = error} -> {:error, error}
    end
  end

  defp require_json(response) do
    case Req.Response.get_header(response, "content-type") do
      [content_type | _] ->
        if String.starts_with?(String.downcase(content_type), "application/json") do
          :ok
        else
          {:error, protocol_error_value("The backend returned an unexpected content type.")}
        end

      _ ->
        {:error, protocol_error_value("The backend returned an unexpected content type.")}
    end
  end

  defp decode_envelope(
         operation,
         status,
         %{
           "state" => state,
           "result" => result,
           "error" => nil
         },
         history_mode
       )
       when status in 200..299 and is_map(state) do
    with {:ok, decoded} <- decode_wire(operation.id, result, history_mode),
         {:ok, decoded_state} <- LlmDiagnosticsWire.decode_state(operation.id, state) do
      {:ok, {:ok, decoded, decoded_state}}
    else
      {:error, _reason} ->
        {:error, protocol_error_value("The backend returned a malformed response.", operation)}
    end
  end

  defp decode_envelope(
         operation,
         status,
         %{
           "state" => state,
           "result" => nil,
           "error" => %{"code" => code, "message" => _message} = error
         },
         _history_mode
       )
       when is_map(state) and is_binary(code) do
    {:ok,
     {:error,
      %APIError{
        category: status_category(status),
        status: status,
        code: safe_code(code),
        message: safe_status_message(status, code),
        field_errors: safe_field_errors(error["fieldErrors"]),
        trace_id: safe_trace_id(state),
        ambiguous?: operation.id == "run" and status >= 500
      }}}
  end

  defp decode_envelope(operation, _status, _body, _history_mode) do
    {:error, protocol_error_value("The backend returned a malformed response.", operation)}
  end

  defp decode_wire(operation, value, nil), do: LlmDiagnosticsWire.decode(operation, value)
  defp decode_wire(operation, value, mode), do: LlmDiagnosticsWire.decode(operation, value, mode)

  defp resolve_token(%{auth: false}, nil), do: {:ok, nil}

  defp resolve_token(%{auth: true}, handle) when is_binary(handle) do
    case SessionVault.lookup(handle) do
      {:ok, token, _expiry_ms} ->
        {:ok, token}

      :error ->
        {:error,
         %APIError{category: :unauthorized, status: 401, message: "Your session has expired."}}
    end
  end

  defp resolve_token(_operation, _handle) do
    {:error,
     %APIError{category: :unauthorized, status: 401, message: "Your session has expired."}}
  end

  defp authorization_header(nil), do: []
  defp authorization_header(token), do: [{"authorization", "Bearer " <> token}]

  defp trace_headers do
    :otel_propagator_text_map.inject([])
  rescue
    _ -> []
  end

  defp expand_path(template, values) do
    Enum.reduce(values, template, fn {name, value}, path ->
      String.replace(path, "{" <> name <> "}", encode_segment(value))
    end)
  end

  defp encode_segment(value) when is_binary(value) do
    URI.encode(value, &URI.char_unreserved?/1)
  end

  defp timeout_for(:run), do: config().run_timeout_ms
  defp timeout_for(:normal), do: config().api_timeout_ms

  defp request_adapter_options do
    Application.get_env(:harden_llm, :harden_api_req_options, [])
  end

  defp config do
    config = Application.fetch_env!(:harden_llm, :harden_api)

    %{
      base_url: Keyword.fetch!(config, :base_url),
      public_base_url: Keyword.fetch!(config, :public_base_url),
      api_timeout_ms: Keyword.fetch!(config, :api_timeout_ms),
      run_timeout_ms: Keyword.fetch!(config, :run_timeout_ms),
      max_run_duration_ms: Keyword.fetch!(config, :max_run_duration_ms)
    }
  end

  defp api_attributes(operation) do
    %{
      "harden_llm.api.operation" => operation.id,
      "http.request.method" => operation.method |> Atom.to_string() |> String.upcase(),
      "http.route" => operation.path
    }
  end

  defp record_result(operation, result, started) do
    duration = System.monotonic_time() - started
    {outcome, status_class, error_category} = result_metadata(result)

    Tracer.set_attributes(%{
      "harden_llm.outcome" => outcome,
      "http.response.status_class" => status_class,
      "error.type" => error_category
    })

    :telemetry.execute(
      [:harden_llm_web, :api, :stop],
      %{duration: duration},
      %{operation: operation.id, status_class: status_class, outcome: outcome}
    )

    Logger.info("backend operation completed",
      operation: operation.id,
      status_class: status_class,
      outcome: outcome,
      error_category: error_category
    )
  end

  defp result_metadata({:ok, _result, _state}), do: {"success", "2xx", "none"}

  defp result_metadata({:error, %APIError{status: status, category: category}}) do
    status_class = if is_integer(status), do: "#{div(status, 100)}xx", else: "transport"
    {"error", status_class, Atom.to_string(category)}
  end

  defp status_category(401), do: :unauthorized
  defp status_category(403), do: :forbidden
  defp status_category(409), do: :conflict
  defp status_category(422), do: :validation
  defp status_category(429), do: :rate_limited
  defp status_category(503), do: :unavailable
  defp status_category(status) when status >= 500, do: :backend
  defp status_category(_status), do: :request

  defp safe_status_message(422, "credential_required"),
    do: "The selected profile has no configured endpoint credential."

  defp safe_status_message(401, _code), do: "Your session has expired."
  defp safe_status_message(403, _code), do: "You are not authorized to perform that action."
  defp safe_status_message(409, _code), do: "The request conflicts with current backend state."
  defp safe_status_message(422, _code), do: "Please correct the highlighted fields."
  defp safe_status_message(429, _code), do: "The service is busy. Try again later."
  defp safe_status_message(503, _code), do: "The backend is temporarily unavailable."

  defp safe_status_message(status, _code) when status >= 500,
    do: "The backend could not complete the request."

  defp safe_status_message(_status, _code), do: "The request was rejected."

  defp safe_code(code), do: String.slice(code, 0, 64)

  defp safe_field_errors(errors) when is_map(errors) do
    Map.new(errors, fn {key, value} ->
      {String.slice(to_string(key), 0, 128), String.slice(to_string(value), 0, 512)}
    end)
  end

  defp safe_field_errors(_errors), do: %{}

  defp safe_trace_id(%{"lastTraceId" => value}) when is_binary(value),
    do: String.slice(value, 0, 128)

  defp safe_trace_id(_state), do: nil

  defp transport_error(operation) do
    {:error,
     %APIError{
       category: :transport,
       message: "The backend could not be reached.",
       ambiguous?: operation.id == "run"
     }}
  end

  defp protocol_error(operation, message), do: {:error, protocol_error_value(message, operation)}

  defp protocol_error_value(message, operation \\ nil) do
    %APIError{
      category: :protocol,
      message: message,
      ambiguous?: is_map(operation) and operation.id == "run"
    }
  end
end
