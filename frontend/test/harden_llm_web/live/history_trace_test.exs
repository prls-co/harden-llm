defmodule HardenLlmWeb.HistoryTraceTest do
  use HardenLlmWeb.ConnCase, async: false

  import Phoenix.LiveViewTest

  alias HardenLlmWeb.{APIFixtures, HardenAPI}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-008

  setup %{conn: conn} do
    Req.Test.set_req_test_to_shared()
    handle = APIFixtures.insert_session()
    {:ok, conn: init_test_session(conn, APIFixtures.session_map(handle))}
  end

  test "history streams a page, appends by cursor, and deletes after success", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path, conn.query_string} do
        {"GET", "/api/v1/history", "limit=20"} ->
          Req.Test.json(
            conn,
            APIFixtures.success(%{
              "items" => [APIFixtures.history_item()],
              "nextCursor" => "cursor-2"
            })
          )

        {"GET", "/api/v1/history", query} ->
          assert URI.decode_query(query)["cursor"] == "cursor-2"

          second =
            APIFixtures.history_item()
            |> Map.put("runId", "run-second")
            |> Map.put("traceId", "trace-second")

          Req.Test.json(conn, APIFixtures.success(%{"items" => [second]}))

        {"DELETE", "/api/v1/history/run-test", _query} ->
          send(test_pid, :deleted)
          Req.Test.json(conn, APIFixtures.success(%{"deleted" => true, "runId" => "run-test"}))
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/history")
    render_async(view, 1_000)
    assert has_element?(view, "#history-run-test")
    view |> element("#history-load-more") |> render_click()
    render_async(view, 1_000)
    assert has_element?(view, "#history-run-second")

    view |> element(~s(button[phx-click="delete"][phx-value-run-id="run-test"])) |> render_click()
    render_async(view, 1_000)
    assert_received :deleted
    refute has_element?(view, "#history-run-test")
  end

  test "restore saves only backend-returned safe request fields then navigates", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/history"} ->
          Req.Test.json(conn, APIFixtures.success(%{"items" => [APIFixtures.history_item()]}))

        {"POST", "/api/v1/state"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          send(test_pid, {:restored, Jason.decode!(body)})
          Req.Test.json(conn, APIFixtures.success(nil, APIFixtures.state()))
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/history")
    render_async(view, 1_000)

    view
    |> element(~s(button[phx-click="restore"][phx-value-run-id="run-test"]))
    |> render_click()

    assert_redirect(view, ~p"/workspace")

    assert_received {:restored,
                     %{"selectedProfileId" => "Primary", "userPrompt" => "safe restored prompt"}}
  end

  test "trace observations stay ordered and artifacts use same-origin controller links", %{
    conn: conn
  } do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/history"} ->
          Req.Test.json(conn, APIFixtures.success(%{"items" => [APIFixtures.history_item()]}))

        {"GET", "/api/v1/traces/trace-test"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.trace()))
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/history")
    render_async(view, 1_000)

    view
    |> element(~s(button[phx-click="open-trace"][phx-value-trace-id="trace-test"]))
    |> render_click()

    render_async(view, 1_000)

    assert has_element?(view, "#trace-dialog")
    assert has_element?(view, "#observation-0", "result")

    assert has_element?(
             view,
             ~s(#artifact-artifact-test[href="/traces/trace-test/artifacts/artifact-test"])
           )

    refute render(view) =~ "X-Amz-Signature"
  end

  test "clear-all confirms and reloads the canonical empty stream", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/history"} ->
          Req.Test.json(conn, APIFixtures.success(%{"items" => [APIFixtures.history_item()]}))

        {"DELETE", "/api/v1/history"} ->
          Req.Test.json(conn, APIFixtures.success(%{"deletedCount" => 1}))
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/history")
    render_async(view, 1_000)
    view |> element("#clear-history") |> render_click()
    assert has_element?(view, "#clear-history-dialog")
    view |> element("#clear-history-confirm") |> render_click()
    render_async(view, 1_000)
    refute has_element?(view, "#history-run-test")
  end

  test "an active trace 401 redirects through session revocation", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/history"} ->
          Req.Test.json(conn, APIFixtures.success(%{"items" => [APIFixtures.history_item()]}))

        {"GET", "/api/v1/traces/trace-test"} ->
          {status, envelope} = APIFixtures.error(401, "session_expired")
          conn |> Plug.Conn.put_status(status) |> Req.Test.json(envelope)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/history")
    render_async(view, 1_000)

    view
    |> element(~s(button[phx-click="open-trace"][phx-value-trace-id="trace-test"]))
    |> render_click()

    assert_redirect(view, ~p"/session/expired", 1_000)
  end

  test "load-more errors clear the pending state", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path, conn.query_string} do
        {"GET", "/api/v1/history", "limit=20"} ->
          Req.Test.json(
            conn,
            APIFixtures.success(%{
              "items" => [APIFixtures.history_item()],
              "nextCursor" => "cursor-2"
            })
          )

        {"GET", "/api/v1/history", _query} ->
          {status, envelope} = APIFixtures.error(503, "temporarily_unavailable")
          conn |> Plug.Conn.put_status(status) |> Req.Test.json(envelope)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/history")
    render_async(view, 1_000)
    view |> element("#history-load-more") |> render_click()
    render_async(view, 1_000)

    assert has_element?(view, "#history-error")
    assert has_element?(view, "#history-load-more:not([disabled])")
  end

  defp install_stub(handler) do
    Req.Test.stub(HardenAPI, fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/auth/session"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.principal()))

        _ ->
          handler.(conn)
      end
    end)
  end
end
