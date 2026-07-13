defmodule HardenLlmWeb.AuthTest do
  use HardenLlmWeb.ConnCase, async: false

  import Phoenix.LiveViewTest

  alias HardenLlmWeb.{APIFixtures, HardenAPI, SessionVault}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-005

  setup do
    Req.Test.set_req_test_to_shared()
    :ok
  end

  test "unauthenticated LiveViews redirect to login", %{conn: conn} do
    assert {:error, {:redirect, %{to: "/login"}}} = live(conn, ~p"/workspace")
  end

  test "mount validates the backend session and token never enters rendered HTML", %{conn: conn} do
    handle = APIFixtures.insert_session()
    install_workspace_stub()

    conn = init_test_session(conn, APIFixtures.session_map(handle))
    {:ok, view, html} = live(conn, ~p"/workspace")

    assert has_element?(view, "#workspace-page")
    refute html =~ APIFixtures.token()
    refute render(view) =~ APIFixtures.token()
    assert has_element?(view, "#backend-status")
  end

  test "backend 401 redirects through the cookie-clearing endpoint", %{conn: conn} do
    handle = APIFixtures.insert_session()

    Req.Test.stub(HardenAPI, fn conn ->
      {status, envelope} = APIFixtures.error(401, "session_expired")
      conn |> Plug.Conn.put_status(status) |> Req.Test.json(envelope)
    end)

    conn = init_test_session(conn, APIFixtures.session_map(handle))
    assert {:error, {:redirect, %{to: "/session/expired"}}} = live(conn, ~p"/workspace")

    expired = get(conn, ~p"/session/expired")
    assert redirected_to(expired) == ~p"/login"
    assert SessionVault.lookup(handle) == :error
  end

  defp install_workspace_stub do
    Req.Test.stub(HardenAPI, fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/auth/session"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.principal()))

        {"GET", "/api/v1/state"} ->
          Req.Test.json(conn, APIFixtures.success(nil, APIFixtures.state()))

        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.success(%{"profiles" => [APIFixtures.profile_state()]}))

        {"GET", "/api/v1/history"} ->
          Req.Test.json(conn, APIFixtures.success(%{"items" => [APIFixtures.history_item()]}))
      end
    end)
  end
end
