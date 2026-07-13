defmodule HardenLlmWeb.PageControllerTest do
  use HardenLlmWeb.ConnCase

  test "GET /", %{conn: conn} do
    conn = get(conn, ~p"/")
    assert redirected_to(conn) == ~p"/login"
  end
end
