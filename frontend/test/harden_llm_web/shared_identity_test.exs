defmodule HardenLlmWeb.SharedIdentityTest do
  use HardenLlmWeb.ConnCase, async: true

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001

  # WEB-TEST-104
  test "sign-in is rendered by the shared PRLS Web controller", %{conn: conn} do
    conn = get(conn, "/login")

    assert response(conn, 200) =~ "Sign in"
  end

  # WEB-TEST-105
  test "product pages redirect unauthenticated requests to shared sign-in", %{conn: conn} do
    conn = get(conn, "/")

    assert redirected_to(conn) == "/login?return_to=%2F"
  end

  # WEB-TEST-106
  test "product access is checked against the current Control Plane account", %{conn: conn} do
    conn =
      conn
      |> Plug.Conn.put_req_header("cookie", HardenLlmWeb.AccessFixtures.no_product_cookie())
      |> get("/profiles")

    assert redirected_to(conn) =~ "/accounts?"
    assert redirected_to(conn) =~ "required_product=harden-llm"
  end

  # WEB-TEST-107
  test "account selection uses the shared account controller", %{conn: conn} do
    conn =
      conn
      |> Plug.Conn.put_req_header("cookie", HardenLlmWeb.AccessFixtures.no_product_cookie())
      |> get("/accounts?required_product=harden-llm")

    body = response(conn, 200)
    assert body =~ "Harden Test Account"
    refute body =~ "Knowledge Test Account"
  end

  # WEB-TEST-108
  test "the product cookie remains encrypted and host-only", _context do
    options = HardenLlmWeb.SessionOptions.options()

    assert Keyword.fetch!(options, :key) == "__Host-harden_llm_web"
    assert Keyword.fetch!(options, :path) == "/"
    assert Keyword.fetch!(options, :secure)
    assert Keyword.fetch!(options, :http_only)
    assert Keyword.fetch!(options, :same_site) == "Lax"
    refute Keyword.has_key?(options, :domain)
  end
end
