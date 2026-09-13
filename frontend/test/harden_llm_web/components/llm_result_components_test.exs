defmodule HardenLlmWeb.LlmResultComponentsTest do
  use ExUnit.Case, async: true
  import Phoenix.LiveViewTest
  alias HardenLlmWeb.LlmResultComponents

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-066
  test "search evidence belongs to the response and shares each card's existing fold" do
    for id <- ["current-result", "history-result"], mode <- ["native", "jina"] do
      output = "Answer with a source."

      doc =
        render_component(&LlmResultComponents.llm_result/1,
          id: id,
          input_id: "#{id}-recorded-input",
          output_id: "#{id}-answer",
          output: output,
          search: %{
            "mode" => mode,
            "executed" => true,
            "sources" => [%{"url" => "https://example.test/source", "title" => "Source"}],
            "entryPointHtml" => "<a>suggestion</a>"
          },
          stats: [%{inner_block: fn _, _ -> "Model statistics" end}]
        )
        |> LazyHTML.from_document()

      response = LazyHTML.query(doc, "##{id} > .llm-result-row > .llm-result-response")
      details = LazyHTML.query(response, "##{id}-search-details.llm-result-search-details")
      assert Enum.count(details) == 1
      assert LazyHTML.query(response, "pre") |> LazyHTML.text() == output
      assert LazyHTML.text(details) =~ "Search fees unavailable"
      assert LazyHTML.text(details) =~ "#{mode}: searched"
      assert LazyHTML.query(details, "aside a") |> LazyHTML.text() == "Source"
      assert LazyHTML.query(details, "iframe") |> Enum.count() == 1
      assert LazyHTML.query(doc, "##{id} > aside, ##{id} > iframe") |> Enum.empty?()
      assert LazyHTML.query(doc, ".llm-result-stats") |> LazyHTML.text() == "Model statistics"
      assert LazyHTML.query(response, ".llm-result-stats") |> Enum.empty?()
      assert LazyHTML.query(doc, ".llm-result.is-expanded") |> Enum.empty?()
      assert LazyHTML.query(doc, ".llm-result-toggle") |> Enum.count() == 2

      for kind <- ["input", "output"] do
        button = LazyHTML.query(doc, "##{id}-toggle-#{kind}")
        assert LazyHTML.attribute(button, "aria-expanded") == ["false"]

        assert LazyHTML.attribute(button, "aria-controls") == [
                 "#{id}-recorded-input #{id}-answer #{id}-search-details"
               ]

        [command] = LazyHTML.attribute(button, "phx-click")

        assert Jason.decode!(command) == [
                 ["toggle_class", %{"names" => ["is-expanded"], "to" => "##{id}"}],
                 [
                   "toggle_attr",
                   %{
                     "attr" => ["aria-expanded", "true", "false"],
                     "to" => "##{id} > .llm-result-row > .llm-result-toggle"
                   }
                 ]
               ]
      end

      assert LazyHTML.query(doc, "##{id}-copy-output") |> LazyHTML.attribute("data-copy-value") ==
               [output]
    end
  end

  test "search evidence visibility uses the existing card expansion state" do
    css = File.read!(Path.expand("../../../assets/css/app.css", __DIR__))
    assert css =~ ~r/\.llm-result-search-details\s*\{\s*display:\s*none;\s*\}/

    assert css =~
             ~r/\.llm-result\.is-expanded\s+\.llm-result-search-details\s*\{\s*display:\s*block;\s*\}/
  end

  test "inline citations link exact Unicode text and ignore invalid or overlapping offsets" do
    output = "🌐 a source and <text>"
    source = %{"url" => "https://example.test/source", "title" => "Source"}

    citations = [
      Map.merge(source, %{"startIndex" => 4, "endIndex" => 10}),
      Map.merge(source, %{"startIndex" => 5, "endIndex" => 7}),
      Map.merge(source, %{"startIndex" => 99, "endIndex" => 100})
    ]

    html =
      render_component(&LlmResultComponents.llm_result/1,
        id: "inline",
        output: output,
        search: %{
          "mode" => "native",
          "executed" => true,
          "sources" => [source],
          "citations" => citations,
          "entryPointHtml" => "<a>suggestion</a>"
        }
      )

    doc = LazyHTML.from_document(html)
    assert LazyHTML.query(doc, "#inline-output a") |> LazyHTML.text() == "source"
    assert LazyHTML.query(doc, "#inline-output a") |> Enum.count() == 1
    assert LazyHTML.query(doc, "#inline-output") |> LazyHTML.text() == output

    assert LazyHTML.query(doc, "#inline-copy-output") |> LazyHTML.attribute("data-copy-value") ==
             [output]

    assert LazyHTML.query(doc, "iframe") |> LazyHTML.attribute("sandbox") == [
             "allow-popups allow-popups-to-escape-sandbox"
           ]
  end

  test "search sources are clickable without changing structured output or trusting unsafe URLs" do
    html =
      render_component(&LlmResultComponents.llm_result/1,
        id: "search-result",
        output: %{"answer" => "source"},
        search: %{
          "mode" => "native",
          "executed" => true,
          "sources" => [
            %{"url" => "https://example.test/source", "title" => "<source>"},
            %{"url" => "javascript:alert(1)", "title" => "bad"}
          ]
        }
      )

    doc = LazyHTML.from_document(html)

    assert LazyHTML.query(doc, "aside a") |> LazyHTML.attribute("href") == [
             "https://example.test/source"
           ]

    assert html =~ "&lt;source&gt;"
    assert html =~ "native: searched"
    refute html =~ "javascript:"

    assert LazyHTML.query(doc, "#search-result-copy-output")
           |> LazyHTML.attribute("data-copy-value") == [
             Jason.encode!(%{"answer" => "source"}, pretty: true)
           ]
  end

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
      assert LazyHTML.query(doc, ".llm-result-search-details") |> Enum.empty?()
      assert LazyHTML.query(doc, ".llm-result-label") |> Enum.empty?()
      assert LazyHTML.query(doc, toggle_selector) |> Enum.count() == 2

      for {kind, label, emoji} <- [{"input", "Input", "💬"}, {"output", "Output", "🤖"}] do
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
