defmodule HardenLlmWeb.AccessFixtures do
  @moduledoc false

  alias PrlsWeb.Access.Context

  @account_id "11111111-1111-4111-8111-111111111111"
  @knowledge_account_id "22222222-2222-4222-8222-222222222222"
  @session_ref "fixture-control-plane-session-reference"
  @no_product_ref "fixture-control-plane-session-without-harden-access"
  @no_product_cookie "prls_session=no-product"

  def cookie, do: "prls_session=fixture"
  def no_product_cookie, do: @no_product_cookie
  def session_ref, do: @session_ref

  def resolve({:cookie, @no_product_cookie}, _options) do
    {:ok, context(@no_product_ref, ["knowledge"])}
  end

  def resolve({:cookie, "prls_session=fixture"}, _options) do
    {:ok, context(@session_ref, ["harden-llm"])}
  end

  def resolve({:reference, @session_ref}, _options),
    do: {:ok, context(@session_ref, ["harden-llm"])}

  def resolve({:reference, @no_product_ref}, _options),
    do: {:ok, context(@no_product_ref, ["knowledge"])}

  def resolve(_credential, _options), do: {:error, :unauthenticated}

  def control(:get, "/accounts", reference, nil, _options)
      when reference in [@session_ref, @no_product_ref] do
    current = if reference == @session_ref, do: ["harden-llm"], else: ["knowledge"]

    {:ok,
     %{
       "context" => context_map(reference, current),
       "accounts" => [
         account_map(@account_id, "Harden Test Account", ["harden-llm"]),
         account_map(@knowledge_account_id, "Knowledge Test Account", ["knowledge"])
       ]
     }}
  end

  def control(_method, _path, _reference, _body, _options),
    do: {:error, :unavailable}

  defp context(reference, products) do
    %Context{
      user_id: "fixture-user",
      email: "operator@example.test",
      name: "Test Operator",
      role: "operator",
      session_ref: reference,
      account: current_account(reference, products)
    }
  end

  defp context_map(reference, products) do
    %{
      "user_id" => "fixture-user",
      "email" => "operator@example.test",
      "name" => "Test Operator",
      "role" => "operator",
      "session_ref" => reference,
      "account" => account_map(account_id(reference), "Test Account", products)
    }
  end

  defp current_account(reference, products) do
    %{account_id: account_id(reference), name: "Test Account", products: products}
  end

  defp account_id(@no_product_ref), do: @knowledge_account_id
  defp account_id(_reference), do: @account_id

  defp account_map(id, name, products) do
    %{"account_id" => id, "name" => name, "products" => products}
  end
end
