defmodule HardenLlmWeb.ProfileWidgetStyleTest do
  use ExUnit.Case, async: true

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-044 WEB-TEST-072 TEST-044
  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-209

  @css_path Path.expand("../../assets/css/app.css", __DIR__)

  @tag :recovery
  test "selected profile and recovery controls share compact utility styling" do
    css = File.read!(@css_path)

    assert css =~
             "grid-template-columns: minmax(4rem, auto) minmax(0, 1fr) minmax(4.75rem, 5.5rem) var(--ullm-profile-control-height) var(--ullm-profile-control-height);"

    assert length(Regex.scan(~r/\.recovery-policy-numbers\s*\{/, css)) == 1
    assert css =~ ".recovery-policy-categories,"
    refute css =~ ".ullm-escalation"
    refute css =~ ".ullm-backup"

    refute css =~ ".ullm-profile-cache-toggle[aria-pressed="
  end
end
