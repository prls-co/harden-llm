defmodule HardenLlmWeb.TraceControllerTest do
  use HardenLlmWeb.ConnCase, async: true

  alias HardenLlmWeb.{APIFixtures, HardenAPI}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-036

  setup %{conn: conn} do
    handle = APIFixtures.insert_session()
    {:ok, conn: init_test_session(conn, APIFixtures.session_map(handle))}
  end

  test "serves the authenticated trace result as no-store JSON", %{conn: conn} do
    Req.Test.stub(HardenAPI, fn conn ->
      assert conn.method == "GET"
      assert conn.request_path == "/api/v1/traces/trace-test"
      Req.Test.json(conn, APIFixtures.success(APIFixtures.trace()))
    end)

    response = get(conn, ~p"/traces/trace-test")

    assert response.status == 200
    assert get_resp_header(response, "content-type") |> hd() =~ "application/json"
    assert get_resp_header(response, "cache-control") == ["no-store"]
    assert get_resp_header(response, "referrer-policy") == ["no-referrer"]
    assert response.resp_body =~ "trace-test"
    assert response.resp_body =~ "safe restored prompt"
    assert response.resp_body =~ "resources"
  end

  test "maps a missing trace to a safe JSON 404", %{conn: conn} do
    Req.Test.stub(HardenAPI, fn conn ->
      {status, envelope} = APIFixtures.error(404, "trace_not_found")
      conn |> Plug.Conn.put_status(status) |> Req.Test.json(envelope)
    end)

    response = get(conn, ~p"/traces/trace-missing")

    assert response.status == 404
    assert json_response(response, 404) == %{"error" => "Trace not found."}
  end
end
