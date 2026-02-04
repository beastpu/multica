"use client";

import { useState, useEffect, useCallback, lazy, Suspense, useRef } from "react";
import { Button } from "@multica/ui/components/ui/button";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { toast } from "@multica/ui/components/ui/sonner";
import {
  useConnectionStore,
  parseConnectionCode,
  saveConnection,
} from "@multica/store";
import { HugeiconsIcon } from "@hugeicons/react";
import { Camera01Icon, TextIcon } from "@hugeicons/core-free-icons";

const LazyQrScannerView = lazy(() =>
  import("@multica/ui/components/qr-scanner-view").then((m) => ({
    default: m.QrScannerView,
  })),
);

type Mode = "scan" | "paste";

export function ConnectPrompt() {
  const gwState = useConnectionStore((s) => s.connectionState);
  const [codeInput, setCodeInput] = useState("");
  const [mode, setMode] = useState<Mode>("paste"); // SSR-safe default
  const [canScan, setCanScan] = useState(false);
  const scannedRef = useRef(false);

  // Detect mobile + camera capability, auto-switch to scan mode
  useEffect(() => {
    const isTouchDevice =
      "ontouchstart" in window || navigator.maxTouchPoints > 0;
    const isNarrow = window.innerWidth < 768;
    const hasGetUserMedia = !!navigator.mediaDevices?.getUserMedia;

    if (hasGetUserMedia) {
      setCanScan(true);
      if (isTouchDevice && isNarrow) {
        setMode("scan");
      }
    }
  }, []);

  // Handle paste-mode connect
  const handleConnect = useCallback(() => {
    const trimmed = codeInput.trim();
    if (!trimmed) return;
    try {
      const info = parseConnectionCode(trimmed);
      saveConnection(info);
      useConnectionStore.getState().connect(info);
      setCodeInput("");
    } catch (e) {
      toast.error((e as Error).message);
    }
  }, [codeInput]);

  // Handle QR scan result — auto-connect, no button needed
  const handleQrScan = useCallback((data: string) => {
    // Prevent duplicate connects from rapid successive scans
    if (scannedRef.current) return;
    scannedRef.current = true;

    try {
      const info = parseConnectionCode(data);
      saveConnection(info);
      useConnectionStore.getState().connect(info);
    } catch (e) {
      toast.error((e as Error).message);
      // Allow re-scan on error (invalid/expired code)
      scannedRef.current = false;
    }
  }, []);

  const handleScanError = useCallback((msg: string) => {
    toast.error(msg);
    setMode("paste");
  }, []);

  const isConnecting = gwState === "connecting" || gwState === "connected";

  return (
    <div className="flex flex-col items-center justify-center h-full gap-4 px-4">
      <div className="text-center space-y-1">
        <p className="text-sm text-muted-foreground">
          {mode === "scan"
            ? "Scan QR code to connect"
            : "Paste a connection code to start"}
        </p>
        {isConnecting && (
          <p className="text-xs text-muted-foreground/60 animate-pulse">
            Connecting...
          </p>
        )}
      </div>

      {/* Mode toggle — only show if camera is available */}
      {canScan && (
        <div className="flex gap-1 bg-muted rounded-lg p-1">
          <Button
            variant={mode === "scan" ? "default" : "ghost"}
            size="sm"
            className="text-xs gap-1.5 h-7 px-3"
            onClick={() => {
              scannedRef.current = false;
              setMode("scan");
            }}
          >
            <HugeiconsIcon icon={Camera01Icon} className="size-3.5" />
            Scan
          </Button>
          <Button
            variant={mode === "paste" ? "default" : "ghost"}
            size="sm"
            className="text-xs gap-1.5 h-7 px-3"
            onClick={() => setMode("paste")}
          >
            <HugeiconsIcon icon={TextIcon} className="size-3.5" />
            Paste
          </Button>
        </div>
      )}

      {/* Content */}
      <div className="w-full max-w-sm space-y-3">
        {mode === "scan" ? (
          <Suspense
            fallback={
              <div className="h-[280px] animate-pulse bg-muted rounded-xl" />
            }
          >
            <LazyQrScannerView
              onScan={handleQrScan}
              onError={handleScanError}
            />
          </Suspense>
        ) : (
          <>
            <Textarea
              value={codeInput}
              onChange={(e) => setCodeInput(e.target.value)}
              placeholder="Paste connection code here..."
              className="text-xs font-mono min-h-[100px] resize-none"
              onKeyDown={(e) => {
                if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                  e.preventDefault();
                  handleConnect();
                }
              }}
            />
            <Button
              size="sm"
              onClick={handleConnect}
              disabled={!codeInput.trim() || gwState === "connecting"}
              className="w-full text-xs"
            >
              Connect
            </Button>
          </>
        )}
      </div>
    </div>
  );
}
