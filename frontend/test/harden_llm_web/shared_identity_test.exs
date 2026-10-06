defmodule HardenLlmWeb.SharedIdentityTest do
  use HardenLlmWeb.ConnCase, async: true

  import Phoenix.LiveViewTest, except: [live: 1, live: 2, live: 3]

  alias HardenLlmWeb.{AccessFixtures, APIFixtures, HardenAPI}
  alias PrlsWeb.Access.Client

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-104 WEB-TEST-105 WEB-TEST-108

  test "only Portal hosts password and account routes", %{conn: conn} do
    for path <- ["/login", "/accounts"], method <- [:get, :post] do
      assert dispatch(conn, HardenLlmWeb.Endpoint, method, path) |> response(404)

      assert Phoenix.Router.route_info(
               HardenLlmWeb.Router,
               String.upcase(to_string(method)),
               path,
               "localhost"
             ) == :error
    end
  end

  test "unauthenticated requests preserve their return path", %{conn: conn} do
    assert conn |> get("/") |> redirected_to() ==
             "https://portal.test/login?return_to=http%3A%2F%2Flocalhost%3A4000%2F"
  end

  test "browser policy permits the Portal redirect after same-origin logout", %{conn: conn} do
    response = get(conn, "/")
    [policy] = get_resp_header(response, "content-security-policy")
    assert policy =~ "default-src 'self'"
    assert policy =~ "frame-ancestors 'none'"
    refute policy =~ "form-action"
  end

  test "enabled logins enter the single reference workspace without account selection", %{
    conn: conn
  } do
    Req.Test.stub(HardenAPI, fn request -> Req.Test.json(request, APIFixtures.models()) end)

    for cookie <- [AccessFixtures.cookie(), AccessFixtures.company_cookie()] do
      response = conn |> put_req_header("cookie", cookie) |> get("/")
      assert response.status == 200
      assert response.resp_body =~ ~s(id="workspace-page")
      refute response.resp_body =~ "company-select"
      refute response.resp_body =~ "profile-form"
    end

    assert Phoenix.Router.route_info(HardenLlmWeb.Router, "GET", "/profiles", "localhost") ==
             :error
  end

  test "proxy requests use one deployment bearer and never forward the human session", %{
    conn: conn
  } do
    parent = self()

    Req.Test.stub(HardenAPI, fn request ->
      send(parent, {
        :proxy_identity,
        Plug.Conn.get_req_header(request, "authorization"),
        Plug.Conn.get_req_header(request, "x-prls-session-reference"),
        Plug.Conn.get_req_header(request, "cookie")
      })

      Req.Test.json(request, APIFixtures.models())
    end)

    for cookie <- [AccessFixtures.cookie(), AccessFixtures.second_cookie()] do
      {:ok, view, _} =
        conn
        |> put_req_header("cookie", cookie)
        |> live("/")

      render_async(view, 1_000)
      assert_received {:proxy_identity, ["Bearer " <> token], [], []}
      assert token == APIFixtures.token()
    end
  end

  test "revoked access is revalidated before a Responses request is dispatched", %{conn: conn} do
    identity = start_supervised!({Agent, fn -> {:ok, identity_context()} end})
    parent = self()

    Req.Test.stub(Client, fn request ->
      case Agent.get(identity, & &1) do
        {:ok, context} ->
          Req.Test.json(request, context)

        {:error, status} ->
          request |> Plug.Conn.put_status(status) |> Req.Test.json(%{"error" => "unavailable"})
      end
    end)

    Req.Test.stub(HardenAPI, fn request ->
      send(parent, {:proxy_call, request.method, request.request_path})

      if request.request_path == "/v1/models" do
        Req.Test.json(request, APIFixtures.models())
      else
        Req.Test.json(request, APIFixtures.response())
      end
    end)

    {:ok, view, _} =
      conn
      |> put_req_header("cookie", AccessFixtures.dynamic_cookie())
      |> live("/")

    render_async(view, 1_000)
    :ok = Req.Test.allow(Client, self(), view.pid)
    Agent.update(identity, fn _ -> {:error, 401} end)

    result =
      view
      |> form("#run-form", %{
        "run" => %{"model" => "model-test", "userPrompt" => "must not dispatch"}
      })
      |> render_submit()

    assert {:error, {:redirect, %{to: target}}} = result
    assert target =~ "login?return_to="
    refute_receive {:proxy_call, "POST", "/v1/responses"}, 50
  end

  test "Portal logout preserves owner cookies and returns to Portal", %{conn: conn} do
    Req.Test.expect(Client, 1, fn request ->
      assert request.method == "POST"
      assert request.request_path == "/api/auth/sign-out"
      assert get_req_header(request, "cookie") == [AccessFixtures.cookie()]
      assert get_req_header(request, "origin") == ["http://localhost:4000"]

      request
      |> prepend_resp_headers([
        {"set-cookie",
         "prls.session_token=; Domain=prls.co; Path=/; Max-Age=0; Secure; HttpOnly"},
        {"set-cookie", "prls.session_data=; Domain=prls.co; Path=/; Max-Age=0; Secure; HttpOnly"}
      ])
      |> Req.Test.json(%{"ok" => true})
    end)

    response = conn |> put_req_header("cookie", AccessFixtures.cookie()) |> post("/logout")
    assert redirected_to(response) == "https://portal.test/login"

    assert Enum.count(
             get_resp_header(response, "set-cookie"),
             &String.contains?(&1, "Domain=prls.co")
           ) ==
             2
  end

  test "logout authority failure stays unavailable", %{conn: conn} do
    Req.Test.expect(Client, 1, fn request ->
      request |> Plug.Conn.put_status(503) |> Req.Test.json(%{"error" => "unavailable"})
    end)

    response = conn |> put_req_header("cookie", AccessFixtures.cookie()) |> post("/logout")
    assert response.status == 503
    assert get_resp_header(response, "location") == []
  end

  test "the product cookie remains encrypted and host-only" do
    options = HardenLlmWeb.SessionOptions.options()
    assert Keyword.fetch!(options, :key) == "__Host-harden_llm_web"
    assert Keyword.fetch!(options, :path) == "/"
    assert Keyword.fetch!(options, :secure)
    assert Keyword.fetch!(options, :http_only)
    assert Keyword.fetch!(options, :same_site) == "Lax"
    refute Keyword.has_key?(options, :domain)
  end

  defp identity_context do
    %{
      "user_id" => "fixture-user",
      "email" => "operator@example.test",
      "name" => "Test Operator",
      "role" => "operator",
      "session_ref" => AccessFixtures.dynamic_ref(),
      "account" => nil
    }
  end
end
