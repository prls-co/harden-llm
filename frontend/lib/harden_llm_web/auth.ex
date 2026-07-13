defmodule HardenLlmWeb.Auth do
  @moduledoc false

  import Plug.Conn

  alias HardenLlmWeb.{APIError, HardenAPI, SessionVault}

  def init(action), do: action
  def call(conn, action), do: apply(__MODULE__, action, [conn, []])

  def fetch_session_handle(conn, _options) do
    handle = get_session(conn, "session_handle")

    case SessionVault.lookup(handle) do
      {:ok, _token, _expiry_ms} -> assign(conn, :session_handle, handle)
      :error -> assign(conn, :session_handle, nil)
    end
  end

  def require_authenticated(%{assigns: %{session_handle: handle}} = conn, _options)
      when is_binary(handle),
      do: conn

  def require_authenticated(conn, _options) do
    conn
    |> Phoenix.Controller.put_flash(:error, "Please sign in to continue.")
    |> Phoenix.Controller.redirect(to: "/login")
    |> halt()
  end

  def redirect_if_authenticated(%{assigns: %{session_handle: handle}} = conn, _options)
      when is_binary(handle) do
    conn |> Phoenix.Controller.redirect(to: "/workspace") |> halt()
  end

  def redirect_if_authenticated(conn, _options), do: conn

  def on_mount(:require_authenticated, _params, session, socket) do
    with {:ok, handle, identity} <- session_shape(session),
         {:ok, _token, _expiry_ms} <- SessionVault.lookup(handle),
         {:ok, principal, _state} <- HardenAPI.get_session(handle) do
      {:cont,
       socket
       |> Phoenix.Component.assign(:session_handle, handle)
       |> Phoenix.Component.assign(:current_identity, safe_identity(identity, principal))
       |> Phoenix.Component.assign(:current_scope, %{authenticated: true})}
    else
      {:error, %APIError{}} -> expired(socket)
      :error -> expired(socket)
      _ -> expired(socket)
    end
  end

  defp expired(socket) do
    {:halt,
     socket
     |> Phoenix.LiveView.put_flash(:error, "Your session has expired. Please sign in again.")
     |> Phoenix.LiveView.redirect(to: "/session/expired")}
  end

  defp session_shape(%{
         "session_handle" => handle,
         "session_expiry" => expiry,
         "identity" => identity
       })
       when is_binary(handle) and is_binary(expiry) and is_map(identity),
       do: {:ok, handle, identity}

  defp session_shape(_session), do: :error

  defp safe_identity(identity, principal) do
    %{
      "email" => safe_text(principal["email"] || identity["email"], 320),
      "ownerId" => safe_text(principal["ownerId"] || identity["ownerId"], 128)
    }
  end

  defp safe_text(value, limit) when is_binary(value), do: String.slice(value, 0, limit)
  defp safe_text(_value, _limit), do: ""
end
