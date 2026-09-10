defmodule HardenLlmWeb.LlmResultComponentsTest do
  use ExUnit.Case, async: true
  import Phoenix.LiveViewTest
  alias HardenLlmWeb.LlmResultComponents

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-066
  test "result rows retain full values for copy and expansion and scope controls to each card" do
    input = "first line\n" <> String.duplicate("long input <tag> ", 40)

    for id <- ["current-result", "history-result"] do
      html =
        render_component(&LlmResultComponents.llm_result/1,
          id: id,
          input: input,
          output: %{"ok" => true}
        )

      doc = LazyHTML.from_document(html)
      assert LazyHTML.query(doc, "##{id}-input") |> LazyHTML.text() == input

      assert LazyHTML.query(doc, "##{id}-copy-input") |> LazyHTML.attribute("data-copy-value") ==
               [input]

      assert LazyHTML.query(doc, "##{id}-copy-output") |> LazyHTML.attribute("data-copy-value") ==
               [Jason.encode!(%{"ok" => true}, pretty: true)]

      assert html =~ "&lt;tag&gt;"
      assert html =~ "Copy input"
      assert html =~ "Copy output"
      assert html =~ "📋"
      assert html =~ ~s(aria-controls="#{id}-input #{id}-output")
      assert html =~ ~s(aria-expanded="false")

      [expand_command] =
        LazyHTML.query(doc, "##{id}-expand") |> LazyHTML.attribute("phx-click")

      card_selector = "##{id}"

      assert [
               ["toggle_class", %{"names" => ["is-expanded"], "to" => ^card_selector}],
               ["toggle_attr", %{"attr" => ["aria-expanded", "true", "false"]}]
             ] = Jason.decode!(expand_command)

      assert html =~ ~s(data-copy-success="✅")
      assert html =~ "&quot;ok&quot;: true"
      assert LazyHTML.query(doc, "##{id} > .llm-result-row") |> Enum.count() == 2
    end
  end

  test "missing content is explicit and cannot be copied" do
    html = render_component(&LlmResultComponents.llm_result/1, id: "missing")
    assert html =~ "Input unavailable."
    assert html =~ "No output."

    assert html |> LazyHTML.from_document() |> LazyHTML.query("button[disabled]") |> Enum.count() ==
             2
  end
end
