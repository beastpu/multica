import { useEffect } from "react";
import { useWS } from "@multica/core/realtime";

/**
 * Probe WS liveness when the main process reports an OS resume-from-sleep
 * or session unlock. After sleep the renderer's WebSocket is usually a dead
 * TCP link that still reports OPEN — without an active probe it never fires
 * onclose, so no reconnect happens, no invalidation runs, and every
 * staleTime-Infinity query serves stale data until app restart.
 *
 * Must be called inside the CoreProvider tree (needs WSProvider context).
 */
export function usePowerResumeWSProbe() {
  const { ensureAlive } = useWS();
  useEffect(() => {
    return window.desktopAPI.onPowerResume(ensureAlive);
  }, [ensureAlive]);
}
