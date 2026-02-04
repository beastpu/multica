"use client"

import { useQrScanner } from "@multica/ui/hooks/use-qr-scanner"

interface QrScannerViewProps {
  onScan: (data: string) => void
  onError?: (error: string) => void
}

/**
 * Camera viewfinder for QR code scanning.
 *
 * Renders a live camera feed with a decorative scan frame overlay.
 * Uses getUserMedia via the qr-scanner library (WebWorker-based decoding).
 * iOS requires playsinline + muted + autoplay on the <video> element.
 */
export function QrScannerView({ onScan, onError }: QrScannerViewProps) {
  const { videoRef, isScanning, error, hasCamera } = useQrScanner({
    onScan,
    onError,
    enabled: true,
  })

  if (!hasCamera) {
    return (
      <div className="flex items-center justify-center h-[280px] rounded-xl bg-muted">
        <p className="text-sm text-muted-foreground">No camera available</p>
      </div>
    )
  }

  if (error) {
    return (
      <div className="flex flex-col items-center justify-center h-[280px] rounded-xl bg-muted gap-2">
        <p className="text-sm text-muted-foreground">Camera access denied</p>
        <p className="text-xs text-muted-foreground/60">
          Switch to paste mode below
        </p>
      </div>
    )
  }

  return (
    <div className="relative w-full max-w-[280px] mx-auto">
      {/* Camera feed */}
      <div className="relative aspect-square rounded-xl overflow-hidden bg-black">
        <video
          ref={videoRef}
          autoPlay
          playsInline
          muted
          className="absolute inset-0 w-full h-full object-cover"
        />

        {/* Scan frame overlay */}
        <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
          <div className="relative w-3/4 h-3/4">
            {/* Corner accents */}
            <div className="absolute -top-1 -left-1 w-5 h-5 border-t-2 border-l-2 border-white/70 rounded-tl-md" />
            <div className="absolute -top-1 -right-1 w-5 h-5 border-t-2 border-r-2 border-white/70 rounded-tr-md" />
            <div className="absolute -bottom-1 -left-1 w-5 h-5 border-b-2 border-l-2 border-white/70 rounded-bl-md" />
            <div className="absolute -bottom-1 -right-1 w-5 h-5 border-b-2 border-r-2 border-white/70 rounded-br-md" />
          </div>
        </div>

        {/* Loading state */}
        {!isScanning && !error && (
          <div className="absolute inset-0 flex items-center justify-center bg-black/60">
            <p className="text-xs text-white/80 animate-pulse">
              Starting camera...
            </p>
          </div>
        )}
      </div>

      {/* Hint text */}
      <p className="text-xs text-muted-foreground text-center mt-3">
        Point camera at QR code on desktop
      </p>
    </div>
  )
}
