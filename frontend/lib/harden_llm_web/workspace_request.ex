defmodule HardenLlmWeb.WorkspaceRequest do
  @moduledoc false

  alias HardenLlmWeb.WorkspaceSchema

  @max_model_length 512

  def build(params) when is_map(params) do
    model = String.trim(value(params, "model"))
    prompt = value(params, "userPrompt")
    instructions = value(params, "systemPrompt")

    cond do
      model == "" ->
        {:error, "Choose a model or enter its ID."}

      String.length(model) > @max_model_length ->
        {:error, "Model IDs may contain at most 512 characters."}

      String.trim(prompt) == "" ->
        {:error, "Enter a prompt before submitting."}

      true ->
        build_options(params, model, prompt, instructions)
    end
  end

  def build(_), do: {:error, "The request form is invalid."}

  def restore(%{"request" => request}) when is_map(request) do
    schema = get_in(request, ["text", "format", "schema"])

    %{
      "model" => request["model"] || "",
      "systemPrompt" => request["instructions"] || "",
      "userPrompt" => request["input"] || "",
      "structured" => if(is_map(schema), do: "true", else: "false"),
      "schema" => if(is_map(schema), do: Jason.encode!(schema, pretty: true), else: ""),
      "recoveryJson" =>
        case get_in(request, ["harden", "recovery"]) do
          recovery when is_map(recovery) -> Jason.encode!(recovery, pretty: true)
          _ -> ""
        end,
      "reasoningEffort" => get_in(request, ["reasoning", "effort"]) || ""
    }
  end

  def restore(_), do: %{}

  def default_form do
    %{
      "model" => "",
      "systemPrompt" => "",
      "userPrompt" => "",
      "structured" => "false",
      "schema" => "",
      "recoveryJson" => "",
      "reasoningEffort" => ""
    }
  end

  defp build_options(params, model, prompt, instructions) do
    with {:ok, schema} <- parse_schema(params),
         {:ok, recovery} <- parse_recovery(params) do
      payload = %{
        "model" => model,
        "input" => prompt,
        "store" => false,
        "harden" => %{"cache" => "off", "diagnostics" => true}
      }

      payload =
        if String.trim(instructions) == "",
          do: payload,
          else: Map.put(payload, "instructions", instructions)

      payload =
        if schema do
          Map.put(payload, "text", %{
            "format" => %{
              "type" => "json_schema",
              "name" => "structured_response",
              "schema" => schema,
              "strict" => true
            }
          })
        else
          payload
        end

      payload = if recovery, do: put_in(payload, ["harden", "recovery"], recovery), else: payload

      payload =
        case String.trim(value(params, "reasoningEffort")) do
          "" -> payload
          effort -> Map.put(payload, "reasoning", %{"effort" => effort})
        end

      {:ok, payload}
    end
  end

  defp parse_schema(params) do
    if value(params, "structured") in ["true", "on", "1"] do
      case WorkspaceSchema.check(value(params, "schema")) do
        {:ok, schema, _message} when is_map(schema) -> {:ok, schema}
        {:ok, nil, _message} -> {:error, "Enter a JSON Schema for structured output."}
        {:error, message} -> {:error, message}
      end
    else
      {:ok, nil}
    end
  end

  defp parse_recovery(params) do
    case String.trim(value(params, "recoveryJson")) do
      "" ->
        {:ok, nil}

      text ->
        case Jason.decode(text) do
          {:ok, recovery} when is_map(recovery) -> {:ok, recovery}
          _ -> {:error, "Recovery policy must be a JSON object."}
        end
    end
  end

  defp value(params, key) do
    case Map.get(params, key, "") do
      value when is_binary(value) -> value
      _ -> ""
    end
  end
end
