defmodule HardenLlmWeb.BrowserFeatureCase do
  @moduledoc false

  import ExUnit.Assertions
  import Wallaby.Browser

  alias HardenLlmWeb.{BrowserArtifactServer, BrowserBackend}
  alias Wallaby.Browser
  alias Wallaby.Query

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-047 WEB-TEST-048 TEST-047 TEST-056

  def setup_browser(_context) do
    BrowserBackend.stop()
    {:ok, _pid} = BrowserBackend.start()

    previous_req_options = Application.fetch_env!(:harden_llm, :harden_api_req_options)
    previous_artifact_origin = Application.fetch_env!(:harden_llm, :artifact_public_origin)
    previous_prls_client_options = Application.get_env(:prls_web, :client_options)

    Application.put_env(:harden_llm, :harden_api_req_options, plug: BrowserBackend)
    Application.put_env(:harden_llm, :artifact_public_origin, "http://127.0.0.1:4003")
    Application.put_env(:prls_web, :client_options, request_options: [plug: BrowserBackend])

    ExUnit.Callbacks.start_supervised!(
      {Bandit, plug: BrowserArtifactServer, ip: {127, 0, 0, 1}, port: 4003, startup_log: false}
    )

    ExUnit.Callbacks.on_exit(fn ->
      BrowserBackend.stop()
      Application.put_env(:harden_llm, :harden_api_req_options, previous_req_options)
      Application.put_env(:harden_llm, :artifact_public_origin, previous_artifact_origin)

      if previous_prls_client_options do
        Application.put_env(:prls_web, :client_options, previous_prls_client_options)
      else
        Application.delete_env(:prls_web, :client_options)
      end
    end)

    :ok
  end

  def assert_shared_login_page(session) do
    session
    |> assert_has(Query.css("main.prls-auth h1", text: "Welcome back"))
    |> assert_has(Query.css("form.prls-form input[name='email']"))
    |> assert_has(Query.css("form.prls-form input[name='password']"))
    |> assert_has(Query.css("form.prls-form button.prls-button", text: "Sign in"))
    |> assert_shared_login_styles()
  end

  def sign_in_shared_login(session, email, password) do
    session
    |> submit_shared_login(email, password)
    |> assert_has(Query.css("#workspace-page"))
  end

  defp submit_shared_login(session, email, password) do
    session
    |> assert_shared_login_page()
    |> then(fn session ->
      return_to =
        javascript_value(
          session,
          "return document.querySelector('form.prls-form input[name=return_to]')?.value;"
        )

      assert return_to == "/",
             "sign-in must preserve the protected return path, got #{inspect(return_to)}"

      session
    end)
    |> fill_in(Query.css("form.prls-form input[name='email']"), with: email)
    |> fill_in(Query.css("form.prls-form input[name='password']"), with: password)
    |> click(Query.css("form.prls-form button[type='submit']"))
  end

  def commit_combobox(session, selector, value) do
    session
    |> fill_in(Query.css(selector), with: value)
    |> send_keys(Query.css(selector), [:enter, :tab])
  end

  def stage_secret(session, input_selector, button_selector, secret) do
    Browser.execute_script(
      session,
      """
      const input = document.querySelector(arguments[0]);
      input.focus();
      input.value = arguments[2];
      document.querySelector(arguments[1]).dispatchEvent(
        new MouseEvent("click", {bubbles: true, cancelable: true})
      );
      """,
      [input_selector, button_selector, secret]
    )
  end

  def assert_combobox_closed(session, selector) do
    assert javascript_value(
             session,
             "return document.querySelector(arguments[0])?.getAttribute('aria-expanded');",
             [selector]
           ) == "false"

    session
  end

  def open_fold(session, toggle_selector, body_selector) do
    session
    |> assert_has(Query.css("#{toggle_selector}:not([disabled])"))
    |> maybe_click_fold(toggle_selector)
    |> assert_has(Query.css(body_selector))
  end

  def open_ui_fold(session, toggle_selector, body_selector) do
    session
    |> assert_has(Query.css("#{toggle_selector}:not([disabled])"))
    |> maybe_click_fold(toggle_selector)
    |> assert_has(Query.css(body_selector))
  end

  def refute_dom_element(session, selector) do
    refute javascript_value(session, "return Boolean(document.querySelector(arguments[0]));", [
             selector
           ])

    session
  end

  def trigger_prompt_shortcut(session, selector) do
    Browser.execute_script(
      session,
      """
      const input = document.querySelector(arguments[0]);
      input.focus();
      input.dispatchEvent(new KeyboardEvent("keydown", {
        key: "Enter",
        code: "Enter",
        ctrlKey: true,
        bubbles: true,
        cancelable: true
      }));
      """,
      [selector]
    )
  end

  def install_clipboard_stub(session) do
    Browser.execute_script(
      session,
      """
      Object.defineProperty(navigator, "clipboard", {
        configurable: true,
        value: {writeText: value => {
          window.__hardenCopiedText = value;
          return Promise.resolve();
        }}
      });
      """
    )
  end

  def choose_option(session, selector, value) do
    Browser.execute_script(
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

  def force_live_reconnect(session) do
    Browser.execute_script(
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

  def assert_live_socket_connected(session) do
    test_pid = self()

    Browser.execute_script(
      session,
      "return Boolean(window.liveSocket && window.liveSocket.isConnected());",
      fn value -> send(test_pid, {:browser_live_socket_connected, value}) end
    )

    assert_receive {:browser_live_socket_connected, true}, 1_000
    session
  end

  def assert_no_horizontal_overflow(session) do
    test_pid = self()

    Browser.execute_script(
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
      fn value -> send(test_pid, {:browser_bounded_viewport, value}) end
    )

    assert_receive {:browser_bounded_viewport, metrics}, 1_000
    assert metrics["bounded"], "horizontal overflow detected: #{inspect(metrics)}"
    session
  end

  def assert_unique_dom_ids(session) do
    test_pid = self()

    Browser.execute_script(
      session,
      """
      const ids = Array.from(document.querySelectorAll('[id]')).map(element => element.id);
      return {unique: ids.length === new Set(ids).size, count: ids.length};
      """,
      fn value -> send(test_pid, {:browser_unique_dom_ids, value}) end
    )

    assert_receive {:browser_unique_dom_ids, %{"unique" => true}}, 1_000
    session
  end

  def assert_dom_attribute(session, selector, attribute, expected) do
    test_pid = self()

    Browser.execute_script(
      session,
      "return document.querySelector(arguments[0])?.getAttribute(arguments[1]);",
      [selector, attribute],
      fn value -> send(test_pid, {:browser_dom_attribute, selector, value}) end
    )

    assert_receive {:browser_dom_attribute, ^selector, actual}, 1_000
    assert actual == expected, "#{selector} #{attribute} was #{inspect(actual)}"
    session
  end

  def scroll_to_selector(session, selector) do
    Browser.execute_script(
      session,
      "document.querySelector(arguments[0])?.scrollIntoView({block: 'center'});",
      [selector]
    )
  end

  def assert_field_value(session, selector, expected) do
    assert Wallaby.Browser.attr(session, Wallaby.Query.css(selector), "value") == expected
    session
  end

  def javascript_value(session, script, arguments \\ []) do
    reference = make_ref()
    test_pid = self()

    Browser.execute_script(session, script, arguments, fn value ->
      send(test_pid, {reference, value})
    end)

    assert_receive {^reference, value}, 2_000
    value
  end

  defp assert_shared_login_styles(session) do
    styles =
      javascript_value(session, """
      const stylesheet = Array.from(document.querySelectorAll('link[rel="stylesheet"]'))
        .find(link => new URL(link.href).pathname === "/assets/prls.css");
      const login = document.querySelector("main.prls-auth");
      return {
        stylesheetLoaded: Boolean(stylesheet?.sheet),
        pageBackground: getComputedStyle(document.documentElement).backgroundColor,
        buttonBackground: getComputedStyle(login.querySelector("button.prls-button")).backgroundColor
      };
      """)

    assert styles == %{
             "stylesheetLoaded" => true,
             "pageBackground" => "rgb(245, 241, 232)",
             "buttonBackground" => "rgb(36, 76, 59)"
           }

    session
  end

  defp maybe_click_fold(session, toggle_selector) do
    expanded =
      javascript_value(
        session,
        "return document.querySelector(arguments[0])?.getAttribute('aria-expanded') === 'true';",
        [toggle_selector]
      )

    if expanded, do: session, else: click(session, Query.css(toggle_selector))
  end

  def exactly_one_call?(operations, calls, operation_id) do
    operation = Enum.find(operations, &(&1.id == operation_id))
    method = operation.method |> Atom.to_string() |> String.upcase()
    Enum.count(calls, &(&1 == {method, operation.path})) == 1
  end
end
