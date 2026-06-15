import type { RuntimeConfigResult } from "../shared/runtime-config";

export const DESKTOP_AUTH_COOKIE_NAME = "multica_auth";

type CookieSameSite = "unspecified" | "no_restriction" | "lax" | "strict";

interface CookieDetails {
  url: string;
  name: string;
  value: string;
  path?: string;
  httpOnly?: boolean;
  secure?: boolean;
  sameSite?: CookieSameSite;
}

export interface CookieSession {
  cookies: {
    set(details: CookieDetails): Promise<void>;
    remove(url: string, name: string): Promise<void>;
  };
}

function authCookieURL(config: RuntimeConfigResult): string | null {
  if (!config.ok) return null;
  try {
    return `${new URL(config.config.apiUrl).origin}/`;
  } catch {
    return null;
  }
}

export async function installDesktopAuthCookie(
  session: CookieSession,
  config: RuntimeConfigResult,
  token: string,
): Promise<boolean> {
  const value = token.trim();
  const url = authCookieURL(config);
  if (!value || !url) return false;

  const secure = url.startsWith("https://");
  await session.cookies.set({
    url,
    name: DESKTOP_AUTH_COOKIE_NAME,
    value,
    path: "/",
    httpOnly: true,
    secure,
    sameSite: secure ? "no_restriction" : "lax",
  });
  return true;
}

export async function clearDesktopAuthCookie(
  session: CookieSession,
  config: RuntimeConfigResult,
): Promise<boolean> {
  const url = authCookieURL(config);
  if (!url) return false;
  await session.cookies.remove(url, DESKTOP_AUTH_COOKIE_NAME);
  return true;
}
