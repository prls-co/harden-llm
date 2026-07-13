defmodule HardenLlmWeb.SessionOptions do
  @moduledoc false

  def options do
    config = Application.fetch_env!(:harden_llm, :browser_session)

    [
      store: :cookie,
      key: Keyword.fetch!(config, :key),
      signing_salt: Keyword.fetch!(config, :signing_salt),
      encryption_salt: Keyword.fetch!(config, :encryption_salt),
      same_site: "Lax",
      secure: Keyword.get(config, :secure, true),
      http_only: true,
      path: "/"
    ]
  end
end
