defmodule HardenLlmWeb.HardenAPI do
  @moduledoc """
  The only Phoenix-to-Go REST boundary.

  It owns bearer resolution, trace propagation, timeout policy, envelope
  validation, and safe error normalization. Callers never receive Req structs.
  """

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

  def operations, do: @operations
  def backend_only_operations, do: ["getHealth", "getReadiness"]

  def validate_config! do
    config = config()
    uri = URI.parse(config.base_url)

    unless uri.scheme in ["http", "https"] and is_binary(uri.host) and uri.host != "" and
             is_nil(uri.userinfo) and is_nil(uri.query) and is_nil(uri.fragment) do
      raise "HARDEN_LLM_API_BASE_URL must be an absolute HTTP(S) origin"
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

  def list_history(handle, options \\ []) do
    params =
      options
      |> Keyword.take([:cursor, :limit])
      |> Enum.reject(fn {_key, value} -> is_nil(value) end)

    request("listHistory", handle, params: params)
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
      {:ok, response} -> decode_response(operation, response)
      {:error, _reason} -> transport_error(operation)
    end
  rescue
    _exception -> protocol_error(operation, "The backend response could not be processed.")
  end

  defp decode_response(%{redirect: true}, %{status: 303} = response) do
    case Req.Response.get_header(response, "location") do
      [location] when is_binary(location) and location != "" -> {:ok, %{location: location}, %{}}
      _ -> protocol_error(nil, "The artifact response was malformed.")
    end
  end

  defp decode_response(operation, response) do
    with :ok <- require_json(response),
         {:ok, result} <- decode_envelope(operation, response.status, response.body) do
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

  defp decode_envelope(_operation, status, %{
         "state" => state,
         "result" => result,
         "error" => nil
       })
       when status in 200..299 and is_map(state) do
    {:ok, {:ok, result, state}}
  end

  defp decode_envelope(operation, status, %{
         "state" => state,
         "result" => nil,
         "error" => %{"code" => code, "message" => _message} = error
       })
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

  defp decode_envelope(operation, _status, _body) do
    {:error, protocol_error_value("The backend returned a malformed response.", operation)}
  end

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
