defmodule HardenLlmWeb.HealthController do
  use HardenLlmWeb, :controller

  def show(conn, _params), do: json(conn, %{status: "ok"})
end
