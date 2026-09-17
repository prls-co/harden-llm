defmodule HardenLlmWeb.HistoryTraceTest do
  use HardenLlmWeb.ConnCase, async: true

  import Phoenix.LiveViewTest, except: [live: 1, live: 2, live: 3]

  alias HardenLlmWeb.{APIFixtures, HardenAPI}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-008 WEB-TEST-033 WEB-TEST-036 WEB-TEST-077 WEB-TEST-078 WEB-TEST-079 WEB-TEST-080
  setup %{conn: conn}, do: {:ok, conn: authenticated_conn(conn)}

  test "retired workspace and audit URLs have no routes or compatibility redirects", %{
    conn: conn
  } do
    install_stub(fn conn -> unexpected(conn) end)

    for path <- [
          "/workspace",
          "/workspace?trace_id=trace-test",
          "/history",
          "/history?trace_id=trace-test"
        ] do
      response = get(conn, path)
      assert response.status == 404
      assert get_resp_header(response, "location") == []
    end
  end

  test "workspace replaces the displayed Result page and supports direct numbered navigation", %{
    conn: conn
  } do
    test_pid = self()

    install_stub(fn conn ->
      assert conn.method == "GET"
      assert conn.request_path == "/api/v1/history"
      query = URI.decode_query(conn.query_string)
      assert query["limit"] == "10"
      page = String.to_integer(query["page"])
      send(test_pid, {:history_page, page})

      page =
        case page do
          1 ->
            APIFixtures.history_page(
              [APIFixtures.history_item() | APIFixtures.history_items(9, "page-one")],
              1,
              10,
              20
            )

          2 ->
            APIFixtures.history_page(
              [
                APIFixtures.history_item("run-second", "trace-second")
                | APIFixtures.history_items(9, "page-two")
              ],
              2,
              10,
              20
            )
        end

      Req.Test.json(conn, page)
    end)

    view = open_history(conn)
    assert_received {:history_page, 1}
    refute has_element?(view, "a[href='/history']")
    refute has_element?(view, "#history-page")
    refute has_element?(view, "#trace-dialog")
    assert has_element?(view, "#workspace-history-run-test.llm-result")
    view |> element("#workspace-history-pagination-page-2") |> render_click()
    render_async(view, 1_000)
    assert_received {:history_page, 2}
    assert has_element?(view, "#workspace-history-run-second.llm-result")
    refute has_element?(view, "#workspace-history-run-test.llm-result")
    assert has_element?(view, "#workspace-history-pagination-summary", "11-20 of 20")
  end

  test "page-size changes reset only History to page one and preserve trace URL state", %{
    conn: conn
  } do
    test_pid = self()

    install_stub(fn conn ->
      case conn.request_path do
        "/api/v1/history" ->
          query = URI.decode_query(conn.query_string)
          send(test_pid, {:history_request, query})

          case {query["page"], query["limit"]} do
            {"1", "10"} ->
              Req.Test.json(
                conn,
                APIFixtures.history_page(
                  [APIFixtures.history_item() | APIFixtures.history_items(9, "size-one")],
                  1,
                  10,
                  20
                )
              )

            {"2", "10"} ->
              Req.Test.json(
                conn,
                APIFixtures.history_page(
                  [
                    APIFixtures.history_item("run-second", "trace-second")
                    | APIFixtures.history_items(9, "size-two")
                  ],
                  2,
                  10,
                  20
                )
              )

            {"1", "25"} ->
              Req.Test.json(
                conn,
                APIFixtures.history_page(
                  [APIFixtures.history_item("run-wide") | APIFixtures.history_items(19, "wide")],
                  1,
                  25,
                  20
                )
              )
          end

        "/api/v1/traces/trace-test" ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.trace()))
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/?trace_id=trace-test")
    render_async(view, 1_000)
    render_async(view, 1_000)
    assert_received {:history_request, %{"page" => "1", "limit" => "10"}}

    view |> element("#workspace-history-pagination-page-2") |> render_click()
    path = assert_patch(view)

    assert URI.decode_query(URI.parse(path).query) == %{
             "trace_id" => "trace-test",
             "history_page" => "2",
             "history_page_size" => "10"
           }

    render_async(view, 1_000)
    assert_received {:history_request, %{"page" => "2", "limit" => "10"}}

    view
    |> element("#workspace-history-pagination-page-size-form")
    |> render_change(%{
      "pagination-id" => "workspace-history-pagination",
      "page-size" => "25",
      "_target" => ["page-size"]
    })

    path = assert_patch(view)

    assert URI.decode_query(URI.parse(path).query) == %{
             "trace_id" => "trace-test",
             "history_page" => "1",
             "history_page_size" => "25"
           }

    render_async(view, 1_000)
    assert_received {:history_request, %{"page" => "1", "limit" => "25"}}
    assert has_element?(view, "#workspace-history-run-wide")
    refute has_element?(view, "#workspace-history-run-second")
  end

  test "manual refresh reloads the currently displayed older page", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      query = URI.decode_query(conn.query_string)
      send(test_pid, {:manual_refresh_history_request, query})

      page = String.to_integer(query["page"])
      items = APIFixtures.history_items(10, "manual-refresh-#{page}")
      Req.Test.json(conn, APIFixtures.history_page(items, page, 10, 20))
    end)

    view = open_history(conn)
    assert_receive {:manual_refresh_history_request, %{"page" => "1", "limit" => "10"}}

    view |> element("#workspace-history-pagination-page-2") |> render_click()
    render_async(view, 1_000)
    assert_receive {:manual_refresh_history_request, %{"page" => "2", "limit" => "10"}}

    render_click(view, "refresh-history", %{})
    render_async(view, 1_000)

    assert_receive {:manual_refresh_history_request, %{"page" => "2", "limit" => "10"}}
    refute has_element?(view, "#workspace-history-refresh-changed")
    assert has_element?(view, "#workspace-history-pagination-summary", "11-20 of 20")
  end

  test "passive completion shows the rendered older-page refresh control", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/history"} ->
          query = URI.decode_query(conn.query_string)
          send(test_pid, {:passive_history_request, query})
          page = String.to_integer(query["page"])

          Req.Test.json(
            conn,
            APIFixtures.history_page(
              APIFixtures.history_items(10, "passive-#{page}"),
              page,
              10,
              20
            )
          )

        {"POST", "/api/v1/run"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.run_result()))

        {"POST", "/api/v1/state"} ->
          Req.Test.json(conn, APIFixtures.success(nil, APIFixtures.state()))

        _ ->
          unexpected(conn)
      end
    end)

    view = open_history(conn)
    assert_receive {:passive_history_request, %{"page" => "1", "limit" => "10"}}

    view |> element("#workspace-history-pagination-page-2") |> render_click()
    render_async(view, 1_000)
    assert_receive {:passive_history_request, %{"page" => "2", "limit" => "10"}}

    view |> element("#input-advanced-toggle") |> render_click()
    render_async(view, 1_000)

    view
    |> form("#run-form", %{
      "run" => %{
        "selectedProfileId" => "Primary",
        "modelId" => "model-test",
        "systemPrompt" => "",
        "userPrompt" => "passive refresh fixture",
        "callType" => "text",
        "cacheMode" => "cache",
        "schema" => ""
      }
    })
    |> render_submit()

    render_async(view, 1_000)
    assert has_element?(view, "#workspace-history-refresh-changed")

    view |> element("#workspace-history-refresh-changed") |> render_click()
    render_async(view, 1_000)

    assert_receive {:passive_history_request, %{"page" => "2", "limit" => "10"}}
    refute has_element?(view, "#workspace-history-refresh-changed")
  end

  test "an explicit History URL opens the widget even when saved UI state is folded", %{
    conn: conn
  } do
    test_pid = self()

    install_stub(
      fn conn ->
        case {conn.method, conn.request_path} do
          {"GET", "/api/v1/history"} ->
            query = URI.decode_query(conn.query_string)
            send(test_pid, {:deep_history_request, query})

            page = String.to_integer(query["page"])

            Req.Test.json(
              conn,
              APIFixtures.history_page(
                APIFixtures.history_items(10, "deep-history"),
                page,
                10,
                20
              )
            )

          {"GET", "/api/v1/traces/trace-deep"} ->
            trace =
              APIFixtures.trace()
              |> Map.put("traceId", "trace-deep")
              |> Map.update!("record", &Map.put(&1, "traceId", "trace-deep"))

            Req.Test.json(conn, APIFixtures.success(trace))

          {"POST", "/api/v1/state"} ->
            Req.Test.json(conn, APIFixtures.success(nil, APIFixtures.state()))
        end
      end,
      history_open: false
    )

    {:ok, view, _html} = live(conn, ~p"/?history_page=2&history_page_size=10")
    render_async(view, 1_000)
    render_async(view, 1_000)

    assert has_element?(view, "#workspace-history")
    assert_receive {:deep_history_request, %{"page" => "2", "limit" => "10"}}

    view |> element("#history-fold-toggle") |> render_click()
    render_async(view, 1_000)
    refute has_element?(view, "#workspace-history")

    render_patch(view, ~p"/?trace_id=trace-deep&history_page=2&history_page_size=10")
    refute has_element?(view, "#workspace-history")
  end

  test "a normal URL preserves saved folded History and lazy loading", %{conn: conn} do
    install_stub(
      fn _conn ->
        flunk("folded History must not fetch /api/v1/history")
      end,
      history_open: false
    )

    {:ok, view, _html} = live(conn, ~p"/")
    render_async(view, 1_000)
    render_async(view, 1_000)

    refute has_element?(view, "#workspace-history")
  end

  test "failed numbered navigation keeps the current page and retries the same target; duplicate clicks do not refetch",
       %{conn: conn} do
    test_pid = self()
    counter = start_supervised!({Agent, fn -> 0 end})

    install_stub(fn conn ->
      query = URI.decode_query(conn.query_string)

      case query["page"] do
        "1" ->
          Req.Test.json(
            conn,
            APIFixtures.history_page(
              [APIFixtures.history_item() | APIFixtures.history_items(9, "failed-one")],
              1,
              10,
              20
            )
          )

        "2" ->
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
              APIFixtures.history_page(
                [
                  APIFixtures.history_item("run-second")
                  | APIFixtures.history_items(9, "failed-two")
                ],
                2,
                10,
                20
              )
            )
          end
      end
    end)

    view = open_history(conn)
    view |> element("#workspace-history-pagination-page-2") |> render_click()
    assert_receive {:page_started, page_pid}
    assert has_element?(view, "#workspace-history-pagination-page-2[disabled]")

    render_click(view, "paginate-history", %{
      "pagination-id" => "workspace-history-pagination",
      "page" => "2"
    })

    send(page_pid, :release_page)
    render_async(view, 1_000)
    assert Agent.get(counter, & &1) == 1
    assert has_element?(view, "#workspace-history-error[role='alert']")
    assert has_element?(view, "#workspace-history-run-test")
    assert has_element?(view, "#workspace-history-pagination-page-2:not([disabled])")
    view |> element("#workspace-history-pagination-page-2") |> render_click()
    render_async(view, 1_000)
    assert Agent.get(counter, & &1) == 2
    assert has_element?(view, "#workspace-history-run-second")
    refute has_element?(view, "#workspace-history-error")
  end

  test "failed initial history has an explicit retry without changing the requested page", %{
    conn: conn
  } do
    counter = start_supervised!({Agent, fn -> 0 end})

    install_stub(fn conn ->
      if Agent.get_and_update(counter, &{&1, &1 + 1}) == 0 do
        unavailable(conn)
      else
        Req.Test.json(conn, APIFixtures.history_page([APIFixtures.history_item()]))
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
      case {conn.method, URI.decode_query(conn.query_string)["page"]} do
        {"GET", "1"} ->
          Req.Test.json(
            conn,
            APIFixtures.history_page(
              [APIFixtures.history_item() | APIFixtures.history_items(9, "clear-one")],
              1,
              10,
              20
            )
          )

        {"GET", "2"} ->
          send(test_pid, {:page_started, self()})

          receive do
            :release_page ->
              Req.Test.json(
                conn,
                APIFixtures.history_page(
                  [
                    APIFixtures.history_item("run-stale")
                    | APIFixtures.history_items(9, "clear-two")
                  ],
                  2,
                  10,
                  20
                )
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
    view |> element("#workspace-history-pagination-page-2") |> render_click()
    assert_receive {:page_started, page_pid}
    view |> element("#workspace-clear-history") |> render_click()
    assert_receive {:clear_started, clear_pid}
    release_request(clear_pid, :release_clear)
    refute has_element?(view, "#workspace-history .llm-result")
    assert_request_cancelled(page_pid)
    render_async(view, 1_000)
    refute has_element?(view, "#workspace-history .llm-result")
    assert has_element?(view, "#workspace-history-pagination-summary", "0 items")
    refute has_element?(view, "#workspace-history-loading")
  end

  test "inline Details retains trace observations, foldable JSON and authorized artifact links",
       %{conn: conn} do
    install_stub(fn conn ->
      case conn.request_path do
        "/api/v1/history" ->
          Req.Test.json(conn, APIFixtures.history_page([APIFixtures.history_item()]))

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
          Req.Test.json(conn, APIFixtures.history_page([APIFixtures.history_item()]))

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
    {:ok, view, _html} = live(conn, ~p"/")
    render_async(view, 1_000)
    render_async(view, 1_000)
    view
  end

  defp release_request(process, message) do
    monitor = Process.monitor(process)
    send(process, message)
    assert_receive {:DOWN, ^monitor, :process, ^process, :normal}, 1_000
  end

  defp assert_request_cancelled(process) do
    monitor = Process.monitor(process)

    assert_receive {:DOWN, ^monitor, :process, ^process, reason}, 1_000
    assert reason in [:killed, :noproc, :shutdown]
  end

  defp install_stub(handler, options \\ []) do
    history_open = Keyword.get(options, :history_open, true)

    Req.Test.stub(HardenAPI, fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/auth/session"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.principal()))

        {"GET", "/api/v1/state"} ->
          state = Map.put(APIFixtures.state(), "ui", %{"historyOpen" => history_open})
          Req.Test.json(conn, APIFixtures.success(nil, state))

        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.profiles([APIFixtures.profile_state()]))

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
