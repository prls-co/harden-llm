defmodule HardenLlmWeb.ComposeSmokeTest do
  use ExUnit.Case, async: false
  use Wallaby.Feature

  @moduletag compose: true, timeout: 900_000

  @compose_capabilities Wallaby.Chrome.default_capabilities()
                        |> Map.put(:acceptInsecureCerts, true)
                        |> update_in([:chromeOptions, :args], fn arguments ->
                          arguments ++
                            [
                              "--ignore-certificate-errors",
                              "--host-resolver-rules=MAP app.smoke.localhost 127.0.0.1, MAP api.smoke.localhost 127.0.0.1, MAP grafana.smoke.localhost 127.0.0.1, MAP artifacts.smoke.localhost 127.0.0.1"
                            ]
                        end)
  @sessions [[capabilities: @compose_capabilities]]

  alias Wallaby.Query

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-012

  setup do
    root = Path.expand("../../..", __DIR__)
    nonce = "#{System.os_time(:millisecond)}-#{System.unique_integer([:positive])}"
    work_dir = Path.join(root, "frontend/tmp/compose/#{nonce}")
    state_path = Path.join(work_dir, "state.json")
    done_path = Path.join(work_dir, "done")
    File.mkdir_p!(work_dir)

    {:ok, coordinator} =
      HardenLlmWeb.ComposeFixture.start(
        root: root,
        work_dir: work_dir,
        state_path: state_path,
        done_path: done_path
      )

    on_exit(fn ->
      try do
        HardenLlmWeb.ComposeFixture.release!(coordinator, done_path, 180_000)
      after
        if Process.alive?(coordinator), do: GenServer.stop(coordinator)
        File.rm_rf(work_dir)
      end
    end)

    fixture = HardenLlmWeb.ComposeFixture.await_state!(coordinator, state_path, 480_000)
    {:ok, fixture: fixture, root: root}
  end

  feature "16-service product preserves browser, routing, recovery, and telemetry invariants", %{
    session: session,
    fixture: fixture,
    root: root
  } do
    provider_secret = "compose-provider-secret-must-never-escape"

    session =
      session
      |> resize_window(1_440, 900)
      |> visit(fixture["web_url"] <> "/login")
      |> assert_has(Query.css("#login-page"))
      |> fill_in(Query.text_field("Email address"), with: fixture["login_email"])
      |> fill_in(Query.css("#session_password"), with: fixture["login_password"])
      |> click(Query.css("#login-submit"))
      |> assert_has(Query.css("#workspace-page"))
      |> assert_has(Query.css("#backend-status", text: "Backend ready"))
      |> assert_live_socket_connected()

    assert String.starts_with?(current_url(session), "https://app.smoke.localhost:")
    assert public_status(fixture, "app.smoke.localhost", "/metrics") == 404
    assert_runtime_image_contract!(fixture, root)

    session =
      session
      |> visit(fixture["web_url"] <> "/profiles")
      |> assert_has(Query.css("#profiles-page"))
      |> click(Query.css("#new-profile"))
      |> fill_in(Query.text_field("Profile name"), with: "Smoke")
      |> fill_in(Query.text_field("Provider family"), with: "openai")
      |> fill_in(Query.text_field("Default model"), with: "smoke-model")
      |> fill_in(Query.fillable_field("HTTPS base URL"), with: "https://fake-provider:8443/v1")
      |> fill_in(Query.text_field("Credential ID"), with: "compose-smoke-provider")
      |> fill_in(Query.css("#profile_apiKey"), with: provider_secret)
      |> click(Query.css("#profile-save"))
      |> assert_has(Query.css("#profile-Smoke", text: "Smoke"))
      |> visit(fixture["web_url"] <> "/workspace")
      |> assert_has(Query.css("#backend-status", text: "Backend ready"))
      |> click(Query.css("#model-config-toggle"))
      |> assert_has(Query.css("#model-options"))
      |> click(Query.css("#profile-retry-toggle"))
      |> assert_has(Query.css("#profile-retry-repair"))
      |> click(Query.css("#profile-escalation-config-toggle"))
      |> assert_has(Query.css("#profile-escalation-config"))
      |> choose_option("#run_selectedProfileId", "Smoke")
      |> fill_in(Query.css("#run_userPrompt"), with: "return the compose smoke response")
      |> click(Query.css("#run-submit"))
      |> assert_has(Query.css("#run-output", text: "smoke-ok"))

    {run_id, domain_trace_id} = result_ids(session)
    assert run_id != ""
    assert domain_trace_id != ""

    resources =
      javascript_value(
        session,
        "return performance.getEntriesByType('resource').map(entry => entry.name);"
      )

    refute Enum.any?(resources, &String.contains?(&1, "api.smoke.localhost"))
    refute Enum.any?(resources, &String.contains?(&1, "/api/v1/"))
    refute page_source(session) =~ provider_secret
    refute page_source(session) =~ fixture["login_password"]

    telemetry = assert_telemetry!(fixture, domain_trace_id)
    assert telemetry.tempo =~ "harden-llm-web"
    assert telemetry.tempo =~ "harden-llm-gateway"
    assert telemetry.loki =~ telemetry.otel_trace_id
    assert telemetry.prometheus =~ "harden_llm_web_api_requests"
    assert telemetry.grafana =~ "harden_llm_web_api_requests"

    refute telemetry.tempo =~ provider_secret
    refute telemetry.loki =~ provider_secret
    refute telemetry.grafana =~ provider_secret

    logs = compose!(fixture, root, ["logs", "--no-color", "--tail", "300"])

    web_logs =
      compose!(fixture, root, [
        "exec",
        "-T",
        "harden-llm-web",
        "sh",
        "-c",
        "cat /var/log/harden-llm-web/app.jsonl"
      ])

    for secret <- [provider_secret, fixture["login_password"]] do
      refute logs =~ secret
      refute web_logs =~ secret
      refute inspect(cookies(session)) =~ secret
    end

    _output = compose!(fixture, root, ["stop", "-t", "5", "harden-llm-gateway"])
    assert public_status(fixture, "app.smoke.localhost", "/healthz") == 200

    session =
      session
      |> visit(fixture["web_url"] <> "/workspace")
      |> assert_has(Query.css("#login-page"))

    _output =
      compose!(fixture, root, [
        "up",
        "-d",
        "--no-deps",
        "--wait",
        "--wait-timeout",
        "120",
        "harden-llm-gateway"
      ])

    session
    |> fill_in(Query.text_field("Email address"), with: fixture["login_email"])
    |> fill_in(Query.css("#session_password"), with: fixture["login_password"])
    |> click(Query.css("#login-submit"))
    |> assert_has(Query.css("#workspace-page"))
    |> assert_has(Query.css("#backend-status", text: "Backend ready"))
    |> assert_live_socket_connected()
  end

  defp assert_telemetry!(fixture, domain_trace_id) do
    search_query = ~s({ span.harden_llm.trace.id = "#{domain_trace_id}" })

    {otel_trace_id, tempo} =
      eventually!(150_000, fn ->
        search =
          internal_fetch!(
            fixture,
            "http://tempo:3200/api/search?q=#{URI.encode_www_form(search_query)}"
          )
          |> Jason.decode!()

        case get_in(search, ["traces", Access.at(0), "traceID"]) do
          trace_id when is_binary(trace_id) and byte_size(trace_id) == 32 ->
            trace = internal_fetch!(fixture, "http://tempo:3200/api/traces/#{trace_id}")

            if trace =~ "harden-llm-web" and trace =~ "harden-llm-gateway" and
                 trace =~ domain_trace_id do
              {:ok, {trace_id, trace}}
            else
              :retry
            end

          _other ->
            :retry
        end
      end)

    loki_query = ~s({service_name="harden-llm-web"} |= "backend operation completed")

    loki =
      eventually!(60_000, fn ->
        body =
          internal_fetch!(
            fixture,
            "http://loki:3100/loki/api/v1/query_range?limit=200&direction=backward&query=#{URI.encode_www_form(loki_query)}"
          )

        if body =~ otel_trace_id and body =~ "backend operation completed",
          do: {:ok, body},
          else: :retry
      end)

    # The shared Collector exporter intentionally uses
    # UnderscoreEscapingWithoutSuffixes, so OTLP counter names omit `_total`.
    metric_query = ~s(harden_llm_web_api_requests{operation="run",outcome="success"})

    prometheus =
      eventually!(60_000, fn ->
        body =
          internal_fetch!(
            fixture,
            "http://prometheus:9090/api/v1/query?query=#{URI.encode_www_form(metric_query)}"
          )

        if prometheus_sample?(body),
          do: {:ok, body},
          else: {:retry, String.slice(body, 0, 1_024)}
      end)

    grafana =
      eventually!(45_000, fn ->
        body = grafana_query!(fixture, metric_query)

        if prometheus_sample?(body),
          do: {:ok, body},
          else: {:retry, String.slice(body, 0, 1_024)}
      end)

    %{
      otel_trace_id: otel_trace_id,
      tempo: tempo,
      loki: loki,
      prometheus: prometheus,
      grafana: grafana
    }
  end

  defp assert_runtime_image_contract!(fixture, root) do
    output =
      compose!(fixture, root, [
        "exec",
        "-T",
        "harden-llm-web",
        "sh",
        "-c",
        """
        set -eu
        test "$(id -u)" = "10001"
        for command in mix hex rebar3 node npm go; do
          if command -v "$command" >/dev/null 2>&1; then exit 1; fi
        done
        for path in /app/mix.exs /app/assets /app/deps /app/.hex /app/.mix; do
          if test -e "$path"; then exit 1; fi
        done
        printf 'runtime-image-contract-ok\\n'
        """
      ])

    assert output == "runtime-image-contract-ok\n"
  end

  defp prometheus_sample?(body) do
    case Jason.decode(body) do
      {:ok, %{"status" => "success", "data" => %{"result" => results}}} when results != [] -> true
      _other -> false
    end
  end

  defp result_ids(session) do
    javascript_value(
      session,
      """
      const facts = Array.from(document.querySelectorAll("#run-result-panel dl > div"));
      const value = label => {
        const fact = facts.find(item => item.querySelector("dt")?.textContent.trim() === label);
        return fact?.querySelector("dd")?.getAttribute("title") || "";
      };
      return {runId: value("Run ID"), traceId: value("Trace ID")};
      """
    )
    |> then(fn result -> {result["runId"], result["traceId"]} end)
  end

  defp choose_option(session, selector, value) do
    execute_script(
      session,
      """
      const select = document.querySelector(arguments[0]);
      select.value = arguments[1];
      select.dispatchEvent(new Event("input", {bubbles: true}));
      select.dispatchEvent(new Event("change", {bubbles: true}));
      """,
      [selector, value]
    )
  end

  defp assert_live_socket_connected(session) do
    assert javascript_value(session, "return Boolean(window.liveSocket?.isConnected());")
    session
  end

  defp javascript_value(session, script) do
    reference = make_ref()
    test_pid = self()

    _session =
      execute_script(session, script, fn value -> send(test_pid, {reference, value}) end)

    assert_receive {^reference, value}, 2_000
    value
  end

  defp internal_fetch!(fixture, url) do
    compose!(fixture, Path.expand("../../..", __DIR__), [
      "exec",
      "-T",
      "fake-provider",
      "/fake-provider",
      "fetch",
      "--url",
      url
    ])
  end

  defp grafana_query!(fixture, query) do
    port = to_string(fixture["https_port"])

    {output, status} =
      System.cmd(
        "curl",
        [
          "--silent",
          "--show-error",
          "--fail",
          "--noproxy",
          "*",
          "--insecure",
          "--resolve",
          "grafana.smoke.localhost:#{port}:127.0.0.1",
          "--user",
          "#{fixture["grafana_user"]}:#{fixture["grafana_password"]}",
          "#{fixture["grafana_url"]}/api/datasources/proxy/uid/harden-prometheus/api/v1/query?query=#{URI.encode_www_form(query)}"
        ],
        stderr_to_stdout: true
      )

    if status != 0, do: raise("Grafana query failed with status #{status}")
    output
  end

  defp public_status(fixture, host, path) do
    port = to_string(fixture["https_port"])

    {output, status} =
      System.cmd(
        "curl",
        [
          "--silent",
          "--noproxy",
          "*",
          "--insecure",
          "--output",
          "/dev/null",
          "--write-out",
          "%{http_code}",
          "--resolve",
          "#{host}:#{port}:127.0.0.1",
          "https://#{host}:#{port}#{path}"
        ],
        stderr_to_stdout: true
      )

    if status != 0, do: raise("public HTTPS probe failed with status #{status}")
    output |> String.trim() |> String.to_integer()
  end

  defp compose!(fixture, root, arguments) do
    files = Enum.flat_map(fixture["compose_files"], &["-f", &1])

    command =
      [
        "compose",
        "--env-file",
        fixture["environment_file"],
        "--project-name",
        fixture["project"]
      ] ++ files ++ arguments

    {output, status} = System.cmd("docker", command, cd: root, stderr_to_stdout: true)

    if status != 0 do
      raise "docker compose #{Enum.join(arguments, " ")} failed with status #{status}: #{String.slice(output, -4_096, 4_096)}"
    end

    output
  end

  defp eventually!(budget_ms, operation) do
    deadline = System.monotonic_time(:millisecond) + budget_ms
    retry_eventually!(deadline, operation)
  end

  defp retry_eventually!(deadline, operation) do
    result =
      try do
        operation.()
      rescue
        error -> {:error, Exception.message(error)}
      end

    case result do
      {:ok, value} ->
        value

      other ->
        if System.monotonic_time(:millisecond) < deadline do
          Process.sleep(2_000)
          retry_eventually!(deadline, operation)
        else
          raise "telemetry did not converge before deadline: #{inspect(other)}"
        end
    end
  end
