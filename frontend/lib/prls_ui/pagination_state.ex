defmodule PrlsUI.PaginationState do
  @moduledoc """
  Small, domain-neutral helpers for server-owned numbered pagination.

  The module deliberately contains no query, HTTP, or LiveView knowledge. A
  consumer owns the request lifecycle and stores the returned metadata; these
  helpers only normalize user-facing parameters and derive display facts.
  """

  @default_page 1
  @default_page_size 10
  @default_page_size_options [10, 25, 50, 100]

  @type metadata :: %{
          required(:page) => pos_integer(),
          required(:page_size) => pos_integer(),
          required(:total_count) => non_neg_integer()
        }

  @spec default_page() :: pos_integer()
  def default_page, do: @default_page

  @spec default_page_size() :: pos_integer()
  def default_page_size, do: @default_page_size

  @spec default_page_size_options() :: [pos_integer()]
  def default_page_size_options, do: @default_page_size_options

  @spec positive_integer(term()) :: {:ok, pos_integer()} | :error
  def positive_integer(value) when is_integer(value) and value > 0, do: {:ok, value}

  def positive_integer(value) when is_binary(value) do
    case Integer.parse(value) do
      {integer, ""} when integer > 0 -> {:ok, integer}
      _ -> :error
    end
  end

  def positive_integer(_value), do: :error

  @spec normalize_page(term(), pos_integer()) :: pos_integer()
  def normalize_page(value, default \\ @default_page) when is_integer(default) and default > 0 do
    case positive_integer(value) do
      {:ok, page} -> page
      :error -> default
    end
  end

  @spec normalize_page_size(term(), [pos_integer()], pos_integer()) :: pos_integer()
  def normalize_page_size(
        value,
        options \\ @default_page_size_options,
        default \\ @default_page_size
      ) do
    case positive_integer(value) do
      {:ok, page_size} -> if(page_size in options, do: page_size, else: default)
      _ -> default
    end
  end

  @type criteria :: %{required(:page) => pos_integer(), required(:page_size) => pos_integer()}
  @type intent :: {:page, term()} | {:page_size, term()} | :reset

  @spec transition(criteria(), intent(), keyword()) ::
          {:ok, criteria()}
          | :unchanged
          | {:error, :invalid_page | :invalid_page_size | :invalid_state}
  def transition(current, intent, options \\ [])

  def transition(current, intent, options) when is_map(current) do
    page_size_options = Keyword.get(options, :page_size_options, @default_page_size_options)

    with {:ok, page} <- positive_integer(Map.get(current, :page)),
         {:ok, page_size} <- positive_integer(Map.get(current, :page_size)),
         true <- page_size in page_size_options do
      apply_transition(%{page: page, page_size: page_size}, intent, page_size_options)
    else
      false -> {:error, :invalid_state}
      :error -> {:error, :invalid_state}
    end
  end

  def transition(_current, _intent, _options), do: {:error, :invalid_state}

  defp apply_transition(current, {:page, value}, _page_size_options) do
    case positive_integer(value) do
      {:ok, page} when page == current.page -> :unchanged
      {:ok, page} -> {:ok, %{current | page: page}}
      :error -> {:error, :invalid_page}
    end
  end

  defp apply_transition(current, {:page_size, value}, page_size_options) do
    case positive_integer(value) do
      {:ok, page_size} ->
        cond do
          page_size not in page_size_options ->
            {:error, :invalid_page_size}

          page_size == current.page_size ->
            :unchanged

          true ->
            {:ok, %{page: 1, page_size: page_size}}
        end

      _ ->
        {:error, :invalid_page_size}
    end
  end

  defp apply_transition(%{page: 1}, :reset, _page_size_options), do: :unchanged
  defp apply_transition(current, :reset, _page_size_options), do: {:ok, %{current | page: 1}}
  defp apply_transition(_current, _intent, _page_size_options), do: {:error, :invalid_state}

  @spec normalize_params(map(), keyword()) :: %{page: pos_integer(), page_size: pos_integer()}
  def normalize_params(params, options \\ []) when is_map(params) do
    page_key = Keyword.get(options, :page_key, "page")
    page_size_key = Keyword.get(options, :page_size_key, "page_size")
    page_size_options = Keyword.get(options, :page_size_options, @default_page_size_options)
    default_page = Keyword.get(options, :default_page, @default_page)
    default_page_size = Keyword.get(options, :default_page_size, @default_page_size)

    %{
      page: normalize_page(Map.get(params, page_key), default_page),
      page_size:
        normalize_page_size(Map.get(params, page_size_key), page_size_options, default_page_size)
    }
  end

  @spec total_pages(non_neg_integer(), pos_integer()) :: non_neg_integer()
  def total_pages(0, _page_size), do: 0
  def total_pages(total_count, page_size), do: div(total_count + page_size - 1, page_size)

  @spec range(metadata()) :: {non_neg_integer(), non_neg_integer()}
  def range(metadata) do
    %{page: page, page_size: page_size, total_count: total_count} = validate_metadata!(metadata)

    if total_count == 0 do
      {0, 0}
    else
      first = (page - 1) * page_size + 1
      last = min(page * page_size, total_count)
      {first, last}
    end
  end

  @spec summary(metadata()) :: String.t()
  def summary(%{page: page, page_size: page_size, total_count: total_count}) do
    case range(%{page: page, page_size: page_size, total_count: total_count}) do
      {0, 0} -> "0 items"
      {first, last} -> "#{first}-#{last} of #{total_count}"
    end
  end

  @spec page_window(pos_integer(), non_neg_integer(), keyword()) :: [pos_integer() | :ellipsis]
  def page_window(current_page, total_pages, options \\ []) do
    sibling_count = Keyword.get(options, :sibling_count, 1)
    boundary_count = Keyword.get(options, :boundary_count, 1)

    validate_window_option!(:current_page, current_page, &positive_integer?/1)
    validate_window_option!(:total_pages, total_pages, &nonnegative_integer?/1)
    validate_window_option!(:sibling_count, sibling_count, &nonnegative_integer?/1)
    validate_window_option!(:boundary_count, boundary_count, &nonnegative_integer?/1)

    cond do
      total_pages <= 0 ->
        []

      total_pages <= boundary_count * 2 + sibling_count * 2 + 3 ->
        Enum.to_list(1..total_pages)

      true ->
        middle_start = max(boundary_count + 1, current_page - sibling_count)
        middle_end = min(total_pages - boundary_count, current_page + sibling_count)

        numbers =
          (Enum.to_list(1..boundary_count) ++
             integer_range(middle_start, middle_end) ++
             Enum.to_list((total_pages - boundary_count + 1)..total_pages))
          |> Enum.uniq()
          |> Enum.sort()

        Enum.reduce(numbers, [], fn number, window ->
          case List.last(window) do
            previous when is_integer(previous) and number - previous > 1 ->
              window ++ [:ellipsis, number]

            _ ->
              window ++ [number]
          end
        end)
    end
  end

  defp integer_range(first, last) when first <= last, do: Enum.to_list(first..last)
  defp integer_range(_first, _last), do: []

  defp validate_window_option!(name, value, predicate) do
    if predicate.(value) do
      :ok
    else
      raise ArgumentError, "invalid pagination #{name}: #{inspect(value)}"
    end
  end

  defp positive_integer?(value), do: is_integer(value) and value > 0
  defp nonnegative_integer?(value), do: is_integer(value) and value >= 0

  @spec valid_metadata?(term()) :: boolean()
  def valid_metadata?(%{page: page, page_size: page_size, total_count: total_count})
      when is_integer(page) and page > 0 and is_integer(page_size) and page_size > 0 and
             is_integer(total_count) and total_count >= 0,
      do: page <= max(total_pages(total_count, page_size), 1)

  def valid_metadata?(_metadata), do: false

  @spec validate_metadata!(metadata()) :: metadata()
  def validate_metadata!(metadata) do
    if valid_metadata?(metadata) do
      metadata
    else
      raise ArgumentError, "invalid pagination metadata: #{inspect(metadata)}"
    end
  end
end
