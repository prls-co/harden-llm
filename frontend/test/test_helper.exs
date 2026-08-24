arguments = System.argv()

browser_gate? =
  Enum.any?(arguments, fn argument ->
    argument in [
      "--only=browser",
      "--only=compose",
      "--only=deployed",
      "--include=browser",
      "--include=compose",
      "--include=deployed"
    ]
  end) or
    arguments
    |> Enum.chunk_every(2, 1, :discard)
    |> Enum.any?(fn
      [flag, tag] when flag in ["--only", "--include"] ->
        tag in ["browser", "compose", "deployed"]

      _pair ->
        false
    end)

if browser_gate? do
  Mix.Task.run("assets.build")
  {:ok, _applications} = Application.ensure_all_started(:wallaby)

  Application.put_env(
    :wallaby,
    :base_url,
    System.get_env("HARDEN_LLM_DEPLOYED_BASE_URL") || HardenLlmWeb.Endpoint.url()
  )
end

# SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 TEST-056
ExUnit.start(exclude: [:browser, :compose, :deployed])
