defmodule HardenLlmWeb.SecurityObservabilityTest do
  use HardenLlmWeb.ConnCase, async: false

  import ExUnit.CaptureIO
  import ExUnit.CaptureLog

  alias HardenLlmWeb.{Observability, PromExPlugin}

  require Logger

  @repo_root Path.expand("../../..", __DIR__)
  @frontend_root Path.join(@repo_root, "frontend")

  test "browser security headers, CSRF, and production origin rules stay strict", %{conn: conn} do
    conn = get(conn, ~p"/login")
    csp = conn |> get_resp_header("content-security-policy") |> List.first()

    assert csp =~ "default-src 'self'"
    assert csp =~ "frame-ancestors 'none'"
    refute csp =~ "'unsafe-inline'"
    refute csp =~ "'unsafe-eval'"
    assert get_resp_header(conn, "x-frame-options") == ["DENY"]
    assert get_resp_header(conn, "referrer-policy") == ["no-referrer"]
    assert html_response(conn, 200) =~ ~s(name="_csrf_token")

    endpoint = File.read!(Path.join(@frontend_root, "lib/harden_llm_web/endpoint.ex"))
    runtime = File.read!(Path.join(@frontend_root, "config/runtime.exs"))
    production = File.read!(Path.join(@frontend_root, "config/prod.exs"))
    assert endpoint =~ "check_origin: true"
    assert runtime =~ ~s(check_origin: ["https://\#{host}"])
    assert production =~ ~s(paths: ["/metrics"])
  end

  test "production source and container configuration contain no deployable secrets" do
    production_files =
      ["lib", "config", "assets"]
      |> Enum.flat_map(&Path.wildcard(Path.join([@frontend_root, &1, "**", "*"])))
      |> Enum.filter(&File.regular?/1)
      |> Kernel.++([
        Path.join(@frontend_root, "Dockerfile"),
        Path.join(@repo_root, ".env.example"),
        Path.join(@repo_root, "deploy/frontend/compose.frontend.yml"),
        Path.join(@repo_root, "deploy/frontend/otel.frontend.yaml")
      ])

    source = Enum.map_join(production_files, "\n", &File.read!/1)

    refute source =~ "fixture-backend-token"
    refute source =~ "fixture-provider-secret"
    refute Regex.match?(~r/sk-[A-Za-z0-9_-]{20,}/, source)
    refute Regex.match?(~r/AKIA[A-Z0-9]{16}/, source)
    refute source =~ "BEGIN PRIVATE KEY"

    overlay = yaml!(Path.join(@repo_root, "deploy/frontend/compose.frontend.yml"))
    web_environment = get_in(overlay, ["services", "harden-llm-web", "environment"])

    forbidden_runtime_inputs =
      ~w(POSTGRES LANGFUSE GARAGE PROVIDER GRAFANA CLICKHOUSE REDIS MINIO)

    refute Enum.any?(Map.keys(web_environment), fn name ->
             Enum.any?(forbidden_runtime_inputs, &String.contains?(name, &1))
           end)
  end

  test "async wrappers attach the initiating OpenTelemetry context and restore the worker" do
    :ok = :otel_ctx.set_value(:harden_llm_test_parent, "parent-context")
    wrapped = Observability.propagate(fn -> :otel_ctx.get_value(:harden_llm_test_parent) end)
    :ok = :otel_ctx.clear()

    task = Task.async(wrapped)
    assert Task.await(task) == "parent-context"
    assert :otel_ctx.get_value(:harden_llm_test_parent, :missing) == :missing
  end

  test "PromEx exposes required series with only bounded labels", %{conn: conn} do
    metric_tags =
      PromExPlugin.event_metrics([])
      |> Enum.flat_map(& &1.metrics)
      |> Enum.flat_map(& &1.tags)
      |> MapSet.new()

    assert MapSet.subset?(
             metric_tags,
             MapSet.new([:route, :status_class, :outcome, :live_view, :operation])
           )

    assert PromExPlugin.api_tags(%{
             operation: "user-controlled-operation",
             status_class: "999xx",
             outcome: "maybe"
           }) == %{operation: "other", status_class: "transport", outcome: "error"}

    assert PromExPlugin.http_tags(%{
             route: "/users/arbitrary-secret",
             conn: %Plug.Conn{status: 200}
           }) == %{route: "other", status_class: "2xx", outcome: "success"}

    body = conn |> get("/metrics") |> response(200)
    assert body =~ "harden_llm_web_session_vault_entries"
    assert body =~ "harden_llm_prom_ex_beam_stats_process_count"
  end

  test "JSON file logs correlate traces, redact metadata, and rotate within bounds" do
    directory =
      Path.join(
        System.tmp_dir!(),
        "harden-llm-observability-#{System.unique_integer([:positive])}"
      )

    path = Path.join(directory, "app.jsonl")

    on_exit(fn ->
      Observability.remove_file_handler()
      File.rm_rf(directory)
    end)

    assert :ok =
             Observability.install_file_handler(path,
               file_log_max_bytes: 512,
               file_log_max_files: 2
             )

    span_context =
      :otel_tracer.from_remote_span(
        0x0123456789ABCDEF0123456789ABCDEF,
        0x0123456789ABCDEF,
        1
      )

    previous_span = :otel_tracer.current_span_ctx()
    :otel_tracer.set_current_span(span_context)

    capture_log(fn ->
      for index <- 1..4 do
        Logger.warning("rotation event #{index} " <> String.duplicate("x", 700))
      end

      Logger.warning("correlated frontend event",
        operation: "run",
        outcome: "success",
        status_class: "2xx",
        prompt: "must-not-appear"
      )

      assert :ok = :logger_std_h.filesync(:harden_llm_web_file)
    end)

    :otel_tracer.set_current_span(previous_span)

    files = Path.wildcard(path <> "*")
    assert path in files
    assert (path <> ".0") in files
    assert length(files) <= 3

    records =
      files
      |> Enum.flat_map(&File.stream!(&1))
      |> Enum.map(&Jason.decode!/1)

    correlated = Enum.find(records, &(&1["message"] == "correlated frontend event"))
    assert correlated["trace"] =~ ~r/^[0-9a-f]{32}$/
    assert correlated["span"] =~ ~r/^[0-9a-f]{16}$/
    assert correlated["metadata"]["operation"] == "run"
    refute inspect(records) =~ "must-not-appear"
  end

  test "frontend Collector extension is additive, private, validatable, and excluded from Langfuse" do
    frontend = yaml!(Path.join(@repo_root, "deploy/frontend/otel.frontend.yaml"))
    base = yaml!(Path.join(@repo_root, "deploy/otel/collector.yaml"))
    overlay = yaml!(Path.join(@repo_root, "deploy/frontend/compose.frontend.yml"))
    caddy = File.read!(Path.join(@repo_root, "deploy/frontend/Caddyfile.frontend"))

    assert Map.has_key?(frontend["receivers"], "filelog/harden_llm_web")
    assert Map.has_key?(frontend["receivers"], "prometheus/harden_llm_web")

    assert Map.keys(get_in(frontend, ["service", "pipelines"])) |> Enum.sort() ==
             ["logs/frontend", "metrics/frontend"]

    refute Map.has_key?(frontend["service"], "extensions")

    assert get_in(frontend, ["service", "pipelines", "logs/frontend", "exporters"]) ==
             ["otlphttp/loki"]

    assert get_in(frontend, ["service", "pipelines", "metrics/frontend", "exporters"]) ==
             ["prometheus"]

    collector_command = get_in(overlay, ["services", "otel-collector", "command"])

    assert collector_command == [
             "--config=file:/etc/otelcol-contrib/config.yaml",
             "--config=file:/etc/otelcol-contrib/frontend.yaml"
           ]

    refute Enum.any?(collector_command, &String.contains?(&1, "merge"))
    assert caddy =~ "@private_metrics path /metrics"
    assert caddy =~ "respond @private_metrics 404"
    refute get_in(overlay, ["services", "harden-llm-web"]) |> Map.has_key?("ports")

    langfuse_processors = get_in(base, ["service", "pipelines", "traces/langfuse", "processors"])
    filter_index = Enum.find_index(langfuse_processors, &(&1 == "filter/langfuse"))
    sampler_index = Enum.find_index(langfuse_processors, &(&1 == "tail_sampling/langfuse"))

    assert is_integer(filter_index) and filter_index < sampler_index

    assert get_in(base, ["processors", "filter/langfuse", "traces", "span"]) == [
             ~s(resource.attributes["service.name"] != "harden-llm-gateway")
           ]

    healthcheck = get_in(overlay, ["services", "otel-collector", "healthcheck", "test"])
    assert "validate" in healthcheck
    assert "--config=file:/etc/otelcol-contrib/frontend.yaml" in healthcheck
  end

  test "observability setup failure is isolated from request handling", %{conn: conn} do
    previous = Application.fetch_env!(:harden_llm, :observability)

    Application.put_env(
      :harden_llm,
      :observability,
      Keyword.merge(previous,
        file_log_enabled: true,
        file_log_path: "/proc/harden-llm-web/app.jsonl"
      )
    )

    on_exit(fn -> Application.put_env(:harden_llm, :observability, previous) end)

    warning = capture_io(:stderr, fn -> assert :ok = Observability.setup() end)
    assert warning =~ "observability setup failed for file_logger"
    assert conn |> get("/healthz") |> json_response(200) == %{"status" => "ok"}
  end

  defp yaml!(path) do
    assert {:ok, value} = YamlElixir.read_from_file(path)
    value
  end
end
