defmodule HardenLlmWeb.AccessFixtures do
  @moduledoc false
  alias PrlsWeb.Access.{Client, Context}

  @session_ref "fixture-control-plane-session-reference"
  @company_ref "fixture-company-session-reference"
  @second_ref "fixture-second-user-session-reference"
  @dynamic_ref "fixture-dynamic-session-reference"

  def cookie, do: "prls.session_token=fixture"
  def company_cookie, do: "prls.session_token=company"
  def second_cookie, do: "prls.session_token=second"
  def dynamic_cookie, do: "prls.session_token=dynamic"
  def session_ref, do: @session_ref
  def second_ref, do: @second_ref
  def dynamic_ref, do: @dynamic_ref

  def resolve({:cookie, header} = credential, options) when is_binary(header) do
    case cookie_value(header) do
      "fixture" -> {:ok, context(@session_ref)}
      "company" -> {:ok, %{context(@company_ref) | account: company("knowledge")}}
      "second" -> {:ok, %{context(@second_ref) | user_id: "fixture-second-user"}}
      "dynamic" -> Client.resolve(credential, options)
      _ -> {:error, :unauthenticated}
    end
  end

  def resolve({:reference, @dynamic_ref} = credential, options),
    do: Client.resolve(credential, options)

  def resolve({:reference, @session_ref}, _options), do: {:ok, context(@session_ref)}

  def resolve({:reference, @company_ref}, _options),
    do: {:ok, %{context(@company_ref) | account: company("knowledge")}}

  def resolve({:reference, @second_ref}, _options),
    do: {:ok, %{context(@second_ref) | user_id: "fixture-second-user"}}

  def resolve(_credential, _options), do: {:error, :unauthenticated}

  defp cookie_value(header) do
    header
    |> String.split(";")
    |> Enum.find_value(fn pair ->
      case String.split(String.trim(pair), "=", parts: 2) do
        ["prls.session_token", value] -> value
        _ -> nil
      end
    end)
  end

  defp context(reference) do
    %Context{
      user_id: "fixture-user",
      email: "operator@example.test",
      name: "Test Operator",
      role: "operator",
      session_ref: reference,
      account: nil
    }
  end

  defp company(product),
    do: %{
      account_id: "11111111-1111-4111-8111-111111111111",
      name: "Other product company",
      products: [product]
    }
end
