defmodule HardenLlmWeb.RestoredWorkflowTest do
  use HardenLlmWeb.ConnCase, async: true

  import Phoenix.LiveViewTest, except: [live: 1, live: 2, live: 3]
  alias HardenLlmWeb.{APIFixtures}
  alias HardenLlmUI.{Client}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-115
  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-428

  setup %{conn: conn} do
    fixture =
      __DIR__
      |> Path.join("../../../../test/fixtures/reference/workspace.json")
      |> File.read!()
      |> Jason.decode!()

    {:ok, agent} = start_supervised({Agent, fn -> %{} end})
    parent = self()

    Req.Test.stub(Client, fn request ->
      assert Plug.Conn.get_req_header(request, "authorization") == [
               "Bearer " <> APIFixtures.token()
             ]

      refute Enum.any?(request.req_headers, fn {name, _} -> name == "x-prls-session-ref" end)

      body =
        if request.method in ["POST", "PUT"] do
          {:ok, body, _request} = Plug.Conn.read_body(request)
          if body == "", do: %{}, else: Jason.decode!(body)
        else
          %{}
        end

      send(parent, {:reference_request, request.method, request.request_path, body})

      case {request.method, request.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(request, fixture["profiles"])

        {"GET", "/api/v1/status"} ->
          Req.Test.json(request, fixture["status"])

        {"GET", "/api/v1/history"} ->
          Req.Test.json(request, fixture["history"])

        {"GET", "/api/v1/stats"} ->
          Req.Test.json(request, fixture["statistics"])

        {"GET", "/api/v1/state"} ->
          query = URI.decode_query(request.query_string)
          key = query["draftKey"]
          state = Agent.get(agent, &Map.get(&1, key, %{"revision" => 0, "payload" => %{}}))
          Req.Test.json(request, Map.put(state, "draftKey", key))

        {"POST", "/api/v1/state"} ->
          saved =
            Map.take(body, ~w(draftKey payload)) |> Map.put("revision", body["revision"] + 1)

          Agent.update(agent, &Map.put(&1, body["draftKey"], saved))
          Req.Test.json(request, saved)

        {"POST", "/api/v1/profiles"} ->
          request |> Plug.Conn.put_status(201) |> Req.Test.json(fixture["savedProfile"])

        {"POST", "/api/v1/run"} ->
          stream_response(request, fixture["stream"])

        {"GET", "/api/v1/history/" <> _id} ->
          Req.Test.json(request, fixture["record"])

        {"GET", "/api/v1/profiles/" <> id} ->
          Req.Test.json(request, Map.fetch!(fixture["details"], URI.decode(id)))

        # Let the superseded app finish mounting so RED reaches the missing UI
        # assertion. This is a test preparation path, not a production fallback.
        {"GET", "/v1/models"} ->
          Req.Test.json(request, APIFixtures.models())

        _ ->
          flunk("unexpected reference request #{request.method} #{request.request_path}")
      end
    end)

    {:ok, conn: authenticated_conn(conn), fixture: fixture, agent: agent}
  end

  test "selecting a template fills the complete restored form and baseline controls", %{
    conn: conn,
    fixture: fixture
  } do
    {:ok, view, _html} = live(conn, ~p"/")
    render_settled(view, 1_000)
    assert has_element?(view, "#workspace-llm-widget")

    view
    |> element("#run_selectedProfileId")
    |> render_change(%{"run" => %{"selectedProfileId" => "Repair Provider"}})

    render_settled(view, 1_000)

    assert dom_value(view, "#run_userPrompt") ==
             fixture["details"]["Repair Provider"]["inputs"]["userPrompt"]

    assert dom_value(view, "#run_modelId") ==
             fixture["details"]["Repair Provider"]["inputs"]["modelId"]

    view
    |> element("#run_selectedProfileId")
    |> render_change(%{"run" => %{"selectedProfileId" => "CPA Responses"}})

    render_settled(view, 1_000)

    definition = fixture["details"]["CPA Responses"]["definition"]
    inputs = fixture["details"]["CPA Responses"]["inputs"]
    assert has_element?(view, "#run_modelId[value='#{definition["modelId"]}']")
    view |> element("#input-advanced-toggle") |> render_click()
    render_settled(view, 1_000)

    for field <- ~w(userPrompt systemPrompt schemaShorthand) do
      assert dom_value(view, "#run_#{field}") == inputs[field]
    end

    assert Jason.decode!(dom_value(view, "#run_schema")) == inputs["schema"]
    assert dom_value(view, "#run_callType") == inputs["callType"]
    assert dom_value(view, "#workspace-reasoning") == inputs["reasoningEffort"]
    assert dom_value(view, "#workspace-cache") == inputs["cacheMode"]
    assert dom_value(view, "#workspace-web-search") == to_string(inputs["webSearch"])

    view |> element("#model-config-toggle") |> render_click()
    render_settled(view, 1_000)

    for field <- ~w(apiInferenceType baseUrl modelId) do
      assert dom_value(view, "#profile_#{field}") == definition[field]
    end

    view |> element("#profile-options-toggle") |> render_click()
    render_settled(view, 1_000)

    assert Jason.decode!(dom_value(view, "#profile_defaultOptionsJson")) ==
             definition["defaultOptions"]

    assert dom_value(view, "#profile_maxTokens") ==
             to_string(definition["defaultOptions"]["max_tokens"])

    assert has_element?(view, "#run_schema")
    assert has_element?(view, "#run-submit")
    assert has_element?(view, ".ullm-input-widget")
    assert has_element?(view, ".ullm-output-widget")
    assert has_element?(view, "#history-fold-toggle")
  end

  test "explicit Save posts the complete setup and selects the newly created snapshot", %{
    conn: conn,
    fixture: fixture
  } do
    {:ok, view, _html} = live(conn, ~p"/")
    render_settled(view, 1_000)
    assert has_element?(view, "#workspace-llm-widget")

    view |> element("#model-config-toggle") |> render_click()
    view |> element("#profile-save") |> render_click()
    render_settled(view, 1_000)

    assert_receive {:reference_request, "POST", "/api/v1/profiles", payload}, 1_000
    assert payload["definition"]["schemaVersion"] == 4
    assert payload["definition"] == fixture["details"]["CPA Responses"]["definition"]
    expected_inputs = fixture["details"]["CPA Responses"]["inputs"]

    assert Map.take(payload["inputs"], Map.keys(expected_inputs)) ==
             Map.put(expected_inputs, "providerOptions", payload["definition"]["defaultOptions"])

    assert payload["inputs"]["recoveryPolicy"] == payload["definition"]["recoveryPolicy"]
    assert payload["credentialBinding"]["source"] == "deployment"

    assert has_element?(
             view,
             "#run_selectedProfileId[value='#{fixture["savedProfile"]["profile"]["id"]}']"
           )
  end

  test "run keeps the repaired result and provider/result statistics", %{conn: conn} do
    {:ok, view, _html} = live(conn, ~p"/")
    render_settled(view, 1_000)
    assert has_element?(view, "#workspace-llm-widget")

    view |> form("#run-form", %{"run" => %{"userPrompt" => "Return ok true"}}) |> render_submit()
    render_settled(view, 1_000)

    refute has_element?(view, "#run-error"),
           if(has_element?(view, "#run-error"),
             do: view |> element("#run-error") |> render(),
             else: ""
           )

    assert_receive {:reference_request, "POST", "/api/v1/run", payload}, 1_000
    assert payload["profileId"] == "CPA Responses"
    assert has_element?(view, "#run-output", "ok")
    assert has_element?(view, "#output-trace")
    assert has_element?(view, "#run-progress", "original.json_repair.initial")
    assert has_element?(view, "#output-trace", "🔁 2")

    assert has_element?(view, "#output-trace", "200") or
             has_element?(view, "#output-trace", "0.20s")

    refute_receive {:reference_request, "POST", "/api/v1/run", _}, 50
  end

  test "history restore fills recorded inputs without saving a profile or running inference", %{
    conn: conn,
    fixture: fixture
  } do
    {:ok, view, _html} = live(conn, ~p"/")
    render_settled(view, 1_000)
    assert has_element?(view, "#workspace-llm-widget")
    view |> element("#history-fold-toggle") |> render_click()
    render_settled(view, 1_000)
    id = fixture["record"]["runId"]

    view
    |> element("#workspace-history-#{id} button[phx-click='restore-history']")
    |> render_click()

    render_settled(view, 1_000)
    assert has_element?(view, "#run_userPrompt", "Return an object with ok true.")
    assert dom_value(view, "#run_selectedProfileId") == "deleted-original-profile"
    assert dom_value(view, "#run_modelId") == "recorded-model"
    view |> element("#model-config-toggle") |> render_click()
    render_settled(view, 1_000)
    assert dom_value(view, "#profile_baseUrl") == "https://recorded-provider.example/v1"
    refute_receive {:reference_request, "POST", "/api/v1/run", _}, 50
    refute_receive {:reference_request, "POST", "/api/v1/profiles", _}, 50
  end

  test "two Portal identities use separate host draft keys and share key-scoped data", %{
    conn: conn,
    fixture: fixture
  } do
    {:ok, one, _html} = live(conn, ~p"/")
    render_settled(one, 1_000)
    second_conn = conn |> put_req_header("cookie", HardenLlmWeb.AccessFixtures.second_cookie())
    {:ok, two, _html} = live(second_conn, ~p"/")
    render_settled(two, 1_000)
    assert has_element?(one, "#workspace-llm-widget")
    assert has_element?(two, "#workspace-llm-widget")

    one |> form("#run-form", %{"run" => %{"userPrompt" => "first draft"}}) |> render_change()
    two |> form("#run-form", %{"run" => %{"userPrompt" => "second draft"}}) |> render_change()
    render_settled(one, 1_000)
    render_settled(two, 1_000)

    assert_receive {:reference_request, "POST", "/api/v1/state",
                    %{"draftKey" => "workspace:fixture-user"}},
                   1_000

    assert_receive {:reference_request, "POST", "/api/v1/state",
                    %{"draftKey" => "workspace:fixture-second-user"}},
                   1_000

    assert has_element?(one, "#run_userPrompt", "first draft")
    assert has_element?(two, "#run_userPrompt", "second draft")

    for view <- [one, two] do
      assert dom_value(view, "#run_selectedProfileId") == "CPA Responses"
      view |> element("#history-fold-toggle") |> render_click()
      render_settled(view, 1_000)
      assert has_element?(view, "#workspace-history-#{fixture["record"]["runId"]}")
    end
  end

  test "a host API-key context sees only its key-scoped fixture data", %{conn: conn} do
    {:ok, view, _html} = live(conn, ~p"/")
    render_settled(view, 1_000)
    assert has_element?(view, "#workspace-llm-widget")
    key_b = "host-key-b-0123456789abcdef0123456789abcdef"

    Req.Test.stub(Client, fn request ->
      assert Plug.Conn.get_req_header(request, "authorization") == ["Bearer " <> key_b]
      assert request.request_path == "/api/v1/profiles"
      Req.Test.json(request, %{"profiles" => []})
    end)

    context =
      HardenLlmUI.Client.Context.new(
        base_url: "http://harden-api.test",
        api_key: key_b,
        timeout_ms: 75_000,
        request_options: [plug: {Req.Test, Client}]
      )

    assert {:ok, %{"profiles" => []}} = Client.list_profiles(context)
  end

  test "recording warnings are explicit and never resubmit inference", %{
    conn: conn,
    fixture: fixture
  } do
    parent = self()

    for status <- ~w(disabled configuration_error not_saved unconfirmed) do
      Req.Test.stub(Client, fn request ->
        case {request.method, request.request_path} do
          {"GET", "/api/v1/profiles"} ->
            Req.Test.json(request, fixture["profiles"])

          {"GET", "/api/v1/state"} ->
            Req.Test.json(request, %{
              "draftKey" => "workspace:fixture-user",
              "revision" => 0,
              "payload" => %{}
            })

          {"GET", "/api/v1/status"} ->
            Req.Test.json(request, fixture["status"])

          {"GET", "/api/v1/stats"} ->
            Req.Test.json(request, fixture["statistics"])

          {"POST", "/api/v1/state"} ->
            Req.Test.json(request, %{
              "draftKey" => "workspace:fixture-user",
              "revision" => 1,
              "payload" => %{}
            })

          {"POST", "/api/v1/run"} ->
            send(parent, {:warning_run, status})

            outcome = %{
              "mode" => if(status == "disabled", do: "disabled", else: "enabled"),
              "status" => status,
              "runId" => fixture["record"]["runId"]
            }

            events = List.update_at(fixture["stream"], 3, &Map.put(&1, "data", outcome))
            stream_response(request, events)

          {"GET", "/api/v1/profiles/" <> id} ->
            Req.Test.json(request, fixture["details"][URI.decode(id)])

          {"GET", "/api/v1/history"} ->
            Req.Test.json(request, fixture["history"])

          {"GET", "/v1/models"} ->
            Req.Test.json(request, APIFixtures.models())

          _ ->
            flunk("warning must not use another execution path")
        end
      end)

      {:ok, view, _html} = live(conn, ~p"/")
      render_settled(view, 1_000)
      assert has_element?(view, "#workspace-llm-widget")

      view
      |> form("#run-form", %{"run" => %{"userPrompt" => "Return ok true"}})
      |> render_submit()

      render_settled(view, 1_000)
      assert has_element?(view, "#run-output", "ok")
      assert has_element?(view, "#recording-status[data-status='#{status}']")
      assert_receive {:warning_run, ^status}, 1_000
      refute_receive {:warning_run, ^status}, 50
    end
  end

  test "rejected server API token does not restart Portal login", %{conn: conn} do
    Req.Test.stub(Client, fn request ->
      request
      |> Plug.Conn.put_status(401)
      |> Req.Test.json(%{
        "error" => %{"code" => "invalid_api_key", "message" => "Invalid bearer."}
      })
    end)

    {:ok, view, _html} = live(conn, ~p"/")
    render_settled(view, 1_000)
    assert has_element?(view, "#workspace-unavailable", "configured API token")
    assert has_element?(view, "#logout-button")
  end

  test "autosaved native options reload with the edited draft and execute once", %{conn: conn} do
    {:ok, one, _} = live(conn, "/")
    render_settled(one, 1_000)
    one |> element("#model-config-toggle") |> render_click()
    one |> element("#profile-options-toggle") |> render_click()
    options = %{"max_tokens" => 777, "temperature" => 0.35}

    one
    |> element("#profile_defaultOptionsJson")
    |> render_change(%{"profile" => %{"defaultOptionsJson" => Jason.encode!(options)}})

    render_settled(one, 1_000)
    {:ok, two, _} = live(conn, "/")
    render_settled(two, 1_000)
    assert Jason.decode!(dom_value(two, "#profile_defaultOptionsJson")) == options
    two |> form("#run-form") |> render_submit()
    render_settled(two, 1_000)
    assert_receive {:reference_request, "POST", "/api/v1/run", payload}, 1_000
    assert payload["providerOptions"] == options
    refute_receive {:reference_request, "POST", "/api/v1/run", _}, 50
  end

  defp dom_value(view, selector) do
    node = view |> render() |> LazyHTML.from_document() |> LazyHTML.query(selector)

    case LazyHTML.tag(node) do
      ["textarea"] ->
        LazyHTML.text(node)

      ["select"] ->
        node |> LazyHTML.query("option[selected]") |> LazyHTML.attribute("value") |> hd()

      _ ->
        node |> LazyHTML.attribute("value") |> hd()
    end
  end

  defp stream_response(request, events) do
    body =
      Enum.map_join(events, fn event ->
        "event: " <> event["type"] <> "\ndata: " <> Jason.encode!(event) <> "\n\n"
      end)

    request
    |> Plug.Conn.put_resp_content_type("text/event-stream")
    |> Plug.Conn.send_resp(200, body)
  end
end
