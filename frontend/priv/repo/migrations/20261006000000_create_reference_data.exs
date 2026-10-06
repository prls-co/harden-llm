defmodule HardenLlm.Repo.Migrations.CreateReferenceData do
  use Ecto.Migration

  def change do
    create table(:reference_history, primary_key: false) do
      add :id, :uuid, primary_key: true
      add :endpoint, :text, null: false
      add :request, :map, null: false
      add :outcome, :map, null: false
      add :outcome_kind, :text, null: false
      add :inserted_at, :utc_datetime_usec, null: false
    end

    create index(:reference_history, [:inserted_at, :id])

    create table(:reference_drafts, primary_key: false) do
      add :user_id, :text, primary_key: true, null: false
      add :namespace, :text, primary_key: true, null: false
      add :revision, :bigint, null: false
      add :state, :map, null: false
      add :updated_at, :utc_datetime_usec, null: false
    end

  end
end
