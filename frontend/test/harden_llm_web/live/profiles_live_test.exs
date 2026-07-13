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

    assert has_element?(view, "#profile-delete-dialog")
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
end
