defmodule HardenLlmWeb.BrowserBackend do
  @moduledoc false

  import Plug.Conn

  alias HardenLlmWeb.APIFixtures

  def init(options), do: options

  def start, do: Agent.start(fn -> %{calls: [], responses: []} end, name: __MODULE__)

  def stop do
    if pid = Process.whereis(__MODULE__), do: Agent.stop(pid)
    :ok
  end

  def calls, do: Agent.get(__MODULE__, &Enum.reverse(&1.calls))
  def responses, do: Agent.get(__MODULE__, &Enum.reverse(&1.responses))

  def call(conn, _options) do
    record(conn)

    case {conn.method, conn.path_info} do
      {"GET", ["internal", "v1", "access-context"]} ->
        access_context(conn)

      {"POST", ["api", "auth", "sign-in", "email"]} ->
        sign_in(conn)

      {"POST", ["api", "auth", "sign-out"]} ->
        sign_out(conn)

      {"GET", ["v1", "models"]} ->
        authenticated(conn, fn conn -> json(conn, 200, APIFixtures.models()) end)

      {"POST", ["v1", "responses"]} ->
        response(conn)

      _ ->
        json(conn, 404, %{"error" => %{"message" => "fixture route not found"}})
    end
  end

  defp access_context(conn) do
    expected = "Bearer " <> Application.fetch_env!(:prls_web, :internal_token)

    if get_req_header(conn, "authorization") == [expected] do
      case HardenLlmWeb.AccessFixtures.resolve(
             {:cookie, Enum.join(get_req_header(conn, "cookie"), "; ")},
             []
           ) do
        {:ok, context} -> json(conn, 200, Map.from_struct(context))
        {:error, _} -> unauthorized(conn)
      end
    else
      unauthorized(conn)
    end
  end

  defp sign_in(conn) do
    with {:ok, body, conn} <- read_json(conn),
         true <-
           body["email"] == "browser@example.test" and
             body["password"] == "browser-password-123" do
      conn
      |> put_resp_header(
        "set-cookie",
        "prls.session_token=fixture; Path=/; HttpOnly; SameSite=Lax"
      )
      |> json(200, %{"ok" => true})
    else
      _ -> unauthorized(conn)
    end
  end

  defp sign_out(conn) do
    if has_fixture_session?(conn) do
      conn
      |> put_resp_header(
        "set-cookie",
        "prls.session_token=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax"
      )
      |> json(200, %{"ok" => true})
    else
      unauthorized(conn)
    end
  end

  defp response(conn) do
    with :ok <- authorize_api(conn),
         {:ok, body, conn} <- read_json(conn) do
      Agent.update(__MODULE__, &%{&1 | responses: [body | &1.responses]})
      json(conn, 200, APIFixtures.response("deterministic browser response"))
    else
      {:error, conn} -> unauthorized(conn)
    end
  end

  defp authenticated(conn, fun) do
    if authorize_api(conn) == :ok, do: fun.(conn), else: unauthorized(conn)
  end

  defp authorize_api(conn) do
    if get_req_header(conn, "authorization") == ["Bearer " <> APIFixtures.token()],
      do: :ok,
      else: {:error, conn}
  end

  defp has_fixture_session?(conn) do
    conn
    |> get_req_header("cookie")
    |> Enum.any?(fn header ->
      Enum.any?(String.split(header, ";"), &(String.trim(&1) == "prls.session_token=fixture"))
    end)
  end

  defp read_json(conn) do
    case read_body(conn) do
      {:ok, body, conn} ->
        case Jason.decode(body) do
          {:ok, decoded} when is_map(decoded) -> {:ok, decoded, conn}
          _ -> {:error, conn}
        end

      {:more, _body, conn} ->
        {:error, conn}

      {:error, _reason} ->
        {:error, conn}
    end
  end

  defp record(conn) do
    Agent.update(__MODULE__, &%{&1 | calls: [{conn.method, conn.request_path} | &1.calls]})
  end

  defp unauthorized(conn),
    do: json(conn, 401, %{"error" => %{"message" => "fixture request unauthorized"}})

  defp json(conn, status, body) do
    conn
    |> put_resp_content_type("application/json")
    |> send_resp(status, Jason.encode!(body))
  end
end
