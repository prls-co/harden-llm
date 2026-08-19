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

  test "hydrates canonical state and profiles, then loads history when opened", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/state"} ->
          Req.Test.json(conn, APIFixtures.success(nil, APIFixtures.state()))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, html} = live(conn, ~p"/workspace")
    assert html =~ "Loading the canonical workspace"
    render_async(view, 1_000)

    assert has_element?(view, "#run-form")

    assert has_element?(
             view,
             ~s(#run-form input[name="run[selectedProfileId]"][value="Primary"])
           )

    view |> element("#history-fold-toggle") |> render_click()
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

  test "profile input preserves a custom typed value and persists it", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/state"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          send(test_pid, {:custom_profile_state, Jason.decode!(body)})
          Req.Test.json(conn, APIFixtures.success(nil, APIFixtures.state()))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    view
    |> form("#run-form", %{
      "run" => %{
        "selectedProfileId" => "typed-custom-profile",
        "userPrompt" => "custom profile draft",
        "callType" => "text",
        "cacheMode" => "off"
      }
    })
    |> render_change()

    render_async(view, 1_000)

    assert has_element?(
             view,
             ~s(#run-form input[name="run[selectedProfileId]"][value="typed-custom-profile"])
           )

    assert_received {:custom_profile_state, %{"selectedProfileId" => "typed-custom-profile"}}
  end

  test "workspace history restores and deletes records through the self-hosted API", %{conn: conn} do
    test_pid = self()
    {:ok, history_calls} = Agent.start_link(fn -> 0 end)

    install_stub(
      fn conn ->
        case {conn.method, conn.request_path} do
          {"POST", "/api/v1/state"} ->
            {:ok, body, conn} = Plug.Conn.read_body(conn)
            send(test_pid, {:workspace_state, Jason.decode!(body)})
            Req.Test.json(conn, APIFixtures.success(nil, APIFixtures.state()))

          {"DELETE", "/api/v1/history/run-test"} ->
            send(test_pid, :workspace_deleted)
            Req.Test.json(conn, APIFixtures.success(%{"deleted" => true}))

          _ ->
            unexpected(conn)
        end
      end,
      history: fn conn ->
        call = Agent.get_and_update(history_calls, fn value -> {value, value + 1} end)
        page = if call == 0, do: [APIFixtures.history_item()], else: []
        Req.Test.json(conn, APIFixtures.success(%{"items" => page}))
      end
    )

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)
    view |> element("#history-fold-toggle") |> render_click()
    render_async(view, 1_000)

    view
    |> element(~s(button[phx-click="restore-history"][phx-value-run-id="run-test"]))
    |> render_click()

    render_async(view, 1_000)
    assert_received {:workspace_state, %{"userPrompt" => "safe restored prompt"}}

    assert has_element?(
             view,
             ~s(#run-form textarea[name="run[userPrompt]"]),
             "safe restored prompt"
           )

    assert has_element?(
             view,
             ~s|button[phx-click="delete-history"][phx-value-run-id="run-test"]:not([disabled])|
           )

    view
    |> element(~s(button[phx-click="delete-history"][phx-value-run-id="run-test"]))
    |> render_click()

    render_async(view, 1_000)
    assert_received :workspace_deleted
    assert Agent.get(history_calls, & &1) == 2
    refute has_element?(view, "#workspace-history-run-test")
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

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-034
  test "translated prompt shortcut, schema debounce, and output request/response controls render",
       %{
         conn: conn
       } do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/state"} ->
          Req.Test.json(conn, APIFixtures.success(nil, APIFixtures.state()))

        {"POST", "/api/v1/run"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.run_result()))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    assert render(view) =~ ~s(phx-hook="PromptShortcut")
    assert has_element?(view, ~s(#run_schema[phx-debounce="5000"]))

    submit_run(view, %{"userPrompt" => "resource controls"})
    render_async(view, 1_000)
    view |> element("#output-details-toggle") |> render_click()
    assert has_element?(view, "#show-run-request")
    assert has_element?(view, "#show-run-response")

    view |> element("#show-run-request") |> render_click()
    assert has_element?(view, "#run-request", "profileId")
    view |> element("#show-run-response") |> render_click()
    assert has_element?(view, "#run-response", "fixture output")
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

  test "missing endpoint credential gives actionable guidance without refresh-history warning", %{
    conn: conn
  } do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/run"} ->
          {status, envelope} = APIFixtures.error(422, "credential_required")
          conn |> Plug.Conn.put_status(status) |> Req.Test.json(envelope)

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)
    submit_run(view, %{"userPrompt" => "missing credential run"})
    render_async(view, 1_000)

    assert has_element?(view, "#run-error", "no configured endpoint credential")
    refute has_element?(view, "#run-error", "Refresh History")
  end

  test "an active run 401 redirects through session revocation", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/run"} ->
          {status, envelope} = APIFixtures.error(401, "session_expired")
          conn |> Plug.Conn.put_status(status) |> Req.Test.json(envelope)

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)
    submit_run(view, %{"userPrompt" => "expired session"})

    assert_redirect(view, ~p"/session/expired", 1_000)
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-031
  test "translates shorthand schemas, persists folds, and sends bounded retry controls", %{
    conn: conn
  } do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/state"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          send(test_pid, {:saved_parity_state, Jason.decode!(body)})
          Req.Test.json(conn, APIFixtures.success(nil, APIFixtures.state()))

        {"POST", "/api/v1/run"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          send(test_pid, {:parity_run, Jason.decode!(body)})
          Req.Test.json(conn, APIFixtures.success(APIFixtures.run_result()))

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
        "modelId" => "model-override",
        "userPrompt" => "schema parity",
        "callType" => "structured",
        "schemaShorthand" => ~s({"answer":"string"}),
        "schema" => "",
        "reasoningEffort" => "highest",
        "cacheMode" => "cache",
        "structuredRepair" => "true",
        "maxAttempts" => "4",
        "initialBackoffMs" => "500",
        "maximumBackoffMs" => "8000",
        "retryNetwork" => "true",
        "retryRateLimit" => "true",
        "retryServerError" => "false",
        "retryEmpty" => "true",
        "retryParse" => "true",
        "repairEscalationProfileId" => "Backup",
        "repairEscalationModelId" => "repair-model",
        "repairEscalationAttempt" => "3",
        "repairEscalationReasoning" => "highest"
      }
    })
    |> render_change()

    view |> element("#input-advanced-toggle") |> render_click()
    view |> element("#generate-schema") |> render_click()
    render_async(view, 1_000)

    assert has_element?(view, "#schema-status", "Schema generated.")
    assert_received {:saved_parity_state, %{"schemaShorthand" => ~s({"answer":"string"})}}

    view |> form("#run-form") |> render_submit()
    render_async(view, 1_000)

    assert_received {:parity_run,
                     %{
                       "profileId" => "Primary",
                       "modelId" => "model-override",
                       "reasoningEffort" => "highest",
                       "cacheMode" => "cache",
                       "maxAttempts" => 4,
                       "initialBackoffMs" => 500,
                       "repairEscalation" => %{
                         "profileId" => "Backup",
                         "modelId" => "repair-model"
                       }
                     }}
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

  defp install_stub(handler, options \\ []) do
    Req.Test.stub(HardenAPI, fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/auth/session"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.principal()))

        {"GET", "/api/v1/state"} ->
          Req.Test.json(conn, APIFixtures.success(nil, APIFixtures.state()))

        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => [APIFixtures.profile_state()]}))

        {"GET", "/api/v1/history"} ->
          case Keyword.get(options, :history) do
            history when is_function(history, 1) ->
              history.(conn)

            _ ->
              Req.Test.json(conn, APIFixtures.success(%{"items" => [APIFixtures.history_item()]}))
          end

        _ ->
          handler.(conn)
      end
    end)
  end

  defp unexpected(conn), do: flunk("unexpected API call: #{conn.method} #{conn.request_path}")
end
