defmodule HardenLlmWeb.APIError do
  @moduledoc """
  Stable, redacted error returned by the Harden-LLM REST boundary.

  The struct deliberately carries no request/response object or raw transport
  exception, so LiveViews cannot accidentally render backend secrets.
  """

  @enforce_keys [:category, :message]
  defstruct [:category, :message, :status, :code, :trace_id, field_errors: %{}, ambiguous?: false]

  @type t :: %__MODULE__{
          category: atom(),
          message: String.t(),
          status: non_neg_integer() | nil,
          code: String.t() | nil,
          trace_id: String.t() | nil,
          field_errors: %{optional(String.t()) => String.t()},
          ambiguous?: boolean()
        }
end
