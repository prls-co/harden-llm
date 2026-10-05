defmodule HardenLlmWeb.BrowserPortal do
  @moduledoc "Test-only Portal host using the production shared controllers and styles."
  use Plug.Router

  plug Plug.Static, at: "/", from: :harden_llm, only: ["assets"]
  plug :match
  plug Plug.Parsers, parsers: [:urlencoded], pass: ["*/*"]
  plug :secret
  plug Plug.Session, store: :cookie, key: "test_portal", signing_salt: "test-portal"
  plug :fetch_session
  plug Plug.CSRFProtection
  plug :dispatch

  get("/login", do: PrlsWeb.AuthController.new(conn, conn.params))
  post("/login", do: PrlsWeb.AuthController.create(conn, conn.params))
  match(_, do: send_resp(conn, 404, "Not found"))

  defp secret(conn, _options),
    do: %{conn | secret_key_base: String.duplicate("test-portal-secret-", 5)}
end
