defmodule HardenLlmWeb.GatewayWorkflowsTest do
  use HardenLlmWeb.ConnCase, async: false
  import Phoenix.LiveViewTest, except: [live: 1, live: 2, live: 3]
  alias HardenLlmUI.Client
  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-454
  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-126
  # Serial resource: global_reference_gateway_application_configuration.
  @moduletag :database

  setup %{conn: conn} do
    Req.Test.set_req_test_to_private()
    original = Application.fetch_env!(:harden_llm, :harden_api)
    options = Application.fetch_env!(:harden_llm, :harden_api_req_options)

    Application.put_env(:harden_llm, :harden_api,
      base_url: System.fetch_env!("HARDEN_LLM_TEST_GATEWAY_URL"),
      token: System.fetch_env!("HARDEN_LLM_TEST_GATEWAY_TOKEN"),
      api_timeout_ms: 10_000
    )

    Application.put_env(:harden_llm, :harden_api_req_options, [])

    on_exit(fn ->
      Application.put_env(:harden_llm, :harden_api, original)
      Application.put_env(:harden_llm, :harden_api_req_options, options)
    end)

    context =
      HardenLlmWeb.APIContext.server("workspace:" <> "fixture-user")

    {:ok, conn: authenticated_conn(conn), context: context}
  end

  test "every shipped preset selects, persists, reloads and executes both modes through the real gateway",
       %{conn: conn, context: context} do
    {:ok, %{"profiles" => profiles}} = Client.list_profiles(context)
    {:ok, view, _} = live(conn, "/")
    render_settled(view, 10_000)
    view |> element("#input-advanced-toggle") |> render_click()
    render_settled(view, 10_000)

    for profile <- profiles do
      id = profile["id"]

      view
      |> element("#run_selectedProfileId")
      |> render_change(%{"run" => %{"selectedProfileId" => id}})

      render_settled(view, 10_000)
      refute has_element?(view, "#draft-error"), id
      {:ok, state} = Client.get_state(context)
      assert state["payload"]["selectedProfileId"] == id
      refute Map.has_key?(state["payload"]["profileEditor"], "apiKey")
      assert state["revision"] > 0

      for mode <- ["text", "structured"] do
        view
        |> form("#run-form", %{
          "run" => %{"callType" => mode, "userPrompt" => "Return the fixture object."}
        })
        |> render_change()

        render_settled(view, 10_000)
        refute has_element?(view, "#run-submit[disabled]"), "#{id}: #{mode}"
        view |> form("#run-form") |> render_submit()
        render_settled(view, 10_000)
        refute has_element?(view, "#run-error"), "#{id}: #{mode}: #{render(view)}"
        assert has_element?(view, "#run-output", "deterministic"), id
      end
    end

    {:ok, persisted} = Client.get_state(context)
    {:ok, reloaded, _} = live(conn, "/")
    render_settled(reloaded, 10_000)

    assert has_element?(
             reloaded,
             "#run_selectedProfileId[value='#{persisted["payload"]["selectedProfileId"]}']"
           )

    refute has_element?(reloaded, "#draft-error")
  end

  test "repair and escalation settings persist and execute the finite six-attempt chain", %{
    conn: conn,
    context: context
  } do
    {:ok, view, _} = live(conn, "/")
    render_settled(view, 10_000)

    view
    |> element("#run_selectedProfileId")
    |> render_change(%{"run" => %{"selectedProfileId" => "CPA GPT-5.6 Luna"}})

    render_settled(view, 10_000)
    view |> element("#model-config-toggle") |> render_click()
    render_settled(view, 10_000)
    view |> element("#profile-retry-toggle") |> render_click()
    render_settled(view, 10_000)

    view
    |> element("[name='profile[recoveryPolicy][maxAttempts]']")
    |> render_change(%{"profile" => %{"recoveryPolicy" => %{"maxAttempts" => "6"}}})

    render_settled(view, 10_000)
    view |> element("#profile-rerun-toggle") |> render_click()
    render_settled(view, 10_000)
    view |> element("#profile-rerun-generation-config-toggle") |> render_click()
    render_settled(view, 10_000)

    for {prefix, path, model, profile_id} <- [
          {"profile-original-repair-initial", ~w(jsonRepair initial),
           "fixture-original-repair-initial", "CPA claude-sonnet-5-5"},
          {"profile-original-repair-escalation", ~w(jsonRepair escalation),
           "fixture-original-repair-escalation", "CPA claude-sonnet-5-5"},
          {"profile-rerun-generation", ~w(rerun target), "fixture-rerun-generation",
           "CPA gpt-6.1-sol"},
          {"profile-rerun-generation-config-json-repair-initial", ~w(rerun jsonRepair initial),
           "fixture-rerun-repair-initial", "CPA claude-haiku-5-5"},
          {"profile-rerun-generation-config-json-repair-escalation",
           ~w(rerun jsonRepair escalation), "fixture-rerun-repair-escalation",
           "CPA claude-sonnet-5-5"}
        ] do
      selected = Enum.reduce(Enum.reverse(path), %{"profileId" => profile_id}, &%{&1 => &2})

      view
      |> element("##{prefix}-profile")
      |> render_change(%{"profile" => %{"recoveryPolicy" => selected}})

      render_settled(view, 10_000)

      unless prefix == "profile-rerun-generation",
        do: view |> element("##{prefix}-config-toggle") |> render_click()

      render_settled(view, 10_000)

      binding =
        "profile[recoveryPolicy]" <> Enum.map_join(path, &("[" <> &1 <> "]")) <> "[modelId]"

      nested = Enum.reduce(Enum.reverse(path), %{"modelId" => model}, &%{&1 => &2})

      view
      |> element("##{prefix}-config [name='#{binding}']")
      |> render_change(%{"profile" => %{"recoveryPolicy" => nested}})

      render_settled(view, 10_000)
      refute has_element?(view, "#draft-error")
    end

    {:ok, stored} = Client.get_state(context)
    policy = stored["payload"]["recoveryPolicy"]
    assert policy["maxAttempts"] == 6
    assert policy["jsonRepair"]["initial"]["modelId"] == "fixture-original-repair-initial"
    assert policy["jsonRepair"]["escalation"]["modelId"] == "fixture-original-repair-escalation"

    assert get_in(policy, ~w(rerun jsonRepair escalation modelId)) ==
             "fixture-rerun-repair-escalation"

    {:ok, reloaded, _} = live(conn, "/")
    render_settled(reloaded, 10_000)
    assert has_element?(reloaded, "[name='profile[recoveryPolicy][maxAttempts]'][value='6']")

    reloaded
    |> form("#run-form", %{
      "run" => %{"userPrompt" => "fixture-force-recovery"}
    })
    |> render_submit()

    render_settled(reloaded, 10_000)
    refute has_element?(reloaded, "#run-submit[disabled]")
    refute has_element?(reloaded, "#run-error"), render(reloaded)
    assert has_element?(reloaded, "#run-output", "deterministic")
    assert has_element?(reloaded, "#output-trace", "6")
    assert {:ok, state_after_reload} = Client.get_state(context)
    assert state_after_reload["payload"]["recoveryPolicy"] == policy
  end
end
