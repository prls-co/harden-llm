defmodule HardenLlmWeb.SessionPlug do
  @moduledoc false
  @behaviour Plug

  @impl true
  def init(options), do: options

  @impl true
  def call(conn, _options) do
    options = HardenLlmWeb.SessionOptions.options()
    Plug.Session.call(conn, Plug.Session.init(options))
  end
end
