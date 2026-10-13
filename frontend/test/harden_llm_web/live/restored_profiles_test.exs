defmodule HardenLlmWeb.RestoredProfilesTest do
  use HardenLlmWeb.ConnCase, async: true
  import Phoenix.LiveViewTest, except: [live: 1, live: 2, live: 3]
  alias HardenLlmUI.{Client, DiagnosticsWire, ProfileForm}
  alias HardenLlmWeb.APIFixtures

  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-447
  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-122 WEB-TEST-125
  setup %{conn: conn} do
    fixture = File.read!("../test/fixtures/reference/workspace.json") |> Jason.decode!()
    saved = fixture["savedProfile"]["profile"]
    detail = fixture["details"]["CPA Responses"]

    saved_detail =
      detail |> Map.put("sourceProfileId", saved["id"]) |> Map.put("presetId", "CPA Responses")

    {:ok, store} =
      start_supervised(
        {Agent,
         fn ->
           %{
             profiles: fixture["profiles"]["profiles"] ++ [saved],
             details: Map.put(fixture["details"], saved["id"], saved_detail),
             saves: [],
             fail_save: false
           }
         end}
      )

    parent = self()

    Req.Test.stub(Client, fn request ->
      assert Plug.Conn.get_req_header(request, "authorization") == [
               "Bearer " <> APIFixtures.token()
             ]

      assert Plug.Conn.get_req_header(request, "cookie") == []
      assert Plug.Conn.get_req_header(request, "x-prls-session-reference") == []

      body =
        if request.method in ["POST", "PUT"] do
          {:ok, bytes, _} = Plug.Conn.read_body(request)
          if bytes == "", do: %{}, else: Jason.decode!(bytes)
        else
          %{}
        end

      send(parent, {:profile_call, request.method, request.request_path, body})

      case {request.method, request.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(request, %{"profiles" => Agent.get(store, & &1.profiles)})

        {"PUT", "/api/v1/profiles/bundle"} ->
          assert DiagnosticsWire.valid?("ProfileBundle", body)
          Req.Test.json(request, %{"profiles" => Agent.get(store, & &1.profiles)})

        {"POST", "/api/v1/profiles"} ->
          assert DiagnosticsWire.valid?("SaveProfileInput", body)

          if Agent.get(store, & &1.fail_save) do
            request
            |> Plug.Conn.put_status(422)
            |> Req.Test.json(%{
              "error" => %{
                "code" => "profile_invalid",
                "message" => "Provider probe rejected the candidate.",
                "fieldErrors" => [
                  %{"field" => "definition.baseUrl", "message" => "Check the provider endpoint."}
                ]
              }
            })
          else
            count = Agent.get(store, &(length(&1.saves) + 1))

            id =
              "00000000-0000-4000-8000-" <>
                String.pad_leading(Integer.to_string(200 + count), 12, "0")

            profile =
              saved
              |> Map.put("id", id)
              |> Map.put("name", "Saved setup · 2026-10-13 · #{200 + count}")
              |> Map.put("definition", body["definition"])

            detail =
              %{
                "sourceProfileId" => id,
                "presetId" => body["presetId"],
                "definition" => body["definition"],
                "inputs" => body["inputs"],
                "credentialBinding" => body["credentialBinding"],
                "credentialStatus" => profile["credentialStatus"]
              }
              |> Map.reject(fn {_key, value} -> is_nil(value) end)

            Agent.update(store, fn state ->
              %{
                state
                | profiles: state.profiles ++ [profile],
                  details: Map.put(state.details, id, detail),
                  saves: state.saves ++ [body]
              }
            end)

            request |> Plug.Conn.put_status(201) |> Req.Test.json(%{"profile" => profile})
          end

        {"POST", "/api/v1/profiles/" <> path} ->
          id = path |> String.trim_trailing("/models:refresh") |> URI.decode()

          refreshed =
            Agent.get(store, &Map.fetch!(&1.details, id))
            |> put_in(["definition", "models"], [
              %{"id" => "fresh-native-model", "label" => "Fresh native model"}
            ])
            |> put_in(["definition", "lastModelRefreshAt"], "2026-10-13T12:00:00Z")

          Req.Test.json(request, refreshed)

        {"GET", "/api/v1/profiles/" <> id} ->
          Req.Test.json(request, Agent.get(store, &Map.fetch!(&1.details, URI.decode(id))))

        {"DELETE", "/api/v1/profiles/" <> id} ->
          id = URI.decode(id)

          Agent.update(store, fn state ->
            %{state | profiles: Enum.reject(state.profiles, &(&1["id"] == id))}
          end)

          Plug.Conn.send_resp(request, 204, "")

        _ ->
          flunk("Unexpected profile operation #{request.method} #{request.request_path}")
      end
    end)

    {:ok, conn: authenticated_conn(conn), fixture: fixture, store: store}
  end

  test "profile page preserves baseline catalog and complete editor bindings", %{
    conn: conn,
    fixture: f
  } do
    {:ok, view, _} = live(conn, "/profiles")
    render_settled(view, 1_000)
    assert has_element?(view, "#profiles-page")
    assert has_element?(view, "#new-profile")
    assert has_element?(view, "#export-bundle[href='/profiles/bundle']")
    assert has_element?(view, "#bundle-import-form")
    id = f["savedProfile"]["profile"]["id"]
    view |> element("#edit-profile-#{id}") |> render_click()
    render_settled(view, 1_000)
    assert has_element?(view, "#profile-definition-widget")

    assert has_element?(
             view,
             "#profile_modelId[value='#{f["details"]["CPA Responses"]["definition"]["modelId"]}']"
           )

    assert has_element?(view, "#profile_baseUrl[value='https://cpa.prls.co/v1']")
    assert has_element?(view, "#profile-refresh-models")
    assert has_element?(view, "#options-fold-toggle")
    assert has_element?(view, "#retry-fold-toggle")
    assert has_element?(view, "#pricing-fold-toggle")
    assert has_element?(view, "#credential-fold-toggle")
    refute_receive {:profile_call, "POST", "/api/v1/profiles", _}, 50
  end

  test "each explicit Save creates a new complete snapshot and selects its returned ID", %{
    conn: conn,
    fixture: f,
    store: store
  } do
    id = f["savedProfile"]["profile"]["id"]
    view = open_saved_editor(conn, id)

    for _ <- 1..2 do
      view |> form("#profile-form") |> render_submit()
      render_settled(view, 1_000)
    end

    saves = Agent.get(store, & &1.saves)
    assert length(saves) == 2

    for payload <- saves do
      assert payload["inputs"] == f["details"]["CPA Responses"]["inputs"]
      assert payload["definition"] == f["details"]["CPA Responses"]["definition"]
      assert payload["credentialBinding"] == f["details"]["CPA Responses"]["credentialBinding"]
    end

    profiles = Agent.get(store, & &1.profiles)
    new = Enum.take(profiles, -2)
    assert Enum.map(new, & &1["id"]) |> Enum.uniq() |> length() == 2
    assert Enum.all?(new, &String.contains?(&1["name"], "2026-10-13"))
    assert has_element?(view, "#profile-#{List.last(new)["id"]}")

    assert has_element?(
             view,
             "#profile-form input[name='profile[profileId]'][value='#{List.last(new)["id"]}']"
           )
  end

  test "refresh edits only the draft and keeps its complete inputs until explicit Save", %{
    conn: conn,
    fixture: f,
    store: store
  } do
    id = f["savedProfile"]["profile"]["id"]
    original = Agent.get(store, &Enum.find(&1.profiles, fn p -> p["id"] == id end))
    view = open_saved_editor(conn, id)
    view |> element("#profile-refresh-models") |> render_click()
    render_settled(view, 1_000)
    assert has_element?(view, "#profile-model-options option[value='fresh-native-model']")
    assert Agent.get(store, &Enum.find(&1.profiles, fn p -> p["id"] == id end)) == original
    assert Agent.get(store, & &1.saves) == []
    view |> form("#profile-form") |> render_submit()
    render_settled(view, 1_000)
    assert [payload] = Agent.get(store, & &1.saves)
    assert payload["inputs"] == f["details"]["CPA Responses"]["inputs"]

    assert payload["definition"]["models"] == [
             %{"id" => "fresh-native-model", "label" => "Fresh native model"}
           ]
  end

  test "failed Save keeps the edited definition and staged key without echoing secrets", %{
    conn: conn,
    fixture: f,
    store: store
  } do
    Agent.update(store, &%{&1 | fail_save: true})
    id = f["savedProfile"]["profile"]["id"]
    view = open_saved_editor(conn, id)

    view
    |> form("#profile-form")
    |> render_change(%{"profile" => %{"baseUrl" => "https://custom-provider.example/v1"}})

    render_settled(view, 1_000)
    view |> element("#credential-fold-toggle") |> render_click()

    view
    |> element("#stage-profile-key")
    |> render_click(%{"apiKey" => "entered-fixture-provider-secret"})

    view |> form("#profile-form") |> render_submit()
    render_settled(view, 1_000)
    assert has_element?(view, "#profile_baseUrl[value='https://custom-provider.example/v1']")
    assert render(view) =~ "Check the provider endpoint."
    refute render(view) =~ "entered-fixture-provider-secret"
    assert_receive {:profile_call, "POST", "/api/v1/profiles", body}
    assert body["credential"]["payload"]["apiKey"] == "entered-fixture-provider-secret"
    assert body["credential"]["origin"] == "https://custom-provider.example"
    assert body["credential"]["revision"] == 0
    assert body["credentialBinding"]["source"] == "entered"
  end

  test "delete hides the selected preset and explicit recreation adds a new immutable profile", %{
    conn: conn,
    fixture: f,
    store: store
  } do
    {:ok, view, _} = live(conn, "/profiles")
    render_settled(view, 1_000)

    view
    |> element("button[phx-click='confirm-delete'][phx-value-id='CPA Responses']")
    |> render_click()

    assert has_element?(view, "#profile-delete-panel")
    view |> element("#profile-delete-confirm") |> render_click()
    render_settled(view, 1_000)
    refute has_element?(view, "button[phx-click='edit'][phx-value-id='CPA Responses']")
    view |> element("#new-profile") |> render_click()
    render_settled(view, 1_000)
    assert has_element?(view, "#profile-editor")

    assert Agent.get(
             store,
             &Enum.any?(&1.profiles, fn p -> p["id"] == f["savedProfile"]["profile"]["id"] end)
           )
  end

  test "Git preset changes leave saved setups fixed", %{conn: conn, fixture: f, store: store} do
    Agent.update(store, fn state ->
      profiles =
        Enum.map(state.profiles, fn profile ->
          if profile["kind"] == "preset",
            do: put_in(profile, ["definition", "modelId"], "new-git-model"),
            else: profile
        end)

      %{state | profiles: profiles}
    end)

    id = f["savedProfile"]["profile"]["id"]
    view = open_saved_editor(conn, id)
    assert has_element?(view, "#profile_modelId[value='gpt-5.6-luna']")
    refute_receive {:profile_call, "POST", "/api/v1/profiles", _}, 50
  end

  test "encrypted bundle import calls the one REST operation and keeps the catalog", %{
    conn: conn,
    fixture: f
  } do
    {:ok, view, _} = live(conn, "/profiles")
    render_settled(view, 1_000)

    bundle = %{
      "schemaVersion" => 4,
      "bundleId" => "00000000-0000-4000-8000-000000000900",
      "createdAt" => "2026-10-13T12:00:00Z",
      "apiKeyHash" => String.duplicate("a", 64),
      "profiles" => [
        %{
          "profile" => f["savedProfile"]["profile"],
          "inputs" => f["details"]["CPA Responses"]["inputs"],
          "credentialBinding" => f["details"]["CPA Responses"]["credentialBinding"]
        }
      ],
      "hiddenPresets" => [],
      "credentials" => []
    }

    upload =
      file_input(view, "#bundle-import-form", :bundle, [
        %{name: "profiles.json", content: Jason.encode!(bundle), type: "application/json"}
      ])

    render_upload(upload, "profiles.json")

    view |> form("#bundle-import-form") |> render_submit()
    assert_receive {:profile_call, "PUT", "/api/v1/profiles/bundle", ^bundle}
    assert has_element?(view, "#profile-#{f["savedProfile"]["profile"]["id"]}")
  end

  test "repair and fresh rerun controls preserve all five leaf bindings", %{
    conn: conn,
    fixture: f,
    store: store
  } do
    id = f["savedProfile"]["profile"]["id"]
    view = open_saved_editor(conn, id)
    view |> element("#retry-fold-toggle") |> render_click()

    view
    |> element("input[phx-click='toggle-rerun'][phx-value-node-id='original']")
    |> render_click()

    render_settled(view, 1_000)

    view
    |> element("#profile-rerun-generation-profile")
    |> render_change(%{
      "profile" => %{
        "recoveryPolicy" => %{"rerun" => %{"target" => %{"profileId" => "Repair Provider"}}}
      }
    })

    render_settled(view, 1_000)
    view |> element("#profile-rerun-generation-config-toggle") |> render_click()
    view |> element("#profile-rerun-generation-config-retry-toggle") |> render_click()

    assert has_element?(
             view,
             "input[phx-click='toggle-rerun-json-repair'][phx-value-node-id='rerun']"
           )

    view
    |> element("input[phx-click='toggle-rerun-json-repair'][phx-value-node-id='rerun']")
    |> render_click()

    render_settled(view, 1_000)
    refute has_element?(view, "input[phx-click='toggle-rerun-json-repair'][checked]")

    view
    |> element("input[phx-click='toggle-rerun-json-repair'][phx-value-node-id='rerun']")
    |> render_click()

    assert has_element?(view, "input[phx-click='toggle-rerun-json-repair'][checked]")
    render_settled(view, 1_000)
    view |> form("#profile-form") |> render_submit()
    render_settled(view, 1_000)
    assert [payload] = Agent.get(store, & &1.saves)
    policy = payload["definition"]["recoveryPolicy"]
    assert policy["jsonRepair"]["initial"]["source"] == "generation"
    assert policy["jsonRepair"]["escalation"]["source"] == "generation"
    assert policy["rerun"]["target"]["source"] == "profile"
    assert policy["rerun"]["target"]["profileId"] == "Repair Provider"
    assert policy["rerun"]["jsonRepair"]["initial"]["source"] == "generation"
    assert Map.has_key?(policy["rerun"]["jsonRepair"], "escalation")
  end

  test "inherited repair stages open the shared editor and save an isolated complete snapshot", %{
    conn: conn,
    fixture: f,
    store: store
  } do
    id = f["savedProfile"]["profile"]["id"]
    view = open_saved_editor(conn, id)
    view |> element("#retry-fold-toggle") |> render_click()
    prefix = "profile-original-repair-initial"
    refute has_element?(view, "##{prefix}-config-toggle[disabled]")
    view |> element("##{prefix}-config-toggle") |> render_click()
    render_settled(view, 1_000)

    for control <- ["baseUrl", "apiInferenceType"] do
      field =
        "##{prefix}-config [name='profile[recoveryPolicy][jsonRepair][initial][#{control}]']"

      refute has_element?(view, field <> "[disabled]")
      assert has_element?(view, field)
    end

    refute has_element?(view, "##{prefix}-config-credential-toggle[disabled]")
    view |> element("##{prefix}-config-options-toggle") |> render_click()

    view
    |> element(
      "##{prefix}-config [name='profile[recoveryPolicy][jsonRepair][initial][maxTokens]']"
    )
    |> render_change(%{
      "profile" => %{
        "recoveryPolicy" => %{"jsonRepair" => %{"initial" => %{"maxTokens" => "64"}}}
      }
    })

    render_settled(view, 1_000)
    assert has_element?(view, "##{prefix}-config")

    assert has_element?(
             view,
             "##{prefix}-config [name='profile[recoveryPolicy][jsonRepair][initial][maxTokens]'][value='64']"
           )

    refute has_element?(view, "##{prefix}-config [phx-click='toggle-rerun']")
    refute has_element?(view, "##{prefix}-config [phx-click='toggle-json-repair']")
    view |> element("##{prefix}-config-save") |> render_click()
    render_settled(view, 1_000)
    assert [payload] = Agent.get(store, & &1.saves)
    assert payload["definition"]["defaultOptions"]["max_tokens"] == 64

    assert payload["inputs"]["userPrompt"] ==
             f["details"]["CPA Responses"]["inputs"]["userPrompt"]

    assert payload["credentialBinding"] == f["details"]["CPA Responses"]["credentialBinding"]
    assert has_element?(view, "#profile-form input[name='profile[profileId]'][value='#{id}']")
    new_id = Agent.get(store, &List.last(&1.profiles)["id"])
    assert has_element?(view, "##{prefix}-profile[value='#{new_id}']")
    assert has_element?(view, "#profile-original-repair-escalation-profile[value='#{id}']")
  end

  test "repair credentials and model refresh use the selected leaf only", %{
    conn: conn,
    fixture: f,
    store: store
  } do
    id = f["savedProfile"]["profile"]["id"]
    view = open_saved_editor(conn, id)
    view |> element("#retry-fold-toggle") |> render_click()
    prefix = "profile-original-repair-escalation"

    view
    |> element("##{prefix}-profile")
    |> render_change(%{
      "profile" => %{
        "recoveryPolicy" => %{
          "jsonRepair" => %{"escalation" => %{"profileId" => "Repair Provider"}}
        }
      }
    })

    view |> element("##{prefix}-config-toggle") |> render_click()
    render_settled(view, 1_000)
    refute has_element?(view, "##{prefix}-config-refresh-models[disabled]")
    view |> element("##{prefix}-config-refresh-models") |> render_click()
    render_settled(view, 1_000)

    assert_receive {:profile_call, "POST", "/api/v1/profiles/Repair%20Provider/models:refresh",
                    %{}}

    assert has_element?(view, "##{prefix}-config [data-value='fresh-native-model']")
    view |> element("##{prefix}-config-credential-toggle") |> render_click()

    view
    |> element("##{prefix}-config-stage-key")
    |> render_click(%{"api-key" => "leaf-key-fixture-sentinel"})

    refute render(view) =~ "leaf-key-fixture-sentinel"
    assert has_element?(view, "#profile-save[disabled]")
    assert has_element?(view, "##{prefix}-config-refresh-models[disabled]")
    view |> element("##{prefix}-config-save") |> render_click()
    render_settled(view, 1_000)
    assert [payload] = Agent.get(store, & &1.saves)
    assert payload["credential"]["payload"]["apiKey"] == "leaf-key-fixture-sentinel"
    assert payload["credentialBinding"]["source"] == "entered"
    assert payload["credential"]["revision"] == 0
    assert has_element?(view, "#profile-original-repair-initial-profile[value='#{id}']")
    assert has_element?(view, "#profile-form input[name='profile[profileId]'][value='#{id}']")
    refute has_element?(view, "#profile-save[disabled]")
  end

  test "all five recovery stages reuse editable profile fields with finite role controls", %{
    conn: conn,
    fixture: f
  } do
    view = open_saved_editor(conn, f["savedProfile"]["profile"]["id"])
    view |> element("#retry-fold-toggle") |> render_click()
    view |> element("#profile-rerun-toggle") |> render_click()
    view |> element("#profile-rerun-generation-config-toggle") |> render_click()
    render_settled(view, 1_000)

    for prefix <- [
          "profile-original-repair-initial",
          "profile-original-repair-escalation",
          "profile-rerun-generation-config-json-repair-initial",
          "profile-rerun-generation-config-json-repair-escalation"
        ] do
      assert has_element?(view, "##{prefix}-config-toggle:not([disabled])")
      view |> element("##{prefix}-config-toggle") |> render_click()
      render_settled(view, 1_000)
      assert has_element?(view, "##{prefix}-config input[name$='[baseUrl]']:not([disabled])")
      assert has_element?(view, "##{prefix}-config-credential-toggle:not([disabled])")
      refute has_element?(view, "##{prefix}-config [phx-click='toggle-rerun']")
      refute has_element?(view, "##{prefix}-config [phx-click='toggle-json-repair']")
      refute has_element?(view, "##{prefix}-config [phx-click='toggle-rerun-json-repair']")
    end

    assert has_element?(
             view,
             "#profile-rerun-generation-config input[name$='[baseUrl]']:not([disabled])"
           )

    refute has_element?(view, "#profile-rerun-generation-config [phx-click='toggle-rerun']")
  end

  test "credential replacement uses the entered record's exact revision and new IDs start at zero",
       %{fixture: f} do
    draft =
      f["details"]["CPA Responses"]
      |> Map.put("credentialBinding", %{
        "source" => "entered",
        "enteredCredentialId" => "credential-old"
      })
      |> Map.put("credentialStatus", %{
        "source" => "entered",
        "id" => "credential-old",
        "configured" => true,
        "revision" => 7
      })

    params = ProfileForm.profile_form(draft) |> Map.put("apiKey", "replacement-fixture-key")
    assert {:ok, payload} = ProfileForm.profile_payload(params)
    assert payload["credential"]["revision"] == 7
    assert payload["credentialBinding"]["enteredCredentialId"] == "credential-old"

    assert {:ok, created} =
             ProfileForm.profile_payload(Map.put(params, "credentialId", "credential-new"))

    assert created["credential"]["revision"] == 0
    assert created["credentialBinding"]["enteredCredentialId"] == "credential-new"
  end

  test "the UI uses only host or profile model metadata and preserves empty provider defaults", %{
    fixture: f
  } do
    assert HardenLlmUI.ProfileWidgetState.model_options(
             nil,
             [%{"id" => "provider-model", "label" => "Provider model"}],
             "manual-model"
           ) == [
             %{"id" => "provider-model", "label" => "Provider model"},
             %{"id" => "manual-model", "label" => ""}
           ]

    draft = put_in(f["details"]["CPA Responses"], ["definition", "defaultOptions"], %{})
    assert {:ok, payload} = draft |> ProfileForm.profile_form() |> ProfileForm.profile_payload()
    assert payload["definition"]["defaultOptions"] == %{}
  end

  test "shared form keeps models, timestamps, optional values and input snapshots", %{fixture: f} do
    draft =
      f["details"]["CPA Responses"]
      |> put_in(["definition", "models"], [%{"id" => "custom-native", "label" => "Custom native"}])
      |> put_in(["definition", "lastModelRefreshAt"], "2026-10-13T12:00:00Z")

    params = ProfileForm.profile_form(draft)
    assert {:ok, payload} = ProfileForm.profile_payload(params)
    assert payload["definition"] == draft["definition"]
    assert payload["inputs"] == draft["inputs"]
  end

  defp open_saved_editor(conn, id) do
    {:ok, view, _} = live(conn, "/profiles?edit=#{id}")
    # Initial catalog hydration starts a separate detail request. Await its
    # observable request before waiting for that operation's completion.
    assert_receive {:profile_call, "GET", "/api/v1/profiles/" <> ^id, _}, 1_000
    render_settled(view, 1_000)
    view
  end
end
