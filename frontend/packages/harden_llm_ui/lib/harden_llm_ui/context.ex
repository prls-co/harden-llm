defmodule HardenLlmUI.Client.Context do
  @moduledoc "Explicit server-held connection and draft selectors supplied by the host."
  @enforce_keys [:base_url, :api_key]
  defstruct [:base_url, :api_key, :draft_key, timeout_ms: 75_000, request_options: []]

  def new(options) when is_list(options) do
    context = struct!(__MODULE__, options)
    uri = URI.parse(context.base_url)

    unless uri.scheme in ["http", "https"] and is_binary(uri.host) and uri.host != "" and
             is_nil(uri.userinfo) and uri.query == nil and uri.fragment == nil and
             uri.path in [nil, "", "/"] do
      raise ArgumentError, "Client base_url must be an absolute HTTP(S) origin"
    end

    unless is_binary(context.api_key) and byte_size(context.api_key) in 32..512 and
             String.trim(context.api_key) == context.api_key and
             String.match?(context.api_key, ~r/^[!-~]+$/) do
      raise ArgumentError, "Client api_key must be a valid server-held bearer"
    end

    unless is_integer(context.timeout_ms) and context.timeout_ms in 1..120_000 do
      raise ArgumentError, "Client timeout_ms must be between 1 and 120000"
    end

    unless is_nil(context.draft_key) or
             (is_binary(context.draft_key) and byte_size(context.draft_key) in 1..128) do
      raise ArgumentError, "Client draft_key must contain between 1 and 128 bytes"
    end

    unless Keyword.keyword?(context.request_options) do
      raise ArgumentError, "Client request_options must be a keyword list"
    end

    context
  end
end
