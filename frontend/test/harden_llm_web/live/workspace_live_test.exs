defmodule HardenLlmWeb.WorkspaceLiveTest do
  use HardenLlmWeb.ConnCase, async: true

  import Phoenix.LiveViewTest, except: [live: 1, live: 2, live: 3]

  alias HardenLlm.LlmTraceProjection
  alias HardenLlmWeb.{APIFixtures, HardenAPI}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-007
  # PLAN-HLLM-WIDGET-PARITY-001 TEST-101 TEST-102 TEST-103 TEST-104 TEST-106 TEST-107 TEST-109 TEST-113

  setup %{conn: conn}, do: {:ok, conn: authenticated_conn(conn)}

  test "stats expose retry, snapshot time, and bounded periodic refresh", %{conn: conn} do
    test_pid = self()
    counter = start_supervised!({Agent, fn -> 0 end})

    install_stub(
      fn conn -> unexpected(conn) end,
      stats: fn conn ->
        request_number = Agent.get_and_update(counter, fn count -> {count + 1, count + 1} end)
        send(test_pid, {:stats_request, request_number})

        if request_number == 1 do
          {status, envelope} = APIFixtures.error(503, "temporarily_unavailable")
          conn |> Plug.Conn.put_status(status) |> Req.Test.json(envelope)
        else
          stats = put_in(APIFixtures.stats(), ["maxCallDurationMs"], request_number * 1_000)
          Req.Test.json(conn, APIFixtures.success(stats))
        end
      end
    )

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    assert_received {:stats_request, 1}
    assert has_element?(view, "#llm-stats-summary-error", "temporarily unavailable")
    assert has_element?(view, "#llm-stats-summary-refresh:not([disabled])", "Retry")

    view |> element("#llm-stats-summary-refresh") |> render_click()
    assert_receive {:stats_request, 2}, 1_000
    render_async(view, 1_000)

    refute has_element?(view, "#llm-stats-summary-error")
    assert has_element?(view, "#llm-stats-summary-updated", "Last updated")
    assert has_element?(view, "#llm-stats-summary", "2000")

    send(view.pid, :refresh_stats_snapshot)
    assert_receive {:stats_request, 3}, 1_000
    render_async(view, 1_000)
    assert has_element?(view, "#llm-stats-summary", "3000")
  end

  test "unexpected run task exits reconcile authoritative stats", %{conn: conn} do
    test_pid = self()
    counter = start_supervised!({Agent, fn -> 0 end})

    install_stub(
      fn conn ->
        case {conn.method, conn.request_path} do
          {"POST", "/api/v1/run"} -> exit(:simulated_task_exit)
          _ -> unexpected(conn)
        end
      end,
      stats: fn conn ->
        request_number = Agent.get_and_update(counter, fn count -> {count + 1, count + 1} end)
        send(test_pid, {:stats_request, request_number})
        Req.Test.json(conn, APIFixtures.success(APIFixtures.stats()))
      end
    )

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)
    assert_received {:stats_request, 1}

    submit_run(view, %{"userPrompt" => "task exit fixture"})
    assert_receive {:stats_request, 2}, 1_000
    render_async(view, 1_000)

    assert has_element?(view, "#run-error", "run could not be completed")
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-053
  test "hydrates the utility preset and exposes the backend catalog when state is empty", %{
    conn: conn
  } do
    preset = widget_profile("CPA GPT-5.6 Luna", "gpt-5.6-luna")
    other = widget_profile("OpenAI GPT-5.4", "gpt-5.4")

    state =
      APIFixtures.state()
      |> Map.put("selectedProfileId", "")
      |> Map.put("modelId", "")

    install_stub(fn conn -> unexpected(conn) end, profiles: [other, preset], state: state)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    assert has_element?(
             view,
             ~s(#run_selectedProfileId[value="CPA GPT-5.6 Luna"])
           )

    assert has_element?(view, ~s(#run_modelId[value="gpt-5.6-luna"]))
    assert has_element?(view, ~s(#run_selectedProfileId-options [data-value="CPA GPT-5.6 Luna"]))
    assert has_element?(view, ~s(#run_selectedProfileId-options [data-value="OpenAI GPT-5.4"]))
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-054
  test "renders every backend-owned utility preset in the workspace picker", %{conn: conn} do
    profiles = utility_preset_profiles()

    state =
      APIFixtures.state()
      |> Map.put("selectedProfileId", "CPA GPT-5.6 Luna")
      |> Map.put("modelId", "gpt-5.6-luna")

    install_stub(fn conn -> unexpected(conn) end, profiles: profiles, state: state)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    assert length(profiles) == 28

    for profile <- profiles do
      profile_id = get_in(profile, ["profile", "llmProfile"])

      assert has_element?(
               view,
               ~s(#run_selectedProfileId-options [data-value="#{profile_id}"])
             ),
             "missing utility preset #{profile_id}"
    end
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-055
  test "switching presets synchronizes the workspace model with the selected profile", %{
    conn: conn
  } do
    primary = widget_profile("CPA GPT-5.6 Luna", "gpt-5.6-luna")
    secondary = widget_profile("OpenAI GPT-5.4", "gpt-5.4")

    state =
      APIFixtures.state()
      |> Map.put("selectedProfileId", "CPA GPT-5.6 Luna")
      |> Map.put("modelId", "gpt-5.6-luna")

    install_stub(
      fn conn ->
        case {conn.method, conn.request_path} do
          {"POST", "/api/v1/state"} ->
            {:ok, body, conn} = Plug.Conn.read_body(conn)
            Req.Test.json(conn, APIFixtures.success(nil, Jason.decode!(body)))

          _ ->
            unexpected(conn)
        end
      end,
      profiles: [primary, secondary],
      state: state
    )

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    view
    |> with_target("#workspace-llm-widget")
    |> render_change("select-profile", %{
      "run" => %{"selectedProfileId" => "OpenAI GPT-5.4"}
    })

    render_async(view, 1_000)

    assert has_element?(view, ~s(#run_selectedProfileId[value="OpenAI GPT-5.4"]))
    assert has_element?(view, ~s(#run_modelId[value="gpt-5.4"]))
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-056
  test "renders utility input defaults and the utility control topology", %{conn: conn} do
    install_stub(
      fn conn ->
        case {conn.method, conn.request_path} do
          {"POST", "/api/v1/state"} ->
            {:ok, body, conn} = Plug.Conn.read_body(conn)
            Req.Test.json(conn, APIFixtures.success(nil, Jason.decode!(body)))

          _ ->
            unexpected(conn)
        end
      end,
      state: %{}
    )

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    assert has_element?(
             view,
             ~s(#run_userPrompt.ullm-input[placeholder="Ask the selected model to produce a concise answer."][rows="8"])
           )

    assert has_element?(view, "#run_userPrompt", "write a haiku joke")
    refute has_element?(view, "#run_systemPrompt")
    refute has_element?(view, "#advanced-input")
    assert has_element?(view, ~s(#input-advanced-toggle[aria-expanded="false"]))
    assert has_element?(view, "#run-submit:not([disabled])")

    view |> element("#input-advanced-toggle") |> render_click()

    assert has_element?(view, "#advanced-input")

    assert has_element?(
             view,
             ~s(#run_systemPrompt[placeholder="Optional system instruction."][rows="3"])
           )

    assert has_element?(view, "#clear-system-prompt:not([disabled])")
    assert has_element?(view, ~s(#run_callType option[value="structured"][selected]))
    assert has_element?(view, ~s(#run_schemaShorthand.ullm-input-mono[rows="4"]))
    assert has_element?(view, ~s(#run_schema.ullm-input-mono[rows="6"]))
    refute has_element?(view, "#run_structuredRepair")
    assert has_element?(view, "#schema-status", "Schema valid.")
    assert has_element?(view, "#generate-schema:not([disabled])")
    assert has_element?(view, "#check-schema:not([disabled])")
    assert has_element?(view, "#clear-schema:not([disabled])")

    html = render(view)
    shorthand_index = :binary.match(html, "id=\"run_schemaShorthand\"") |> elem(0)
    generate_index = :binary.match(html, "id=\"generate-schema\"") |> elem(0)
    schema_index = :binary.match(html, "id=\"run_schema\"") |> elem(0)
    check_index = :binary.match(html, "id=\"check-schema\"") |> elem(0)

    assert shorthand_index < generate_index
    assert generate_index < schema_index
    assert schema_index < check_index
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-059
  test "structured output selector controls mode while retry policy stays in the profile fold", %{
    conn: conn
  } do
    test_pid = self()

    profile =
      widget_profile("Primary", "model-test")
      |> put_in(["profile", "defaultOptions", "structuredRepairRetry"], false)

    install_stub(
      fn conn ->
        case {conn.method, conn.request_path} do
          {"POST", "/api/v1/run"} ->
            {:ok, body, conn} = Plug.Conn.read_body(conn)
            send(test_pid, {:mode_run_payload, Jason.decode!(body)})
            Req.Test.json(conn, APIFixtures.success(APIFixtures.run_result()))

          _ ->
            unexpected(conn)
        end
      end,
      profiles: [profile]
    )

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    view |> element("#input-advanced-toggle") |> render_click()

    assert has_element?(view, ~s(#run_callType option[value="text"][selected]))
    refute has_element?(view, "#run_structuredRepair")

    schema =
      Jason.encode!(%{
        "type" => "object",
        "properties" => %{"answer" => %{"type" => "string"}},
        "required" => ["answer"],
        "additionalProperties" => false
      })

    submit_run(view, %{"callType" => "structured", "schema" => schema})
    render_async(view, 1_000)

    assert_received {:mode_run_payload,
                     %{
                       "callType" => "structured",
                       "structuredRepair" => false
                     }}

    view
    |> form("#run-form", %{
      "run" => %{
        "callType" => "text",
        "schema" => "{invalid",
        "userPrompt" => "plain text despite a schema draft"
      }
    })
    |> render_change()

    assert has_element?(view, ~s(#run_callType option[value="text"][selected]))
    assert has_element?(view, "#run-submit:not([disabled])")

    view |> form("#run-form") |> render_submit()
    render_async(view, 1_000)

    assert_received {:mode_run_payload,
                     %{
                       "callType" => "text",
                       "structuredRepair" => false
                     }}
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-057
  test "schema validation uses utility's contracted subset and gates selected structured runs", %{
    conn: conn
  } do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/state"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          Req.Test.json(conn, APIFixtures.success(nil, Jason.decode!(body)))

        {"POST", "/api/v1/run"} ->
          flunk("invalid schema reached backend")

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)
    view |> element("#input-advanced-toggle") |> render_click()

    unsupported_schema =
      Jason.encode!(%{
        "type" => "object",
        "properties" => %{"answer" => %{"type" => "string"}},
        "required" => ["answer"],
        "additionalProperties" => false,
        "minLength" => 1
      })

    view
    |> form("#run-form", %{
      "run" => %{
        "selectedProfileId" => "Primary",
        "modelId" => "model-test",
        "userPrompt" => "schema gate",
        "callType" => "structured",
        "schema" => unsupported_schema
      }
    })
    |> render_change()

    assert has_element?(
             view,
             "#schema-status",
             "not part of the utility-llm contracted schema subset"
           )

    assert has_element?(view, "#run-submit[disabled]")

    view
    |> form("#run-form", %{
      "run" => %{
        "selectedProfileId" => "Primary",
        "modelId" => "model-test",
        "userPrompt" => "schema gate",
        "callType" => "structured",
        "schema" => unsupported_schema
      }
    })
    |> render_submit()

    assert has_element?(
             view,
             "#run-error",
             "not part of the utility-llm contracted schema subset"
           )

    valid_schema =
      Jason.encode!(%{
        "type" => "object",
        "properties" => %{"answer" => %{"type" => "string"}},
        "required" => ["answer"],
        "additionalProperties" => false
      })

    view
    |> form("#run-form", %{
      "run" => %{
        "selectedProfileId" => "Primary",
        "modelId" => "model-test",
        "userPrompt" => "schema gate",
        "callType" => "structured",
        "schema" => valid_schema
      }
    })
    |> render_change()

    refute has_element?(view, "#run-error")
    assert has_element?(view, "#run-submit:not([disabled])")
  end

  test "hydrates canonical state and profiles, then loads history when opened", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/state"} ->
          Req.Test.json(conn, APIFixtures.success(nil, APIFixtures.state()))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, html} = live(conn, ~p"/workspace")
    assert html =~ "Loading the canonical workspace"
    render_async(view, 1_000)

    assert has_element?(view, "#run-form")

    assert has_element?(
             view,
             ~s(#run-form input[name="run[selectedProfileId]"][value="Primary"])
           )

    view |> element("#history-fold-toggle") |> render_click()
    render_async(view, 1_000)
    assert has_element?(view, "#workspace-history-run-test")
  end

  test "draft save sends backend-owned state", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/state"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          state = Jason.decode!(body)
          send(test_pid, {:saved_state, state})
          Req.Test.json(conn, APIFixtures.success(nil, state))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    view
    |> form("#run-form", %{
      "run" => %{
        "selectedProfileId" => "Primary",
        "modelId" => "model-test",
        "userPrompt" => "updated safe prompt",
        "cacheMode" => "cache"
      }
    })
    |> render_change()

    render_async(view, 1_000)
    assert_received {:saved_state, %{"schemaVersion" => 1, "userPrompt" => "updated safe prompt"}}
  end

  test "individual select changes preserve the rest of the workspace draft", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/state"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          send(test_pid, {:select_saved_state, Jason.decode!(body)})
          Req.Test.json(conn, APIFixtures.success(nil, APIFixtures.state()))

        {"POST", "/api/v1/run"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          send(test_pid, {:select_run_payload, Jason.decode!(body)})
          Req.Test.json(conn, APIFixtures.success(APIFixtures.run_result()))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    view
    |> element("#input-advanced-toggle")
    |> render_click()

    view
    |> form("#run-form", %{
      "run" => %{
        "selectedProfileId" => "Primary",
        "userPrompt" => "select preservation prompt",
        "reasoningEffort" => "lowest",
        "cacheMode" => "cache"
      }
    })
    |> render_change()

    view
    |> element("#workspace-reasoning")
    |> render_change(%{"run" => %{"reasoningEffort" => "highest"}})

    view
    |> element("#workspace-cache")
    |> render_change(%{"run" => %{"cacheMode" => "cache"}})

    view
    |> with_target("#workspace-llm-widget")
    |> render_change("workspace-control", %{
      "_target" => ["run", "reasoningEffort"],
      "reasoningEffort" => "highest"
    })

    view
    |> with_target("#workspace-llm-widget")
    |> render_change("workspace-control", %{
      "_target" => ["run", "cacheMode"],
      "cacheMode" => "cache"
    })

    assert has_element?(view, ~s(#run_selectedProfileId[value="Primary"]))
    assert has_element?(view, "#run_userPrompt", "select preservation prompt")
    assert has_element?(view, ~s(#workspace-reasoning option[value="highest"][selected]))
    assert has_element?(view, ~s(#workspace-cache option[value="cache"][selected]))

    view |> form("#run-form") |> render_submit()
    render_async(view, 1_000)

    assert_received {:select_run_payload,
                     %{
                       "profileId" => "Primary",
                       "userPrompt" => "select preservation prompt",
                       "reasoningEffort" => "highest",
                       "cacheMode" => "cache"
                     }}
  end

  test "profile input preserves a custom typed value and persists it", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/state"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          send(test_pid, {:custom_profile_state, Jason.decode!(body)})
          Req.Test.json(conn, APIFixtures.success(nil, APIFixtures.state()))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    view
    |> form("#run-form", %{
      "run" => %{
        "selectedProfileId" => "typed-custom-profile",
        "userPrompt" => "custom profile draft",
        "cacheMode" => "cache"
      }
    })
    |> render_change()

    render_async(view, 1_000)

    assert has_element?(
             view,
             ~s(#run-form input[name="run[selectedProfileId]"][value="typed-custom-profile"])
           )

    assert_received {:custom_profile_state, %{"selectedProfileId" => "typed-custom-profile"}}
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-037
  test "workspace buttons, folds, and every run input are wired", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/state"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          Req.Test.json(conn, APIFixtures.success(nil, Jason.decode!(body)))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    view |> element("#model-config-toggle") |> render_click()
    render_async(view, 1_000)
    assert has_element?(view, "#model-options")
    assert has_element?(view, ~s(#input-advanced-toggle[phx-value-open="true"]))
    refute has_element?(view, ~s(#input-advanced-toggle[phx-value-value]))

    view |> element("#input-advanced-toggle") |> render_click()
    render_async(view, 1_000)
    view |> element("#profile-retry-toggle") |> render_click()
    render_async(view, 1_000)

    for selector <- [
          "#workspace-reasoning",
          "#workspace-cache",
          "#run_selectedProfileId",
          "#run_modelId",
          "#run_userPrompt",
          "#run_systemPrompt",
          "#run_callType",
          "#run_schemaShorthand",
          "#run_schema",
          "#profile-retry-repair",
          "#profile_structuredRepairRetryEnabled",
          "#profile_enableRetryOn429",
          "#profile_enableRetryOn5xx",
          "#profile_enableRetryOnNetworkError",
          "#profile_enableRetryOnParseError",
          "#profile_retryMaxAttempts",
          "#profile_retryBaseDelayMs",
          "#profile_retryMaxDelayMs",
          "#profile_escalationAttempt",
          "#profile-escalation-profile"
        ] do
      assert has_element?(view, selector), "missing workspace control #{selector}"
    end

    view
    |> form("#run-form", %{
      "run" => %{
        "selectedProfileId" => "Primary",
        "modelId" => "model-control",
        "userPrompt" => "control coverage prompt",
        "systemPrompt" => "control coverage system",
        "schemaShorthand" => ~s({"answer":"string"}),
        "schema" => "",
        "callType" => "text",
        "reasoningEffort" => "highest",
        "cacheMode" => "cache"
      }
    })
    |> render_change()

    view |> element("#generate-schema") |> render_click()
    assert has_element?(view, "#schema-status", "Schema generated.")
    assert has_element?(view, ~s(#run_callType option[value="structured"][selected]))
    view |> element("#check-schema") |> render_click()
    assert has_element?(view, "#schema-status", "Schema valid.")
    view |> element("#clear-schema") |> render_click()
    assert has_element?(view, ~s(#run_callType option[value="text"][selected]))
    view |> element("#new-conversation") |> render_click()
    render_async(view, 1_000)

    assert has_element?(view, "#run-submit")
    assert has_element?(view, "#output-trace-details-toggle") == false
    assert has_element?(view, "#history-fold-toggle")
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-038
  test "embedded profile widget exposes utility-llm controls through nested folds", %{conn: conn} do
    primary = widget_profile("CPA GPT-5.6 Luna", "gpt-5.6-luna")
    backup = widget_profile("Backup LLM", "backup-model")

    state =
      APIFixtures.state()
      |> Map.put("selectedProfileId", "CPA GPT-5.6 Luna")
      |> Map.put("modelId", "gpt-5.6-luna")

    Req.Test.stub(HardenAPI, fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/auth/session"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.principal()))

        {"GET", "/api/v1/state"} ->
          Req.Test.json(conn, APIFixtures.success(nil, state))

        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => [primary, backup]}))

        {"GET", "/api/v1/history"} ->
          Req.Test.json(conn, APIFixtures.success(%{"items" => []}))

        {"POST", "/api/v1/state"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          Req.Test.json(conn, APIFixtures.success(nil, Jason.decode!(body)))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    refute has_element?(view, "nav")
    refute has_element?(view, ~s([role="tab"]))
    assert has_element?(view, "#workspace-llm-widget .ullm-profile-category", "LLM")
    assert has_element?(view, ~s(#run_selectedProfileId[value="CPA GPT-5.6 Luna"]))
    assert has_element?(view, "#workspace-reasoning")
    assert has_element?(view, "#workspace-cache-toggle")
    assert has_element?(view, "#model-config-toggle")
    assert has_element?(view, ~s(#run_selectedProfileId[role="combobox"]))
    assert has_element?(view, ~s(#run_selectedProfileId-options[role="listbox"]))

    view |> element("#model-config-toggle") |> render_click()
    render_async(view, 1_000)

    for selector <- [
          "#model-options",
          "#profile-config-fields",
          "#profile_apiInferenceType",
          "#profile_baseUrl",
          "#profile-credential-toggle",
          "#profile-refresh-models",
          "#profile_modelId",
          "#profile-fallback-toggle",
          "#profile-fallback-list",
          "#profile-options-toggle",
          "#profile-retry-toggle",
          "#profile-pricing-toggle",
          "#profile-new",
          "#profile-bundle-file",
          "#profile-export-bundle",
          "#profile-save",
          "#profile-delete"
        ] do
      assert has_element?(view, selector), "missing profile widget control #{selector}"
    end

    assert has_element?(view, ~s(#profile_apiInferenceType[role="combobox"]))
    assert has_element?(view, ~s(#profile_baseUrl[role="combobox"]))
    assert has_element?(view, ~s(#profile_modelId[role="combobox"]))
    refute has_element?(view, ~s([role="tab"]))

    view |> element("#profile-credential-toggle") |> render_click()
    assert has_element?(view, "#profile-credential-drawer")
    view |> element("#profile-credential-toggle") |> render_click()
    refute has_element?(view, "#profile-credential-drawer")

    view |> element("#profile-fallback-toggle") |> render_click()
    assert has_element?(view, "#profile-fallback-options")

    view
    |> element("#profile-fallback-0")
    |> render_change(%{"profile" => %{"backupProfiles" => "Backup LLM"}, "index" => "0"})

    assert has_element?(view, "#profile-fallback-list", "Backup LLM")

    view |> element("#profile-options-toggle") |> render_click()

    for selector <- [
          "#profile-options",
          "#profile_maxTokens",
          "#profile_temperature",
          "#profile_topP",
          "#profile_topK",
          "#profile_stopSequences",
          "#profile_defaultOptionsJson"
        ] do
      assert has_element?(view, selector), "missing options control #{selector}"
    end

    view
    |> element("#profile_maxTokens")
    |> render_change(%{"profile" => %{"maxTokens" => "12000"}})

    assert has_element?(view, "#profile_defaultOptionsJson", ~s("max_tokens": 12000))

    view |> element("#profile-retry-toggle") |> render_click()

    for selector <- [
          "#profile-retry-repair",
          "#profile_structuredRepairRetryEnabled",
          "#profile_enableRetryOn429",
          "#profile_enableRetryOn5xx",
          "#profile_enableRetryOnNetworkError",
          "#profile_enableRetryOnParseError",
          "#profile_retryMaxAttempts",
          "#profile_retryBaseDelayMs",
          "#profile_retryMaxDelayMs",
          "#profile_escalationAttempt",
          "#profile-escalation-profile",
          "#profile-escalation-cache-toggle",
          "#profile-escalation-config-toggle"
        ] do
      assert has_element?(view, selector), "missing retry control #{selector}"
    end

    view |> element("#profile-pricing-toggle") |> render_click()
    html = render(view)

    for selector <- [
          "#profile-pricing",
          "#profile_pricingInput",
          "#profile_pricingOutput",
          "#profile_pricingCacheRead",
          "#profile_pricingCacheWrite",
          "#profile_pricingReasoning"
        ] do
      id = String.trim_leading(selector, "#")
      assert html =~ ~s(id="#{id}"), "missing pricing control #{selector}"
    end

    view |> element("#profile-escalation-config-toggle") |> render_click()
    html = render(view)
    assert html =~ ~s(id="profile-escalation-config")

    for selector <- [
          "#escalation-config-fields",
          "#escalation_apiInferenceType",
          "#escalation_baseUrl",
          "#escalation-credential-toggle",
          "#escalation-refresh-models",
          "#escalation_modelId",
          "#escalation-fallback-toggle",
          "#escalation-options-toggle",
          "#escalation-pricing-toggle",
          "#escalation-bundle-file",
          "#escalation-export-bundle",
          "#escalation-save",
          "#escalation-delete"
        ] do
      id = String.trim_leading(selector, "#")
      assert html =~ ~s(id="#{id}"), "missing nested profile control #{selector}"
    end

    assert has_element?(
             view,
             ~s(input[type="file"][name="escalation_profile_bundle"])
           )

    view
    |> with_target("#workspace-llm-widget")
    |> render_click("toggle-fold", %{"kind" => "escalation", "fold" => "options"})

    html = render(view)
    assert html =~ ~s(id="escalation-options")

    view
    |> with_target("#workspace-llm-widget")
    |> render_click("toggle-fold", %{"kind" => "escalation", "fold" => "pricing"})

    html = render(view)

    for selector <- [
          "#escalation-pricing"
        ] do
      id = String.trim_leading(selector, "#")
      assert html =~ ~s(id="#{id}"), "missing pricing control #{selector}"
    end
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-039
  test "embedded profile widget stages credentials and delegates profile mutations", %{conn: conn} do
    test_pid = self()
    primary = widget_profile("CPA GPT-5.6 Luna", "gpt-5.6-luna")
    backup = widget_profile("Backup LLM", "backup-model")
    refreshed = put_in(primary, ["profile", "models"], [%{"id" => "refreshed-model"}])

    state =
      APIFixtures.state()
      |> Map.put("selectedProfileId", "CPA GPT-5.6 Luna")
      |> Map.put("modelId", "gpt-5.6-luna")

    Req.Test.stub(HardenAPI, fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/auth/session"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.principal()))

        {"GET", "/api/v1/state"} ->
          Req.Test.json(conn, APIFixtures.success(nil, state))

        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => [primary, backup]}))

        {"POST", "/api/v1/state"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          Req.Test.json(conn, APIFixtures.success(nil, Jason.decode!(body)))

        {"POST", "/api/v1/profiles/CPA%20GPT-5.6%20Luna/models:refresh"} ->
          send(test_pid, :widget_refreshed)
          Req.Test.json(conn, APIFixtures.success(refreshed))

        {"PUT", "/api/v1/profiles/CPA%20GPT-5.6%20Luna"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          send(test_pid, {:widget_saved, Jason.decode!(body)})
          Req.Test.json(conn, APIFixtures.success(primary))

        {"DELETE", "/api/v1/profiles/CPA%20GPT-5.6%20Luna"} ->
          send(test_pid, :widget_deleted)
          Req.Test.json(conn, APIFixtures.success(%{"deleted" => true}))

        {"PUT", "/api/v1/profiles/bundle"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          send(test_pid, {:widget_imported, Jason.decode!(body)})
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => [primary, backup]}))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)
    view |> element("#model-config-toggle") |> render_click()
    render_async(view, 1_000)

    view |> element("#profile-credential-toggle") |> render_click()

    view
    |> with_target("#workspace-llm-widget")
    |> render_click("stage-key", %{"kind" => "main", "apiKey" => "widget-secret"})

    assert has_element?(view, "#profile-credential-toggle", "Replace key")
    refute render(view) =~ "widget-secret"

    view |> element("#profile-save") |> render_click()
    render_async(view, 1_000)
    assert_received {:widget_saved, payload}
    assert get_in(payload, ["credential", "apiKey"]) == "widget-secret"
    refute render(view) =~ "widget-secret"

    view |> element("#profile-refresh-models") |> render_click()
    render_async(view, 1_000)
    assert_received :widget_refreshed
    assert has_element?(view, ~s(#profile_modelId-options [data-value="refreshed-model"]))

    view |> element("#profile-delete") |> render_click()
    assert has_element?(view, "#profile-delete-confirmation")
    view |> element("#profile-delete-confirm") |> render_click()
    render_async(view, 1_000)
    assert_received :widget_deleted
    refute has_element?(view, "#profile-delete")

    upload =
      file_input(view, "#run-form", :profile_bundle, [
        %{
          name: "profiles.json",
          content: Jason.encode!(%{"schemaVersion" => 1}),
          type: "application/json"
        }
      ])

    render_upload(upload, "profiles.json")
    render_change(view, "import-bundle", %{"kind" => "main", "widget" => ""})
    assert_received {:widget_imported, %{"schemaVersion" => 1}}
    assert has_element?(view, ~s(#run_selectedProfileId-options [data-value="CPA GPT-5.6 Luna"]))
  end

  test "dirty profile configuration requires save before model refresh", %{conn: conn} do
    primary = widget_profile("Primary", "model-primary")

    install_stub(
      fn conn ->
        unexpected(conn)
      end,
      profiles: [primary]
    )

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)
    view |> element("#model-config-toggle") |> render_click()

    refute has_element?(view, "#profile-refresh-models[disabled]")

    view
    |> with_target("#workspace-llm-widget")
    |> render_change("profile-draft-change", %{
      "profile" => %{"baseUrl" => "https://changed.example.test/v1"}
    })

    assert has_element?(view, "#profile-save", "Save Profile")
    assert has_element?(view, "#profile-refresh-models[disabled]")
    assert has_element?(view, "#profile-save-required", "Save profile before refreshing models.")
  end

  test "ordinary profile drafts stay local until a committed model selection", %{conn: conn} do
    {:ok, counter} = Agent.start_link(fn -> 0 end)
    on_exit(fn -> if Process.alive?(counter), do: Agent.stop(counter) end)

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/state"} ->
          Agent.update(counter, &(&1 + 1))
          Req.Test.json(conn, APIFixtures.success(nil, APIFixtures.state()))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)
    view |> element("#model-config-toggle") |> render_click()
    render_async(view, 1_000)
    Agent.update(counter, fn _ -> 0 end)

    for value <- ["16001", "16002", "16003"] do
      view
      |> with_target("#workspace-llm-widget")
      |> render_change("profile-draft-change", %{"profile" => %{"maxTokens" => value}})
    end

    assert Agent.get(counter, & &1) == 0

    view
    |> with_target("#workspace-llm-widget")
    |> render_change("profile-draft-change", %{"profile" => %{"modelId" => "model-committed"}})

    render_async(view, 1_000)
    assert Agent.get(counter, & &1) == 1
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-040
  test "omits portable reasoning for a profile without a reasoning map", %{conn: conn} do
    test_pid = self()
    custom = widget_profile_without_reasoning("Custom LLM", "custom-model")

    state =
      APIFixtures.state()
      |> Map.put("selectedProfileId", "Custom LLM")
      |> Map.put("modelId", "custom-model")

    Req.Test.stub(HardenAPI, fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/auth/session"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.principal()))

        {"GET", "/api/v1/state"} ->
          Req.Test.json(conn, APIFixtures.success(nil, state))

        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => [custom]}))

        {"POST", "/api/v1/state"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          saved_state = Jason.decode!(body)
          send(test_pid, {:unmapped_profile_state, saved_state})
          Req.Test.json(conn, APIFixtures.success(nil, saved_state))

        {"POST", "/api/v1/run"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          send(test_pid, {:unmapped_run_payload, Jason.decode!(body)})
          Req.Test.json(conn, APIFixtures.success(APIFixtures.run_result()))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    assert has_element?(view, ~s(#workspace-reasoning[disabled]))
    assert has_element?(view, ~s(#workspace-reasoning option[value=""][selected]))

    view
    |> with_target("#workspace-llm-widget")
    |> render_change("select-profile", %{"run" => %{"selectedProfileId" => "Custom LLM"}})

    render_async(view, 1_000)

    assert_received {:unmapped_profile_state, saved_state}
    refute get_in(saved_state, ["reasoningByProfile", "Custom LLM"]) == ""

    submit_run(view, %{
      "selectedProfileId" => "Custom LLM",
      "modelId" => "custom-model"
    })

    render_async(view, 1_000)

    assert_received {:unmapped_run_payload, payload}
    refute Map.has_key?(payload, "reasoningEffort")
    assert has_element?(view, "#run-output", "fixture output")
  end

  test "workspace history restores and deletes records through the self-hosted API", %{conn: conn} do
    test_pid = self()
    {:ok, history_calls} = Agent.start_link(fn -> 0 end)

    install_stub(
      fn conn ->
        case {conn.method, conn.request_path} do
          {"POST", "/api/v1/state"} ->
            {:ok, body, conn} = Plug.Conn.read_body(conn)
            send(test_pid, {:workspace_state, Jason.decode!(body)})
            Req.Test.json(conn, APIFixtures.success(nil, APIFixtures.state()))

          {"DELETE", "/api/v1/history/run-test"} ->
            send(test_pid, {:workspace_delete_started, self()})

            receive do
              :release_workspace_delete ->
                send(test_pid, :workspace_deleted)
                Req.Test.json(conn, APIFixtures.success(%{"deleted" => true}))
            end

          _ ->
            unexpected(conn)
        end
      end,
      history: fn conn ->
        case Agent.get_and_update(history_calls, fn value -> {value, value + 1} end) do
          0 ->
            Req.Test.json(
              conn,
              APIFixtures.success(%{"items" => [APIFixtures.history_item()]})
            )

          1 ->
            send(test_pid, {:workspace_history_refresh_started, self()})

            receive do
              :release_workspace_history_refresh ->
                Req.Test.json(conn, APIFixtures.success(%{"items" => []}))
            end

          call ->
            flunk("unexpected history request #{call + 1}")
        end
      end
    )

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)
    view |> element("#history-fold-toggle") |> render_click()
    render_async(view, 1_000)

    view
    |> element(~s(button[phx-click="restore-history"][phx-value-run-id="run-test"]))
    |> render_click()

    render_async(view, 1_000)
    assert_received {:workspace_state, %{"userPrompt" => "safe restored prompt"}}

    assert has_element?(
             view,
             ~s(#run-form textarea[name="run[userPrompt]"]),
             "safe restored prompt"
           )

    assert has_element?(
             view,
             ~s|button[phx-click="delete-history"][phx-value-run-id="run-test"]:not([disabled])|
           )

    view
    |> element(~s(button[phx-click="delete-history"][phx-value-run-id="run-test"]))
    |> render_click()

    assert_receive {:workspace_delete_started, delete_process}
    refute has_element?(view, "#workspace-history-run-test")
    release_request(delete_process, :release_workspace_delete)
    render(view)
    assert_received :workspace_deleted

    assert_receive {:workspace_history_refresh_started, history_process}
    release_request(history_process, :release_workspace_history_refresh)
    render_async(view, 1_000)

    assert Agent.get(history_calls, & &1) == 2
    refute has_element?(view, "#workspace-history-run-test")
  end

  test "failed workspace history deletion restores the optimistically hidden row", %{conn: conn} do
    test_pid = self()

    state =
      APIFixtures.state()
      |> Map.put("ui", %{"historyOpen" => true})

    install_stub(
      fn conn ->
        case {conn.method, conn.request_path} do
          {"DELETE", "/api/v1/history/run-test"} ->
            send(test_pid, {:failing_workspace_delete_started, self()})

            receive do
              :release_failing_workspace_delete ->
                conn
                |> Plug.Conn.put_status(503)
                |> Req.Test.json(%{
                  "state" => %{},
                  "result" => nil,
                  "error" => %{"code" => "service_unavailable", "message" => "detail"}
                })
            end

          _ ->
            unexpected(conn)
        end
      end,
      state: state,
      history: fn conn ->
        send(test_pid, {:rollback_history_load_started, self()})

        receive do
          :release_rollback_history_load ->
            Req.Test.json(conn, APIFixtures.success(%{"items" => [APIFixtures.history_item()]}))
        end
      end
    )

    {:ok, view, _html} = live(conn, ~p"/workspace")
    assert_receive {:rollback_history_load_started, history_process}
    release_request(history_process, :release_rollback_history_load)
    render_async(view, 1_000)
    assert has_element?(view, "#workspace-history-run-test")

    view
    |> element(~s(button[phx-click="delete-history"][phx-value-run-id="run-test"]))
    |> render_click()

    assert_receive {:failing_workspace_delete_started, delete_process}
    refute has_element?(view, "#workspace-history-run-test")
    release_request(delete_process, :release_failing_workspace_delete)
    render_async(view, 1_000)

    assert has_element?(view, "#workspace-history-run-test")
    assert has_element?(view, ~s(#workspace-history-error[role="alert"]))
  end

  test "successful run refreshes an already loaded open history snapshot", %{conn: conn} do
    test_pid = self()
    {:ok, history_calls} = Agent.start_link(fn -> 0 end)

    state =
      APIFixtures.state()
      |> Map.put("ui", %{"historyOpen" => true})

    fresh_history =
      APIFixtures.history_item("run-fresh", "trace-fresh")
      |> put_in(["request", "userPrompt"], "fresh run prompt")

    run_result =
      APIFixtures.run_result()
      |> Map.put("runId", "run-fresh")
      |> Map.put("traceId", "trace-fresh")

    install_stub(
      fn conn ->
        case {conn.method, conn.request_path} do
          {"POST", "/api/v1/run"} ->
            send(test_pid, {:refresh_run_started, self()})

            receive do
              :release_refresh_run -> Req.Test.json(conn, APIFixtures.success(run_result))
            end

          _ ->
            unexpected(conn)
        end
      end,
      state: state,
      history: fn conn ->
        case Agent.get_and_update(history_calls, fn value -> {value, value + 1} end) do
          0 ->
            send(test_pid, {:loaded_history_started, self()})

            receive do
              :release_loaded_history ->
                Req.Test.json(
                  conn,
                  APIFixtures.success(%{"items" => [APIFixtures.history_item()]})
                )
            end

          1 ->
            send(test_pid, {:loaded_history_refresh_started, self()})

            receive do
              :release_loaded_history_refresh ->
                Req.Test.json(conn, APIFixtures.success(%{"items" => [fresh_history]}))
            end

          call ->
            flunk("unexpected history request #{call + 1}")
        end
      end
    )

    {:ok, view, _html} = live(conn, ~p"/workspace")
    assert_receive {:loaded_history_started, history_process}
    release_request(history_process, :release_loaded_history)
    render_async(view, 1_000)
    assert has_element?(view, "#workspace-history-run-test")

    submit_run(view, %{"userPrompt" => "fresh run prompt"})
    assert_receive {:refresh_run_started, run_process}
    release_request(run_process, :release_refresh_run)
    render(view)

    assert_receive {:loaded_history_refresh_started, refresh_process}
    release_request(refresh_process, :release_loaded_history_refresh)
    render_async(view, 1_000)

    assert Agent.get(history_calls, & &1) == 2
    assert has_element?(view, "#workspace-history-run-fresh", "fresh run prompt")
  end

  test "successful run coalesces a refresh behind an in-flight history load", %{conn: conn} do
    test_pid = self()
    {:ok, history_calls} = Agent.start_link(fn -> 0 end)

    state =
      APIFixtures.state()
      |> Map.put("ui", %{"historyOpen" => true})

    fresh_history =
      APIFixtures.history_item("run-fresh", "trace-fresh")
      |> put_in(["request", "userPrompt"], "fresh run prompt")

    run_result =
      APIFixtures.run_result()
      |> Map.put("runId", "run-fresh")
      |> Map.put("traceId", "trace-fresh")

    install_stub(
      fn conn ->
        case {conn.method, conn.request_path} do
          {"POST", "/api/v1/run"} ->
            send(test_pid, {:run_started, self()})

            receive do
              :release_run -> Req.Test.json(conn, APIFixtures.success(run_result))
            end

          _ ->
            unexpected(conn)
        end
      end,
      state: state,
      history: fn conn ->
        case Agent.get_and_update(history_calls, fn value -> {value, value + 1} end) do
          0 ->
            send(test_pid, {:initial_history_started, self()})

            receive do
              :release_initial_history ->
                Req.Test.json(
                  conn,
                  APIFixtures.success(%{"items" => [APIFixtures.history_item()]})
                )
            end

          1 ->
            send(test_pid, {:history_refresh_started, self()})

            receive do
              :release_history_refresh ->
                Req.Test.json(conn, APIFixtures.success(%{"items" => [fresh_history]}))
            end

          call ->
            flunk("unexpected history request #{call + 1}")
        end
      end
    )

    {:ok, view, _html} = live(conn, ~p"/workspace")
    assert_receive {:initial_history_started, history_pid}, 1_000
    assert has_element?(view, "#workspace-history-loading")

    submit_run(view, %{"userPrompt" => "fresh run prompt"})
    assert_receive {:run_started, run_pid}, 1_000
    release_request(run_pid, :release_run)
    assert has_element?(view, "#run-output")

    release_request(history_pid, :release_initial_history)
    render(view)

    assert_receive {:history_refresh_started, refresh_pid}
    release_request(refresh_pid, :release_history_refresh)
    render_async(view, 1_000)

    assert Agent.get(history_calls, & &1) == 2
    assert has_element?(view, "#workspace-history-run-fresh", "fresh run prompt")
  end

  test "workspace clear-history control deletes the loaded page", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/state"} ->
          Req.Test.json(conn, APIFixtures.success(nil, APIFixtures.state()))

        {"DELETE", "/api/v1/history"} ->
          send(test_pid, :workspace_cleared)
          Req.Test.json(conn, APIFixtures.success(%{"deleted" => 1}))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)
    view |> element("#history-fold-toggle") |> render_click()
    render_async(view, 1_000)

    assert has_element?(view, "#workspace-clear-history")
    view |> element("#workspace-clear-history") |> render_click()
    render_async(view, 1_000)

    assert_received :workspace_cleared
    refute has_element?(view, "#workspace-history-run-test")
  end

  test "one async run renders normalized result fields", %{conn: conn} do
    test_pid = self()

    run_result =
      APIFixtures.run_result()
      |> Map.put("artifacts", [
        %{
          "artifactId" => "artifact-test",
          "kind" => "trace",
          "state" => "available",
          "sha256" => String.duplicate("a", 64),
          "sizeBytes" => 1373,
          "contentType" => "application/json"
        }
      ])

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/run"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          send(test_pid, {:run_payload, Jason.decode!(body)})

          Req.Test.json(
            conn,
            APIFixtures.success(run_result, %{
              "lastRunId" => "run-test",
              "lastTraceId" => "trace-test"
            })
          )

        {"POST", "/api/v1/state"} ->
          Req.Test.json(conn, APIFixtures.success(nil, APIFixtures.state()))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    submit_run(view, %{"userPrompt" => "run fixture"})
    render_async(view, 1_000)
    assert_patch(view, ~p"/workspace?trace_id=trace-test")

    assert_received {:run_payload,
                     %{
                       "profileId" => "Primary",
                       "userPrompt" => "run fixture",
                       "callType" => "text",
                       "structuredRepair" => false
                     }}

    assert has_element?(view, "#run-output", "fixture output")
    assert has_element?(view, "#run-result-panel", "trace-test")
    assert has_element?(view, "#run-result-panel.ullm-widget.ullm-output-widget")
    assert has_element?(view, ".ullm-run-output-label", "Latest output")

    assert has_element?(
             view,
             ".ullm-run-output-meta",
             "responses · https://provider.example.test/v1"
           )

    assert has_element?(view, ".llm-trace-summary", "ID: trace-test")
    assert has_element?(view, ".llm-trace-summary", "Model: model-test")
    assert has_element?(view, ".llm-trace-summary", "📥 1")
    assert has_element?(view, ".llm-trace-summary", "📤 1")
    assert has_element?(view, "#output-trace-cache-status", "💾")
    assert has_element?(view, ~s(#output-trace-cache-status[data-cache-status="disabled"]))
    assert has_element?(view, ~s(#output-trace-cache-status[role="img"]))

    assert has_element?(
             view,
             ~s(#output-trace-cache-status[aria-label="Harden-LLM cache: Disabled"])
           )

    assert has_element?(
             view,
             ~s(#output-trace-cache-status[title="Harden-LLM cache was disabled for this run."])
           )

    assert has_element?(view, ".llm-trace-summary", "$0.0010")
    assert has_element?(view, ~s(.llm-trace-summary span[title="Completion tokens"]), "📤 1")
    assert has_element?(view, ".trace-controls #output-trace-details-toggle", "Hide")
    assert has_element?(view, ".trace-controls a", "View JSON Trace")
    assert has_element?(view, ".trace-controls #output-trace-copy-curl", "Copy cURL")
    assert has_element?(view, ".trace-controls #output-trace-show-request", "Show Request")
    assert has_element?(view, ".trace-controls #output-trace-show-response", "Show Response")
    assert has_element?(view, ~s(.trace-controls a[href="/traces/trace-test"]))
    assert has_element?(view, ~s(.trace-controls a[rel="noopener noreferrer"]))

    assert has_element?(
             view,
             ~s(.trace-controls a[href="/traces/trace-test/artifacts/artifact-test"]),
             "trace · 1373 bytes"
           )

    refute has_element?(view, "#run-artifacts")
    assert has_element?(view, ".llm-trace-details", "Success (200)")

    view
    |> form("#run-form", %{"run" => %{"userPrompt" => "changed after run"}})
    |> render_change()

    render_async(view, 1_000)
    assert has_element?(view, "#output-trace-copy-curl[data-copy-value*='run fixture']")
    view |> element("#output-trace-show-request") |> render_click()
    assert has_element?(view, "#output-trace-request-content", "run fixture")
    refute has_element?(view, "#output-trace-request-content", "changed after run")
  end

  test "workspace trace URL restores the redacted output after a hard refresh", %{conn: conn} do
    trace =
      APIFixtures.trace()
      |> Map.put("record", Map.put(APIFixtures.run_result(), "status", "succeeded"))

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/traces/trace-test"} ->
          Req.Test.json(conn, APIFixtures.success(trace))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace?trace_id=trace-test")
    render_async(view, 1_000)

    assert has_element?(view, "#run-output", "fixture output")
    assert has_element?(view, "#run-result-panel", "trace-test")
    assert has_element?(view, "#run-result-panel", "Success (200)")
    assert has_element?(view, ".trace-controls a[href=\"/traces/trace-test\"]", "View JSON Trace")

    view |> element("#output-trace-show-request") |> render_click()
    assert has_element?(view, "#output-trace-request-content", "safe restored prompt")
    refute has_element?(view, "#output-trace-request-content", "write a haiku joke")

    view |> element("#output-trace-show-response") |> render_click()
    assert has_element?(view, "#output-trace-response-content", "fixture output")
  end

  test "failed zero-token runs reload persisted trace details and resources", %{conn: conn} do
    failed_result =
      APIFixtures.run_result()
      |> Map.put("status", "failed")
      |> put_in(["attempts", Access.at(0), "category"], "rate_limit")
      |> put_in(["attempts", Access.at(0), "httpStatus"], 429)
      |> put_in(
        ["accounting", "result", "usage"],
        %{
          "inputTokens" => 0,
          "cacheReadTokens" => 0,
          "cacheCreationTokens" => 0,
          "outputTokens" => 0,
          "reasoningTokens" => 0,
          "promptTokens" => 0,
          "completionTokens" => 0,
          "totalTokens" => 0,
          "status" => "complete"
        }
      )

    trace =
      APIFixtures.trace()
      |> Map.put("record", failed_result)
      |> put_in(["resources", "response", "payload"], failed_result)

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/run"} ->
          conn
          |> Plug.Conn.put_status(503)
          |> Req.Test.json(%{
            "state" => %{"lastRunId" => "run-test", "lastTraceId" => "trace-test"},
            "result" => nil,
            "error" => %{"code" => "run_failed", "message" => "provider detail"}
          })

        {"GET", "/api/v1/traces/trace-test"} ->
          Req.Test.json(conn, APIFixtures.success(trace))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)
    submit_run(view, %{"userPrompt" => "failed fixture"})
    render_async(view, 1_000)

    assert_patch(view, ~p"/workspace?trace_id=trace-test")
    assert has_element?(view, "#run-error", "run outcome is unknown")
    refute render(view) =~ "provider detail"
    assert has_element?(view, ".llm-trace-summary", "ID: trace-test")
    assert has_element?(view, ".llm-trace-details", "Rate Limit (429)")
    assert has_element?(view, ".llm-trace-details", "Profile: Primary")
    assert has_element?(view, ".trace-controls #output-trace-show-request:not([disabled])")
    assert has_element?(view, ".trace-controls #output-trace-show-response:not([disabled])")
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-058
  test "workspace reset controls clear only their documented fields", %{conn: conn} do
    trace =
      APIFixtures.trace()
      |> Map.put("record", Map.put(APIFixtures.run_result(), "status", "succeeded"))

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/traces/trace-test"} ->
          Req.Test.json(conn, APIFixtures.success(trace))

        {"POST", "/api/v1/state"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          Req.Test.json(conn, APIFixtures.success(nil, Jason.decode!(body)))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace?trace_id=trace-test")
    render_async(view, 1_000)
    view |> element("#input-advanced-toggle") |> render_click()

    schema =
      Jason.encode!(%{
        "type" => "object",
        "properties" => %{"answer" => %{"type" => "string"}},
        "required" => ["answer"],
        "additionalProperties" => false
      })

    view
    |> form("#run-form", %{
      "run" => %{
        "userPrompt" => "prompt to clear",
        "systemPrompt" => "system to preserve",
        "schemaShorthand" => ~s({"answer":"string"}),
        "schema" => schema
      }
    })
    |> render_change()

    view |> element("#new-prompt") |> render_click()
    assert has_element?(view, "#run_systemPrompt", "system to preserve")
    assert has_element?(view, "#run_schemaShorthand", "answer")
    assert has_element?(view, "#run_schema", "additionalProperties")
    refute has_element?(view, "#run_userPrompt", "prompt to clear")

    view |> element("#clear-system-prompt") |> render_click()
    refute has_element?(view, "#run_systemPrompt", "system to preserve")
    assert has_element?(view, "#run_schema", "additionalProperties")

    view |> element("#new-conversation") |> render_click()
    assert_patch(view, ~p"/workspace")
    render_async(view, 1_000)

    refute has_element?(view, "#run-output")
    refute has_element?(view, "#run_schemaShorthand", "answer")
    refute has_element?(view, "#run_schema", "additionalProperties")
    assert has_element?(view, "#new-conversation", "New")
    assert has_element?(view, "#new-prompt", "New Prompt")
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-035
  test "output trace helpers normalize utility usage, retries, repair, and status fields" do
    [base_attempt] = APIFixtures.run_result()["attempts"]

    result =
      APIFixtures.run_result()
      |> Map.put("totalCallDurationMs", 1050)
      |> Map.put("attempts", [
        Map.merge(base_attempt, %{
          "number" => 1,
          "retryLocalNumber" => 1,
          "category" => "rate_limit",
          "httpStatus" => 429,
          "retryable" => true,
          "wait" => 500_000_000,
          "duration" => 25_000_000,
          "repair" => true
        }),
        Map.merge(base_attempt, %{
          "number" => 2,
          "retryLocalNumber" => 2,
          "category" => "success",
          "httpStatus" => 200,
          "wait" => 0,
          "duration" => 0
        })
      ])
      |> put_in(["accounting", "result", "usage"], %{
        "inputTokens" => 1_830,
        "cacheReadTokens" => 200,
        "cacheCreationTokens" => 50,
        "outputTokens" => 122,
        "reasoningTokens" => 8,
        "promptTokens" => 2_080,
        "completionTokens" => 130,
        "totalTokens" => 2_210,
        "status" => "complete"
      })

    assert LlmTraceProjection.meta(result) ==
             "responses · https://provider.example.test/v1"

    assert LlmTraceProjection.summary(result)["model_id"] == "model-test"
    assert LlmTraceProjection.trace_id(result) == "trace-test"
    assert LlmTraceProjection.trace_available?(result)
    assert LlmTraceProjection.attempt_count(result) == 2
    assert LlmTraceProjection.duration(result) == "1.05s"
    assert LlmTraceProjection.number(1_830) == "1,830"
    assert LlmTraceProjection.cache_tokens(result) == 250
    assert LlmTraceProjection.completion_tokens(result) == 130
    assert LlmTraceProjection.cost(result) == "$0.0010"

    assert LlmTraceProjection.cost_title(result) ==
             "Result exact trace-attributed cost $0.0010"

    assert LlmTraceProjection.cache_served?(result) == false
    assert LlmTraceProjection.cache_status(result) == "disabled"
    assert LlmTraceProjection.cache_status_label(result) == "Disabled"
    assert LlmTraceProjection.used_repair?(result)
    assert LlmTraceProjection.attempt_count(Map.put(result, "attempts", [])) == 0
    assert LlmTraceProjection.attempts(Map.put(result, "attempts", [])) == []
    assert LlmTraceProjection.json_text(nil) == ""

    cached =
      result
      |> put_in(["cache", "served"], true)
      |> put_in(["resultSource"], %{
        "kind" => "cache",
        "producer" => get_in(result, ["resultSource", "producer"])
      })

    assert LlmTraceProjection.cache_served?(cached)
    assert LlmTraceProjection.cache_status(cached) == "hit"
    assert LlmTraceProjection.cache_status_label(cached) == "Hit"
    assert LlmTraceProjection.cost(cached) == "🗄️$0.0010"

    assert LlmTraceProjection.cost_title(cached) ==
             "Cached result exact trace-attributed cost $0.0010"

    miss =
      put_in(result, ["cache"], %{
        "mode" => "cache",
        "status" => "miss",
        "served" => false,
        "written" => true
      })

    assert LlmTraceProjection.cache_status_label(miss) == "Miss · saved"

    assert LlmTraceProjection.cache_status_title(miss) ==
             "Harden-LLM cache miss: ran the provider and saved the successful response."

    assert [
             %{
               "attempt" => 1,
               "retry_local_attempt" => 1,
               "category" => "rate_limit",
               "status_code" => 429,
               "retryable" => true,
               "delay_ms" => 500,
               "duration_ms" => 25,
               "provider_used" => true
             },
             %{
               "attempt" => 2,
               "retry_local_attempt" => 2,
               "category" => "success",
               "status_code" => 200,
               "retryable" => false,
               "delay_ms" => 0,
               "duration_ms" => 0,
               "provider_used" => true
             }
           ] = LlmTraceProjection.attempts(result)

    failure =
      result
      |> Map.put("status", "failed")
      |> put_in(["attempts", Access.at(1), "category"], "rate_limit")
      |> put_in(["attempts", Access.at(1), "httpStatus"], 429)

    assert LlmTraceProjection.summary(failure)["status_icon"] == "❌"
    assert LlmTraceProjection.details(failure)["status"] == "Rate Limit (429)"
  end

  test "structured calls reject invalid local JSON without backend mutation", %{conn: conn} do
    install_stub(fn conn ->
      if conn.request_path == "/api/v1/run",
        do: flunk("invalid schema reached backend"),
        else: unexpected(conn)
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    submit_run(view, %{"callType" => "structured", "schema" => "{invalid"})
    assert has_element?(view, "#run-error", "valid JSON object schema")
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-034
  test "translated prompt shortcut, schema debounce, and output request/response controls render",
       %{
         conn: conn
       } do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/state"} ->
          Req.Test.json(conn, APIFixtures.success(nil, APIFixtures.state()))

        {"POST", "/api/v1/run"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.run_result()))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    assert render(view) =~ ~s(phx-hook="PromptShortcut")
    view |> element("#input-advanced-toggle") |> render_click()
    assert has_element?(view, ~s(#run_schema[phx-debounce="5000"]))

    submit_run(view, %{"userPrompt" => "resource controls"})
    render_async(view, 1_000)
    assert has_element?(view, "#output-trace-show-request")
    assert has_element?(view, "#output-trace-show-response")

    view |> element("#output-trace-show-request") |> render_click()
    view |> element("#output-trace-show-response") |> render_click()
    assert has_element?(view, "#output-trace-request-content", "profileId")
    assert has_element?(view, "#output-trace-response-content", "fixture output")

    view |> element("#output-trace-details-toggle") |> render_click()
    render_async(view, 1_000)
    refute has_element?(view, "#output-trace-details")
    assert has_element?(view, ".trace-controls #output-trace-details-toggle", "Details")
    assert has_element?(view, ".trace-controls #output-trace-show-request", "Show Request")
    assert has_element?(view, ".trace-controls #output-trace-show-response", "Show Response")

    view |> element("#output-trace-details-toggle") |> render_click()
    render_async(view, 1_000)
    assert has_element?(view, "#output-trace-show-request")
    assert has_element?(view, "#output-trace-show-response")
    refute has_element?(view, "#output-trace-request-content")
    refute has_element?(view, "#output-trace-response-content")
  end

  test "duplicate active submits are ignored and the run button alone is disabled", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/run"} ->
          send(test_pid, {:run_started, self()})

          receive do
            :release -> Req.Test.json(conn, APIFixtures.success(APIFixtures.run_result()))
          end

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)
    submit_run(view, %{"userPrompt" => "one active run"})
    assert_receive {:run_started, task_pid}, 1_000
    submit_run(view, %{"userPrompt" => "duplicate event"})
    refute_receive {:run_started, _pid}, 100
    assert has_element?(view, "#run-submit[disabled]")
    send(task_pid, :release)
    render_async(view, 1_000)
    assert has_element?(view, "#run-output")
  end

  test "ambiguous transport failure gives refresh-history guidance and performs one request", %{
    conn: conn
  } do
    {:ok, counter} = Agent.start_link(fn -> 0 end)

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/run"} ->
          Agent.update(counter, &(&1 + 1))
          Req.Test.transport_error(conn, :closed)

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)
    submit_run(view, %{"userPrompt" => "ambiguous run"})
    render_async(view, 1_000)

    assert has_element?(view, "#run-error", "Refresh History")
    assert Agent.get(counter, & &1) == 1
  end

  test "missing endpoint credential gives actionable guidance without refresh-history warning", %{
    conn: conn
  } do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/run"} ->
          {status, envelope} = APIFixtures.error(422, "credential_required")
          conn |> Plug.Conn.put_status(status) |> Req.Test.json(envelope)

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)
    submit_run(view, %{"userPrompt" => "missing credential run"})
    render_async(view, 1_000)

    assert has_element?(view, "#run-error", "no configured endpoint credential")
    refute has_element?(view, "#run-error", "Refresh History")
  end

  test "an active run 401 redirects through session revocation", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/run"} ->
          {status, envelope} = APIFixtures.error(401, "session_expired")
          conn |> Plug.Conn.put_status(status) |> Req.Test.json(envelope)

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)
    submit_run(view, %{"userPrompt" => "expired session"})

    assert_redirect(view, ~p"/session/expired", 1_000)
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-031
  test "translates shorthand schemas, persists folds, and sends bounded retry controls", %{
    conn: conn
  } do
    test_pid = self()
    backup = widget_profile("Backup", "repair-model")

    install_stub(
      fn conn ->
        case {conn.method, conn.request_path} do
          {"POST", "/api/v1/state"} ->
            {:ok, body, conn} = Plug.Conn.read_body(conn)
            send(test_pid, {:saved_parity_state, Jason.decode!(body)})
            Req.Test.json(conn, APIFixtures.success(nil, APIFixtures.state()))

          {"POST", "/api/v1/run"} ->
            {:ok, body, conn} = Plug.Conn.read_body(conn)
            send(test_pid, {:parity_run, Jason.decode!(body)})
            Req.Test.json(conn, APIFixtures.success(APIFixtures.run_result()))

          _ ->
            unexpected(conn)
        end
      end,
      profiles: [APIFixtures.profile_state(), backup]
    )

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    view |> element("#input-advanced-toggle") |> render_click()

    view
    |> form("#run-form", %{
      "run" => %{
        "selectedProfileId" => "Primary",
        "modelId" => "model-override",
        "userPrompt" => "schema parity",
        "schemaShorthand" => ~s({"answer":"string"}),
        "schema" => "",
        "reasoningEffort" => "highest",
        "cacheMode" => "cache"
      }
    })
    |> render_change()

    view |> element("#model-config-toggle") |> render_click()
    render_async(view, 1_000)
    view |> element("#profile-options-toggle") |> render_click()
    render_async(view, 1_000)
    view |> element("#profile-retry-toggle") |> render_click()
    render_async(view, 1_000)
    view |> element("#profile-pricing-toggle") |> render_click()
    render_async(view, 1_000)

    view
    |> with_target("#workspace-llm-widget")
    |> render_change("profile-draft-change", %{
      "profile" => %{
        "structuredRepairRetryEnabled" => "true",
        "enableRetryOn429" => "true",
        "enableRetryOn5xx" => "false",
        "enableRetryOnNetworkError" => "true",
        "enableRetryOnParseError" => "true",
        "retryMaxAttempts" => "4",
        "retryBaseDelayMs" => "500",
        "retryMaxDelayMs" => "8000",
        "escalationProfile" => "Backup",
        "escalationAttempt" => "3",
        "escalationReasoning" => "highest"
      }
    })

    view
    |> with_target("#workspace-llm-widget")
    |> render_change("profile-draft-change", %{"escalation" => %{"modelId" => "repair-model"}})

    view |> element("#generate-schema") |> render_click()
    render_async(view, 1_000)

    assert has_element?(view, "#schema-status", "Schema generated.")
    assert_received {:saved_parity_state, %{"schemaShorthand" => ~s({"answer":"string"})}}

    assert_received {:saved_parity_state,
                     %{
                       "ui" => %{
                         "modelOptionsOpen" => true,
                         "pricingOpen" => true,
                         "retryRepairOpen" => true
                       }
                     }}

    view |> form("#run-form") |> render_submit()
    render_async(view, 1_000)

    assert_received {:parity_run, payload}
    assert payload["profileId"] == "Primary"
    assert payload["modelId"] == "model-override"
    assert payload["callType"] == "structured"
    assert payload["structuredRepair"] == true
    assert payload["reasoningEffort"] == "highest"
    assert payload["cacheMode"] == "cache"
    assert payload["maxAttempts"] == 4
    assert payload["initialBackoffMs"] == 500

    assert payload["repairEscalation"] == %{
             "attempt" => 3,
             "profileId" => "Backup",
             "modelId" => "repair-model",
             "reasoningEffort" => "highest"
           }

    assert payload["providerOptions"]["max_tokens"] == 16_000
    refute Map.has_key?(payload["providerOptions"], "structuredRepairRetry")
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-041
  test "utility cache control is two-state and migrates legacy off drafts", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/state"} ->
          legacy_state = Map.put(APIFixtures.state(), "cacheMode", "off")
          Req.Test.json(conn, APIFixtures.success(nil, legacy_state))

        {"POST", "/api/v1/state"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          state = Jason.decode!(body)
          send(test_pid, {:cache_state, state})
          Req.Test.json(conn, APIFixtures.success(nil, state))

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)

    assert has_element?(view, ~s(#workspace-cache option[value="cache"][selected]))
    refute has_element?(view, ~s(#workspace-cache option[value="off"]))

    view |> element("#workspace-cache-toggle") |> render_click()
    assert has_element?(view, ~s(#workspace-cache option[value="refresh"][selected]))
    render_async(view, 1_000)
    assert_received {:cache_state, %{"cacheMode" => "refresh"}}

    view |> element("#workspace-cache-toggle") |> render_click()
    assert has_element?(view, ~s(#workspace-cache option[value="cache"][selected]))
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-042
  test "endpoint edits require an explicit profile save before running", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/state"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          state = Jason.decode!(body)
          Req.Test.json(conn, APIFixtures.success(nil, state))

        {"POST", "/api/v1/run"} ->
          flunk("an unsaved endpoint edit must not reach the run endpoint")

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/workspace")
    render_async(view, 1_000)
    view |> element("#model-config-toggle") |> render_click()

    view
    |> element("#profile_baseUrl")
    |> render_change(%{"profile" => %{"baseUrl" => "https://changed.example.test/v1"}})

    render_async(view, 1_000)
    submit_run(view, %{"userPrompt" => "should be blocked"})

    assert has_element?(
             view,
             "#run-error",
             "Save the LLM profile before running endpoint, credential, fallback, or identity changes."
           )
  end

  defp widget_profile(profile_id, model_id) do
    options = %{
      "max_tokens" => 16_000,
      "temperature" => 0.2,
      "top_p" => 0.95,
      "top_k" => 40,
      "stop" => ["DONE"],
      "maxAttempts" => 4,
      "baseDelayMs" => 500,
      "maxDelayMs" => 8_000,
      "enableRetryOn429" => true,
      "enableRetryOn5xx" => true,
      "enableRetryOnNetworkError" => true,
      "enableRetryOnParseError" => true,
      "structuredRepairRetry" => %{
        "enabled" => true,
        "escalation" => %{
          "attempt" => 3,
          "llmProfile" => profile_id,
          "reasoningEffort" => "highest"
        }
      }
    }

    APIFixtures.profile_state()
    |> put_in(["profile", "llmProfile"], profile_id)
    |> put_in(["profile", "modelId"], model_id)
    |> put_in(
      ["profile", "models"],
      [%{"id" => model_id, "label" => "Primary model"}, %{"id" => "alternate-model"}]
    )
    |> put_in(["profile", "defaultOptions"], options)
    |> put_in(
      ["profile", "reasoningEffortMap"],
      %{
        "lowest" => %{"reasoning" => %{"effort" => "low"}},
        "middle" => %{"reasoning" => %{"effort" => "medium"}},
        "highest" => %{"reasoning" => %{"effort" => "high"}}
      }
    )
    |> put_in(
      ["profile", "pricing"],
      %{
        "input_cost_per_token" => 0.0000002,
        "output_cost_per_token" => 0.0000012,
        "cache_read_input_token_cost" => 0.00000002,
        "cache_creation_input_token_cost" => 0.00000025,
        "output_cost_per_reasoning_token" => 0
      }
    )
    |> put_in(["credential", "credentialId"], "credential-#{profile_id}")
  end

  defp utility_preset_profiles do
    catalog_path =
      Path.expand("../../../../internal/profiles/default-profile-catalog.json", __DIR__)

    catalog_path
    |> File.read!()
    |> Jason.decode!()
    |> Enum.sort_by(fn {profile_id, _profile} -> profile_id end)
    |> Enum.map(fn {_profile_id, profile} ->
      %{"profile" => profile, "credential" => %{"configured" => false}}
    end)
  end

  defp widget_profile_without_reasoning(profile_id, model_id) do
    widget_profile(profile_id, model_id)
    |> update_in(["profile"], &Map.delete(&1, "reasoningEffortMap"))
  end

  defp submit_run(view, overrides) do
    unless has_element?(view, "#advanced-input") do
      view |> element("#input-advanced-toggle") |> render_click()
    end

    params =
      Map.merge(
        %{
          "selectedProfileId" => "Primary",
          "modelId" => "model-test",
          "systemPrompt" => "",
          "userPrompt" => "fixture prompt",
          "callType" => "text",
          "cacheMode" => "cache",
          "schema" => ""
        },
        overrides
      )

    view |> form("#run-form", %{"run" => params}) |> render_submit()
  end

  defp install_stub(handler, options \\ []) do
    Req.Test.stub(HardenAPI, fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/auth/session"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.principal()))

        {"GET", "/api/v1/state"} ->
          state = Keyword.get(options, :state, APIFixtures.state())
          Req.Test.json(conn, APIFixtures.success(nil, state))

        {"GET", "/api/v1/profiles"} ->
          profiles = Keyword.get(options, :profiles, [APIFixtures.profile_state()])
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => profiles}))

        {"GET", "/api/v1/stats"} ->
          case Keyword.get(options, :stats, APIFixtures.stats()) do
            stats when is_function(stats, 1) ->
              stats.(conn)

            stats ->
              Req.Test.json(conn, APIFixtures.success(stats))
          end

        {"GET", "/api/v1/history"} ->
          case Keyword.get(options, :history) do
            history when is_function(history, 1) ->
              history.(conn)

            _ ->
              Req.Test.json(conn, APIFixtures.success(%{"items" => [APIFixtures.history_item()]}))
          end

        _ ->
          handler.(conn)
      end
    end)
  end

  defp release_request(process, message) do
    monitor = Process.monitor(process)
    send(process, message)
    assert_receive {:DOWN, ^monitor, :process, ^process, :normal}, 1_000
  end

  defp unexpected(conn), do: flunk("unexpected API call: #{conn.method} #{conn.request_path}")
end
