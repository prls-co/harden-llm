defmodule PrlsUI.PaginationTest do
  use ExUnit.Case, async: true

  import Phoenix.LiveViewTest

  alias PrlsUI.Pagination

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-077

  test "renders independent navigation, size, summary, jump, and disabled states" do
    html =
      render_component(&Pagination.pagination/1,
        id: "history-pagination",
        page: 3,
        page_size: 10,
        total_count: 463,
        event: "history-paginate"
      )

    doc = LazyHTML.from_document(html)

    assert LazyHTML.query(doc, "#history-pagination-summary") |> LazyHTML.text() |> String.trim() ==
             "21-30 of 463"

    assert LazyHTML.query(doc, "#history-pagination-page-3[aria-current='page']") |> Enum.count() ==
             1

    assert LazyHTML.query(doc, "span[id^='history-pagination-ellipsis']") |> Enum.count() == 1

    assert LazyHTML.query(doc, "#history-pagination-page-size option[selected]")
           |> LazyHTML.attribute("value") == ["10"]

    assert LazyHTML.query(
             doc,
             "#history-pagination-page-size-form[phx-change='history-paginate']"
           )
           |> Enum.count() == 1

    assert LazyHTML.query(doc, "#history-pagination-page-size-form #history-pagination-page-size")
           |> Enum.count() == 1

    assert LazyHTML.query(doc, "#history-pagination-jump-form[phx-submit='history-paginate']")
           |> Enum.count() == 1

    assert LazyHTML.query(doc, "#history-pagination-first")
           |> LazyHTML.attribute("phx-value-page") == ["1"]

    assert LazyHTML.query(doc, "#history-pagination-last")
           |> LazyHTML.attribute("phx-value-page") == ["47"]

    assert LazyHTML.query(doc, "#history-pagination-next") |> LazyHTML.attribute("phx-value-page") ==
             ["4"]

    assert LazyHTML.query(doc, "#history-pagination-page-3[disabled]") |> Enum.count() == 1

    assert LazyHTML.query(doc, "#history-pagination-jump") |> LazyHTML.attribute("value") == ["3"]

    assert LazyHTML.query(doc, "#history-pagination-page-2")
           |> LazyHTML.attribute("phx-value-pagination-id") == ["history-pagination"]
  end

  test "empty collections expose a summary and no enabled navigation" do
    doc =
      render_component(&Pagination.pagination/1,
        id: "empty-pagination",
        page: 1,
        page_size: 10,
        total_count: 0,
        event: "paginate"
      )
      |> LazyHTML.from_document()

    assert LazyHTML.query(doc, "#empty-pagination-summary") |> LazyHTML.text() |> String.trim() ==
             "0 items"

    for selector <- [
          "#empty-pagination-first",
          "#empty-pagination-previous",
          "#empty-pagination-next",
          "#empty-pagination-last"
        ] do
      assert LazyHTML.query(doc, "#{selector}[disabled]") |> Enum.count() == 1
    end

    assert LazyHTML.query(doc, "#empty-pagination-jump-form button[disabled]") |> Enum.count() ==
             1
  end

  test "two instances have distinct IDs and event namespaces" do
    doc =
      Enum.map_join(["left", "right"], fn id ->
        render_component(&Pagination.pagination/1,
          id: "#{id}-pagination",
          page: 1,
          page_size: 25,
          total_count: 50,
          event: "paginate"
        )
      end)
      |> LazyHTML.from_document()

    assert LazyHTML.query(doc, "#left-pagination") |> Enum.count() == 1
    assert LazyHTML.query(doc, "#right-pagination") |> Enum.count() == 1

    assert LazyHTML.query(doc, "#left-pagination-page-2")
           |> LazyHTML.attribute("phx-value-pagination-id") == ["left-pagination"]

    assert LazyHTML.query(doc, "#right-pagination-page-2")
           |> LazyHTML.attribute("phx-value-pagination-id") == ["right-pagination"]
  end

  test "renders page-window tokens in order and keeps the full control set" do
    html =
      render_component(&Pagination.pagination/1,
        id: "ordered-pagination",
        page: 10,
        page_size: 10,
        total_count: 200,
        event: "paginate"
      )

    assert offset!(html, ~s(id="ordered-pagination-page-1")) <
             offset!(html, ~s(id="ordered-pagination-ellipsis-1"))

    assert offset!(html, ~s(id="ordered-pagination-ellipsis-1")) <
             offset!(html, ~s(id="ordered-pagination-page-9"))

    assert offset!(html, ~s(id="ordered-pagination-page-11")) <
             offset!(html, ~s(id="ordered-pagination-ellipsis-5"))

    assert offset!(html, ~s(id="ordered-pagination-ellipsis-5")) <
             offset!(html, ~s(id="ordered-pagination-page-20"))

    doc = LazyHTML.from_document(html)
    assert LazyHTML.query(doc, "#ordered-pagination-first") |> Enum.count() == 1
    assert LazyHTML.query(doc, "#ordered-pagination-previous") |> Enum.count() == 1
    assert LazyHTML.query(doc, "#ordered-pagination-next") |> Enum.count() == 1
    assert LazyHTML.query(doc, "#ordered-pagination-last") |> Enum.count() == 1
    assert LazyHTML.query(doc, "#ordered-pagination-jump-form button") |> Enum.count() == 1
    refute html =~ ~s(role="listitem")
  end

  test "uses one toolbar container for summary, controls, and navigation" do
    doc =
      render_component(&Pagination.pagination/1,
        id: "toolbar-pagination",
        page: 3,
        page_size: 10,
        total_count: 463,
        event: "paginate"
      )
      |> LazyHTML.from_document()

    assert LazyHTML.query(doc, "#toolbar-pagination > div") |> Enum.count() == 1
  end

  test "supports compact presentation options and fixed page sizes" do
    doc =
      render_component(&Pagination.pagination/1,
        id: "compact-pagination",
        page: 2,
        page_size: 10,
        page_size_options: [10],
        total_count: 100,
        sibling_count: 0,
        show_jump?: false,
        event: "paginate",
        target: "#owner"
      )
      |> LazyHTML.from_document()

    assert LazyHTML.query(doc, "#compact-pagination-page-size-form") |> Enum.count() == 0
    assert LazyHTML.query(doc, "#compact-pagination-jump-form") |> Enum.count() == 0

    assert LazyHTML.query(doc, "#compact-pagination-page-1") |> LazyHTML.attribute("phx-target") ==
             ["#owner"]

    assert LazyHTML.query(doc, "#compact-pagination-page-2[aria-current='page']")
  end

  test "exposes busy state and rejects impossible effective metadata" do
    doc =
      render_component(&Pagination.pagination/1,
        id: "busy-pagination",
        page: 2,
        page_size: 10,
        total_count: 30,
        loading?: true,
        event: "paginate"
      )
      |> LazyHTML.from_document()

    assert LazyHTML.query(doc, "#busy-pagination[aria-busy='true']") |> Enum.count() == 1
    assert LazyHTML.query(doc, "#busy-pagination-next[disabled]") |> Enum.count() == 1

    assert_raise ArgumentError, fn ->
      render_component(&Pagination.pagination/1,
        id: "invalid-pagination",
        page: 2,
        page_size: 10,
        total_count: 1,
        event: "paginate"
      )
    end
  end

  defp offset!(html, token) do
    {offset, _length} = :binary.match(html, token)
    offset
  end
end
