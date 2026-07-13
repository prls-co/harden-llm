import Config

config :harden_llm, HardenLlmWeb.Endpoint,
  http: [ip: {127, 0, 0, 1}, port: 4002],
  secret_key_base: "5dLH0wNT6/cqjy5KDzfp9HwkttRVEJF1g7s7rVePY8w3Q6fcEvmFonqkv0SlxhKT",
  server: true

config :harden_llm, :browser_session,
  key: "_harden_llm_web_test",
  signing_salt: "test-signing-salt",
  encryption_salt: "test-encryption-salt",
  secure: false

config :harden_llm, :harden_api_req_options, plug: {Req.Test, HardenLlmWeb.HardenAPI}

config :wallaby,
  otp_app: :harden_llm,
  driver: Wallaby.Chrome,
  chromedriver: [
    binary: System.get_env("CHROME_BIN"),
    path: System.get_env("CHROMEDRIVER_BIN", "chromedriver")
  ],
  max_wait_time: 15_000,
  screenshot_on_failure: true,
  screenshot_dir: "tmp/wallaby"

# Print only warnings and errors during test
config :logger, level: :warning

# Initialize plugs at runtime for faster test compilation
config :phoenix, :plug_init_mode, :runtime

# Enable helpful, but potentially expensive runtime checks
config :phoenix_live_view,
  enable_expensive_runtime_checks: true

# Sort query params output of verified routes for robust url comparisons
config :phoenix,
  sort_verified_routes_query_params: true
