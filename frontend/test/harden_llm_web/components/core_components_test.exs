defmodule HardenLlmWeb.CoreComponentsTest do
  use ExUnit.Case, async: true

  import Phoenix.LiveViewTest

  alias HardenLlmWeb.CoreComponents

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-052

  for type <- ~w(checkbox select textarea number) do
    test "#{type} help is an independent disclosure beside its field label" do
      type = unquote(type)
      id = "setting-#{type}"
      help = "Uses another model call. Repair attempts count toward Max Attempts."

      doc =
        render_component(&CoreComponents.input/1,
          id: id,
          name: "profile[setting]",
          type: type,
          label: "Setting",
          info: help,
          value: if(type == "checkbox", do: true, else: "4"),
          options: ["4", "5"]
        )
        |> LazyHTML.from_document()

      button = LazyHTML.query(doc, "button.ullm-field-label-info")
      assert LazyHTML.text(button) == "?"
      assert LazyHTML.attribute(button, "type") == ["button"]
      assert LazyHTML.attribute(button, "aria-label") == ["Help for Setting"]
      assert LazyHTML.attribute(button, "aria-expanded") == ["false"]
      assert LazyHTML.attribute(button, "aria-controls") == ["#{id}-help"]
      assert LazyHTML.query(doc, "label button") |> Enum.empty?()

      assert LazyHTML.query(doc, ~s(label[for="#{id}"])) |> LazyHTML.text() |> String.trim() ==
               "Setting"

      assert LazyHTML.query(doc, "##{id}-help[hidden]") |> LazyHTML.text() |> String.trim() ==
               help

      [click] = LazyHTML.attribute(button, "phx-click")

      assert Jason.decode!(click) == [
               ["toggle_attr", %{"attr" => ["hidden", ""], "to" => "##{id}-help"}],
               ["toggle_attr", %{"attr" => ["aria-expanded", "true", "false"]}]
             ]

      if type == "checkbox" do
        assert LazyHTML.query(doc, "##{id}[checked]") |> Enum.count() == 1

        assert LazyHTML.query(doc, ~s(input[type="hidden"][name="profile[setting]"]))
               |> LazyHTML.attribute("value") == ["false"]
      end
    end
  end

  test "help targets remain scoped to independent field IDs" do
    doc =
      Enum.map_join(["first-setting", "second-setting"], fn id ->
        render_component(&CoreComponents.input/1,
          id: id,
          name: "setting",
          label: "Setting",
          info: "Help for #{id}",
          value: ""
        )
      end)
      |> LazyHTML.from_document()

    assert LazyHTML.query(doc, "button") |> LazyHTML.attribute("aria-controls") ==
             ["first-setting-help", "second-setting-help"]

    assert LazyHTML.query(doc, "#first-setting-help") |> LazyHTML.text() |> String.trim() ==
             "Help for first-setting"

    assert LazyHTML.query(doc, "#second-setting-help") |> LazyHTML.text() |> String.trim() ==
             "Help for second-setting"
  end

  test "fields without help keep their labels and omit empty disclosures" do
    doc =
      render_component(&CoreComponents.input/1,
        id: "plain-setting",
        name: "setting",
        label: "Setting",
        value: "4",
        hint: "Required",
        hint_id: "setting-hint",
        hint_tone: "error",
        errors: ["Invalid setting"]
      )
      |> LazyHTML.from_document()

    assert LazyHTML.query(doc, ~s(label[for="plain-setting"])) |> LazyHTML.text() == "Setting"
    assert LazyHTML.query(doc, "button, .ullm-field-info-text") |> Enum.empty?()
    assert LazyHTML.query(doc, ~s(#setting-hint[role="alert"])) |> LazyHTML.text() == "Required"

    assert LazyHTML.query(doc, ".ullm-input-error") |> LazyHTML.text() |> String.trim() ==
             "Invalid setting"

    assert LazyHTML.query(doc, ~s(#plain-setting[aria-invalid="true"])) |> Enum.count() == 1
  end

  test "a named field without an explicit ID retains its label and help association" do
    doc =
      render_component(&CoreComponents.input/1,
        name: "profile[setting]",
        label: "Setting",
        info: "Setting help",
        value: "4"
      )
      |> LazyHTML.from_document()

    [id] = LazyHTML.query(doc, "input") |> LazyHTML.attribute("id")
    assert LazyHTML.query(doc, "label") |> LazyHTML.attribute("for") == [id]
    assert LazyHTML.query(doc, "button") |> LazyHTML.attribute("aria-controls") == ["#{id}-help"]

    assert LazyHTML.query(doc, "##{id}-help[hidden]") |> LazyHTML.text() |> String.trim() ==
             "Setting help"
  end
end
