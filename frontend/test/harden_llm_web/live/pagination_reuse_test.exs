defmodule HardenLlmWeb.PaginationReuseTest do
  use HardenLlmWeb.ConnCase, async: true

  import Phoenix.LiveViewTest

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-081

  test "editable searchable and batch consumers share isolated pagination state", %{conn: conn} do
    {:ok, view, _html} =
      live_isolated(conn, HardenLlmWeb.PaginationReuseHarness, session: %{"source" => "fixture"})

    assert has_element?(view, "#reuse-source", "fixture")
    assert has_element?(view, "#reuse-record-fixture-1")
    refute has_element?(view, "#reuse-record-fixture-3")
    assert has_element?(view, "#reuse-batch-record-batch-1")

    view |> element("#reuse-editable-list #reuse-select-fixture-1") |> render_click()

    view
    |> form("#reuse-edit-form-fixture-1")
    |> render_change(%{"record-id" => "fixture-1", "value" => "edited draft"})

    assert has_element?(view, "#reuse-select-fixture-1[aria-pressed='true']")

    view |> element("#reuse-list-pagination-page-2") |> render_click()
    assert has_element?(view, "#reuse-record-fixture-3")
    refute has_element?(view, "#reuse-record-fixture-1")

    view |> element("#reuse-list-pagination-page-1") |> render_click()
    assert has_element?(view, "#reuse-draft-fixture-1[value='edited draft']")
    assert has_element?(view, "#reuse-select-fixture-1[aria-pressed='true']")

    view
    |> element("#reuse-list-pagination-page-size-form")
    |> render_change(%{
      "pagination-id" => "reuse-list-pagination",
      "page-size" => "4",
      "_target" => ["page-size"]
    })

    assert has_element?(view, "#reuse-list-pagination-summary", "1-4 of 9")
    assert has_element?(view, "#reuse-record-fixture-4")
    refute has_element?(view, "#reuse-record-fixture-5")

    view |> element("#reuse-list-pagination-page-2") |> render_click()
    assert has_element?(view, "#reuse-list-pagination-summary", "5-8 of 9")
    assert has_element?(view, "#reuse-record-fixture-5")
    assert has_element?(view, "#reuse-record-fixture-8")
    refute has_element?(view, "#reuse-record-fixture-4")

    view
    |> form("#reuse-list-pagination-jump-form", %{"page" => "3"})
    |> render_submit()

    assert has_element?(view, "#reuse-list-pagination-summary", "9-9 of 9")
    assert has_element?(view, "#reuse-record-fixture-9")
    refute has_element?(view, "#reuse-record-fixture-8")

    view
    |> form("#reuse-list-pagination-jump-form", %{"page" => "1"})
    |> render_submit()

    assert has_element?(view, "#reuse-list-pagination-summary", "1-4 of 9")

    view |> form("#reuse-search-form", %{"search" => "item 9"}) |> render_change()
    assert has_element?(view, "#reuse-list-pagination-summary", "1-1 of 1")
    assert has_element?(view, "#reuse-record-fixture-9")
    refute has_element?(view, "#reuse-record-fixture-1")
    assert has_element?(view, "#reuse-batch-pagination-summary", "1-2 of 7")

    view
    |> element("#reuse-batch-pagination-page-size-form")
    |> render_change(%{
      "pagination-id" => "reuse-batch-pagination",
      "page-size" => "5",
      "_target" => ["page-size"]
    })

    assert has_element?(view, "#reuse-batch-pagination-summary", "1-5 of 7")
    assert has_element?(view, "#reuse-batch-record-batch-5")

    view |> element("#reuse-batch-pagination-page-2") |> render_click()
    assert has_element?(view, "#reuse-batch-pagination-summary", "6-7 of 7")
    assert has_element?(view, "#reuse-batch-record-batch-6")
    refute has_element?(view, "#reuse-batch-record-batch-5")

    view
    |> form("#reuse-batch-pagination-jump-form", %{"page" => "1"})
    |> render_submit()

    assert has_element?(view, "#reuse-batch-pagination-summary", "1-5 of 7")
    assert has_element?(view, "#reuse-batch-record-batch-5")

    view
    |> form("#reuse-batch-scope-form", %{"scope" => "short"})
    |> render_change()

    assert has_element?(view, "#reuse-batch-pagination-summary", "1-3 of 3")
    assert has_element?(view, "#reuse-batch-record-batch-3")
    refute has_element?(view, "#reuse-batch-record-batch-5")
    assert has_element?(view, "#reuse-list-pagination-summary", "1-1 of 1")
    assert has_element?(view, "#reuse-record-fixture-9")
  end
end
