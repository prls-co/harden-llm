defmodule HardenLlmWeb.BrowserBackend do
  @moduledoc false

  import Plug.Conn

  @token "browser-fixture-token-that-never-leaves-the-server"
  @expiry "2099-07-13T12:00:00Z"
  @artifact_origin "http://127.0.0.1:4003"

  def init(options), do: options

  def start do
    Agent.start(fn -> initial_state() end, name: __MODULE__)
  end

  def reset do
    Agent.update(__MODULE__, fn _state -> initial_state() end)
  end

  def fail_next_run do
    Agent.update(__MODULE__, &Map.put(&1, :fail_next_run, true))
  end

  def stop do
    if pid = Process.whereis(__MODULE__), do: Agent.stop(pid)
    :ok
  end

  def calls, do: Agent.get(__MODULE__, &Enum.reverse(&1.calls))

  def run_requests, do: Agent.get(__MODULE__, &Enum.reverse(&1.run_requests))

  def call(conn, _options) do
    with {:ok, body, conn} <- read_json(conn) do
      record_call(conn)

      if public_route?(conn) or authorized?(conn) do
        dispatch(conn, body)
      else
        error(conn, 401, "unauthorized")
      end
    else
      {:error, conn} -> error(conn, 400, "invalid_json")
    end
  end

  defp dispatch(%{method: "POST", path_info: ["api", "v1", "auth", "login"]} = conn, body) do
    if body["email"] == "browser@example.test" and body["password"] == "browser-password-123" do
      json(conn, 200, success(login_result()))
    else
      error(conn, 401, "invalid_credentials")
    end
  end

  defp dispatch(%{method: "GET", path_info: ["api", "v1", "auth", "session"]} = conn, _body) do
    json(conn, 200, success(principal()))
  end

  defp dispatch(%{method: "POST", path_info: ["api", "v1", "auth", "logout"]} = conn, _body) do
    json(conn, 200, success(%{"loggedOut" => true}))
  end

  defp dispatch(%{method: "GET", path_info: ["api", "v1", "state"]} = conn, _body) do
    state = Agent.get(__MODULE__, & &1.workspace)
    json(conn, 200, success(nil, state))
  end

  defp dispatch(%{method: "POST", path_info: ["api", "v1", "state"]} = conn, body) do
    Agent.update(__MODULE__, &Map.put(&1, :workspace, body))
    json(conn, 200, success(nil, body))
  end

  defp dispatch(%{method: "GET", path_info: ["api", "v1", "profiles"]} = conn, _body) do
    profiles = Agent.get(__MODULE__, &Map.values(&1.profiles))
    json(conn, 200, success(%{"profiles" => profiles}))
  end

  defp dispatch(
         %{method: "PUT", path_info: ["api", "v1", "profiles", "bundle"]} = conn,
         _body
       ) do
    profiles = Agent.get(__MODULE__, &Map.values(&1.profiles))
    json(conn, 200, success(%{"profiles" => profiles}))
  end

  defp dispatch(
         %{method: "GET", path_info: ["api", "v1", "profiles", "bundle"]} = conn,
         _body
       ) do
    profiles = Agent.get(__MODULE__, &Map.values(&1.profiles))
    json(conn, 200, success(%{"schemaVersion" => 1, "profiles" => profiles}))
  end

  defp dispatch(
         %{
           method: "POST",
           path_info: ["api", "v1", "profiles", profile_id, "models:refresh"]
         } = conn,
         _body
       ) do
    case Agent.get_and_update(__MODULE__, fn state ->
           case state.profiles[profile_id] do
             nil ->
               {nil, state}

             profile ->
               refreshed =
                 put_in(profile, ["profile", "models"], [
                   %{"id" => "model-browser", "label" => "Browser model"},
                   %{"id" => "model-refreshed", "label" => "Refreshed model"}
                 ])

               {refreshed, put_in(state, [:profiles, profile_id], refreshed)}
           end
         end) do
      nil -> error(conn, 404, "profile_not_found")
      profile -> json(conn, 200, success(profile))
    end
  end

  defp dispatch(
         %{method: "PUT", path_info: ["api", "v1", "profiles", profile_id]} = conn,
         body
       ) do
    profile = profile_state(profile_id, body)
    Agent.update(__MODULE__, &put_in(&1, [:profiles, profile_id], profile))
    json(conn, 200, success(profile))
  end

  defp dispatch(
         %{method: "DELETE", path_info: ["api", "v1", "profiles", profile_id]} = conn,
         _body
       ) do
    Agent.update(
      __MODULE__,
      &update_in(&1.profiles, fn profiles -> Map.delete(profiles, profile_id) end)
    )

    json(conn, 200, success(%{"deleted" => true, "profileId" => profile_id}))
  end

  defp dispatch(%{method: "GET", path_info: ["api", "v1", "history"]} = conn, _body) do
    history = Agent.get(__MODULE__, & &1.history)
    json(conn, 200, success(%{"items" => history, "nextCursor" => nil}))
  end

  defp dispatch(%{method: "DELETE", path_info: ["api", "v1", "history"]} = conn, _body) do
    count = Agent.get(__MODULE__, &length(&1.history))
    Agent.update(__MODULE__, &%{&1 | history: []})
    json(conn, 200, success(%{"deletedCount" => count}))
  end

  defp dispatch(
         %{method: "DELETE", path_info: ["api", "v1", "history", run_id]} = conn,
         _body
       ) do
    Agent.update(
      __MODULE__,
      &%{&1 | history: Enum.reject(&1.history, fn item -> item["runId"] == run_id end)}
    )

    json(conn, 200, success(%{"deleted" => true, "runId" => run_id}))
  end

  defp dispatch(%{method: "POST", path_info: ["api", "v1", "run"]} = conn, body) do
    case Agent.get_and_update(__MODULE__, fn state ->
           state = %{state | run_requests: [body | state.run_requests]}

           if state.fail_next_run do
             {:failure, %{state | fail_next_run: false}}
           else
             {result, cache_entries} = run_result(body, state.cache_entries)
             {{:success, result}, %{state | cache_entries: cache_entries}}
           end
         end) do
      :failure ->
        error(conn, 503, "upstream_timeout")

      {:success, result} ->
        item = history_item(body, result)
        trace = trace(body, result)

        Agent.update(__MODULE__, fn state ->
          %{
            state
            | history: [item | Enum.reject(state.history, &(&1["runId"] == result["runId"]))],
              traces: Map.put(state.traces, result["traceId"], trace),
              workspace:
                state.workspace
                |> Map.put("lastRunId", result["runId"])
                |> Map.put("lastTraceId", result["traceId"])
          }
        end)

        json(
          conn,
          200,
          success(result, %{"lastRunId" => result["runId"], "lastTraceId" => result["traceId"]})
        )
    end
  end

  defp dispatch(
         %{method: "GET", path_info: ["api", "v1", "traces", trace_id]} = conn,
         _body
       ) do
    case Agent.get(__MODULE__, & &1.traces[trace_id]) do
      nil -> error(conn, 404, "trace_not_found")
      trace -> json(conn, 200, success(trace))
    end
  end

  defp dispatch(
         %{
           method: "GET",
           path_info: ["api", "v1", "traces", "trace-browser", "artifacts", "artifact-browser"]
         } = conn,
         _body
       ) do
    conn
    |> put_resp_header("location", @artifact_origin <> "/download/browser-artifact")
    |> send_resp(303, "")
  end

  defp dispatch(conn, _body), do: error(conn, 404, "fixture_route_not_found")

  defp initial_state do
    %{
      workspace: %{
        "schemaVersion" => 1,
        "selectedProfileId" => "",
        "modelId" => "",
        "systemPrompt" => "",
        "userPrompt" => "",
        "callType" => "text",
        "structuredRepair" => false,
        "cacheMode" => "off"
      },
      profiles: %{},
      history: [],
      traces: %{},
      fail_next_run: false,
      calls: [],
      run_requests: [],
      cache_entries: %{}
    }
  end

  defp profile_state(profile_id, body) do
    profile =
      body
      |> Map.get("profile", %{})
      |> Map.put("llmProfile", profile_id)
      |> Map.put("models", [%{"id" => "model-browser", "label" => "Browser model"}])

    %{
      "profile" => profile,
      "credential" => %{
        "schemaVersion" => 1,
        "credentialId" => body["credentialId"],
        "scope" => "user",
        "origin" => origin(profile["baseUrl"]),
        "apiInferenceTypes" => [profile["apiInferenceType"]],
        "configured" => is_binary(get_in(body, ["credential", "apiKey"])),
        "createdAt" => "2026-07-13T12:00:00Z"
      }
    }
  end

  defp run_result(request, cache_entries) do
    result = %{
      "runId" => "run-browser",
      "callId" => "call-browser",
      "traceId" => "trace-browser",
      "output" => "deterministic browser output",
      "usage" => %{
        "inputTokens" => 4,
        "cacheReadTokens" => 0,
        "cacheCreationTokens" => 0,
        "outputTokens" => 3,
        "reasoningTokens" => 0,
        "totalTokens" => 7
      },
      "cost" => %{"totalUsd" => 0.0, "known" => true, "source" => "fixture"},
      "attempts" => [%{"profileId" => "BrowserProfile", "outcome" => "success"}],
      "artifacts" => []
    }

    {cache, next_cache_entries} = cache_facts(request, cache_entries)
    {Map.put(result, "cache", cache), next_cache_entries}
  end

  defp cache_facts(request, cache_entries) do
    mode = request["cacheMode"] || "off"
    key = Map.delete(request, "cacheMode")

    cond do
      mode == "cache" and Map.has_key?(cache_entries, key) ->
        {%{"mode" => "cache", "status" => "hit", "served" => true, "written" => false},
         cache_entries}

      mode == "cache" ->
        {%{"mode" => "cache", "status" => "miss", "served" => false, "written" => true},
         Map.put(cache_entries, key, true)}

      mode == "refresh" ->
        {%{"mode" => "refresh", "status" => "refresh", "served" => false, "written" => true},
         Map.put(cache_entries, key, true)}

      true ->
        {%{"mode" => "off", "status" => "disabled", "served" => false, "written" => false},
         cache_entries}
    end
  end

  defp history_item(request, result) do
    %{
      "runId" => result["runId"],
      "profileId" => request["profileId"],
      "traceId" => result["traceId"],
      "status" => "succeeded",
      "request" => request,
      "result" => %{"output" => result["output"]},
      "startedAt" => "2026-07-13T12:00:00Z",
      "completedAt" => "2026-07-13T12:00:01Z"
    }
  end

  defp trace(request, result) do
    %{
      "traceId" => result["traceId"],
      "record" => %{"status" => "succeeded"},
      "observations" => [
        %{
          "sequence" => 0,
          "type" => "result",
          "data" => %{"outcome" => "success", "output" => result["output"]},
          "createdAt" => "2026-07-13T12:00:01Z"
        }
      ],
      "artifacts" => [
        %{
          "artifactId" => "artifact-browser",
          "kind" => "trace",
          "sha256" => String.duplicate("a", 64),
          "sizeBytes" => 42,
          "contentType" => "application/json",
          "createdAt" => "2026-07-13T12:00:01Z"
        }
      ],
      "resources" => %{
        "request" => %{"available" => true, "payload" => request},
        "response" => %{"available" => true, "payload" => result}
      }
    }
  end

  defp login_result do
    %{"accessToken" => @token, "expiresAt" => @expiry, "principal" => principal()}
  end

  defp principal do
    %{
      "ownerId" => "owner-browser",
      "email" => "browser@example.test",
      "sessionId" => "session-browser",
      "expiresAt" => @expiry
    }
  end

  defp public_route?(%{method: "POST", path_info: ["api", "v1", "auth", "login"]}), do: true
  defp public_route?(_conn), do: false

  defp authorized?(conn), do: get_req_header(conn, "authorization") == ["Bearer " <> @token]

  defp record_call(conn) do
    Agent.update(__MODULE__, fn state ->
      %{state | calls: [{conn.method, conn.request_path} | state.calls]}
    end)
  end

  defp read_json(conn) do
    case read_body(conn) do
      {:ok, "", conn} -> {:ok, %{}, conn}
      {:ok, body, conn} -> decode_json(body, conn)
      {:more, _body, conn} -> {:error, conn}
      {:error, _reason} -> {:error, conn}
    end
  end

  defp decode_json(body, conn) do
    case Jason.decode(body) do
      {:ok, decoded} when is_map(decoded) -> {:ok, decoded, conn}
      _ -> {:error, conn}
    end
  end

  defp json(conn, status, body) do
    conn
    |> put_resp_content_type("application/json")
    |> send_resp(status, Jason.encode!(body))
  end

  defp success(result, state \\ %{}), do: %{"state" => state, "result" => result, "error" => nil}

  defp error(conn, status, code) do
    json(conn, status, %{
      "state" => %{},
      "result" => nil,
      "error" => %{"code" => code, "message" => "deterministic fixture error"}
    })
  end

  defp origin(url) do
    case URI.parse(url || "") do
      %URI{scheme: scheme, host: host} when is_binary(scheme) and is_binary(host) ->
        "#{scheme}://#{host}"

      _ ->
        ""
    end
  end
end

defmodule HardenLlmWeb.BrowserArtifactServer do
  @moduledoc false

  import Plug.Conn

  def init(options), do: options

  def call(%{method: "GET", path_info: ["download", "browser-artifact"]} = conn, _options) do
    conn
    |> put_resp_content_type("application/json")
    |> put_resp_header("cache-control", "no-store")
    |> send_resp(200, Jason.encode!(%{"artifact" => "browser-fixture"}))
  end

  def call(conn, _options), do: send_resp(conn, 404, "not found")
end
