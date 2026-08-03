import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./test/setup.ts"],
    include: ["**/*.test.{ts,tsx}"],
    // Two kinds of case here are slow by nature, not by fault. Opening a Base
    // UI dialog costs ~2s under jsdom before a test does anything of its own,
    // so a case that opens one and drives a form sits at 4-5s idle. The cron
    // grammar's property tests run thousands of expressions against a
    // reference parser and take ~15s on their own. Both crossed the 5s default
    // the moment files ran in parallel, and every failure was a timeout rather
    // than an assertion — the same cases pass when run alone. A timeout should
    // catch a test that hangs, not one that is merely slow on a loaded runner.
    testTimeout: 30_000,
  },
});
