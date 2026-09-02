defmodule HardenLlmWeb.LiveStats do
  @moduledoc """
  Owns the shared LiveView lifecycle for the aggregate LLM stats snapshot.

  The backend remains authoritative. Hosts opt into refresh events and a
  bounded polling interval while the reusable component stays transport-free.
  """

  import Phoenix.Component, only: [assign: 3]
  import Phoenix.LiveView, only: [start_async: 3]

  alias HardenLlm.LlmStatsProjection
  alias HardenLlmWeb.{HardenAPI, Observability}
  alias Phoenix.LiveView.AsyncResult

  @refresh_interval_ms :timer.minutes(1)

  def init(socket) do
    socket
    |> assign(:stats, AsyncResult.loading())
    |> assign(:stats_ref, nil)
    |> assign(:stats_refresh_pending?, false)
    |> assign(:stats_updated_at, nil)
  end

  def refresh(%{assigns: %{stats_ref: nil}} = socket) do
    reference = System.unique_integer([:positive, :monotonic])
    handle = socket.assigns.session_handle

    socket
    |> assign(:stats, AsyncResult.loading(socket.assigns.stats))
    |> assign(:stats_ref, reference)
    |> start_async(
      {:load_stats, reference},
      Observability.propagate(fn -> HardenAPI.get_stats(handle) end)
    )
  end

  def refresh(socket), do: assign(socket, :stats_refresh_pending?, true)

  def complete(
        %{assigns: %{stats_ref: reference}} = socket,
        reference,
        {:ok, {:ok, stats, _state}}
      ) do
    socket
    |> assign(:stats, AsyncResult.ok(socket.assigns.stats, LlmStatsProjection.project(stats)))
    |> assign(:stats_ref, nil)
    |> assign(:stats_updated_at, DateTime.utc_now() |> DateTime.truncate(:second))
    |> continue_pending_refresh()
  end

  def complete(%{assigns: %{stats_ref: reference}} = socket, reference, _result) do
    socket
    |> assign(:stats, AsyncResult.failed(socket.assigns.stats, :unavailable))
    |> assign(:stats_ref, nil)
    |> continue_pending_refresh()
  end

  def complete(socket, _reference, _result), do: socket

  def schedule_refresh do
    Process.send_after(self(), :refresh_stats_snapshot, @refresh_interval_ms)
  end

  defp continue_pending_refresh(%{assigns: %{stats_refresh_pending?: true}} = socket) do
    socket
    |> assign(:stats_refresh_pending?, false)
    |> refresh()
  end

  defp continue_pending_refresh(socket), do: socket
end
