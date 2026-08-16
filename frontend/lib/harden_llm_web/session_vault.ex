defmodule HardenLlmWeb.SessionVault do
  @moduledoc """
  Process-local vault for opaque backend bearer tokens.

  Browser-visible handles are random 256-bit values. Only their SHA-256
  digests are used as private ETS keys, and the table disappears on restart.
  """

  use GenServer

  @cleanup_interval :timer.minutes(1)

  def start_link(options \\ []) do
    GenServer.start_link(__MODULE__, options, name: __MODULE__)
  end

  def insert(token, expires_at) when is_binary(token) and is_binary(expires_at) do
    GenServer.call(__MODULE__, {:insert, token, expires_at})
  end

  def lookup(handle) when is_binary(handle) do
    GenServer.call(__MODULE__, {:lookup, handle})
  end

  def lookup(_handle), do: :error

  def revoke(handle) when is_binary(handle) do
    GenServer.call(__MODULE__, {:revoke, handle})
  end

  def revoke(_handle), do: :ok

  def cleanup do
    GenServer.call(__MODULE__, :cleanup)
  end

  def count do
    GenServer.call(__MODULE__, :count)
  end

  @impl true
  def init(_options) do
    table =
      :ets.new(__MODULE__, [:set, :private, read_concurrency: false, write_concurrency: false])

    schedule_cleanup()
    {:ok, %{table: table}}
  end

  @impl true
  def handle_call({:insert, token, expires_at}, _from, state) do
    with true <- byte_size(token) > 0,
         {:ok, expiry_ms} <- parse_expiry(expires_at),
         true <- expiry_ms > now_ms() do
      handle = :crypto.strong_rand_bytes(32) |> Base.url_encode64(padding: false)
      true = :ets.insert(state.table, {digest(handle), token, expiry_ms})
      {:reply, {:ok, handle}, state}
    else
      _ -> {:reply, {:error, :invalid_expiry}, state}
    end
  end

  def handle_call({:lookup, handle}, _from, state) do
    key = digest(handle)
    now = now_ms()

    reply =
      case :ets.lookup(state.table, key) do
        [{^key, token, expiry_ms}] when expiry_ms > now ->
          {:ok, token, expiry_ms}

        [_expired] ->
          true = :ets.delete(state.table, key)
          :error

        [] ->
          :error
      end

    {:reply, reply, state}
  end

  def handle_call({:revoke, handle}, _from, state) do
    true = :ets.delete(state.table, digest(handle))
    {:reply, :ok, state}
  end

  def handle_call(:cleanup, _from, state) do
    delete_expired(state.table)
    {:reply, :ok, state}
  end

  def handle_call(:count, _from, state) do
    {:reply, :ets.info(state.table, :size), state}
  end

  @impl true
  def handle_info(:cleanup, state) do
    delete_expired(state.table)
    schedule_cleanup()
    {:noreply, state}
  end

  defp delete_expired(table) do
    now = now_ms()
    :ets.select_delete(table, [{{:_, :_, :"$1"}, [{:"=<", :"$1", now}], [true]}])
    :ok
  end

  defp digest(handle), do: :crypto.hash(:sha256, handle)

  defp parse_expiry(value) do
    case DateTime.from_iso8601(value) do
      {:ok, datetime, _offset} -> {:ok, DateTime.to_unix(datetime, :millisecond)}
      _ -> {:error, :invalid_expiry}
    end
  end

  defp now_ms do
    clock = Application.get_env(:harden_llm, :clock, &DateTime.utc_now/0)
    clock.() |> DateTime.to_unix(:millisecond)
  end

  defp schedule_cleanup do
    Process.send_after(self(), :cleanup, @cleanup_interval)
  end
end
