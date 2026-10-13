defmodule HardenLlmWeb.APIContext do
  @moduledoc "The reference host's environment configuration, passed explicitly to the UI client."
  alias HardenLlmUI.Client.Context

  def server(draft_key \\ nil) do
    config = Application.fetch_env!(:harden_llm, :harden_api)

    Context.new(
      base_url: Keyword.fetch!(config, :base_url),
      api_key: Keyword.fetch!(config, :token),
      timeout_ms: Keyword.fetch!(config, :api_timeout_ms),
      draft_key: draft_key,
      request_options: Application.get_env(:harden_llm, :harden_api_req_options, [])
    )
  end

  def validate! do
    server()
    :ok
  end
end
