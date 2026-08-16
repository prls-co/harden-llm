defmodule HardenLlmWeb.SessionControllerTest do
  use HardenLlmWeb.ConnCase, async: false

  alias HardenLlmWeb.{APIFixtures, HardenAPI, SessionVault}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-004

  setup do
    Req.Test.set_req_test_to_private()
    :ok
  end

  test "GET /login renders a labeled CSRF-protected form", %{conn: conn} do
    conn = get(conn, ~p"/login")
    html = html_response(conn, 200)
    assert html =~ ~s(id="login-form")
    assert html =~ ~s(name="_csrf_token")
    assert html =~ ~s(autocomplete="current-password")
  end

  test "login rotates the handle and keeps the backend token out of session and response", %{
    conn: conn
  } do
    old_handle = APIFixtures.insert_session()

    Req.Test.stub(HardenAPI, fn conn ->
      Req.Test.json(conn, APIFixtures.success(APIFixtures.login_result()))
    end)

    conn =
      conn
      |> init_test_session(APIFixtures.session_map(old_handle))
      |> post(~p"/login", %{
        "session" => %{"email" => "operator@example.test", "password" => "fixture-password-123"}
      })

    assert redirected_to(conn) == ~p"/workspace"
    new_handle = get_session(conn, "session_handle")
    assert is_binary(new_handle)
    refute new_handle == old_handle
    assert SessionVault.lookup(old_handle) == :error
    assert {:ok, token, _expiry_ms} = SessionVault.lookup(new_handle)
    assert token == APIFixtures.token()

    session = get_session(conn)
    refute inspect(session) =~ APIFixtures.token()
    refute conn.resp_body =~ APIFixtures.token()
    assert session["identity"] == %{"email" => "operator@example.test", "ownerId" => "owner-test"}
  end

  test "logout revokes locally even when the backend is unavailable", %{conn: conn} do
    handle = APIFixtures.insert_session()

    Req.Test.stub(HardenAPI, fn conn -> Req.Test.transport_error(conn, :closed) end)

    conn =
      conn
      |> init_test_session(APIFixtures.session_map(handle))
      |> post(~p"/logout")

    assert redirected_to(conn) == ~p"/login"
    assert SessionVault.lookup(handle) == :error
    assert get_session(conn, "session_handle") == nil
  end

  test "malformed login response never stores a token", %{conn: conn} do
    Req.Test.stub(HardenAPI, fn conn ->
      Req.Test.json(conn, %{
        "state" => %{},
        "result" => %{"accessToken" => APIFixtures.token()},
        "error" => nil
      })
    end)

    before_count = SessionVault.count()

    conn =
      post(conn, ~p"/login", %{
        "session" => %{"email" => "operator@example.test", "password" => "fixture-password-123"}
      })

    assert redirected_to(conn) == ~p"/login"
    assert SessionVault.count() == before_count
  end

  test "invalid CSRF submit is rejected", %{conn: conn} do
    assert_raise Plug.CSRFProtection.InvalidCSRFTokenError, fn ->
      conn
      |> Map.update!(:private, &Map.delete(&1, :plug_skip_csrf_protection))
      |> init_test_session(%{})
      |> post(~p"/login", %{
        "_csrf_token" => "invalid",
        "session" => %{"email" => "operator@example.test", "password" => "fixture-password-123"}
      })
    end
  end
end
