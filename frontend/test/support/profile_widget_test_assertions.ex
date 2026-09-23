defmodule HardenLlmWeb.ProfileWidgetTestAssertions do
  @moduledoc false

  import ExUnit.Assertions

  def assert_numeric_option_matrix(html, name_prefix) do
    doc = LazyHTML.from_document(html)

    expected = [
      {"maxTokens", "Max Output Tokens", "16000", nil},
      {"temperature", "Temperature", "0.2", "any"},
      {"topP", "Top P", "0.95", "any"},
      {"topK", "Top K", "40", nil}
    ]

    expected_names = Enum.map(expected, fn {field, _, _, _} -> "#{name_prefix}[#{field}]" end)

    actual_names =
      LazyHTML.query(doc, "input[type='number']")
      |> LazyHTML.attribute("name")
      |> Enum.filter(&String.starts_with?(&1, name_prefix <> "["))

    assert actual_names == expected_names

    targets =
      Enum.map(expected, fn {field, label, placeholder, step} ->
        name = "#{name_prefix}[#{field}]"
        input = LazyHTML.query(doc, ~s(input[name="#{name}"][type="number"]))
        assert Enum.count(input) == 1
        assert LazyHTML.attribute(input, "min") == ["0"]
        assert LazyHTML.attribute(input, "placeholder") == [placeholder]
        assert LazyHTML.attribute(input, "phx-change") == ["profile-draft-change"]

        assert LazyHTML.attribute(input, "step") == if(step, do: [step], else: [])

        [id] = LazyHTML.attribute(input, "id")

        assert LazyHTML.query(doc, ~s(label[for="#{id}"])) |> LazyHTML.text() |> String.trim() ==
                 label

        [target] = LazyHTML.attribute(input, "phx-target")
        target
      end)

    assert Enum.uniq(targets) |> length() == 1
  end
end
