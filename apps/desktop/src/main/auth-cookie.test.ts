import { describe, expect, it, vi } from "vitest";
import type { RuntimeConfigResult } from "../shared/runtime-config";
import {
  clearDesktopAuthCookie,
  DESKTOP_AUTH_COOKIE_NAME,
  installDesktopAuthCookie,
  type CookieSession,
} from "./auth-cookie";

function makeSession(): CookieSession & {
  cookies: CookieSession["cookies"] & {
    set: ReturnType<typeof vi.fn>;
    remove: ReturnType<typeof vi.fn>;
  };
} {
  return {
    cookies: {
      set: vi.fn(async () => undefined),
      remove: vi.fn(async () => undefined),
    },
  };
}

function okConfig(apiUrl: string): RuntimeConfigResult {
  return {
    ok: true,
    config: {
      schemaVersion: 1,
      apiUrl,
      wsUrl: "wss://multica.lilithgames.com/ws",
      appUrl: "https://multica.lilithgames.com",
    },
  };
}

describe("desktop auth cookie", () => {
  it("installs an HttpOnly auth cookie for the configured HTTPS API origin", async () => {
    const session = makeSession();

    await expect(
      installDesktopAuthCookie(
        session,
        okConfig("https://multica.lilithgames.com/api"),
        "jwt-token",
      ),
    ).resolves.toBe(true);

    expect(session.cookies.set).toHaveBeenCalledWith({
      url: "https://multica.lilithgames.com/",
      name: DESKTOP_AUTH_COOKIE_NAME,
      value: "jwt-token",
      path: "/",
      httpOnly: true,
      secure: true,
      sameSite: "no_restriction",
    });
  });

  it("uses a lax non-secure cookie for local HTTP development", async () => {
    const session = makeSession();

    await installDesktopAuthCookie(
      session,
      okConfig("http://localhost:8080"),
      "jwt-token",
    );

    expect(session.cookies.set).toHaveBeenCalledWith(
      expect.objectContaining({
        url: "http://localhost:8080/",
        secure: false,
        sameSite: "lax",
      }),
    );
  });

  it("clears the auth cookie from the configured API origin", async () => {
    const session = makeSession();

    await expect(
      clearDesktopAuthCookie(
        session,
        okConfig("https://multica.lilithgames.com"),
      ),
    ).resolves.toBe(true);

    expect(session.cookies.remove).toHaveBeenCalledWith(
      "https://multica.lilithgames.com/",
      DESKTOP_AUTH_COOKIE_NAME,
    );
  });

  it("does not install a cookie without a token or valid runtime config", async () => {
    const session = makeSession();

    await expect(
      installDesktopAuthCookie(session, okConfig("https://example.test"), " "),
    ).resolves.toBe(false);
    await expect(
      installDesktopAuthCookie(
        session,
        { ok: false, error: { message: "bad config" } },
        "jwt-token",
      ),
    ).resolves.toBe(false);

    expect(session.cookies.set).not.toHaveBeenCalled();
  });
});
