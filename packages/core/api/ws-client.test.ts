import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { WSClient } from "./ws-client";

// Minimal WebSocket double: records the upgrade URL, outbound frames, and
// every constructed instance so tests can assert both connect-time query
// strings and reconnect behavior (a forced reconnect creates a new instance).
class FakeWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;
  static lastUrl: string | null = null;
  static lastInstance: FakeWebSocket | null = null;
  static instances: FakeWebSocket[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  readyState = 0;
  sent: string[] = [];
  constructor(url: string) {
    FakeWebSocket.lastUrl = url;
    FakeWebSocket.lastInstance = this;
    FakeWebSocket.instances.push(this);
  }
  close() {
    this.readyState = FakeWebSocket.CLOSED;
  }
  send(data: string) {
    this.sent.push(data);
  }
}

// Drives a freshly connect()ed client through token auth so the heartbeat
// starts: onopen sends the auth frame, auth_ack marks it authenticated.
function openAndAuthenticate(): FakeWebSocket {
  const sock = FakeWebSocket.lastInstance!;
  sock.readyState = FakeWebSocket.OPEN;
  sock.onopen?.();
  sock.onmessage?.({ data: '{"type":"auth_ack"}' });
  return sock;
}

describe("WSClient", () => {
  beforeEach(() => {
    FakeWebSocket.lastUrl = null;
    FakeWebSocket.lastInstance = null;
    FakeWebSocket.instances = [];
    vi.stubGlobal("WebSocket", FakeWebSocket as unknown as typeof WebSocket);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("includes client identity in the upgrade URL when configured", () => {
    const ws = new WSClient("ws://example.test/ws", {
      identity: { platform: "desktop", version: "1.2.3", os: "macos" },
    });
    ws.setAuth("tok", "acme");
    ws.connect();

    const url = new URL(FakeWebSocket.lastUrl!);
    expect(url.searchParams.get("workspace_slug")).toBe("acme");
    expect(url.searchParams.get("client_platform")).toBe("desktop");
    expect(url.searchParams.get("client_version")).toBe("1.2.3");
    expect(url.searchParams.get("client_os")).toBe("macos");
    // Token must never appear in the URL — it is delivered as the first
    // WS message in token mode.
    expect(url.searchParams.has("token")).toBe(false);
  });

  it("omits client_* params when identity is not configured", () => {
    const ws = new WSClient("ws://example.test/ws");
    ws.setAuth("tok", "acme");
    ws.connect();

    const url = new URL(FakeWebSocket.lastUrl!);
    expect(url.searchParams.has("client_platform")).toBe(false);
    expect(url.searchParams.has("client_version")).toBe(false);
    expect(url.searchParams.has("client_os")).toBe(false);
  });

  it("only includes the identity fields that are set", () => {
    const ws = new WSClient("ws://example.test/ws", {
      identity: { platform: "cli" },
    });
    ws.setAuth("tok", "acme");
    ws.connect();

    const url = new URL(FakeWebSocket.lastUrl!);
    expect(url.searchParams.get("client_platform")).toBe("cli");
    expect(url.searchParams.has("client_version")).toBe(false);
    expect(url.searchParams.has("client_os")).toBe(false);
  });

  it("truncates the logged payload when an unparseable frame is large", () => {
    const logger = {
      debug: vi.fn(),
      info: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
    };
    const ws = new WSClient("ws://example.test/ws", { logger });
    ws.connect();

    const huge = "x".repeat(5000);
    FakeWebSocket.lastInstance!.onmessage?.({ data: huge });

    expect(logger.warn).toHaveBeenCalledTimes(1);
    const [, summary] = logger.warn.mock.calls[0] as [string, string];
    expect(summary.length).toBeLessThan(huge.length);
    expect(summary).toContain("truncated");
    expect(summary).toContain("5000");
    expect(summary.startsWith("x".repeat(200))).toBe(true);
  });

  it("logs and skips malformed frames without breaking later messages", () => {
    const logger = {
      debug: vi.fn(),
      info: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
    };
    const ws = new WSClient("ws://example.test/ws", { logger });
    const handler = vi.fn();
    ws.on("issue:updated", handler);
    ws.connect();

    expect(() => {
      FakeWebSocket.lastInstance!.onmessage?.({ data: `{"type":"issue` });
    }).not.toThrow();

    FakeWebSocket.lastInstance!.onmessage?.({
      data: JSON.stringify({
        type: "issue:updated",
        payload: { id: "issue-1" },
      }),
    });

    expect(logger.warn).toHaveBeenCalledWith(
      "ws: received unparseable message",
      `{"type":"issue`,
    );
    expect(handler).toHaveBeenCalledWith(
      { id: "issue-1" },
      undefined,
      undefined,
    );
  });

  it("passes actor_id and actor_type to event handlers", () => {
    const ws = new WSClient("ws://example.test/ws");
    ws.setAuth("tok", "acme");
    ws.connect();

    const handler = vi.fn();
    ws.on("issue:created", handler);

    const fakeWs = (ws as any).ws as FakeWebSocket;
    fakeWs.onmessage?.({
      data: JSON.stringify({
        type: "issue:created",
        payload: { id: "issue-1" },
        actor_id: "user-123",
        actor_type: "user",
      }),
    });

    expect(handler).toHaveBeenCalledWith(
      { id: "issue-1" },
      "user-123",
      "user",
    );
  });

  describe("heartbeat", () => {
    it("sends an application-level ping on the heartbeat interval after auth", () => {
      vi.useFakeTimers();
      const ws = new WSClient("ws://example.test/ws");
      ws.setAuth("tok", "acme");
      ws.connect();
      const sock = openAndAuthenticate();

      expect(sock.sent).not.toContain('{"type":"ping"}');
      vi.advanceTimersByTime(30_000);
      expect(sock.sent).toContain('{"type":"ping"}');
    });

    it("force-reconnects when a heartbeat ping gets no reply", () => {
      vi.useFakeTimers();
      const ws = new WSClient("ws://example.test/ws");
      ws.setAuth("tok", "acme");
      ws.connect();
      openAndAuthenticate();
      expect(FakeWebSocket.instances).toHaveLength(1);

      vi.advanceTimersByTime(30_000); // ping sent, reply timer armed
      vi.advanceTimersByTime(10_000); // no traffic -> zombie detected

      expect(FakeWebSocket.instances).toHaveLength(2);
    });

    it("keeps the connection when any frame arrives before the timeout", () => {
      vi.useFakeTimers();
      const ws = new WSClient("ws://example.test/ws");
      ws.setAuth("tok", "acme");
      ws.connect();
      const sock = openAndAuthenticate();

      vi.advanceTimersByTime(30_000);
      sock.onmessage?.({ data: '{"type":"pong"}' });
      vi.advanceTimersByTime(10_000);

      expect(FakeWebSocket.instances).toHaveLength(1);
    });

    it("re-runs reconnect callbacks after a forced reconnect re-authenticates", () => {
      vi.useFakeTimers();
      const ws = new WSClient("ws://example.test/ws");
      ws.setAuth("tok", "acme");
      ws.connect();
      openAndAuthenticate();
      const onReconnect = vi.fn();
      ws.onReconnect(onReconnect);

      vi.advanceTimersByTime(40_000); // zombie detected, new socket dialed
      openAndAuthenticate(); // new instance authenticates

      expect(onReconnect).toHaveBeenCalledTimes(1);
    });

    it("does not dispatch pong frames to handlers", () => {
      const ws = new WSClient("ws://example.test/ws");
      ws.setAuth("tok", "acme");
      ws.connect();
      const sock = openAndAuthenticate();
      const anyHandler = vi.fn();
      ws.onAny(anyHandler);

      sock.onmessage?.({ data: '{"type":"pong"}' });

      expect(anyHandler).not.toHaveBeenCalled();
    });

    it("stops the heartbeat on disconnect", () => {
      vi.useFakeTimers();
      const ws = new WSClient("ws://example.test/ws");
      ws.setAuth("tok", "acme");
      ws.connect();
      openAndAuthenticate();
      ws.disconnect();

      vi.advanceTimersByTime(120_000);

      expect(FakeWebSocket.instances).toHaveLength(1);
    });
  });

  describe("ensureAlive", () => {
    it("probes an OPEN socket immediately and reconnects when nothing answers", () => {
      vi.useFakeTimers();
      const ws = new WSClient("ws://example.test/ws");
      ws.setAuth("tok", "acme");
      ws.connect();
      const sock = openAndAuthenticate();

      ws.ensureAlive();
      expect(sock.sent).toContain('{"type":"ping"}');

      vi.advanceTimersByTime(10_000); // no reply -> dead socket
      expect(FakeWebSocket.instances).toHaveLength(2);
    });

    it("does nothing when the probe is answered", () => {
      vi.useFakeTimers();
      const ws = new WSClient("ws://example.test/ws");
      ws.setAuth("tok", "acme");
      ws.connect();
      const sock = openAndAuthenticate();

      ws.ensureAlive();
      sock.onmessage?.({ data: '{"type":"pong"}' });
      vi.advanceTimersByTime(10_000);

      expect(FakeWebSocket.instances).toHaveLength(1);
    });

    it("reconnects immediately when the socket is already closed", () => {
      const ws = new WSClient("ws://example.test/ws");
      ws.setAuth("tok", "acme");
      ws.connect();
      const sock = openAndAuthenticate();
      sock.readyState = FakeWebSocket.CLOSED;

      ws.ensureAlive();

      expect(FakeWebSocket.instances).toHaveLength(2);
    });

    it("fires a pending reconnect timer immediately instead of waiting", () => {
      vi.useFakeTimers();
      const ws = new WSClient("ws://example.test/ws");
      ws.setAuth("tok", "acme");
      ws.connect();
      const sock = openAndAuthenticate();
      sock.onclose?.(); // schedules reconnect in 3s
      expect(FakeWebSocket.instances).toHaveLength(1);

      ws.ensureAlive();

      expect(FakeWebSocket.instances).toHaveLength(2);
    });
  });
});
