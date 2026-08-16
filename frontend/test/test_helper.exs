arguments = System.argv()

browser_gate? =
  Enum.any?(arguments, fn argument ->
    argument in ["--only=browser", "--only=compose", "--include=browser", "--include=compose"]
  end) or
    arguments
    |> Enum.chunk_every(2, 1, :discard)
    |> Enum.any?(fn
      [flag, tag] when flag in ["--only", "--include"] -> tag in ["browser", "compose"]
      _pair -> false
    end)

if browser_gate? do
  Mix.Task.run("assets.build")
  {:ok, _applications} = Application.ensure_all_started(:wallaby)
  Application.put_env(:wallaby, :base_url, HardenLlmWeb.Endpoint.url())
end

ExUnit.start(exclude: [:browser, :compose])
