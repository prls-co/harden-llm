defmodule HardenLlmWeb.AuthenticatedWorkflowCanaryTest do
  use ExUnit.Case, async: false
  use Wallaby.Feature

  @moduletag :browser

  import HardenLlmWeb.BrowserFeatureCase

  alias HardenLlmWeb.BrowserBackend
  alias Wallaby.Query

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-047

  setup {HardenLlmWeb.BrowserFeatureCase, :setup_browser}

  feature "an enabled login preserves one streamed reference result when history is unavailable",
          %{
            session: session,
            backend: backend
          } do
    session =
      session
      |> resize_window(1_440, 900)
      |> visit("/")
      |> sign_in_shared_login("browser@example.test", "browser-password-123")
      |> assert_has(Query.css("#workspace-page"))
      |> assert_has(Query.css("#backend-status", text: "Backend ready"))
      |> assert_has(Query.css("#run_selectedProfileId[value='CPA Responses']"))
      |> assert_has(Query.css("#run_modelId[value='gpt-5.6-luna']", visible: :any))
      |> click(Query.css("#history-fold-toggle"))
      |> assert_has(Query.css("#workspace-history-error", text: "History unavailable"))
      |> fill_in(Query.css("#run_userPrompt"), with: "one reliable call")
      |> click(Query.css("#run_selectedProfileId"))
      |> send_keys([:enter])

    assert BrowserBackend.responses(backend) == []

    session =
      session
      |> click(Query.css("#run-submit"))
      |> assert_has(Query.css("#run-output", text: "ok"))
      |> assert_has(Query.css("#recording-status[data-status='not_saved']", text: "preserved"))
      |> assert_has(Query.css("#workspace-history-error", text: "History unavailable"))
      |> assert_has(Query.css("#logout-button"))

    calls = BrowserBackend.calls(backend)
    assert {"GET", "/api/v1/profiles"} in calls
    assert Enum.count(calls, &(&1 == {"POST", "/api/v1/run"})) == 1
    refute Enum.any?(calls, fn {_method, path} -> String.starts_with?(path, "/v1/") end)

    assert [request] = BrowserBackend.responses(backend)
    assert request["profileId"] == "CPA Responses"
    assert request["userPrompt"] == "one reliable call"
    assert request["callType"] == "text"
    assert request["modelId"] == "gpt-5.6-luna"
    refute Map.has_key?(request, "apiKeyHash")

    session |> click(Query.css("#logout-button")) |> assert_shared_login_page()
  end
end
