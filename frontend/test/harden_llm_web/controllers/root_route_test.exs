defmodule HardenLlmWeb.RootRouteTest do
  use HardenLlmWeb.ConnCase, async: true

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-005

  test "GET /", %{conn: conn} do
    conn = get(conn, ~p"/")
    assert redirected_to(conn) == PrlsWeb.SessionNavigation.login_url("/")
  end
end
