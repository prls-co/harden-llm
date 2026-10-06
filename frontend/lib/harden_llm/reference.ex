defmodule HardenLlm.Reference do
  @moduledoc "Shared reference-app calls and access-scoped workspace drafts."

  import Ecto.Query

  alias HardenLlm.Reference.{Call, Draft}
  alias HardenLlm.Repo

  @namespace "workspace"
  @sql_timeout 600
  @pool_timeout 400
  @max_draft_bytes 256 * 1024
  @max_record_bytes 17 * 1024 * 1024
  @draft_lifetime_seconds 7 * 24 * 60 * 60
  @sensitive_key ~r/(authorization|api[-_]?key|token|password|secret|credential|session)/i

  def namespace, do: @namespace
  def new_call_id, do: Ecto.UUID.generate()

  def record_call(%{
        id: id,
        endpoint: endpoint,
        request: request,
        outcome: outcome,
        outcome_kind: kind
      })
      when endpoint in ["/v1/responses"] and kind in ["succeeded", "failed", "unknown"] and
             is_map(request) and is_map(outcome) do
    with {:ok, id} <- Ecto.UUID.cast(id),
         {:ok, request} <- safe_json(request, @max_record_bytes),
         {:ok, outcome} <- safe_json(outcome, @max_record_bytes) do
      case repository(fn ->
             attrs = %{
               id: id,
               endpoint: endpoint,
               request: request,
               outcome: outcome,
               outcome_kind: kind
             }

             %Call{}
             |> Ecto.Changeset.cast(attrs, [:id, :endpoint, :request, :outcome, :outcome_kind])
             |> Ecto.Changeset.validate_required([
               :id,
               :endpoint,
               :request,
               :outcome,
               :outcome_kind
             ])
             |> Ecto.Changeset.unique_constraint(:id, name: "reference_history_pkey")
             |> Repo.insert(timeout: @sql_timeout, pool_timeout: @pool_timeout)
           end) do
        {:ok, %Call{} = call} -> {:ok, call}
        {:error, %Ecto.Changeset{} = changeset} -> {:error, {:invalid_record, changeset}}
        other -> other
      end
    else
      :error -> {:error, :invalid_submission_id}
      {:error, reason} -> {:error, reason}
    end
  end

  def record_call(_), do: {:error, :invalid_record}

  def list_calls(page, page_size)
      when is_integer(page) and page > 0 and is_integer(page_size) and
             page_size in [10, 25, 50, 100] do
    repository(fn ->
      total =
        Repo.aggregate(Call, :count, :id, timeout: @sql_timeout, pool_timeout: @pool_timeout)

      items =
        Call
        |> order_by([call], desc: call.inserted_at, desc: call.id)
        |> limit(^page_size)
        |> offset(^((page - 1) * page_size))
        |> Repo.all(timeout: @sql_timeout, pool_timeout: @pool_timeout)

      %{items: items, page: page, page_size: page_size, total_count: total}
    end)
  end

  def list_calls(_page, _page_size), do: {:error, :invalid_pagination}

  def statistics do
    repository(fn ->
      counts =
        from(call in Call,
          group_by: call.outcome_kind,
          select: {call.outcome_kind, count(call.id)}
        )
        |> Repo.all(timeout: @sql_timeout, pool_timeout: @pool_timeout)
        |> Map.new()

      %{
        total: Enum.sum(Map.values(counts)),
        succeeded: Map.get(counts, "succeeded", 0),
        failed: Map.get(counts, "failed", 0),
        unknown: Map.get(counts, "unknown", 0),
        accounting: :unavailable
      }
    end)
  end

  def get_call(id) when is_binary(id) do
    case Ecto.UUID.cast(id) do
      {:ok, uuid} ->
        repository(fn ->
          Repo.get(Call, uuid, timeout: @sql_timeout, pool_timeout: @pool_timeout)
        end)

      :error ->
        {:ok, nil}
    end
  end

  def delete_call(id) when is_binary(id) do
    case Ecto.UUID.cast(id) do
      {:ok, uuid} ->
        repository(fn ->
          {count, _} =
            Repo.delete_all(from(call in Call, where: call.id == ^uuid),
              timeout: @sql_timeout,
              pool_timeout: @pool_timeout
            )

          {:ok, count}
        end)

      :error ->
        {:ok, 0}
    end
  end

  def clear_history do
    repository(fn ->
      {count, _} = Repo.delete_all(Call, timeout: @sql_timeout, pool_timeout: @pool_timeout)
      {:ok, count}
    end)
  end

  def download(%Call{} = call) do
    %{
      "id" => call.id,
      "createdAt" => DateTime.to_iso8601(call.inserted_at),
      "endpoint" => call.endpoint,
      "request" => call.request,
      "outcome" => call.outcome,
      "outcomeKind" => call.outcome_kind
    }
  end

  def get_draft(user_id) when is_binary(user_id) and byte_size(user_id) in 1..256 do
    repository(fn ->
      now = DateTime.utc_now()

      case Repo.get_by(
             Draft,
             [user_id: user_id, namespace: @namespace],
             timeout: @sql_timeout,
             pool_timeout: @pool_timeout
           ) do
        nil ->
          nil

        %Draft{updated_at: updated_at} = draft ->
          if DateTime.diff(now, updated_at, :second) >= @draft_lifetime_seconds do
            Repo.delete_all(
              from(row in Draft,
                where:
                  row.user_id == ^user_id and row.namespace == ^@namespace and
                    row.updated_at == ^updated_at
              ),
              timeout: @sql_timeout,
              pool_timeout: @pool_timeout
            )

            nil
          else
            draft
          end
      end
    end)
  end

  def get_draft(_), do: {:error, :invalid_user}

  def save_draft(user_id, revision, state)
      when is_binary(user_id) and byte_size(user_id) in 1..256 and is_integer(revision) and
             revision > 0 and is_map(state) do
    with {:ok, state} <- safe_json(state, @max_draft_bytes) do
      now = DateTime.utc_now()

      case repository(fn ->
             Repo.query(
               """
               INSERT INTO reference_drafts (user_id, namespace, revision, state, updated_at)
               VALUES ($1, $2, $3, $4::jsonb, $5)
               ON CONFLICT (user_id, namespace) DO UPDATE
                 SET revision = EXCLUDED.revision,
                     state = EXCLUDED.state,
                     updated_at = EXCLUDED.updated_at
                 WHERE reference_drafts.revision < EXCLUDED.revision
               """,
               [user_id, @namespace, revision, state, now],
               timeout: @sql_timeout,
               pool_timeout: @pool_timeout
             )
           end) do
        {:ok, %Postgrex.Result{num_rows: 1}} -> :ok
        {:ok, %Postgrex.Result{num_rows: 0}} -> {:error, :stale_revision}
        other -> other
      end
    end
  end

  def save_draft(_, _, _), do: {:error, :invalid_draft}

  defp safe_json(value, maximum_bytes) do
    value = redact(value)

    case Jason.encode(value) do
      {:ok, encoded} when byte_size(encoded) <= maximum_bytes -> {:ok, value}
      {:ok, _encoded} -> {:error, :too_large}
      {:error, _reason} -> {:error, :not_json}
    end
  end

  defp redact(value) when is_map(value) do
    Map.new(value, fn {key, child} ->
      if is_binary(key) and Regex.match?(@sensitive_key, key),
        do: {key, "[REDACTED]"},
        else: {key, redact(child)}
    end)
  end

  defp redact(value) when is_list(value), do: Enum.map(value, &redact/1)
  defp redact(value), do: value

  defp repository(fun) do
    if Application.get_env(:harden_llm, :reference_repo_enabled, false) and
         Process.whereis(Repo) do
      try do
        fun.()
      rescue
        _error in [DBConnection.ConnectionError, Postgrex.Error] ->
          {:error, :unavailable}
      catch
        :exit, _reason -> {:error, :unavailable}
      end
    else
      {:error, :unavailable}
    end
  end
end
