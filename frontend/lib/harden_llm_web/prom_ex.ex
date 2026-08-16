defmodule HardenLlmWeb.PromEx do
  @moduledoc "Private PromEx registry for bounded frontend operational metrics."

  use PromEx, otp_app: :harden_llm

  alias PromEx.Plugins

  @impl true
  def plugins do
    [
      {Plugins.Beam, poll_rate: 10_000},
      HardenLlmWeb.PromExPlugin
    ]
  end

  @impl true
  def dashboard_assigns do
    [datasource_id: "harden-prometheus", default_selected_interval: "30s"]
  end

  @impl true
  def dashboards, do: []
end
