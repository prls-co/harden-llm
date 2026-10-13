defmodule HardenLlmWeb.ArtifactController do
  use HardenLlmWeb, :controller
  alias HardenLlmUI.{APIError, Client}

  def show(conn, %{"run_id" => run_id, "artifact_id" => artifact_id}) do
    deliver(conn, Client.get_artifact(HardenLlmWeb.APIContext.server(), run_id, artifact_id))
  end

  def trace(conn, %{"run_id" => run_id}) do
    deliver(conn, Client.download_trace(HardenLlmWeb.APIContext.server(), run_id))
  end

  defp deliver(conn, result) do
    case result do
      {:ok, artifact} ->
        conn
        |> put_resp_header("cache-control", "no-store")
        |> put_resp_header("referrer-policy", "no-referrer")
        |> put_resp_header("content-disposition", artifact.disposition)
        |> put_resp_content_type(artifact.content_type)
        |> send_resp(200, artifact.body)

      {:error, %APIError{status: status}} ->
        conn
        |> put_status(if(status in [401, 403, 404], do: status, else: 502))
        |> text("Artifact download is temporarily unavailable.")
    end
  end
end
