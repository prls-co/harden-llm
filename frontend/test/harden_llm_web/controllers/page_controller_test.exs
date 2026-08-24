defmodule HardenLlmWeb.PageControllerTest do
  use HardenLlmWeb.ConnCase, async: true

  test "GET /", %{conn: conn} do
    conn = get(conn, ~p"/")
    assert redirected_to(conn) == ~p"/login"
  end
end
