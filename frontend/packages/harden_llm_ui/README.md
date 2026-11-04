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

### 2.1. Selection-only profile rows

Hosts that only select a saved profile and reasoning level can render
`HardenLlmUI.ProfileWidgetComponent.profile_row/1` without mounting the full
LiveComponent/editor. Keep the canonical `catalog["profiles"]` maps on the
server; build options with `ProfileWidgetState.profile_options/1` and
`reasoning_options/2`, also used by the full widget. For example, inside a host
form whose `phx-change` targets its owning LiveComponent:

```heex
<HardenLlmUI.ProfileWidgetComponent.profile_row
  category="Query generation"
  profile_input_id={@id <> "-query-profile"}
  profile_name="run[query_profile_id]"
  profile_value={@query_profile_id}
  profile_options={HardenLlmUI.ProfileWidgetState.profile_options(@profiles)}
  profile_allow_custom={false}
  profile_required={true}
  profile_change={nil}
  reasoning_input_id={@id <> "-query-reasoning"}
  reasoning_name="run[query_reasoning_level]"
  reasoning_value={@query_reasoning_level}
  reasoning_options={HardenLlmUI.ProfileWidgetState.reasoning_options(@profiles, @query_profile_id)}
  reasoning_change={nil}
  target={@myself}
/>
```

The host handles the normal nested `{"run": {...}}` form event, validates IDs
against its current eligible catalog, and submits its own product request.
Use `nil` change attributes to inherit the containing form's change event and
serialize all current form fields. A string/JS event may instead target one
row field when the host intentionally handles field-only changes.
Initialize once with `resolve_selected_profile_id/2` and use
`profile_reasoning_effort/2` to resolve configured reasoning. Explicit unknown
profile IDs do not select another profile; their reasoning options are empty.
The blank-ID reasoning choices preserve the full editor's draft behavior,
not permission to run with an invalid product selection.

Do not pass optional search/cache/config/model IDs when those controls are not
part of the host's form. The row itself performs no HTTP, draft writes, Save
or inference and requires no Portal context. Use the same hooks and `ui.css`;
selection-only hosts need no JSON viewer or full-editor asset integration.
Profile creation/editing remains the full widget's responsibility. Give every
row a distinct ID even across embedded workspaces. The host owns access checks,
catalog refresh and stale-response handling; never pass the server key in
component attributes or hook data.

## 3. Verification

```bash
mix test --seed 104729
mix format --check-formatted
```

The standalone test host mounts two independent sets of components without the Harden-LLM host, Portal or a database. Private Req tests exercise explicit server contexts and canonical fixtures. Native browser layout and hook delivery require separate, explicitly requested browser checks.
