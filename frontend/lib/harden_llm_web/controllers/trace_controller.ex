defmodule HardenLlmWeb.TraceController do
  use HardenLlmWeb, :controller

  alias HardenLlmWeb.{APIError, HardenAPI}

  @doc "Returns the authenticated, redacted trace JSON for the reusable trace widget."
  def show(conn, %{"trace_id" => trace_id}) do
    case HardenAPI.get_trace(conn.assigns.session_handle, trace_id) do
      {:ok, trace, _state} ->
        conn
        |> put_resp_header("cache-control", "no-store")
        |> put_resp_header("referrer-policy", "no-referrer")
        |> json(trace)

      {:error, %APIError{status: 401}} ->
        redirect(conn, to: ~p"/session/expired")

      {:error, %APIError{status: 404}} ->
        conn
        |> put_status(:not_found)
        |> json(%{error: "Trace not found."})

      {:error, %APIError{}} ->
        conn
        |> put_status(:bad_gateway)
        |> json(%{error: "Trace JSON is temporarily unavailable."})
    end
  end
end
