defmodule HardenLlmUI.StatsComponentsTest do
  use ExUnit.Case, async: true
  import Phoenix.LiveViewTest
  alias HardenLlmUI.{DiagnosticsWire, StatsComponents, TraceProjection}

  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-429
  test "captured usage, cost and cache remain exact, partial or unknown without repricing" do
    f =
      Path.expand("../../../../../test/fixtures/reference/workspace.json", __DIR__)
      |> File.read!()
      |> Jason.decode!()

    original = f["record"]["result"]

    for {status, amount, expected} <- [
          {"exact", 0.125, "$0.1250"},
          {"partial", 0.125, "≥$0.1250"},
          {"unknown", 0, "$—"}
        ] do
      result =
        original
        |> put_in(["accounting", "result", "cost", "status"], status)
        |> put_in(["accounting", "result", "cost", "knownSubtotalUsd"], amount)
        |> put_in(["accounting", "result", "usage", "status"], "complete")
        |> put_in(["accounting", "result", "usage", "inputTokens"], 1234)
        |> put_in(["accounting", "result", "usage", "promptTokens"], 1234)
        |> put_in(["accounting", "result", "usage", "totalTokens"], 1234)
        |> put_in(["cache", "served"], true)
        |> put_in(["cache", "status"], "hit")

      assert DiagnosticsWire.valid?("RecordedRunOutput", result)

      html =
        render_component(&StatsComponents.llm_stats/1,
          id: "captured",
          summary: TraceProjection.summary(result)
        )

      assert html =~ "1,234"
      assert html =~ expected
      assert html =~ "Harden-LLM cache hit"
      assert html =~ "🔁 2"
      assert html =~ "⏱️ 0.20s"
    end

    html =
      render_component(&StatsComponents.llm_stats/1,
        id: "missing",
        summary: TraceProjection.summary(original)
      )

    assert html =~ "Input tokens (unavailable)"
    assert html =~ "📥 —"
    assert html =~ "$—"
  end
end
