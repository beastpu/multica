"use client"

import { useRef, useState, useEffect, useCallback } from "react"
import type QrScannerLib from "qr-scanner"

export interface UseQrScannerOptions {
  onScan: (data: string) => void
  onError?: (error: string) => void
  enabled?: boolean
}

export interface UseQrScannerResult {
  videoRef: React.RefObject<HTMLVideoElement | null>
  isScanning: boolean
  error: string | null
  hasCamera: boolean
}

/**
 * Hook wrapping qr-scanner lifecycle.
 *
 * - Dynamically imports qr-scanner (keeps it out of SSR bundles)
 * - Creates/destroys scanner instance based on `enabled`
 * - Releases camera stream on cleanup
 */
export function useQrScanner({
  onScan,
  onError,
  enabled = true,
}: UseQrScannerOptions): UseQrScannerResult {
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const scannerRef = useRef<QrScannerLib | null>(null)
  const [isScanning, setIsScanning] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [hasCamera, setHasCamera] = useState(true)

  // Stable callback refs to avoid re-creating scanner on every render
  const onScanRef = useRef(onScan)
  onScanRef.current = onScan
  const onErrorRef = useRef(onError)
  onErrorRef.current = onError

  // Check camera availability once
  useEffect(() => {
    let cancelled = false
    import("qr-scanner").then((mod) => {
      const QrScanner = mod.default
      QrScanner.hasCamera().then((has) => {
        if (!cancelled) setHasCamera(has)
      })
    })
    return () => { cancelled = true }
  }, [])

  // Start/stop scanner based on `enabled` and video element
  useEffect(() => {
    if (!enabled || !videoRef.current || !hasCamera) return

    let destroyed = false
    const video = videoRef.current

    import("qr-scanner").then((mod) => {
      if (destroyed) return
      const QrScanner = mod.default

      const scanner = new QrScanner(
        video,
        (result) => {
          console.log("[QrScanner] scanned:", result.data)
          onScanRef.current(result.data)
        },
        {
          preferredCamera: "environment",
          maxScansPerSecond: 5,
          returnDetailedScanResult: true,
          highlightScanRegion: false,
          highlightCodeOutline: false,
          onDecodeError: (err) => {
            // "No QR code found" fires every frame — ignore it
            if (typeof err === "string" && err.includes("No QR code found")) return
            console.warn("[QrScanner] decode error:", err)
          },
        },
      )

      scannerRef.current = scanner

      scanner
        .start()
        .then(() => {
          if (!destroyed) {
            console.log("[QrScanner] started successfully")
            setIsScanning(true)
            setError(null)
          }
        })
        .catch((err: Error) => {
          if (destroyed) return
          const msg = err.message || "Camera access failed"
          setError(msg)
          setIsScanning(false)
          onErrorRef.current?.(msg)
        })
    })

    return () => {
      destroyed = true
      if (scannerRef.current) {
        scannerRef.current.stop()
        scannerRef.current.destroy()
        scannerRef.current = null
      }
      setIsScanning(false)
    }
  }, [enabled, hasCamera])

  return { videoRef, isScanning, error, hasCamera }
}
