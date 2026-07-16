import { app, ipcMain } from "electron";

// Launch-at-login (开机自启) support. `app.getLoginItemSettings` /
// `app.setLoginItemSettings` only wire up the OS login-item registry on macOS
// and Windows — Electron has no cross-desktop autostart API on Linux, so the
// calls are silent no-ops there. We advertise support per-platform and let the
// renderer hide the toggle where it can't take effect.
export function isLoginItemSupported(
  platform: NodeJS.Platform = process.platform,
): boolean {
  return platform === "darwin" || platform === "win32";
}

export interface LoginItemState {
  /** Whether this OS supports toggling launch-at-login from the app. */
  supported: boolean;
  /** Whether the app is currently registered to launch at login. */
  openAtLogin: boolean;
}

export function getLoginItemState(): LoginItemState {
  if (!isLoginItemSupported()) {
    return { supported: false, openAtLogin: false };
  }
  return {
    supported: true,
    openAtLogin: app.getLoginItemSettings().openAtLogin,
  };
}

export function setLoginItem(enabled: boolean): LoginItemState {
  if (isLoginItemSupported()) {
    app.setLoginItemSettings({ openAtLogin: enabled });
  }
  // Read back from the OS instead of trusting the input — the OS is the
  // source of truth, and on an unsupported platform this reflects the no-op.
  return getLoginItemState();
}

export function setupLoginItem(): void {
  ipcMain.handle("app:get-login-item", () => getLoginItemState());
  ipcMain.handle("app:set-login-item", (_event, enabled: boolean) =>
    setLoginItem(enabled === true),
  );
}
