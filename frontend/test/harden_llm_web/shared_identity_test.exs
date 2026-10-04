defmodule HardenLlmWeb.SharedIdentityTest do
  use HardenLlmWeb.ConnCase, async: true
  import Phoenix.LiveViewTest, except: [live: 1, live: 2, live: 3]
  alias HardenLlmWeb.{AccessFixtures, APIFixtures, HardenAPI}
  alias PrlsWeb.Access.Client
  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001

  # WEB-TEST-104
  test "sign-in is rendered by the shared PRLS Web controller", %{conn: conn} do
    assert conn |> get("/login") |> response(200) =~ "Sign in"
  end

  # WEB-TEST-105
  test "unauthenticated requests preserve their return path", %{conn: conn} do
    assert conn |> get("/") |> redirected_to() == "/login?return_to=%2F"
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
  test "shared sign-in defaults to workspace, preserves deep links and rejects unsafe paths", %{
    conn: conn
  } do
    Req.Test.stub(Client, fn request ->
      assert request.method == "POST"
      assert request.request_path == "/api/auth/sign-in/email"
      assert get_req_header(request, "origin") == ["http://localhost:4000"]

      request
      |> put_resp_header("set-cookie", "prls.session_token=fixture; Path=/; HttpOnly; Secure")
      |> Req.Test.json(%{"ok" => true})
    end)

    for {path, target} <- [
          {nil, "/"},
          {"/profiles?new=1", "/profiles?new=1"},
          {"//outside.test", "/"},
          {"https://outside.test", "/"}
        ] do
      params = if path, do: %{"return_to" => path}, else: %{}
      page = get(conn, "/login", params)
      assert html_response(page, 200) =~ ~s(value="#{target}")

      signed_in =
        page
        |> recycle()
        |> post(
          "/login",
          Map.merge(params, %{
            "email" => "test@example.test",
            "password" => "test-password",
            "_csrf_token" => Plug.CSRFProtection.get_csrf_token()
          })
        )

      assert redirected_to(signed_in) == target

      assert Enum.any?(
               get_resp_header(signed_in, "set-cookie"),
               &String.starts_with?(&1, "prls.session_token=fixture;")
             )
    end

    for method <- [:get, :post] do
      assert dispatch(conn, HardenLlmWeb.Endpoint, method, "/accounts") |> response(404)

      assert Phoenix.Router.route_info(
               HardenLlmWeb.Router,
               String.upcase(to_string(method)),
               "/accounts",
               "localhost"
             ) == :error
    end
  end

  # WEB-TEST-111
  for {change, redirect} <- [
        {:company, nil},
        {:user, "/"},
        {:revoked, "/login"},
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
           |> redirected_to() == "/login"
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
