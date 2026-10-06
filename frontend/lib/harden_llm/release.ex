defmodule HardenLlm.Release do
  @moduledoc false

  @app :harden_llm

  def migrate do
    migrations_path = Application.app_dir(@app, "priv/repo/migrations")

    {:ok, _, _} =
      Ecto.Migrator.with_repo(HardenLlm.Repo, fn repo ->
        Ecto.Migrator.run(repo, migrations_path, :up, all: true)
      end)
  end
end
