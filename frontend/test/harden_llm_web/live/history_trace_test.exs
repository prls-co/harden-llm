defmodule HardenLlmWeb.HistoryTraceTest do
  use HardenLlmWeb.ConnCase, async: true

  import Phoenix.LiveViewTest, except: [live: 1, live: 2, live: 3]

  alias HardenLlmWeb.{APIFixtures, HardenAPI}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-008

  setup %{conn: conn}, do: {:ok, conn: authenticated_conn(conn)}

  test "history stats can be refreshed explicitly", %{conn: conn} do
    test_pid = self()

    install_stub(
      fn conn ->
        case {conn.method, conn.request_path} do
          {"GET", "/api/v1/history"} ->
            Req.Test.json(conn, APIFixtures.success(%{"items" => [APIFixtures.history_item()]}))
        end
      end,
      stats: fn conn ->
        send(test_pid, :stats_request)
        Req.Test.json(conn, APIFixtures.success(APIFixtures.stats()))
      end
    )

    {:ok, view, _html} = live(conn, ~p"/history")
    render_async(view, 1_000)
    assert_received :stats_request

    view |> element("#history-stats-summary-refresh") |> render_click()
    assert_receive :stats_request, 1_000
    render_async(view, 1_000)
    assert has_element?(view, "#history-stats-summary-updated", "Last updated")
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

          second = APIFixtures.history_item("run-second", "trace-second")

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

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-036
  test "translated page-size control reloads the cursor history from page one", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path, conn.query_string} do
        {"GET", "/api/v1/history", "limit=20"} ->
          Req.Test.json(conn, APIFixtures.success(%{"items" => [APIFixtures.history_item()]}))

        {"GET", "/api/v1/history", "limit=50"} ->
          item = APIFixtures.history_item("run-page-size")
          Req.Test.json(conn, APIFixtures.success(%{"items" => [item]}))
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/history")
    render_async(view, 1_000)
    view |> form("#history-pagination", %{"pageSize" => "50"}) |> render_change()
    render_async(view, 1_000)

    assert has_element?(view, "#history-run-page-size")
    assert has_element?(view, "#history-pagination", "Page 1")
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-033
  test "history expands redacted request/result records and exposes copy controls", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/history"} ->
          Req.Test.json(conn, APIFixtures.success(%{"items" => [APIFixtures.history_item()]}))
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/history")
    render_async(view, 1_000)

    view
    |> element(~s(button[phx-click="toggle-history"][phx-value-run-id="run-test"]))
    |> render_click()

    assert has_element?(view, "#history-expanded", "Request")
    assert has_element?(view, "#history-expanded", "safe restored prompt")
    assert has_element?(view, "#history-run-stats", "Prompt tokens")
    assert has_element?(view, "#copy-history-output")
    assert has_element?(view, "#copy-history-curl")
    refute render(view) =~ APIFixtures.token()
  end

  defp install_stub(handler, options \\ []) do
    Req.Test.stub(HardenAPI, fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/auth/session"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.principal()))

        {"GET", "/api/v1/stats"} ->
          case Keyword.get(options, :stats, APIFixtures.stats()) do
            stats when is_function(stats, 1) -> stats.(conn)
            stats -> Req.Test.json(conn, APIFixtures.success(stats))
          end

        _ ->
          handler.(conn)
      end
    end)
  end
end
