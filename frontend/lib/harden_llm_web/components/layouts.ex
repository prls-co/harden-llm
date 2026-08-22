defmodule HardenLlmWeb.Layouts do
  @moduledoc """
  This module holds layouts and related functionality
  used by your application.
  """
  use HardenLlmWeb, :html

  # Embed all files in layouts/* within this module.
  # The default root.html.heex file contains the HTML
  # skeleton of your application, namely HTML headers
  # and other static content.
  embed_templates "layouts/*"

  @doc """
  Renders your app layout.

  This function is typically invoked from every template,
  and it often contains your application menu, sidebar,
  or similar.

  ## Examples

      <Layouts.app flash={@flash}>
        <h1>Content</h1>
      </Layouts.app>

  """
  attr :flash, :map, required: true, doc: "the map of flash messages"

  attr :current_scope, :any,
    default: nil,
    doc: "the current [scope](https://phoenix.hexdocs.pm/scopes.html)"

  attr :current_identity, :map, default: nil

  slot :inner_block, required: true

  def app(assigns) do
    ~H"""
    <header
      :if={@current_scope}
      id="app-header"
      class="sticky top-0 z-40 border-b border-slate-200/90 bg-white/95 backdrop-blur"
    >
      <div class="mx-auto flex max-w-7xl flex-wrap items-center gap-4 px-4 py-3 sm:px-6 lg:px-8">
        <.link
          id="brand-home"
          navigate={~p"/workspace"}
          class="mr-auto flex items-center gap-3 rounded-lg focus:outline-none focus:ring-2 focus:ring-teal-600"
        >
          <span class="grid size-9 place-items-center rounded-xl bg-slate-950 text-sm font-bold text-white">HL</span>
          <span>
            <span class="block text-sm font-semibold text-slate-950">Harden LLM</span>
            <span class="block text-[11px] uppercase tracking-[0.12em] text-slate-500">Operator console</span>
          </span>
        </.link>
        <div class="flex items-center gap-3">
          <span
            class="hidden max-w-48 truncate text-xs text-slate-500 md:block"
            title={identity_email(@current_identity)}
          >
            {identity_email(@current_identity)}
          </span>
          <form action={~p"/logout"} method="post">
            <input type="hidden" name="_csrf_token" value={get_csrf_token()} />
            <button
              id="logout-button"
              type="submit"
              class="inline-flex items-center gap-1.5 rounded-lg border border-slate-200 px-3 py-2 text-xs font-semibold text-slate-700 transition hover:border-slate-300 hover:bg-slate-50 focus:outline-none focus:ring-2 focus:ring-teal-600"
            >
              <.icon name="hero-arrow-right-start-on-rectangle" class="size-4" /> Sign out
            </button>
          </form>
        </div>
      </div>
    </header>

    {render_slot(@inner_block)}

    <.flash_group flash={@flash} />
    """
  end

  defp identity_email(%{"email" => email}) when is_binary(email), do: email
  defp identity_email(_identity), do: "Operator"

  @doc """
  Shows the flash group with standard titles and content.

  ## Examples

      <.flash_group flash={@flash} />
  """
  attr :flash, :map, required: true, doc: "the map of flash messages"
  attr :id, :string, default: "flash-group", doc: "the optional id of flash container"

  def flash_group(assigns) do
    ~H"""
    <div id={@id} aria-live="polite">
      <.flash kind={:info} flash={@flash} />
      <.flash kind={:error} flash={@flash} />

      <.flash
        id="client-error"
        kind={:error}
        title={gettext("We can't find the internet")}
        phx-disconnected={
          show(".phx-client-error #client-error")
          |> JS.remove_attribute("hidden", to: ".phx-client-error #client-error")
        }
        phx-connected={hide("#client-error") |> JS.set_attribute({"hidden", ""})}
        hidden
      >
        {gettext("Attempting to reconnect")}
        <.icon name="hero-arrow-path" class="ml-1 size-3 motion-safe:animate-spin" />
      </.flash>

      <.flash
        id="server-error"
        kind={:error}
        title={gettext("Something went wrong!")}
        phx-disconnected={
          show(".phx-server-error #server-error")
          |> JS.remove_attribute("hidden", to: ".phx-server-error #server-error")
        }
        phx-connected={hide("#server-error") |> JS.set_attribute({"hidden", ""})}
        hidden
      >
        {gettext("Attempting to reconnect")}
        <.icon name="hero-arrow-path" class="ml-1 size-3 motion-safe:animate-spin" />
      </.flash>
    </div>
    """
  end
end
