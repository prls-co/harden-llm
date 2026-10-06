defmodule HardenLlmWeb.AuthenticatedWorkflowCanaryTest do
  use ExUnit.Case, async: false
  use Wallaby.Feature

  @moduletag :browser

  import HardenLlmWeb.BrowserFeatureCase

  alias HardenLlmWeb.BrowserBackend
  alias Wallaby.Query

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-047

  setup {HardenLlmWeb.BrowserFeatureCase, :setup_browser}

  feature "an enabled login sends a standard Responses request through the proxy", %{
    session: session
  } do
    session =
      session
      |> resize_window(1_440, 900)
      |> visit("/")
      |> sign_in_shared_login("browser@example.test", "browser-password-123")
      |> assert_has(Query.css("#workspace-page"))
      |> assert_has(Query.css("#proxy-status", text: "Proxy reachable"))
      |> assert_has(Query.css("#run_model"))
      |> assert_has(Query.css("#available-models option[value='model-test']", visible: :any))
      |> fill_in(Query.css("#run_model"), with: "model-test")
      |> fill_in(Query.css("#run_userPrompt"), with: "return a deterministic response")
      |> click(Query.css("#run-submit"))
      |> assert_has(Query.css("#current-result", text: "deterministic browser response"))
      |> assert_has(Query.css("#recording-error", text: "result is shown"))
      |> assert_has(Query.css("#history-error", text: "Shared history is unavailable"))
      |> assert_has(Query.css("#logout-button"))

    calls = BrowserBackend.calls()
    assert {"GET", "/v1/models"} in calls
    assert Enum.count(calls, &(&1 == {"POST", "/v1/responses"})) == 1
    refute Enum.any?(calls, fn {_method, path} -> String.starts_with?(path, "/api/v1/") end)

    assert [request] = BrowserBackend.responses()
    assert request["model"] == "model-test"
    assert request["input"] == "return a deterministic response"
    assert request["store"] == false
    assert request["harden"] == %{"cache" => "off", "diagnostics" => true}
    refute Map.has_key?(request, "profileId")

    session |> click(Query.css("#logout-button")) |> assert_shared_login_page()
  end
end
