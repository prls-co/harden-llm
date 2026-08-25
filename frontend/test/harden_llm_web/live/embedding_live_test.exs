defmodule HardenLlmWeb.EmbeddingLiveTest do
  use HardenLlmWeb.ConnCase, async: true

  import Phoenix.LiveViewTest, except: [live: 1, live: 2, live: 3]

  alias HardenLlmWeb.{APIFixtures, HardenAPI}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-043
  # PLAN-HLLM-WIDGET-PARITY-001 TEST-110 TEST-113
  setup %{conn: conn}, do: {:ok, conn: authenticated_conn(conn)}

  test "host namespaces two widget instances and routes their controls independently", %{
    conn: conn
  } do
    primary = embedding_profile("Primary", "model-primary")
    secondary = embedding_profile("Secondary", "model-secondary")

    state =
      APIFixtures.state()
      |> Map.put("selectedProfileId", "Primary")
      |> Map.put("modelId", "model-primary")

    Req.Test.stub(HardenAPI, fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/auth/session"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.principal()))

        {"GET", "/api/v1/state"} ->
          Req.Test.json(conn, APIFixtures.success(nil, state))

        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => [primary, secondary]}))

        _ ->
          flunk("unexpected API call: #{conn.method} #{conn.request_path}")
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/embed/llm")
    render_async(view, 1_000)

    refute has_element?(view, "nav")
    refute has_element?(view, ~s([role="tab"]))

    for prefix <- ["embed-primary", "embed-secondary"] do
      assert has_element?(view, "##{prefix}-llm-widget")
      assert has_element?(view, "##{prefix}-run_selectedProfileId")
      assert has_element?(view, "##{prefix}-model-config-toggle")
    end

    view |> element("#embed-primary-model-config-toggle") |> render_click()
    assert has_element?(view, "#embed-primary-model-options")

    assert has_element?(
             view,
             "#embed-primary-profile_modelId-options [data-value=\"model-primary\"]"
           )

    refute has_element?(
             view,
             "#embed-primary-profile_modelId-options [data-value=\"gpt-5.6-luna\"]"
           )

    refute has_element?(view, "#embed-secondary-model-options")
    assert has_element?(view, ~s(input[type="file"][name="embed_primary_profile_bundle"]))

    view |> element("#embed-primary-profile-options-toggle") |> render_click()
    assert has_element?(view, "#embed-primary-profile-options")

    view |> element("#embed-secondary-model-config-toggle") |> render_click()
    view |> element("#embed-secondary-profile-retry-toggle") |> render_click()
    view |> element("#embed-secondary-profile-escalation-config-toggle") |> render_click()

    assert has_element?(view, "#embed-secondary-model-options")
    assert has_element?(view, "#embed-secondary-profile-escalation-config")

    assert has_element?(
             view,
             ~s(input[type="file"][name="embed_secondary_escalation_profile_bundle"])
           )

    view |> element("#embed-primary-workspace-cache-toggle") |> render_click()
    render_async(view, 1_000)

    assert has_element?(
             view,
             ~s(#embed-primary-workspace-cache-toggle[aria-label="Overwrite cache on next run"])
           )

    assert has_element?(
             view,
             ~s(#embed-secondary-workspace-cache-toggle[aria-label="Use cache"])
           )

    view
    |> element("#embed-primary-run_selectedProfileId")
    |> render_change(%{"run" => %{"selectedProfileId" => "Secondary"}})

    assert has_element?(view, ~s(#embed-primary-run_selectedProfileId[value="Secondary"]))
    assert has_element?(view, ~s(#embed-secondary-run_selectedProfileId[value="Primary"]))

    ids =
      Regex.scan(~r/\bid="([^"]+)"/, render(view), capture: :all_but_first)
      |> List.flatten()

    assert length(ids) == length(Enum.uniq(ids)), "duplicate DOM ids found"
  end

  defp embedding_profile(profile_id, model_id) do
    APIFixtures.profile_state()
    |> put_in(["profile", "llmProfile"], profile_id)
    |> put_in(["profile", "modelId"], model_id)
    |> put_in(["profile", "models"], [%{"id" => model_id, "label" => model_id}])
    |> put_in(["credential", "credentialId"], "credential-#{profile_id}")
  end
end
