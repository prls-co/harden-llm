defmodule HardenLlmWeb.LlmTraceComponentsTest do
  use ExUnit.Case, async: true

  import Phoenix.LiveViewTest

  alias HardenLlmWeb.LlmTraceComponents

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
          "response" => %{"available" => true, "payload" => nil}
        },
        request_open: true,
        response_open: true,
        details_event: "toggle-details",
        resource_event: "toggle-resource"
      )

    assert html =~ ~s(id="trace-widget")
    assert html =~ "ID: trace-1"
    assert html =~ "Model: model-1"
    assert html =~ "Profile:</strong> Primary"
    assert html =~ "Provider:</strong> openai"
    assert html =~ "API inference type:</strong> responses"
    assert html =~ "Provider base URL:</strong> https://provider.example.test/v1"
    assert html =~ "Success (200)"
    assert html =~ "120ms"
    assert html =~ ~s(href="/traces/trace-1")
    assert html =~ ~s(data-copy-value="curl -X POST /api/v1/run")
    assert html =~ ~s(phx-value-kind="request")
    assert html =~ ~s(phx-value-kind="response")
    assert html =~ ~s(id="trace-widget-request-content")
    assert html =~ "hello"
    assert html =~ ~s(id="trace-widget-response-content")
    assert html =~ ">null</pre>"
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
        resource_loading?: true,
        details_event: "toggle-details",
        resource_event: "toggle-resource"
      )

    assert loading_html =~ "Loading trace JSON"
    assert loading_html =~ ~s(id="loading-trace-resource-loading")

    error_html =
      render_component(&LlmTraceComponents.llm_trace/1,
        id: "error-trace",
        summary: %{"trace_id" => "trace-error", "metrics" => []},
        details: %{"trace_id" => "trace-error", "attempts" => []},
        resource_error: "Trace JSON request failed.",
        details_event: "toggle-details",
        resource_event: "toggle-resource"
      )

    assert error_html =~ "Trace JSON request failed."
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

  test "renders reusable aggregate stats with zeroes and missing values distinctly" do
    html =
      render_component(&LlmTraceComponents.llm_stats_summary/1,
        id: "stats-widget",
        stats: %{
          "success" => 0,
          "known_cost" => "$0.0000",
          "cached_cost" => "$0.0000",
          "cached_count" => 0,
          "average_duration" => nil,
          "max_duration" => 0,
          "over_budget_count" => 0,
          "max_over_budget" => 0
        },
        navigate: "/history"
      )

    assert html =~ "LLM stats summary"
    assert html =~ ~s(href="/history")
    assert html =~ "Cached cost"
    assert html =~ "Cached runs"
    assert html =~ "Max duration ms"
    assert html =~ "Over budget"
    assert html =~ ">0</dd>"
    assert html =~ ">—</dd>"

    atom_key_html =
      render_component(&LlmTraceComponents.llm_stats_summary/1,
        id: "atom-stats-widget",
        stats: %{success: 2, total_tokens: 7, average_duration: 125}
      )

    assert atom_key_html =~ ">2</dd>"
    assert atom_key_html =~ ">7</dd>"
    assert atom_key_html =~ ">125</dd>"
  end
end
