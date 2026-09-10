defmodule HardenLlmWeb.JsonViewerTest do
  use ExUnit.Case, async: true

  import Phoenix.LiveViewTest

  alias HardenLlmWeb.JsonViewer

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-064

  test "preserves decoded JSON types, including strings that look like JSON" do
    html =
      render_component(&JsonViewer.json_viewer/1,
        id: "typed-json",
        data: %{
          "string_number" => "42",
          "string_boolean" => "true",
          "string_null" => "null",
          "string_object" => ~s({"answer":42}),
          "number" => 42,
          "boolean" => true,
          "null" => nil
        }
      )

    assert html =~ ~s(&quot;string_number&quot;:)
    assert html =~ ~s(&quot;42&quot;)
    assert html =~ ~s(&quot;string_boolean&quot;:)
    assert html =~ ~s(&quot;true&quot;)
    assert html =~ ~s(&quot;string_null&quot;:)
    assert html =~ ~s(&quot;null&quot;)
    assert html =~ ~s(&quot;string_object&quot;:)
    assert html =~ "&quot;{\\&quot;answer\\&quot;:42}&quot;"
    assert html =~ ~s(&quot;number&quot;:)
    assert html =~ ">42</span>"
    assert html =~ ~s(&quot;boolean&quot;:)
    assert html =~ ">true</span>"
    assert html =~ ~s(&quot;null&quot;:)
    assert html =~ ">null</span>"
  end

  test "uses path-based instance IDs for keys containing separators and unicode" do
    path = ["a/b", "~key", "café"]
    encoded_path = path |> Jason.encode!() |> Base.url_encode64(padding: false)

    html =
      render_component(&JsonViewer.json_viewer/1,
        id: "instance-a",
        data: %{"a/b" => %{"~key" => %{"café" => "value"}}}
      )

    assert html =~ ~s(id="instance-a-root")
    assert html =~ ~s(id="instance-a-node-#{encoded_path}")
  end

  test "renders root scalars without an empty key column" do
    html = render_component(&JsonViewer.json_viewer/1, id: "scalar", data: "hello")

    assert html =~ ~s(class="json-viewer-leaf json-viewer-root-leaf")
    assert html =~ ~s(&quot;hello&quot;)
    refute html =~ ~s(class="json-viewer-key")
  end

  test "rejects non-JSON object keys and values instead of stringifying them" do
    assert_raise ArgumentError, ~r/object keys to be strings/, fn ->
      render_component(&JsonViewer.json_viewer/1, id: "bad-key", data: %{answer: 42})
    end

    assert_raise ArgumentError, ~r/JSON-compatible values/, fn ->
      render_component(&JsonViewer.json_viewer/1, id: "bad-value", data: %{"value" => self()})
    end
  end

  test "empty containers remain visible and nested containers start folded" do
    html =
      render_component(&JsonViewer.json_viewer/1,
        id: "folds",
        data: %{"empty" => %{}, "nested" => %{"child" => 1}},
        expand_depth: 1
      )

    assert html =~ "{0 keys}"
    assert html =~ "{1 keys}"
    assert html =~ ~s(id="folds-root" class="json-viewer-node" open)

    refute html =~
             ~s(id="folds-node-#{Base.url_encode64(Jason.encode!(["nested"]), padding: false)}" class="json-viewer-node" open)
  end
end
