defmodule PrlsUI.PaginationStateTest do
  use ExUnit.Case, async: true

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-078

  alias PrlsUI.PaginationState

  test "normalizes URL parameters without coercing invalid values into requests" do
    assert PaginationState.normalize_params(%{"page" => "7", "page_size" => "25"}) == %{
             page: 7,
             page_size: 25
           }

    assert PaginationState.normalize_params(%{"page" => "0", "page_size" => "13"}) == %{
             page: 1,
             page_size: 10
           }

    assert PaginationState.normalize_params(%{"page" => "2.5", "page_size" => "-1"}) == %{
             page: 1,
             page_size: 10
           }
  end

  test "derives empty, bounded, and exact page ranges" do
    assert PaginationState.total_pages(0, 10) == 0
    assert PaginationState.total_pages(20, 10) == 2
    assert PaginationState.total_pages(21, 10) == 3
    assert PaginationState.range(%{page: 1, page_size: 10, total_count: 0}) == {0, 0}
    assert PaginationState.range(%{page: 3, page_size: 10, total_count: 21}) == {21, 21}
    assert PaginationState.summary(%{page: 1, page_size: 10, total_count: 0}) == "0 items"
    assert PaginationState.summary(%{page: 2, page_size: 10, total_count: 21}) == "11-20 of 21"
  end

  test "page window retains boundaries and a bounded middle" do
    assert PaginationState.page_window(1, 20) == [1, 2, :ellipsis, 20]
    assert PaginationState.page_window(10, 20) == [1, :ellipsis, 9, 10, 11, :ellipsis, 20]
    assert PaginationState.page_window(1, 4) == [1, 2, 3, 4]
  end
end
