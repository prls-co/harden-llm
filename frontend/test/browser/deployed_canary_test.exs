defmodule HardenLlmWeb.DeployedCanaryTest do
  use ExUnit.Case, async: false
  use Wallaby.Feature

  @moduletag :deployed

  import HardenLlmWeb.BrowserFeatureCase,
    only: [
      assert_field_value: 3,
      assert_live_socket_connected: 1,
      assert_no_horizontal_overflow: 1,
      commit_combobox: 3,
      javascript_value: 2,
      javascript_value: 3,
      open_fold: 3,
      open_ui_fold: 3
    ]

  alias Wallaby.Query

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-048 TEST-056

  setup do
    {:ok,
     %{
       email: System.fetch_env!("HARDEN_LLM_LOCAL_OPERATOR_EMAIL"),
       password: System.fetch_env!("HARDEN_LLM_LOCAL_OPERATOR_PASSWORD"),
       nonce: System.fetch_env!("HARDEN_LLM_SMOKE_NONCE")
     }}
  end

  feature "deployed widget runs one bounded CPA GPT-5.6 Luna smoke with History cleanup", %{
    session: session,
    email: email,
    password: password,
    nonce: nonce
  } do
    prompt = "Tell me one short joke. Smoke nonce: #{nonce}"

    session =
      session
      |> resize_window(1_440, 900)
      |> visit("/login")
      |> fill_in(Query.text_field("Email address"), with: email)
      |> fill_in(Query.css("#session_password"), with: password)
      |> click(Query.css("#login-submit"))
      |> assert_has(Query.css("#workspace-page"))
      |> assert_has(Query.css("#workspace-llm-widget"))
      |> assert_live_socket_connected()
      |> assert_no_horizontal_overflow()
      |> commit_combobox("#run_selectedProfileId", "CPA GPT-5.6 Luna")
      |> assert_field_value("#run_selectedProfileId", "CPA GPT-5.6 Luna")
      |> open_ui_fold("#model-config-toggle", "#profile-config-fields")
      |> open_fold("#profile-options-toggle", "#profile-options")
      |> open_fold("#profile-retry-toggle", "#profile-retry-repair")
      |> open_fold("#profile-escalation-config-toggle", "#profile-escalation-config")
      |> open_fold("#escalation-options-toggle", "#escalation-options")
      |> open_fold("#profile-pricing-toggle", "#profile-pricing")
      |> open_ui_fold("#input-advanced-toggle", "#advanced-input:not(.hidden)")
      |> fill_in(Query.css("#run_userPrompt"), with: prompt)
      |> click(Query.css("#run-submit"))
      |> assert_has(Query.css("#run-output"))

    output =
      javascript_value(
        session,
        "return document.querySelector('#run-output')?.textContent?.trim() || '';"
      )

    assert is_binary(output) and String.trim(output) != ""

    session =
      session
      |> open_ui_fold("#output-details-toggle", "#output-details")
      |> click(Query.css("#show-run-request"))
      |> assert_has(Query.css("#run-request"))
      |> click(Query.css("#show-run-response"))
      |> assert_has(Query.css("#run-response"))
      |> open_ui_fold("#history-fold-toggle", "#workspace-history")

    run_id =
      javascript_value(
        session,
        """
        const article = Array.from(document.querySelectorAll('#workspace-history article'))
          .find(node => node.textContent.includes(arguments[0]));
        return article?.id || '';
        """,
        [nonce]
      )

    assert is_binary(run_id) and String.starts_with?(run_id, "workspace-history-")

    session =
      session
      |> click(Query.css("##{run_id} button[phx-click='delete-history']"))
      |> refute_has(Query.css("##{run_id}"))
      |> assert_no_horizontal_overflow()
      |> click(Query.css("#logout-button"))
      |> assert_has(Query.css("#login-page"))

    refute page_source(session) =~ System.fetch_env!("HARDEN_LLM_LOCAL_OPERATOR_PASSWORD")
    refute page_source(session) =~ "CPA GPT-5.6 Luna"
  end
end
