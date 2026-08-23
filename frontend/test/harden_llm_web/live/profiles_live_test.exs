defmodule HardenLlmWeb.ProfilesLiveTest do
  use HardenLlmWeb.ConnCase, async: false

  import Phoenix.LiveViewTest

  alias HardenLlmWeb.{APIFixtures, HardenAPI}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-006

  setup %{conn: conn} do
    Req.Test.set_req_test_to_shared()
    handle = APIFixtures.insert_session()
    {:ok, conn: init_test_session(conn, APIFixtures.session_map(handle))}
  end

  test "lists profiles with write-only credential state and refreshes models", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => [APIFixtures.profile_state()]}))

        {"POST", "/api/v1/profiles/Primary/models:refresh"} ->
          send(test_pid, :refreshed)
          Req.Test.json(conn, APIFixtures.success(APIFixtures.profile_state()))
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/profiles")
    render_async(view, 1_000)

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
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => [APIFixtures.profile_state()]}))
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
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => [APIFixtures.profile_state()]}))

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
        "backupProfiles" => "Backup",
        "supportsTemperature" => "true",
        "supportsContractedStructuredOutput" => "true"
      }
    })
    |> render_submit()

    render_async(view, 1_000)
    assert_received {:saved, payload}
    assert get_in(payload, ["credential", "apiKey"]) == "replacement-fixture-secret"
    assert get_in(payload, ["profile", "backupProfiles"]) == ["Backup"]
    refute render(view) =~ "replacement-fixture-secret"
  end

  test "backend field errors remain beside the matching profile input", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => []}))

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
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => [APIFixtures.profile_state()]}))

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

  test "delete confirmation preserves backend dependency errors", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => [APIFixtures.profile_state()]}))

        {"DELETE", "/api/v1/profiles/Primary"} ->
          {status, envelope} = APIFixtures.error(409, "profile_referenced")
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
    assert has_element?(view, "#profiles-error", "conflicts with current backend state")
  end

  test "bundle import is bounded and replaces state only after backend success", %{conn: conn} do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => []}))

        {"PUT", "/api/v1/profiles/bundle"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          send(test_pid, {:bundle, Jason.decode!(body)})
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => [APIFixtures.profile_state()]}))
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/profiles")
    render_async(view, 1_000)

    upload =
      file_input(view, "#bundle-import-form", :bundle, [
        %{
          name: "bundle.json",
          content: Jason.encode!(%{"schemaVersion" => 1}),
          type: "application/json"
        }
      ])

    render_upload(upload, "bundle.json")
    view |> form("#bundle-import-form", %{}) |> render_submit()

    assert_received {:bundle, %{"schemaVersion" => 1}}
    assert has_element?(view, "#profile-Primary")
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-037
  test "every profile fold, field, and local action stays in the inline editor", %{conn: conn} do
    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => [APIFixtures.profile_state()]}))

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
    assert has_element?(view, "#backup-profile-picker")
    assert has_element?(view, "#profile_backupProfiles")
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
    assert has_element?(view, "#profile-retry-repair")
    assert has_element?(view, "#profile-pricing")

    assert has_element?(view, "#profile_maxTokens")
    assert has_element?(view, "#profile_temperature")
    assert has_element?(view, "#profile_topP")
    assert has_element?(view, "#profile_topK")
    assert has_element?(view, "#profile_stopSequences")
    assert has_element?(view, "#profile_defaultOptionsJson")
    assert has_element?(view, "#profile_structuredRepairRetryEnabled")
    assert has_element?(view, "#profile_enableRetryOn429")
    assert has_element?(view, "#profile_enableRetryOn5xx")
    assert has_element?(view, "#profile_enableRetryOnNetworkError")
    assert has_element?(view, "#profile_enableRetryOnParseError")
    assert has_element?(view, "#profile_retryMaxAttempts")
    assert has_element?(view, "#profile_retryBaseDelayMs")
    assert has_element?(view, "#profile_retryMaxDelayMs")
    assert has_element?(view, "#profile_escalationProfile")
    assert has_element?(view, "#profile_escalationAttempt")
    assert has_element?(view, "#profile_escalationReasoning")
    assert has_element?(view, "#profile_pricingInput")
    assert has_element?(view, "#profile_pricingOutput")
    assert has_element?(view, "#profile_pricingCacheRead")
    assert has_element?(view, "#profile_pricingCacheWrite")
    assert has_element?(view, "#profile_pricingReasoning")

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
        "backupProfiles" => "Primary",
        "supportsTemperature" => "true",
        "supportsContractedStructuredOutput" => "true",
        "maxTokens" => "128",
        "temperature" => "0.2",
        "topP" => "0.9",
        "topK" => "40",
        "stopSequences" => "END",
        "defaultOptionsJson" => "{}",
        "structuredRepairRetryEnabled" => "true",
        "enableRetryOn429" => "true",
        "enableRetryOn5xx" => "true",
        "enableRetryOnNetworkError" => "true",
        "enableRetryOnParseError" => "true",
        "retryMaxAttempts" => "3",
        "retryBaseDelayMs" => "100",
        "retryMaxDelayMs" => "1000",
        "escalationProfile" => "Primary",
        "escalationAttempt" => "2",
        "escalationReasoning" => "lowest",
        "pricingInput" => "1",
        "pricingOutput" => "2",
        "pricingCacheRead" => "0.1",
        "pricingCacheWrite" => "0.2",
        "pricingReasoning" => "3"
      }
    })
    |> render_change()

    assert has_element?(view, "#backup-profile-list", "Primary")

    view
    |> element("#backup-profile-picker")
    |> render_change(%{"profile" => %{"backupProfile" => "Primary"}})

    assert has_element?(view, "#backup-profile-list", "Primary")

    view
    |> element(~s(button[phx-click="move-backup"][phx-value-direction="down"]))
    |> render_click()

    view
    |> element(~s(button[phx-click="move-backup"][phx-value-direction="up"]))
    |> render_click()

    view |> element(~s(button[phx-click="remove-backup"])) |> render_click()
    assert has_element?(view, "#backup-profile-list")

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
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => [APIFixtures.profile_state()]}))

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
  test "profile editor translates options, ordered fallbacks, retry repair, and pricing", %{
    conn: conn
  } do
    test_pid = self()

    install_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => [APIFixtures.profile_state()]}))

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
        "backupProfiles" => "Backup, Fallback",
        "maxTokens" => "2048",
        "temperature" => "0.2",
        "topP" => "0.9",
        "topK" => "40",
        "stopSequences" => "END\nDONE",
        "defaultOptionsJson" => "{}",
        "structuredRepairRetryEnabled" => "true",
        "retryMaxAttempts" => "4",
        "retryBaseDelayMs" => "500",
        "retryMaxDelayMs" => "8000",
        "escalationAttempt" => "3",
        "escalationProfile" => "Backup",
        "escalationReasoning" => "highest",
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
    assert get_in(payload, ["profile", "backupProfiles"]) == ["Backup", "Fallback"]
    assert get_in(payload, ["profile", "defaultOptions", "max_tokens"]) == 2048
    assert get_in(payload, ["profile", "defaultOptions", "stop"]) == ["END", "DONE"]

    assert get_in(payload, [
             "profile",
             "defaultOptions",
             "structuredRepairRetry",
             "escalation",
             "llmProfile"
           ]) == "Backup"

    options = get_in(payload, ["profile", "defaultOptions"])
    assert options["maxAttempts"] == 4
    assert options["baseDelayMs"] == 500
    assert options["maxDelayMs"] == 8000
    assert options["enableRetryOn429"] == true
    assert options["enableRetryOn5xx"] == true
    assert options["enableRetryOnNetworkError"] == true
    assert options["enableRetryOnParseError"] == true
    assert get_in(options, ["structuredRepairRetry", "enabled"]) == true
    refute Map.has_key?(options["structuredRepairRetry"], "maxAttempts")
    refute Map.has_key?(options["structuredRepairRetry"], "enableRetryOn429")

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
end
