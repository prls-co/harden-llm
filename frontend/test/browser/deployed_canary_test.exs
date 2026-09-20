defmodule HardenLlmWeb.DeployedCanaryTest do
  use ExUnit.Case, async: false
  use Wallaby.Feature

  @moduletag :deployed

  import HardenLlmWeb.BrowserFeatureCase,
    only: [
      assert_field_value: 3,
      assert_dom_attribute: 4,
      assert_live_socket_connected: 1,
      assert_no_horizontal_overflow: 1,
      commit_combobox: 3,
      javascript_value: 2,
      javascript_value: 3,
      open_fold: 3,
      open_ui_fold: 3,
      scroll_to_selector: 2
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

  feature "deployed widget runs one bounded CPA GPT-5.6 Luna search smoke with History cleanup",
          %{
            session: session,
            email: email,
            password: password,
            nonce: nonce
          } do
    prompt_prefix = "Search the web for the official OpenAI website"

    prompt =
      "#{prompt_prefix} and reply with its domain only. Smoke nonce: #{nonce}"

    session =
      session
      |> resize_window(1_440, 900)
      |> visit("/login")
      |> fill_in(Query.text_field("Email address"), with: email)
      |> fill_in(Query.css("#session_password"), with: password)
      |> click(Query.css("#login-submit"))
      |> assert_has(Query.css("#workspace-page"))
      |> visit("/")
      |> assert_has(Query.css("#workspace-page"))
      |> assert_has(Query.css("#workspace-llm-widget"))
      |> assert_has(Query.css("#new-prompt", text: "Clear Prompt"))
      |> assert_live_socket_connected()
      |> assert_no_horizontal_overflow()
      |> commit_combobox("#run_selectedProfileId", "CPA GPT-5.6 Luna")
      |> assert_field_value("#run_selectedProfileId", "CPA GPT-5.6 Luna")

    session =
      if javascript_value(
           session,
           "return document.querySelector('#workspace-web-search-toggle')?.getAttribute('aria-pressed');"
         ) == "true" do
        session
      else
        click(session, Query.css("#workspace-web-search-toggle"))
      end

    session =
      session
      |> assert_has(Query.css("#workspace-web-search-toggle[aria-pressed='true']"))

    assert javascript_value(
             session,
             "return document.querySelector('#workspace-web-search')?.value;"
           ) == "true"

    session =
      session
      |> open_ui_fold("#model-config-toggle", "#profile-config-fields")
      |> open_fold("#profile-options-toggle", "#profile-options")
      |> open_fold("#profile-retry-toggle", "#profile-retry-repair")
      |> assert_has(Query.css("#profile-escalation-config-toggle", count: 0, visible: :any))

    session =
      session
      |> click(Query.css("#profile-rerun-toggle"))
      |> assert_has(Query.css("#profile-rerun-generation-profile[value='CPA GPT-6 Astra']"))
      |> assert_has(Query.css("#profile-rerun-generation-reasoning:not([disabled])"))

    session =
      session
      |> click(Query.css("#profile-rerun-generation-config-toggle"))
      |> assert_has(Query.css("#profile-rerun-generation-config-json-repair-toggle"))
      |> assert_has(
        Query.css(
          "#profile-rerun-generation-config-json-repair-initial-profile[value='CPA GPT-6 Astra']"
        )
      )
      |> assert_has(
        Query.css(
          "#profile-rerun-generation-config-json-repair-escalation-profile[value='CPA GPT-6 Astra']"
        )
      )
      |> assert_has(
        Query.css(
          "#profile-rerun-generation-config-json-repair-initial-reasoning:not([disabled])"
        )
      )
      |> assert_has(
        Query.css(
          "#profile-rerun-generation-config-json-repair-escalation-reasoning:not([disabled])"
        )
      )

    astra_reasoning_values =
      javascript_value(
        session,
        """
        return Array.from(document.querySelectorAll(
          '#profile-rerun-generation-reasoning option'
        )).map(option => option.value);
        """
      )

    assert astra_reasoning_values == ["lowest", "middle", "highest"]

    session =
      session
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
      |> assert_has(Query.css("#run-result-panel button[id$='-expand']", count: 0))
      |> click(Query.css("#run-result-panel button[id$='-toggle-input']", text: "💬"))
      |> assert_has(Query.css("#run-result-panel .llm-result.is-expanded"))
      |> assert_has(
        Query.css("#run-result-panel .llm-result-toggle[aria-expanded='true']", count: 2)
      )
      |> click(Query.css("#run-result-panel button[id$='-toggle-output']", text: "🤖"))
      |> assert_has(Query.css("#run-result-panel .llm-result:not(.is-expanded)"))
      |> assert_has(
        Query.css("#run-result-panel .llm-result-toggle[aria-expanded='false']", count: 2)
      )

    session =
      if javascript_value(
           session,
           "return document.querySelector('#output-trace-summary')?.getAttribute('aria-expanded');"
         ) == "true" do
        session
      else
        click(session, Query.css("#output-trace-summary"))
      end

    session =
      session
      |> assert_has(Query.css("#output-trace-summary[aria-expanded='true']"))
      |> assert_dom_attribute("#output-trace-summary", "aria-expanded", "true")
      |> click(Query.css("#output-trace-summary"))
      |> assert_has(Query.css("#output-trace-summary[aria-expanded='false']"))
      |> assert_dom_attribute("#output-trace-summary", "aria-expanded", "false")
      |> assert_dom_attribute("#output-trace-content", "hidden", "")
      |> click(Query.css("#output-trace-summary"))
      |> assert_has(Query.css("#output-trace-summary[aria-expanded='true']"))
      |> assert_dom_attribute("#output-trace-summary", "aria-expanded", "true")
      |> assert_dom_attribute("#output-trace-summary", "aria-label", "Trace controls")
      |> assert_dom_attribute("#output-trace-content", "hidden", nil)
      |> assert_has(Query.css("#output-trace-details:not([hidden])"))
      |> assert_has(Query.css(".trace-controls #output-trace-details-toggle", text: "Overview"))
      |> assert_has(Query.css(".trace-controls #output-trace-view-json", text: "Details"))
      |> assert_has(Query.css(".trace-controls #output-trace-copy-curl", text: "cURL"))
      |> assert_has(Query.css(".trace-controls #output-trace-show-request", text: "Request"))
      |> assert_has(Query.css(".trace-controls #output-trace-show-response", text: "Response"))
      |> click(Query.css("#output-trace-view-json"))
      |> assert_has(Query.css("#output-trace-trace-json .json-viewer-node", text: "traceId"))
      |> click(Query.css("#output-trace-show-request"))
      |> assert_has(Query.css("#output-trace-request-content"))
      |> click(Query.css("#output-trace-show-response"))
      |> assert_has(Query.css("#output-trace-response-content"))

    search_facts =
      javascript_value(
        session,
        """
        const request = document.querySelector('#output-trace-request-content')?.textContent || '';
        const response = document.querySelector('#output-trace-response-content')?.textContent || '';
        return {
          requestWebSearch: /["']webSearch["'][^a-zA-Z0-9]+true/.test(request),
          responseSearch: /["']search["'][^a-zA-Z0-9]+/.test(response),
          responseExecuted: /["']executed["'][^a-zA-Z0-9]+true/.test(response)
        };
        """
      )

    assert search_facts["requestWebSearch"], "deployed request did not retain web search intent"

    session =
      session
      |> click(Query.css("#output-trace-summary"))
      |> assert_has(Query.css("#output-trace-content[hidden]", visible: :any))
      |> assert_dom_attribute("#output-trace-content", "hidden", "")
      |> click(Query.css("#output-trace-summary"))
      |> assert_has(Query.css("#output-trace-content:not([hidden])"))
      |> assert_dom_attribute("#output-trace-content", "hidden", nil)
      |> open_ui_fold("#history-fold-toggle", "#workspace-history")
      |> assert_has(Query.css("#workspace-history article", text: nonce))
      |> assert_has(Query.css("#llm-stats-summary", count: 0, visible: :any))
      |> assert_has(Query.css("a[href='/history']", count: 0, visible: :any))
      |> assert_has(Query.css("#trace-dialog", count: 0, visible: :any))
      |> assert_has(Query.css("[aria-label='Inspect in audit history']", count: 0, visible: :any))
      |> click(Query.css("#output-trace-show-request"))

    widget_facts =
      javascript_value(
        session,
        """
        const controls = document.querySelector('.trace-controls');
        const cacheStyle = window.getComputedStyle(document.querySelector('#output-trace-cache-status'));
        const selectedStyle = window.getComputedStyle(document.querySelector('#output-trace-details-toggle'));
        const closedStyle = window.getComputedStyle(document.querySelector('#output-trace-show-request'));
        return {
          controlDisplay: window.getComputedStyle(controls).display,
          directLabels: Array.from(controls.querySelectorAll(':scope > button')).map(node => node.textContent.trim()),
          selectedBackground: selectedStyle.backgroundColor,
          closedBackground: closedStyle.backgroundColor,
          selectedShadow: selectedStyle.boxShadow,
          cacheBorderWidth: cacheStyle.borderTopWidth,
          cacheBorderRadius: cacheStyle.borderRadius,
          cacheBackground: cacheStyle.backgroundColor,
          cachePadding: cacheStyle.padding,
          curl: document.querySelector('#output-trace-copy-curl')?.dataset.copyValue || ''
        };
        """
      )

    assert widget_facts["controlDisplay"] == "flex"

    assert widget_facts["directLabels"] == [
             "Overview",
             "Details",
             "Request",
             "Response",
             "cURL",
             "🔁"
           ]

    assert widget_facts["selectedBackground"] != widget_facts["closedBackground"]
    assert widget_facts["selectedShadow"] != "none"

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

    matching_run_ids =
      javascript_value(
        session,
        """
        return Array.from(document.querySelectorAll('#workspace-history article'))
          .filter(node => node.textContent.includes(arguments[0]))
          .map(node => node.id);
        """,
        [prompt_prefix]
      )

    assert is_list(matching_run_ids) and run_id in matching_run_ids

    session =
      Enum.reduce(matching_run_ids, session, fn matching_run_id, session ->
        session
        |> scroll_to_selector("##{matching_run_id} button[phx-click='delete-history']")
        |> click(Query.css("##{matching_run_id} button[phx-click='delete-history']"))
        |> assert_has(Query.css("##{matching_run_id}", count: 0, visible: :any))
      end)

    assert search_facts["responseSearch"], "deployed response omitted search metadata"
    assert search_facts["responseExecuted"], "deployed response did not report executed search"

    session =
      if javascript_value(
           session,
           "return Boolean(document.querySelector('#run-result-panel .llm-result.is-expanded'));"
         ) do
        session
      else
        send_keys(session, Query.css("#run-result-panel button[id$='-toggle-output']"), [:enter])
      end

    session =
      session
      |> assert_has(Query.css("#run-result-panel .llm-result.is-expanded"))
      |> assert_has(Query.css("#run-result-panel .llm-result-search-details"))
      |> assert_has(
        Query.css("#run-result-panel .llm-result-search-details span", text: "searched")
      )
      |> assert_no_horizontal_overflow()
      |> click(Query.css("#logout-button"))
      |> assert_has(Query.css("#login-page"))

    refute page_source(session) =~ System.fetch_env!("HARDEN_LLM_LOCAL_OPERATOR_PASSWORD")
    refute page_source(session) =~ "CPA GPT-5.6 Luna"
  end
end
