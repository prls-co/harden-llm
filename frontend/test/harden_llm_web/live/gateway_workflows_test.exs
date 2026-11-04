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

    {:ok,
     conn: authenticated_conn(conn),
     context: HardenLlmWeb.APIContext.server("workspace:fixture-user")}
  end

  @tag :preset_workflow
  test "every shipped preset selects, persists, reloads and executes both modes through the real gateway",
       %{conn: conn, context: context} do
    {:ok, %{"profiles" => profiles}} = Client.list_profiles(context)
    {:ok, view, _} = live(conn, "/")
    render_settled(view, 10_000)
    view |> element("#input-advanced-toggle") |> render_click()
    render_settled(view, 10_000)

    view =
      Enum.reduce(profiles, view, fn profile, view ->
        id = profile["id"]

        view
        |> element("#run_selectedProfileId")
        |> render_change(%{"run" => %{"selectedProfileId" => id}})

        render_settled(view, 10_000)
        refute has_element?(view, "#draft-error"), "#{id}: #{draft_error_text(view)}"
        {:ok, state} = Client.get_state(context)
        assert state["payload"]["selectedProfileId"] == id
        refute Map.has_key?(state["payload"]["profileEditor"], "apiKey")
        assert state["revision"] > 0

        GenServer.stop(view.pid)
        {:ok, reloaded, _} = live(conn, "/")
        render_settled(reloaded, 10_000)
        assert has_element?(reloaded, "#run_selectedProfileId[value='#{id}']"), id
        refute has_element?(reloaded, "#draft-error"), id
        {:ok, restored} = Client.get_state(context)
        assert restored["payload"] == state["payload"], id

        for mode <- ["text", "structured"] do
          reloaded
          |> form("#run-form", %{
            "run" => %{"callType" => mode, "userPrompt" => "Return the fixture object."}
          })
          |> render_change()

          render_settled(reloaded, 10_000)
          refute has_element?(reloaded, "#run-submit[disabled]"), "#{id}: #{mode}"
          reloaded |> form("#run-form") |> render_submit()
          render_settled(reloaded, 10_000)
          refute has_element?(reloaded, "#run-error"), "#{id}: #{mode}"
          assert has_element?(reloaded, "#run-output", "deterministic"), id
        end

        reloaded
      end)

    {:ok, persisted} = Client.get_state(context)
    GenServer.stop(view.pid)
    {:ok, reloaded, _} = live(conn, "/")
    render_settled(reloaded, 10_000)

    assert has_element?(
             reloaded,
             "#run_selectedProfileId[value='#{persisted["payload"]["selectedProfileId"]}']"
           )

    refute has_element?(reloaded, "#draft-error")
  end

  @tag :preset_workflow
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

  # TEST-464 / WEB-TEST-130
  @tag :transport_workflow
  test "actual interruption during inference gives an unknown outcome without replay", %{
    conn: conn,
    context: context
  } do
    view = run_transport_prompt(conn, "interrupt-before-terminal")
    assert has_element?(view, "#run-error", "outcome is unknown")
    refute has_element?(view, "#run-output", "OK")
    assert_terminal_record(context, "interrupt-before-terminal", "failed")
  end

  @tag :transport_workflow
  test "actual interruption after terminal preserves known output and reports recording uncertainty",
       %{conn: conn, context: context} do
    view = run_transport_prompt(conn, "interrupt-after-terminal")
    assert has_element?(view, "#run-output", "OK")
    assert has_element?(view, "#run-error", "history recording could not be confirmed")
    assert_terminal_record(context, "interrupt-after-terminal", "succeeded")
  end

  defp assert_terminal_record(context, prompt, status) do
    # The inference task may finish detached recording just after transport ends.
    # Await the owned record using a bounded API read, never resubmit inference.
    deadline = System.monotonic_time(:millisecond) + 5_000
    record = read_terminal_record(context, prompt, deadline)
    assert record["request"]["input"]["userPrompt"] == prompt
    assert record["result"]["status"] == status
    assert length(record["result"]["attempts"]) == 1
    assert record["result"]["providerInvoked"]
  end

  defp read_terminal_record(context, prompt, deadline) do
    assert System.monotonic_time(:millisecond) < deadline,
           "detached terminal record did not appear"

    {:ok, %{"items" => items}} = Client.list_history(context)

    records =
      Enum.map(items, fn item ->
        {:ok, record} = Client.get_history(context, item["runId"])
        record
      end)

    case Enum.filter(records, &(get_in(&1, ["request", "input", "userPrompt"]) == prompt)) do
      [record] -> record
      [] -> read_terminal_record(context, prompt, deadline)
      _ -> flunk("transport interruption produced duplicate history")
    end
  end

  defp run_transport_prompt(conn, prompt) do
    {:ok, view, _} = live(conn, "/")
    render_settled(view, 10_000)

    unless has_element?(view, "[name='run[callType]']") do
      view |> element("#input-advanced-toggle") |> render_click()
      render_settled(view, 10_000)
    end

    view
    |> form("#run-form", %{"run" => %{"userPrompt" => prompt, "callType" => "text"}})
    |> render_change()

    render_settled(view, 10_000)
    view |> form("#run-form") |> render_submit()
    render_settled(view, 10_000)
    view
  end

  defp draft_error_text(view) do
    if has_element?(view, "#draft-error") do
      view |> element("#draft-error") |> render()
    else
      "no draft error"
    end
  end
end
