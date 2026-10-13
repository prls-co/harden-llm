import Config

if config_env() == :test do
  config :harden_llm_ui, HardenLlmUI.TestEndpoint,
    url: [host: "localhost"],
    secret_key_base: String.duplicate("independent-ui-test-", 4),
    live_view: [signing_salt: "ui-test-salt"],
    pubsub_server: HardenLlmUI.TestPubSub,
    server: false,
    render_errors: [formats: [html: HardenLlmUI.TestErrorHTML], layout: false]

  config :phoenix, :json_library, Jason
  config :logger, level: :warning
end
