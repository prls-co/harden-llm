defmodule PrlsUI.PaginationAssetTest do
  use ExUnit.Case, async: true

  @moduletag :asset

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-082

  test "a fresh asset build includes the reusable pagination utilities" do
    source_path = Path.expand("../../assets/css/app.css", __DIR__)
    compiled_path = Path.expand("../../priv/static/assets/css/app.css", __DIR__)

    source = File.read!(source_path)
    compiled = File.read!(compiled_path)

    assert source =~ ~s(@source "../../lib/prls_ui";)

    for selector <- [
          ".min-w-8",
          ".gap-x-2",
          ~S(.gap-y-1\.5),
          ".w-16",
          ".border-teal-600",
          ~S(.focus\:ring-teal-600)
        ] do
      assert compiled =~ selector, "compiled CSS is missing #{selector}"
    end
  end
end
