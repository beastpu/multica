import type { WSMessage, WSEventType } from "../types/events";
import { type Logger, noopLogger } from "../logger";

type EventHandler = (payload: unknown, actorId?: string, actorType?: string) => void;

// Cap how much of an unparseable frame we put into the log. A malformed or
// rogue server can stream arbitrarily large garbage, and the warn handler may
// be a console / IPC bridge whose buffers we don't want to blow.
const UNPARSEABLE_LOG_MAX_CHARS = 200;

// Application-level heartbeat. The server replies to `{"type":"ping"}` with
// `{"type":"pong"}` (realtime/hub.go handleFrame). This is the client's only
// liveness signal: the server's protocol-level pings are answered by the
// browser below the JS layer and are invisible here, and this client never
// writes after auth — so a silently dead TCP link (laptop sleep/wake, Wi-Fi
// switch, NAT/proxy idle timeout) never fires `onclose` and the connection
// becomes a zombie that the staleTime-Infinity query cache waits on forever.
const HEARTBEAT_INTERVAL_MS = 30_000;
const HEARTBEAT_REPLY_TIMEOUT_MS = 10_000;

function summarizeUnparseable(data: unknown): string {
  const text = typeof data === "string" ? data : String(data);
  if (text.length <= UNPARSEABLE_LOG_MAX_CHARS) return text;
  return `${text.slice(0, UNPARSEABLE_LOG_MAX_CHARS)}… (truncated, ${text.length} chars total)`;
}

/** Identifies the WS client to the server. Sent as `client_platform`,
 *  `client_version`, and `client_os` query parameters on the upgrade URL —
 *  browsers cannot set custom headers on WebSocket handshakes, so query
 *  params are the only portable channel. */
export interface WSClientIdentity {
  platform?: string;
  version?: string;
  os?: string;
}

export class WSClient {
  private ws: WebSocket | null = null;
  private baseUrl: string;
  private token: string | null = null;
  private workspaceSlug: string | null = null;
  private cookieAuth = false;
  private identity: WSClientIdentity | undefined;
  private handlers = new Map<WSEventType, Set<EventHandler>>();
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null;
  private heartbeatReplyTimer: ReturnType<typeof setTimeout> | null = null;
  private hasConnectedBefore = false;
  private onReconnectCallbacks = new Set<() => void>();
  private anyHandlers = new Set<(msg: WSMessage) => void>();
  private logger: Logger;

  constructor(
    url: string,
    options?: {
      logger?: Logger;
      cookieAuth?: boolean;
      identity?: WSClientIdentity;
    },
  ) {
    this.baseUrl = url;
    this.logger = options?.logger ?? noopLogger;
    this.cookieAuth = options?.cookieAuth ?? false;
    this.identity = options?.identity;
  }

  setAuth(token: string | null, workspaceSlug: string) {
    this.token = token;
    this.workspaceSlug = workspaceSlug;
  }

  connect() {
    const url = new URL(this.baseUrl);
    // Token is never sent as a URL query parameter — it would be logged by
    // proxies, CDNs, and browser history.  In cookie mode the HttpOnly cookie
    // is sent automatically with the upgrade request.  In token mode the token
    // is delivered as the first WebSocket message after the connection opens.
    if (this.workspaceSlug)
      url.searchParams.set("workspace_slug", this.workspaceSlug);
    if (this.identity?.platform)
      url.searchParams.set("client_platform", this.identity.platform);
    if (this.identity?.version)
      url.searchParams.set("client_version", this.identity.version);
    if (this.identity?.os)
      url.searchParams.set("client_os", this.identity.os);

    this.ws = new WebSocket(url.toString());

    this.ws.onopen = () => {
      if (!this.cookieAuth && this.token) {
        this.ws!.send(
          JSON.stringify({ type: "auth", payload: { token: this.token } }),
        );
        return;
      }

      this.onAuthenticated();
    };

    this.ws.onmessage = (event) => {
      // Any inbound frame proves the link is alive — cancel a pending
      // heartbeat-reply deadline before even parsing.
      this.clearHeartbeatReplyTimer();
      let msg: WSMessage;
      try {
        msg = JSON.parse(event.data as string) as WSMessage;
      } catch {
        this.logger.warn(
          "ws: received unparseable message",
          summarizeUnparseable(event.data),
        );
        return;
      }
      if ((msg as any).type === "auth_ack") {
        this.onAuthenticated();
        return;
      }
      if ((msg as any).type === "pong") {
        // Heartbeat reply — liveness already recorded above, not an app event.
        return;
      }
      this.logger.debug("received", msg.type);
      const eventHandlers = this.handlers.get(msg.type);
      if (eventHandlers) {
        for (const handler of eventHandlers) {
          handler(msg.payload, msg.actor_id, msg.actor_type);
        }
      }
      for (const handler of this.anyHandlers) {
        handler(msg);
      }
    };

    this.ws.onclose = () => {
      this.stopHeartbeat();
      this.logger.warn("disconnected, reconnecting in 3s");
      this.reconnectTimer = setTimeout(() => this.connect(), 3000);
    };

    this.ws.onerror = () => {
      // Suppress — onclose handles reconnect; errors during StrictMode
      // double-fire are expected in dev and harmless.
    };
  }

