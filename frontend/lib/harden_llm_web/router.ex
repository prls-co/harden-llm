defmodule HardenLlmWeb.Router do
  use HardenLlmWeb, :router

  pipeline :browser do
    plug :accepts, ["html"]
    plug :fetch_session
    plug :fetch_live_flash
    plug :put_root_layout, html: {HardenLlmWeb.Layouts, :root}
    plug :protect_from_forgery

    plug :put_secure_browser_headers, %{
      "content-security-policy" =>
        "default-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'; object-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self' ws: wss:",
      "referrer-policy" => "no-referrer",
      "permissions-policy" => "camera=(), microphone=(), geolocation=()",
      "x-frame-options" => "DENY"
    }

    plug HardenLlmWeb.Auth, :fetch_session_handle
  end

  pipeline :health do
    plug :accepts, ["json"]
  end

  pipeline :redirect_if_authenticated do
    plug HardenLlmWeb.Auth, :redirect_if_authenticated
  end

  pipeline :require_authenticated do
    plug HardenLlmWeb.Auth, :require_authenticated
  end

  scope "/", HardenLlmWeb do
    pipe_through :health

    get "/healthz", HealthController, :show
  end

  scope "/", HardenLlmWeb do
    pipe_through :browser

    get "/", PageController, :home
    get "/session/expired", SessionController, :expired
  end

  scope "/", HardenLlmWeb do
    pipe_through [:browser, :redirect_if_authenticated]

    get "/login", SessionController, :new
  end

  scope "/", HardenLlmWeb do
    pipe_through :browser

    post "/login", SessionController, :create
  end

  scope "/", HardenLlmWeb do
    pipe_through [:browser, :require_authenticated]

    post "/logout", SessionController, :delete
    get "/profiles/bundle", BundleController, :show
    get "/traces/:trace_id/artifacts/:artifact_id", ArtifactController, :show

    live_session :authenticated,
      on_mount: [{HardenLlmWeb.Auth, :require_authenticated}] do
      live "/workspace", WorkspaceLive
      live "/embed/llm", EmbeddingLive
      live "/profiles", ProfilesLive
      live "/history", HistoryLive
    end
  end

  # Enable LiveDashboard in development
  if Application.compile_env(:harden_llm, :dev_routes) do
    # If you want to use the LiveDashboard in production, you should put
    # it behind authentication and allow only admins to access it.
    # If your application does not have an admins-only section yet,
    # you can use Plug.BasicAuth to set up some basic authentication
    # as long as you are also using SSL (which you should anyway).
    import Phoenix.LiveDashboard.Router

    scope "/dev" do
      pipe_through :browser

      live_dashboard "/dashboard", metrics: HardenLlmWeb.Telemetry
    end
  end
end
