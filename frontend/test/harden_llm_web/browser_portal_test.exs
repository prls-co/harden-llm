defmodule HardenLlmWeb.BrowserPortalTest do
  use HardenLlmWeb.ConnCase, async: true
  alias PrlsWeb.Access.Client

  # SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-110
  test "browser fixture serves the production Portal form and its product return URL" do
    target = "http://localhost:4000/"

    conn =
      Plug.Test.conn(:get, "/login?" <> URI.encode_query(%{"return_to" => target}))
      |> HardenLlmWeb.BrowserPortal.call([])

    assert conn.status == 200
    assert conn.resp_body =~ ~s(name="return_to" value="#{target}")

    assert conn.resp_body =~
             ~s(<meta name="viewport" content="width=device-width, initial-scale=1")

    assert conn.resp_body =~ ~s(<main class="prls-auth">)
    assert conn.resp_body =~ "<h1>Welcome back</h1>"
    assert conn.resp_body =~ ~s(action="/login" method="post" class="prls-form")

    assert conn.resp_body =~
             ~s(<label>Email<input type="email" name="email" autocomplete="username" required></label>)

    assert conn.resp_body =~
             ~s(<label>Password<input type="password" name="password" autocomplete="current-password" required></label>)

    assert conn.resp_body =~ ~s(<button type="submit" class="prls-button">Sign in</button>)
    assert conn.resp_body =~ ~s(name="_csrf_token")
  end

  test "browser fixture calls the owner through the real shared controller" do
    Req.Test.expect(Client, 1, fn request ->
      assert request.request_path == "/api/auth/sign-in/email"

      request
      |> put_resp_header("set-cookie", "prls.session_token=fixture; Path=/; HttpOnly")
      |> Req.Test.json(%{"ok" => true})
    end)

    conn =
      Plug.Test.conn(:post, "/login", %{
        "return_to" => "http://localhost:4000/",
        "email" => "fixture@example.test",
        "password" => "fixture-password"
      })
      |> put_private(:plug_skip_csrf_protection, true)
      |> HardenLlmWeb.BrowserPortal.call([])

    assert conn.status == 303
    assert get_resp_header(conn, "location") == ["http://localhost:4000/"]

    assert Enum.any?(
             get_resp_header(conn, "set-cookie"),
             &String.starts_with?(&1, "prls.session_token=fixture;")
           )
  end
end
