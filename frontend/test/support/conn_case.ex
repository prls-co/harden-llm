defmodule HardenLlmWeb.ConnCase do
  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-044 WEB-TEST-045 TEST-044 TEST-045

  @moduledoc """
  This module defines the test case to be used by
  tests that require setting up a connection.

  Such tests rely on `Phoenix.ConnTest` and also
  import other functionality to make it easier
  to build common data structures and query the data layer.

  The reference host owns no database. Req stubs stay private to the test
  process and are explicitly allowed for its LiveView children.
  """

  use ExUnit.CaseTemplate

  using do
    quote do
      # The default endpoint for testing
      @endpoint HardenLlmWeb.Endpoint

      use HardenLlmWeb, :verified_routes

      # Import conveniences for testing with connections
      import Plug.Conn
      import Phoenix.ConnTest
      import HardenLlmWeb.ConnCase
    end
  end

  setup :configure_req_test

  setup _tags do
    {:ok, conn: Phoenix.ConnTest.build_conn()}
  end

  def configure_req_test(context) do
    Req.Test.set_req_test_from_context(context)
    Req.Test.verify_on_exit!(context)
  end

  defmacro live(conn, path \\ nil, opts \\ []) do
    quote do
      HardenLlmWeb.ConnCase.live_with_req(
        unquote(conn),
        unquote(path),
        unquote(opts)
      )
    end
  end

  def live_with_req(conn, path, opts) do
    result =
      case path do
        nil ->
          Phoenix.LiveViewTest.__live__(conn, nil, opts)

        path when is_binary(path) ->
          conn = Phoenix.ConnTest.dispatch(conn, HardenLlmWeb.Endpoint, :get, path)
          Phoenix.LiveViewTest.__live__(conn, path, opts)

        _ ->
          raise ArgumentError, "path must be nil or a binary, got: #{inspect(path)}"
      end

    case result do
      {:ok, %{pid: pid} = view, html} ->
        :ok = Req.Test.allow(HardenLlmUI.Client, self(), pid)
        ExUnit.Callbacks.on_exit(fn -> stop_live_view(view) end)
        {:ok, view, html}

      other ->
        other
    end
  end

  def authenticated_conn(conn) do
    Plug.Conn.put_req_header(conn, "cookie", HardenLlmWeb.APIFixtures.cookie())
  end

  # Phoenix's render_async waits a snapshot of current tasks. A completion can
  # start another detail/draft operation, so await the process-owned chain under
  # one unchanged deadline. This only observes tasks; it never reissues work.
  def render_settled(view, timeout_ms) do
    settle(view, System.monotonic_time(:millisecond) + timeout_ms)
  end

  defp settle(view, deadline) do
    remaining = deadline - System.monotonic_time(:millisecond)

    if remaining <= 0,
      do: raise("expected dependent async processes to finish within the deadline")

    html = Phoenix.LiveViewTest.render_async(view, remaining)

    case Phoenix.LiveView.Channel.async_pids(view.pid) do
      {:ok, []} -> html
      {:ok, _pids} -> settle(view, deadline)
    end
  end

  defp stop_live_view(%{pid: pid}) do
    async_pids =
      if Process.alive?(pid) do
        {:ok, pids} = Phoenix.LiveView.Channel.async_pids(pid)
        pids
      else
        []
      end

    monitors =
      [pid | async_pids]
      |> Enum.filter(&Process.alive?/1)
      |> Enum.map(&Process.monitor/1)

    if Process.alive?(pid), do: Phoenix.LiveView.Channel.graceful_exit(pid, :shutdown)

    Enum.each(monitors, fn monitor ->
      receive do
        {:DOWN, ^monitor, :process, _pid, _reason} -> :ok
      after
        1_000 -> Process.demonitor(monitor, [:flush])
      end
    end)
  end
end
