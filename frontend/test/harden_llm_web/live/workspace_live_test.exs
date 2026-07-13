defmodule HardenLlmWeb.WorkspaceLiveTest do
  use HardenLlmWeb.ConnCase, async: false

  import Phoenix.LiveViewTest

  alias HardenLlmWeb.{APIFixtures, HardenAPI}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-007

  setup %{conn: conn} do
    Req.Test.set_req_test_to_shared()
    handle = APIFixtures.insert_session()
    {:ok, conn: init_test_session(conn, APIFixtures.session_map(handle))}
  end

  test "hydrates canonical state, profiles, and history", %{conn: conn} do
    install_stub(fn conn -> unexpected(conn) end)
    {:ok, view, html} = live(conn, ~p"/workspace")
    assert html =~ "Loading the canonical workspace"
    render_async(view, 1_000)

    assert has_element?(view, "#run-form")

    assert has_element?(
             view,
             ~s(#run-form select[name="run[selectedProfileId]"] option[selected][value="Primary"])
           )

    assert has_element?(view, "#workspace-history-run-test")
  end

  test "draft save sends backend-owned state", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/state"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          state = Jason.decode!(body)
          send(test_pid, {:saved_state, state})
          Req.Test.json(conn, APIFixtures.success(nil, state))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    view
    |> form("#run-form", %{
      "run" => %{
        "selectedProfileId" => "Primary",
        "modelId" => "model-test",
        "userPrompt" => "updated safe prompt",
        "callType" => "text",
        "cacheMode" => "off"
      }
    })
    |> render_change()

    render_async(view, 1_000)
    assert_received {:saved_state, %{"schemaVersion" => 1, "userPrompt" => "updated safe prompt"}}
  end

  test "one async run renders normalized result fields", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/run"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          send(test_pid, {:run_payload, Jason.decode!(body)})

          Req.Test.json(
            conn,
            APIFixtures.success(APIFixtures.run_result(), %{
              "lastRunId" => "run-test",
              "lastTraceId" => "trace-test"
            })
          )

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    submit_run(view, %{"userPrompt" => "run fixture"})
    render_async(view, 1_000)

    assert_received {:run_payload, %{"profileId" => "Primary", "userPrompt" => "run fixture"}}
    assert has_element?(view, "#run-output", "fixture output")
    assert has_element?(view, "#run-result-panel", "trace-test")
    assert has_element?(view, "#run-result-panel", "2")
  end

  test "structured calls reject invalid local JSON without backend mutation", %{conn: conn} do
    install_stub(fn conn ->
      if conn.request_path == "/api/v1/run",
        do: flunk("invalid schema reached backend"),
        else: unexpected(conn)
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    submit_run(view, %{"callType" => "structured", "schema" => "{invalid"})
    assert has_element?(view, "#run-error", "valid JSON object schema")
  end

  test "duplicate active submits are ignored and the run button alone is disabled", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/run"} ->
          send(test_pid, {:run_started, self()})

          receive do
            :release -> Req.Test.json(conn, APIFixtures.success(APIFixtures.run_result()))
          end

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)
    submit_run(view, %{"userPrompt" => "one active run"})
    assert_receive {:run_started, task_pid}, 1_000
    submit_run(view, %{"userPrompt" => "duplicate event"})
    refute_receive {:run_started, _pid}, 100
    assert has_element?(view, "#run-submit[disabled]")
    send(task_pid, :release)
    render_async(view, 1_000)
    assert has_element?(view, "#run-output")
  end

  test "ambiguous transport failure gives refresh-history guidance and performs one request", %{
    conn: conn
  } do
    {:ok, counter} = Agent.start_link(fn -> 0 end)

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/run"} ->
          Agent.update(counter, &(&1 + 1))
          Req.Test.transport_error(conn, :closed)

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)
    submit_run(view, %{"userPrompt" => "ambiguous run"})
    render_async(view, 1_000)

    assert has_element?(view, "#run-error", "Refresh History")
    assert Agent.get(counter, & &1) == 1
  end

  defp submit_run(view, overrides) do
    params =
      Map.merge(
        %{
          "selectedProfileId" => "Primary",
          "modelId" => "model-test",
          "systemPrompt" => "",
          "userPrompt" => "fixture prompt",
          "callType" => "text",
          "cacheMode" => "off",
          "structuredRepair" => "false",
          "schema" => ""
        },
        overrides
      )

    view |> form("#run-form", %{"run" => params}) |> render_submit()
  end

  defp install_stub(handler) do
    Req.Test.stub(HardenAPI, fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/auth/session"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.principal()))

        {"GET", "/api/v1/state"} ->
          Req.Test.json(conn, APIFixtures.success(nil, APIFixtures.state()))

        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => [APIFixtures.profile_state()]}))

        {"GET", "/api/v1/history"} ->
          Req.Test.json(conn, APIFixtures.success(%{"items" => [APIFixtures.history_item()]}))

        _ ->
          handler.(conn)
      end
    end)
  end

  defp unexpected(conn), do: flunk("unexpected API call: #{conn.method} #{conn.request_path}")
end
