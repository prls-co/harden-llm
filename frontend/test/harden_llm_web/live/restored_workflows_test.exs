defmodule HardenLlmWeb.RestoredWorkflowsTest do
  use HardenLlmWeb.ConnCase, async: true
  import Phoenix.LiveViewTest, except: [live: 1, live: 2, live: 3]
  alias HardenLlmUI.{Client, DiagnosticsWire}
  alias HardenLlmWeb.APIFixtures

  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-431
  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-121
  setup %{conn: conn} do
    f = File.read!("../test/fixtures/reference/workspace.json") |> Jason.decode!()

    bundle = %{
      "schemaVersion" => 4,
      "bundleId" => "00000000-0000-4000-8000-000000000011",
      "createdAt" => "2026-10-13T12:00:00Z",
      "apiKeyHash" => String.duplicate("a", 64),
      "profiles" => [],
      "hiddenPresets" => ["CPA Responses"],
      "credentials" => []
    }

    assert DiagnosticsWire.valid?("ProfileBundle", bundle)
    parent = self()

    Req.Test.stub(Client, fn request ->
      assert get_req_header(request, "authorization") == ["Bearer " <> APIFixtures.token()]
      assert get_req_header(request, "cookie") == []
      send(parent, {:workflow_call, request.method, request.request_path})

      case {request.method, request.request_path} do
        {"GET", "/api/v1/profiles/bundle"} ->
          Req.Test.json(request, bundle)

        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(request, f["profiles"])

        {"GET", "/api/v1/profiles/" <> id} ->
          Req.Test.json(request, f["details"][URI.decode(id)])

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

        {"PUT", "/api/v1/profiles/bundle"} ->
          {:ok, bytes, request} = Plug.Conn.read_body(request)
          assert Jason.decode!(bytes) == bundle
          Req.Test.json(request, f["profiles"])

        _ ->
          flunk("Unexpected workflow operation #{request.method} #{request.request_path}")
      end
    end)

    {:ok, conn: authenticated_conn(conn), bundle: bundle}
  end

  test "embedding keeps two baseline instances independent", %{conn: conn} do
    {:ok, view, _} = live(conn, "/embed/llm")
    render_settled(view, 1_000)
    assert has_element?(view, "#embedding-page")

    for prefix <- ["embed-primary", "embed-secondary"] do
      assert has_element?(view, "##{prefix}-run_selectedProfileId[value='CPA Responses']")
    end

    view |> element("#embed-primary-model-config-toggle") |> render_click()
    assert has_element?(view, "#embed-primary-model-options")
    refute has_element?(view, "#embed-secondary-model-options")

    view
    |> element("#embed-primary-run_selectedProfileId")
    |> render_change(%{"run" => %{"selectedProfileId" => "Repair Provider"}})

    render_settled(view, 1_000)
    assert has_element?(view, "#embed-primary-run_selectedProfileId[value='Repair Provider']")
    assert has_element?(view, "#embed-secondary-run_selectedProfileId[value='CPA Responses']")
  end

  test "embedding upload stays in its selected widget namespace", %{conn: conn, bundle: bundle} do
    {:ok, view, _} = live(conn, "/embed/llm")
    render_settled(view, 1_000)
    view |> element("#embed-secondary-model-config-toggle") |> render_click()

    upload =
      file_input(view, "#embed-secondary-host", :embed_secondary_profile_bundle, [
        %{name: "setup.json", content: Jason.encode!(bundle), type: "application/json"}
      ])

    render_upload(upload, "setup.json")

    view
    |> element("#embed-secondary-profile-bundle-file input")
    |> render_change(%{"widget" => "embed-secondary"})

    render_settled(view, 1_000)
    assert_receive {:workflow_call, "PUT", "/api/v1/profiles/bundle"}
    assert has_element?(view, "#embed-primary-run_selectedProfileId[value='CPA Responses']")
    refute_receive {:workflow_call, "POST", "/api/v1/run"}, 50
  end

  test "bundle download returns the same canonical setup with no-store", %{
    conn: conn,
    bundle: bundle
  } do
    result = get(conn, "/profiles/bundle")
    assert Jason.decode!(response(result, 200)) == bundle
    assert [disposition] = get_resp_header(result, "content-disposition")
    assert disposition =~ "harden-llm-profile-bundle.json"
    assert get_resp_header(result, "cache-control") == ["no-store"]
    assert get_resp_header(result, "location") == []
  end

  test "all restored routes use shared Portal admission", %{conn: _conn} do
    for path <- [
          "/profiles",
          "/embed/llm",
          "/profiles/bundle",
          "/traces/00000000-0000-4000-8000-000000000001"
        ] do
      result = get(build_conn(), path)
      assert redirected_to(result) =~ "https://portal.test/login?return_to="
    end

    refute_receive {:workflow_call, _, _}, 50
  end

  test "schema helpers preserve baseline bindings without executing inference", %{conn: conn} do
    {:ok, view, _} = live(conn, "/")
    render_settled(view, 1_000)
    view |> element("#input-advanced-toggle") |> render_click()

    view
    |> form("#run-form", %{"run" => %{"schemaShorthand" => ~s({"answer":"string"})}})
    |> render_change()

    render_settled(view, 1_000)
    view |> element("#generate-schema") |> render_click()
    render_settled(view, 1_000)
    assert has_element?(view, "#run_schema", "answer")
    view |> element("#check-schema") |> render_click()
    assert has_element?(view, "#schema-status", "valid")
    view |> element("#clear-schema") |> render_click()
    render_settled(view, 1_000)
    assert textarea(view, "#run_schema") == ""
    assert textarea(view, "#run_schemaShorthand") == ""
    refute_receive {:workflow_call, "POST", "/api/v1/run"}, 50
    refute_receive {:workflow_call, "POST", "/api/v1/profiles"}, 50
  end

  test "empty embedding catalog still permits creating a new setup", %{conn: conn} do
    Req.Test.stub(Client, fn request ->
      case request.request_path do
        "/api/v1/state" ->
          Req.Test.json(request, %{
            "draftKey" => URI.decode_query(request.query_string)["draftKey"],
            "revision" => 0,
            "payload" => %{}
          })

        "/api/v1/profiles" ->
          Req.Test.json(request, %{"profiles" => []})

        _ ->
          flunk("An empty catalog must not select an invented profile")
      end
    end)

    {:ok, view, _} = live(conn, "/embed/llm")
    render_settled(view, 1_000)
    assert has_element?(view, "#embedding-surface")
    view |> element("#embed-primary-model-config-toggle") |> render_click()
    assert has_element?(view, "#embed-primary-profile-new")
    refute has_element?(view, "#embedding-unavailable")
    {:ok, workspace, _} = live(conn, "/")
    render_settled(workspace, 1_000)
    assert has_element?(workspace, "#workspace-llm-widget")
    refute has_element?(workspace, "#workspace-unavailable")
    assert has_element?(workspace, "#run-submit[disabled]")
  end

  defp textarea(view, selector),
    do:
      view |> render() |> LazyHTML.from_document() |> LazyHTML.query(selector) |> LazyHTML.text()
end
