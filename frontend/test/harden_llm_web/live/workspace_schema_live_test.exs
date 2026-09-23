defmodule HardenLlmWeb.WorkspaceSchemaLiveTest do
  use HardenLlmWeb.ConnCase, async: true

  import Phoenix.LiveViewTest, except: [live: 1, live: 2, live: 3]

  alias HardenLlmWeb.{APIFixtures, HardenAPI}

  setup %{conn: conn}, do: {:ok, conn: authenticated_conn(conn)}

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-057
  test "schema validation uses utility's contracted subset and gates selected structured runs", %{
    conn: conn
  } do
    install_schema_stub(fn conn ->
      case {conn.method, conn.request_path} do
        {"POST", "/api/v1/state"} ->
          {:ok, body, conn} = Plug.Conn.read_body(conn)
          Req.Test.json(conn, APIFixtures.success(nil, Jason.decode!(body)))

        {"POST", "/api/v1/run"} ->
          flunk("invalid schema reached backend")

        _ ->
          unexpected(conn)
      end
    end)

    {:ok, view, _html} = live(conn, ~p"/")
    render_async(view, 1_000)
    view |> element("#input-advanced-toggle") |> render_click()

    unsupported_schema =
      Jason.encode!(%{
        "type" => "object",
        "properties" => %{"answer" => %{"type" => "string"}},
        "required" => ["answer"],
        "additionalProperties" => false,
        "minLength" => 1
      })

    view
    |> form("#run-form", %{
      "run" => %{
        "selectedProfileId" => "Primary",
        "modelId" => "model-test",
        "userPrompt" => "schema gate",
        "callType" => "structured",
        "schema" => unsupported_schema
      }
    })
    |> render_change()

    assert has_element?(
             view,
             "#schema-status",
             "not part of the utility-llm contracted schema subset"
           )

    assert has_element?(view, "#run-submit[disabled]")

    view
    |> form("#run-form", %{
      "run" => %{
        "selectedProfileId" => "Primary",
        "modelId" => "model-test",
        "userPrompt" => "schema gate",
        "callType" => "structured",
        "schema" => unsupported_schema
      }
    })
    |> render_submit()

    assert has_element?(
             view,
             "#run-error",
             "not part of the utility-llm contracted schema subset"
           )

    valid_schema =
      Jason.encode!(%{
        "type" => "object",
        "properties" => %{"answer" => %{"type" => "string"}},
        "required" => ["answer"],
        "additionalProperties" => false
      })

    view
    |> form("#run-form", %{
      "run" => %{
        "selectedProfileId" => "Primary",
        "modelId" => "model-test",
        "userPrompt" => "schema gate",
        "callType" => "structured",
        "schema" => valid_schema
      }
    })
    |> render_change()

    refute has_element?(view, "#run-error")
    assert has_element?(view, "#run-submit:not([disabled])")
  end

  test "structured calls reject invalid local JSON without backend mutation", %{conn: conn} do
    install_schema_stub(fn conn ->
      if conn.request_path == "/api/v1/run",
        do: flunk("invalid schema reached backend"),
        else: unexpected(conn)
    end)

    {:ok, view, _html} = live(conn, ~p"/")
    render_async(view, 1_000)

    submit_run(view, %{"callType" => "structured", "schema" => "{invalid"})
    assert has_element?(view, "#run-error", "valid JSON object schema")
  end

  defp submit_run(view, overrides) do
    unless has_element?(view, "#advanced-input") do
      view |> element("#input-advanced-toggle") |> render_click()
    end

    params =
      Map.merge(
        %{
          "selectedProfileId" => "Primary",
          "modelId" => "model-test",
          "systemPrompt" => "",
          "userPrompt" => "fixture prompt",
          "callType" => "text",
          "cacheMode" => "cache",
          "schema" => ""
        },
        overrides
      )

    view |> form("#run-form", %{"run" => params}) |> render_submit()
  end

  defp install_schema_stub(handler) do
    Req.Test.stub(HardenAPI, fn conn ->
      case {conn.method, conn.request_path} do
        {"GET", "/api/v1/auth/session"} ->
          Req.Test.json(conn, APIFixtures.success(APIFixtures.principal()))

        {"GET", "/api/v1/state"} ->
          Req.Test.json(conn, APIFixtures.success(nil, APIFixtures.state()))

        {"GET", "/api/v1/profiles"} ->
          Req.Test.json(conn, APIFixtures.profiles([APIFixtures.profile_state()]))

        _ ->
          handler.(conn)
      end
    end)
  end

  defp unexpected(conn), do: flunk("unexpected API call: #{conn.method} #{conn.request_path}")
end
