defmodule HardenLlmWeb.ProfileWidgetEditorContractTest do
  use ExUnit.Case, async: true

  import Phoenix.LiveViewTest, only: [render_component: 2]
  import HardenLlmWeb.APIFixtures, only: [profile: 2]
  import HardenLlmWeb.ProfileWidgetTestAssertions, only: [assert_numeric_option_matrix: 2]

  alias HardenLlmWeb.{ProfileForm, ProfileWidgetComponent}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-044
  # PLAN-HLLM-WIDGET-PARITY-001 WEB-TEST-100 WEB-TEST-101 WEB-TEST-102

  test "profile editor option and capability controls keep their rendered contract" do
    form = Phoenix.Component.to_form(ProfileForm.empty_form(%{}), as: :profile)

    html =
      render_component(&ProfileWidgetComponent.profile_editor/1,
        form: form,
        id_prefix: "profile",
        target: "#profile-widget",
        profiles: [],
        options_open: true,
        show_identity_fields: true,
        host_context: "profile_definition"
      )

    assert_numeric_option_matrix(html, "profile")
    assert_capability_checkbox_matrix(html)
  end

  test "profile editor model consumers recompute from each current render" do
    primary = profile("Primary", "primary-model")
    secondary = profile("Secondary", "secondary-model")
    profiles = [primary, secondary]
    defaults = ["gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra"]

    cases = [
      {primary, "primary-custom", nil, [%{"id" => "primary-extra", "label" => "Primary extra"}],
       defaults ++ ["primary-model", "primary-extra", "primary-custom"]},
      {secondary, "secondary-custom", nil, [],
       defaults ++ ["secondary-model", "secondary-custom"]},
      {primary, "refreshed-custom", [%{"id" => "refreshed-model", "label" => "Refreshed"}],
       [%{"id" => "stale-extra", "label" => "Stale"}], ["refreshed-model", "refreshed-custom"]},
      {secondary, "other-custom", [%{"id" => "other-instance-model", "label" => "Other"}], [],
       ["other-instance-model", "other-custom"]}
    ]

    Enum.each(cases, fn {selected_profile, current_model, catalog, extras, expected} ->
      html = render_profile_editor(selected_profile, profiles, current_model, catalog, extras)
      assert_model_consumers(html, expected)
    end)
  end

  test "partial parent fold assigns replace only supplied booleans" do
    primary = profile("Primary", "primary-model")

    {:ok, socket} =
      ProfileWidgetComponent.mount(%Phoenix.LiveView.Socket{assigns: %{__changed__: %{}}})

    {:ok, socket} =
      ProfileWidgetComponent.update(
        %{
          id_prefix: "widget",
          profiles: [primary],
          selected_profile_id: "Primary",
          config_open: true,
          credential_open: true,
          options_open: true,
          retry_open: true,
          pricing_open: true,
          fold_disabled: true,
          target_config_open: %{"target.options" => true}
        },
        socket
      )

    {:ok, socket} =
      ProfileWidgetComponent.update(
        %{
          profiles: [primary],
          selected_profile_id: "Primary",
          config_open: false,
          credential_open: nil,
          options_open: "invalid",
          retry_open: false,
          fold_disabled: false
        },
        socket
      )

    assert socket.assigns.main_config_open == false
    assert socket.assigns.main_credential_open == true
    assert socket.assigns.main_options_open == true
    assert socket.assigns.main_retry_open == false
    assert socket.assigns.main_pricing_open == true
    assert socket.assigns.fold_disabled == false
    assert socket.assigns.target_config_open == %{"target.options" => true}
  end

  defp render_profile_editor(
         profile_state,
         profiles,
         current_model_id,
         model_catalog,
         model_options
       ) do
    form_params =
      profile_state
      |> ProfileForm.profile_form()
      |> Map.put("modelId", current_model_id)

    render_component(&ProfileWidgetComponent.profile_editor/1,
      form: Phoenix.Component.to_form(form_params, as: :profile),
      id_prefix: "profile",
      target: "#profile-widget",
      profiles: profiles,
      model_catalog: model_catalog,
      model_options: model_options,
      options_open: true,
      host_context: "profile_definition"
    )
  end

  defp assert_model_consumers(html, expected_ids) do
    doc = LazyHTML.from_document(html)

    combobox_ids =
      LazyHTML.query(doc, "#profile_modelId-options [role='option']")
      |> LazyHTML.attribute("data-value")

    datalist_ids =
      LazyHTML.query(doc, "#profile-model-options option")
      |> LazyHTML.attribute("value")

    count =
      LazyHTML.query(doc, ".ullm-model-slot-field > .ullm-field-help")
      |> LazyHTML.text()
      |> String.trim()

    assert combobox_ids == expected_ids
    assert datalist_ids == expected_ids
    assert count == "#{length(expected_ids)} options"
  end

  defp assert_capability_checkbox_matrix(html) do
    doc = LazyHTML.from_document(html)

    fields = [
      {"supportsTemperature", "Supports temperature"},
      {"supportsContractedStructuredOutput", "Supports contracted structured output"},
      {"supportsWebSearch", "Supports native web search"}
    ]

    expected_names = Enum.map(fields, fn {field, _} -> "profile[#{field}]" end)
    checkboxes = LazyHTML.query(doc, "input[type='checkbox']")

    names =
      LazyHTML.attribute(checkboxes, "name")
      |> Enum.filter(&String.starts_with?(&1, "profile[supports"))

    assert names == expected_names

    Enum.each(fields, fn {field, label} ->
      name = "profile[#{field}]"
      pair = LazyHTML.query(doc, ~s(input[name="#{name}"]))
      assert LazyHTML.attribute(pair, "type") == ["hidden", "checkbox"]
      assert LazyHTML.attribute(pair, "value") == ["false", "true"]
      assert LazyHTML.attribute(pair, "phx-change") == ["profile-draft-change"]
      assert LazyHTML.attribute(pair, "phx-target") == ["#profile-widget"]
      [id] = LazyHTML.attribute(pair, "id") |> Enum.reject(&(&1 == ""))

      assert LazyHTML.query(doc, ~s(label[for="#{id}"])) |> LazyHTML.text() |> String.trim() ==
               label
    end)
  end
end
