defmodule HardenLlmWeb.AuthenticatedWorkflowCanaryTest do
  use ExUnit.Case, async: false
  use Wallaby.Feature

  @moduletag :browser

  import HardenLlmWeb.BrowserFeatureCase

  alias HardenLlmWeb.BrowserBackend
  alias Wallaby.Query

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-047 TEST-047

  setup {HardenLlmWeb.BrowserFeatureCase, :setup_browser}

  feature "operator can run, reconcile history, reconnect, and sign out", %{
    session: session
  } do
    session =
      session
      |> resize_window(1_440, 900)
      |> visit("/login")
      |> assert_has(Query.css("#login-page"))
      |> fill_in(Query.text_field("Email address"), with: "browser@example.test")
      |> fill_in(Query.css("#session_password"), with: "browser-password-123")
      |> click(Query.css("#login-submit"))
      |> assert_has(Query.css("#workspace-page"))
      |> assert_has(Query.css("#backend-status", text: "Backend ready"))
      |> assert_live_socket_connected()
      |> assert_no_horizontal_overflow()
      |> visit("/profiles")
      |> assert_has(Query.css("#profiles-page"))
      |> click(Query.css("#new-profile"))
      |> fill_in(Query.text_field("Profile name"), with: "BrowserProfile")
      |> fill_in(Query.text_field("Provider family"), with: "openai")
      |> fill_in(Query.text_field("Default model"), with: "model-browser")
      |> fill_in(Query.fillable_field("HTTPS base URL"),
        with: "https://provider.example.test/v1"
      )
      |> fill_in(Query.text_field("Credential ID"), with: "browser-credential")
      |> fill_in(Query.css("#profile_apiKey"), with: "browser-provider-secret")
      |> click(Query.css("#profile-save"))
      |> assert_has(Query.css("#profile-BrowserProfile", text: "BrowserProfile"))
      |> click(Query.css("#profile-BrowserProfile button[aria-label='Refresh models']"))
      |> assert_has(Query.css("#flash-info", text: "Model catalog refreshed"))
      |> visit("/workspace")
      |> assert_has(Query.css("#backend-status", text: "Backend ready"))
      |> choose_option("#run_selectedProfileId", "BrowserProfile")
      |> click(Query.css("#input-advanced-toggle"))
      |> assert_has(Query.css("#advanced-input"))
      |> fill_in(Query.css("#run_schema"), with: ~s({"type":"object"}))
      |> assert_has(Query.css("#schema-status", text: "Schema check pending."))
      |> click(Query.css("#clear-schema"))
      |> fill_in(Query.css("#run_userPrompt"), with: "run the browser canary")
      |> trigger_prompt_shortcut("#run_userPrompt")
      |> assert_has(Query.css("#run-output", text: "deterministic browser output"))
      |> assert_has(Query.css("#run-result-panel", text: "trace-browser"))
      |> click(Query.css("#output-details-toggle"))
      |> click(Query.css("#show-run-request"))
      |> assert_has(Query.css("#run-request"))
      |> click(Query.css("#show-run-response"))
      |> assert_has(Query.css("#run-response"))

    session =
      session
      |> install_clipboard_stub()
      |> click(Query.css("#copy-run-output"))
      |> assert_has(Query.css("#copy-run-output", text: "Copied"))
      |> visit("/history")
      |> assert_has(Query.css("#history-page"))
      |> assert_has(Query.css("#history-run-browser"))
      |> click(Query.css("#history-run-browser button[aria-label='Open trace']"))
      |> assert_has(Query.css("#trace-dialog"))
      |> assert_has(Query.css("#observation-0", text: "deterministic browser output"))

    BrowserBackend.fail_next_run()

    session =
      session
      |> visit("/workspace")
      |> fill_in(Query.css("#run_userPrompt"), with: "ambiguous browser outcome")
      |> trigger_prompt_shortcut("#run_userPrompt")
      |> assert_has(
        Query.css(
          "#run-error",
          text:
            "The run outcome is unknown. Refresh History before deciding whether to run again."
        )
      )
      |> click(Query.css("#history-fold-toggle"))
      |> assert_has(Query.css("#workspace-history"))
      |> assert_has(Query.css("#workspace-history-run-browser"))
      |> force_live_reconnect()
      |> assert_has(Query.css("body[data-browser-reconnected='true']"))
      |> assert_no_horizontal_overflow()
      |> click(Query.css("#logout-button"))
      |> assert_has(Query.css("#login-page"))
      |> visit("/workspace")
      |> assert_has(Query.css("#login-page"))

    assert Enum.count(BrowserBackend.calls(), &(&1 == {"POST", "/api/v1/run"})) == 2
    refute page_source(session) =~ "browser-provider-secret"
    refute inspect(cookies(session)) =~ "browser-fixture-token-that-never-leaves-the-server"
  end
end
