defmodule HardenLlmWeb.ProfileWidgetComponentTest do
  use HardenLlmWeb.ConnCase, async: true

  import Phoenix.LiveViewTest, except: [live: 1, live: 2, live: 3]

  alias HardenLlmWeb.{APIFixtures, HardenAPI, ProfileWidgetComponent}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-044 TEST-044
  # PLAN-HLLM-WIDGET-PARITY-001 TEST-101 TEST-102 TEST-103 TEST-104 TEST-112 WEB-TEST-084 TEST-264 TEST-265 TEST-266 WEB-TEST-100 WEB-TEST-101 WEB-TEST-102

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
    # The control update persists asynchronously. Wait before starting the
    # cache update so the following fold interaction cannot observe a queued
    # state save and remain disabled on a slower test runner.
    render_async(view, 1_000)

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
    render_async(view, 1_000)

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
    render_async(view, 1_000)
    assert has_element?(view, "#profile-credential-drawer")
    view |> element("#profile-credential-toggle") |> render_click()
    render_async(view, 1_000)
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

    assert has_element?(view, "#profile-recovery-policy .ullm-recovery-policy-scope")

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
             "Total provider calls, including the first call, retries, repairs, escalations and fresh reruns."
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

  # WEB-TEST-084: target-only mode reuses the leaf picker and cannot expose
  # generation-owned cache/search/recovery controls.
  test "target-only picker renders only the shared leaf controls and emits namespaced edits" do
    target = %{"source" => "profile", "profileId" => "Primary", "modelId" => "fixture"}

    html =
      render_component(&ProfileWidgetComponent.render/1,
        id: "repair-target-widget",
        id_prefix: "repair-target",
        target_only: true,
        target_name: "repairTarget",
        target_value: target,
        profiles: [profile("Primary", "fixture")],
        myself: nil
      )

    assert html =~ "data-target-only"
    assert html =~ "ullm-profile-row"
    assert html =~ "ullm-profile-select"
    refute html =~ "Model ID override"
    assert html =~ ~s(name="repairTarget[profileId]")
    assert html =~ ~s(name="repairTarget[modelId]")
    refute html =~ "workspace-cache-toggle"
    refute html =~ "toggle-json-repair"

    socket = %Phoenix.LiveView.Socket{
      assigns: %{
        __changed__: %{},
        id_prefix: "repair-target",
        target_name: "repairTarget",
        target_value: target
      }
    }

    assert {:noreply, updated} =
             ProfileWidgetComponent.handle_event(
               "recovery-target-change",
               %{"repairTarget" => %{"profileId" => "Escalated", "source" => "profile"}},
               socket
             )

    assert updated.assigns.target_value["profileId"] == "Escalated"

    assert_receive {:profile_widget, "repair-target",
                    {:profile_widget_target, "repairTarget", emitted}}

    assert emitted["profileId"] == "Escalated"
  end

  test "recovery branch events require the server-owned node identity" do
    socket = %Phoenix.LiveView.Socket{assigns: %{__changed__: %{}}}

    assert {:noreply, ^socket} =
             ProfileWidgetComponent.handle_event("toggle-rerun", %{}, socket)

    assert {:noreply, ^socket} =
             ProfileWidgetComponent.handle_event(
               "toggle-rerun-json-repair",
               %{"path" => "rerun.jsonRepair", "node-id" => "original"},
               socket
             )

    assert {:noreply, ^socket} =
             ProfileWidgetComponent.handle_event(
               "toggle-repair-escalation",
               %{"path" => "rerun.jsonRepair", "node-id" => "unknown"},
               socket
             )
  end

  test "recovery roles reuse the profile row and only rerun generation exposes search", %{
    conn: conn
  } do
    primary = profile("CPA GPT-5.6 Luna", "gpt-5.6-luna")
    astra = profile("CPA GPT-6 Astra", "gpt-6-astra")
    defaults = full_recovery_policy()

    state =
      APIFixtures.state()
      |> Map.put("recoveryPolicy", defaults)

    install_stub_with([primary, astra], primary, state, defaults)

    {:ok, view, _} = live(conn, ~p"/")
    render_async(view, 1_000)
    view |> element("#model-config-toggle") |> render_click()
    render_async(view, 1_000)
    view |> element("#profile-retry-toggle") |> render_click()
    render_async(view, 1_000)

    for id <- [
          "#profile-original-repair-initial",
          "#profile-original-repair-escalation"
        ] do
      assert has_element?(view, "#{id} .ullm-profile-row")
      assert has_element?(view, "#{id} .ullm-profile-select")
      refute has_element?(view, "#{id} .ullm-profile-search-toggle")
      refute has_element?(view, "#{id}", "Model ID override")
    end

    assert has_element?(view, "#profile-rerun-generation .ullm-profile-row")
    assert has_element?(view, "#profile-rerun-generation .ullm-profile-select")
    assert has_element?(view, "#profile-rerun-generation .ullm-profile-search-toggle")

    assert has_element?(view, "#profile-original-repair-initial-config-toggle")
    refute has_element?(view, "#profile-original-repair-initial-config")

    view |> element("#profile-original-repair-initial-config-toggle") |> render_click()

    assert has_element?(view, "#profile-original-repair-initial-config")

    assert has_element?(
             view,
             "#profile-original-repair-initial-config-retry-toggle",
             "Retries & Repair (inherited)"
           )

    assert has_element?(
             view,
             "#profile-original-repair-initial-config .ullm-recovery-policy-inherited",
             "Retry categories, call budget, and backoff are inherited"
           )

    assert has_element?(
             view,
             "#profile-original-repair-initial-config .recovery-policy-categories"
           )

    assert has_element?(
             view,
             "#profile-original-repair-initial-config input[type='checkbox'][disabled]"
           )

    refute has_element?(
             view,
             "#profile-original-repair-initial-config input[name*='retryOn']"
           )

    refute has_element?(
             view,
             "#profile-original-repair-initial-config input[name*='maxAttempts']"
           )

    refute has_element?(view, "#profile-original-repair-initial-config-json-repair-toggle")
    refute has_element?(view, "#profile-original-repair-initial-config-rerun-toggle")

    view |> element("#profile-original-repair-escalation-config-toggle") |> render_click()

    for config_id <- [
          "#profile-original-repair-initial-config",
          "#profile-original-repair-escalation-config"
        ] do
      assert has_element?(view, "#{config_id} .recovery-policy-categories")
      assert has_element?(view, "#{config_id} input[type='checkbox'][disabled]")
      refute has_element?(view, "#{config_id} input[name*='retryOn']")
      assert has_element?(view, "#{config_id} input[type='number'][value='6'][disabled]")
      assert has_element?(view, "#{config_id} input[type='number'][value='500'][disabled]")
      assert has_element?(view, "#{config_id} input[type='number'][value='8000'][disabled]")
      assert has_element?(view, "#{config_id} button", "Edit shared retry policy")
    end

    view |> element("#profile-rerun-generation-config-toggle") |> render_click()

    assert has_element?(view, "#profile-rerun-generation-config")
    assert has_element?(view, "#profile-rerun-generation-config-json-repair-toggle")
    assert has_element?(view, "#profile-rerun-generation-config-json-repair-initial-profile")

    for id <- [
          "#profile-rerun-generation-config-json-repair-initial",
          "#profile-rerun-generation-config-json-repair-escalation"
        ] do
      assert has_element?(view, "#{id} .ullm-profile-row")
      refute has_element?(view, "#{id} .ullm-profile-search-toggle")
    end

    refute has_element?(view, "#profile-rerun-generation-config #profile-rerun-toggle")

    view
    |> element("#profile-rerun-generation-config-json-repair-toggle")
    |> render_click()

    refute has_element?(view, "#profile-rerun-generation-config-json-repair-initial")
  end

  # WEB-TEST-090: rerun repair is a sibling of rerun.target in the REST policy,
  # not a child of that leaf target. This is exercised through rendered form
  # names so a pure serializer test cannot hide a binding regression.
  @tag :recovery
  test "rerun repair target fields use the canonical sibling policy path", %{conn: conn} do
    primary = profile("CPA GPT-5.6 Luna", "gpt-5.6-luna")
    astra = profile("CPA GPT-6 Astra", "gpt-6-astra")
    defaults = full_recovery_policy()
    state = APIFixtures.state() |> Map.put("recoveryPolicy", defaults)

    install_stub_with([primary, astra], primary, state, defaults)

    {:ok, view, _} = live(conn, ~p"/")
    render_async(view, 1_000)
    view |> element("#model-config-toggle") |> render_click()
    render_async(view, 1_000)
    view |> element("#profile-retry-toggle") |> render_click()
    render_async(view, 1_000)
    view |> element("#profile-rerun-generation-config-toggle") |> render_click()

    assert has_element?(
             view,
             ~s(#profile-rerun-generation-config-json-repair-initial-profile[name="profile[recoveryPolicy][rerun][jsonRepair][initial][profileId]"])
           )

    assert has_element?(
             view,
             ~s(#profile-rerun-generation-config-json-repair-escalation-profile[name="profile[recoveryPolicy][rerun][jsonRepair][escalation][profileId]"])
           )

    refute has_element?(
             view,
             ~s(input[name="profile[recoveryPolicy][rerun][target][jsonRepair][initial][profileId]"])
           )

    view
    |> element("#profile-rerun-generation-config-options-toggle")
    |> render_click()

    assert has_element?(
             view,
             ~s(input[name="profile[recoveryPolicy][rerun][target][maxTokens]"])
           )

    view
    |> element(~s(input[name="profile[recoveryPolicy][rerun][target][maxTokens]"]))
    |> render_change(%{
      "profile" => %{
        "recoveryPolicy" => %{
          "rerun" => %{"target" => %{"maxTokens" => "2048"}}
        }
      }
    })

    assert has_element?(
             view,
             ~s(input[name="profile[recoveryPolicy][rerun][target][providerOptions]"][value*="2048"])
           )
  end

  # WEB-TEST-096: invocation edits and explicit shared-profile saves have
  # separate destinations.
  @tag :recovery
  test "nested save explicitly updates the selected shared profile", %{conn: conn} do
    test_pid = self()
    primary = profile("CPA GPT-5.6 Luna", "gpt-5.6-luna")
    astra = profile("CPA GPT-6 Astra", "gpt-6-astra")
    defaults = full_recovery_policy()
    state = APIFixtures.state() |> Map.put("recoveryPolicy", defaults)

    Req.Test.stub(HardenAPI, fn request ->
      case {request.method, request.request_path} do
        {"GET", "/api/v1/auth/session"} ->
          Req.Test.json(request, APIFixtures.success(APIFixtures.principal()))

        {"GET", "/api/v1/state"} ->
          Req.Test.json(request, APIFixtures.success(nil, state))

        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(
            request,
            put_in(
              APIFixtures.profiles([primary, astra]),
              ["result", "defaults", "recoveryPolicy"],
              defaults
            )
          )

        {"GET", "/api/v1/history"} ->
          Req.Test.json(request, APIFixtures.history_page([]))

        {"POST", "/api/v1/state"} ->
          {:ok, body, request} = Plug.Conn.read_body(request)
          Req.Test.json(request, APIFixtures.success(nil, Jason.decode!(body)))

        {"PUT", "/api/v1/profiles/CPA%20GPT-6%20Astra"} ->
          {:ok, body, request} = Plug.Conn.read_body(request)
          send(test_pid, {:nested_profile_saved, Jason.decode!(body)})
          Req.Test.json(request, APIFixtures.success(astra))

        _ ->
          flunk("unexpected API call: #{request.method} #{request.request_path}")
      end
    end)

    {:ok, view, _} = live(conn, ~p"/")
    render_async(view, 1_000)
    view |> element("#model-config-toggle") |> render_click()
    render_async(view, 1_000)
    view |> element("#profile-retry-toggle") |> render_click()
    render_async(view, 1_000)
    view |> element("#profile-rerun-generation-config-toggle") |> render_click()
    view |> element("#profile-rerun-generation-config-options-toggle") |> render_click()

    view
    |> element(~s(input[name="profile[recoveryPolicy][rerun][target][maxTokens]"]))
    |> render_change(%{
      "profile" => %{
        "recoveryPolicy" => %{"rerun" => %{"target" => %{"maxTokens" => "2048"}}}
      }
    })

    refute has_element?(view, "#profile-rerun-generation-config-save[disabled]")
    view |> element("#profile-rerun-generation-config-save") |> render_click()
    render_async(view, 1_000)

    assert_receive {:nested_profile_saved, payload}, 1_000
    assert payload["profile"]["llmProfile"] == "CPA GPT-6 Astra"
    assert payload["profile"]["defaultOptions"]["max_tokens"] == 2048
  end

  defp install_stub(profiles, state_profile, save_response \\ nil) do
    state =
      APIFixtures.state()
      |> Map.put("selectedProfileId", get_in(state_profile, ["profile", "llmProfile"]))
      |> Map.put("modelId", get_in(state_profile, ["profile", "modelId"]))

    install_stub_with(
      profiles,
      state_profile,
      state,
      APIFixtures.recovery_policy(),
      save_response
    )
  end

  defp install_stub_with(profiles, state_profile, state, recovery_policy, save_response \\ nil) do
    state =
      state
      |> Map.put("selectedProfileId", get_in(state_profile, ["profile", "llmProfile"]))
      |> Map.put("modelId", get_in(state_profile, ["profile", "modelId"]))

    Req.Test.stub(HardenAPI, fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/auth/session"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.principal()))

        {"GET", "/api/v1/state"} ->
          Req.Test.json(conn, APIFixtures.success(nil, state))

        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(
            conn,
            put_in(
              APIFixtures.profiles(profiles),
              ["result", "defaults", "recoveryPolicy"],
              recovery_policy
            )
          )

        {"GET", "/api/v1/history"} ->
          Req.Test.json(conn, APIFixtures.history_page([]))

        {"POST", "/api/v1/state"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          Req.Test.json(conn, APIFixtures.success(nil, Jason.decode!(body)))

        {"PUT", "/api/v1/profiles/Primary"} when is_function(save_response, 1) ->
          save_response.(conn)

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

  defp full_recovery_policy do
    %{
      "maxAttempts" => 6,
      "retryOn" => ["network", "rate_limit", "server_error", "empty_response", "provider_retry"],
      "jsonRepair" => %{
        "initial" => %{
          "source" => "profile",
          "profileId" => "CPA GPT-5.6 Luna",
          "reasoningEffort" => "lowest"
        },
        "escalation" => %{
          "source" => "profile",
          "profileId" => "CPA GPT-5.6 Luna",
          "reasoningEffort" => "highest"
        }
      },
      "rerun" => %{
        "target" => %{
          "source" => "profile",
          "profileId" => "CPA GPT-6 Astra",
          "reasoningEffort" => "lowest"
        },
        "jsonRepair" => %{
          "initial" => %{
            "source" => "profile",
            "profileId" => "CPA GPT-6 Astra",
            "reasoningEffort" => "lowest"
          },
          "escalation" => %{
            "source" => "profile",
            "profileId" => "CPA GPT-6 Astra",
            "reasoningEffort" => "highest"
          }
        }
      },
      "backoff" => %{"baseDelayMs" => 500, "maxDelayMs" => 8000}
    }
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

  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-209 WEB-TEST-085
  @tag :recovery
  test "recovery branch toggles use backend profile targets", %{conn: conn} do
    primary = profile("CPA GPT-5.6 Luna", "gpt-5.6-luna")
    astra = profile("CPA GPT-6 Astra", "gpt-6-astra")
    defaults = full_recovery_policy()

    state =
      APIFixtures.state()
      |> Map.put("recoveryPolicy", %{
        "maxAttempts" => 4,
        "retryOn" => ["network"],
        "jsonRepair" => nil,
        "rerun" => nil,
        "backoff" => %{"baseDelayMs" => 500, "maxDelayMs" => 8000}
      })

    install_stub_with([primary, astra], primary, state, defaults)

    {:ok, view, _} = live(conn, ~p"/")
    render_async(view, 1_000)
    view |> element("#model-config-toggle") |> render_click()
    render_async(view, 1_000)
    view |> element("#profile-retry-toggle") |> render_click()
    render_async(view, 1_000)

    refute has_element?(view, "#profile-original-repair-initial-profile")
    refute has_element?(view, "#profile-rerun-generation-profile")
    assert has_element?(view, "#profile-json-repair-toggle")
    assert has_element?(view, "#profile-rerun-toggle")

    view |> element("#profile-json-repair-toggle") |> render_click()
    render_async(view, 1_000)

    assert has_element?(
             view,
             ~s(#profile-original-repair-initial-profile[value="CPA GPT-5.6 Luna"])
           )

    assert has_element?(
             view,
             ~s(#profile-original-repair-escalation-profile[value="CPA GPT-5.6 Luna"])
           )

    assert has_element?(view, "#profile-original-repair-initial-config-toggle")

    refute has_element?(
             view,
             ".ullm-recovery-generation-target",
             "Use this branch's generation model"
           )

    view |> element("#profile-rerun-toggle") |> render_click()
    render_async(view, 1_000)

    assert has_element?(view, ~s(#profile-rerun-generation-profile[value="CPA GPT-6 Astra"]))

    view |> element("#profile-rerun-generation-config-toggle") |> render_click()

    assert has_element?(
             view,
             ~s(#profile-rerun-generation-config-json-repair-initial-profile[value="CPA GPT-6 Astra"])
           )

    assert has_element?(
             view,
             ~s(#profile-rerun-generation-config-json-repair-escalation-profile[value="CPA GPT-6 Astra"])
           )
  end

  @tag :recovery
  test "enabled generation-relative branches offer configured target adoption", %{conn: conn} do
    primary = profile("CPA GPT-5.6 Luna", "gpt-5.6-luna")
    astra = profile("CPA GPT-6 Astra", "gpt-6-astra")
    defaults = full_recovery_policy()

    generation_repair = %{
      "initial" => %{"source" => "generation"},
      "escalation" => %{"source" => "generation"}
    }

    generation_rerun = %{
      "target" => %{"source" => "generation"},
      "jsonRepair" => generation_repair
    }

    policy =
      defaults
      |> Map.put("jsonRepair", generation_repair)
      |> Map.put("rerun", generation_rerun)

    state = APIFixtures.state() |> Map.put("recoveryPolicy", policy)
    install_stub_with([primary, astra], primary, state, defaults)

    {:ok, view, _} = live(conn, ~p"/")
    render_async(view, 1_000)
    view |> element("#model-config-toggle") |> render_click()
    render_async(view, 1_000)
    view |> element("#profile-retry-toggle") |> render_click()
    render_async(view, 1_000)

    assert has_element?(view, "#profile-original-repair .ullm-recovery-default-preview")
    assert has_element?(view, "#profile-rerun .ullm-recovery-default-preview")
    assert has_element?(view, "#profile-original-repair-initial-profile[disabled]")

    view
    |> element(~s(button[phx-click="use-recovery-default"][phx-value-path="jsonRepair"]))
    |> render_click()

    refute has_element?(view, "#profile-original-repair-initial-profile[disabled]")

    view
    |> element(~s(button[phx-click="use-recovery-default"][phx-value-path="rerun"]))
    |> render_click()

    refute has_element?(view, "#profile-rerun-generation-profile[disabled]")
  end

  @tag :recovery
  test "generation-relative targets render inherited values in the complete picker", %{
    conn: conn
  } do
    primary = profile("Primary", "primary-model")
    astra = profile("CPA GPT-6 Astra", "gpt-6-astra")

    generation_repair = %{
      "initial" => %{"source" => "generation"},
      "escalation" => %{"source" => "generation"}
    }

    generation_policy = %{
      "maxAttempts" => 4,
      "retryOn" => ["network"],
      "jsonRepair" => generation_repair,
      "rerun" => %{
        "target" => %{"source" => "generation"},
        "jsonRepair" => generation_repair
      },
      "backoff" => %{"baseDelayMs" => 500, "maxDelayMs" => 8000}
    }

    state = APIFixtures.state() |> Map.put("recoveryPolicy", generation_policy)
    install_stub_with([primary, astra], primary, state, generation_policy)

    {:ok, view, _} = live(conn, ~p"/")
    render_async(view, 1_000)
    view |> element("#model-config-toggle") |> render_click()
    render_async(view, 1_000)
    view |> element("#profile-retry-toggle") |> render_click()
    render_async(view, 1_000)

    assert has_element?(view, ~s(#profile-original-repair-initial-profile[value="Primary"]))

    assert has_element?(
             view,
             ~s(#profile-original-repair-initial-model[value="primary-model"][disabled])
           )

    assert has_element?(view, "#profile-original-repair-initial-reasoning[disabled]")
    assert has_element?(view, "#profile-original-repair-initial-config-toggle[disabled]")

    assert has_element?(
             view,
             "#profile-original-repair-initial",
             "Inherited from the generation model"
           )

    refute has_element?(view, "#profile-original-repair-initial-search-toggle")

    assert has_element?(view, ~s(#profile-rerun-generation-profile[value="Primary"]))

    assert has_element?(
             view,
             ~s(#profile-rerun-generation-model[value="primary-model"][disabled])
           )

    view
    |> element("#profile-original-repair-initial-profile")
    |> render_change(%{
      "profile" => %{
        "recoveryPolicy" => %{
          "jsonRepair" => %{
            "initial" => %{"profileId" => "CPA GPT-6 Astra"}
          }
        }
      }
    })

    render_async(view, 1_000)

    assert has_element?(
             view,
             ~s(#profile-original-repair-initial-profile[value="CPA GPT-6 Astra"])
           )

    refute has_element?(view, "#profile-original-repair-initial-profile[disabled]")
    refute has_element?(view, "#profile-original-repair-initial-reasoning[disabled]")

    assert has_element?(
             view,
             ~s(input[name="profile[recoveryPolicy][jsonRepair][initial][source]"][value="profile"])
           )
  end

  test "the inherited leaf renderer keeps the wire source while showing the full row" do
    html =
      render_component(&ProfileWidgetComponent.recovery_target_fields/1,
        id_prefix: "inherited-target",
        name: "profile[recoveryPolicy][jsonRepair][initial]",
        target_value: %{"source" => "generation"},
        profiles: [profile("Primary", "primary-model")],
        target: nil,
        config_path: "jsonRepair.initial",
        generation_profile_id: "Primary",
        generation_model_id: "primary-model",
        generation_reasoning: "middle"
      )

    assert html =~ ~s(id="inherited-target-profile")
    assert html =~ ~s(value="Primary")
    assert html =~ "Inherited from the generation model"
    assert html =~ ~s(name="profile[recoveryPolicy][jsonRepair][initial][source]")
    assert html =~ ~s(value="generation")
    assert html =~ ~s(id="inherited-target-reasoning")
    assert html =~ ~s(id="inherited-target-model")
    assert html =~ ~s(id="inherited-target-config-toggle")
    assert Regex.match?(~r/id="inherited-target-reasoning"[^>]*disabled/, html)
    assert Regex.match?(~r/id="inherited-target-model"[^>]*disabled/, html)
    assert Regex.match?(~r/id="inherited-target-config-toggle"[^>]*disabled/, html)
    refute html =~ "Edit shared retry policy"
    refute html =~ "target-recovery-editor"
  end

  @tag :recovery
  test "inline profile saves retain policy values and display backend field errors", %{conn: conn} do
    primary = profile("Primary", "fixture")

    install_stub([primary], primary, fn conn ->
      {:ok, body, conn} = Plug.Conn.read_body(conn)
      assert Jason.decode!(body)["profile"]["recoveryPolicy"]["maxAttempts"] == 11

      {status, envelope} =
        APIFixtures.error(422, "validation_failed", %{
          "Primary.recoveryPolicy.maxAttempts" => "must be between 1 and 10"
        })

      conn |> Plug.Conn.put_status(status) |> Req.Test.json(envelope)
    end)

    {:ok, view, _} = live(conn, ~p"/")
    render_async(view, 1_000)
    view |> element("#model-config-toggle") |> render_click()
    render_async(view, 1_000)
    view |> element("#profile-retry-toggle") |> render_click()

    view
    |> element("#profile-recovery-maxAttempts")
    |> render_change(%{"profile" => %{"recoveryPolicy" => %{"maxAttempts" => "11"}}})

    view |> element("#profile-save") |> render_click()
    render_async(view, 1_000)
    assert has_element?(view, ~s(#profile-recovery-maxAttempts[value="11"][aria-invalid="true"]))
    assert has_element?(view, "#profile-recovery-policy", "must be between 1 and 10")
  end
end
