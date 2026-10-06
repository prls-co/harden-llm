defmodule HardenLlmWeb.HistoryController do
  use HardenLlmWeb, :controller

  alias HardenLlm.Reference
  alias HardenLlm.Reference.Call

  def download(conn, %{"id" => id}) do
    case Reference.get_call(id) do
      %Call{} = call ->
        filename = "harden-llm-call-#{call.id}.json"

        conn
        |> put_resp_content_type("application/json")
        |> put_resp_header("content-disposition", "attachment; filename=\"#{filename}\"")
        |> send_resp(200, Jason.encode!(Reference.download(call), pretty: true))

      _ ->
        conn
        |> put_resp_content_type("application/json")
        |> send_resp(404, Jason.encode!(%{"error" => "History entry not found."}))
    end
  end
end
