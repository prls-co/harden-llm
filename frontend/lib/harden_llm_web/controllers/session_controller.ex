defmodule HardenLlmWeb.SessionController do
  use HardenLlmWeb, :controller

  alias HardenLlmWeb.{APIError, HardenAPI, SessionVault}

  def new(conn, _params) do
    render(conn, :new, form: Phoenix.Component.to_form(%{"email" => ""}, as: :session))
  end

  def create(conn, %{"session" => %{"email" => email, "password" => password}}) do
    prior_handle = get_session(conn, "session_handle")

    case HardenAPI.login(email, password) do
      {:ok,
       %{
         "accessToken" => token,
         "expiresAt" => expires_at,
         "principal" => principal
       }, _state} ->
        :ok = SessionVault.revoke(prior_handle)

        with {:ok, handle} <- SessionVault.insert(token, expires_at) do
          conn
          |> configure_session(renew: true)
          |> put_session("session_handle", handle)
          |> put_session("session_expiry", expires_at)
          |> put_session("identity", safe_identity(principal))
          |> redirect(to: "/workspace")
        else
          _ -> unavailable(conn)
        end

      {:error, %APIError{category: category}} when category in [:unauthorized, :request] ->
        conn
        |> put_flash(:error, "Email or password was not accepted.")
        |> redirect(to: "/login")

      {:error, %APIError{}} ->
        unavailable(conn)

      {:ok, _malformed_result, _state} ->
        unavailable(conn)
    end
  end

  def create(conn, _params) do
    conn
    |> put_flash(:error, "Enter an email address and password.")
    |> redirect(to: "/login")
  end

  def delete(conn, _params) do
    handle = get_session(conn, "session_handle")
    _ = HardenAPI.logout(handle)
    :ok = SessionVault.revoke(handle)

    conn
    |> clear_session()
    |> configure_session(drop: true)
    |> put_flash(:info, "You have been signed out.")
    |> redirect(to: "/login")
  end

  def expired(conn, _params) do
    handle = get_session(conn, "session_handle")
    :ok = SessionVault.revoke(handle)

    conn
    |> clear_session()
    |> configure_session(drop: true)
    |> put_flash(:error, "Your session has expired. Please sign in again.")
    |> redirect(to: "/login")
  end

  defp unavailable(conn) do
    conn
    |> put_flash(:error, "The Harden-LLM service is temporarily unavailable.")
    |> redirect(to: "/login")
  end

  defp safe_identity(principal) when is_map(principal) do
    %{
      "email" => safe_text(principal["email"], 320),
      "ownerId" => safe_text(principal["ownerId"], 128)
    }
  end

  defp safe_identity(_principal), do: %{"email" => "", "ownerId" => ""}

  defp safe_text(value, limit) when is_binary(value), do: String.slice(value, 0, limit)
  defp safe_text(_value, _limit), do: ""
end
