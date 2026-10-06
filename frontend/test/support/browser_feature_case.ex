defmodule HardenLlmWeb.BrowserFeatureCase do
  @moduledoc false

  import ExUnit.Assertions
  import Wallaby.Browser

  alias HardenLlmWeb.BrowserBackend
  alias Wallaby.Browser
  alias Wallaby.Query

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-047

  def setup_browser(_context) do
    BrowserBackend.stop()
    {:ok, _pid} = BrowserBackend.start()

    previous_req_options = Application.fetch_env!(:harden_llm, :harden_api_req_options)
    previous_prls_client_options = Application.get_env(:prls_web, :client_options)
    product_origin = HardenLlmWeb.Endpoint.url()

    start_portal(%{
      public_origin: product_origin,
      portal_origin: "http://localhost:4004",
      login_return_origins: [product_origin, "http://localhost:4004"]
    })

    Application.put_env(:harden_llm, :harden_api_req_options, plug: BrowserBackend)
    Application.put_env(:prls_web, :client_options, request_options: [plug: BrowserBackend])

    ExUnit.Callbacks.on_exit(fn ->
      BrowserBackend.stop()
      Application.put_env(:harden_llm, :harden_api_req_options, previous_req_options)

      if previous_prls_client_options do
        Application.put_env(:prls_web, :client_options, previous_prls_client_options)
      else
        Application.delete_env(:prls_web, :client_options)
      end
    end)

    :ok
  end

  def start_portal(settings, ip \\ {127, 0, 0, 1}) do
    previous = for {key, _} <- settings, into: %{}, do: {key, Application.get_env(:prls_web, key)}
    for {key, value} <- settings, do: Application.put_env(:prls_web, key, value)

    ExUnit.Callbacks.on_exit(fn ->
      for {key, value} <- previous do
        if value,
          do: Application.put_env(:prls_web, key, value),
          else: Application.delete_env(:prls_web, key)
      end
    end)

    ExUnit.Callbacks.start_supervised!(
      Supervisor.child_spec(
        {Bandit, plug: HardenLlmWeb.BrowserPortal, ip: ip, port: 4004, startup_log: false},
        id: HardenLlmWeb.BrowserPortal
      )
    )
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
    |> assert_shared_login_page()
    |> then(fn session ->
      return_to =
        javascript_value(
          session,
          "return document.querySelector('form.prls-form input[name=return_to]')?.value;"
        )

      expected =
        session
        |> current_url()
        |> URI.parse()
        |> Map.fetch!(:query)
        |> URI.decode_query()
        |> Map.fetch!("return_to")

      assert return_to == expected,
             "Portal sign-in must preserve the protected return URL, got #{inspect(return_to)}"

      assert URI.parse(return_to).path == "/"
      session
    end)
    |> fill_in(Query.css("form.prls-form input[name='email']"), with: email)
    |> fill_in(Query.css("form.prls-form input[name='password']"), with: password)
    |> click(Query.css("form.prls-form button[type='submit']"))
    |> assert_has(Query.css("#workspace-page"))
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

  defp javascript_value(session, script) do
    reference = make_ref()
    test_pid = self()

    Browser.execute_script(session, script, fn value -> send(test_pid, {reference, value}) end)

    assert_receive {^reference, value}, 2_000
    value
  end
end
