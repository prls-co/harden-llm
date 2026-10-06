defmodule HardenLlmWeb.SharedWorkspaceTest do
  use HardenLlmWeb.ConnCase, async: true

  import Phoenix.LiveViewTest, except: [live: 1, live: 2, live: 3]

  alias HardenLlmWeb.{APIFixtures, HardenAPI}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-114
  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-405

  setup %{conn: conn}, do: {:ok, conn: authenticated_conn(conn)}

  test "the workspace stays usable without reference storage and displays a response before save failure",
       %{
         conn: conn
       } do
    parent = self()

    Req.Test.stub(HardenAPI, fn request ->
      case {request.method, request.request_path} do
        {"GET", "/v1/models"} ->
          Req.Test.json(request, APIFixtures.models())

        {"POST", "/v1/responses"} ->
          {:ok, body, request} = Plug.Conn.read_body(request)
          payload = Jason.decode!(body)
          send(parent, {:responses_request, payload})
          Req.Test.json(request, APIFixtures.response("single frontend result"))

        _ ->
          flunk("unexpected proxy call: #{request.method} #{request.request_path}")
      end
    end)

    {:ok, view, html} = live(conn, ~p"/")
    assert html =~ ~s(id="workspace-page")
    render_async(view, 1_000)

    assert has_element?(view, "#history-error", "Shared history is unavailable")
    assert has_element?(view, "#run_model")
    assert has_element?(view, "#available-models option[value='model-test']")
    refute has_element?(view, "#profile-form")
    refute has_element?(view, "#company-select")

    view
    |> form("#run-form", %{
      "run" => %{
        "model" => "model-test",
        "userPrompt" => "test the direct Responses path"
      }
    })
    |> render_submit()

    render_async(view, 1_000)
    assert has_element?(view, "#current-result", "single frontend result")
    assert has_element?(view, "#recording-error", "result is shown")
    assert_receive {:responses_request, payload}, 1_000
    assert payload["input"] == "test the direct Responses path"
    assert payload["model"] == "model-test"
    assert payload["store"] == false
    refute Map.has_key?(payload, "profileId")
    refute_receive {:responses_request, _}, 50
  end

  test "manual native model IDs keep the request form available when model listing fails", %{
    conn: conn
  } do
    Req.Test.stub(HardenAPI, fn request ->
      if request.request_path == "/v1/models" do
        request |> Plug.Conn.put_status(503) |> Req.Test.json(APIFixtures.error("unavailable"))
      else
        flunk("model listing failure must not dispatch inference")
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/")
    render_async(view, 1_000)

    assert has_element?(view, "#proxy-status", "enter a model ID")
    assert has_element?(view, "#run_model")
    assert has_element?(view, "#run-submit:not([disabled])")
  end

  test "shared login entry has one workspace path and no profile route", %{conn: conn} do
    Req.Test.stub(HardenAPI, fn request -> Req.Test.json(request, APIFixtures.models()) end)

    for cookie <- [
          HardenLlmWeb.AccessFixtures.cookie(),
          HardenLlmWeb.AccessFixtures.company_cookie()
        ] do
      {:ok, view, _html} =
        conn
        |> put_req_header("cookie", cookie)
        |> live(~p"/")

      render_async(view, 1_000)
      assert has_element?(view, "#workspace-page")
      refute has_element?(view, "#profile-form")
      refute has_element?(view, "#workspace-llm-widget")
    end

    assert Phoenix.Router.route_info(HardenLlmWeb.Router, "GET", "/profiles", "localhost") ==
             :error
  end
end
