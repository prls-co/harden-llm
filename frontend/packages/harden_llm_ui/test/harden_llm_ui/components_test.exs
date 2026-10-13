defmodule HardenLlmUI.ComponentsTest do
  use ExUnit.Case, async: true
  import Phoenix.ConnTest
  import Phoenix.LiveViewTest
  @endpoint HardenLlmUI.TestEndpoint

  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-429
  test "independent host keeps two profile picker and fold instances separate" do
    {:ok, view, _html} = live(build_conn(), "/components")
    assert has_element?(view, "#component-messages", "[]")
    assert has_element?(view, "#left-run_selectedProfileId[value='CPA Responses']")
    assert has_element?(view, "#right-run_selectedProfileId[value='CPA Responses']")
    refute has_element?(view, "#left-model-options")
    refute has_element?(view, "#right-model-options")

    view |> element("#left-model-config-toggle") |> render_click()
    assert has_element?(view, "#left-model-options")
    refute has_element?(view, "#right-model-options")

    view
    |> element("#left-run_selectedProfileId")
    |> render_change(%{"run" => %{"selectedProfileId" => "Repair Provider"}})

    assert has_element?(view, "#left-run_selectedProfileId[value='Repair Provider']")
    assert has_element?(view, "#right-run_selectedProfileId[value='CPA Responses']")
    assert render(view) =~ "profile_widget_selection"
    assert render(view) =~ "left"
  end

  test "independent host renders input, response, stats, history and pagination with isolated controls" do
    {:ok, view, _} = live(build_conn(), "/components")

    for id <- ["left", "right"] do
      assert has_element?(view, "##{id}-prompt", id <> " input")
      assert has_element?(view, "##{id}-response", "ok")
      assert has_element?(view, "##{id}-history", "Return an object with ok true.")
      assert has_element?(view, "##{id}-pages-page-2")
      assert has_element?(view, "##{id}-stats-content[hidden]")
    end

    view |> element("#left-stats-summary") |> render_click()
    refute has_element?(view, "#left-stats-content[hidden]")
    assert has_element?(view, "#right-stats-content[hidden]")
    view |> element("#left-history-trace-summary") |> render_click()
    assert has_element?(view, "#left-history-trace-content[hidden]")
    assert has_element?(view, "#right-history-trace-content[hidden]")
  end

  test "package code imports no host identity, state or router implementation" do
    for path <- Path.wildcard(Path.expand("../../lib/**/*.ex", __DIR__)) do
      source = File.read!(path)
      refute source =~ "HardenLlmWeb", path
      refute source =~ "HardenLlm.Reference", path
      refute source =~ "PrlsWeb", path
      refute source =~ "Ecto", path
    end
  end
end
