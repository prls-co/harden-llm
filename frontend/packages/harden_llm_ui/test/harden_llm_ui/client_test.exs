defmodule HardenLlmUI.IndependentClientTest do
  use ExUnit.Case, async: true
  alias HardenLlmUI.{APIError, Client, DiagnosticsWire}
  alias HardenLlmUI.Client.Context

  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-446
  @key_a "harden-llm-test-token-0123456789abcdef"
  @key_b "harden-llm-other-token-0123456789abcdef"

  setup context do
    Req.Test.set_req_test_from_context(context)
    Req.Test.verify_on_exit!(context)
    path = Path.expand("../../../../../test/fixtures/reference/workspace.json", __DIR__)
    {:ok, fixture: File.read!(path) |> Jason.decode!()}
  end

  test "two hosts share one key while each request carries its explicit context", %{fixture: f} do
    parent = self()

    Req.Test.stub(Client, fn conn ->
      headers = Plug.Conn.get_req_header(conn, "authorization")
      send(parent, {:request, conn.host, conn.request_path, headers})
      assert Plug.Conn.get_req_header(conn, "cookie") == []
      assert Plug.Conn.get_req_header(conn, "x-prls-session-reference") == []
      assert conn.query_string == ""
      body = if headers == ["Bearer " <> @key_a], do: f["profiles"], else: %{"profiles" => []}
      Req.Test.json(conn, body)
    end)

    a = context("https://host-a.invalid", @key_a)
    same_key = context("https://host-b.invalid", @key_a)
    b = context("https://host-a.invalid", @key_b)
    assert Client.list_profiles(a) == {:ok, f["profiles"]}
    assert Client.list_profiles(same_key) == {:ok, f["profiles"]}
    assert Client.list_profiles(b) == {:ok, %{"profiles" => []}}
    assert_received {:request, "host-a.invalid", "/api/v1/profiles", ["Bearer " <> @key_a]}
    assert_received {:request, "host-b.invalid", "/api/v1/profiles", ["Bearer " <> @key_a]}
    assert_received {:request, "host-a.invalid", "/api/v1/profiles", ["Bearer " <> @key_b]}
  end

  test "complete immutable profile detail and history preserve the canonical payload", %{
    fixture: f
  } do
    Req.Test.stub(Client, fn conn ->
      case conn.request_path do
        "/api/v1/profiles/CPA%20Responses" -> Req.Test.json(conn, f["details"]["CPA Responses"])
        "/api/v1/profiles/CPA Responses" -> Req.Test.json(conn, f["details"]["CPA Responses"])
        "/api/v1/history/" <> _id -> Req.Test.json(conn, f["record"])
      end
    end)

    ctx = context("https://host-a.invalid", @key_a)
    assert Client.get_profile(ctx, "CPA Responses") == {:ok, f["details"]["CPA Responses"]}
    assert Client.get_history(ctx, f["record"]["runId"]) == {:ok, f["record"]}
  end

  test "one canonical decoder validates every consumed field and rejects corruption", %{
    fixture: f
  } do
    for {schema, value} <- [
          {"ProfilesEnvelope", f["profiles"]},
          {"ProfileEnvelope", f["savedProfile"]},
          {"ProfileDraft", f["details"]["CPA Responses"]},
          {"HistoryRecord", f["record"]},
          {"HistoryPage", f["history"]},
          {"Statistics", f["statistics"]},
          {"RunEnvelope", f["run"]}
        ] do
      assert DiagnosticsWire.valid?(schema, value), schema
    end

    for id <- ["CPA Responses", "Λ model 🚀"] do
      assert DiagnosticsWire.valid?("ReferenceProfileID", id)
    end

    for id <- ["bad/name", "bad" <> <<0>>, "bad" <> <<159::utf8>>] do
      refute DiagnosticsWire.valid?("ReferenceProfileID", id)
    end

    bad = [
      {"HistoryPage", put_in(f["history"], ["pagination", "totalCount"], "many")},
      {"HistoryRecord",
       put_in(f["record"], ["result", "accounting", "provider", "usage", "inputTokens"], "bad")},
      {"ProfileDraft", put_in(f["details"]["CPA Responses"], ["definition", "pricing"], "bad")},
      {"ProfileEnvelope", put_in(f["savedProfile"], ["profile", "createdAt"], 42)},
      {"HistoryRecord", Map.put(f["record"], "runId", "not-a-uuid")},
      {"RunEnvelope", put_in(f["run"], ["result", "cache", "served"], "bad")},
      {"ProfilesEnvelope", Map.put(f["profiles"], "apiKey", @key_a)}
    ]

    for {schema, value} <- bad, do: refute(DiagnosticsWire.valid?(schema, value), schema)
  end

  test "malformed success stays a safe protocol error and is never retried", %{fixture: f} do
    parent = self()

    Req.Test.stub(Client, fn conn ->
      send(parent, :request)
      Req.Test.json(conn, put_in(f["profiles"], ["profiles"], [%{"id" => "bad"}]))
    end)

    assert {:error, %APIError{category: :protocol} = error} =
             Client.list_profiles(context("https://host-a.invalid", @key_a))

    refute inspect(error) =~ @key_a
    assert_receive :request
    refute_receive :request, 50
  end

  test "server context rejects invalid origins, keys and timeout bounds" do
    for options <- [
          [base_url: "https://user:password@host.invalid", api_key: @key_a],
          [base_url: "https://host.invalid/path", api_key: @key_a],
          [base_url: "https://host.invalid", api_key: "short"],
          [base_url: "https://host.invalid", api_key: @key_a, timeout_ms: 0],
          [base_url: "https://host.invalid", api_key: @key_a, timeout_ms: 120_001]
        ] do
      assert_raise ArgumentError, fn -> Context.new(options) end
    end
  end

  test "reference errors preserve bounded debugging fields without raw server messages", %{
    fixture: f
  } do
    diag = %{
      "code" => "history_read_failed",
      "component" => "history",
      "operation" => "list",
      "requestId" => f["record"]["runId"],
      "runId" => f["record"]["runId"],
      "inferenceOutcome" => "not_started",
      "persistenceOutcome" => "unavailable",
      "cause" => "connection",
      "durationMs" => 500,
      "message" => "History is unavailable.",
      "action" => "Inspect correlated storage diagnostics."
    }

    Req.Test.stub(Client, fn conn ->
      conn
      |> Plug.Conn.put_status(503)
      |> Req.Test.json(%{
        "error" => %{
          "code" => "history_read_failed",
          "message" => @key_a,
          "diagnostic" => diag,
          "currentRevision" => 7,
          "fieldErrors" => [
            %{"field" => "profileId", "message" => "Select a configured profile."}
          ]
        }
      })
    end)

    assert {:error, %APIError{} = error} =
             Client.list_history(context("https://host-a.invalid", @key_a))

    assert Map.get(error, :diagnostic) == diag
    assert Map.get(error, :current_revision) == 7
    assert error.field_errors == %{"profileId" => "Select a configured profile."}
    refute inspect(error) =~ @key_a
  end

  test "standalone SSE returns one terminal result then recording and never repeats work", %{
    fixture: f
  } do
    parent = self()

    Req.Test.stub(Client, fn conn ->
      send(parent, :inference_request)
      assert Plug.Conn.get_req_header(conn, "authorization") == ["Bearer " <> @key_a]

      body =
        Enum.map_join(f["stream"], "", fn event ->
          "event: " <> event["type"] <> "\ndata: " <> Jason.encode!(event) <> "\n\n"
        end)

      conn |> Plug.Conn.put_resp_content_type("text/event-stream") |> Plug.Conn.resp(200, body)
    end)

    assert {:ok, %{"result" => result}} =
             Client.run_stream(
               context("https://host-a.invalid", @key_a),
               f["record"]["request"]["input"],
               fn event ->
                 send(parent, {:event, event["type"]})
                 :cont
               end
             )

    assert result == f["run"]["result"]
    assert_receive {:event, "run.progress"}
    assert_receive {:event, "run.progress"}
    assert_receive {:event, "run.completed"}
    assert_receive {:event, "history.recorded"}
    assert_receive :inference_request
    refute_receive :inference_request, 50
  end

  defp context(origin, key) do
    Context.new(
      base_url: origin,
      api_key: key,
      draft_key: "host-draft",
      request_options: [plug: {Req.Test, Client}]
    )
  end

  # TEST-457: validation needs a useful, correlated message without reflecting
  # the raw server message or request/credential material.
  test "profile validation identifies the stage field and run in the visible message", %{
    fixture: f
  } do
    run_id = f["record"]["runId"]
    field = "recoveryPolicy.jsonRepair.escalation.reasoningEffort"

    diagnostic = %{
      "code" => "profile_invalid",
      "component" => "reference",
      "operation" => "run",
      "requestId" => run_id,
      "runId" => run_id,
      "inferenceOutcome" => "failed",
      "persistenceOutcome" => "saved",
      "cause" => "rejected",
      "durationMs" => 0,
      "message" => "Reference settings are invalid.",
      "action" => "Correct the indicated fields."
    }

    Req.Test.stub(Client, fn conn ->
      conn
      |> Plug.Conn.put_status(400)
      |> Req.Test.json(%{
        "error" => %{
          "code" => "profile_invalid",
          "message" => @key_a,
          "diagnostic" => diagnostic,
          "fieldErrors" => [
            %{
              "field" => field,
              "message" => "This profile does not configure the requested level."
            }
          ]
        }
      })
    end)

    assert {:error, %APIError{} = error} =
             Client.run(
               context("https://host-a.invalid", @key_a),
               f["record"]["request"]["input"]
             )

    assert error.message =~ "profile_invalid"
    assert error.message =~ field
    assert error.message =~ run_id
    refute error.message =~ @key_a
    refute error.ambiguous?
  end

  test "download contract preserves arbitrary binary bytes and validates media once" do
    raw = <<0, 255, 10, 13, 34>>

    Req.Test.stub(Client, fn conn ->
      conn
      |> Plug.Conn.put_resp_content_type("application/octet-stream")
      |> Plug.Conn.put_resp_header("content-disposition", "attachment; filename=support.json")
      |> Plug.Conn.resp(200, raw)
    end)

    assert {:ok, %{body: ^raw, content_type: "application/octet-stream"}} =
             Client.get_artifact(
               context("https://host-a.invalid", @key_a),
               "00000000-0000-4000-8000-000000000001",
               "support.json"
             )
  end

  test "history reads share only their explicit bearer and foreign-key denial never retries", %{
    fixture: f
  } do
    parent = self()

    Req.Test.stub(Client, fn conn ->
      [bearer] = Plug.Conn.get_req_header(conn, "authorization")
      send(parent, {:history_key, bearer})
      assert Plug.Conn.get_req_header(conn, "cookie") == []

      if bearer == "Bearer " <> @key_a do
        Req.Test.json(conn, f["record"])
      else
        conn
        |> Plug.Conn.put_status(404)
        |> Req.Test.json(%{
          "error" => %{"code" => "history_not_found", "message" => "Not found."}
        })
      end
    end)

    id = f["record"]["runId"]

    for host <- ["https://host-a.invalid", "https://host-b.invalid"] do
      assert Client.get_history(context(host, @key_a), id) == {:ok, f["record"]}
      assert_receive {:history_key, "Bearer " <> @key_a}
    end

    assert {:error, %APIError{status: 404}} =
             Client.get_history(context("https://host-a.invalid", @key_b), id)

    assert_receive {:history_key, "Bearer " <> @key_b}
    refute_receive {:history_key, _}, 50
  end
end
