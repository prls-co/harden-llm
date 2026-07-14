defmodule HardenLlmWeb.ArtifactController do
  use HardenLlmWeb, :controller

  alias HardenLlmWeb.{APIError, HardenAPI}

  def show(conn, %{"trace_id" => trace_id, "artifact_id" => artifact_id}) do
    with {:ok, %{location: location}, _state} <-
           HardenAPI.get_artifact(conn.assigns.session_handle, trace_id, artifact_id),
         true <- exact_origin?(location) do
      conn
      |> put_resp_header("location", location)
      |> put_resp_header("cache-control", "no-store")
      |> put_resp_header("referrer-policy", "no-referrer")
      |> send_resp(303, "")
    else
      {:error, %APIError{status: 401}} ->
        redirect(conn, to: ~p"/session/expired")

      _ ->
        conn |> put_status(:bad_gateway) |> text("Artifact download is temporarily unavailable.")
    end
  end

  def exact_origin?(location) when is_binary(location) do
    expected = URI.parse(Application.fetch_env!(:harden_llm, :artifact_public_origin))
    candidate = URI.parse(location)

    candidate.scheme in ["http", "https"] and is_binary(candidate.host) and
      origin(candidate) == origin(expected) and is_nil(candidate.userinfo) and
      is_nil(candidate.fragment)
  end

  def exact_origin?(_location), do: false

  defp origin(uri),
    do: {String.downcase(uri.scheme || ""), String.downcase(uri.host || ""), normalized_port(uri)}

  defp normalized_port(%URI{port: port}) when is_integer(port), do: port
  defp normalized_port(%URI{scheme: "https"}), do: 443
  defp normalized_port(%URI{scheme: "http"}), do: 80
  defp normalized_port(_uri), do: nil
end
