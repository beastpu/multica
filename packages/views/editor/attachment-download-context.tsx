"use client";

import { createContext, use, useMemo, type ReactNode } from "react";
import type { Attachment } from "@multica/core/types";
import { attachmentIdFromDownloadURL } from "@multica/core/types/attachment-url";
import { openExternal } from "../platform";
import { useDownloadAttachment } from "./use-download-attachment";

interface ResolvedDownload {
  // Returns the attachment id for a URL referenced in the markdown, or
  // `undefined` if it's an external link we don't manage.
  resolveAttachmentId: (url: string) => string | undefined;
  // Returns the full Attachment record (content_type, filename, download_url,
  // ...) for a URL referenced in the markdown. NodeView preview triggers use
  // this to decide whether the type is previewable and to feed the modal.
  resolveAttachment: (url: string) => Attachment | undefined;
  // Called by NodeView click handlers. Re-signs through `getAttachment` when
  // the URL maps to a known attachment; falls back to `openExternal` for
  // external URLs so Electron still routes through the IPC bridge instead of
  // letting `window.open` hit the `setWindowOpenHandler` deny path.
  openByUrl: (url: string) => void;
}

const AttachmentDownloadContext = createContext<ResolvedDownload | null>(null);

interface ProviderProps {
  attachments?: Attachment[];
  children: ReactNode;
}

function stripQueryAndFragment(url: string): string {
  return url.split(/[?#]/, 1)[0] ?? "";
}

function comparableUrlParts(raw: string): string | null {
  if (!raw) return null;
  try {
    const url = raw.startsWith("/")
      ? new URL(raw, "https://multica.local")
      : new URL(raw);
    return `${url.pathname}${url.search}`;
  } catch {
    return null;
  }
}

function matchesAttachmentURL(embeddedURL: string, attachmentURL?: string | null): boolean {
  if (!embeddedURL || !attachmentURL) return false;
  if (embeddedURL === attachmentURL) return true;

  const embeddedStable = stripQueryAndFragment(embeddedURL);
  const attachmentStable = stripQueryAndFragment(attachmentURL);
  if (embeddedStable !== "" && embeddedStable === attachmentStable) return true;

  const embeddedParts = comparableUrlParts(embeddedURL);
  const attachmentParts = comparableUrlParts(attachmentURL);
  return !!embeddedParts && embeddedParts === attachmentParts;
}

/**
 * Provides a click-time download handler to Tiptap NodeViews mounted inside
 * `ContentEditor`. Without a provider the consumer falls back to opening the
 * raw URL via `openExternal` — same behaviour as before this hook existed.
 *
 * URL → attachment matching has two fallbacks (in order). New comments
 * (post-MUL-3130) persist the stable `/api/attachments/<id>/download`
 * shape; legacy comments persist whatever was in `att.url` at upload
 * time, including the short-lived `/uploads/<key>?exp&sig` pattern that
 * triggered MUL-3130. The id-from-URL extractor handles new content;
 * URL equivalence covers legacy and S3/CloudFront markdown that never
 * got the new shape.
 */
export function AttachmentDownloadProvider({ attachments, children }: ProviderProps) {
  const download = useDownloadAttachment();
  const value = useMemo<ResolvedDownload>(
    () => {
      const lookup = (url: string): Attachment | undefined => {
        if (!url || !attachments?.length) return undefined;
        const idFromUrl = attachmentIdFromDownloadURL(url);
        if (idFromUrl) {
          const byId = attachments.find((a) => a.id === idFromUrl);
          if (byId) return byId;
        }
        return attachments.find(
          (a) =>
            matchesAttachmentURL(url, a.url) ||
            matchesAttachmentURL(url, a.content_url) ||
            matchesAttachmentURL(url, a.download_url) ||
            matchesAttachmentURL(url, a.markdown_url),
        );
      };
      return {
        resolveAttachmentId: (url) => lookup(url)?.id,
        resolveAttachment: lookup,
        openByUrl: (url) => {
          const att = lookup(url);
          if (att) {
            download(att.id);
            return;
          }
          if (url) openExternal(url);
        },
      };
    },
    [attachments, download],
  );
  return (
    <AttachmentDownloadContext.Provider value={value}>
      {children}
    </AttachmentDownloadContext.Provider>
  );
}

/**
 * Returns the click-time download handler installed by a surrounding
 * `AttachmentDownloadProvider`, or a fallback that just opens the raw URL
 * externally. Used by file-card and image NodeViews so they can stay
 * usable in editor surfaces that haven't been wired up yet.
 */
export function useAttachmentDownloadResolver(): ResolvedDownload {
  const ctx = use(AttachmentDownloadContext);
  // Hooks-must-be-unconditional: always create the fallback object, but
  // memoization is unnecessary here because each NodeView render also
  // re-runs the click handler closure.
  if (ctx) return ctx;
  return {
    resolveAttachmentId: () => undefined,
    resolveAttachment: () => undefined,
    openByUrl: (url) => {
      if (url) openExternal(url);
    },
  };
}
