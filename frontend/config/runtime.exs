import Config

# config/runtime.exs is executed for all environments, including
# during releases. It is executed after compilation and before the
# system starts, so it is typically used to load production configuration
# and secrets from environment variables or elsewhere. Do not define
# any compile-time configuration in here, as it won't be applied.
# The block below contains prod specific runtime configuration.

# ## Using releases
#
# If you use `mix release`, you need to explicitly enable the server
# by passing the PHX_SERVER=true when you start it:
#
#     PHX_SERVER=true bin/harden_llm start
#
# Alternatively, you can use `mix phx.gen.release` to generate a `bin/server`
# script that automatically sets the env var above.
if System.get_env("PHX_SERVER") do
  config :harden_llm, HardenLlmWeb.Endpoint, server: true
end

default_http_port = if config_env() == :test, do: "4002", else: "4000"

config :harden_llm, HardenLlmWeb.Endpoint,
  http: [port: String.to_integer(System.get_env("PORT", default_http_port))]

parse_positive_integer = fn name, default ->
  value = System.get_env(name, default)

  case Integer.parse(value) do
    {integer, ""} when integer > 0 -> integer
    _ -> raise "#{name} must be a positive integer"
  end
end

config :harden_llm, :harden_api,
  base_url: System.get_env("HARDEN_LLM_API_BASE_URL", "http://127.0.0.1:8080"),
  api_timeout_ms: parse_positive_integer.("HARDEN_LLM_WEB_API_TIMEOUT_MS", "15000"),
  run_timeout_ms: parse_positive_integer.("HARDEN_LLM_WEB_RUN_TIMEOUT_MS", "65000"),
  max_run_duration_ms: parse_positive_integer.("HARDEN_LLM_MAX_RUN_DURATION_MS", "60000")

config :harden_llm,
  artifact_public_origin:
    System.get_env("HARDEN_LLM_ARTIFACT_PUBLIC_ORIGIN", "https://artifacts.example.test")

if config_env() == :dev do
  # Reload browser tabs when matching files change.
  config :harden_llm, HardenLlmWeb.Endpoint,
    live_reload: [
      web_console_logger: true,
      patterns: [
        # Static assets, except user uploads
        ~r"priv/static/(?!uploads/).*\.(js|css|png|jpeg|jpg|gif|svg)$"E,
        # Gettext translations
        ~r"priv/gettext/.*\.po$"E,
        # Router, Controllers, LiveViews and LiveComponents
        ~r"lib/harden_llm_web/router\.ex$"E,
        ~r"lib/harden_llm_web/(controllers|live|components)/.*\.(ex|heex)$"E
      ]
    ]
end

if config_env() == :prod do
  # The secret key base is used to sign/encrypt cookies and other secrets.
  # A default value is used in config/dev.exs and config/test.exs but you
  # want to use a different value for prod and you most likely don't want
  # to check this value into version control, so we use an environment
  # variable instead.
  secret_key_base =
    System.get_env("HARDEN_LLM_WEB_SECRET_KEY_BASE") ||
      raise """
      environment variable HARDEN_LLM_WEB_SECRET_KEY_BASE is missing.
      You can generate one by calling: mix phx.gen.secret
      """

  host = System.fetch_env!("HARDEN_LLM_WEB_HOST")
  signing_salt = System.fetch_env!("HARDEN_LLM_WEB_SESSION_SIGNING_SALT")
  encryption_salt = System.fetch_env!("HARDEN_LLM_WEB_SESSION_ENCRYPTION_SALT")
  release = System.fetch_env!("HARDEN_LLM_RELEASE")
  environment = System.get_env("HARDEN_LLM_ENVIRONMENT", "production")

  instance_id =
    System.get_env("HARDEN_LLM_WEB_INSTANCE_ID", System.get_env("HOSTNAME", "unknown"))

  otlp_endpoint = System.get_env("OTEL_EXPORTER_OTLP_ENDPOINT", "http://otel-collector:4317")

  config :opentelemetry,
    span_processor: :batch,
    traces_exporter: :otlp,
    resource: [
      service: [name: "harden-llm-web", version: release, instance: [id: instance_id]],
      deployment: [environment: [name: environment]]
    ]

  config :opentelemetry_exporter,
    otlp_protocol: :grpc,
    otlp_endpoint: otlp_endpoint

  config :harden_llm, :observability,
    file_log_enabled: true,
    file_log_path: System.get_env("HARDEN_LLM_WEB_LOG_PATH", "/var/log/harden-llm-web/app.jsonl"),
    file_log_max_bytes: parse_positive_integer.("HARDEN_LLM_WEB_LOG_MAX_BYTES", "10485760"),
    file_log_max_files: parse_positive_integer.("HARDEN_LLM_WEB_LOG_MAX_FILES", "5")

  config :harden_llm, :browser_session,
    key: "__Host-harden_llm_web",
    signing_salt: signing_salt,
    encryption_salt: encryption_salt,
    secure: true

  config :harden_llm, :dns_cluster_query, System.get_env("DNS_CLUSTER_QUERY")

  config :harden_llm, HardenLlmWeb.Endpoint,
    url: [host: host, port: 443, scheme: "https"],
    http: [
      # Enable IPv6 and bind on all interfaces.
      # Set it to  {0, 0, 0, 0, 0, 0, 0, 1} for local network only access.
      # See the documentation on https://bandit.hexdocs.pm/Bandit.html#t:options/0
      # for details about using IPv6 vs IPv4 and loopback vs public addresses.
      ip: {0, 0, 0, 0},
      port: parse_positive_integer.("HARDEN_LLM_WEB_PORT", "4000")
    ],
    secret_key_base: secret_key_base,
    check_origin: ["https://#{host}"]

  # ## SSL Support
  #
  # To get SSL working, you will need to add the `https` key
  # to your endpoint configuration:
  #
  #     config :harden_llm, HardenLlmWeb.Endpoint,
  #       https: [
  #         ...,
  #         port: 443,
  #         cipher_suite: :strong,
  #         keyfile: System.get_env("SOME_APP_SSL_KEY_PATH"),
  #         certfile: System.get_env("SOME_APP_SSL_CERT_PATH")
  #       ]
  #
  # The `cipher_suite` is set to `:strong` to support only the
  # latest and more secure SSL ciphers. This means old browsers
  # and clients may not be supported. You can set it to
  # `:compatible` for wider support.
  #
  # `:keyfile` and `:certfile` expect an absolute path to the key
  # and cert in disk or a relative path inside priv, for example
  # "priv/ssl/server.key". For all supported SSL configuration
  # options, see https://plug.hexdocs.pm/Plug.SSL.html#configure/1
  #
  # We also recommend setting `force_ssl` in your config/prod.exs,
  # ensuring no data is ever sent via http, always redirecting to https:
  #
  #     config :harden_llm, HardenLlmWeb.Endpoint,
  #       force_ssl: [hsts: true]
  #
  # Check `Plug.SSL` for all available options in `force_ssl`.
end
