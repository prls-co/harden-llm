defmodule HardenLlmWeb.HardenAPIStreamTest do
  use ExUnit.Case, async: true
  alias HardenLlmWeb.{APIError, APIFixtures, HardenAPI}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-115
  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-428
  setup context do
    Req.Test.set_req_test_from_context(context)
    fixture = File.read!("../test/fixtures/reference/workspace.json") |> Jason.decode!()
    {:ok, fixture: fixture}
  end

  test "one POST delivers progress, terminal output and later recording", %{fixture: fixture} do
    parent = self()
    payload = fixture["record"]["request"]["input"]

    Req.Test.stub(HardenAPI, fn request ->
      assert request.method == "POST"
      assert request.request_path == "/api/v1/run"
      assert Plug.Conn.get_req_header(request, "accept") == ["text/event-stream"]

      assert Plug.Conn.get_req_header(request, "authorization") == [
               "Bearer " <> APIFixtures.token()
             ]

      assert Plug.Conn.get_req_header(request, "x-prls-session-reference") == []
      assert Plug.Conn.get_req_header(request, "cookie") == []
      {:ok, body, request} = Plug.Conn.read_body(request)
      assert Jason.decode!(body) == payload
      send(parent, :run_request)
      response(request, fixture["stream"], "\r\n")
    end)

    assert {:ok, %{"result" => result, "state" => state}} =
             HardenAPI.run_stream(HardenAPI.context(), payload, fn event ->
               send(parent, {:event, event["type"]})
               :cont
             end)

    assert result == fixture["run"]["result"]
    assert state == fixture["run"]["state"]
    assert_receive {:event, "run.progress"}
    assert_receive {:event, "run.progress"}
    assert_receive {:event, "run.completed"}
    assert_receive {:event, "history.recorded"}
    assert_receive :run_request
    refute_receive :run_request, 50
  end

  test "malformed order, correlation and duplicate terminals fail without replay", %{
    fixture: fixture
  } do
    parent = self()
    events = fixture["stream"]

    invalid = [
      List.update_at(events, 1, &Map.put(&1, "sequence", 1)),
      List.update_at(events, 1, &Map.put(&1, "runId", "00000000-0000-4000-8000-000000000099")),
      List.update_at(
        events,
        2,
        &put_in(&1, ["data", "result", "runId"], "00000000-0000-4000-8000-000000000099")
      ),
      List.update_at(events, 3, fn _ -> Map.put(Enum.at(events, 2), "sequence", 4) end),
      [List.last(events) | Enum.drop(events, -1)]
    ]

    for {stream, index} <- Enum.with_index(invalid) do
      Req.Test.stub(HardenAPI, fn request ->
        send(parent, {:run_request, index})
        response(request, stream)
      end)

      assert {:error, %APIError{category: :protocol}} =
               HardenAPI.run_stream(HardenAPI.context(), %{}, fn _ -> :cont end)

      assert_receive {:run_request, ^index}
      refute_receive {:run_request, ^index}, 50
    end
  end

  test "an incomplete recording stream still delivers the terminal inference once", %{
    fixture: fixture
  } do
    parent = self()
    Req.Test.stub(HardenAPI, &response(&1, Enum.drop(fixture["stream"], -1)))

    assert {:error, %APIError{category: :protocol}} =
             HardenAPI.run_stream(HardenAPI.context(), %{}, fn event ->
               if event["type"] == "run.completed",
                 do: send(parent, {:result, event["data"]["result"]})

               :cont
             end)

    assert_receive {:result, %{"output" => %{"ok" => true}}}
    refute_receive {:result, _}, 50
  end

  test "timeout terminal remains a typed failure rather than a protocol error", %{
    fixture: fixture
  } do
    stream =
      List.update_at(fixture["stream"], 2, fn event ->
        event
        |> Map.put("type", "run.failed")
        |> put_in(["data", "result", "status"], "timeout")
        |> put_in(["data", "error"], %{"code" => "run_timeout", "message" => "Deadline reached."})
      end)

    Req.Test.stub(HardenAPI, &response(&1, stream))

    assert {:error, %APIError{category: :backend, ambiguous?: false}} =
             HardenAPI.run_stream(HardenAPI.context(), %{}, fn _ -> :cont end)
  end

  defp response(request, events, newline \\ "\n") do
    body =
      Enum.map_join(events, fn event ->
        "event: " <>
          event["type"] <> newline <> "data: " <> Jason.encode!(event) <> newline <> newline
      end)

    request
    |> Plug.Conn.put_resp_content_type("text/event-stream")
    |> Plug.Conn.send_resp(200, body)
  end
end
