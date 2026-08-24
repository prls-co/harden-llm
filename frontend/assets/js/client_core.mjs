function text(value) {
  return value == null ? "" : String(value);
}

export function normalizeSearch(value) {
  return text(value).trim().toLowerCase();
}

export function visibleOptionIndices(options, query) {
  const normalizedQuery = normalizeSearch(query);

  return options.reduce((visible, option, index) => {
    const search = normalizeSearch(option?.search ?? option?.value);
    if (normalizedQuery === "" || search.includes(normalizedQuery)) visible.push(index);
    return visible;
  }, []);
}

export function emptyStateVisible(visibleCount) {
  return visibleCount === 0;
}

export function highlightIndex(current, direction, count) {
  if (count <= 0) return -1;
  return ((current + direction) % count + count) % count;
}

function knownValue(value, knownValues) {
  return knownValues.some(candidate => text(candidate) === value);
}

export function commitValue({value, committed, knownValues = [], allowCustom = false}) {
  const next = text(value);
  const previous = text(committed);

  if (knownValue(next, knownValues)) {
    return {action: next === previous ? "noop" : "known", value: next};
  }
  if (allowCustom && next !== previous) return {action: "custom", value: next};
  if (next === previous) return {action: "noop", value: previous};
  return {action: "revert", value: previous};
}

export function escapeValue({committed}) {
  return {action: "revert", value: text(committed), close: true};
}

export function blurValue({value, committed, knownValues = [], allowCustom = false}) {
  const next = text(value);
  const previous = text(committed);

  if (allowCustom && next !== previous) {
    return {action: "custom", value: next, close: true};
  }
  if (!allowCustom && !knownValue(next, knownValues)) {
    return {action: "revert", value: previous, close: true};
  }
  return {action: "close", value: next, close: true};
}

export function isSubmitShortcut(event = {}) {
  return event.key === "Enter" &&
    (event.ctrlKey === true || event.metaKey === true) &&
    event.altKey !== true &&
    event.shiftKey !== true;
}

export function schemaPendingState(value) {
  if (normalizeSearch(value) === "") {
    return {pending: false, message: "", className: "", role: null};
  }
  return {
    pending: true,
    message: "Schema check pending.",
    className: "text-xs text-slate-500",
    role: null,
  };
}
