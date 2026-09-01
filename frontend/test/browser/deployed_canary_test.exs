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
  # PLAN-HLLM-WIDGET-PARITY-001 TEST-118

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
      |> open_ui_fold("#output-trace-details-toggle", "#output-trace-details")
      |> assert_has(Query.css(".trace-controls #output-trace-details-toggle", text: "Hide"))
      |> assert_has(Query.css(".trace-controls a", text: "View JSON Trace"))
      |> assert_has(Query.css(".trace-controls #output-trace-copy-curl", text: "Copy cURL"))
      |> assert_has(Query.css(".trace-controls #output-trace-show-request", text: "Show Request"))
      |> assert_has(
        Query.css(".trace-controls #output-trace-show-response", text: "Show Response")
      )
      |> click(Query.css("#output-trace-show-request"))
      |> assert_has(Query.css("#output-trace-request-content"))
      |> click(Query.css("#output-trace-show-response"))
      |> assert_has(Query.css("#output-trace-response-content"))
      |> open_ui_fold("#history-fold-toggle", "#workspace-history")

    widget_facts =
      javascript_value(
        session,
        """
        const controls = document.querySelector('.trace-controls');
        const cacheStyle = window.getComputedStyle(document.querySelector('#output-trace-cache-status'));
        return {
          controlDisplay: window.getComputedStyle(controls).display,
          directLabels: Array.from(controls.children).map(node => node.textContent.trim()),
          cacheBorderWidth: cacheStyle.borderTopWidth,
          cacheBorderRadius: cacheStyle.borderRadius,
          cacheBackground: cacheStyle.backgroundColor,
          cachePadding: cacheStyle.padding,
          curl: document.querySelector('#output-trace-copy-curl')?.dataset.copyValue || ''
        };
        """
      )

    assert widget_facts["controlDisplay"] == "flex"

    assert Enum.take(widget_facts["directLabels"], 5) == [
             "Hide",
             "View JSON Trace",
             "Copy cURL",
             "Hide Request",
             "Hide Response"
           ]

    assert widget_facts["cacheBorderWidth"] == "0px"
    assert widget_facts["cacheBorderRadius"] == "0px"
    assert widget_facts["cacheBackground"] == "rgba(0, 0, 0, 0)"
    assert widget_facts["cachePadding"] == "0px"

    public_api_origin = System.fetch_env!("HARDEN_LLM_PUBLIC_API_BASE_URL")

    assert String.starts_with?(
             widget_facts["curl"],
             "curl --fail-with-body --request POST '#{public_api_origin}/api/v1/run'"
           )

    assert widget_facts["curl"] =~ ~s(authorization: Bearer ${HARDEN_LLM_TOKEN})
    refute widget_facts["curl"] =~ password

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
