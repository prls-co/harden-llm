defmodule HardenLlmWeb.RootRouteTest do
  use HardenLlmWeb.ConnCase, async: true

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-005

  test "authenticated login links return directly to root", %{conn: conn} do
    conn = conn |> authenticated_conn() |> get(~p"/login")
    assert redirected_to(conn) == ~p"/"
  end

  test "GET /", %{conn: conn} do
    conn = get(conn, ~p"/")
    assert redirected_to(conn) == ~p"/login"
  end
end
