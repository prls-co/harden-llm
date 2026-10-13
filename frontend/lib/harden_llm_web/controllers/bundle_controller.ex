defmodule HardenLlmWeb.BundleController do
  use HardenLlmWeb, :controller
  alias HardenLlmUI.{APIError, Client}

  def show(conn, _params) do
    case Client.export_profile_bundle(HardenLlmWeb.APIContext.server()) do
      {:ok, bundle} ->
        conn
        |> put_resp_header("cache-control", "no-store")
        |> send_download({:binary, Jason.encode!(bundle)},
          filename: "harden-llm-profile-bundle.json",
          content_type: "application/json",
          disposition: :attachment
        )

      {:error, %APIError{status: status}} ->
        conn
        |> put_status(if(status in [401, 403], do: status, else: 502))
        |> text("Profile bundle is temporarily unavailable.")
    end
  end
end
