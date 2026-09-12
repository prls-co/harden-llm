defmodule HardenLlmWeb.LlmResultComponents do
  @moduledoc """
  Transport-free result card: recorded input, output, and a host-supplied stats
  slot. Hosts own execution, resource loading, and collection management.
  Text expansion is local to each card and does not change stored data.
  """
  use Phoenix.Component
  alias Phoenix.LiveView.JS

  attr :id, :string, required: true
  attr :input, :any, default: nil
  attr :output, :any, default: nil
  attr :search, :map, default: nil
  attr :input_id, :string, default: nil
  attr :output_id, :string, default: nil
  attr :copy_output_id, :string, default: nil
  slot :stats
  slot :actions

  def llm_result(assigns) do
    assigns =
      assigns
      |> assign(:input_id, assigns.input_id || "#{assigns.id}-input")
      |> assign(:output_id, assigns.output_id || "#{assigns.id}-output")
      |> assign(:sources, safe_sources(assigns.search))
      |> assign(:output_parts, output_parts(assigns.output, assigns.search))
      |> assign(
        :toggle_text,
        JS.toggle_class("is-expanded", to: "##{assigns.id}")
        |> JS.toggle_attribute({"aria-expanded", "true", "false"},
          to: "##{assigns.id} > .llm-result-row > .llm-result-toggle"
        )
      )

    ~H"""
    <article id={@id} class="llm-result">
      <div class="llm-result-row">
        <.text_toggle
          id={"#{@id}-toggle-input"}
          label="Input"
          emoji="💬"
          controls={"#{@input_id} #{@output_id}"}
          command={@toggle_text}
        />
        <pre id={@input_id} class="llm-result-text"><%= text(@input) || "Input unavailable." %></pre>
        <.copy_button id={"#{@id}-copy-input"} label="Copy input" value={text(@input)} />
        <span :if={@actions != []} class="llm-result-actions">{render_slot(@actions)}</span>
      </div>
      <div class="llm-result-row">
        <.text_toggle
          id={"#{@id}-toggle-output"}
          label="Output"
          emoji="🤖"
          controls={"#{@input_id} #{@output_id}"}
          command={@toggle_text}
        />
        <pre id={@output_id} class="llm-result-text"><%= for part <- @output_parts do %><%= if part.url do %><a href={part.url} target="_blank" rel="noopener noreferrer" class="underline"><%= part.text %></a><% else %><%= part.text %><% end %><% end %></pre>
        <.copy_button
          id={@copy_output_id || "#{@id}-copy-output"}
          label="Copy output"
          value={text(@output)}
        />
      </div>
      <aside :if={@search} aria-label="Web search evidence" class="llm-result-sources">
        <span title="Model token accounting excludes search fees">Search fees unavailable</span>
        <span>🌐 {@search["mode"]}: {if @search["executed"], do: "searched", else: "no search reported"}</span>
        <a :for={source <- @sources} href={source["url"]} target="_blank" rel="noopener noreferrer">{source[
          "title"
        ] || source["url"]}</a>
      </aside>
      <iframe
        :if={@search && @search["entryPointHtml"]}
        title="Google Search suggestions"
        srcdoc={@search["entryPointHtml"]}
        sandbox="allow-popups allow-popups-to-escape-sandbox"
        class="w-full border-0"
      />
      <div :if={@stats != []} class="llm-result-stats">{render_slot(@stats)}</div>
    </article>
    """
  end

  attr :id, :string, required: true
  attr :label, :string, required: true
  attr :emoji, :string, required: true
  attr :controls, :string, required: true
  attr :command, JS, required: true

  defp text_toggle(assigns) do
    ~H"""
    <button
      id={@id}
      type="button"
      class="llm-result-action llm-result-toggle"
      aria-label={"#{@label}: expand or collapse input and output"}
      title={"#{@label}: expand or collapse input and output"}
      aria-controls={@controls}
      aria-expanded="false"
      phx-click={@command}
    >{@emoji}</button>
    """
  end

  attr :id, :string, required: true
  attr :label, :string, required: true
  attr :value, :string, default: nil

  defp copy_button(assigns) do
    ~H"""
    <button
      id={@id}
      type="button"
      class="llm-result-action"
      phx-hook="Clipboard"
      data-copy-value={@value}
      data-copy-success="✅"
      data-copy-error="❌"
      aria-label={@label}
      title={@label}
      disabled={is_nil(@value)}
    >📋</button>
    """
  end

  defp text(nil), do: nil
  defp text(value) when is_binary(value), do: value
  defp text(value), do: Jason.encode!(value, pretty: true)

  defp safe_sources(%{"sources" => sources}) when is_list(sources) do
    Enum.filter(sources, fn
      %{"url" => url} when is_binary(url) ->
        uri = URI.parse(url)

        uri.scheme in ["https", "http"] and is_binary(uri.host) and uri.host != "" and
          is_nil(uri.userinfo)

      _ ->
        false
    end)
  end

  defp safe_sources(_), do: []

  defp output_parts(output, %{"citations" => citations} = search)
       when is_binary(output) and is_list(citations) do
    chars = String.codepoints(output)
    urls = MapSet.new(safe_sources(search), & &1["url"])

    citations =
      Enum.filter(citations, fn c ->
        is_map(c) and is_integer(c["startIndex"]) and is_integer(c["endIndex"])
      end)

    {parts, offset} =
      citations
      |> Enum.sort_by(& &1["startIndex"])
      |> Enum.reduce({[], 0}, fn c, {parts, offset} ->
        first = c["startIndex"]
        last = c["endIndex"]

        if first >= offset and last > first and last <= length(chars) and
             MapSet.member?(urls, c["url"]) do
          {parts ++
             [
               %{text: chars |> Enum.slice(offset, first - offset) |> Enum.join(), url: nil},
               %{text: chars |> Enum.slice(first, last - first) |> Enum.join(), url: c["url"]}
             ], last}
        else
          {parts, offset}
        end
      end)

    parts ++ [%{text: chars |> Enum.drop(offset) |> Enum.join(), url: nil}]
  end

  defp output_parts(output, _), do: [%{text: text(output) || "No output.", url: nil}]
end
