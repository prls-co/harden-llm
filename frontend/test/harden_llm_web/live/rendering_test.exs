defmodule HardenLlmWeb.RenderingTest do
  use HardenLlmWeb.ConnCase, async: true

  import Phoenix.LiveViewTest, except: [live: 1, live: 2, live: 3]

  alias HardenLlmWeb.{APIFixtures, HardenAPI}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-010

  setup %{conn: conn}, do: {:ok, conn: authenticated_conn(conn), anonymous_conn: build_conn()}

  test "login has a responsive landmark and explicitly labelled focus targets", %{
    anonymous_conn: conn
  } do
    html = conn |> get(~p"/login") |> html_response(200)

    assert html =~ ~s(<meta name="viewport" content="width=device-width, initial-scale=1")
    assert html =~ ~s(id="login-page")
    assert html =~ ~s(<h1 class="text-3xl)
    assert html =~ ~s(for="session_email")
    assert html =~ ~s(for="session_password")
    assert html =~ ~s(id="login-submit")
    assert html =~ "max-w-md"
    assert html =~ "w-full"
  end

  test "loading and empty states retain landmarks, labels, and stable controls", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/state"} ->
          state = Map.put(APIFixtures.state(), "ui", %{"historyOpen" => true})
          Req.Test.json(conn, APIFixtures.success(nil, state))

        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => []}))

        {"GET", "/api/v1/history"} ->
          Req.Test.json(conn, APIFixtures.success(%{"items" => []}))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, workspace, workspace_html} = live(conn, ~p"/workspace")
    assert workspace_html =~ ~s(id="workspace-loading")
    assert workspace_html =~ ~s(role="status")
    render_async(workspace, 1_000)

    assert has_element?(workspace, "main#workspace-page h1")
    refute has_element?(workspace, "nav")
    refute has_element?(workspace, ~s([role="tab"]))
    assert has_element?(workspace, ~s(label[for="run_selectedProfileId"]), "Profile")
    assert has_element?(workspace, ~s(label[for="run_userPrompt"]), "Prompt")
    assert has_element?(workspace, "#run-submit")
    assert has_element?(workspace, "#run-submit[formnovalidate]")
    assert has_element?(workspace, "#workspace-history", "No runs yet in this session.")

    assert has_element?(workspace, ".studio-stack")

    refute has_element?(
             workspace,
             ".lg\\:grid-cols-\\[minmax\\(0\\,1\\.35fr\\)_minmax\\(20rem\\,0\\.65fr\\)\\]"
           )

    {:ok, profiles, profiles_html} = live(conn, ~p"/profiles")
    assert profiles_html =~ ~s(id="profiles-loading")
    render_async(profiles, 1_000)

    assert has_element?(profiles, "main#profiles-page h1")
    assert has_element?(profiles, "#new-profile")
    assert has_element?(profiles, "#export-bundle")
    assert has_element?(profiles, "#bundle-import-form label", "Encrypted profile bundle JSON")
    assert has_element?(profiles, "#bundle-import-submit")
    assert has_element?(profiles, "#profiles-empty", "No profiles available.")
    assert has_element?(profiles, "#profiles")
    refute has_element?(profiles, ".overflow-x-auto table")

    {:ok, history, history_html} = live(conn, ~p"/history")
    assert history_html =~ ~s(id="history-loading")
    render_async(history, 1_000)

    assert has_element?(history, "main#history-page h1")
    assert has_element?(history, "#clear-history")
    assert has_element?(history, "#history-empty", "No history yet.")
    assert has_element?(history, ~s(#history-page th[scope="col"]))
    assert has_element?(history, ".overflow-x-auto table")
    refute has_element?(history, "nav")
    refute has_element?(history, ~s([role="tab"]))
    assert has_element?(history, "#logout-button")
  end

  test "success states bound long backend values without removing their full accessible text", %{
    conn: conn
  } do
    long_profile = "Profile" <> String.duplicate("X", 240)
    long_run = "run-" <> String.duplicate("r", 240)
    long_trace = "trace-" <> String.duplicate("t", 240)
    long_output = String.duplicate("bounded output ", 300)
    trace_path = "/api/v1/traces/#{long_trace}"

    profile =
      APIFixtures.profile_state()
      |> put_in(["profile", "llmProfile"], long_profile)
      |> put_in(["profile", "baseUrl"], "https://#{String.duplicate("a", 120)}.example.test/v1")

    history = APIFixtures.history_item(long_run, long_trace, long_profile)

    run_result =
      APIFixtures.run_result()
      |> Map.put("runId", long_run)
      |> Map.put("traceId", long_trace)
      |> Map.put("output", long_output)

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/state"} ->
          state =
            APIFixtures.state()
            |> Map.put("selectedProfileId", long_profile)
            |> Map.put("ui", %{"historyOpen" => true})

          Req.Test.json(conn, APIFixtures.success(nil, state))

        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => [profile]}))

        {"GET", "/api/v1/history"} ->
          Req.Test.json(conn, APIFixtures.success(%{"items" => [history]}))

        {"POST", "/api/v1/run"} ->
          Req.Test.json(conn, APIFixtures.success(run_result))

        {"GET", ^trace_path} ->
          trace =
            APIFixtures.trace()
            |> Map.put("traceId", long_trace)
            |> Map.put("record", run_result)
            |> put_in(["resources", "response", "payload"], run_result)
            |> put_in(["observations", Access.at(0), "data"], %{"output" => long_output})

          Req.Test.json(conn, APIFixtures.success(trace))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, workspace, _html} = live(conn, ~p"/workspace")
    render_async(workspace, 1_000)

    workspace |> element("#input-advanced-toggle") |> render_click()

    workspace
    |> form("#run-form", %{
      "run" => %{
        "selectedProfileId" => long_profile,
        "modelId" => "model-test",
        "systemPrompt" => "",
        "userPrompt" => "render the bounded result",
        "cacheMode" => "cache",
        "schema" => ""
      }
    })
    |> render_submit()

    render_async(workspace, 1_000)

    assert has_element?(
             workspace,
             "#run-output.ullm-result-body.ullm-mono"
           )

    assert has_element?(workspace, "#run-result-panel", long_run)
    assert has_element?(workspace, "#run-result-panel", long_trace)
    assert has_element?(workspace, ~s(#workspace-history .truncate[title="#{long_run}"]))

    {:ok, profiles, _html} = live(conn, ~p"/profiles")
    render_async(profiles, 1_000)
    assert has_element?(profiles, ~s(#profiles .break-words[title="#{long_profile}"]))
    assert has_element?(profiles, "#profiles article")
    assert has_element?(profiles, ~s(button[aria-label="Refresh models"]))
    assert has_element?(profiles, ~s(button[aria-label="Edit profile"]))
    assert has_element?(profiles, ~s(button[aria-label="Delete profile"]))

    {:ok, history_view, _html} = live(conn, ~p"/history")
    render_async(history_view, 1_000)
    assert has_element?(history_view, ~s(#history .truncate[title="#{long_run}"]))
    assert has_element?(history_view, ~s(#history .truncate[title="#{long_trace}"]))
    assert has_element?(history_view, ~s(#history .truncate[title="#{long_profile}"]))

    history_view
    |> element(~s(button[phx-click="open-trace"][phx-value-trace-id="#{long_trace}"]))
    |> render_click()

    render_async(history_view, 1_000)
    assert has_element?(history_view, ~s(#trace-title.truncate[title="#{long_trace}"]))

    assert has_element?(
             history_view,
             "#trace-observations pre.max-h-64.overflow-auto.break-words"
           )
  end

  test "backend error states are announced and trace failures leave loading state", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/state"} -> unavailable(conn)
        {"GET", "/api/v1/profiles"} -> unavailable(conn)
        {"GET", "/api/v1/history"} -> unavailable(conn)
        _ -> unexpected(conn)
      end
    end)

    {:ok, workspace, _html} = live(conn, ~p"/workspace")
    render_async(workspace, 1_000)
    assert has_element?(workspace, ~s(#workspace-unavailable[role="alert"]))
    assert has_element?(workspace, "#backend-status", "Backend unavailable")

    {:ok, profiles, _html} = live(conn, ~p"/profiles")
    render_async(profiles, 1_000)
    assert has_element?(profiles, ~s(#profiles-error[role="alert"]), "temporarily unavailable")
    refute has_element?(profiles, "#profiles-loading")

    {:ok, history, _html} = live(conn, ~p"/history")
    render_async(history, 1_000)
    assert has_element?(history, ~s(#history-error[role="alert"]), "temporarily unavailable")
    refute has_element?(history, "#history-loading")

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/history"} ->
          Req.Test.json(conn, APIFixtures.success(%{"items" => [APIFixtures.history_item()]}))

        {"GET", "/api/v1/traces/trace-test"} ->
          unavailable(conn)

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, trace_view, _html} = live(conn, ~p"/history")
    render_async(trace_view, 1_000)

    trace_view
    |> element(~s(button[phx-click="open-trace"][phx-value-trace-id="trace-test"]))
    |> render_click()

    render_async(trace_view, 1_000)

    assert has_element?(
             trace_view,
             ~s(#trace-dialog[role="dialog"][aria-labelledby="trace-title"])
           )

    assert has_element?(trace_view, ~s(#trace-error[role="alert"]), "temporarily unavailable")
    refute has_element?(trace_view, "#trace-loading")
    assert has_element?(trace_view, "#trace-dialog-close[autofocus]")
  end

  test "profile editing is an inline fold and focused dialogs remain accessible", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => [APIFixtures.profile_state()]}))

        {"GET", "/api/v1/history"} ->
          Req.Test.json(conn, APIFixtures.success(%{"items" => [APIFixtures.history_item()]}))

        {"GET", "/api/v1/traces/trace-test"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.trace()))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, profiles, _html} = live(conn, ~p"/profiles")
    render_async(profiles, 1_000)
    profiles |> element("#new-profile") |> render_click()

    assert has_element?(profiles, ~s(#profile-editor[aria-labelledby="profile-editor-title"]))
    refute has_element?(profiles, "#profile-dialog")
    refute render(profiles) =~ "fixed inset-0"
    assert has_element?(profiles, "#profile-editor-close")
    assert has_element?(profiles, "#profile-save")

    assert has_element?(
             profiles,
             ~s(#profile-form input[type="password"][autocomplete="new-password"])
           )

    profiles |> element("#profile-editor-close") |> render_click()

    profiles
    |> element(~s(button[phx-click="confirm-delete"][phx-value-id="Primary"]))
    |> render_click()

    assert has_element?(
             profiles,
             ~s(#profile-delete-panel[role="alert"][aria-labelledby="profile-delete-title"])
           )

    assert has_element?(
             profiles,
             ~s(#profile-delete-panel[aria-describedby="profile-delete-description"])
           )

    assert has_element?(profiles, "#profile-delete-cancel")
    assert has_element?(profiles, "#profile-delete-confirm")

    {:ok, history, _html} = live(conn, ~p"/history")
    render_async(history, 1_000)
    history |> element("#clear-history") |> render_click()

    assert has_element?(
             history,
             ~s(#clear-history-dialog[role="alertdialog"][aria-labelledby="clear-history-title"])
           )

    assert has_element?(
             history,
             ~s(#clear-history-dialog[aria-describedby="clear-history-description"])
           )

    assert has_element?(history, "#clear-history-cancel[autofocus]")
    assert has_element?(history, "#clear-history-confirm")
  end

  defp install_stub(handler) do
    Req.Test.stub(HardenAPI, fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/auth/session"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.principal()))

        {"GET", "/api/v1/stats"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.stats()))

        _ ->
          handler.(conn)
      end
    end)
  end

  defp unavailable(conn) do
    {status, envelope} = APIFixtures.error(503, "service_unavailable")
    conn |> Plug.Conn.put_status(status) |> Req.Test.json(envelope)
  end

  defp unexpected(conn), do: flunk("unexpected API call: #{conn.method} #{conn.request_path}")
end
