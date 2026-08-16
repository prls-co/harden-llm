defmodule HardenLlmWeb.PageController do
  use HardenLlmWeb, :controller

  def home(conn, _params) do
    if conn.assigns[:session_handle] do
      redirect(conn, to: ~p"/workspace")
    else
      redirect(conn, to: ~p"/login")
    end
  end
end
