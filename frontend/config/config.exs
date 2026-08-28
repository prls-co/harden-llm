# This file is responsible for configuring your application
# and its dependencies with the aid of the Config module.
#
# This configuration file is loaded before any dependency and
# is restricted to this project.

# General application configuration
import Config

config :harden_llm,
  generators: [timestamp_type: :utc_datetime]

config :harden_llm, :harden_api,
  base_url: "http://127.0.0.1:8080",
  api_timeout_ms: 15_000,
  run_timeout_ms: 65_000,
  max_run_duration_ms: 60_000

config :harden_llm, :browser_session,
  key: "_harden_llm_web_dev",
  signing_salt: "dev-signing-salt",
  encryption_salt: "dev-encryption-salt",
  secure: false

config :harden_llm, :session_vault,
  path: Path.expand("../tmp/harden-llm-session-vault.dets", __DIR__)

config :harden_llm,
  artifact_public_origin: "https://artifacts.example.test",
  max_bundle_bytes: 2_097_152

config :harden_llm, :observability,
  file_log_enabled: false,
  file_log_path: "/var/log/harden-llm-web/app.jsonl",
  file_log_max_bytes: 10_485_760,
  file_log_max_files: 5

config :harden_llm, HardenLlmWeb.PromEx,
  disabled: false,
  manual_metrics_start_delay: :no_delay,
  drop_metrics_groups: [],
  grafana: :disabled,
  metrics_server: :disabled

config :opentelemetry,
  traces_exporter: :none,
  attribute_count_limit: 32,
  attribute_value_length_limit: 256

# Configure the endpoint
config :harden_llm, HardenLlmWeb.Endpoint,
  url: [host: "localhost"],
  adapter: Bandit.PhoenixAdapter,
  render_errors: [
    formats: [html: HardenLlmWeb.ErrorHTML, json: HardenLlmWeb.ErrorJSON],
    layout: false
  ],
  pubsub_server: HardenLlm.PubSub,
  live_view: [signing_salt: "5xrrXWfE"]

# Configure LiveView
config :phoenix_live_view,
  # the attribute set on all root tags. Used for Phoenix.LiveView.ColocatedCSS.
  root_tag_attribute: "phx-r"

# Configure esbuild (the version is required)
config :esbuild,
  version: "0.25.4",
  harden_llm: [
    args:
      ~w(js/app.js --bundle --target=es2022 --outdir=../priv/static/assets/js --external:/fonts/* --external:/images/* --alias:@=.),
    cd: Path.expand("../assets", __DIR__),
    env: %{"NODE_PATH" => [Path.expand("../deps", __DIR__), Mix.Project.build_path()]}
  ]

# Configure tailwind (the version is required)
config :tailwind,
  version: "4.3.0",
  harden_llm: [
    args: ~w(
      --input=assets/css/app.css
      --output=priv/static/assets/css/app.css
    ),
    cd: Path.expand("..", __DIR__),
    env: %{"NODE_PATH" => [Path.expand("../deps", __DIR__), Mix.Project.build_path()]}
  ]

# Configure Elixir's Logger
config :logger, :default_formatter,
  format: "$time $metadata[$level] $message\n",
  metadata: [:request_id, :trace_id, :span_id, :operation, :outcome]

# No form value is safe to include in framework logs. Operational fields are
# emitted explicitly through the bounded metadata allowlist above.
config :phoenix, :filter_parameters, {:keep, []}

# Use Jason for JSON parsing in Phoenix
config :phoenix, :json_library, Jason

# Import environment specific config. This must remain at the bottom
# of this file so it overrides the configuration defined above.
import_config "#{config_env()}.exs"
