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
      |> visit("/")
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
      |> visit("/")
      |> assert_has(Query.css("#backend-status", text: "Backend ready"))
      |> choose_option("#run_selectedProfileId", "BrowserProfile")
      |> click(Query.css("#input-advanced-toggle"))
      |> assert_has(Query.css("#advanced-input"))
      |> fill_in(
        Query.css("#run_schema"),
        with:
          ~s({"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false})
      )
      |> assert_has(Query.css("#schema-status", text: "Schema valid."))
      |> click(Query.css("#clear-schema"))
      |> assert_has(Query.css("#run-submit:not([disabled])"))
      |> fill_in(Query.css("#run_userPrompt"), with: "run the browser canary")
      |> assert_has(Query.css("#run-submit:not([disabled])"))
      |> click(Query.css("#run-submit"))
      |> assert_has(Query.css("#run-output", text: "deterministic browser output"))
      |> assert_has(Query.css("#run-result-panel", text: "trace-browser"))
      |> assert_compact_stats_layout("#output-trace-summary")
      |> assert_has(Query.css("#output-trace-cache-status[data-cache-status='miss']", text: "💾"))
      |> assert_has(Query.css("#run-submit:not([disabled])"))
      |> click(Query.css("#run-submit"))
      |> assert_has(Query.css("#output-trace-cache-status[data-cache-status='hit']", text: "💾"))
      |> assert_dom_attribute("#workspace-cache-toggle", "data-cache-mode", "cache")
      |> scroll_to_selector("#workspace-cache-toggle")
      |> click(Query.css("#workspace-cache-toggle"))
      |> assert_has(Query.css("#workspace-cache-toggle[data-cache-mode='refresh']"))
      |> assert_dom_attribute(
        "#workspace-cache-toggle",
        "aria-label",
        "Overwrite cache on next run"
      )
      |> assert_field_value("#workspace-cache", "refresh")
      |> assert_has(Query.css("#run-submit:not([disabled])"))
      |> click(Query.css("#run-submit"))
      |> assert_has(
        Query.css("#output-trace-cache-status[data-cache-status='refresh']", text: "💾")
      )
      |> assert_has(Query.css("#output-trace-details"))
      |> click(Query.css("#output-trace-details-toggle"))
      |> assert_has(Query.css("#output-trace-details[hidden]", visible: :any))
      |> assert_dom_attribute("#output-trace-summary", "aria-expanded", "true")
      |> click(Query.css("#output-trace-summary"))
      |> assert_dom_attribute("#output-trace-summary", "aria-expanded", "false")
      |> assert_dom_attribute("#output-trace-content", "hidden", "")
      |> click(Query.css("#output-trace-summary"))
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
      |> refute_has(Query.css("#output-trace-artifact-0"))
      |> click(Query.css("#output-trace-show-request"))
      |> assert_has(Query.css("#output-trace-request-content"))
      |> click(Query.css("#output-trace-show-response"))
      |> assert_has(Query.css("#output-trace-response-content"))
      |> click(Query.css("#output-trace-summary"))
      |> assert_dom_attribute("#output-trace-content", "hidden", "")
      |> click(Query.css("#output-trace-summary"))
      |> assert_dom_attribute("#output-trace-content", "hidden", nil)
      |> click(Query.css("#output-trace-show-request"))

    nested_json_id =
      javascript_value(
        session,
        """
        const node = Array.from(document.querySelectorAll('#output-trace-trace-json .json-viewer-node'))
          .find(element => !element.open);
        node?.querySelector('summary')?.click();
        return node?.id || null;
        """
      )

    if is_binary(nested_json_id) do
      session =
        session
        |> fill_in(Query.css("#run_userPrompt"), with: "trigger an unrelated draft patch")

      assert javascript_value(
               session,
               "return document.getElementById(arguments[0])?.open === true;",
               [nested_json_id]
             )
    end

    session =
      session
      |> click(Query.css("#run-result-panel button[id$='-expand']"))
      |> assert_has(Query.css("#run-result-panel .llm-result.is-expanded"))
      |> click(Query.css("#output-trace-summary"))
      |> assert_has(Query.css("#output-trace-content[hidden]", visible: :any))
      |> click(Query.css("#output-trace-summary"))
      |> assert_has(Query.css("#output-trace-content:not([hidden])"))

    result_facts =
      javascript_value(session, """
        const card = document.querySelector('#run-result-panel .llm-result');
        return {
          rows: Array.from(card.children).map(node => node.className),
          expanded: card.classList.contains('is-expanded'),
          wrapping: Array.from(card.querySelectorAll('.llm-result-text')).map(node => getComputedStyle(node).whiteSpace)
        };
      """)

    assert result_facts["rows"] == ["llm-result-row", "llm-result-row", "llm-result-stats"]
    assert result_facts["expanded"]
    assert result_facts["wrapping"] == ["pre-wrap", "pre-wrap"]

    session =
      session
      |> scroll_to_selector("#run-result-panel button[id$='-expand']")
      |> Wallaby.Browser.take_screenshot(name: "result-card-expanded")

    assert javascript_value(session, """
             const button = document.querySelector('#run-result-panel button[id$="-expand"]');
             const rect = button.getBoundingClientRect();
             return button.contains(document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2));
           """)

    session =
      session
      |> click(Query.css("#run-result-panel button[id$='-expand']"))
      |> assert_has(Query.css("#run-result-panel .llm-result:not(.is-expanded)"))

    widget_facts =
      javascript_value(
        session,
        """
        const controls = document.querySelector('.trace-controls');
        const cache = document.querySelector('#output-trace-cache-status');
        const cacheStyle = window.getComputedStyle(cache);
        const selectedAction = document.querySelector('#output-trace-details-toggle');
        const closedAction = document.querySelector('#output-trace-show-request');
        const selectedStyle = window.getComputedStyle(selectedAction);
        const closedStyle = window.getComputedStyle(closedAction);
        return {
          controlDisplay: window.getComputedStyle(controls).display,
          directLabels: Array.from(controls.querySelectorAll(':scope > button')).map(node => node.textContent.trim()),
          selectedBackground: selectedStyle.backgroundColor,
          closedBackground: closedStyle.backgroundColor,
          selectedShadow: selectedStyle.boxShadow,
          cacheBorderWidth: cacheStyle.borderTopWidth,
          cacheBorderRadius: cacheStyle.borderRadius,
          cacheBackground: cacheStyle.backgroundColor,
          cachePadding: cacheStyle.padding
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

    curl =
      javascript_value(
        session,
        "return document.querySelector('#output-trace-copy-curl')?.dataset.copyValue || '';"
      )

    assert String.starts_with?(
             curl,
             "curl --fail-with-body --request POST 'https://api.example.test/api/v1/run'"
           )

    assert curl =~ ~s(authorization: Bearer ${HARDEN_LLM_TOKEN})
    refute curl =~ "browser-fixture-token-that-never-leaves-the-server"

    session =
      session
      |> install_clipboard_stub()
      |> click(Query.css("#run-result-panel button[aria-label='Copy input']"))
      |> assert_has(Query.css("#run-result-panel button[aria-label='Copy input']", text: "✅"))

    assert javascript_value(session, "return window.__hardenCopiedText;") ==
             "run the browser canary"

    session =
      session
      |> click(Query.css("#output-trace-copy-curl"))
      |> assert_has(Query.css("#output-trace-copy-curl", text: "Copied"))
      |> click(Query.css("#copy-run-output"))
      |> assert_has(Query.css("#copy-run-output", text: "✅"))

    assert javascript_value(session, "return window.__hardenCopiedText;") ==
             "deterministic browser output"

    session =
      session
      |> assert_has(Query.css("#llm-stats-summary", count: 0, visible: :any))
      |> assert_has(Query.css("a[href='/history']", count: 0, visible: :any))
      |> assert_has(Query.css("#output-trace-trace-json .json-viewer", text: "observations"))
      |> assert_has(Query.css("#trace-dialog", count: 0, visible: :any))

    BrowserBackend.fail_next_run()

    session =
      session
      |> visit("/")
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
      |> assert_has(Query.css("#workspace-history-run-browser.llm-result .llm-result-stats"))
      |> assert_compact_stats_layout("#history-trace-run-browser-summary")
      |> assert_has(Query.css("[aria-label='Inspect in audit history']", count: 0, visible: :any))
      |> scroll_to_selector("#history-trace-run-browser-summary")
      |> click(Query.css("#history-trace-run-browser-summary"))
      |> assert_has(Query.css("#history-trace-run-browser-details:not([hidden])"))
      |> click(Query.css("#history-trace-run-browser-view-json"))
      |> assert_has(
        Query.css("#history-trace-run-browser-trace-json .json-viewer", text: "observations")
      )
      |> assert_has(
        Query.css(
          "#history-trace-run-browser-controls a[href='/traces/trace-browser/artifacts/artifact-browser']"
        )
      )
      |> scroll_to_selector("#history-trace-run-browser-summary")
      |> click(Query.css("#history-trace-run-browser-summary"))
      |> assert_has(Query.css("#history-trace-run-browser-content[hidden]", visible: :any))
      |> click(Query.css("#workspace-history-run-browser-expand"))
      |> assert_has(Query.css("#workspace-history-run-browser.is-expanded"))
      |> scroll_to_selector("#workspace-history-run-browser")
      |> Wallaby.Browser.take_screenshot(name: "history-result-card")
      |> force_live_reconnect()
      |> assert_has(Query.css("body[data-browser-reconnected='true']"))
      |> scroll_to_selector("#workspace-history-run-browser button[phx-click='delete-history']")
      |> click(Query.css("#workspace-history-run-browser button[phx-click='delete-history']"))
      |> assert_has(Query.css("#workspace-history-run-browser", count: 0, visible: :any))
      |> assert_no_horizontal_overflow()
      |> click(Query.css("#logout-button"))
      |> assert_has(Query.css("#login-page"))
      |> visit("/")
      |> assert_has(Query.css("#login-page"))

    assert Enum.count(BrowserBackend.calls(), &(&1 == {"POST", "/api/v1/run"})) == 4
    refute {"GET", "/api/v1/stats"} in BrowserBackend.calls()

    assert Enum.map(BrowserBackend.run_requests(), & &1["cacheMode"]) == [
             "cache",
             "cache",
             "refresh",
             "refresh"
           ]

    refute page_source(session) =~ "browser-provider-secret"
    refute inspect(cookies(session)) =~ "browser-fixture-token-that-never-leaves-the-server"
  end

  # Real layout boundary only; markup/full-value invariants live in WEB-TEST-036.
  defp assert_compact_stats_layout(session, selector) do
    measurements =
      javascript_value(
        session,
        """
        const source = document.querySelector(arguments[0]);
        return [900, 700, 320].map(width => {
          const host = document.createElement('div');
          host.style.cssText = `position:fixed;left:0;top:0;width:${width}px;visibility:hidden`;
          const bar = source.cloneNode(true);
          bar.removeAttribute('id');
          bar.querySelectorAll('[id]').forEach(node => node.removeAttribute('id'));
          bar.querySelector('.llm-trace-id').textContent = 'ID: c955f912-65e1-4ded-84df-52f5c4696c77';
          bar.querySelector('.llm-trace-model').textContent = 'Model: provider/a-very-long-model-name-and-version';
          host.append(bar);
          source.parentElement.append(host);
          try {
            const identity = bar.querySelector('.llm-trace-identity');
            const metrics = bar.querySelector('.llm-trace-metrics');
            const lineHeight = parseFloat(getComputedStyle(bar).lineHeight);
            return {
              width, font: getComputedStyle(bar).fontSize,
              overflow: bar.scrollWidth > bar.clientWidth,
              height: bar.getBoundingClientRect().height,
              singleRow: Math.abs(identity.getBoundingClientRect().top - metrics.getBoundingClientRect().top) < 2,
              wholeMetrics: Array.from(metrics.children).every(metric => metric.getBoundingClientRect().height <= lineHeight + 1),
              wholeIdentity: Array.from(identity.children).every(item => item.getBoundingClientRect().height <= lineHeight + 1)
            };
          } finally { host.remove(); }
        });
        """,
        [selector]
      )

    for measurement <- measurements do
      assert measurement["font"] == "14px", inspect(measurement)
      refute measurement["overflow"], inspect(measurement)
      assert measurement["wholeMetrics"], inspect(measurement)
      assert measurement["wholeIdentity"], inspect(measurement)

      if measurement["width"] >= 700 do
        assert measurement["singleRow"], inspect(measurement)
        assert measurement["height"] < 40, inspect(measurement)
      end
    end

    session
  end
end
