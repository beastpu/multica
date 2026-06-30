"use client";

import { create } from "zustand";
import { createJSONStorage, persist } from "zustand/middleware";
import { defaultStorage } from "../platform/storage";

// Resizable columns of the operations table. "原因/描述" is the flex filler
// (absorbs slack) and "时间" is a fixed slim date column pinned last, so only
// these three leading columns carry an explicit, user-draggable width. Widths
// persist per device (localStorage) so a tuned layout survives reloads.
export type OperationsColumnKey = "agent" | "issue" | "status";

// A column can't shrink into nothing or run away and bury the others.
export const OPERATIONS_COLUMN_MIN = 80;
export const OPERATIONS_COLUMN_MAX = 900;

/** Clamp a candidate width to the allowed range (rounded to whole px). */
export function clampOperationsColumnWidth(width: number): number {
  return Math.min(
    OPERATIONS_COLUMN_MAX,
    Math.max(OPERATIONS_COLUMN_MIN, Math.round(width)),
  );
}

// Defaults give 关联 issue extra room up front since it carries the longest
// scannable content; agent/status mirror their previous sizes.
export const OPERATIONS_DEFAULT_WIDTHS: Record<OperationsColumnKey, number> = {
  agent: 200,
  issue: 340,
  status: 120,
};

export const OPERATIONS_COLUMN_KEYS = Object.keys(
  OPERATIONS_DEFAULT_WIDTHS,
) as OperationsColumnKey[];

export interface OperationsViewState {
  columnWidths: Record<OperationsColumnKey, number>;
  setColumnWidth: (key: OperationsColumnKey, width: number) => void;
  resetColumnWidths: () => void;
}

export const useOperationsViewStore = create<OperationsViewState>()(
  persist(
    (set) => ({
      columnWidths: { ...OPERATIONS_DEFAULT_WIDTHS },
      setColumnWidth: (key, width) =>
        set((s) => ({
          columnWidths: {
            ...s.columnWidths,
            [key]: clampOperationsColumnWidth(width),
          },
        })),
      resetColumnWidths: () =>
        set({ columnWidths: { ...OPERATIONS_DEFAULT_WIDTHS } }),
    }),
    {
      name: "multica_operations_view",
      storage: createJSONStorage(() => defaultStorage),
      partialize: (s) => ({ columnWidths: s.columnWidths }),
      // A payload persisted before a column existed — or with a junk/NaN value —
      // must fall back to that column's default, never undefined: a bad value
      // would render `NaNpx` in the grid template and collapse the column.
      merge: (persisted, current) => {
        const saved =
          (persisted as Partial<OperationsViewState> | undefined)
            ?.columnWidths ?? {};
        const widths = {} as Record<OperationsColumnKey, number>;
        for (const key of OPERATIONS_COLUMN_KEYS) {
          const v = (saved as Record<string, unknown>)[key];
          widths[key] =
            typeof v === "number" && Number.isFinite(v)
              ? clampOperationsColumnWidth(v)
              : OPERATIONS_DEFAULT_WIDTHS[key];
        }
        return { ...current, columnWidths: widths };
      },
    },
  ),
);
