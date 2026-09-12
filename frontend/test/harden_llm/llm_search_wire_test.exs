defmodule HardenLlm.LlmSearchWireTest do
  use ExUnit.Case, async: true

  alias HardenLlm.LlmDiagnosticsWire
  alias HardenLlmWeb.APIFixtures

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-070
  # The real gateway adds optional search metadata to run/history/trace v2.
  @source %{"url" => "https://example.test/evidence", "title" => "Evidence"}
  @search %{
    "mode" => "native",
    "executed" => true,
    "sources" => [],
    "costStatus" => "unavailable"
  }

  test "accepts optional native and Jina evidence without losing it on any diagnostics route" do
    for search <- [
          @search,
          %{@search | "executed" => false},
          %{@search | "mode" => "jina", "sources" => List.duplicate(@source, 50)},
          Map.put(@search, "entryPointHtml", "<div>untrusted provider suggestions</div>"),
          @search
          |> Map.put("sources", [@source])
          |> Map.put("citations", [Map.merge(@source, %{"startIndex" => 0, "endIndex" => 7})])
        ] do
      result = Map.put(APIFixtures.run_result(), "search", search)

      for {operation, payload} <- envelopes(result) do
        assert {:ok, ^payload} = LlmDiagnosticsWire.decode(operation, payload)
      end
    end
  end

  test "search remains optional and other required or unknown run fields stay strict" do
    result = APIFixtures.run_result()
    assert {:ok, ^result} = LlmDiagnosticsWire.decode("run", result)

    for invalid <- [Map.delete(result, "accounting"), Map.put(result, "surprise", true)] do
      assert {:error, :malformed_diagnostics} = LlmDiagnosticsWire.decode("run", invalid)
    end
  end

  test "cached evidence stays executed while current provider accounting is zero" do
    original = Map.put(APIFixtures.run_result(), "search", @search)

    zero = %{
      "usage" =>
        Map.new(original["accounting"]["provider"]["usage"], fn
          {"status", _} -> {"status", "unavailable"}
          {key, _} -> {key, 0}
        end),
      "cost" => %{
        "knownSubtotalUsd" => 0,
        "status" => "unavailable",
        "source" => "",
        "knownObservations" => 0,
        "unknownObservations" => 0
      }
    }

    cached =
      original
      |> Map.put("providerInvoked", false)
      |> Map.put("attempts", [])
      |> Map.put("resultSource", %{
        "kind" => "cache",
        "producer" => original["resultSource"]["producer"]
      })
      |> Map.put("cache", %{
        "mode" => "cache",
        "status" => "hit",
        "served" => true,
        "written" => false,
        "operationHash" => String.duplicate("a", 64),
        "version" => "operation-v2"
      })
      |> put_in(["accounting", "provider"], zero)

    for {operation, payload} <- envelopes(cached) do
      assert {:ok, ^payload} = LlmDiagnosticsWire.decode(operation, payload)
    end

    assert cached["search"] == original["search"]
  end

  test "rejects malformed evidence at run history and trace boundaries" do
    for search <- [
          nil,
          [],
          "native",
          Map.delete(@search, "sources"),
          Map.put(@search, "unknown", true),
          %{@search | "mode" => "unsupported"},
          %{@search | "executed" => "true"},
          %{@search | "costStatus" => "exact"},
          %{@search | "sources" => nil},
          %{@search | "sources" => List.duplicate(@source, 51)},
          %{@search | "sources" => [%{@source | "url" => "javascript:alert(1)"}]},
          %{@search | "sources" => [%{@source | "url" => "https://secret@example.test/"}]},
          %{@search | "sources" => [%{@source | "title" => nil}]},
          %{@search | "sources" => [Map.put(@source, "unknown", true)]},
          Map.put(@search, "entryPointHtml", String.duplicate("x", 32_769)),
          Map.put(@search, "entryPointHtml", nil),
          Map.put(@search, "citations", %{}),
          Map.put(@search, "citations", [
            Map.merge(@source, %{"startIndex" => 2, "endIndex" => 1})
          ]),
          Map.put(@search, "citations", [
            Map.merge(@source, %{"startIndex" => -1, "endIndex" => 1})
          ])
        ] do
      for {operation, payload} <- envelopes(Map.put(APIFixtures.run_result(), "search", search)) do
        assert {:error, :malformed_diagnostics} = LlmDiagnosticsWire.decode(operation, payload)
      end
    end
  end

  defp envelopes(result) do
    [
      {"run", result},
      {"listHistory", %{"items" => [Map.put(APIFixtures.history_item(), "result", result)]}},
      {"getTrace", Map.put(APIFixtures.trace(), "record", result)}
    ]
  end
end
