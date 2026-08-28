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

    assert {:ok, %{"totalCount" => 3, "cachedCount" => 1}, %{}} =
             HardenAPI.get_stats(handle)
  end

  test "saved-profile model refresh sends only the profile ID path" do
    handle = APIFixtures.insert_session()

    Req.Test.stub(HardenAPI, fn conn ->
      assert conn.method == "POST"
      assert conn.request_path == "/api/v1/profiles/Primary/models:refresh"
      {:ok, body, conn} = Plug.Conn.read_body(conn)
      assert body == ""
      assert Plug.Conn.get_req_header(conn, "authorization") == ["Bearer " <> APIFixtures.token()]

      Req.Test.json(conn, APIFixtures.success(%{"profile" => %{"models" => []}}))
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
end