end

defmodule HardenLlmWeb.ComposeFixture do
  @moduledoc false

  use GenServer

  def start(options), do: GenServer.start(__MODULE__, options)

  def await_state!(server, state_path, timeout_ms) do
    deadline = System.monotonic_time(:millisecond) + timeout_ms
    await_state_loop!(server, state_path, deadline)
  end

  def release!(server, done_path, timeout_ms) do
    File.write!(done_path, "done\n", [:sync])
    deadline = System.monotonic_time(:millisecond) + timeout_ms
    await_exit_loop!(server, deadline)
  end

  @impl true
  def init(options) do
    root = Keyword.fetch!(options, :root)
    state_path = Keyword.fetch!(options, :state_path)
    done_path = Keyword.fetch!(options, :done_path)
    work_dir = Keyword.fetch!(options, :work_dir)
    go = System.find_executable("go") || raise "Go is required for WEB-TEST-012"

    port =
      Port.open(
        {:spawn_executable, go},
        [
          :binary,
          :exit_status,
          :stderr_to_stdout,
          cd: root,
          args: [
            "test",
            "./internal/smoke",
            "-tags=compose",
            "-run",
            "^TestFrontendComposeFixture$",
            "-count=1",
            "-v"
          ],
          env: [
            {~c"HARDEN_LLM_FRONTEND_SMOKE_STATE", String.to_charlist(state_path)},
            {~c"HARDEN_LLM_FRONTEND_SMOKE_DONE", String.to_charlist(done_path)},
            {~c"HARDEN_LLM_FRONTEND_SMOKE_DIR", String.to_charlist(work_dir)}
          ]
        ]
      )

    {:ok, %{port: port, status: :running, output: ""}}
  end

  @impl true
  def handle_call(:status, _from, state), do: {:reply, {state.status, state.output}, state}

  @impl true
  def handle_info({_port, {:data, data}}, state) do
    output = String.slice(state.output <> data, -32_768, 32_768)
    {:noreply, %{state | output: output}}
  end

  def handle_info({_port, {:exit_status, status}}, state) do
    {:noreply, %{state | status: {:exited, status}}}
  end

  defp await_state_loop!(server, state_path, deadline) do
    case File.read(state_path) do
      {:ok, contents} ->
        Jason.decode!(contents)

      {:error, _reason} ->
        case GenServer.call(server, :status) do
          {{:exited, status}, output} ->
            raise "frontend Compose fixture exited with status #{status}: #{output}"

          {:running, _output} ->
            if System.monotonic_time(:millisecond) < deadline do
              Process.sleep(500)
              await_state_loop!(server, state_path, deadline)
            else
              raise "frontend Compose fixture did not become ready before the deadline"
            end
        end
    end
  end

  defp await_exit_loop!(server, deadline) do
    case GenServer.call(server, :status) do
      {{:exited, 0}, _output} ->
        :ok

      {{:exited, status}, output} ->
        raise "frontend Compose fixture cleanup exited with status #{status}: #{output}"

      {:running, _output} ->
        if System.monotonic_time(:millisecond) < deadline do
          Process.sleep(500)
          await_exit_loop!(server, deadline)
        else
          raise "frontend Compose fixture cleanup exceeded its deadline"
        end
    end
  end
end
