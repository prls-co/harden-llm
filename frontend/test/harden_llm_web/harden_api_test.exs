defmodule HardenLlmWeb.HardenAPITest do
  use ExUnit.Case, async: true

  alias HardenLlmWeb.{APIError, APIFixtures, HardenAPI}

  require OpenTelemetry.Tracer, as: Tracer

  setup context do
    Req.Test.set_req_test_from_context(context)
    :ok
  end

  test "model listing uses the OpenAI route and the configured bearer token only" do
    parent = self()

    Req.Test.stub(HardenAPI, fn conn ->
      send(parent, {
        :request,
        conn.method,
        conn.request_path,
        Plug.Conn.get_req_header(conn, "authorization"),
        Plug.Conn.get_req_header(conn, "x-prls-session-reference"),
        Plug.Conn.get_req_header(conn, "cookie")
      })

      Req.Test.json(conn, APIFixtures.models())
    end)

    assert {:ok, %{"object" => "list", "data" => [%{"id" => "model-test"}]}} = HardenAPI.models()
    assert_received {:request, "GET", "/v1/models", ["Bearer " <> token], [], []}
    assert token == APIFixtures.token()
  end

  test "Responses sends the standard JSON request once and returns the standard response" do
    request = %{"model" => "model-test", "input" => "hello", "store" => false}

    Req.Test.stub(HardenAPI, fn conn ->
      assert conn.method == "POST"
      assert conn.request_path == "/v1/responses"
      assert Plug.Conn.get_req_header(conn, "authorization") == ["Bearer " <> APIFixtures.token()]
      assert Plug.Conn.get_req_header(conn, "x-prls-session-reference") == []
      assert Plug.Conn.get_req_header(conn, "cookie") == []
      assert Plug.Conn.get_req_header(conn, "content-type") |> hd() =~ "application/json"
      {:ok, body, conn} = Plug.Conn.read_body(conn)
      assert Jason.decode!(body) == request
      Req.Test.json(conn, APIFixtures.response())
    end)

    assert {:ok, %{"output_text" => "fixture response"}} = HardenAPI.responses(request)
    Req.Test.verify!()
  end

  test "proxy errors are normalized without exposing upstream details" do
    Req.Test.stub(HardenAPI, fn conn ->
      conn
      |> Plug.Conn.put_status(429)
      |> Req.Test.json(APIFixtures.error("rate_limit_exceeded"))
    end)

    assert {:error,
            %APIError{category: :rate_limited, status: 429, code: "rate_limit_exceeded"} = error} =
             HardenAPI.responses(%{"model" => "model-test", "input" => "hello"})

    assert error.message == "The service is busy. Try again later."
    refute error.message =~ "sensitive"
    refute error.ambiguous?
  end

  test "an upstream failure after dispatch is marked as an unknown outcome" do
    Req.Test.stub(HardenAPI, fn conn ->
      conn |> Plug.Conn.put_status(503) |> Req.Test.json(APIFixtures.error("provider_error"))
    end)

    assert {:error, %APIError{status: 503, ambiguous?: true}} =
             HardenAPI.responses(%{"model" => "model-test", "input" => "hello"})
  end

  test "malformed success bodies fail closed" do
    Req.Test.stub(HardenAPI, fn conn -> Req.Test.json(conn, %{"unexpected" => true}) end)

    assert {:error, %APIError{category: :protocol}} =
             HardenAPI.responses(%{"model" => "m", "input" => "x"})
  end

  test "active W3C trace context is propagated without forwarding human session material" do
    parent = self()

    Tracer.with_span "harden-api-propagation-test" do
      expected_trace_id =
        :otel_tracer.current_span_ctx()
        |> elem(1)
        |> Integer.to_string(16)
        |> String.downcase()
        |> String.pad_leading(32, "0")

      Req.Test.stub(HardenAPI, fn conn ->
        send(parent, {:traceparent, Plug.Conn.get_req_header(conn, "traceparent")})
        Req.Test.json(conn, APIFixtures.models())
      end)

      assert {:ok, _} = HardenAPI.models()
      assert_receive {:traceparent, ["00-" <> rest]}, 1_000
      assert rest =~ ~r/^[0-9a-f]{32}-[0-9a-f]{16}-0[01]$/
      assert String.starts_with?(rest, expected_trace_id <> "-")
    end
  end

  test "client disables retries and redirects" do
    source = File.read!("lib/harden_llm_web/harden_api.ex")
    assert source =~ "retry: false"
    assert source =~ "redirect: false"
  end
end
