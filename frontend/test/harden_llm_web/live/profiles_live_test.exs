defmodule HardenLlmWeb.ProfilesLiveTest do
  use HardenLlmWeb.ConnCase, async: true

  import Phoenix.LiveViewTest, except: [live: 1, live: 2, live: 3]

  alias HardenLlmWeb.{APIFixtures, HardenAPI}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-006

  setup %{conn: conn}, do: {:ok, conn: authenticated_conn(conn)}

  test "lists profiles with write-only credential state and refreshes models", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.profiles([APIFixtures.profile_state()]))

        {"POST", "/api/v1/profiles/Primary/models:refresh"} ->
          send(test_pid, :refreshed)
          Req.Test.json(conn, APIFixtures.success(APIFixtures.profile_state()))
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/profiles")
    render_async(view, 1_000)

    assert has_element?(view, "#profile-catalog-title", "Profile catalog")
    assert has_element?(view, "#profile-presets-help", "utility-llm presets")
    assert has_element?(view, "#profile-Primary")
    assert has_element?(view, "#profile-Primary", "Configured")
    refute render(view) =~ APIFixtures.token()

    view |> element(~s(button[phx-click="refresh"][phx-value-id="Primary"])) |> render_click()
    render_async(view, 1_000)
    assert_received :refreshed
  end

  test "profile deep links open the canonical editor after hydration", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.profiles([APIFixtures.profile_state()]))
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/profiles?edit=Primary")
    render_async(view, 1_000)

    assert has_element?(view, "#profile-editor")
    refute has_element?(view, "#profile-dialog")
    assert has_element?(view, "#profile-form")

    assert has_element?(
             view,
             "#profile-form input[name=\"profile[profileId]\"][value=\"Primary\"]"
           )

    assert has_element?(view, "#credential-status", "Stored key available")
    refute has_element?(view, "#credential-drawer")
  end

  test "create and edit use one mutation and never repopulate credential", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.profiles([APIFixtures.profile_state()]))

        {"PUT", "/api/v1/profiles/Primary"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          payload = Jason.decode!(body)
          send(test_pid, {:saved, payload})
          Req.Test.json(conn, APIFixtures.success(APIFixtures.profile_state()))
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/profiles")
    render_async(view, 1_000)
    view |> element(~s(button[phx-click="edit"][phx-value-id="Primary"])) |> render_click()
    open_credential_drawer(view)

    assert has_element?(view, "#profile-form")
    refute element(view, "#profile-form") |> render() =~ APIFixtures.token()
    assert has_element?(view, ~s(#profile-form input[name="profile[apiKey]"][value=""]))

    view
    |> form("#profile-form", %{
      "profile" => %{
        "profileId" => "Primary",
        "provider" => "openai",
        "apiInferenceType" => "responses",
        "baseUrl" => "https://provider.example.test/v1",
        "modelId" => "model-test",
        "credentialId" => "credential-test",
        "apiKey" => "replacement-fixture-secret",
        "supportsTemperature" => "true",
        "supportsContractedStructuredOutput" => "true"
      }
    })
    |> render_submit()

    render_async(view, 1_000)
    assert_received {:saved, payload}
    assert get_in(payload, ["credential", "apiKey"]) == "replacement-fixture-secret"
    refute Map.has_key?(payload["profile"], "backupProfiles")
    refute render(view) =~ "replacement-fixture-secret"
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-035
  test "cancelled staged replacement is omitted from the next profile save", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.profiles([APIFixtures.profile_state()]))

        {"PUT", "/api/v1/profiles/Primary"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          send(test_pid, {:saved_after_cancel, Jason.decode!(body)})
          Req.Test.json(conn, APIFixtures.success(APIFixtures.profile_state()))
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/profiles")
    render_async(view, 1_000)
    view |> element(~s(button[phx-click="edit"][phx-value-id="Primary"])) |> render_click()
    open_credential_drawer(view)

    view
    |> form("#profile-form", %{"profile" => %{"apiKey" => "cancelled-replacement-secret"}})
    |> render_change()

    view |> element(~s(button[phx-click="stage-key"])) |> render_click()
    open_credential_drawer(view)
    view |> element(~s(button[phx-click="cancel-key"])) |> render_click()
    refute render(view) =~ "cancelled-replacement-secret"

    view
    |> form("#profile-form", %{"profile" => %{"modelId" => "model-after-cancel"}})
    |> render_submit()

    render_async(view, 1_000)
    assert_received {:saved_after_cancel, payload}
    refute Map.has_key?(payload, "credential")
    refute Jason.encode!(payload) =~ "cancelled-replacement-secret"
  end

  test "backend field errors remain beside the matching profile input", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.profiles([]))

        {"PUT", "/api/v1/profiles/Unsafe"} ->
          {status, envelope} =
            APIFixtures.error(422, "profile_invalid", %{
              "profile.baseUrl" => "Use an approved HTTPS origin."
            })

          conn |> Plug.Conn.put_status(status) |> Req.Test.json(envelope)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/profiles")
    render_async(view, 1_000)
    view |> element("#new-profile") |> render_click()

    view
    |> form("#profile-form", %{
      "profile" => %{
        "profileId" => "Unsafe",
        "provider" => "openai",
        "apiInferenceType" => "responses",
        "baseUrl" => "https://unsafe.example.test/v1",
        "modelId" => "model-test",
        "credentialId" => "credential-test"
      }
    })
    |> render_submit()

    render_async(view, 1_000)
    assert has_element?(view, "#profile-form", "Use an approved HTTPS origin.")
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-035
  test "translated profile combobox suggestions and write-only key staging stay local", %{
    conn: conn
  } do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.profiles([APIFixtures.profile_state()]))

        _ ->
          flunk("unexpected API call: #{conn.method} #{conn.request_path}")
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/profiles")
    render_async(view, 1_000)
    view |> element(~s(button[phx-click="edit"][phx-value-id="Primary"])) |> render_click()
    open_credential_drawer(view)

    assert has_element?(view, "#profile-base-url-options")
    assert has_element?(view, "#profile-model-options")
    refute render(view) =~ APIFixtures.token()

    view
    |> form("#profile-form", %{"profile" => %{"apiKey" => "replacement-local-secret"}})
    |> render_change()

    view |> element(~s(button[phx-click="stage-key"])) |> render_click()
    refute has_element?(view, "#credential-drawer")
    assert has_element?(view, "#credential-status", "New key staged for save")

    open_credential_drawer(view)
    view |> element(~s(button[phx-click="cancel-key"])) |> render_click()
    refute has_element?(view, "#credential-drawer")
    refute render(view) =~ "replacement-local-secret"
  end

  test "delete confirmation preserves backend service errors", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.profiles([APIFixtures.profile_state()]))

        {"DELETE", "/api/v1/profiles/Primary"} ->
          {status, envelope} = APIFixtures.error(503, "service_unavailable")
          conn |> Plug.Conn.put_status(status) |> Req.Test.json(envelope)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/profiles")
    render_async(view, 1_000)

    view
    |> element(~s(button[phx-click="confirm-delete"][phx-value-id="Primary"]))
    |> render_click()

    assert has_element?(view, "#profile-delete-panel")
    refute has_element?(view, "#profile-delete-dialog")
    view |> element("#profile-delete-confirm") |> render_click()
    render_async(view, 1_000)

    assert has_element?(view, "#profile-Primary")
    assert has_element?(view, "#profiles-error", "temporarily unavailable")
  end

  test "bundle import is bounded and replaces state only after backend success", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.profiles([]))

        {"PUT", "/api/v1/profiles/bundle"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          send(test_pid, {:bundle, Jason.decode!(body)})
          Req.Test.json(conn, APIFixtures.profiles([APIFixtures.profile_state()]))
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/profiles")
    render_async(view, 1_000)

    upload =
      file_input(view, "#bundle-import-form", :bundle, [
        %{
          name: "bundle.json",
          content: Jason.encode!(%{"schemaVersion" => 2}),
          type: "application/json"
        }
      ])

    render_upload(upload, "bundle.json")
    view |> form("#bundle-import-form", %{}) |> render_submit()

    assert_received {:bundle, %{"schemaVersion" => 2}}
    assert has_element?(view, "#profile-Primary")
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-037
  test "every profile fold, field, and local action stays in the inline editor", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.profiles([APIFixtures.profile_state()]))

        _ ->
          flunk("unexpected API call: #{conn.method} #{conn.request_path}")
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/profiles")
    render_async(view, 1_000)

    view |> element("#new-profile") |> render_click()
    assert has_element?(view, "#profiles")
    assert has_element?(view, "#profile-editor")
    refute render(view) =~ "fixed inset-0"

    assert has_element?(view, "#profile_profileId")
    assert has_element?(view, "#profile_provider")
    assert has_element?(view, "#profile_apiInferenceType")
    assert has_element?(view, "#profile_modelId")
    assert has_element?(view, "#profile_baseUrl")
    assert has_element?(view, "#credential-fold-toggle")
    assert has_element?(view, "#profile_supportsTemperature")
    assert has_element?(view, "#profile_supportsContractedStructuredOutput")
    assert has_element?(view, "#options-fold-toggle")
    assert has_element?(view, "#retry-fold-toggle")
    assert has_element?(view, "#pricing-fold-toggle")
    assert has_element?(view, "#profile-cancel")
    assert has_element?(view, "#profile-save")

    view |> element("#options-fold-toggle") |> render_click()
    view |> element("#retry-fold-toggle") |> render_click()
    view |> element("#pricing-fold-toggle") |> render_click()

    assert has_element?(view, "#profile-options")
    assert has_element?(view, "#profile-recovery-policy")
    assert has_element?(view, "#profile-pricing")

    assert has_element?(view, "#profile_maxTokens")
    assert has_element?(view, "#profile_temperature")
    assert has_element?(view, "#profile_topP")
    assert has_element?(view, "#profile_topK")
    assert has_element?(view, "#profile_stopSequences")
    assert has_element?(view, "#profile_defaultOptionsJson")
    assert has_element?(view, "#profile-repair-invalid-output")
    assert has_element?(view, "#profile-retry-rate_limit")
    assert has_element?(view, "#profile-retry-server_error")
    assert has_element?(view, "#profile-retry-network")
    assert has_element?(view, "#profile-recovery-maxAttempts")
    assert has_element?(view, "#profile-recovery-baseDelayMs")
    assert has_element?(view, "#profile-recovery-maxDelayMs")
    assert has_element?(view, "#profile_pricingInput")
    assert has_element?(view, "#profile_pricingOutput")
    assert has_element?(view, "#profile_pricingCacheRead")
    assert has_element?(view, "#profile_pricingCacheWrite")
    assert has_element?(view, "#profile_pricingReasoning")

    assert has_element?(view, ~s(#profile_modelId[placeholder="gpt-5.6-luna"]))
    assert has_element?(view, ~s(#profile_baseUrl[placeholder="https://openrouter.ai/api/v1"]))
    assert has_element?(view, ~s(#profile_maxTokens[value="16000"][placeholder="16000"]))
    assert has_element?(view, ~s(#profile_temperature[placeholder="0.2"]))
    assert has_element?(view, ~s(#profile_topP[placeholder="0.95"]))
    assert has_element?(view, ~s(#profile_topK[placeholder="40"]))
    assert has_element?(view, ~s(#profile_stopSequences[placeholder="one sequence per line"]))

    assert has_element?(
             view,
             ~s(#profile_defaultOptionsJson[placeholder='{"temperature":0,"max_tokens":16000}'])
           )

    assert has_element?(view, ~s(#profile-recovery-maxAttempts[value="4"]))
    assert has_element?(view, ~s(#profile-recovery-baseDelayMs[value="500"]))
    assert has_element?(view, ~s(#profile-recovery-maxDelayMs[value="8000"]))
    assert has_element?(view, ~s(#profile_pricingInput[placeholder="n/a"]))
    assert has_element?(view, ~s(#profile_pricingOutput[placeholder="n/a"]))
    assert has_element?(view, ~s(#profile_pricingCacheRead[placeholder="n/a"]))
    assert has_element?(view, ~s(#profile_pricingCacheWrite[placeholder="n/a"]))
    assert has_element?(view, ~s(#profile_pricingReasoning[placeholder="n/a"]))

    view
    |> form("#profile-form", %{
      "profile" => %{
        "profileId" => "InlineProfile",
        "provider" => "openai",
        "apiInferenceType" => "responses",
        "baseUrl" => "https://provider.example.test/v1",
        "modelId" => "model-inline",
        "credentialId" => "credential-inline",
        "endpointCredentialScope" => "user",
        "apiKey" => "inline-secret",
        "supportsTemperature" => "true",
        "supportsContractedStructuredOutput" => "true",
        "maxTokens" => "128",
        "temperature" => "0.2",
        "topP" => "0.9",
        "topK" => "40",
        "stopSequences" => "END",
        "defaultOptionsJson" => "{}",
        "pricingInput" => "1",
        "pricingOutput" => "2",
        "pricingCacheRead" => "0.1",
        "pricingCacheWrite" => "0.2",
        "pricingReasoning" => "3"
      }
    })
    |> render_change()

    assert has_element?(view, "#credential-drawer")
    view |> element("#credential-fold-toggle") |> render_click()
    refute has_element?(view, "#credential-drawer")
    view |> element("#credential-fold-toggle") |> render_click()
    assert has_element?(view, "#credential-drawer")
    assert has_element?(view, "#profile_credentialId")
    assert has_element?(view, "#profile_endpointCredentialScope")
    assert has_element?(view, "#profile_apiKey")
    view |> element(~s(button[phx-click="clear-staged-key"])) |> render_click()
    view |> element(~s(button[phx-click="cancel-key"])) |> render_click()
    refute has_element?(view, "#credential-drawer")

    view |> element("#profile-cancel") |> render_click()
    refute has_element?(view, "#profile-editor")
  end

  test "an active profile operation 401 redirects through session revocation", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.profiles([APIFixtures.profile_state()]))

        {"POST", "/api/v1/profiles/Primary/models:refresh"} ->
          {status, envelope} = APIFixtures.error(401, "session_expired")
          conn |> Plug.Conn.put_status(status) |> Req.Test.json(envelope)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/profiles")
    render_async(view, 1_000)

    view |> element(~s(button[phx-click="refresh"][phx-value-id="Primary"])) |> render_click()

    assert_redirect(view, ~p"/session/expired", 1_000)
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-032
  test "profile editor translates options, recovery policy, and pricing", %{
    conn: conn
  } do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.profiles([APIFixtures.profile_state()]))

        {"PUT", "/api/v1/profiles/Primary"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          send(test_pid, {:profile_parity, Jason.decode!(body)})
          Req.Test.json(conn, APIFixtures.success(APIFixtures.profile_state()))
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/profiles")
    render_async(view, 1_000)
    view |> element(~s(button[phx-click="edit"][phx-value-id="Primary"])) |> render_click()
    open_credential_drawer(view)

    view
    |> element(~s(button[phx-click="toggle-section"][phx-value-section="options_open"]))
    |> render_click()

    view
    |> element(~s(button[phx-click="toggle-section"][phx-value-section="retry_open"]))
    |> render_click()

    view
    |> element(~s(button[phx-click="toggle-section"][phx-value-section="pricing_open"]))
    |> render_click()

    view
    |> form("#profile-form", %{
      "profile" => %{
        "profileId" => "Primary",
        "provider" => "openai",
        "apiInferenceType" => "responses",
        "baseUrl" => "https://provider.example.test/v1",
        "modelId" => "model-test",
        "credentialId" => "credential-test",
        "maxTokens" => "2048",
        "temperature" => "0.2",
        "topP" => "0.9",
        "topK" => "40",
        "stopSequences" => "END\nDONE",
        "defaultOptionsJson" => "{}",
        "pricingInput" => "1.5",
        "pricingOutput" => "3",
        "pricingCacheRead" => "0.2",
        "pricingCacheWrite" => "0.4",
        "pricingReasoning" => "5"
      }
    })
    |> render_submit()

    render_async(view, 1_000)
    assert_received {:profile_parity, payload}

    assert get_in(payload, ["profile", "recoveryPolicy"]) ==
             HardenLlmWeb.ProfileWidgetState.serialize_current_recovery_policy(
               APIFixtures.recovery_policy()
             )

    assert get_in(payload, ["profile", "defaultOptions", "max_tokens"]) == 2048
    assert get_in(payload, ["profile", "defaultOptions", "stop"]) == ["END", "DONE"]

    refute Map.has_key?(payload["profile"], "backupProfiles")
    refute Map.has_key?(payload["profile"]["defaultOptions"], "structuredRepairRetry")

    assert get_in(payload, ["profile", "pricing", "input_cost_per_token"]) == 0.0000015
  end

  defp install_stub(handler) do
    Req.Test.stub(HardenAPI, fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/auth/session"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.principal()))

        _ ->
          handler.(conn)
      end
    end)
  end

  defp open_credential_drawer(view) do
    view
    |> element("#credential-fold-toggle")
    |> render_click()
  end

  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-209
  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-073
  @tag :recovery
  test "profile payload uses the complete policy serializer" do
    policy = %{
      "maxAttempts" => 1,
      "retryOn" => [],
      "repairInvalidOutput" => false,
      "backoff" => %{"baseDelayMs" => 0, "maxDelayMs" => 0}
    }

    state = put_in(APIFixtures.profile_state(), ["profile", "recoveryPolicy"], policy)
    form = HardenLlmWeb.ProfilesLive.profile_form(state)
    assert form["recoveryPolicy"] == policy
    assert {:ok, payload} = HardenLlmWeb.ProfilesLive.profile_payload(form)
    assert payload["profile"]["schemaVersion"] == 3

    assert payload["profile"]["recoveryPolicy"] ==
             HardenLlmWeb.ProfileWidgetState.serialize_current_recovery_policy(policy)

    refute Map.has_key?(payload["profile"], "backupProfiles")
    refute Map.has_key?(payload["profile"]["defaultOptions"], "structuredRepairRetry")
  end

  @tag :recovery
  test "new profiles receive server defaults after hydration", %{conn: conn} do
    policy = %{
      APIFixtures.recovery_policy()
      | "maxAttempts" => 7,
        "repairInvalidOutput" => false,
        "retryOn" => [],
        "backoff" => %{"baseDelayMs" => 0, "maxDelayMs" => 0}
    }

    install_stub(fn conn ->
      assert conn.request_path == "/api/v1/profiles"

      Req.Test.json(
        conn,
        put_in(APIFixtures.profiles([]), ["result", "defaults", "recoveryPolicy"], policy)
      )
    end)

    {:ok, view, _} = live(conn, ~p"/profiles?new=1")
    render_async(view, 1_000)
    view |> element("#retry-fold-toggle") |> render_click()
    assert has_element?(view, ~s(#profile-recovery-maxAttempts[value="7"]))
    assert has_element?(view, ~s(#profile-recovery-baseDelayMs[value="0"]))
    refute has_element?(view, "#profile-repair-invalid-output[checked]")
    refute has_element?(view, "#profile-retry-network[checked]")
  end

  @tag :recovery
  test "profile editor exposes and adopts configured recovery targets", %{conn: conn} do
    defaults =
      APIFixtures.recovery_policy()
      |> Map.delete("repairInvalidOutput")
      |> Map.put("jsonRepair", %{
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
      })
      |> Map.put("rerun", %{
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
      })

    legacy_policy = %{
      "maxAttempts" => 4,
      "retryOn" => ["network"],
      "backoff" => %{"baseDelayMs" => 500, "maxDelayMs" => 8000},
      "jsonRepair" => nil,
      "rerun" => nil
    }

    profile = put_in(APIFixtures.profile_state(), ["profile", "recoveryPolicy"], legacy_policy)

    install_stub(fn conn ->
      assert conn.request_path == "/api/v1/profiles"

      Req.Test.json(
        conn,
        put_in(
          APIFixtures.profiles([profile]),
          ["result", "defaults", "recoveryPolicy"],
          defaults
        )
      )
    end)

    {:ok, view, _html} = live(conn, ~p"/profiles?edit=Primary")
    render_async(view, 1_000)
    view |> element("#retry-fold-toggle") |> render_click()

    refute has_element?(view, "#profile-original-repair-initial-profile")
    refute has_element?(view, "#profile-rerun-generation-profile")

    view |> element("#profile-json-repair-toggle") |> render_click()

    assert has_element?(
             view,
             ~s(#profile-original-repair-initial-profile[value="CPA GPT-5.6 Luna"])
           )

    refute has_element?(view, "#profile-original-repair-initial-profile[disabled]")

    view |> element("#profile-rerun-toggle") |> render_click()
    assert has_element?(view, ~s(#profile-rerun-generation-profile[value="CPA GPT-6 Astra"]))
    refute has_element?(view, "#profile-rerun-generation-profile[disabled]")

    view |> element("#profile-rerun-generation-config-toggle") |> render_click()
    assert has_element?(view, "#profile-rerun-generation-config-json-repair-toggle")
  end

  @tag :recovery
  test "policy validation errors mark the field and retain the submitted draft", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.profiles([APIFixtures.profile_state()]))

        {"PUT", "/api/v1/profiles/Primary"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          send(test_pid, {:invalid_policy, Jason.decode!(body)["profile"]["recoveryPolicy"]})

          {status, envelope} =
            APIFixtures.error(422, "validation_failed", %{
              "Primary.recoveryPolicy.maxAttempts" => "must be between 1 and 10"
            })

          conn |> Plug.Conn.put_status(status) |> Req.Test.json(envelope)
      end
    end)

    {:ok, view, _} = live(conn, ~p"/profiles?edit=Primary")
    render_async(view, 1_000)
    view |> element("#retry-fold-toggle") |> render_click()

    view
    |> form("#profile-form", %{"profile" => %{"recoveryPolicy" => %{"maxAttempts" => "11"}}})
    |> render_submit()

    render_async(view, 1_000)
    assert_received {:invalid_policy, %{"maxAttempts" => 11}}
    assert has_element?(view, ~s(#profile-recovery-maxAttempts[value="11"][aria-invalid="true"]))
    assert has_element?(view, "#profile-recovery-policy", "must be between 1 and 10")
  end
end
