defmodule HardenLlmWeb.HistoryTraceTest do
  use HardenLlmWeb.ConnCase, async: true

  import Phoenix.LiveViewTest, except: [live: 1, live: 2, live: 3]

  alias HardenLlmWeb.{APIFixtures, HardenAPI}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-008 WEB-TEST-033 WEB-TEST-036 WEB-TEST-069
  setup %{conn: conn}, do: {:ok, conn: authenticated_conn(conn)}

  test "retired audit URLs redirect to the canonical workspace and preserve trace selection", %{
    conn: conn
  } do
    install_stub(fn conn -> unexpected(conn) end)
    assert conn |> get("/history") |> redirected_to() == "/workspace"

    assert conn |> get("/history?trace_id=trace-test") |> redirected_to() ==
             "/workspace?trace_id=trace-test"
  end

  test "workspace appends older Result cards by cursor without duplicating records", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      assert conn.method == "GET"
      assert conn.request_path == "/api/v1/history"
      query = URI.decode_query(conn.query_string)
      assert query["limit"] == "10"
      send(test_pid, {:history_cursor, query["cursor"]})

      page =
        case query["cursor"] do
          nil ->
            %{"items" => [APIFixtures.history_item()], "nextCursor" => "cursor-2"}

          "cursor-2" ->
            %{
              "items" => [
                APIFixtures.history_item(),
                APIFixtures.history_item("run-second", "trace-second")
              ]
            }
        end

      Req.Test.json(conn, APIFixtures.success(page))
    end)

    view = open_history(conn)
    assert_received {:history_cursor, nil}
    refute has_element?(view, "a[href='/history']")
    refute has_element?(view, "#history-page")
    refute has_element?(view, "#trace-dialog")
    view |> element("#history-trace-run-test-summary") |> render_click()
    view |> element("#workspace-history-load-more") |> render_click()
    render_async(view, 1_000)
    assert_received {:history_cursor, "cursor-2"}
    assert has_element?(view, "#workspace-history-run-second.llm-result")

    assert Enum.count(
             LazyHTML.query(LazyHTML.from_document(render(view)), "#workspace-history-run-test")
           ) == 1

    assert has_element?(view, "#history-trace-run-test-summary[aria-expanded='true']")
    refute has_element?(view, "#workspace-history-load-more")
  end

  test "failed pagination keeps existing cards and retries the same cursor; duplicate clicks do not refetch",
       %{conn: conn} do
    test_pid = self()
    counter = start_supervised!({Agent, fn -> 0 end})

    install_stub(fn conn ->
      query = URI.decode_query(conn.query_string)

      case query["cursor"] do
        nil ->
          Req.Test.json(
            conn,
            APIFixtures.success(%{
              "items" => [APIFixtures.history_item()],
              "nextCursor" => "cursor-2"
            })
          )

        "cursor-2" ->
          number = Agent.get_and_update(counter, &{&1 + 1, &1 + 1})

          if number == 1 do
            send(test_pid, {:page_started, self()})

            receive do
              :release_page -> unavailable(conn)
            after
              2_000 -> flunk("pagination request was not released")
            end
          else
            Req.Test.json(
              conn,
              APIFixtures.success(%{"items" => [APIFixtures.history_item("run-second")]})
            )
          end
      end
    end)

    view = open_history(conn)
    view |> element("#workspace-history-load-more") |> render_click()
    assert_receive {:page_started, page_pid}
    assert has_element?(view, "#workspace-history-load-more[disabled]")
    render_click(view, "load-more-history")
    send(page_pid, :release_page)
    render_async(view, 1_000)
    assert Agent.get(counter, & &1) == 1
    assert has_element?(view, "#workspace-history-error[role='alert']")
    assert has_element?(view, "#workspace-history-run-test")
    assert has_element?(view, "#workspace-history-load-more:not([disabled])")
    view |> element("#workspace-history-load-more") |> render_click()
    render_async(view, 1_000)
    assert Agent.get(counter, & &1) == 2
    assert has_element?(view, "#workspace-history-run-second")
    refute has_element?(view, "#workspace-history-error")
  end

  test "failed initial history has an explicit retry without toggling folds", %{conn: conn} do
    counter = start_supervised!({Agent, fn -> 0 end})

    install_stub(fn conn ->
      if Agent.get_and_update(counter, &{&1, &1 + 1}) == 0 do
        unavailable(conn)
      else
        Req.Test.json(conn, APIFixtures.success(%{"items" => [APIFixtures.history_item()]}))
      end
    end)

    view = open_history(conn)
    assert has_element?(view, "#workspace-history-error[role='alert']")
    view |> element("#workspace-history-retry") |> render_click()
    render_async(view, 1_000)
    assert has_element?(view, "#workspace-history-run-test")
    refute has_element?(view, "#workspace-history-error")
  end

  test "clear invalidates an in-flight older page so deleted records cannot reappear", %{
    conn: conn
  } do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, URI.decode_query(conn.query_string)["cursor"]} do
        {"GET", nil} ->
          Req.Test.json(
            conn,
            APIFixtures.success(%{
              "items" => [APIFixtures.history_item()],
              "nextCursor" => "cursor-2"
            })
          )

        {"GET", "cursor-2"} ->
          send(test_pid, {:page_started, self()})

          receive do
            :release_page ->
              Req.Test.json(
                conn,
                APIFixtures.success(%{
                  "items" => [APIFixtures.history_item("run-stale")],
                  "nextCursor" => "cursor-3"
                })
              )
          after
            2_000 -> flunk("pagination request was not released")
          end

        {"DELETE", nil} ->
          send(test_pid, {:clear_started, self()})

          receive do
            :release_clear ->
              Req.Test.json(conn, APIFixtures.success(%{"deletedCount" => 2}))
          after
            2_000 -> flunk("clear request was not released")
          end
      end
    end)

    view = open_history(conn)
    view |> element("#workspace-history-load-more") |> render_click()
    assert_receive {:page_started, page_pid}
    view |> element("#workspace-clear-history") |> render_click()
    assert_receive {:clear_started, clear_pid}
    release_request(clear_pid, :release_clear)
    refute has_element?(view, "#workspace-history .llm-result")
    release_request(page_pid, :release_page)
    render_async(view, 1_000)
    refute has_element?(view, "#workspace-history .llm-result")
    refute has_element?(view, "#workspace-history-load-more")
    refute has_element?(view, "#workspace-history-loading")
  end

  test "inline Details retains trace observations, foldable JSON and authorized artifact links",
       %{conn: conn} do
    install_stub(fn conn ->
      case conn.request_path do
        "/api/v1/history" ->
          Req.Test.json(conn, APIFixtures.success(%{"items" => [APIFixtures.history_item()]}))

        "/api/v1/traces/trace-test" ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.trace()))
      end
    end)

    view = open_history(conn)
    view |> element("#history-trace-run-test-summary") |> render_click()
    view |> element("#history-trace-run-test-view-json") |> render_click()
    render_async(view, 1_000)
    assert has_element?(view, "#history-trace-run-test-trace-json .json-viewer", "observations")
    assert has_element?(view, "#history-trace-run-test-trace-json", "fixture output")
    assert has_element?(view, "#history-trace-run-test-trace-json", "artifact-test")

    assert has_element?(
             view,
             "#history-trace-run-test-controls a[href='/traces/trace-test/artifacts/artifact-test']"
           )

    assert has_element?(view, "#workspace-history-run-test button[aria-label='Copy input']")
    assert has_element?(view, "#workspace-history-run-test button[aria-label='Copy output']")
    assert has_element?(view, "#history-trace-run-test-copy-curl")
    refute has_element?(view, "[role='dialog']")
    refute render(view) =~ "X-Amz-Signature"
    refute render(view) =~ APIFixtures.token()
  end

  test "an active inline trace 401 revokes the session", %{conn: conn} do
    install_stub(fn conn ->
      case conn.request_path do
        "/api/v1/history" ->
          Req.Test.json(conn, APIFixtures.success(%{"items" => [APIFixtures.history_item()]}))

        "/api/v1/traces/trace-test" ->
          {status, envelope} = APIFixtures.error(401, "session_expired")
          conn |> Plug.Conn.put_status(status) |> Req.Test.json(envelope)
      end
    end)

    view = open_history(conn)
    view |> element("#history-trace-run-test-summary") |> render_click()
    view |> element("#history-trace-run-test-view-json") |> render_click()
    assert_redirect(view, ~p"/session/expired", 1_000)
  end

  defp open_history(conn) do
    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)
    render_async(view, 1_000)
    view
  end

  defp release_request(process, message) do
    monitor = Process.monitor(process)
    send(process, message)
    assert_receive {:DOWN, ^monitor, :process, ^process, :normal}, 1_000
  end

  defp install_stub(handler) do
    Req.Test.stub(HardenAPI, fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/auth/session"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.principal()))

        {"GET", "/api/v1/state"} ->
          state = Map.put(APIFixtures.state(), "ui", %{"historyOpen" => true})
          Req.Test.json(conn, APIFixtures.success(nil, state))

        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => [APIFixtures.profile_state()]}))

        {"GET", "/api/v1/stats"} ->
          flunk("the workspace must not fetch retired aggregate stats")

        _ ->
          handler.(conn)
      end
    end)
  end

  defp unavailable(conn) do
    {status, envelope} = APIFixtures.error(503, "temporarily_unavailable")
    conn |> Plug.Conn.put_status(status) |> Req.Test.json(envelope)
  end

  defp unexpected(conn), do: flunk("unexpected API call: #{conn.method} #{conn.request_path}")
end
