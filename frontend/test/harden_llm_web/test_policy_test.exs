defmodule HardenLlmWeb.TestPolicyTest do
  use ExUnit.Case, async: true

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-045 TEST-045

  @frontend_root Path.expand("../..", __DIR__)
  @repo_root Path.expand("../../..", __DIR__)
  @test_root Path.join(@frontend_root, "test")
  @manifest_path Path.join(@repo_root, "test/test-tiers.json")

  test "deterministic frontend modules use private async ownership by default" do
    manifest = @manifest_path |> File.read!() |> Jason.decode!()

    exceptions =
      manifest
      |> Map.fetch!("frontendSerialExceptions")
      |> Map.new(fn entry -> {Map.fetch!(entry, "path"), Map.fetch!(entry, "resource")} end)

    assert map_size(exceptions) <= 2,
           "deterministic frontend policy allows at most two serial exceptions: #{inspect(exceptions)}"

    files =
      @test_root
      |> Path.join("harden_llm_web/**/*_test.exs")
      |> Path.wildcard()
      |> Enum.reject(&browser_or_compose_or_policy?/1)

    assert files != [], "deterministic frontend inventory must not be empty"

    Enum.each(files, fn path ->
      relative = Path.relative_to(path, @repo_root)
      source = File.read!(path)

      case Regex.run(
             ~r/use\s+(?:HardenLlmWeb\.ConnCase|ExUnit\.Case),\s+async:\s+(true|false)/,
             source
           ) do
        [_, "true"] ->
          :ok

        [_, "false"] ->
          assert Map.has_key?(exceptions, relative),
                 "#{relative} is async: false but is not a documented serial exception"

        nil ->
          flunk("#{relative} does not declare async: true/false in its ExUnit case")
      end

      if Regex.match?(~r/Req\.Test\.set_req_test_to_shared\s*\(/, source) do
        assert Map.has_key?(exceptions, relative),
               "#{relative} uses shared Req ownership outside the documented exceptions"
      end
    end)

    Enum.each(exceptions, fn {relative, resource} ->
      path = Path.expand(relative, @repo_root)
      assert File.exists?(path), "serial exception #{relative} (#{resource}) is missing"

      assert File.read!(path) =~ "async: false",
             "serial exception #{relative} must declare async: false"

      assert is_binary(resource) and byte_size(resource) > 0,
             "serial exception #{relative} must name its global resource"
    end)

    conn_case = File.read!(Path.join(@test_root, "support/conn_case.ex"))

    assert conn_case =~ "set_req_test_from_context",
           "ConnCase must configure private/shared Req ownership from ExUnit context"
  end

  defp browser_or_compose?(path) do
    String.contains?(path, "/browser/") or String.ends_with?(path, "compose_smoke_test.exs")
  end

  defp browser_or_compose_or_policy?(path) do
    browser_or_compose?(path) or String.ends_with?(path, "test_policy_test.exs")
  end
end
