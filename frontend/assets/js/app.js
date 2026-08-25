// If you want to use Phoenix channels, run `mix help phx.gen.channel`
// to get started and then uncomment the line below.
// import "./user_socket.js"

// You can include dependencies in two ways.
//
// The simplest option is to put them in assets/vendor and
// import them using relative paths:
//
//     import "../vendor/some-package.js"
//
// Alternatively, you can `npm install some-package --prefix assets` and import
// them using a path starting with the package name:
//
//     import "some-package"
//
// If you have dependencies that try to import CSS, esbuild will generate a separate `app.css` file.
// To load it, simply add a second `<link>` to your `root.html.heex` file.

// Include phoenix_html to handle method=PUT/DELETE in forms and buttons.
import "phoenix_html"
// Establish Phoenix Socket and LiveView configuration.
import {Socket} from "phoenix"
import {LiveSocket} from "phoenix_live_view"
import {hooks as colocatedHooks} from "phoenix-colocated/harden_llm"
import topbar from "../vendor/topbar"
import {
  blurValue,
  commitValue,
  emptyStateVisible,
  escapeValue,
  focusValue,
  highlightIndex,
  isSubmitShortcut,
  normalizeSearch,
  schemaPendingState,
  visibleOptionIndices,
} from "./client_core.mjs"

const Clipboard = {
  mounted() {
    this.copy = async () => {
      const value = this.el.dataset.copyValue || ""
      const label = this.el.textContent
      try {
        await navigator.clipboard.writeText(value)
        this.el.textContent = "Copied"
        window.setTimeout(() => {
          if (this.el.isConnected) this.el.textContent = label
        }, 1200)
      } catch (_error) {
        this.el.textContent = "Copy failed"
        window.setTimeout(() => {
          if (this.el.isConnected) this.el.textContent = label
        }, 1200)
      }
    }
    this.el.addEventListener("click", this.copy)
  },
  destroyed() {
    this.el.removeEventListener("click", this.copy)
  },
}

const PromptShortcut = {
  mounted() {
    this.submit = event => {
      if (!isSubmitShortcut(event)) return
      const form = document.getElementById(this.el.dataset.formId || "run-form")
      if (!form || form.querySelector("button[type='submit']")?.disabled) return
      event.preventDefault()
      form.requestSubmit()
    }
    this.el.addEventListener("keydown", this.submit)
  },
  destroyed() {
    this.el.removeEventListener("keydown", this.submit)
  },
}

const SchemaPending = {
  mounted() {
    this.markPending = () => {
      const status = document.getElementById(this.el.dataset.statusId || "schema-status")
      if (!status) return
      const state = schemaPendingState(this.el.value)
      status.textContent = state.message
      if (state.pending) status.className = state.className
      status.removeAttribute("role")
    }
    this.el.addEventListener("input", this.markPending)
  },
  destroyed() {
    this.el.removeEventListener("input", this.markPending)
  },
}

