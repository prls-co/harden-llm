defmodule HardenLlmWeb.Observability do
  @moduledoc """
  Safe, failure-isolated setup for traces and structured logging.

  Async work is wrapped with the initiating process's OpenTelemetry context so
  Req client spans stay attached to the LiveView event that started them.
  """

  @file_handler :harden_llm_web_file
  @safe_metadata [
    :request_id,
    :trace_id,
    :span_id,
    :operation,
    :outcome,
    :status_class,
    :error_category
  ]

  def setup do
    safely(:logger_metadata, &OpentelemetryLoggerMetadata.setup/0)
    safely(:phoenix_instrumentation, fn -> OpentelemetryPhoenix.setup(adapter: :bandit) end)

    if config()[:file_log_enabled] do
      safely(:file_logger, &install_file_handler/0)
    end

    :ok
  end

  def propagate(fun) when is_function(fun, 0) do
    context = :otel_ctx.get_current()

    fn ->
      token = :otel_ctx.attach(context)

      try do
        fun.()
      after
        :otel_ctx.detach(token)
      end
    end
  end

  def install_file_handler(path \\ nil, overrides \\ []) do
    settings = Keyword.merge(config(), overrides)
    path = path || Keyword.fetch!(settings, :file_log_path)
    :ok = File.mkdir_p(Path.dirname(path))

    handler_config = %{
      level: :info,
      config: %{
        file: String.to_charlist(path),
        modes: [:raw, :append, :delayed_write],
        max_no_bytes: Keyword.fetch!(settings, :file_log_max_bytes),
        max_no_files: Keyword.fetch!(settings, :file_log_max_files),
        compress_on_rotate: false,
        filesync_repeat_interval: 5_000
      },
      formatter: formatter()
    }

    case :logger.add_handler(@file_handler, :logger_std_h, handler_config) do
      :ok -> :ok
      {:error, {:already_exist, @file_handler}} -> :ok
      {:error, {:already_exists, @file_handler}} -> :ok
      other -> other
    end
  end

  def remove_file_handler do
    case :logger.remove_handler(@file_handler) do
      :ok -> :ok
      {:error, {:not_found, @file_handler}} -> :ok
      other -> other
    end
  end

  def formatter do
    LoggerJSON.Formatters.Basic.new(
      metadata: @safe_metadata,
      redactors: [
        LoggerJSON.Redactors.RedactKeys.new([
          "authorization",
          "cookie",
          "password",
          "token",
          "apiKey",
          "prompt",
          "response",
          "location"
        ])
      ]
    )
  end

  def safe_metadata, do: @safe_metadata

  defp config do
    Application.fetch_env!(:harden_llm, :observability)
  end

  defp safely(component, fun) do
    case fun.() do
      {:error, _reason} -> warn(component)
      _result -> :ok
    end
  rescue
    _exception -> warn(component)
  catch
    _kind, _reason -> warn(component)
  end

  defp warn(component) do
    IO.puts(:stderr, "harden-llm-web observability setup failed for #{component}")
    :ok
  end
end
