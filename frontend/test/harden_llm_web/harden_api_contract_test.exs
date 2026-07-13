defmodule HardenLlmWeb.HardenAPIContractTest do
  use ExUnit.Case, async: true

  alias HardenLlmWeb.HardenAPI

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-002

  test "client operation registry matches OpenAPI methods, paths, and security" do
    openapi = parse_openapi!(Path.expand("../../../api/openapi.yaml", __DIR__))
    registry = Map.new(HardenAPI.operations(), &{&1.id, &1})

    assert MapSet.new(Map.keys(openapi)) ==
             MapSet.new(Map.keys(registry) ++ HardenAPI.backend_only_operations())

    for {id, operation} <- registry do
      assert %{method: method, path: path, auth: auth} = Map.fetch!(openapi, id)
      assert method == operation.method
      assert path == operation.path
      assert auth == operation.auth
      assert function_exported?(HardenAPI, operation.function, operation_arity(operation))
    end

    assert openapi["getHealth"].auth == false
    assert openapi["getReadiness"].auth == false
  end

  test "OpenAPI examples remain the fixture source" do
    source = File.read!(Path.expand("../../../api/openapi.yaml", __DIR__))
    assert source =~ "operator@example.test"
    assert source =~ "Summarize the incident."
    assert source =~ "example-write-only-key"
    assert source =~ "X-Amz-Expires=60"
  end

  defp operation_arity(%{function: function})
       when function in [:login, :save_state, :import_profile_bundle, :run], do: 2

  defp operation_arity(%{function: :list_history}), do: 2

  defp operation_arity(%{function: function})
       when function in [:delete_history, :delete_profile, :refresh_profile_models, :get_trace],
       do: 2

  defp operation_arity(%{function: :save_profile}), do: 3
  defp operation_arity(%{function: :get_artifact}), do: 3
  defp operation_arity(_operation), do: 1

  defp parse_openapi!(path) do
    {_path, _method, _id, operations} =
      path
      |> File.stream!()
      |> Enum.reduce({nil, nil, nil, %{}}, fn line, {current_path, method, id, operations} ->
        cond do
          captures = Regex.run(~r/^  (\/.+):\s*$/, line) ->
            [_, route] = captures
            {route, nil, nil, operations}

          captures = Regex.run(~r/^    (get|post|put|delete):\s*$/, line) ->
            [_, verb] = captures
            {current_path, String.to_atom(verb), nil, operations}

          captures = Regex.run(~r/^      operationId: ([A-Za-z0-9_]+)\s*$/, line) ->
            [_, operation_id] = captures
            operation = %{path: current_path, method: method, auth: true}
            {current_path, method, operation_id, Map.put(operations, operation_id, operation)}

          id && Regex.match?(~r/^      security: \[\]\s*$/, line) ->
            {current_path, method, id, put_in(operations, [id, :auth], false)}

          true ->
            {current_path, method, id, operations}
        end
      end)

    operations
  end
end
