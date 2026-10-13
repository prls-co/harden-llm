# HardenLlmUI

## 1. Ownership

This reusable Phoenix package owns profile/input/result/statistics/history/trace/JSON/pagination components, complete profile form transformations, an OpenAPI-driven server REST client, and shared styles/hooks. The host owns routes, Portal admission, draft selectors, page data and asynchronous workspace work. The widget keeps its existing local Save/refresh pending state. There are no host identity, Ecto or router imports.

## 2. Host integration

Add `{:harden_llm_ui, path: "packages/harden_llm_ui"}` (or the equivalent package source) to the host's Mix dependencies. Match the Phoenix/LiveView/Req versions pinned in this package's `mix.exs`.

Construct the connection once on the server:

```elixir
context = HardenLlmUI.Client.Context.new(
  base_url: "https://harden-llm.prls.co",
  api_key: deployment_key,
  draft_key: "workspace:" <> stable_portal_user_id,
  timeout_ms: 75_000
)
{:ok, catalog} = HardenLlmUI.Client.list_profiles(context)
```

Pass `api_context`, `profiles`, full `run_inputs`, `editor_draft`, `bundle_export_url` and a unique `id_prefix` to `ProfileWidgetComponent`. Handle `{:profile_widget, prefix, message}` in the host. Prefixes identify component instances; draft selectors partition unsaved edits within the server key's scope. The server key stays on the server. Shared data authority is the bearer, never a human session or caller-supplied scope hash.

`InputComponents.input/1`, `ResponseComponents.llm_result/1`, `StatsComponents.llm_stats/1`, `TraceComponents.llm_trace/1`, `HistoryComponents.history_row/1` and `Pagination.pagination/1` accept data and host event names. Captured accounting belongs to `TraceProjection`; components never reprice it. Keep summary history pages and load a full record through `Client.get_history/2` when its existing details control expands. Download through `Client.download_trace/2` or `Client.get_artifact/3`; bytes are preserved independently of history storage.

Import `assets/js/hooks.mjs` into the host LiveSocket hooks. Import `assets/css/ui.css` and `assets/css/json_viewer.css` into the host stylesheet and add this package's `lib/` as a Tailwind source. The reference app shows exact imports in `frontend/assets/`. The host owns Tailwind, Heroicons and asset compilation. There is no second stylesheet or decision implementation. The CLI imports the same package's `run_progress.mjs`; browser hooks import `client_core.mjs`.

The bundled `priv/openapi.yaml` is copied from canonical `api/openapi.yaml` and checked byte-for-byte by Go TEST-436. Update both in the same contract change. All reference response validation uses that document; do not add handwritten parallel schemas.

## 3. Verification

```bash
mix test --seed 104729
mix format --check-formatted
```

The standalone test host mounts two independent sets of components without the Harden-LLM host, Portal or a database. Private Req tests exercise explicit server contexts and canonical fixtures. Native browser layout and hook delivery require separate, explicitly requested browser checks.
