defmodule HardenLlmWeb.ProfileWidgetComponentTest do
  use HardenLlmWeb.ConnCase, async: true

  import Phoenix.LiveViewTest, except: [live: 1, live: 2, live: 3]

  alias HardenLlmWeb.{APIFixtures, HardenAPI}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-044 TEST-044
  # PLAN-HLLM-WIDGET-PARITY-001 TEST-101 TEST-102 TEST-103 TEST-104 TEST-112

  setup %{conn: conn}, do: {:ok, conn: authenticated_conn(conn)}

  test "compact widget row and every main fold stay in flow", %{conn: conn} do
    primary = profile("CPA GPT-5.6 Luna", "gpt-5.6-luna")
    install_stub([primary], primary)

    {:ok, view, _html} = live(conn, ~p"/")
    render_async(view, 1_000)
    html = render(view)

    assert html =~ "LLM Profile"
    assert has_element?(view, "#workspace-llm-widget .ullm-profile-category", "LLM")
    assert has_element?(view, "#workspace-llm-widget #workspace-reasoning")

    assert has_element?(
             view,
             "#workspace-llm-widget .ullm-reasoning-field > .ullm-profile-label"
           )

    assert has_element?(
             view,
             "#workspace-llm-widget .ullm-reasoning-field > #workspace-reasoning"
           )

    assert has_element?(view, "#workspace-llm-widget #workspace-cache-toggle")
    assert has_element?(view, "#workspace-llm-widget #model-config-toggle")
    assert has_element?(view, "#workspace-llm-widget #workspace-web-search-toggle")
    refute has_element?(view, ~s([role="tab"]))

    assert has_element?(
             view,
             ~s(#workspace-web-search-toggle[aria-label="Enable web search"][aria-pressed="false"][data-web-search="false"])
           )

    assert has_element?(view, "#workspace-web-search-toggle", "🌐")
    refute has_element?(view, "#workspace-web-search-toggle.is-enabled")
    assert has_element?(view, "#workspace-web-search[value=\"false\"]")

    html = render(view)
    reasoning_index = :binary.match(html, "id=\"workspace-reasoning\"") |> elem(0)
    search_index = :binary.match(html, "id=\"workspace-web-search-toggle\"") |> elem(0)
    cache_index = :binary.match(html, "id=\"workspace-cache-toggle\"") |> elem(0)

    assert reasoning_index < search_index
    assert search_index < cache_index

    view |> element("#workspace-web-search-toggle") |> render_click()

    assert has_element?(
             view,
             ~s(#workspace-web-search-toggle[aria-label="Disable web search"][aria-pressed="true"][data-web-search="true"])
           )

    assert has_element?(view, "#workspace-web-search[value=\"true\"]")
    assert has_element?(view, "#workspace-web-search-toggle.is-enabled")

    assert has_element?(
             view,
             ~s(#workspace-cache-toggle[aria-label="Use cache"][aria-pressed="true"][data-cache-mode="cache"])
           )

    assert has_element?(view, "#workspace-cache-toggle", "💾")
    refute has_element?(view, "#workspace-cache-toggle .ullm-cache-toggle-label")

    assert has_element?(
             view,
             ~s(#workspace-cache-toggle[title="Uses a saved response when this exact operation has already run."])
           )

    view |> element("#workspace-cache-toggle") |> render_click()

    assert has_element?(
             view,
             ~s(#workspace-cache-toggle[aria-label="Overwrite cache on next run"][aria-pressed="false"][data-cache-mode="refresh"])
           )

    assert has_element?(view, "#workspace-cache-toggle", "↻")
    refute has_element?(view, "#workspace-cache-toggle .ullm-cache-toggle-label")

    assert has_element?(
             view,
             ~s(#workspace-cache-toggle[title="Fresh run: skips old cache and overwrites the saved response after success."])
           )

    view |> element("#model-config-toggle") |> render_click()
    render_async(view, 1_000)

    for selector <- [
          "#profile-config-fields",
          "#profile-credential-toggle",
          "#profile-options-toggle",
          "#profile-retry-toggle",
          "#profile-pricing-toggle",
          "#profile-bundle-file",
          "#profile-save",
          "#profile-delete"
        ] do
      assert has_element?(view, selector), "missing main widget selector #{selector}"
    end

    refute has_element?(view, "#profile-identity-toggle")
    refute has_element?(view, "#profile_credentialId")
    refute has_element?(view, "#profile_endpointCredentialScope")

    view |> element("#profile-credential-toggle") |> render_click()
    assert has_element?(view, "#profile-credential-drawer")
    view |> element("#profile-credential-toggle") |> render_click()
    refute has_element?(view, "#profile-credential-drawer")

    view |> element("#profile-options-toggle") |> render_click()
    render_async(view, 1_000)

    for selector <- [
          "#profile-options",
          "#profile_maxTokens",
          "#profile_temperature",
          "#profile_topP",
          "#profile_topK",
          "#profile_stopSequences",
          "#profile_defaultOptionsJson"
        ] do
      assert has_element?(view, selector), "missing options selector #{selector}"
    end

    assert has_element?(view, ~s(#profile_modelId[placeholder="gpt-5.6-luna"]))
    assert has_element?(view, ~s(#profile_baseUrl[placeholder="https://openrouter.ai/api/v1"]))
    assert has_element?(view, ~s(#profile_maxTokens[placeholder="16000"]))
    assert has_element?(view, ~s(#profile_temperature[placeholder="0.2"]))
    assert has_element?(view, ~s(#profile_topP[placeholder="0.95"]))
    assert has_element?(view, ~s(#profile_topK[placeholder="40"]))
    assert has_element?(view, ~s(#profile_stopSequences[placeholder="one sequence per line"]))

    assert has_element?(
             view,
             ~s(#profile_defaultOptionsJson[placeholder='{"temperature":0,"max_tokens":16000}'])
           )

    view |> element("#profile-retry-toggle") |> render_click()
    render_async(view, 1_000)

    for selector <- [
          "#profile-retry-repair",
          "#profile-retry-rate_limit",
          "#profile-retry-server_error",
          "#profile-retry-network",
          "#profile-recovery-maxAttempts"
        ] do
      assert has_element?(view, selector), "missing retry selector #{selector}"
    end

    assert has_element?(view, ~s(#profile-recovery-maxAttempts[value="4"]))
    assert has_element?(view, ~s(#profile-recovery-baseDelayMs[value="500"]))
    assert has_element?(view, ~s(#profile-recovery-maxDelayMs[value="8000"]))

    assert has_element?(
             view,
             "#profile-recovery-maxAttempts-help[hidden]",
             "Total provider calls, including the first call, retries and repairs. The selected profile and model stay the same."
           )

    assert has_element?(view, ".ullm-field-info-text", "original schema")

    assert has_element?(
             view,
             ~s(button.ullm-field-label-info[type="button"][aria-controls="profile-repair-invalid-output-help"][aria-expanded="false"])
           )

    view |> element("#profile-pricing-toggle") |> render_click()
    assert has_element?(view, "#profile-pricing")
    assert has_element?(view, ~s(#profile_pricingInput[placeholder="n/a"]))
    assert has_element?(view, ~s(#profile_pricingOutput[placeholder="n/a"]))
    assert has_element?(view, ~s(#profile_pricingCacheRead[placeholder="n/a"]))
    assert has_element?(view, ~s(#profile_pricingCacheWrite[placeholder="n/a"]))
    assert has_element?(view, ~s(#profile_pricingReasoning[placeholder="n/a"]))
    assert has_element?(view, "#profile_pricingCacheWrite-help[hidden]", "Cache write applies")

    assert has_element?(
             view,
             "#profile_pricingReasoning-help[hidden]",
             "Reasoning output applies"
           )
  end

  test "selected profile capabilities remain server-owned", %{conn: conn} do
    primary = profile_without_reasoning("Primary", "primary-model")
    install_stub([primary, profile("Another", "another-model")], primary)
    {:ok, view, _} = live(conn, ~p"/")
    render_async(view, 1_000)
    assert has_element?(view, ~s(#workspace-reasoning[disabled]))
    assert has_element?(view, ~s(#workspace-reasoning option[value=""][selected]))
    view |> element("#model-config-toggle") |> render_click()
    render_async(view, 1_000)
    view |> element("#profile-retry-toggle") |> render_click()
    render_async(view, 1_000)
    assert has_element?(view, "#profile-recovery-policy")
    refute render(view) =~ "Escalation"
    assert has_element?(view, ~s(#run_selectedProfileId[value="Primary"]))
  end

  test "two widget instances retain independent IDs, folds, and controls", %{conn: conn} do
    primary = profile("Primary", "model-primary")
    secondary = profile("Secondary", "model-secondary")
    install_stub([primary, secondary], primary)

    {:ok, view, _html} = live(conn, ~p"/embed/llm")
    render_async(view, 1_000)

    for prefix <- ["embed-primary", "embed-secondary"] do
      assert has_element?(view, "##{prefix}-llm-widget")
      assert has_element?(view, "##{prefix}-run_selectedProfileId")
      assert has_element?(view, "##{prefix}-model-config-toggle")
    end

    view |> element("#embed-primary-model-config-toggle") |> render_click()
    view |> element("#embed-secondary-model-config-toggle") |> render_click()
    view |> element("#embed-primary-profile-options-toggle") |> render_click()

    assert has_element?(view, "#embed-primary-profile-options")
    refute has_element?(view, "#embed-secondary-profile-options")

    ids =
      Regex.scan(~r/\bid="([^"]+)"/, render(view), capture: :all_but_first)
      |> List.flatten()

    assert length(ids) == length(Enum.uniq(ids)), "duplicate DOM ids found"
  end

  defp install_stub(profiles, state_profile) do
    state =
      APIFixtures.state()
      |> Map.put("selectedProfileId", get_in(state_profile, ["profile", "llmProfile"]))
      |> Map.put("modelId", get_in(state_profile, ["profile", "modelId"]))

    Req.Test.stub(HardenAPI, fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/auth/session"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.principal()))

        {"GET", "/api/v1/state"} ->
          Req.Test.json(conn, APIFixtures.success(nil, state))

        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.profiles(profiles))

        {"GET", "/api/v1/history"} ->
          Req.Test.json(conn, APIFixtures.success(%{"items" => []}))

        {"POST", "/api/v1/state"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          Req.Test.json(conn, APIFixtures.success(nil, Jason.decode!(body)))

        _ ->
          flunk("unexpected API call: #{conn.method} #{conn.request_path}")
      end
    end)
  end

  defp profile(profile_id, model_id) do
    APIFixtures.profile_state()
    |> put_in(["profile", "llmProfile"], profile_id)
    |> put_in(["profile", "modelId"], model_id)
    |> put_in(["profile", "models"], [%{"id" => model_id, "label" => model_id}])
    |> put_in(
      ["profile", "defaultOptions"],
      %{
        "max_tokens" => 16_000
      }
    )
    |> put_in(["credential", "credentialId"], "credential-#{profile_id}")
  end

  defp profile_without_reasoning(profile_id, model_id) do
    profile(profile_id, model_id)
    |> update_in(["profile"], &Map.delete(&1, "reasoningEffortMap"))
  end

  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-209
  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-072
  @tag :recovery
  test "recovery exposes the complete policy and clickable help", %{conn: conn} do
    primary = profile("Primary", "fixture")
    install_stub([primary], primary)
    {:ok, view, _} = live(conn, ~p"/")
    render_async(view, 1_000)
    view |> element("#model-config-toggle") |> render_click()
    render_async(view, 1_000)
    view |> element("#profile-retry-toggle") |> render_click()

    for category <- ~w(network rate_limit server_error empty_response provider_retry) do
      assert has_element?(
               view,
               ~s(input[name="profile[recoveryPolicy][retryOn][]"][value="#{category}"])
             )
    end

    assert has_element?(view, ~s(input[name="profile[recoveryPolicy][maxAttempts]"]))

    assert has_element?(
             view,
             ~s(input[name="profile[recoveryPolicy][repairInvalidOutput]"][type="checkbox"])
           )

    assert has_element?(view, "#profile-recovery-policy [phx-click][aria-expanded]")
    refute has_element?(view, "#profile-fallback-toggle")
    refute render(view) =~ "Escalation"
    refute render(view) =~ "enableRetryOnParseError"
  end
end
