defmodule HardenLlmWeb.ShippedPresetsTest do
  use HardenLlmWeb.ConnCase, async: true

  import Phoenix.LiveViewTest, except: [live: 1, live: 2, live: 3]
  alias HardenLlmUI.{Client, DiagnosticsWire, WorkspaceSchema}
  alias HardenLlmWeb.APIFixtures

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-124
  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-452
  @catalog Path.expand("../../../../stateful/presets.json", __DIR__)
           |> File.read!()
           |> Jason.decode!()

  setup %{conn: conn} = context do
    parent = self()
    fixture = File.read!("../test/fixtures/reference/workspace.json") |> Jason.decode!()

    profiles =
      @catalog
      |> Enum.sort_by(&elem(&1, 0))
      |> Enum.map(fn {id, preset} ->
        %{
          "id" => id,
          "name" => preset["name"],
          "kind" => "preset",
          "presetId" => id,
          "definition" => preset["definition"],
          "credentialStatus" => %{
            "configured" => id != context[:missing_credentials],
            "source" => "deployment",
            "id" => preset["credentialBinding"]["deploymentUpstreamId"]
          }
        }
      end)

    Req.Test.stub(Client, fn request ->
      assert get_req_header(request, "authorization") == ["Bearer " <> APIFixtures.token()]

      case {request.method, request.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(request, %{"profiles" => profiles})

        {"GET", "/api/v1/profiles/" <> encoded_id} ->
          id = URI.decode(encoded_id)
          preset = Map.fetch!(@catalog, id)

          draft =
            preset
            |> Map.take(~w(definition inputs credentialBinding))
            |> Map.put("sourceProfileId", id)
            |> Map.put("presetId", id)
            |> Map.put("credentialStatus", %{
              "configured" => id != context[:missing_credentials],
              "source" => "deployment",
              "id" => preset["credentialBinding"]["deploymentUpstreamId"]
            })

          assert DiagnosticsWire.valid?("ProfileDraft", draft)
          Req.Test.json(request, draft)

        {"GET", "/api/v1/state"} ->
          Req.Test.json(request, %{
            "draftKey" => URI.decode_query(request.query_string)["draftKey"],
            "revision" => 0,
            "payload" => %{}
          })

        {"POST", "/api/v1/state"} ->
          {:ok, bytes, request} = Plug.Conn.read_body(request)
          body = Jason.decode!(bytes)
          # The real gateway rejects credential fields even when their value is empty.
          refute Map.has_key?(body["payload"]["profileEditor"] || %{}, "apiKey")
          send(parent, {:preset_state, body})
          Req.Test.json(request, Map.put(body, "revision", body["revision"] + 1))

        {"POST", "/api/v1/run"} ->
          {:ok, bytes, request} = Plug.Conn.read_body(request)
          body = Jason.decode!(bytes)
          assert DiagnosticsWire.valid?("RunInput", body)
          send(parent, {:preset_run, body})

          stream =
            Enum.map_join(fixture["stream"], fn event ->
              "event: " <> event["type"] <> "\ndata: " <> Jason.encode!(event) <> "\n\n"
            end)

          request |> put_resp_content_type("text/event-stream") |> send_resp(200, stream)

        other ->
          flunk("Unexpected shipped-preset operation #{inspect(other)}")
      end
    end)

    {:ok, conn: authenticated_conn(conn)}
  end

  test "fresh load and every shipped preset allow text and structured Run through the rendered control",
       %{conn: conn} do
    {:ok, view, _} = live(conn, ~p"/")
    render_settled(view, 1_000)
    assert has_element?(view, "#workspace-llm-widget")
    refute has_element?(view, "#run-submit[disabled]")
    view |> element("#input-advanced-toggle") |> render_click()
    render_settled(view, 1_000)

    for {id, preset} <- Enum.sort(@catalog) do
      assert has_element?(view, "#run_selectedProfileId-options [data-value='#{id}']")

      view
      |> element("#run_selectedProfileId")
      |> render_change(%{"run" => %{"selectedProfileId" => id}})

      render_settled(view, 1_000)
      assert has_element?(view, "#run_selectedProfileId[value='#{id}']")
      refute has_element?(view, "#draft-error")

      for call_type <- ["text", "structured"] do
        view
        |> form("#run-form", %{
          "run" => %{"userPrompt" => "Return the configured schema.", "callType" => call_type}
        })
        |> render_change()

        render_settled(view, 1_000)
        refute has_element?(view, "#run-submit[disabled]"), "#{id}: #{call_type} disabled"
        assert has_element?(view, "#run-submit[type='submit']")
        view |> form("#run-form") |> render_submit()
        render_settled(view, 1_000)
        assert_receive {:preset_run, payload}, 1_000
        assert payload["profileId"] == id
        assert payload["callType"] == call_type
        assert payload["modelId"] == preset["definition"]["modelId"]

        if call_type == "structured" do
          assert payload["schema"] == preset["inputs"]["schema"]
          assert {:ok, _, _} = WorkspaceSchema.check(Jason.encode!(payload["schema"]))
        end

        refute has_element?(view, "#run-error")
      end
    end

    view
    |> form("#run-form", %{
      "run" => %{"callType" => "structured", "schema" => ~s({"type":"object"})}
    })
    |> render_change()

    render_settled(view, 1_000)
    assert has_element?(view, "#run-submit[disabled]")
    assert has_element?(view, "#schema-status", "object schemas must define properties")
    refute_receive {:preset_run, _}, 50
  end

  @tag missing_credentials: "CPA GPT-5.6 Sol"
  test "unconfigured templates remain editable but cannot run", %{conn: conn} do
    {:ok, view, _} = live(conn, "/")
    render_settled(view, 1_000)

    view
    |> element("#run_selectedProfileId")
    |> render_change(%{"run" => %{"selectedProfileId" => "CPA GPT-5.6 Sol"}})

    render_settled(view, 1_000)
    assert has_element?(view, "#run-submit[disabled]")
    view |> element("#model-config-toggle") |> render_click()
    assert has_element?(view, "#profile-credential-toggle:not([disabled])")
    refute_receive {:preset_run, _}, 50
  end
end
