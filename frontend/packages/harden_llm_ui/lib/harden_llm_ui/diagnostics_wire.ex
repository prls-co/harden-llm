defmodule HardenLlmUI.DiagnosticsWire.Contract do
  @moduledoc false

  def load(path) do
    document = YamlElixir.read_from_file!(path)

    root =
      ExJsonSchema.Schema.resolve(
        %{
          "$schema" => "http://json-schema.org/draft-07/schema#",
          "components" => %{"schemas" => normalize(document["components"]["schemas"])}
        },
        custom_format_validator: {__MODULE__, :valid_format?}
      )

    routes =
      for {path, operations} <- document["paths"],
          {method, operation} <- operations,
          method in ~w(get post put delete patch),
          reduce: [] do
        routes ->
          pattern = path |> Regex.escape() |> String.replace(~r/\\\{[^}]+\\\}/, "[^/]+")
          [{Regex.compile!("^" <> pattern <> "$"), method, operation["responses"]} | routes]
      end

    routes =
      Enum.sort_by(routes, fn {pattern, _, _} -> length(String.split(pattern.source, "[^/]+")) end)

    {root, routes}
  end

  # OpenAPI UUID and binary formats are not built into ExJsonSchema.
  def valid_format?("uuid", value),
    do: Regex.match?(~r/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i, value)

  def valid_format?("binary", value), do: is_binary(value)

  # OpenAPI's nullable keyword maps directly to a JSON Schema null branch.
  # All constraints and references come from the canonical contract.
  defp normalize(value) when is_map(value) do
    normalized =
      Map.new(value, fn
        {"pattern", pattern} when is_binary(pattern) ->
          pattern =
            Regex.replace(~r/\\u([0-9a-fA-F]{4})/, pattern, fn _, hex -> "\\x{" <> hex <> "}" end)

          {"pattern", "(*UTF)" <> pattern}

        {key, item} ->
          {key, normalize(item)}
      end)

    if normalized["nullable"] == true do
      %{"anyOf" => [Map.delete(normalized, "nullable"), %{"type" => "null"}]}
    else
      normalized
    end
  end

  defp normalize(value) when is_list(value), do: Enum.map(value, &normalize/1)
  defp normalize(value), do: value
end

defmodule HardenLlmUI.DiagnosticsWire do
  @moduledoc "One decoder boundary driven by the bundled canonical OpenAPI contract."
  @contract_path Path.expand("../../priv/openapi.yaml", __DIR__)
  @external_resource @contract_path
  @contract HardenLlmUI.DiagnosticsWire.Contract.load(@contract_path)

  def valid?(schema, value) do
    {root, _routes} = @contract
    ExJsonSchema.Validator.valid_fragment?(root, "#/components/schemas/" <> schema, value) == true
  end

  def valid_response?(method, path, status, value, media_type \\ "application/json") do
    {root, routes} = @contract
    path = URI.parse(path).path
    method = Atom.to_string(method)

    case Enum.find(routes, fn {pattern, expected, _} ->
           expected == method and Regex.match?(pattern, path)
         end) do
      {_pattern, _method, responses} ->
        case get_in(responses, [
               Integer.to_string(status),
               "content",
               media_type,
               "schema"
             ]) do
          nil ->
            status == 204 and is_nil(value) and Map.has_key?(responses, "204")

          %{"$ref" => "#/components/schemas/" <> schema} ->
            valid?(schema, value)

          schema when is_map(schema) ->
            ExJsonSchema.Validator.valid_fragment?(root, schema, value) == true
        end

      nil ->
        false
    end
  end
end
