defmodule HardenLlmWeb.LlmTraceComponentsTest do
  use ExUnit.Case, async: true

  import Phoenix.LiveViewTest

  alias HardenLlmWeb.LlmTraceComponents
  alias HardenLlmWeb.LlmStatsComponents
  alias Phoenix.LiveView.AsyncResult

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-036

  test "renders the reusable trace summary, details, controls, and exact resources" do
    html =
      render_component(&LlmTraceComponents.llm_trace/1,
        id: "trace-widget",
        summary: %{
          "status_icon" => "✅",
          "trace_id" => "trace-1",
          "model_id" => "model-1",
          "metrics" => [%{"value" => "🔁 1", "title" => "Attempts"}]
        },
        details: %{
          "trace_id" => "trace-1",
          "run_id" => "run-1",
          "profile_id" => "Primary",
          "model_id" => "model-1",
          "provider" => "openai",
          "api_inference_type" => "responses",
          "provider_base_url" => "https://provider.example.test/v1",
          "status" => "Success (200)",
          "used_repair" => false,
          "attempts" => [
            %{
              "attempt" => 1,
              "category" => "success",
              "status_code" => 200,
              "duration_ms" => 120
            }
          ]
        },
        resources: %{
          "trace_url" => "/traces/trace-1",
          "curl" => "curl -X POST /api/v1/run",
          "request" => %{"available" => true, "payload" => %{"userPrompt" => "hello"}},
          "response" => %{"available" => true, "payload" => nil},
          "artifacts" => [
            %{"available" => false, "href" => nil, "label" => "trace · unavailable"}
          ]
        },
        request_open: true,
        response_open: true,
        trace_open: true,
        trace_data: %{"traceId" => "trace-1", "observations" => [%{"ok" => true}]},
        details_event: "toggle-details",
        controls_event: "toggle-controls",
        resource_event: "toggle-resource"
      )

    assert html =~ ~s(id="trace-widget")

    assert html =~ ~s(<button id="trace-widget-summary" type="button" class="llm-trace-summary")
    assert html =~ ~s(phx-click="toggle-controls")
    assert html =~ ~s(aria-controls="trace-widget-content")
    assert html =~ ~s(aria-expanded="true")
    assert html =~ ~s(aria-label="Trace controls")
    assert html =~ ~s(id="trace-widget-content")
    assert html =~ ~s(id="trace-widget-details-toggle")
    assert html =~ ~s(aria-label="Execution overview")
    assert html =~ ~s(aria-controls="trace-widget-details")
    assert html =~ ~s(id="trace-widget-controls")
    assert html =~ ~s(id="trace-widget-view-json")
    assert html =~ ~s(aria-label="Full trace details")
    assert html =~ ~s(>Overview</button>)
    assert html =~ ~s(>Details</button>)
    assert html =~ ~s(>cURL</button>)
    assert html =~ ~s(>Request</button>)
    assert html =~ ~s(>Response</button>)
    assert html =~ ~s(phx-value-kind="trace")
    assert html =~ "ID: trace-1"
    assert html =~ "Model: model-1"
    assert html =~ "&quot;profile_id&quot;:"
    assert html =~ "&quot;Primary&quot;"
    assert html =~ "&quot;provider&quot;:"
    assert html =~ "&quot;openai&quot;"
    assert html =~ "&quot;api_inference_type&quot;:"
    assert html =~ "&quot;responses&quot;"
    assert html =~ "&quot;provider_base_url&quot;:"
    assert html =~ "https://provider.example.test/v1"
    assert html =~ "Success (200)"
    assert html =~ ">120</span>"
    refute html =~ ~s(href="/traces/trace-1")
    refute html =~ ~s(target="_blank")
    assert html =~ ~s(data-copy-value="curl -X POST /api/v1/run")
    assert html =~ ~s(phx-value-kind="request")
    assert html =~ ~s(phx-value-kind="response")
    assert html =~ ~s(id="trace-widget-request-content")
    assert html =~ "hello"
    assert html =~ ~s(id="trace-widget-response-content")
    assert html =~ ~s(class="json-viewer-node")
    assert html =~ "traceId"
    assert html =~ "observations"
    assert html =~ "null"
    refute html =~ "trace · unavailable"

    assert html
           |> LazyHTML.from_document()
           |> LazyHTML.query("#trace-widget-controls > *")
           |> Enum.map(&LazyHTML.text/1)
           |> Enum.map(&String.trim/1) == ["Overview", "Details", "Request", "Response", "cURL"]

    refute html =~ "View JSON Trace"
    refute html =~ "Show trace controls"
    refute html =~ "Hide trace controls"
    refute html =~ "Show Request"
    refute html =~ "Hide Request"
    refute html =~ "Show Response"
    refute html =~ "Hide Response"
    refute html =~ ~s(href="")

    collapsed_html =
      render_component(&LlmTraceComponents.llm_trace/1,
        id: "collapsed-trace",
        summary: %{"trace_id" => "trace-collapsed", "metrics" => []},
        controls_open: false,
        controls_event: "toggle-controls",
        details_event: "toggle-details",
        resource_event: "toggle-resource"
      )

    assert collapsed_html =~ ~s(id="collapsed-trace-content")
    assert collapsed_html =~ ~s(id="collapsed-trace-controls")
    assert collapsed_html =~ ~s( hidden>)
    assert collapsed_html =~ ~s(aria-expanded="false")
  end

  test "renders explicit unavailable, loading, and error resource states" do
    unavailable_html =
      render_component(&LlmTraceComponents.llm_trace/1,
        id: "missing-trace",
        summary: %{"trace_id" => "trace-missing", "metrics" => []},
        details: %{"trace_id" => "trace-missing", "attempts" => []},
        resources: %{
          "request" => %{
            "available" => false,
            "message" => "Request payload is not available for this trace."
          },
          "response" => %{"available" => false}
        },
        details_event: "toggle-details",
        resource_event: "toggle-resource"
      )

    assert unavailable_html =~ "disabled>"
    refute unavailable_html =~ ~s(href="/traces/")

    loading_html =
      render_component(&LlmTraceComponents.llm_trace/1,
        id: "loading-trace",
        summary: %{"trace_id" => "trace-loading", "metrics" => []},
        details: %{"trace_id" => "trace-loading", "attempts" => []},
        resources: %{
          "request" => %{"available" => true, "payload" => %{}},
          "response" => %{"available" => true, "payload" => %{}}
        },
        trace_open: true,
        trace_loading?: true,
        details_event: "toggle-details",
        resource_event: "toggle-resource"
      )

    assert loading_html =~ "Loading trace JSON"
    assert loading_html =~ ~s(id="loading-trace-trace-loading")

    error_html =
      render_component(&LlmTraceComponents.llm_trace/1,
        id: "error-trace",
        summary: %{"trace_id" => "trace-error", "metrics" => []},
        details: %{"trace_id" => "trace-error", "attempts" => []},
        trace_open: true,
        trace_error: "Trace JSON request failed.",
        details_event: "toggle-details",
        resource_event: "toggle-resource"
      )

    assert error_html =~ "Trace JSON request failed."
    assert error_html =~ ~s(id="error-trace-trace-error")
    assert error_html =~ ~s(role="alert")

    payload_on_unavailable_resource_html =
      render_component(&LlmTraceComponents.llm_trace/1,
        id: "inconsistent-trace",
        summary: %{"trace_id" => "trace-inconsistent", "metrics" => []},
        details: %{"trace_id" => "trace-inconsistent", "attempts" => []},
        resources: %{
          "request" => %{
            "available" => false,
            "payload" => %{"must_not" => "render"}
          }
        },
        request_open: true,
        details_event: "toggle-details",
        resource_event: "toggle-resource"
      )

    assert payload_on_unavailable_resource_html =~
             "Request payload is not available for this trace."

    refute payload_on_unavailable_resource_html =~ "must_not"
  end

  test "keeps request errors local and preserves JSON-looking string payloads" do
    html =
      render_component(&LlmTraceComponents.llm_trace/1,
        id: "independent-trace",
        summary: %{"trace_id" => "trace-independent", "metrics" => []},
        resources: %{
          "request" => %{"available" => true, "payload" => "42"},
          "response" => %{"available" => true, "payload" => %{"ok" => true}}
        },
        request_open: true,
        response_open: true,
        trace_open: true,
        trace_error: "Trace JSON request failed.",
        details_event: "toggle-details",
        resource_event: "toggle-resource"
      )

    assert html =~ "Trace JSON request failed."
    assert html =~ ~s(id="independent-trace-request-content")
    assert html =~ ~s(&quot;42&quot;)
    assert html =~ ~s(&quot;ok&quot;:)
    refute html =~ ~s(>42</span>)
  end

  test "renders concise aggregate cost disclosures and scalar stats" do
    html =
      render_component(&LlmStatsComponents.llm_stats_summary/1,
        id: "stats-widget",
        stats:
          AsyncResult.ok(%{
            success: 0,
            result_cost: %{
              text: "⚠️ $0.0000",
              state: :partial,
              detail:
                "Known subtotal; not total cost. 1 exact · 0 partial · 1 unknown · 0 unavailable",
              aria_label: "Partial result cost, $0.0000 known; show details"
            },
            cached_cost: %{
              text: "—",
              state: :unavailable,
              detail:
                "No cost observation was available. 0 exact · 0 partial · 0 unknown · 0 unavailable",
              aria_label: "Cached cost unavailable; show details"
            },
            cached_count: 0,
            average_duration: nil,
            max_duration: 0,
            over_budget_count: 0,
            max_over_budget: 0
          }),
        navigate: "/history"
      )

    assert html =~ "LLM stats summary"
    assert html =~ ~s(href="/history")
    assert html =~ ~s(aria-busy="false")
    assert html =~ ~s(id="stats-widget-result_cost-details")
    assert html =~ ~s(aria-label="Partial result cost, $0.0000 known; show details")
    assert html =~ "Known subtotal; not total cost."
    assert html =~ ~s(id="stats-widget-cached_cost-details")
    assert html =~ "Cached cost unavailable; show details"
    refute html =~ "Result cost coverage"
    refute html =~ "Cached cost coverage"
    assert html =~ "Cached runs"
    assert html =~ "Max duration ms"
    assert html =~ "Over budget"
    assert html =~ ">0</dd>"
    assert html =~ ">—</dd>"

    atom_key_html =
      render_component(&LlmStatsComponents.llm_stats_summary/1,
        id: "atom-stats-widget",
        stats: AsyncResult.ok(%{success: 2, result_total_tokens: 7, average_duration: 125})
      )

    refute atom_key_html =~ "<details"
    assert atom_key_html =~ ">2</dd>"
    assert atom_key_html =~ ">7</dd>"
    assert atom_key_html =~ ">125</dd>"
  end

  test "marks aggregate stats busy while refreshing" do
    html =
      render_component(&LlmStatsComponents.llm_stats_summary/1,
        id: "refreshing-stats",
        stats: AsyncResult.ok(%{success: 1}) |> AsyncResult.loading()
      )

    assert html =~ ~s(aria-busy="true")
    assert html =~ "Loading aggregate diagnostics"
    assert html =~ ">1</dd>"
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-061
  test "renders loading, unavailable, and stale aggregate resource states without zero defaults" do
    loading =
      render_component(&LlmStatsComponents.llm_stats_summary/1,
        id: "loading-stats",
        stats: AsyncResult.loading()
      )

    assert loading =~ "Loading aggregate diagnostics"
    refute loading =~ "Result known subtotal"

    unavailable =
      render_component(&LlmStatsComponents.llm_stats_summary/1,
        id: "failed-stats",
        stats: AsyncResult.loading() |> AsyncResult.failed(:unavailable)
      )

    assert unavailable =~ "temporarily unavailable"
    refute unavailable =~ "$0.0000"

    stale =
      AsyncResult.ok(%{
        runs: 2,
        result_cost: %{
          text: "$0.0042",
          state: :exact,
          detail: "Known result subtotal is exact.",
          aria_label: "Exact result cost, $0.0042; show details"
        }
      })
      |> AsyncResult.failed(:unavailable)

    stale_html =
      render_component(&LlmStatsComponents.llm_stats_summary/1,
        id: "stale-stats",
        stats: stale,
        updated_at: ~U[2026-09-02 10:15:30Z],
        refresh_event: "refresh-stats"
      )

    assert stale_html =~ "Showing the last successful snapshot from 2026-09-02 10:15:30 UTC"
    assert stale_html =~ "$0.0042"
    assert stale_html =~ ~s(id="stale-stats-refresh")
    assert stale_html =~ ~s(phx-click="refresh-stats")
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-062
  test "derives every DOM id from the component instance" do
    html =
      for id <- ["trace-a", "trace-b"] do
        render_component(&LlmTraceComponents.llm_trace/1,
          id: id,
          summary: %{
            "trace_id" => id,
            "metrics" => [%{"key" => "cache-status", "value" => "💾"}]
          },
          details: %{"trace_id" => id, "attempts" => []},
          details_event: "toggle-details",
          resource_event: "toggle-resource"
        )
      end
      |> Enum.join()

    for id <- ["trace-a", "trace-b"] do
      assert html =~ ~s(id="#{id}-cache-status")
      assert html =~ ~s(id="#{id}-details-toggle")
      assert html =~ ~s(id="#{id}-copy-curl")
    end
  end
end
