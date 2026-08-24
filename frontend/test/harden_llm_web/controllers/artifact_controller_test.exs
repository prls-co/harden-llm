defmodule HardenLlmWeb.ArtifactControllerTest do
  use HardenLlmWeb.ConnCase, async: true

  alias HardenLlmWeb.{APIFixtures, ArtifactController, HardenAPI, SessionVault}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-008

  setup %{conn: conn} do
    handle = APIFixtures.insert_session()
    {:ok, conn: init_test_session(conn, APIFixtures.session_map(handle)), handle: handle}
  end

  test "accepts only an exact configured origin and emits a no-store 303", %{conn: conn} do
    location = "https://artifacts.example.test/harden-llm/object.json?X-Amz-Signature=sensitive"

    Req.Test.stub(HardenAPI, fn conn ->
      conn |> Plug.Conn.put_resp_header("location", location) |> Plug.Conn.send_resp(303, "")
    end)

    conn = get(conn, ~p"/traces/trace-test/artifacts/artifact-test")
    assert conn.status == 303
    assert get_resp_header(conn, "location") == [location]
    assert get_resp_header(conn, "cache-control") == ["no-store"]
    assert get_resp_header(conn, "referrer-policy") == ["no-referrer"]
    refute conn.resp_body =~ "X-Amz-Signature"
  end

  test "rejects scheme, host, port, userinfo, and fragment changes" do
    refute ArtifactController.exact_origin?("http://artifacts.example.test/object")
    refute ArtifactController.exact_origin?("https://evil.example.test/object")
    refute ArtifactController.exact_origin?("https://artifacts.example.test:444/object")
    refute ArtifactController.exact_origin?("https://user@artifacts.example.test/object")
    refute ArtifactController.exact_origin?("https://artifacts.example.test/object#secret")

    assert ArtifactController.exact_origin?(
             "https://artifacts.example.test:443/object?signature=secret"
           )
  end

  test "unsafe backend redirect becomes a generic gateway failure", %{conn: conn} do
    Req.Test.stub(HardenAPI, fn conn ->
      conn
      |> Plug.Conn.put_resp_header("location", "https://evil.example.test/object?secret=yes")
      |> Plug.Conn.send_resp(303, "")
    end)

    conn = get(conn, ~p"/traces/trace-test/artifacts/artifact-test")
    assert text_response(conn, 502) == "Artifact download is temporarily unavailable."
    refute conn.resp_body =~ "evil.example.test"
  end

  test "artifact 401 redirects through the session-revocation endpoint", %{
    conn: conn,
    handle: handle
  } do
    stub_unauthorized()

    response = get(conn, ~p"/traces/trace-test/artifacts/artifact-test")
    assert redirected_to(response) == ~p"/session/expired"

    expired = get(conn, ~p"/session/expired")
    assert redirected_to(expired) == ~p"/login"
    assert SessionVault.lookup(handle) == :error
  end

  test "bundle 401 redirects through the session-revocation endpoint", %{
    conn: conn,
    handle: handle
  } do
    stub_unauthorized()

    response = get(conn, ~p"/profiles/bundle")
    assert redirected_to(response) == ~p"/session/expired"

    expired = get(conn, ~p"/session/expired")
    assert redirected_to(expired) == ~p"/login"
    assert SessionVault.lookup(handle) == :error
  end

  defp stub_unauthorized do
    Req.Test.stub(HardenAPI, fn conn ->
      {status, envelope} = APIFixtures.error(401, "session_expired")
      conn |> Plug.Conn.put_status(status) |> Req.Test.json(envelope)
    end)
  end
end
