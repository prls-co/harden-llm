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

      card_selector = "##{id}"
      toggle_selector = "##{id} > .llm-result-row > .llm-result-toggle"

      assert LazyHTML.query(doc, "##{id}-expand") |> Enum.empty?()
      assert LazyHTML.query(doc, ".llm-result-label") |> Enum.empty?()
      assert LazyHTML.query(doc, toggle_selector) |> Enum.count() == 2

      for {kind, label, emoji} <- [{"input", "Input", "📥"}, {"output", "Output", "📤"}] do
        button = LazyHTML.query(doc, "##{id}-toggle-#{kind}")
        assert LazyHTML.text(button) == emoji
        assert LazyHTML.attribute(button, "type") == ["button"]
        assert LazyHTML.attribute(button, "aria-expanded") == ["false"]
        assert LazyHTML.attribute(button, "aria-controls") == ["#{id}-input #{id}-output"]

        assert LazyHTML.attribute(button, "aria-label") ==
                 ["#{label}: expand or collapse input and output"]

        [command] = LazyHTML.attribute(button, "phx-click")

        assert [
                 ["toggle_class", %{"names" => ["is-expanded"], "to" => ^card_selector}],
                 [
                   "toggle_attr",
                   %{"attr" => ["aria-expanded", "true", "false"], "to" => ^toggle_selector}
                 ]
               ] = Jason.decode!(command)
      end

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

  test "both emoji controls reference host-supplied text IDs" do
    doc =
      render_component(&LlmResultComponents.llm_result/1,
        id: "custom-result",
        input_id: "recorded-input",
        output_id: "run-output"
      )
      |> LazyHTML.from_document()

    assert LazyHTML.query(doc, ".llm-result-toggle") |> LazyHTML.attribute("aria-controls") ==
             ["recorded-input run-output", "recorded-input run-output"]
  end
end
