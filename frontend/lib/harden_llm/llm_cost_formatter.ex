defmodule HardenLlm.LlmCostFormatter do
  @moduledoc false

  @fixed_precision_threshold 0.0001
  @maximum_decimal_places 12

  def format(value) when is_number(value) and value >= 0 do
    "$" <> decimal(value * 1.0)
  end

  def format(_value), do: "$—"

  defp decimal(value) when value == 0.0, do: "0.0000"

  defp decimal(value) when value >= @fixed_precision_threshold,
    do: :erlang.float_to_binary(value, decimals: 4)

  defp decimal(value) when value >= 1.0e-12 do
    value
    |> :erlang.float_to_binary([:compact, decimals: @maximum_decimal_places])
    |> pad_decimal_places(4)
  end

  defp decimal(value), do: :erlang.float_to_binary(value, scientific: 4)

  defp pad_decimal_places(value, minimum) do
    case String.split(value, ".", parts: 2) do
      [whole, fraction] -> whole <> "." <> String.pad_trailing(fraction, minimum, "0")
      [whole] -> whole <> "." <> String.duplicate("0", minimum)
    end
  end
end
