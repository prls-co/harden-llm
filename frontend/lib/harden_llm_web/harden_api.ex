defmodule HardenLlmWeb.HardenAPI do
  @moduledoc "The server-side OpenAI-compatible API client used by the reference app."

  alias HardenLlmWeb.APIError
  require Logger
  require OpenTelemetry.Tracer, as: Tracer

  def validate_config! do
    config = config()
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

  def models, do: request(:get, "/v1/models")
  def responses(payload) when is_map(payload), do: request(:post, "/v1/responses", payload)

  defp request(method, path, payload \\ nil) do
    started = System.monotonic_time()

    result =
      Tracer.with_span "harden_llm.openai.request",
                       %{
                         attributes: %{
                           "http.request.method" => method |> Atom.to_string() |> String.upcase(),
                           "http.route" => path
                         }
                       } do
        options = [
          method: method,
          base_url: config().base_url,
          url: path,
          headers:
            [
              {"accept", "application/json"},
              {"authorization", "Bearer " <> config().token}
            ] ++ trace_headers(),
          retry: false,
          redirect: false,
          receive_timeout: config().api_timeout_ms,
          pool_timeout: min(config().api_timeout_ms, 1_000)
        ]

        options = if is_map(payload), do: Keyword.put(options, :json, payload), else: options
        options = Keyword.merge(options, request_adapter_options())

        case Req.request(options) do
          {:ok, response} -> decode_response(path, response)
          {:error, _reason} -> {:error, transport_error()}
        end
      end

    record_result(path, result, System.monotonic_time() - started)
    result
  end

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
