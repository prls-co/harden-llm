ExUnit.start()

{:ok, _} =
  Supervisor.start_link(
    [{Phoenix.PubSub, name: HardenLlmUI.TestPubSub}, HardenLlmUI.TestEndpoint],
    strategy: :one_for_one
  )
