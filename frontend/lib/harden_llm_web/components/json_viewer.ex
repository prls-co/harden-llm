defmodule HardenLlmWeb.JsonViewer do
  @moduledoc """
  Small, transport-free JSON tree viewer for Phoenix components.

  Callers provide already-decoded JSON-compatible data. The viewer deliberately
  does not fetch, parse, authorize, or serialize a value for copying. Those
  concerns remain with the host that owns the data.
  """

  use Phoenix.Component

  attr :id, :string, required: true
  attr :data, :any, required: true
  attr :expand_depth, :integer, default: 1
  attr :class, :any, default: nil

  @doc "Renders a foldable tree for a decoded JSON value."
  def json_viewer(assigns) do
    assigns = assign_new(assigns, :expand_depth, fn -> 1 end)

    ~H"""
    <div id={@id} class={[@class, "json-viewer"]} data-json-viewer>
      <.json_node
        id={node_id(@id, [])}
        root_id={@id}
        value={@data}
        path={[]}
        depth={0}
        expand_depth={@expand_depth}
      />
    </div>
    """
  end

  attr :id, :string, required: true
  attr :value, :any, required: true
  attr :root_id, :string, required: true
  attr :path, :list, required: true
  attr :depth, :integer, required: true
  attr :expand_depth, :integer, required: true
  attr :label, :any, default: nil

  @doc false
  defp json_node(assigns) do
    ~H"""
    <%= if json_container?(@value) do %>
      <details id={@id} class="json-viewer-node" open={open_by_default?(@depth, @expand_depth)}>
        <summary>
          <span :if={not is_nil(@label)} class="json-viewer-key">{json_key(@label)}</span>
          <span class="json-viewer-type">{json_container_label(@value)}</span>
        </summary>
        <div class="json-viewer-children">
          <.json_node
            :for={{key, child, child_path} <- json_entries(@value, @path)}
            id={node_id(@root_id, child_path)}
            root_id={@root_id}
            value={child}
            label={key}
            path={child_path}
            depth={@depth + 1}
            expand_depth={@expand_depth}
          />
        </div>
      </details>
    <% else %>
      <div
        id={@id}
        class={[
          "json-viewer-leaf",
          if(is_nil(@label), do: "json-viewer-root-leaf", else: nil)
        ]}
      >
        <span :if={not is_nil(@label)} class="json-viewer-key">{json_key(@label)}</span>
        <span class="json-viewer-value">{json_scalar(@value)}</span>
      </div>
    <% end %>
    """
  end

  defp node_id(root_id, []), do: "#{root_id}-root"

  defp node_id(root_id, path) do
    encoded_path = path |> Jason.encode!() |> Base.url_encode64(padding: false)
    "#{root_id}-node-#{encoded_path}"
  end

  defp json_container?(value), do: is_map(value) or is_list(value)

  defp open_by_default?(depth, expand_depth), do: depth < max(expand_depth, 0)

  defp json_entries(value, path) when is_map(value) do
    value
    |> Map.keys()
    |> Enum.each(&ensure_json_key!/1)

    value
    |> Enum.sort_by(fn {key, _child} -> key end)
    |> Enum.map(fn {key, child} -> {key, child, path ++ [key]} end)
  end

  defp json_entries(value, path) when is_list(value) do
    Enum.with_index(value)
    |> Enum.map(fn {child, index} -> {index, child, path ++ [index]} end)
  end

  defp ensure_json_key!(key) when is_binary(key), do: :ok

  defp ensure_json_key!(key) do
    raise ArgumentError,
          "JsonViewer expects JSON object keys to be strings, got: #{inspect(key)}"
  end

  defp json_container_label(value) when is_map(value), do: "{#{map_size(value)} keys}"
  defp json_container_label(value) when is_list(value), do: "[#{length(value)} items]"

  defp json_key(index) when is_integer(index), do: "[#{index}]"
  defp json_key(key), do: Jason.encode!(key) <> ":"

  defp json_scalar(nil), do: "null"
  defp json_scalar(value) when is_boolean(value) or is_number(value), do: Jason.encode!(value)
  defp json_scalar(value) when is_binary(value), do: Jason.encode!(value)

  defp json_scalar(value) do
    raise ArgumentError,
          "JsonViewer expects JSON-compatible values, got: #{inspect(value)}"
  end
end