const SearchableCombobox = {
  mounted() {
    this.bindCombobox()
  },
  updated() {
    this.unbindCombobox()
    this.bindCombobox()
  },
  destroyed() {
    this.unbindCombobox()
  },
  bindCombobox() {
    this.input = this.el.querySelector("input[role='combobox']")
    this.menu = this.el.querySelector("[role='listbox']")
    this.options = [...this.el.querySelectorAll("[role='option']")]
    this.empty = this.el.querySelector(".ullm-combobox-empty")
    if (!this.input || !this.menu) return

    this.allowCustom = this.el.dataset.allowCustom === "true"
    this.committed = this.input.value
    this.highlighted = -1
    this.open = false
    this.onInput = () => {
      this.filterOptions()
      this.openMenu()
    }
    this.onFocus = () => {
      const decision = focusValue({value: this.committed})
      this.input.value = decision.value
      this.input.select()
      this.filterOptions()
      this.openMenu()
    }
    this.onKeydown = event => {
      if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        event.preventDefault()
        this.moveHighlight(event.key === "ArrowDown" ? 1 : -1)
      } else if (event.key === "Enter") {
        if (this.highlighted >= 0) {
          event.preventDefault()
          this.selectOption(this.visibleOptions()[this.highlighted])
        } else if (this.allowCustom && this.input.value !== this.committed) {
          event.preventDefault()
          this.commitCustomValue()
        }
      } else if (event.key === "Escape") {
        event.preventDefault()
        this.input.value = escapeValue({committed: this.committed}).value
        this.closeMenu()
      }
    }
    this.onBlur = () => {
      window.setTimeout(() => {
        if (!this.el.contains(document.activeElement)) {
          const decision = blurValue({
            value: this.input.value,
            committed: this.committed,
            knownValues: this.options.map(option => option.dataset.value || ""),
            allowCustom: this.allowCustom,
          })
          if (decision.action === "custom") this.commitCustomValue()
          else if (decision.action === "revert") this.input.value = decision.value
          this.closeMenu()
        }
      }, 0)
    }
    this.onOptionClick = event => {
      const option = event.target.closest("[role='option']")
      if (option) this.selectOption(option)
    }
    this.onMenuMouseDown = event => event.preventDefault()

    this.input.addEventListener("input", this.onInput)
    this.input.addEventListener("focus", this.onFocus)
    this.input.addEventListener("keydown", this.onKeydown)
    this.input.addEventListener("blur", this.onBlur)
    this.menu.addEventListener("mousedown", this.onMenuMouseDown)
    this.menu.addEventListener("click", this.onOptionClick)
    this.filterOptions()
  },
  unbindCombobox() {
    if (!this.input || !this.menu) return
    this.input.removeEventListener("input", this.onInput)
    this.input.removeEventListener("focus", this.onFocus)
    this.input.removeEventListener("keydown", this.onKeydown)
    this.input.removeEventListener("blur", this.onBlur)
    this.menu.removeEventListener("mousedown", this.onMenuMouseDown)
    this.menu.removeEventListener("click", this.onOptionClick)
    this.input = null
    this.menu = null
  },
  visibleOptions() {
    return this.options.filter(option => !option.hidden)
  },
  filterOptions() {
    const options = this.options.map(option => ({
      search: normalizeSearch(option.dataset.search || option.dataset.value || ""),
      value: option.dataset.value || "",
    }))
    const visibleIndices = new Set(visibleOptionIndices(options, this.input.value))
    this.options.forEach((option, index) => {
      option.hidden = !visibleIndices.has(index)
      option.classList.remove("is-highlighted")
    })
    this.empty.hidden = !emptyStateVisible(visibleIndices.size)
    this.highlighted = -1
  },
  openMenu() {
    this.menu.hidden = false
    this.input.setAttribute("aria-expanded", "true")
    this.open = true
  },
  closeMenu() {
    this.menu.hidden = true
    this.input.setAttribute("aria-expanded", "false")
    this.open = false
    this.highlighted = -1
    this.options.forEach(option => option.classList.remove("is-highlighted"))
  },
  moveHighlight(direction) {
    const visible = this.visibleOptions()
    if (!visible.length) return
    this.highlighted = highlightIndex(this.highlighted, direction, visible.length)
    this.options.forEach(option => option.classList.remove("is-highlighted"))
    visible[this.highlighted].classList.add("is-highlighted")
    visible[this.highlighted].scrollIntoView({block: "nearest"})
  },
  commitCustomValue() {
    const decision = commitValue({
      value: this.input.value,
      committed: this.committed,
      knownValues: this.options.map(option => option.dataset.value || ""),
      allowCustom: true,
    })
    if (decision.action === "custom" || decision.action === "known") {
      this.committed = decision.value
      this.dispatchChange()
    }
    this.closeMenu()
  },
  selectOption(option) {
    if (!option) return
    const value = option.dataset.value || ""
    const decision = commitValue({
      value,
      committed: this.committed,
      knownValues: this.options.map(item => item.dataset.value || ""),
      allowCustom: this.allowCustom,
    })
    this.input.value = decision.value
    if (decision.action !== "revert") {
      this.committed = decision.value
      this.dispatchChange()
    }
    this.closeMenu()
  },
  dispatchChange() {
    this.input.dispatchEvent(new Event("change", {bubbles: true}))
  },
}

const SecretStager = {
  mounted() {
    this.stage = event => {
      const button = event.target.closest("[data-stage-key]")
      if (!button || !this.el.contains(button)) return
      const input = this.el.querySelector("[data-secret-input]")
      if (!input) return
      button.setAttribute("phx-value-api-key", input.value)
      window.setTimeout(() => {
        if (button.isConnected) button.removeAttribute("phx-value-api-key")
      }, 0)
    }
    this.el.addEventListener("click", this.stage, true)
  },
  destroyed() {
    this.el.removeEventListener("click", this.stage, true)
  },
}

const csrfToken = document.querySelector("meta[name='csrf-token']").getAttribute("content")
const liveSocket = new LiveSocket("/live", Socket, {
  longPollFallbackMs: 2500,
  params: {_csrf_token: csrfToken},
  hooks: {...colocatedHooks, Clipboard, PromptShortcut, SchemaPending, SearchableCombobox, SecretStager},
})

// Show progress bar on live navigation and form submits
topbar.config({barColors: {0: "#29d"}, shadowColor: "rgba(0, 0, 0, .3)"})
window.addEventListener("phx:page-loading-start", _info => topbar.show(300))
window.addEventListener("phx:page-loading-stop", _info => topbar.hide())

// connect if there are any LiveViews on the page
liveSocket.connect()

// expose liveSocket on window for web console debug logs and latency simulation:
// >> liveSocket.enableDebug()
// >> liveSocket.enableLatencySim(1000)  // enabled for duration of browser session
// >> liveSocket.disableLatencySim()
window.liveSocket = liveSocket

// The lines below enable quality of life phoenix_live_reload
// development features:
//
//     1. stream server logs to the browser console
//     2. click on elements to jump to their definitions in your code editor
//
if (process.env.NODE_ENV === "development") {
  window.addEventListener("phx:live_reload:attached", ({detail: reloader}) => {
    // Enable server log streaming to client.
    // Disable with reloader.disableServerLogs()
    reloader.enableServerLogs()

    // Open configured PLUG_EDITOR at file:line of the clicked element's HEEx component
    //
    //   * click with "c" key pressed to open at caller location
    //   * click with "d" key pressed to open at function component definition location
    let keyDown
    window.addEventListener("keydown", e => keyDown = e.key)
    window.addEventListener("keyup", _e => keyDown = null)
    window.addEventListener("click", e => {
      if(keyDown === "c"){
        e.preventDefault()
        e.stopImmediatePropagation()
        reloader.openEditorAtCaller(e.target)
      } else if(keyDown === "d"){
        e.preventDefault()
        e.stopImmediatePropagation()
        reloader.openEditorAtDef(e.target)
      }
    }, true)

    window.liveReloader = reloader
  })
}
