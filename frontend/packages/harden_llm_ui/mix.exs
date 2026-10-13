defmodule HardenLlmUI.MixProject do
  use Mix.Project

  def project do
    [
      app: :harden_llm_ui,
      version: "1.0.0",
      elixir: "~> 1.20.2",
      elixirc_paths: if(Mix.env() == :test, do: ["lib", "test/support"], else: ["lib"]),
      compilers: [:phoenix_live_view] ++ Mix.compilers(),
      deps: [
        {:phoenix, "== 1.8.9"},
        {:phoenix_html, "~> 4.1"},
        {:phoenix_live_view, "== 1.2.10"},
        {:phoenix_template,
         git: "https://github.com/phoenixframework/phoenix_template.git",
         ref: "a5dd67cee1190bca4b7662ec3553373b5d67a0e6",
         override: true},
        {:gettext, "~> 1.0"},
        {:jason, "~> 1.2"},
        {:yaml_elixir, "== 2.12.1"},
        {:ex_json_schema, "== 0.11.5"},
        {:req, "== 0.6.1"},
        {:opentelemetry_api, "== 1.5.0"},
        {:lazy_html, "~> 0.1.13", only: :test}
      ]
    ]
  end

  def application, do: [extra_applications: [:logger]]
end
