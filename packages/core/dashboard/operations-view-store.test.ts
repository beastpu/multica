import { describe, it, expect, beforeEach } from "vitest";
import {
  useOperationsViewStore,
  clampOperationsColumnWidth,
  OPERATIONS_DEFAULT_WIDTHS,
  OPERATIONS_COLUMN_MIN,
  OPERATIONS_COLUMN_MAX,
  type OperationsViewState,
} from "./operations-view-store";

describe("clampOperationsColumnWidth", () => {
  it("floors at the minimum", () => {
    expect(clampOperationsColumnWidth(10)).toBe(OPERATIONS_COLUMN_MIN);
  });

  it("caps at the maximum", () => {
    expect(clampOperationsColumnWidth(5000)).toBe(OPERATIONS_COLUMN_MAX);
  });

  it("rounds fractional pixels", () => {
    expect(clampOperationsColumnWidth(321.6)).toBe(322);
  });
});

describe("useOperationsViewStore", () => {
  beforeEach(() => {
    useOperationsViewStore.getState().resetColumnWidths();
  });

  it("starts at the default widths", () => {
    expect(useOperationsViewStore.getState().columnWidths).toEqual(
      OPERATIONS_DEFAULT_WIDTHS,
    );
  });

  it("setColumnWidth clamps and only touches the named column", () => {
    useOperationsViewStore.getState().setColumnWidth("issue", 10_000);
    const widths = useOperationsViewStore.getState().columnWidths;
    expect(widths.issue).toBe(OPERATIONS_COLUMN_MAX);
    // Untouched columns keep their defaults.
    expect(widths.agent).toBe(OPERATIONS_DEFAULT_WIDTHS.agent);
    expect(widths.status).toBe(OPERATIONS_DEFAULT_WIDTHS.status);
  });

  it("resetColumnWidths restores every default", () => {
    useOperationsViewStore.getState().setColumnWidth("agent", 400);
    useOperationsViewStore.getState().resetColumnWidths();
    expect(useOperationsViewStore.getState().columnWidths).toEqual(
      OPERATIONS_DEFAULT_WIDTHS,
    );
  });
});

// The persist `merge` is the defensive boundary for layouts saved on an older
// build (a missing column) or corrupted on disk (a NaN/string width). A bad
// value must fall back to that column's default — never leak `undefined`/`NaN`
// into the grid template, which would collapse the column.
describe("operations view store merge", () => {
  const merge = useOperationsViewStore.persist.getOptions().merge as (
    persisted: unknown,
    current: OperationsViewState,
  ) => OperationsViewState;

  it("fills missing and non-finite widths with defaults", () => {
    const current = useOperationsViewStore.getState();
    const merged = merge(
      { columnWidths: { issue: Number.NaN, status: 200 } },
      current,
    );
    expect(merged.columnWidths.issue).toBe(OPERATIONS_DEFAULT_WIDTHS.issue);
    expect(merged.columnWidths.agent).toBe(OPERATIONS_DEFAULT_WIDTHS.agent);
    expect(merged.columnWidths.status).toBe(200);
  });

  it("clamps an out-of-range persisted width", () => {
    const current = useOperationsViewStore.getState();
    const merged = merge({ columnWidths: { agent: 99_999 } }, current);
    expect(merged.columnWidths.agent).toBe(OPERATIONS_COLUMN_MAX);
  });

  it("tolerates a completely absent payload", () => {
    const current = useOperationsViewStore.getState();
    const merged = merge(undefined, current);
    expect(merged.columnWidths).toEqual(OPERATIONS_DEFAULT_WIDTHS);
  });
});
