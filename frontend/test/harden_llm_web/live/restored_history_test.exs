defmodule HardenLlmWeb.RestoredHistoryTest do
  use HardenLlmWeb.ConnCase, async: true
  import Phoenix.LiveViewTest, except: [live: 1, live: 2, live: 3]
  alias HardenLlmUI.Client

  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-448
  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-123
  setup %{conn: conn} do
    f = File.read!("../test/fixtures/reference/workspace.json") |> Jason.decode!()

    {:ok, store} =
      start_supervised(
        {Agent,
         fn ->
           %{
             failed: false,
             partial_clear: false,
             deleted: false,
             delete_failed: false,
             artifact_status: 200
           }
         end}
      )

    parent = self()
    raw = "{\n  \"schemaVersion\": 2, \"fixture\": \"retained bytes\"\n}\n"

    Req.Test.stub(Client, fn request ->
      send(
        parent,
        {:history_call, request.method, request.request_path,
         URI.decode_query(request.query_string)}
      )

      case {request.method, request.request_path} do
        {"GET", "/api/v1/state"} ->
          Req.Test.json(request, %{
            "draftKey" => URI.decode_query(request.query_string)["draftKey"],
            "revision" => 0,
            "payload" => %{}
          })

        {"POST", "/api/v1/state"} ->
          {:ok, bytes, request} = Plug.Conn.read_body(request)
          body = Jason.decode!(bytes)
          Req.Test.json(request, Map.put(body, "revision", body["revision"] + 1))

        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(request, f["profiles"])

        {"GET", "/api/v1/profiles/" <> id} ->
          Req.Test.json(request, f["details"][URI.decode(id)])

        {"GET", "/api/v1/history"} ->
          if Agent.get(store, & &1.failed) do
            request
            |> Plug.Conn.put_status(503)
            |> Req.Test.json(%{
              "error" => %{"code" => "history_read_failed", "message" => "History unavailable."}
            })
          else
            query = URI.decode_query(request.query_string)
            page = String.to_integer(query["page"] || "1")
            items = if Agent.get(store, & &1.deleted), do: [], else: f["history"]["items"]

            response =
              f["history"]
              |> Map.put("items", items)
              |> put_in(["pagination", "page"], page)
              |> put_in(["pagination", "totalCount"], if(items == [], do: 0, else: 26))

            Req.Test.json(request, response)
          end

        {"GET", "/api/v1/stats"} ->
          Req.Test.json(request, f["statistics"])

        {"GET", "/api/v1/history/" <> _id} ->
          Req.Test.json(request, f["record"])

        {"DELETE", "/api/v1/history/" <> _id} ->
          if Agent.get(store, & &1.delete_failed) do
            request
            |> Plug.Conn.put_status(502)
            |> Req.Test.json(%{
              "error" => %{
                "code" => "artifact_cleanup_failed",
                "message" => "Artifact cleanup failed."
              }
            })
          else
            Agent.update(store, &%{&1 | deleted: true})
            Plug.Conn.send_resp(request, 204, "")
          end

        {"DELETE", "/api/v1/history"} ->
          if Agent.get(store, & &1.partial_clear) do
            Req.Test.json(request, %{
              "deleted" => 1,
              "complete" => false,
              "cutoff" => "2026-10-13T12:00:00Z",
              "failedRunId" => f["record"]["runId"],
              "error" => %{
                "code" => "artifact_cleanup_failed",
                "message" => "Artifact cleanup failed; retry deletion to finish."
              }
            })
          else
            Agent.update(store, &%{&1 | deleted: true})

            Req.Test.json(request, %{
              "deleted" => 1,
              "complete" => true,
              "cutoff" => "2026-10-13T12:00:00Z"
            })
          end

        {"GET", "/api/v1/traces/" <> _id} ->
          case Agent.get(store, & &1.artifact_status) do
            200 ->
              request
              |> Plug.Conn.put_resp_header(
                "content-disposition",
                "attachment; filename=trace.json"
              )
              |> Plug.Conn.put_resp_content_type("application/json")
              |> Plug.Conn.resp(200, raw)

            status ->
              request
              |> Plug.Conn.put_status(status)
              |> Req.Test.json(%{
                "error" => %{
                  "code" => "artifact_not_found",
                  "message" => "Artifact unavailable."
                }
              })
          end

        _ ->
          flunk("Unexpected history operation #{request.method} #{request.request_path}")
      end
    end)

    {:ok, conn: authenticated_conn(conn), f: f, store: store, raw: raw}
  end

  test "numbered pages load full captured content only through the existing details control", %{
    conn: conn,
    f: f
  } do
    {:ok, view, _} = live(conn, "/")
    render_settled(view, 1_000)
    view |> element("#history-fold-toggle") |> render_click()
    render_settled(view, 1_000)
    id = f["record"]["runId"]
    assert has_element?(view, "#workspace-history-pagination-page-2")
    refute_receive {:history_call, "GET", "/api/v1/history/" <> _id, _}, 50
    view |> element("#history-trace-#{id}-summary") |> render_click()
    render_settled(view, 1_000)
    assert_receive {:history_call, "GET", "/api/v1/history/" <> ^id, _}
    assert has_element?(view, "#workspace-history-#{id}", "Return an object with ok true.")
    assert has_element?(view, "#workspace-history-#{id}", "ok")
    view |> element("#workspace-history-pagination-page-2") |> render_click()
    render_settled(view, 1_000)
    assert_receive {:history_call, "GET", "/api/v1/history", %{"page" => "2"}}
  end

  test "trace resource buttons use run ID and show the canonical raw trace", %{conn: conn, f: f} do
    {:ok, view, _} = live(conn, "/")
    render_settled(view, 1_000)
    view |> element("#history-fold-toggle") |> render_click()
    render_settled(view, 1_000)
    id = f["record"]["runId"]
    view |> element("#history-trace-#{id}-summary") |> render_click()
    render_settled(view, 1_000)
    view |> element("#history-trace-#{id}-view-json") |> render_click()
    render_settled(view, 1_000)
    assert_receive {:history_call, "GET", "/api/v1/traces/" <> ^id, _}
    assert has_element?(view, "#history-trace-#{id}-trace-json", "retained bytes")
  end

  test "partial clear is explicit and preserves rows until cleanup finishes", %{
    conn: conn,
    f: f,
    store: store
  } do
    Agent.update(store, &%{&1 | partial_clear: true})
    {:ok, view, _} = live(conn, "/")
    render_settled(view, 1_000)
    view |> element("#history-fold-toggle") |> render_click()
    render_settled(view, 1_000)
    view |> element("button[phx-click='clear-history']") |> render_click()
    render_settled(view, 1_000)
    assert has_element?(view, "#workspace-history-error", "Artifact cleanup failed")
    assert has_element?(view, "#workspace-history-#{f["record"]["runId"]}")
  end

  test "failed list is unavailable rather than an empty history", %{conn: conn, store: store} do
    Agent.update(store, &%{&1 | failed: true})
    {:ok, view, _} = live(conn, "/")
    render_settled(view, 1_000)
    view |> element("#history-fold-toggle") |> render_click()
    render_settled(view, 1_000)
    assert has_element?(view, "#workspace-history-error")
    refute has_element?(view, "#workspace-history-pagination")
    refute render(view) =~ "No runs yet"
  end

  test "artifact bytes download through the host without a history read or external redirect", %{
    conn: conn,
    f: f,
    raw: raw
  } do
    id = f["record"]["runId"]
    result = get(conn, "/traces/#{id}/artifacts/trace.json")
    assert response(result, 200) == raw
    assert get_resp_header(result, "content-type") == ["application/json; charset=utf-8"]
    assert get_resp_header(result, "content-disposition") == ["attachment; filename=trace.json"]
    assert get_resp_header(result, "cache-control") == ["no-store"]
    assert get_resp_header(result, "location") == []
    assert_receive {:history_call, "GET", "/api/v1/traces/" <> path, _}
    assert path == id <> "/artifacts/trace.json"
    refute_receive {:history_call, "GET", "/api/v1/history/" <> _id, _}, 50
  end

  test "conversation selection restores the full result and loads raw trace separately", %{
    conn: conn,
    f: f
  } do
    id = f["record"]["runId"]
    {:ok, view, _} = live(conn, "/?trace_id=" <> id)
    render_settled(view, 1_000)
    assert_receive {:history_call, "GET", "/api/v1/history/" <> ^id, _}
    render_settled(view, 1_000)
    assert has_element?(view, "#run-output", "ok")
    refute_receive {:history_call, "GET", "/api/v1/traces/" <> _path, _}, 50
    view |> element("#output-trace-view-json") |> render_click()
    render_settled(view, 1_000)
    assert_receive {:history_call, "GET", "/api/v1/traces/" <> ^id, _}
    assert has_element?(view, "#output-trace-trace-json", "retained bytes")
  end

  test "failed deletion restores the row and successful deletion refreshes its count", %{
    conn: conn,
    f: f,
    store: store
  } do
    Agent.update(store, &%{&1 | delete_failed: true})
    {:ok, view, _} = live(conn, "/")
    render_settled(view, 1_000)
    view |> element("#history-fold-toggle") |> render_click()
    render_settled(view, 1_000)
    id = f["record"]["runId"]
    selector = "#workspace-history-#{id} button[phx-click='delete-history']"
    view |> element(selector) |> render_click()
    render_settled(view, 1_000)
    assert has_element?(view, "#workspace-history-#{id}")
    assert has_element?(view, "#workspace-history-error")
    Agent.update(store, &%{&1 | delete_failed: false})
    view |> element(selector) |> render_click()
    render_settled(view, 1_000)
    refute has_element?(view, "#workspace-history-#{id}")
    assert has_element?(view, "#workspace-history-panel", "0 total")
    assert Agent.get(store, & &1.deleted)
  end

  test "unknown summary cost is a subtotal until captured accounting is loaded", %{
    conn: conn,
    f: f
  } do
    {:ok, view, _} = live(conn, "/")
    render_settled(view, 1_000)
    view |> element("#history-fold-toggle") |> render_click()
    render_settled(view, 1_000)
    id = f["record"]["runId"]
    assert has_element?(view, "#history-trace-#{id}-summary", "≥")
    view |> element("#history-trace-#{id}-summary") |> render_click()
    render_settled(view, 1_000)
    assert has_element?(view, "#history-trace-#{id}", "unavailable")
  end

  test "missing or unauthorized artifact is not redirected or retried", %{
    conn: conn,
    f: f,
    store: store
  } do
    id = f["record"]["runId"]

    for status <- [401, 403, 404] do
      Agent.update(store, &%{&1 | artifact_status: status})
      result = get(conn, "/traces/#{id}/artifacts/trace.json")
      assert response(result, status) =~ "unavailable"
      assert get_resp_header(result, "location") == []
      assert_receive {:history_call, "GET", "/api/v1/traces/" <> _path, _}
      refute_receive {:history_call, "GET", "/api/v1/traces/" <> _path, _}, 50
    end
  end

  test "trace download retains bytes and does not load product history", %{
    conn: conn,
    f: f,
    raw: raw
  } do
    result = get(conn, "/traces/#{f["record"]["runId"]}")
    assert response(result, 200) == raw
    assert get_resp_header(result, "content-disposition") == ["attachment; filename=trace.json"]
    assert get_resp_header(result, "cache-control") == ["no-store"]
    refute_receive {:history_call, "GET", "/api/v1/history/" <> _id, _}, 50
  end
end