  private onAuthenticated() {
    this.logger.info("connected");
    this.startHeartbeat();
    if (this.hasConnectedBefore) {
      for (const cb of this.onReconnectCallbacks) {
        try {
          cb();
        } catch {
          // ignore reconnect callback errors
        }
      }
    }
    this.hasConnectedBefore = true;
  }

  private startHeartbeat() {
    this.stopHeartbeat();
    this.heartbeatTimer = setInterval(
      () => this.sendHeartbeatPing(),
      HEARTBEAT_INTERVAL_MS,
    );
  }

  private stopHeartbeat() {
    if (this.heartbeatTimer) {
      clearInterval(this.heartbeatTimer);
      this.heartbeatTimer = null;
    }
    this.clearHeartbeatReplyTimer();
  }

  private clearHeartbeatReplyTimer() {
    if (this.heartbeatReplyTimer) {
      clearTimeout(this.heartbeatReplyTimer);
      this.heartbeatReplyTimer = null;
    }
  }

  private sendHeartbeatPing() {
    if (this.ws?.readyState !== WebSocket.OPEN) return;
    this.ws.send(JSON.stringify({ type: "ping" }));
    // Keep an earlier deadline if one is already armed — extending it on
    // every ping would let a zombie socket postpone detection indefinitely.
    if (this.heartbeatReplyTimer) return;
    this.heartbeatReplyTimer = setTimeout(() => {
      this.heartbeatReplyTimer = null;
      this.logger.warn("ws: heartbeat timed out, forcing reconnect");
      this.forceReconnect();
    }, HEARTBEAT_REPLY_TIMEOUT_MS);
  }

  /**
   * Tear down the current socket and dial a new one immediately. Used when
   * the socket is known (or suspected) dead but `onclose` never fired — a
   * zombie connection cannot be recovered any other way, since this client
   * otherwise only reconnects from the `onclose` handler.
   */
  private forceReconnect() {
    this.stopHeartbeat();
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (this.ws) {
      // Detach handlers first: close() on a half-dead socket may still fire
      // onclose later, which would schedule a competing reconnect.
      this.ws.onopen = null;
      this.ws.onmessage = null;
      this.ws.onclose = null;
      this.ws.onerror = null;
      this.ws.close();
      this.ws = null;
    }
    this.connect();
  }

  /**
   * Probe liveness now instead of waiting for the next heartbeat tick.
   * Called on wake signals (tab became visible, network came back online,
   * OS resumed from sleep) — exactly the moments a socket that still reports
   * OPEN is most likely to be dead. A live connection answers the probe and
   * nothing else happens; a dead one is replaced within the reply timeout.
   */
  ensureAlive() {
    if (this.reconnectTimer) {
      // A reconnect is already scheduled — fire it now instead of in 3s.
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
      this.connect();
      return;
    }
    // Never connected or explicitly disconnected — lifecycle owner decides.
    if (!this.ws) return;
    if (this.ws.readyState === WebSocket.OPEN) {
      this.sendHeartbeatPing();
      return;
    }
    if (
      this.ws.readyState === WebSocket.CLOSING ||
      this.ws.readyState === WebSocket.CLOSED
    ) {
      this.forceReconnect();
    }
    // CONNECTING: a dial is already in flight; let it resolve.
  }

  disconnect() {
    this.stopHeartbeat();
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (this.ws) {
      // Remove handlers before close to prevent onclose from scheduling a reconnect
      this.ws.onclose = null;
      this.ws.onerror = null;
      this.ws.close();
      this.ws = null;
    }
    this.hasConnectedBefore = false;
    this.handlers.clear();
    this.anyHandlers.clear();
    this.onReconnectCallbacks.clear();
  }

  on(event: WSEventType, handler: EventHandler) {
    if (!this.handlers.has(event)) {
      this.handlers.set(event, new Set());
    }
    this.handlers.get(event)!.add(handler);
    return () => {
      this.handlers.get(event)?.delete(handler);
    };
  }

  onAny(handler: (msg: WSMessage) => void) {
    this.anyHandlers.add(handler);
    return () => {
      this.anyHandlers.delete(handler);
    };
  }

  onReconnect(callback: () => void) {
    this.onReconnectCallbacks.add(callback);
    return () => {
      this.onReconnectCallbacks.delete(callback);
    };
  }

  send(message: WSMessage) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(message));
    }
  }
}
