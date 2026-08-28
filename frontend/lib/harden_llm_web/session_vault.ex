defmodule HardenLlmWeb.SessionVault do
  @moduledoc """
  Durable server-side vault for opaque backend bearer tokens.

  Browser-visible handles are random 256-bit values. Only their SHA-256
  digests are used as keys, and the backend token is encrypted before it is
  written to the supervised DETS table. The production table lives on a
  retained Docker volume so a frontend release can restart without invalidating
  otherwise-valid browser sessions.
  """

  use GenServer

  alias Plug.Crypto.{KeyGenerator, MessageEncryptor}

  @table_name :harden_llm_session_vault
  @vault_key_salt "harden-llm-session-vault-key-v1"
  @vault_aad "harden-llm-session-vault-v1"
  @vault_sign_secret "harden-llm-session-vault"
  @default_path "/var/lib/harden-llm-web/session-vault.dets"
  @cleanup_interval :timer.minutes(1)
  @auto_save_interval :timer.seconds(1)

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
    path = vault_path()
    :ok = File.mkdir_p(Path.dirname(path))
    encryption_key = vault_key()

    case :dets.open_file(@table_name,
           file: String.to_charlist(path),
           type: :set,
           auto_save: @auto_save_interval
         ) do
      {:ok, table} ->
        case File.chmod(path, 0o600) do
          :ok ->
            schedule_cleanup()
            {:ok, %{table: table, encryption_key: encryption_key}}

          {:error, reason} ->
            _ = :dets.close(table)
            {:stop, {:session_vault_permissions_failed, reason}}
        end

      {:error, reason} ->
        {:stop, {:session_vault_open_failed, reason}}
    end
  end

  @impl true
  def handle_call({:insert, token, expires_at}, _from, state) do
    with true <- byte_size(token) > 0,
         {:ok, expiry_ms} <- parse_expiry(expires_at),
         true <- expiry_ms > now_ms() do
      handle = :crypto.strong_rand_bytes(32) |> Base.url_encode64(padding: false)
      encrypted_token = encrypt_token(token, state.encryption_key)

      case persist(state, fn ->
             :dets.insert(state.table, {digest(handle), encrypted_token, expiry_ms})
           end) do
        :ok -> {:reply, {:ok, handle}, state}
        {:error, _reason} -> {:reply, {:error, :unavailable}, state}
      end
    else
      _ -> {:reply, {:error, :invalid_expiry}, state}
    end
  end

  @impl true
  def handle_call({:lookup, handle}, _from, state) do
    key = digest(handle)
    now = now_ms()

    reply =
      case :dets.lookup(state.table, key) do
        [{^key, encrypted_token, expiry_ms}]
        when is_binary(encrypted_token) and is_integer(expiry_ms) and expiry_ms > now ->
          case decrypt_token(encrypted_token, state.encryption_key) do
            {:ok, token} -> {:ok, token, expiry_ms}
            :error -> expire_entry(state, key)
          end

        [{^key, _encrypted_token, _expiry_ms}] ->
          expire_entry(state, key)

        [] ->
          :error
      end

    {:reply, reply, state}
  end

  @impl true
  def handle_call({:revoke, handle}, _from, state) do
    _ = persist(state, fn -> :dets.delete(state.table, digest(handle)) end)
    {:reply, :ok, state}
  end

  @impl true
  def handle_call(:cleanup, _from, state) do
    delete_expired(state)
    {:reply, :ok, state}
  end

  @impl true
  def handle_call(:count, _from, state) do
    {:reply, :dets.info(state.table, :size), state}
  end

  @impl true
  def handle_info(:cleanup, state) do
    delete_expired(state)
    schedule_cleanup()
    {:noreply, state}
  end

  @impl true
  def terminate(_reason, state) do
    _ = :dets.sync(state.table)
    _ = :dets.close(state.table)
    :ok
  end

  defp delete_expired(state) do
    now = now_ms()

    deleted =
      :dets.select_delete(state.table, [
        {{:_, :_, :"$1"}, [{:"=<", :"$1", now}], [true]}
      ])

    if deleted > 0, do: :dets.sync(state.table)
    :ok
  end

  defp expire_entry(state, key) do
    _ = persist(state, fn -> :dets.delete(state.table, key) end)
    :error
  end

  defp persist(state, operation) do
    case operation.() do
      :ok -> :dets.sync(state.table)
      {:error, reason} -> {:error, reason}
    end
  catch
    :exit, reason -> {:error, reason}
  end

  defp encrypt_token(token, key) do
    MessageEncryptor.encrypt(token, @vault_aad, key, @vault_sign_secret)
  end

  defp decrypt_token(encrypted_token, key) do
    MessageEncryptor.decrypt(encrypted_token, @vault_aad, key, @vault_sign_secret)
  rescue
    _ -> :error
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

  defp vault_path do
    config = Application.get_env(:harden_llm, :session_vault, [])

    Keyword.get(
      config,
      :path,
      System.get_env("HARDEN_LLM_WEB_SESSION_VAULT_PATH", @default_path)
    )
  end

  defp vault_key do
    endpoint_config = Application.fetch_env!(:harden_llm, HardenLlmWeb.Endpoint)
    secret_key_base = Keyword.fetch!(endpoint_config, :secret_key_base)
    KeyGenerator.generate(secret_key_base, @vault_key_salt, length: 32)
  end
end
