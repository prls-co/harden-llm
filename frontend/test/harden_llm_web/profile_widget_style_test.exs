defmodule HardenLlmWeb.ProfileWidgetStyleTest do
  use ExUnit.Case, async: true

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-044 TEST-044

  @css_path Path.expand("../../assets/css/app.css", __DIR__)

  test "compact escalation row and cache control retain utility styling" do
    css = File.read!(@css_path)

    assert css =~
             "grid-template-columns: minmax(0, auto) minmax(0, 1fr) minmax(4.75rem, 5.5rem) var(--ullm-profile-control-height) var(--ullm-profile-control-height);"

    refute css =~ ".ullm-profile-cache-toggle[aria-pressed="
  end
end
