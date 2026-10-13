defmodule HardenLlmWeb.SharedWorkspaceTest do
  use HardenLlmWeb.ConnCase, async: true
  import Phoenix.LiveViewTest, except: [live: 1, live: 2, live: 3]
  alias HardenLlmWeb.{APIFixtures, HardenAPI}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-114
  # TEST-405's profile-free UI policy is superseded by ADR-HLLM-033.
  # Retain unavailable-history/result/no-replay, manual-model and admission oracles.
  setup %{conn: conn} do
    fixture = File.read!("../test/fixtures/reference/workspace.json") |> Jason.decode!()
    parent = self()

    Req.Test.stub(HardenAPI, fn request ->
      case {request.method, request.request_path} do
        {"GET", "/api/v1/state"} ->
          key = URI.decode_query(request.query_string)["draftKey"]
          Req.Test.json(request, %{"draftKey" => key, "revision" => 0, "payload" => %{}})

        {"POST", "/api/v1/state"} ->
          {:ok, body, request} = Plug.Conn.read_body(request)
          body = Jason.decode!(body)
          Req.Test.json(request, Map.put(body, "revision", body["revision"] + 1))

        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(request, fixture["profiles"])

        {"GET", "/api/v1/profiles/" <> id} ->
          Req.Test.json(request, fixture["details"][URI.decode(id)])

        {"POST", "/api/v1/profiles/" <> _id} ->
          request
          |> Plug.Conn.put_status(503)
          |> Req.Test.json(APIFixtures.error("model_refresh_failed"))

        {"GET", "/api/v1/history"} ->
          request
          |> Plug.Conn.put_status(503)
          |> Req.Test.json(APIFixtures.error("history_unavailable"))

        {"POST", "/api/v1/run"} ->
          {:ok, body, request} = Plug.Conn.read_body(request)
          send(parent, {:run_request, Jason.decode!(body)})

          events =
            List.update_at(fixture["stream"], 3, &put_in(&1, ["data", "status"], "not_saved"))

          body =
            Enum.map_join(events, fn event ->
              "event: " <> event["type"] <> "\ndata: " <> Jason.encode!(event) <> "\n\n"
            end)

          request
          |> Plug.Conn.put_resp_content_type("text/event-stream")
          |> Plug.Conn.send_resp(200, body)

        _ ->
          flunk("unexpected reference operation #{request.method} #{request.request_path}")
      end
    end)

    {:ok, conn: authenticated_conn(conn)}
  end

  test "history failure keeps the workspace and one inference result usable", %{conn: conn} do
    {:ok, view, _html} = live(conn, ~p"/")
    render_async(view, 1_000)
    view |> element("#history-fold-toggle") |> render_click()
    render_async(view, 1_000)
    assert has_element?(view, "#workspace-history-error")
    assert has_element?(view, "#workspace-llm-widget")
    refute has_element?(view, "#company-select")

    view
    |> form("#run-form", %{"run" => %{"userPrompt" => "one reliable call"}})
    |> render_submit()

    render_async(view, 1_000)
    assert has_element?(view, "#run-output", "ok")
    assert has_element?(view, "#recording-status[data-status='not_saved']")

    assert_receive {:run_request,
                    %{"profileId" => "CPA Responses", "userPrompt" => "one reliable call"}}

    refute_receive {:run_request, _}, 50
  end

  test "manual native model IDs remain usable when model refresh fails", %{conn: conn} do
    {:ok, view, _html} = live(conn, ~p"/")
    render_async(view, 1_000)
    view |> element("#model-config-toggle") |> render_click()
    view |> element("#profile-refresh-models") |> render_click()
    render_async(view, 1_000)

    view
    |> element("#profile_modelId")
    |> render_change(%{"profile" => %{"modelId" => "manual-model"}})

    render_async(view, 1_000)
    assert has_element?(view, "#run_modelId[value='manual-model']")
    assert has_element?(view, "#run-submit:not([disabled])")
    refute_receive {:run_request, _}, 50
  end

  test "every enabled Portal login reaches the same picker without company selection", %{
    conn: conn
  } do
    for cookie <- [
          HardenLlmWeb.AccessFixtures.cookie(),
          HardenLlmWeb.AccessFixtures.company_cookie()
        ] do
      {:ok, view, _html} = conn |> put_req_header("cookie", cookie) |> live(~p"/")
      render_async(view, 1_000)
      assert has_element?(view, "#workspace-page")
      assert has_element?(view, "#workspace-llm-widget")
      refute has_element?(view, "#company-select")
    end
  end
end
