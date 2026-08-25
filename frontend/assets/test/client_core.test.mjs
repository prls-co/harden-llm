import test from "node:test";
import assert from "node:assert/strict";

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
} from "../js/client_core.mjs";

// SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001 WEB-TEST-046 TEST-046
// PLAN-HLLM-WIDGET-PARITY-001 TEST-111

test("focus selects the committed value without requiring a DOM", () => {
  assert.deepEqual(focusValue({value: "gpt-5.6-luna"}), {
    action: "select",
    value: "gpt-5.6-luna",
  });
});

test("normalizes search text and returns visible option indices", () => {
  assert.equal(normalizeSearch("  CPA GPT-5.6 Luna "), "cpa gpt-5.6 luna");

  const options = [
    {search: "CPA GPT-5.6 Luna", value: "gpt-5.6-luna"},
    {search: "Repair model", value: "repair-model"},
    {value: "Custom", search: ""},
  ];

  const cases = [
    {query: "", expected: [0, 1, 2]},
    {query: "LUNA", expected: [0]},
    {query: "repair", expected: [1]},
    {query: "missing", expected: []},
  ];

  for (const {query, expected} of cases) {
    assert.deepEqual(visibleOptionIndices(options, query), expected);
  }
});

test("reports empty-state visibility from the visible option count", () => {
  assert.equal(emptyStateVisible(0), true);
  assert.equal(emptyStateVisible(1), false);
});

test("wraps highlighted option indices in either direction", () => {
  const cases = [
    {current: -1, direction: 1, count: 3, expected: 0},
    {current: 0, direction: -1, count: 3, expected: 2},
    {current: 2, direction: 1, count: 3, expected: 0},
    {current: 0, direction: 1, count: 0, expected: -1},
  ];

  for (const {current, direction, count, expected} of cases) {
    assert.equal(highlightIndex(current, direction, count), expected);
  }
});

test("commits known values, allowed custom values, or a safe reversion", () => {
  const cases = [
    {
      input: {value: "repair-model", committed: "primary-model", knownValues: ["repair-model"], allowCustom: false},
      expected: {action: "known", value: "repair-model"},
    },
    {
      input: {value: "custom-model", committed: "primary-model", knownValues: ["primary-model"], allowCustom: true},
      expected: {action: "custom", value: "custom-model"},
    },
    {
      input: {value: "unknown-model", committed: "primary-model", knownValues: ["primary-model"], allowCustom: false},
      expected: {action: "revert", value: "primary-model"},
    },
    {
      input: {value: "primary-model", committed: "primary-model", knownValues: ["primary-model"], allowCustom: true},
      expected: {action: "noop", value: "primary-model"},
    },
  ];

  for (const {input, expected} of cases) {
    assert.deepEqual(commitValue(input), expected);
  }
});

test("escape always restores the committed value and blur follows the ownership rule", () => {
  assert.deepEqual(escapeValue({committed: "primary-model"}), {
    action: "revert",
    value: "primary-model",
    close: true,
  });

  const cases = [
    {
      input: {value: "custom-model", committed: "primary-model", knownValues: [], allowCustom: true},
      expected: {action: "custom", value: "custom-model", close: true},
    },
    {
      input: {value: "unknown-model", committed: "primary-model", knownValues: ["primary-model"], allowCustom: false},
      expected: {action: "revert", value: "primary-model", close: true},
    },
    {
      input: {value: "primary-model", committed: "primary-model", knownValues: ["primary-model"], allowCustom: false},
      expected: {action: "close", value: "primary-model", close: true},
    },
  ];

  for (const {input, expected} of cases) {
    assert.deepEqual(blurValue(input), expected);
  }
});

test("qualifies only the intended modified-enter submit shortcut", () => {
  const cases = [
    {event: {key: "Enter", ctrlKey: true}, expected: true},
    {event: {key: "Enter", metaKey: true}, expected: true},
    {event: {key: "Enter", ctrlKey: true, altKey: true}, expected: false},
    {event: {key: "Enter", metaKey: true, shiftKey: true}, expected: false},
    {event: {key: "Enter"}, expected: false},
    {event: {key: "Escape", ctrlKey: true}, expected: false},
  ];

  for (const {event, expected} of cases) {
    assert.equal(isSubmitShortcut(event), expected);
  }
});

test("returns schema-pending presentation without DOM state", () => {
  assert.deepEqual(schemaPendingState(""), {
    pending: false,
    message: "",
    className: "",
    role: null,
  });
  assert.deepEqual(schemaPendingState("  {\"answer\":\"string\"}"), {
    pending: true,
    message: "Schema check pending.",
    className: "text-xs text-slate-500",
    role: null,
  });
});
