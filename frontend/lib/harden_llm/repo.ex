defmodule HardenLlm.Repo do
  @moduledoc false

  use Ecto.Repo,
    otp_app: :harden_llm,
    adapter: Ecto.Adapters.Postgres
end
