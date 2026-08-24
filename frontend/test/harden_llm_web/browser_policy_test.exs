defmodule HardenLlmWeb.BrowserPolicyTest do
  use ExUnit.Case, async: true

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-047 TEST-052 TEST-056

  @repo_root Path.expand("../../..", __DIR__)
  @browser_root Path.join(@repo_root, "frontend/test/browser")
  @manifest_path Path.join(@repo_root, "test/test-tiers.json")
  @ordinary_files [
    "authenticated_workflow_canary_test.exs",
    "widget_canary_test.exs"
  ]

  test "ordinary browser coverage is exactly two focused serialized canaries" do
    browser_files = Path.wildcard(Path.join(@browser_root, "*_test.exs"))
    ordinary_files = Enum.reject(browser_files, &non_ordinary_file?/1)
    ordinary_names = Enum.map(ordinary_files, &Path.basename/1) |> Enum.sort()

    assert ordinary_names == Enum.sort(@ordinary_files),
           "ordinary browser files must be exactly #{@ordinary_files}, got #{inspect(ordinary_names)}"

    for file <- ordinary_files do
      source = File.read!(file)
      assert source =~ "use ExUnit.Case, async: false", "#{file} must serialize browser access"
      assert source =~ "@moduletag :browser", "#{file} must be a browser-only feature"

      assert length(Regex.scan(~r/\bfeature\s+\"/, source)) == 1,
             "#{file} must contain one focused browser feature"

      refute source =~ "desktop size"
      refute source =~ "mobile size"
    end
  end

  test "Compose remains one independent feature" do
    compose = Path.join(@browser_root, "compose_smoke_test.exs")
    assert File.exists?(compose), "the Compose browser boundary must remain present"

    source = File.read!(compose)
    assert source =~ "use ExUnit.Case, async: false"
    assert source =~ "@moduletag compose: true"
    assert length(Regex.scan(~r/\bfeature\s+\"/, source)) == 1
    refute source =~ "@moduletag :browser"
  end

  test "browser tier is pinned and serialized" do
    manifest = @manifest_path |> File.read!() |> Jason.decode!()
    task = Enum.find(manifest["tasks"], &(&1["id"] == "frontend-browser"))
    refute is_nil(task), "frontend-browser task is required"

    assert task["command"] == ["mix", "test", "--only", "browser", "--max-cases", "1"]
    assert task["resourceClass"] == "browser"
    assert task["container"]["image"] == "harden-llm-browser-test:local"
    assert task["container"]["network"] == "host"
    assert task["container"]["dockerSocket"] == true
    assert task["container"]["shmSize"] == "2g"
    assert "frontend/test/browser/widget_canary_test.exs" in task["pathSelectors"]
    assert "frontend/test/browser/authenticated_workflow_canary_test.exs" in task["pathSelectors"]
    assert "frontend/test/support/browser_feature_case.ex" in task["pathSelectors"]
    refute "frontend/test/browser/compose_smoke_test.exs" in task["pathSelectors"]
  end

  defp non_ordinary_file?(path) do
    Path.basename(path) in ["compose_smoke_test.exs", "deployed_canary_test.exs"]
  end
end
