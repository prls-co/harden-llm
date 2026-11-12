defmodule HardenLlmWeb.BrowserBackend do
  @moduledoc false

  import Plug.Conn

  alias HardenLlmWeb.APIFixtures

  def init(options), do: options

  def start do
    workspace =
      __DIR__
      |> Path.join("../../../test/fixtures/reference/workspace.json")
      |> File.read!()
      |> Jason.decode!()

    Agent.start(fn -> %{calls: [], responses: [], drafts: %{}, workspace: workspace} end)
  end

  def stop(owner) do
    if Process.alive?(owner), do: Agent.stop(owner)
    :ok
  end

  def calls(owner), do: Agent.get(owner, &Enum.reverse(&1.calls))
  def responses(owner), do: Agent.get(owner, &Enum.reverse(&1.responses))

  def call(conn, options) do
    owner = Keyword.fetch!(options, :owner)
    record(conn, owner)

    case {conn.method, conn.path_info} do
      {"GET", ["internal", "v1", "access-context"]} ->
        access_context(conn)

      {"POST", ["api", "auth", "sign-in", "email"]} ->
        sign_in(conn)

      {"POST", ["api", "auth", "sign-out"]} ->
        sign_out(conn)

      {"GET", ["api", "v1", "profiles"]} ->
        reference(conn, owner, "profiles")

      {"GET", ["api", "v1", "profiles", id]} ->
        authenticated(conn, fn conn ->
          json(conn, 200, Agent.get(owner, &Map.fetch!(&1.workspace["details"], id)))
        end)

      {"GET", ["api", "v1", "status"]} ->
        reference(conn, owner, "status")

      {"GET", ["api", "v1", "state"]} ->
        authenticated(conn, fn conn ->
          conn = fetch_query_params(conn)
          key = conn.query_params["draftKey"]
          state = Agent.get(owner, &Map.get(&1.drafts, key, %{"revision" => 0, "payload" => %{}}))
          json(conn, 200, Map.put(state, "draftKey", key))
        end)

      {"POST", ["api", "v1", "state"]} ->
        authenticated(conn, fn conn ->
          {:ok, body, conn} = read_json(conn)

          saved =
            Map.take(body, ~w(draftKey payload)) |> Map.put("revision", body["revision"] + 1)

          Agent.update(owner, &put_in(&1, [:drafts, body["draftKey"]], saved))
          json(conn, 200, saved)
        end)

      {"GET", ["api", "v1", "history"]} ->
        authenticated(conn, fn conn ->
          json(conn, 503, %{
            "error" => %{"code" => "history_unavailable", "message" => "History unavailable."}
          })
        end)

      {"POST", ["api", "v1", "run"]} ->
        response(conn, owner)

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

  defp reference(conn, owner, key) do
    authenticated(conn, fn conn -> json(conn, 200, Agent.get(owner, & &1.workspace[key])) end)
  end

  defp response(conn, owner) do
    with :ok <- authorize_api(conn),
         {:ok, body, conn} <- read_json(conn) do
      Agent.update(owner, &%{&1 | responses: [body | &1.responses]})

      events =
        Agent.get(owner, & &1.workspace["stream"])
        |> List.update_at(3, &put_in(&1, ["data", "status"], "not_saved"))

      body =
        Enum.map_join(events, fn event ->
          "event: " <> event["type"] <> "\ndata: " <> Jason.encode!(event) <> "\n\n"
        end)

      conn
      |> put_resp_content_type("text/event-stream")
      |> send_resp(200, body)
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

  defp record(conn, owner) do
    Agent.update(owner, &%{&1 | calls: [{conn.method, conn.request_path} | &1.calls]})
  end

  defp unauthorized(conn),
    do: json(conn, 401, %{"error" => %{"message" => "fixture request unauthorized"}})

  defp json(conn, status, body) do
    conn
    |> put_resp_content_type("application/json")
    |> send_resp(status, Jason.encode!(body))
  end
end
