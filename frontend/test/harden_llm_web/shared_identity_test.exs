defmodule HardenLlmWeb.SharedIdentityTest do
  use HardenLlmWeb.ConnCase, async: true
  import Phoenix.LiveViewTest, except: [live: 1, live: 2, live: 3]
  alias HardenLlmWeb.{AccessFixtures, APIFixtures, HardenAPI}
  alias PrlsWeb.Access.Client
  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001

  # WEB-TEST-104
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

  # WEB-TEST-105
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

  # WEB-TEST-109 WEB-TEST-112
  test "enabled logins enter without company selection or an HLLM grant", %{conn: conn} do
    Req.Test.stub(HardenAPI, fn request ->
      assert get_req_header(request, "authorization") == ["Bearer " <> APIFixtures.token()]
      assert get_req_header(request, "x-prls-session-reference") != []
      assert request.request_path == "/api/v1/traces/trace-test"
      Req.Test.json(request, APIFixtures.success(APIFixtures.trace()))
    end)

    for cookie <- [AccessFixtures.cookie(), AccessFixtures.company_cookie()] do
      assert conn
             |> put_req_header("cookie", cookie)
             |> get("/traces/trace-test")
             |> json_response(200) == APIFixtures.trace()
    end
  end

  # WEB-TEST-110
  test "Portal entry preserves product deep links and query strings", %{conn: conn} do
    assert conn |> get("/profiles?new=1") |> redirected_to() ==
             "https://portal.test/login?return_to=http%3A%2F%2Flocalhost%3A4000%2Fprofiles%3Fnew%3D1"
  end

  # WEB-TEST-111
  for {change, redirect} <- [
        {:company, nil},
        {:user, "/"},
        {:revoked,
         "https://portal.test/login?return_to=http%3A%2F%2Flocalhost%3A4000%2Fprofiles"},
        {:unavailable, "/session/unavailable"}
      ] do
    test "connected identity #{change} is enforced before a profile event", %{conn: conn} do
      identity = start_supervised!({Agent, fn -> {:ok, identity_context()} end})

      Req.Test.stub(Client, fn request ->
        case Agent.get(identity, & &1) do
          {:ok, context} ->
            Req.Test.json(request, context)

          {:error, status} ->
            request |> put_status(status) |> Req.Test.json(%{"error" => "identity unavailable"})
        end
      end)

      Req.Test.stub(HardenAPI, fn request ->
        assert request.method == "GET"
        assert request.request_path == "/api/v1/profiles"
        Req.Test.json(request, APIFixtures.profiles([]))
      end)

      {:ok, view, _} =
        live(put_req_header(conn, "cookie", AccessFixtures.dynamic_cookie()), "/profiles")

      render_async(view)
      :ok = Req.Test.allow(Client, self(), view.pid)
      Agent.update(identity, fn _ -> changed_identity(unquote(change)) end)

      assert_identity_event(view, unquote(redirect))
    end
  end

  # WEB-TEST-112
  test "gateway denial never retries without the human reference", %{conn: conn} do
    Req.Test.expect(HardenAPI, 1, fn request ->
      assert get_req_header(request, "x-prls-session-reference") == [AccessFixtures.session_ref()]
      {status, body} = APIFixtures.error(401)
      request |> put_status(status) |> Req.Test.json(body)
    end)

    assert conn
           |> put_req_header("cookie", AccessFixtures.cookie())
           |> get("/traces/trace-test")
           |> redirected_to() ==
             "https://portal.test/login?return_to=http%3A%2F%2Flocalhost%3A4000%2Ftraces%2Ftrace-test"
  end

  # WEB-TEST-113
  test "separate login requests forward separate references and expose no service bearer", %{
    conn: conn
  } do
    parent = self()

    Req.Test.stub(HardenAPI, fn request ->
      send(parent, {:reference, get_req_header(request, "x-prls-session-reference")})
      Req.Test.json(request, APIFixtures.success(APIFixtures.trace()))
    end)

    for {cookie, reference} <- [
          {AccessFixtures.cookie(), AccessFixtures.session_ref()},
          {AccessFixtures.second_cookie(), AccessFixtures.second_ref()}
        ] do
      response =
        conn |> put_req_header("cookie", cookie) |> get("/traces/trace-test") |> response(200)

      refute response =~ APIFixtures.token()
      assert_received {:reference, [^reference]}
    end
  end

  test "product logout preserves owner cookies and returns to Portal", %{conn: conn} do
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
           ) == 2
  end

  test "logout authority failure is unavailable without a success redirect", %{conn: conn} do
    Req.Test.expect(Client, 1, fn request ->
      request |> put_status(503) |> Req.Test.json(%{"error" => "unavailable"})
    end)

    response = conn |> put_req_header("cookie", AccessFixtures.cookie()) |> post("/logout")
    assert response.status == 503
    assert get_resp_header(response, "location") == []
  end

  # WEB-TEST-108
  test "the product cookie remains encrypted and host-only" do
    options = HardenLlmWeb.SessionOptions.options()
    assert Keyword.fetch!(options, :key) == "__Host-harden_llm_web"
    assert Keyword.fetch!(options, :path) == "/"
    assert Keyword.fetch!(options, :secure)
    assert Keyword.fetch!(options, :http_only)
    assert Keyword.fetch!(options, :same_site) == "Lax"
    refute Keyword.has_key?(options, :domain)
  end

  defp assert_identity_event(view, nil) do
    render_click(view, "new")
    assert has_element?(view, "#profile-form")
  end

  defp assert_identity_event(view, target) do
    assert {:error, {:redirect, %{to: ^target}}} = render_click(view, "new")
  end

  defp changed_identity(:company),
    do:
      {:ok,
       Map.put(identity_context(), "account", %{
         "account_id" => "11111111-1111-4111-8111-111111111111",
         "name" => "Other company",
         "products" => ["knowledge"]
       })}

  defp changed_identity(:user), do: {:ok, Map.put(identity_context(), "user_id", "another-user")}
  defp changed_identity(:revoked), do: {:error, 401}
  defp changed_identity(:unavailable), do: {:error, 503}

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
