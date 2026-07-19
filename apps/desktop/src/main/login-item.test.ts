import { describe, expect, it, vi, beforeEach } from "vitest";

vi.mock("electron", () => ({
  app: {
    getLoginItemSettings: vi.fn(() => ({ openAtLogin: false })),
    setLoginItemSettings: vi.fn(),
  },
  ipcMain: { handle: vi.fn() },
}));

import { app } from "electron";
import {
  isLoginItemSupported,
  getLoginItemState,
  setLoginItem,
} from "./login-item";

describe("isLoginItemSupported", () => {
  it("supports macOS and Windows", () => {
    expect(isLoginItemSupported("darwin")).toBe(true);
    expect(isLoginItemSupported("win32")).toBe(true);
  });

  it("does not support Linux or other platforms", () => {
    expect(isLoginItemSupported("linux")).toBe(false);
    expect(isLoginItemSupported("freebsd")).toBe(false);
  });
});

describe("getLoginItemState", () => {
  beforeEach(() => {
    vi.mocked(app.getLoginItemSettings).mockClear();
    vi.mocked(app.getLoginItemSettings).mockReturnValue({
      openAtLogin: true,
    } as ReturnType<typeof app.getLoginItemSettings>);
  });

  it("reads openAtLogin from the OS on a supported platform", () => {
    // Test process runs on a supported platform in CI (macOS/Windows/Linux).
    // Guard on the real support result so the assertion holds everywhere.
    if (!isLoginItemSupported()) {
      expect(getLoginItemState()).toEqual({
        supported: false,
        openAtLogin: false,
      });
      return;
    }
    expect(getLoginItemState()).toEqual({ supported: true, openAtLogin: true });
    expect(app.getLoginItemSettings).toHaveBeenCalled();
  });
});

describe("setLoginItem", () => {
  beforeEach(() => {
    vi.mocked(app.setLoginItemSettings).mockClear();
    vi.mocked(app.getLoginItemSettings).mockReturnValue({
      openAtLogin: true,
    } as ReturnType<typeof app.getLoginItemSettings>);
  });

  it("writes the OS login item and returns the read-back state on a supported platform", () => {
    if (!isLoginItemSupported()) {
      expect(setLoginItem(true)).toEqual({
        supported: false,
        openAtLogin: false,
      });
      expect(app.setLoginItemSettings).not.toHaveBeenCalled();
      return;
    }
    expect(setLoginItem(true)).toEqual({ supported: true, openAtLogin: true });
    expect(app.setLoginItemSettings).toHaveBeenCalledWith({
      openAtLogin: true,
    });
  });
});
