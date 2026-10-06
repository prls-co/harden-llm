defmodule HardenLlm.ReferenceIntegrationTest do
  use HardenLlmWeb.ConnCase, async: false

  import Ecto.Query

  alias HardenLlm.Reference
  alias HardenLlm.Reference.Call
  alias HardenLlm.Repo
  alias HardenLlmWeb.AccessFixtures

  @moduletag :database
  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-407 WEB-TEST-116

  setup_all do
    migrations = Path.expand("../../priv/repo/migrations", __DIR__)
    Ecto.Migrator.run(Repo, migrations, :up, all: true)
    :ok
  end

  setup %{conn: conn} do
    {:ok, _deleted} = Reference.clear_history()
    Repo.query!("TRUNCATE TABLE reference_drafts", [], timeout: 1_000, pool_timeout: 1_000)
    {:ok, conn: authenticated_conn(conn)}
  end

  # SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-411 WEB-TEST-117
  test "release migration is repeatable while the application is already loaded" do
    assert Enum.any?(Application.loaded_applications(), fn {app, _description, _version} ->
             app == :harden_llm
           end)

    assert {:ok, _repo_pid, []} = HardenLlm.Release.migrate()
  end

  test "one global call row powers shared listing, statistics, redaction, and download", %{
    conn: conn
  } do
    id = Ecto.UUID.generate()

    assert {:ok, %Call{} = call} =
             Reference.record_call(%{
               id: id,
               endpoint: "/v1/responses",
               request: %{
                 "model" => "model-test",
                 "input" => "safe prompt",
                 "nested" => %{"api_key" => "must-not-persist"}
               },
               outcome: %{"output_text" => "safe output", "usage" => %{"total_tokens" => 7}},
               outcome_kind: "succeeded"
             })

    assert call.request["nested"]["api_key"] == "[REDACTED]"
    assert call.request["input"] == "safe prompt"
    assert Reference.get_call(id).id == id
    assert %{items: [%Call{id: ^id}], total_count: 1} = Reference.list_calls(1, 10)

    assert Reference.statistics() == %{
             total: 1,
             succeeded: 1,
             failed: 0,
             unknown: 0,
             accounting: :unavailable
           }

    response = get(conn, "/history/#{id}/download")
    assert response.status == 200

    assert get_resp_header(response, "content-disposition") ==
             ["attachment; filename=\"harden-llm-call-#{id}.json\""]

    downloaded = json_response(response, 200)
    assert downloaded["request"]["input"] == "safe prompt"
    assert downloaded["outcome"]["output_text"] == "safe output"
    refute response.resp_body =~ "must-not-persist"

    second_login =
      conn
      |> put_req_header("cookie", AccessFixtures.second_cookie())
      |> get("/history/#{id}/download")

    assert second_login.status == 200

    assert {:error, {:invalid_record, changeset}} =
             Reference.record_call(%{
               id: id,
               endpoint: "/v1/responses",
               request: %{"model" => "model-test"},
               outcome: %{"output_text" => "second"},
               outcome_kind: "succeeded"
             })

    assert %{id: ["has already been taken"]} =
             Ecto.Changeset.traverse_errors(changeset, fn {message, _} -> message end)

    assert Repo.aggregate(Call, :count, :id) == 1
  end

  test "global pagination, per-entry delete, and clear touch only reference history" do
    assert :ok = Reference.save_draft("draft-remains", 1, %{"userPrompt" => "preserved"})

    ids =
      for number <- 1..12 do
        id = Ecto.UUID.generate()
        assert {:ok, _} = Reference.record_call(call(id, "prompt #{number}", "succeeded"))
        id
      end

    first = Reference.list_calls(1, 10)
    second = Reference.list_calls(2, 10)
    all = first.items ++ second.items

    assert first.total_count == 12
    assert second.total_count == 12
    assert Enum.map(all, & &1.id) |> Enum.uniq() |> length() == 12
    assert all == Enum.sort_by(all, &{&1.inserted_at, &1.id}, :desc)

    assert {:ok, 1} = Reference.delete_call(hd(ids))
    assert Reference.statistics().total == 11
    assert {:ok, 11} = Reference.clear_history()
    assert Reference.list_calls(1, 10) == %{items: [], page: 1, page_size: 10, total_count: 0}
    assert Reference.get_draft("draft-remains") != nil

    assert Repo.query!("SELECT to_regclass('public.reference_drafts') IS NOT NULL", []).rows == [
             [true]
           ]

    assert Repo.query!("SELECT to_regclass('public.reference_history') IS NOT NULL", []).rows == [
             [true]
           ]
  end

  test "drafts stay user-scoped and ignore stale revisions" do
    assert :ok = Reference.save_draft("user-one", 1, %{"userPrompt" => "first"})
    assert :ok = Reference.save_draft("user-two", 1, %{"userPrompt" => "second"})
    assert :ok = Reference.save_draft("user-one", 2, %{"userPrompt" => "newer"})

    assert {:error, :stale_revision} =
             Reference.save_draft("user-one", 1, %{"userPrompt" => "stale"})

    assert %HardenLlm.Reference.Draft{state: %{"userPrompt" => "newer"}, revision: 2} =
             Reference.get_draft("user-one")

    assert %HardenLlm.Reference.Draft{state: %{"userPrompt" => "second"}, revision: 1} =
             Reference.get_draft("user-two")

    Repo.update_all(
      from(draft in HardenLlm.Reference.Draft, where: draft.user_id == "user-one"),
      set: [updated_at: DateTime.add(DateTime.utc_now(), -8 * 24 * 60 * 60, :second)]
    )

    assert Reference.get_draft("user-one") == nil
    assert Reference.get_draft("user-two") != nil
  end

  test "SQL statement timeout bounds a blocked storage operation" do
    started = System.monotonic_time(:millisecond)

    assert {:error, %Postgrex.Error{}} =
             Repo.query("SELECT pg_sleep(2)", [], timeout: 1_000, pool_timeout: 400)

    assert System.monotonic_time(:millisecond) - started < 1_200

    assert is_map(Reference.statistics())
  end

  test "unavailable Repo reports history failure without a wait", %{conn: _conn} do
    supervisor = HardenLlm.Supervisor

    on_exit(fn ->
      if is_nil(Process.whereis(Repo)), do: Supervisor.restart_child(supervisor, Repo)
    end)

    assert :ok = Supervisor.terminate_child(supervisor, Repo)
    started = System.monotonic_time(:millisecond)
    assert {:error, :unavailable} = Reference.statistics()
    assert System.monotonic_time(:millisecond) - started < 100
    assert {:ok, _pid} = Supervisor.restart_child(supervisor, Repo)
  end

  defp call(id, prompt, kind) do
    %{
      id: id,
      endpoint: "/v1/responses",
      request: %{"model" => "model-test", "input" => prompt},
      outcome: %{"output_text" => prompt},
      outcome_kind: kind
    }
  end
end
