import {
  blurValue,
  commitValue,
  comboboxKeyAction,
  emptyStateVisible,
  escapeValue,
  focusValue,
  highlightIndex,
  isSubmitShortcut,
  normalizeSearch,
  schemaCheckPayload,
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
        this.el.textContent = this.el.dataset.copySuccess || "Copied"
        window.setTimeout(() => {
          if (this.el.isConnected) this.el.textContent = label
        }, 1200)
      } catch (_error) {
        this.el.textContent = this.el.dataset.copyError || "Failed"
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
      status.className = state.className
      status.removeAttribute("role")

      const run = document.getElementById(this.el.dataset.runSubmitId || "run-submit")
      if (!run) return

      if (state.pending) {
        if (!run.disabled) {
          run.disabled = true
          run.dataset.schemaPending = "true"
        }
      } else if (run.dataset.schemaPending === "true") {
        run.disabled = false
        delete run.dataset.schemaPending
      }
    }
    this.el.addEventListener("input", this.markPending)
  },
  destroyed() {
    this.el.removeEventListener("input", this.markPending)
  },
}

const SchemaCheck = {
  mounted() {
    this.check = event => {
      event.preventDefault()
      event.stopImmediatePropagation()
      const form = document.getElementById(this.el.dataset.formId || "run-form")
      const schema = form?.querySelector("[name='run[schema]']")
      this.pushEvent("check-schema", schemaCheckPayload(schema?.value))
    }
    this.el.addEventListener("click", this.check)
  },
  destroyed() {
    this.el.removeEventListener("click", this.check)
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
      const action = comboboxKeyAction({key: event.key, highlighted: this.highlighted,
        allowCustom: this.allowCustom, value: this.input.value, committed: this.committed})
      if (action !== "ignore") event.preventDefault()
      if (action === "next" || action === "previous") {
        this.moveHighlight(action === "next" ? 1 : -1)
      } else if (action === "select") {
        this.selectOption(this.visibleOptions()[this.highlighted])
      } else if (action === "commit") {
        this.commitCustomValue()
      } else if (action === "escape") {
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

export const hooks = {Clipboard, PromptShortcut, SchemaPending, SchemaCheck, SearchableCombobox, SecretStager};
