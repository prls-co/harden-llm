defmodule HardenLlmWeb.SessionVaultTest do
  use ExUnit.Case, async: false

  alias HardenLlmWeb.{APIFixtures, SessionVault}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-004

  setup do
    prior_clock = Application.get_env(:harden_llm, :clock)

    on_exit(fn ->
      if prior_clock,
        do: Application.put_env(:harden_llm, :clock, prior_clock),
        else: Application.delete_env(:harden_llm, :clock)
    end)

    :ok
  end

  test "stores a token behind a random handle and revokes it" do
    {:ok, handle} = SessionVault.insert(APIFixtures.token(), APIFixtures.expiry())

    assert byte_size(handle) == 43
    refute handle == APIFixtures.token()
    assert {:ok, token, _expiry_ms} = SessionVault.lookup(handle)
    assert token == APIFixtures.token()
    assert :ok = SessionVault.revoke(handle)
    assert SessionVault.lookup(handle) == :error

    source = File.read!("lib/harden_llm_web/session_vault.ex")
    assert source =~ ":crypto.hash(:sha256, handle)"
    assert source =~ ":dets.open_file"
    assert source =~ "MessageEncryptor"
    refute source =~ ":ets.tab2list"

    path = Application.fetch_env!(:harden_llm, :session_vault) |> Keyword.fetch!(:path)
    refute File.read!(path) =~ APIFixtures.token()
  end

  test "expired entries are rejected and cleaned without waiting" do
    Application.put_env(:harden_llm, :clock, fn -> ~U[2099-07-13 11:59:59Z] end)
    {:ok, handle} = SessionVault.insert(APIFixtures.token(), APIFixtures.expiry())

    Application.put_env(:harden_llm, :clock, fn -> ~U[2099-07-13 12:00:01Z] end)
    assert SessionVault.lookup(handle) == :error
    assert :ok = SessionVault.cleanup()
  end

  test "a vault restart preserves valid frontend sessions" do
    {:ok, handle} = SessionVault.insert(APIFixtures.token(), APIFixtures.expiry())
    assert {:ok, _, _} = SessionVault.lookup(handle)

    assert :ok = Supervisor.terminate_child(HardenLlm.Supervisor, SessionVault)
    assert {:ok, _pid} = Supervisor.restart_child(HardenLlm.Supervisor, SessionVault)
    assert {:ok, token, _expiry_ms} = SessionVault.lookup(handle)
    assert token == APIFixtures.token()
    assert :ok = SessionVault.revoke(handle)
  end
end
