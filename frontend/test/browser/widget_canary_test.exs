defmodule HardenLlmWeb.WidgetCanaryTest do
  use ExUnit.Case, async: false
  use Wallaby.Feature

  @moduletag :browser

  import HardenLlmWeb.BrowserFeatureCase

  alias Wallaby.Query

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-047 TEST-047
  # PLAN-HLLM-WIDGET-PARITY-001 TEST-114

  setup {HardenLlmWeb.BrowserFeatureCase, :setup_browser}

  feature "widget preserves native controls, folds, secrets, and embedding boundaries", %{
    session: session
  } do
    session =
      session
      |> resize_window(390, 844)
      |> visit("/login")
      |> fill_in(Query.text_field("Email address"), with: "browser@example.test")
      |> fill_in(Query.css("#session_password"), with: "browser-password-123")
      |> click(Query.css("#login-submit"))
      |> assert_has(Query.css("#workspace-page"))
      |> assert_has(Query.css("#workspace-llm-widget"))
      |> assert_live_socket_connected()
      |> assert_no_horizontal_overflow()
      |> resize_window(1_440, 900)
      |> commit_combobox("#run_selectedProfileId", "WidgetCustomProfile")
      |> assert_field_value("#run_selectedProfileId", "WidgetCustomProfile")
      |> assert_combobox_closed("#run_selectedProfileId")
      |> click(Query.css("#model-config-toggle"))
      |> assert_has(Query.css("#profile-config-fields"))
      |> click(Query.css("#profile-credential-toggle"))
      |> stage_secret(
        "#profile_apiKey",
        "#profile-stage-key",
        "widget-secret-must-never-escape"
      )
      |> assert_text("New key staged for save")

    refute page_source(session) =~ "widget-secret-must-never-escape"

    session =
      session
      |> click(Query.css("#profile-credential-toggle"))
      |> click(Query.css("#profile-clear-staged-key"))
      |> click(Query.css("#profile-credential-toggle"))
      |> click(Query.css("#profile-options-toggle"))
      |> assert_has(Query.css("#profile-options"))
      |> click(Query.css("#profile-retry-toggle"))
      |> assert_has(Query.css("#profile-retry-repair"))
      |> click(Query.css("#profile-escalation-config-toggle"))
      |> assert_has(Query.css("#profile-escalation-config"))
      |> click(Query.css("#escalation-options-toggle"))
      |> assert_has(Query.css("#escalation-options"))
      |> click(Query.css("#profile-pricing-toggle"))
      |> assert_has(Query.css("#profile-pricing"))
      |> scroll_to_selector("#workspace-cache-toggle")
      |> click(Query.css("#workspace-cache-toggle"))
      |> assert_dom_attribute(
        "#workspace-cache-toggle",
        "aria-label",
        "Overwrite cache on next run"
      )
      |> assert_no_horizontal_overflow()
      |> visit("/embed/llm")
      |> assert_has(Query.css("#embedding-page"))
      |> refute_dom_element("#embedding-page nav")
      |> refute_dom_element("#embedding-page [role='tab']")
      |> click(Query.css("#embed-primary-model-config-toggle"))
      |> assert_has(Query.css("#embed-primary-model-options"))
      |> refute_dom_element("#embed-secondary-model-options")
      |> click(Query.css("#embed-secondary-model-config-toggle"))
      |> click(Query.css("#embed-secondary-profile-options-toggle"))
      |> assert_has(Query.css("#embed-secondary-profile-options"))
      |> refute_dom_element("#embed-primary-profile-options")
      |> assert_unique_dom_ids()
      |> assert_no_horizontal_overflow()

    assert page_source(session) =~ "embed-secondary-profile-options"
  end
end
