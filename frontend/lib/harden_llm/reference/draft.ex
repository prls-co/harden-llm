defmodule HardenLlm.Reference.Draft do
  @moduledoc false

  use Ecto.Schema

  @primary_key false
  schema "reference_drafts" do
    field(:user_id, :string, primary_key: true)
    field(:namespace, :string, primary_key: true)
    field(:revision, :integer)
    field(:state, :map)
    field(:updated_at, :utc_datetime_usec)
  end
end
