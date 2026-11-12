defmodule HardenLlmWeb.BrowserBackendTest do
  use ExUnit.Case, async: true

  alias HardenLlmWeb.{APIFixtures, BrowserBackend}
  alias HardenLlmUI.Client
  alias HardenLlmUI.Client.Context

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-047
  # Cheap regression for the reference boundary used by the native canary.
  setup do
    {:ok, owner} = BrowserBackend.start()
    on_exit(fn -> if Process.alive?(owner), do: Agent.stop(owner) end)
    {:ok, owner: owner}
  end

  test "the canary uses reference profiles, drafts and one streamed Run", %{owner: owner} do
    profiles = request(owner, :get, "/api/v1/profiles")
    assert profiles.status == 200
    assert [%{"id" => "CPA Responses"} | _] = Jason.decode!(profiles.resp_body)["profiles"]

    draft = %{"draftKey" => "workspace:browser-test", "revision" => 0, "payload" => %{}}
    saved = request(owner, :post, "/api/v1/state", draft)
    assert saved.status == 200
    assert Jason.decode!(saved.resp_body)["revision"] == 1

    restored = request(owner, :get, "/api/v1/state?draftKey=workspace%3Abrowser-test")
    assert Jason.decode!(restored.resp_body) == Jason.decode!(saved.resp_body)

    body = %{"profileId" => "CPA Responses", "userPrompt" => "one reliable call"}

    context =
      Context.new(
        base_url: "https://fixture.test",
        api_key: APIFixtures.token(),
        request_options: [plug: {BrowserBackend, owner: owner}]
      )

    assert {:ok, profile} = Client.get_profile(context, "CPA Responses")
    assert profile["sourceProfileId"] == "CPA Responses"

    assert {:ok, %{"result" => result, "state" => state}} =
             Client.run_stream(context, body, fn _event -> :cont end)

    assert result["status"] == "succeeded"
    assert result["output"] == %{"ok" => true}
    assert result["history"]["status"] == "not_saved"
    assert state["lastRunId"] == result["runId"]

    history = request(owner, :get, "/api/v1/history")
    assert history.status == 503
    assert Jason.decode!(history.resp_body)["error"]["code"] == "history_unavailable"

    assert BrowserBackend.responses(owner) == [body]
    assert Enum.count(BrowserBackend.calls(owner), &(&1 == {"POST", "/api/v1/run"})) == 1

    refute Enum.any?(BrowserBackend.calls(owner), fn {_method, path} ->
             String.starts_with?(path, "/v1/")
           end)
  end

  test "independent browser fixtures do not share draft state", %{owner: owner} do
    {:ok, other} = BrowserBackend.start()
    on_exit(fn -> if Process.alive?(other), do: Agent.stop(other) end)

    request(owner, :post, "/api/v1/state", %{
      "draftKey" => "workspace:browser-test",
      "revision" => 0,
      "payload" => %{"userPrompt" => "owned"}
    })

    result = request(other, :get, "/api/v1/state?draftKey=workspace%3Abrowser-test")
    assert Jason.decode!(result.resp_body)["payload"] == %{}
    assert Jason.decode!(result.resp_body)["revision"] == 0
  end

  defp request(owner, method, path, body \\ nil) do
    conn = Plug.Test.conn(method, path, if(body, do: Jason.encode!(body), else: ""))

    conn
    |> Plug.Conn.put_req_header("authorization", "Bearer " <> APIFixtures.token())
    |> BrowserBackend.call(owner: owner)
  end
end
