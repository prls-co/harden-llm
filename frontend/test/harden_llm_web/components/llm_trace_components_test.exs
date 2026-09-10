defmodule HardenLlmWeb.LlmTraceComponentsTest do
  use ExUnit.Case, async: true

  import Phoenix.LiveViewTest

  alias HardenLlmWeb.LlmTraceComponents

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-036

  test "compact identity retains full values and metrics have a separate layout boundary" do
    trace_id = "c955f912-65e1-4ded-84df-52f5c4696c77"
    model = "provider/a-long-model-name-with-a-version"

    document =
      render_component(&LlmTraceComponents.llm_trace/1,
        id: "compact-trace",
        summary: %{
          "trace_id" => trace_id,
          "model_id" => model,
          "metrics" => [%{"value" => "📥 123456", "title" => "Input tokens"}]
        }
      )
      |> LazyHTML.from_document()

    identity = LazyHTML.query(document, ".llm-trace-identity")

    assert LazyHTML.attribute(LazyHTML.query(identity, ".llm-trace-id"), "title") == [
             "ID: #{trace_id}"
           ]

    assert LazyHTML.attribute(LazyHTML.query(identity, ".llm-trace-model"), "title") == [
             "Model: #{model}"
           ]

    assert LazyHTML.text(identity) =~ trace_id
    assert LazyHTML.text(identity) =~ model
    assert LazyHTML.text(LazyHTML.query(document, ".llm-trace-metrics")) =~ "📥 123456"
  end

  test "shared trace controls expose available host-projected artifact links only once" do
    html =
      render_component(&LlmTraceComponents.llm_trace/1,
        id: "artifact-trace",
        summary: %{"trace_id" => "trace-1", "metrics" => []},
        resources: %{
          "artifacts" => [
            %{
              "available" => true,
              "href" => "/traces/trace-1/artifacts/available",
              "label" => "trace · 42 bytes"
            },
            %{"available" => false, "href" => nil, "label" => "trace · unavailable"}
          ]
        }
      )

    links = html |> LazyHTML.from_document() |> LazyHTML.query("#artifact-trace-controls a")
    assert Enum.count(links) == 1
    assert LazyHTML.attribute(links, "href") == ["/traces/trace-1/artifacts/available"]
    assert LazyHTML.attribute(links, "aria-label") == ["Download trace · 42 bytes"]
    assert LazyHTML.text(links) == "📎"
    refute html =~ "trace · unavailable"
  end

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
