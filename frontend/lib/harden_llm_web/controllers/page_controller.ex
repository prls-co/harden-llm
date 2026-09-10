defmodule HardenLlmWeb.PageController do
  use HardenLlmWeb, :controller

  # Retired audit-page bookmarks use the existing workspace trace selection.
  def history(conn, %{"trace_id" => trace_id}),
    do: redirect(conn, to: ~p"/workspace?trace_id=#{trace_id}")

  def history(conn, _params), do: redirect(conn, to: ~p"/workspace")

  def home(conn, _params) do
    if conn.assigns[:session_handle] do
      redirect(conn, to: ~p"/workspace")
    else
      redirect(conn, to: ~p"/login")
    end
  end
end
