defmodule HardenLlm.Reference.Call do
  @moduledoc false

  use Ecto.Schema

  @primary_key {:id, :binary_id, autogenerate: true}
  @foreign_key_type :binary_id

  schema "reference_history" do
    field(:endpoint, :string)
    field(:request, :map)
    field(:outcome, :map)
    field(:outcome_kind, :string)
    timestamps(type: :utc_datetime_usec, updated_at: false)
  end
end
