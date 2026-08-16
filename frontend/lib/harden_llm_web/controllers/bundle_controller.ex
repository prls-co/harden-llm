defmodule HardenLlmWeb.BundleController do
  use HardenLlmWeb, :controller

  alias HardenLlmWeb.{APIError, HardenAPI}

  def show(conn, _params) do
    case HardenAPI.export_profile_bundle(conn.assigns.session_handle) do
      {:ok, bundle, _state} ->
        send_download(conn, {:binary, Jason.encode!(bundle)},
          filename: "harden-llm-profile-bundle.json",
          content_type: "application/json",
          disposition: :attachment
        )

      {:error, %APIError{status: 401}} ->
        redirect(conn, to: ~p"/session/expired")

      {:error, %APIError{}} ->
        conn |> put_status(:bad_gateway) |> text("Profile bundle is temporarily unavailable.")
    end
  end
end
