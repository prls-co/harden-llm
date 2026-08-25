defmodule HardenLlmWeb.BoundaryTest do
  use ExUnit.Case, async: true

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-001
  # PLAN-HLLM-WIDGET-PARITY-001 TEST-115

  @forbidden_dependencies ~w(ecto ecto_sql postgrex sqlite_ecto2 firebase garage aws temporal oban broadway react)

  test "runtime and package pins match the frontend specification" do
    assert System.version() == "1.20.2"
    assert String.starts_with?(System.otp_release(), "28")

    lock = Mix.Dep.Lock.read()

    for {name, version} <- %{
          phoenix: "1.8.9",
          phoenix_live_view: "1.2.9",
          req: "0.6.1",
          prom_ex: "1.12.0",
          logger_json: "7.0.4",
          opentelemetry_logger_metadata: "0.2.0"
        } do
      assert {:hex, ^name, ^version, _checksum, _managers, _dependencies, "hexpm",
              _outer_checksum} =
               Map.fetch!(lock, name)
    end
  end

  test "patched LiveView rejects browser-normalized unsafe URL schemes" do
    assert_raise ArgumentError, ~r/unsupported scheme/, fn ->
      Phoenix.LiveView.Utils.valid_destination!(" javascript:alert(1)", "<.link>")
    end
  end

  test "frontend has no persistence, provider, storage, Firebase, or React dependency" do
    direct_names =
      Mix.Project.config()[:deps] |> Enum.map(&elem(&1, 0)) |> Enum.map(&Atom.to_string/1)

    for forbidden <- @forbidden_dependencies do
      refute forbidden in direct_names
    end

    source = source_files()
    refute source =~ "Ecto."
    refute source =~ "Firebase"
    refute source =~ "Garage"
    refute source =~ "React"
    refute source =~ "Langfuse"
  end

  test "only HardenAPI invokes Req" do
    req_users =
      Path.wildcard("lib/**/*.ex")
      |> Enum.filter(&(File.read!(&1) =~ ~r/\bReq\./))

    assert req_users == ["lib/harden_llm_web/harden_api.ex"]
  end

  test "production runtime consumes the specified frontend environment contract" do
    runtime = File.read!("config/runtime.exs")
    compose = File.read!("../deploy/frontend/compose.frontend.yml")

    for name <- ~w(
      HARDEN_LLM_WEB_OTEL_EXPORTER_OTLP_ENDPOINT
      HARDEN_LLM_WEB_SERVICE_NAME
      HARDEN_LLM_WEB_ENVIRONMENT
      HARDEN_LLM_WEB_RELEASE
    ) do
      assert runtime =~ name
      assert compose =~ name
    end
  end

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-046 WEB-TEST-051 TEST-046 TEST-051
  test "client decisions are imported from one dependency-free pure module" do
    app = File.read!("assets/js/app.js")
    core = File.read!("assets/js/client_core.mjs")

    assert app =~ ~s(from "./client_core.mjs")

    for function <- ~w(
      normalizeSearch visibleOptionIndices emptyStateVisible highlightIndex commitValue
      escapeValue focusValue blurValue isSubmitShortcut schemaPendingState
    ) do
      assert app =~ function
      assert core =~ "export function #{function}"
    end

    for browser_effect <- ~w(
      document window navigator setTimeout addEventListener removeEventListener
      dispatchEvent fetch
    ) do
      refute core =~ ~r/\b#{browser_effect}\b/,
             "pure client core must not depend on browser effect #{browser_effect}"
    end

    refute File.exists?("assets/package.json"),
           "client-core coverage must not add a package manifest"

    for forbidden <- ~w(happy-dom jsdom vitest jest) do
      refute source_files() =~ forbidden
    end

    for hook <- ~w(Clipboard PromptShortcut SchemaPending SearchableCombobox SecretStager) do
      [_, body] = Regex.run(~r/const #{hook} = \{(.*?)\n\}\n\nconst/s, app)
      assert body =~ ".addEventListener"
      assert body =~ ".removeEventListener"
    end

    assert app =~ "dispatchEvent(new Event(\"change\", {bubbles: true}))"
  end

  defp source_files do
    Path.wildcard("{lib,assets}/**/*")
    |> Enum.filter(&File.regular?/1)
    |> Enum.map_join("\n", &File.read!/1)
  end
end
