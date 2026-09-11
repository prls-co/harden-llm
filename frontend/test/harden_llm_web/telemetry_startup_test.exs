defmodule HardenLlmWeb.TelemetryStartupTest do
  use ExUnit.Case, async: true

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-009
  test "release explicitly boots exporter dependencies before the telemetry SDK" do
    assert get_in(Mix.Project.config(), [:releases, :harden_llm, :applications]) ==
             [opentelemetry_exporter: :permanent, opentelemetry: :permanent]

    dependencies = Enum.map(Mix.Project.config()[:deps], &elem(&1, 0))

    assert Enum.find_index(dependencies, &(&1 == :opentelemetry_exporter)) <
             Enum.find_index(dependencies, &(&1 == :opentelemetry))
  end
end
