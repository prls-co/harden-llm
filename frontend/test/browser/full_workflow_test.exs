defmodule HardenLlmWeb.FullWorkflowTest do
  use ExUnit.Case, async: false
  use Wallaby.Feature

  @moduletag :browser

  alias HardenLlmWeb.{BrowserArtifactServer, BrowserBackend, HardenAPI}
  alias Wallaby.Query

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-011

  setup do
    BrowserBackend.stop()
    {:ok, _pid} = BrowserBackend.start()

    previous_req_options = Application.fetch_env!(:harden_llm, :harden_api_req_options)
    previous_artifact_origin = Application.fetch_env!(:harden_llm, :artifact_public_origin)

    Application.put_env(:harden_llm, :harden_api_req_options, plug: BrowserBackend)
    Application.put_env(:harden_llm, :artifact_public_origin, "http://127.0.0.1:4003")

    start_supervised!(
      {Bandit, plug: BrowserArtifactServer, ip: {127, 0, 0, 1}, port: 4003, startup_log: false}
    )

    on_exit(fn ->
      BrowserBackend.stop()
      Application.put_env(:harden_llm, :harden_api_req_options, previous_req_options)
      Application.put_env(:harden_llm, :artifact_public_origin, previous_artifact_origin)
    end)

    :ok
  end

  feature "operator completes the full workflow at desktop size", %{session: session} do
    run_full_workflow(session, 1_440, 900)
  end

  feature "operator completes the full workflow at mobile size", %{session: session} do
    run_full_workflow(session, 390, 844)
  end

  defp run_full_workflow(session, width, height) do
    session =
      session
      |> resize_window(width, height)
      |> visit("/login")
      |> assert_has(Query.css("#login-page"))
      |> assert_no_horizontal_overflow()
      |> fill_in(Query.text_field("Email address"), with: "browser@example.test")
      |> fill_in(Query.css("#session_password"), with: "browser-password-123")
      |> click(Query.css("#login-submit"))
      |> assert_has(Query.css("#workspace-page"))
      |> assert_has(Query.css("#backend-status", text: "Backend ready"))
      |> assert_live_socket_connected()
      |> assert_no_horizontal_overflow()

    refute page_source(session) =~ "browser-fixture-token-that-never-leaves-the-server"

    session =
      session
      |> click(Query.css("#nav-profiles"))
      |> assert_has(Query.css("#profiles-page"))
      |> assert_has(Query.css("#profiles-empty", visible: :any))
      |> assert_no_horizontal_overflow()
      |> click(Query.css("#new-profile"))
      |> assert_has(Query.css("#profile-dialog"))
      |> fill_in(Query.text_field("Profile name"), with: "BrowserProfile")
      |> fill_in(Query.text_field("Provider family"), with: "openai")
      |> fill_in(Query.text_field("Default model"), with: "model-browser")
      |> fill_in(Query.fillable_field("HTTPS base URL"),
        with: "https://provider.example.test/v1"
      )
      |> fill_in(Query.text_field("Credential ID"), with: "browser-credential")
      |> fill_in(Query.css("#profile_apiKey"), with: "browser-provider-secret")
      |> click(Query.css("#profile-save"))
      |> assert_has(Query.css("#profile-BrowserProfile", text: "BrowserProfile"))
      |> refute_has(Query.css("#profile-dialog"))
      |> assert_no_horizontal_overflow()
      |> click(Query.css("#profile-BrowserProfile button[aria-label='Refresh models']"))
      |> assert_has(Query.css("#flash-info", text: "Model catalog refreshed"))
      |> visit("/profiles")
      |> assert_has(Query.css("#profile-BrowserProfile", text: "BrowserProfile"))

    refute page_source(session) =~ "browser-provider-secret"

    session =
      session
      |> click(Query.css("#nav-workspace"))
      |> assert_has(Query.css("#backend-status", text: "Backend ready"))
      |> choose_option("#run_selectedProfileId", "BrowserProfile")
      |> fill_in(Query.css("#run_userPrompt"), with: "run the deterministic browser fixture")
      |> click(Query.css("#run-submit"))
      |> assert_has(Query.css("#run-output", text: "deterministic browser output"))
      |> assert_has(Query.css("#run-result-panel", text: "trace-browser"))
      |> assert_no_horizontal_overflow()
      |> click(Query.css("#nav-history"))
      |> assert_has(Query.css("#history-run-browser"))
      |> click(Query.css("#history-run-browser button[aria-label='Restore to workspace']"))
      |> assert_has(Query.css("#workspace-page"))
      |> assert_field_value("#run_userPrompt", "run the deterministic browser fixture")
      |> click(Query.css("#nav-history"))
      |> assert_has(Query.css("#history-run-browser"))
      |> click(Query.css("#history-run-browser button[aria-label='Open trace']"))
      |> assert_has(Query.css("#trace-dialog"))
      |> assert_has(Query.css("#observation-0", text: "deterministic browser output"))
      |> assert_no_horizontal_overflow()
      |> click(Query.css("#artifact-artifact-browser"))
      |> assert_has(Query.css("body", text: "browser-fixture"))

    assert current_url(session) == "http://127.0.0.1:4003/download/browser-artifact"

    session =
      session
      |> visit("/history")
      |> assert_has(Query.css("#history-run-browser"))
      |> force_live_reconnect()
      |> assert_has(Query.css("body[data-browser-reconnected='true']"))
      |> assert_has(Query.css("#history-run-browser"))
      |> assert_no_horizontal_overflow()
      |> click(Query.css("#logout-button"))
      |> assert_has(Query.css("#login-page"))
      |> visit("/workspace")
      |> assert_has(Query.css("#login-page"))

    assert exactly_one_call?(HardenAPI.operations(), BrowserBackend.calls(), "run")

    assert Enum.member?(
             BrowserBackend.calls(),
             {"POST", "/api/v1/profiles/BrowserProfile/models:refresh"}
           )

    assert Enum.member?(BrowserBackend.calls(), {"GET", "/api/v1/traces/trace-browser"})

    cookies = cookies(session)
    refute inspect(cookies) =~ "browser-fixture-token-that-never-leaves-the-server"
  end

  defp choose_option(session, selector, value) do
    execute_script(
      session,
      """
      const select = document.querySelector(arguments[0]);
      select.value = arguments[1];
      select.dispatchEvent(new Event("input", {bubbles: true}));
      select.dispatchEvent(new Event("change", {bubbles: true}));
      """,
      [selector, value]
    )
  end

  defp force_live_reconnect(session) do
    execute_script(
      session,
      """
      document.body.dataset.browserReconnected = "false";
      window.liveSocket.disconnect();
      window.liveSocket.connect();
      const timer = window.setInterval(() => {
        if (window.liveSocket.isConnected()) {
          document.body.dataset.browserReconnected = "true";
          window.clearInterval(timer);
        }
      }, 25);
      """
    )
  end

  defp assert_live_socket_connected(session) do
    test_pid = self()

    session =
      execute_script(
        session,
        "return Boolean(window.liveSocket && window.liveSocket.isConnected());",
        fn value ->
          send(test_pid, {:live_socket_connected, value})
        end
      )

    assert_receive {:live_socket_connected, true}, 1_000
    session
  end

  defp assert_no_horizontal_overflow(session) do
    test_pid = self()

    session =
      execute_script(
        session,
        """
        const root = document.documentElement;
        const clientWidth = root.clientWidth;
        const offenders = Array.from(document.querySelectorAll("body *"))
          .filter(element => {
            const rect = element.getBoundingClientRect();
            const style = window.getComputedStyle(element);
            return style.position !== "fixed" && (rect.right > clientWidth + 1 || rect.left < -1);
          })
          .slice(0, 12)
          .map(element => ({
            id: element.id,
            tag: element.tagName,
            className: String(element.className).slice(0, 160),
            left: Math.round(element.getBoundingClientRect().left),
            right: Math.round(element.getBoundingClientRect().right)
          }));

        return {
          bounded: root.scrollWidth <= clientWidth + 1,
          clientWidth,
          scrollWidth: root.scrollWidth,
          offenders
        };
        """,
        fn value -> send(test_pid, {:bounded_viewport, value}) end
      )

    assert_receive {:bounded_viewport, metrics}, 1_000
    assert metrics["bounded"], "horizontal overflow detected: #{inspect(metrics)}"
    session
  end

  defp assert_field_value(session, selector, expected) do
    assert attr(session, Query.css(selector), "value") == expected
    session
  end

  defp exactly_one_call?(operations, calls, operation_id) do
    operation = Enum.find(operations, &(&1.id == operation_id))
    method = operation.method |> Atom.to_string() |> String.upcase()
    Enum.count(calls, &(&1 == {method, operation.path})) == 1
  end
end
