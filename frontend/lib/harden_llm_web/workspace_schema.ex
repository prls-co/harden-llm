defmodule HardenLlmWeb.WorkspaceSchema do
  @moduledoc false

  @schema_keywords ~w($schema $defs additionalProperties allOf anyOf const default definitions description enum examples exclusiveMaximum exclusiveMinimum format items maxItems maxLength maximum minItems minLength minimum multipleOf not oneOf pattern prefixItems properties propertyOrdering required title type uniqueItems)
  @contracted_schema_keywords ~w(type properties required additionalProperties items description enum)
  @schema_types ~w(object array string number integer boolean)

  def check(value) when is_binary(value) do
    text = String.trim(value)

    if text == "" do
      {:ok, nil, ""}
    else
      case Jason.decode(text) do
        {:ok, schema} when is_map(schema) ->
          cond do
            not schema_object?(schema) ->
              {:error,
               "schemaJson must be a JSON Schema object. Generate it from shorthand first."}

            true ->
              case validate_contracted_schema(schema) do
                :ok -> {:ok, schema, "Schema valid."}
                {:error, message} -> {:error, message}
              end
          end

        {:ok, _} ->
          {:error, "schemaJson must be a JSON object."}

        {:error, _} ->
          {:error, "schemaJson must be valid JSON."}
      end
    end
  end

  def check(_value), do: {:error, "schemaJson must be valid JSON."}

  def generate_schema(value) do
    case Jason.decode(String.trim(value || "{}")) do
      {:ok, shorthand} when is_map(shorthand) ->
        schema = shorthand_schema(shorthand) |> prepare_schema() |> Map.delete("$schema")

        case validate_contracted_schema(schema) do
          :ok -> {:ok, schema, "Schema generated."}
          {:error, message} -> {:error, message}
        end

      {:ok, _} ->
        {:error, "schemaShorthand must be a JSON object."}

      {:error, _} ->
        {:error, "schemaShorthand must be valid JSON."}
    end
  end

  defp shorthand_schema(value) when is_map(value) do
    if schema_object?(value) do
      normalize_schema_object(value)
    else
      properties =
        Map.new(value, fn {key, descriptor} -> {key, schema_descriptor(descriptor)} end)

      %{
        "type" => "object",
        "properties" => properties,
        "required" => Map.keys(properties),
        "additionalProperties" => false
      }
    end
  end

  defp schema_descriptor(value) when is_binary(value) do
    %{"type" => normalize_type(value) || "string"}
  end

  defp schema_descriptor(value) when is_list(value) do
    %{"type" => "array", "items" => schema_descriptor(List.first(value) || "string")}
  end

  defp schema_descriptor(value) when is_map(value), do: shorthand_schema(value)
  defp schema_descriptor(_value), do: %{"type" => "string"}

  defp normalize_schema_object(value) do
    value =
      if is_binary(value["type"]),
        do: Map.put(value, "type", normalize_type(value["type"]) || value["type"]),
        else: value

    value =
      if is_map(value["properties"]),
        do:
          Map.put(
            value,
            "properties",
            Map.new(value["properties"], fn {key, descriptor} ->
              {key, schema_descriptor(descriptor)}
            end)
          ),
        else: value

    if is_map(value["items"]),
      do: Map.put(value, "items", schema_descriptor(value["items"])),
      else: value
  end

  defp prepare_schema(value) when is_map(value) do
    value =
      value
      |> maybe_prepare_properties()
      |> maybe_prepare_items()

    if value["type"] == "object" || is_map(value["properties"]),
      do: Map.put_new(value, "additionalProperties", false),
      else: value
  end

  defp prepare_schema(value), do: value

  defp maybe_prepare_properties(value) do
    if is_map(value["properties"]),
      do:
        Map.put(
          value,
          "properties",
          Map.new(value["properties"], fn {key, child} -> {key, prepare_schema(child)} end)
        ),
      else: value
  end

  defp maybe_prepare_items(value) do
    if is_map(value["items"]),
      do: Map.put(value, "items", prepare_schema(value["items"])),
      else: value
  end

  defp validate_contracted_schema(schema) when is_map(schema),
    do: validate_schema_node(schema, "", true)

  defp validate_schema_node(node, path, root?) when is_map(node) do
    with :ok <- validate_schema_keys(node, path),
         :ok <- validate_schema_type(node, path, root?),
         :ok <- validate_schema_enum(node, path),
         :ok <- validate_schema_object(node, path),
         :ok <- validate_schema_array(node, path) do
      validate_schema_children(node, path)
    end
  end

  defp validate_schema_node(_node, path, _root?),
    do: {:error, "#{path || "schema"} must be an object."}

  defp validate_schema_keys(node, path) do
    case Enum.find(Map.keys(node), &(&1 not in @contracted_schema_keywords)) do
      nil ->
        :ok

      key ->
        {:error,
         "Unsupported structured output schema at #{json_pointer(path, key)}: " <>
           "#{key} is not part of the utility-llm contracted schema subset."}
    end
  end

  defp validate_schema_type(node, path, root?) do
    type = node["type"]

    cond do
      not is_binary(type) ->
        {:error,
         "Unsupported structured output schema at #{json_pointer(path, "type")}: " <>
           "type must be a contracted string type."}

      type not in @schema_types ->
        {:error,
         "Unsupported structured output schema at #{json_pointer(path, "type")}: " <>
           "#{type} is not a contracted schema type."}

      root? and type != "object" ->
        {:error,
         "Unsupported structured output schema at #{json_pointer(path, "type")}: " <>
           "root schema must be an object."}

      true ->
        :ok
    end
  end

  defp validate_schema_enum(%{"enum" => values}, path)
       when is_list(values) do
    if Enum.all?(values, &scalar_enum_value?/1) do
      :ok
    else
      {:error,
       "Unsupported structured output schema at #{json_pointer(path, "enum")}: " <>
         "enum must contain only scalar values."}
    end
  end

  defp validate_schema_enum(%{"enum" => _values}, path),
    do:
      {:error,
       "Unsupported structured output schema at #{json_pointer(path, "enum")}: " <>
         "enum must contain only scalar values."}

  defp validate_schema_enum(_node, _path), do: :ok

  defp validate_schema_object(%{"type" => "object"} = node, path) do
    cond do
      not is_map(node["properties"]) ->
        {:error,
         "Unsupported structured output schema at #{json_pointer(path, "properties")}: " <>
           "object schemas must define properties."}

      node["additionalProperties"] != false ->
        {:error,
         "Unsupported structured output schema at #{json_pointer(path, "additionalProperties")}: " <>
           "object schemas must set additionalProperties: false."}

      not is_list(node["required"]) ->
        {:error,
         "Unsupported structured output schema at #{json_pointer(path, "required")}: " <>
           "object schemas must list all required properties."}

      Enum.any?(node["required"], &(not Map.has_key?(node["properties"], &1))) ->
        {:error,
         "Unsupported structured output schema at #{json_pointer(path, "required")}: " <>
           "required property is not defined in properties."}

      Enum.any?(Map.keys(node["properties"]), &(&1 not in node["required"])) ->
        {:error,
         "Unsupported structured output schema at #{json_pointer(path, "properties")}: " <>
           "every property must be listed in required."}

      true ->
        :ok
    end
  end

  defp validate_schema_object(_node, _path), do: :ok

  defp validate_schema_array(%{"type" => "array"} = node, path) do
    if is_map(node["items"]) do
      :ok
    else
      {:error,
       "Unsupported structured output schema at #{json_pointer(path, "items")}: " <>
         "array schemas must define a single object-form items schema."}
    end
  end

  defp validate_schema_array(_node, _path), do: :ok

  defp scalar_enum_value?(value),
    do: is_nil(value) or is_binary(value) or is_number(value) or is_boolean(value)

  defp json_pointer(path, key) do
    escaped = key |> to_string() |> String.replace("~", "~0") |> String.replace("/", "~1")
    "#{path}/#{escaped}"
  end

  defp validate_schema_children(node, path) do
    property_result =
      if is_map(node["properties"]) do
        Enum.reduce_while(node["properties"], :ok, fn {key, child}, :ok ->
          case validate_schema_node(child, "#{path}/properties/#{key}", false) do
            :ok -> {:cont, :ok}
            error -> {:halt, error}
          end
        end)
      else
        :ok
      end

    with :ok <- property_result do
      if is_map(node["items"]),
        do: validate_schema_node(node["items"], "#{path}/items", false),
        else: :ok
    end
  end

  defp schema_object?(value) do
    keys = Map.keys(value)
    type = value["type"]

    Enum.any?(keys, &(&1 != "type" and &1 in @schema_keywords)) or
      ((is_binary(type) or is_list(type)) and Enum.all?(keys, &(&1 in @schema_keywords)))
  end

  defp normalize_type(value) do
    %{
      "array" => "array",
      "bool" => "boolean",
      "boolean" => "boolean",
      "double" => "number",
      "float" => "number",
      "int" => "integer",
      "integer" => "integer",
      "list" => "array",
      "null" => "null",
      "number" => "number",
      "object" => "object",
      "str" => "string",
      "string" => "string",
      "text" => "string"
    }[String.downcase(String.trim(value))]
  end
end
