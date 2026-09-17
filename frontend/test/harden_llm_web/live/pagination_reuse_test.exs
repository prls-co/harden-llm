defmodule HardenLlmWeb.PaginationReuseTest do
  use HardenLlmWeb.ConnCase, async: true

  import Phoenix.LiveViewTest

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-081

  test "editable searchable and batch consumers share isolated pagination state", %{conn: conn} do
    {:ok, view, _html} =
      live_isolated(conn, HardenLlmWeb.PaginationReuseHarness, session: %{"source" => "api"})

    assert has_element?(view, "#reuse-source", "api")
    assert has_element?(view, "#reuse-record-api-1")
    refute has_element?(view, "#reuse-record-api-3")
    assert has_element?(view, "#reuse-batch-record-batch-1")

    view |> element("#reuse-editable-list #reuse-select-api-1") |> render_click()
    render_change(view, "edit-draft", %{"record-id" => "api-1", "value" => "edited draft"})
    assert has_element?(view, "#reuse-select-api-1[aria-pressed='true']")

    view |> element("#reuse-list-pagination-page-2") |> render_click()
    assert has_element?(view, "#reuse-record-api-3")
    refute has_element?(view, "#reuse-record-api-1")

    view |> element("#reuse-list-pagination-page-1") |> render_click()
    assert has_element?(view, "#reuse-draft-api-1[value='edited draft']")
    assert has_element?(view, "#reuse-select-api-1[aria-pressed='true']")

    view
    |> element("#reuse-list-pagination-page-size-form")
    |> render_change(%{
      "pagination-id" => "reuse-list-pagination",
      "page-size" => "4",
      "_target" => ["page-size"]
    })

    assert has_element?(view, "#reuse-list-pagination-summary", "1-4 of 9")
    assert has_element?(view, "#reuse-record-api-4")
    refute has_element?(view, "#reuse-record-api-5")

    render_change(view, "search", %{"search" => "item 9"})
    assert has_element?(view, "#reuse-list-pagination-summary", "1-1 of 1")
    assert has_element?(view, "#reuse-record-api-9")
    refute has_element?(view, "#reuse-record-api-1")
    assert has_element?(view, "#reuse-batch-pagination-summary", "1-2 of 7")

    view |> element("#reuse-batch-pagination-page-2") |> render_click()
    assert has_element?(view, "#reuse-batch-record-batch-3")
    assert has_element?(view, "#reuse-list-pagination-summary", "1-1 of 1")
    assert has_element?(view, "#reuse-record-api-9")
  end
end
