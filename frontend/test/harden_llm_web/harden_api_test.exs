defmodule HardenLlmWeb.HardenAPITest do
  use ExUnit.Case, async: true

  alias HardenLlmWeb.{APIError, APIFixtures, HardenAPI, SessionVault}

  require OpenTelemetry.Tracer, as: Tracer

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-003
  # PLAN-HLLM-WIDGET-PARITY-001 TEST-109

  setup context do
    Req.Test.set_req_test_from_context(context)
    :ok
  end

  test "login sends JSON and never sends bearer credentials" do
    Req.Test.stub(HardenAPI, fn conn ->
      assert conn.method == "POST"
      assert conn.request_path == "/api/v1/auth/login"
      assert Plug.Conn.get_req_header(conn, "accept") == ["application/json"]
      assert Plug.Conn.get_req_header(conn, "authorization") == []
      {:ok, body, conn} = Plug.Conn.read_body(conn)

      assert Jason.decode!(body) == %{
               "email" => "operator@example.test",
               "password" => "fixture-password-123"
             }

      Req.Test.json(conn, APIFixtures.success(APIFixtures.login_result()))
    end)

    assert {:ok, result, %{}} = HardenAPI.login("operator@example.test", "fixture-password-123")
    assert result["accessToken"] == APIFixtures.token()
  end

  test "authenticated requests resolve the vault token and use one bearer header" do
    handle = APIFixtures.insert_session()

    Req.Test.stub(HardenAPI, fn conn ->
      assert conn.method == "GET"
      assert conn.request_path == "/api/v1/auth/session"
      assert Plug.Conn.get_req_header(conn, "authorization") == ["Bearer " <> APIFixtures.token()]
      assert length(Plug.Conn.get_req_header(conn, "authorization")) == 1
      Req.Test.json(conn, APIFixtures.success(APIFixtures.principal()))
    end)

    assert {:ok, %{"ownerId" => "owner-test"}, %{}} = HardenAPI.get_session(handle)
  end

  test "stats use the authenticated authoritative aggregate endpoint" do
    handle = APIFixtures.insert_session()

    Req.Test.stub(HardenAPI, fn conn ->
      assert conn.method == "GET"
      assert conn.request_path == "/api/v1/stats"
      assert Plug.Conn.get_req_header(conn, "authorization") == ["Bearer " <> APIFixtures.token()]
      Req.Test.json(conn, APIFixtures.success(APIFixtures.stats()))
    end)

    assert {:ok, %{"totalCount" => 3, "cached" => %{"count" => 1}}, %{}} =
             HardenAPI.get_stats(handle)
  end

  test "history accepts the terminal page's null cursor" do
    handle = APIFixtures.insert_session()

    Req.Test.stub(HardenAPI, fn conn ->
      assert conn.method == "GET"
      assert conn.request_path == "/api/v1/history"

      Req.Test.json(
        conn,
        APIFixtures.success(%{"items" => [APIFixtures.history_item()], "nextCursor" => nil})
      )
    end)

    assert {:ok, %{"items" => [%{"runId" => "run-test"}], "nextCursor" => nil}, %{}} =
             HardenAPI.list_history(handle, limit: 20)
  end

  test "numbered history sends page and size and rejects a legacy response" do
    handle = APIFixtures.insert_session()

    Req.Test.stub(HardenAPI, fn conn ->
      assert URI.decode_query(conn.query_string) == %{"limit" => "25", "page" => "7"}

      Req.Test.json(
        conn,
        APIFixtures.history_page([APIFixtures.history_item()], 7, 25, 151)
      )
    end)

    assert {:ok, %{"items" => [_], "pagination" => pagination}, %{}} =
             HardenAPI.list_history(handle, page: 7, limit: 25)

    assert pagination == %{"page" => 7, "pageSize" => 25, "totalCount" => 151}

    Req.Test.stub(HardenAPI, fn conn ->
      Req.Test.json(
        conn,
        APIFixtures.success(%{"items" => [APIFixtures.history_item()], "nextCursor" => nil})
      )
    end)

    assert {:error, %APIError{category: :protocol}} =
             HardenAPI.list_history(handle, page: 7, limit: 25)
  end

  test "numbered history rejects a page whose item count disagrees with its exact total" do
    handle = APIFixtures.insert_session()

    Req.Test.stub(HardenAPI, fn conn ->
      Req.Test.json(
        conn,
        APIFixtures.history_page([APIFixtures.history_item()], 1, 10, 20)
      )
    end)

    assert {:error, %APIError{category: :protocol}} =
             HardenAPI.list_history(handle, page: 1, limit: 10)
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-060
  test "history and trace never pass retired execution records to the UI" do
    handle = APIFixtures.insert_session()
    old = %{"runId" => "run-test", "traceId" => "trace-test", "status" => "succeeded"}

    Req.Test.stub(HardenAPI, fn conn ->
      payload =
        case conn.request_path do
          "/api/v1/history" -> %{"items" => [Map.put(APIFixtures.history_item(), "result", old)]}
          "/api/v1/traces/trace-test" -> Map.put(APIFixtures.trace(), "record", old)
        end

      Req.Test.json(conn, APIFixtures.success(payload))
    end)

    assert {:error, %APIError{category: :protocol}} = HardenAPI.list_history(handle)
    assert {:error, %APIError{category: :protocol}} = HardenAPI.get_trace(handle, "trace-test")
  end

  test "diagnostics operations reject malformed identities, equations, and coverage" do
    handle = APIFixtures.insert_session()

    malformed = [
      {"/api/v1/run", :post,
       put_in(APIFixtures.run_result(), ["accounting", "result", "usage", "totalTokens"], 99)},
      {"/api/v1/stats", :get,
       put_in(APIFixtures.stats(), ["resultAccounting", "cost", "coverage", "unknown"], 0)},
      {"/api/v1/traces/trace-test", :get,
       put_in(APIFixtures.trace(), ["record", "traceId"], "different-trace")}
    ]

    for {path, method, result} <- malformed do
      Req.Test.stub(HardenAPI, fn conn ->
        assert conn.method == method |> Atom.to_string() |> String.upcase()
        assert conn.request_path == path
        Req.Test.json(conn, APIFixtures.success(result))
      end)

      response =
        case path do
          "/api/v1/run" -> HardenAPI.run(handle, %{})
          "/api/v1/stats" -> HardenAPI.get_stats(handle)
          _ -> HardenAPI.get_trace(handle, "trace-test")
        end

      assert {:error, %APIError{category: :protocol}} = response
      Req.Test.verify!()
    end
  end

  test "saved-profile model refresh sends only the profile ID path" do
    handle = APIFixtures.insert_session()

    Req.Test.stub(HardenAPI, fn conn ->
      assert conn.method == "POST"
      assert conn.request_path == "/api/v1/profiles/Primary/models:refresh"
      {:ok, body, conn} = Plug.Conn.read_body(conn)
      assert body == ""
      assert Plug.Conn.get_req_header(conn, "authorization") == ["Bearer " <> APIFixtures.token()]

      Req.Test.json(
        conn,
        APIFixtures.success(put_in(APIFixtures.profile_state(), ["profile", "models"], []))
      )
    end)

    assert {:ok, %{"profile" => %{"models" => []}}, %{}} =
             HardenAPI.refresh_profile_models(handle, "Primary")
  end

  test "active W3C trace context is injected into backend requests" do
    handle = APIFixtures.insert_session()
    parent = self()

    Tracer.with_span "harden-api-propagation-test" do
      expected_trace_id =
        :otel_tracer.current_span_ctx()
        |> elem(1)
        |> Integer.to_string(16)
        |> String.downcase()
        |> String.pad_leading(32, "0")

      Req.Test.stub(HardenAPI, fn conn ->
        [traceparent] = Plug.Conn.get_req_header(conn, "traceparent")
        assert traceparent =~ ~r/^00-[0-9a-f]{32}-[0-9a-f]{16}-0[01]$/
        refute traceparent =~ "00000000000000000000000000000000"
        send(parent, {:traceparent, traceparent})
        Req.Test.json(conn, APIFixtures.success(APIFixtures.principal()))
      end)

      assert {:ok, %{"ownerId" => "owner-test"}, %{}} = HardenAPI.get_session(handle)
      assert_receive {:traceparent, "00-" <> rest}, 1_000
      assert String.starts_with?(rest, expected_trace_id <> "-")
    end
  end

  test "request options disable retries and redirects and expose bounded timeout policy" do
    source = File.read!("lib/harden_llm_web/harden_api.ex")
    assert source =~ "retry: false"
    assert source =~ "redirect: false"
    assert source =~ "run_timeout_ms"
    assert source =~ "api_timeout_ms"

    handle = APIFixtures.insert_session()
    {:ok, counter} = Agent.start_link(fn -> 0 end)

    Req.Test.stub(HardenAPI, fn conn ->
      Agent.update(counter, &(&1 + 1))
      {status, envelope} = APIFixtures.error(503, "backend_unavailable")
      conn |> Plug.Conn.put_status(status) |> Req.Test.json(envelope)
    end)

    assert {:error, %APIError{category: :unavailable}} = HardenAPI.get_session(handle)
    assert Agent.get(counter, & &1) == 1
  end

  test "run transport failure is ambiguous and is never retried" do
    handle = APIFixtures.insert_session()
    {:ok, counter} = Agent.start_link(fn -> 0 end)

    Req.Test.stub(HardenAPI, fn conn ->
      Agent.update(counter, &(&1 + 1))
      Req.Test.transport_error(conn, :closed)
    end)

    assert {:error, %APIError{category: :transport, ambiguous?: true}} =
             HardenAPI.run(handle, %{
               "profileId" => "Primary",
               "userPrompt" => "fixture",
               "callType" => "text"
             })

    assert Agent.get(counter, & &1) == 1
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-088 TEST-252 TEST-253
  test "run_stream parses one request-bound SSE and stops at the terminal event" do
    handle = APIFixtures.insert_session()
    parent = self()
    payload = %{"profileId" => "Primary", "userPrompt" => "fixture", "callType" => "text"}

    progress = %{
      "schemaVersion" => 1,
      "sequence" => 1,
      "runId" => "run-test",
      "callId" => "call-test",
      "traceId" => "trace-test",
      "type" => "run.started",
      "data" => %{
        "schemaVersion" => 1,
        "sequence" => 1,
        "runId" => "run-test",
        "callId" => "call-test",
        "traceId" => "trace-test",
        "type" => "run.started",
        "stage" => "original.generate",
        "branch" => "original",
        "profileId" => "Primary",
        "attemptsUsed" => 0,
        "attemptsRemaining" => 1,
        "elapsedMs" => 0,
        "receivedBytes" => 0,
        "eventCount" => 0,
        "outputBytes" => 0,
        "outputCodePoints" => 0,
        "terminal" => false
      }
    }

    terminal = %{
      "schemaVersion" => 1,
      "sequence" => 2,
      "runId" => "run-test",
      "callId" => "call-test",
      "traceId" => "trace-test",
      "type" => "run.completed",
      "data" => %{
        "state" => %{"lastRunId" => "run-test", "lastTraceId" => "trace-test"},
        "result" => APIFixtures.run_result(),
        "error" => nil
      }
    }

    Req.Test.stub(HardenAPI, fn conn ->
      assert Plug.Conn.get_req_header(conn, "accept") == ["text/event-stream"]
      assert Plug.Conn.get_req_header(conn, "authorization") == ["Bearer " <> APIFixtures.token()]
      {:ok, body, conn} = Plug.Conn.read_body(conn)
      assert Jason.decode!(body) == payload

      body =
        "event: run.started\ndata: " <>
          Jason.encode!(progress) <>
          "\n\n" <>
          "event: run.completed\ndata: " <> Jason.encode!(terminal) <> "\n\n"

      conn
      |> Plug.Conn.put_resp_content_type("text/event-stream")
      |> Plug.Conn.send_resp(200, body)
    end)

    assert {:ok, result, state} =
             HardenAPI.run_stream(handle, payload, fn event ->
               send(parent, {:stream_event, event["type"]})
               :cont
             end)

    assert result["runId"] == "run-test"
    assert state == %{"lastRunId" => "run-test", "lastTraceId" => "trace-test"}
    assert_receive {:stream_event, "run.started"}
    assert_receive {:stream_event, "run.completed"}
  end

  # WEB-TEST-088: a terminal failure is delivered once and never resubmitted.
  test "run_stream exposes a redacted terminal failure without retrying" do
    handle = APIFixtures.insert_session()
    {:ok, counter} = Agent.start_link(fn -> 0 end)
    result = APIFixtures.run_result() |> Map.put("status", "failed")

    terminal = %{
      "schemaVersion" => 1,
      "sequence" => 1,
      "runId" => "run-test",
      "callId" => "call-test",
      "traceId" => "trace-test",
      "type" => "run.failed",
      "data" => %{
        "state" => %{"lastRunId" => "run-test", "lastTraceId" => "trace-test"},
        "result" => result,
        "error" => %{"code" => "run_failed", "message" => "private provider output"}
      }
    }

    Req.Test.stub(HardenAPI, fn conn ->
      Agent.update(counter, &(&1 + 1))
      body = "event: run.failed\ndata: " <> Jason.encode!(terminal) <> "\n\n"

      conn
      |> Plug.Conn.put_resp_content_type("text/event-stream")
      |> Plug.Conn.send_resp(200, body)
    end)

    assert {:error, %APIError{category: :backend, code: "run_failed", message: message}} =
             HardenAPI.run_stream(handle, %{}, fn _event -> :cont end)

    assert message == "The run failed."
    assert Agent.get(counter, & &1) == 1
  end

  test "missing endpoint credential is a non-ambiguous validation error" do
    handle = APIFixtures.insert_session()

    Req.Test.stub(HardenAPI, fn conn ->
      {status, envelope} = APIFixtures.error(422, "credential_required")
      conn |> Plug.Conn.put_status(status) |> Req.Test.json(envelope)
    end)

    assert {:error,
            %APIError{
              category: :validation,
              status: 422,
              code: "credential_required",
              message: "The selected profile has no configured endpoint credential.",
              ambiguous?: false
            }} =
             HardenAPI.run(handle, %{
               "profileId" => "CPA GPT-5.6 Luna",
               "userPrompt" => "fixture",
               "callType" => "text"
             })
  end

  test "malformed envelopes, JSON, and content types become redacted protocol errors" do
    handle = APIFixtures.insert_session()

    responses = [
      fn conn -> Req.Test.json(conn, %{"result" => %{}}) end,
      fn conn -> Plug.Conn.send_resp(conn, 200, "not-json secret-response-body") end,
      fn conn ->
        conn
        |> Plug.Conn.put_resp_content_type("text/plain")
        |> Plug.Conn.send_resp(200, "private")
      end
    ]

    for response <- responses do
      Req.Test.stub(HardenAPI, response)
      assert {:error, %APIError{category: :protocol} = error} = HardenAPI.get_session(handle)
      refute inspect(error) =~ "secret-response-body"
      refute inspect(error) =~ "private"
      Req.Test.verify!()
    end
  end

  test "backend field errors are bounded while raw backend detail is discarded" do
    handle = APIFixtures.insert_session()

    Req.Test.stub(HardenAPI, fn conn ->
      {status, envelope} =
        APIFixtures.error(422, "profile_invalid", %{
          "profile.baseUrl" => "Use an approved HTTPS origin."
        })

      conn |> Plug.Conn.put_status(status) |> Req.Test.json(envelope)
    end)

    assert {:error,
            %APIError{category: :validation, message: "Please correct the highlighted fields."} =
              error} =
             HardenAPI.save_profile(handle, "Primary", %{})

    assert error.field_errors == %{"profile.baseUrl" => "Use an approved HTTPS origin."}
    refute inspect(error) =~ "sensitive backend detail"
  end

  test "artifact redirects are returned without following them" do
    handle = APIFixtures.insert_session()

    Req.Test.stub(HardenAPI, fn conn ->
      conn
      |> Plug.Conn.put_resp_header(
        "location",
        "https://artifacts.example.test/object?signature=sensitive"
      )
      |> Plug.Conn.send_resp(303, "")
    end)

    assert {:ok, %{location: location}, %{}} =
             HardenAPI.get_artifact(handle, "trace-test", "artifact-test")

    assert URI.parse(location).host == "artifacts.example.test"
  end

  test "missing vault entry fails before any request" do
    assert {:error, %APIError{category: :unauthorized}} = HardenAPI.get_state("missing-handle")
    assert SessionVault.count() >= 0
  end

  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-209
  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-073
  @tag :recovery
  test "profile responses require backend defaults and current policy shape" do
    handle = APIFixtures.insert_session()
    result = APIFixtures.profiles([APIFixtures.profile_state()])["result"]

    policy = %{
      "maxAttempts" => 1,
      "retryOn" => [],
      "jsonRepair" => nil,
      "rerun" => nil,
      "backoff" => %{"baseDelayMs" => 0, "maxDelayMs" => 0}
    }

    legacy_policy =
      policy
      |> Map.drop(["jsonRepair", "rerun"])
      |> Map.put("repairInvalidOutput", true)

    result = put_in(result, ["defaults", "recoveryPolicy"], policy)
    Req.Test.stub(HardenAPI, fn conn -> Req.Test.json(conn, APIFixtures.success(result)) end)
    assert {:ok, ^result, %{}} = HardenAPI.list_profiles(handle)

    for invalid <- [
          Map.delete(result, "defaults"),
          put_in(result, ["defaults", "recoveryPolicy"], nil),
          put_in(result, ["defaults", "recoveryPolicy"], legacy_policy),
          put_in(
            result,
            ["defaults", "recoveryPolicy"],
            Map.put(policy, "repairInvalidOutput", false)
          ),
          put_in(result, ["profiles", Access.at(0), "profile", "recoveryPolicy"], legacy_policy),
          put_in(result, ["defaults", "recoveryPolicy", "backoff"], %{}),
          put_in(result, ["profiles", Access.at(0), "profile", "schemaVersion"], 1)
        ] do
      Req.Test.stub(HardenAPI, fn conn -> Req.Test.json(conn, APIFixtures.success(invalid)) end)
      assert {:error, %APIError{category: :protocol}} = HardenAPI.list_profiles(handle)
    end
  end

  @tag :recovery
  test "state and individual profile responses require the current policy contract" do
    handle = APIFixtures.insert_session()
    state = APIFixtures.state()

    legacy_policy =
      state["recoveryPolicy"]
      |> Map.drop(["jsonRepair", "rerun"])
      |> Map.put("repairInvalidOutput", true)

    for invalid <- [
          Map.put(state, "schemaVersion", 1),
          Map.delete(state, "recoveryPolicy"),
          put_in(state, ["recoveryPolicy"], legacy_policy),
          put_in(state, ["recoveryPolicy", "backoff"], %{})
        ] do
      Req.Test.stub(HardenAPI, fn conn ->
        Req.Test.json(conn, APIFixtures.success(nil, invalid))
      end)

      assert {:error, %APIError{category: :protocol}} = HardenAPI.get_state(handle)
      assert {:error, %APIError{category: :protocol}} = HardenAPI.save_state(handle, state)
    end

    Req.Test.stub(HardenAPI, fn conn -> Req.Test.json(conn, APIFixtures.success(nil, state)) end)
    assert {:ok, nil, ^state} = HardenAPI.get_state(handle)

    invalid =
      update_in(APIFixtures.profile_state(), ["profile"], &Map.delete(&1, "recoveryPolicy"))

    Req.Test.stub(HardenAPI, fn conn -> Req.Test.json(conn, APIFixtures.success(invalid)) end)

    assert {:error, %APIError{category: :protocol}} =
             HardenAPI.save_profile(handle, "Primary", %{})

    assert {:error, %APIError{category: :protocol}} =
             HardenAPI.refresh_profile_models(handle, "Primary")
  end
end
