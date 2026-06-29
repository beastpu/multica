"use client";

/**
 * Attachment — single unified renderer for every attachment surface.
 *
 * Takes one attachment-shaped input (a full record, a URL-only reference, or
 * an in-flight upload) and dispatches by PreviewKind:
 *
 *   - image  → ImageAttachmentView (figure + hover toolbar + lightbox via
 *              the shared AttachmentPreviewModal)
 *   - html   → HtmlAttachmentPreview (inline iframe + hover toolbar)
 *   - others → AttachmentCard (icon + filename + Eye/Download row)
 *
 * Call sites:
 *   - extensions/file-card.tsx FileCardView (Tiptap NodeView)
 *   - extensions/image-view.tsx ImageView (Tiptap NodeView)
 *   - readonly-content.tsx (markdown img + fileCard div renderers)
 *   - issues/components/comment-card.tsx AttachmentList (standalone fallback)
 *   - common/markdown.tsx (chat / skill viewer Markdown wrapper)
 *
 * The component owns its own preview modal and download dispatcher — callers
 * just pass `attachment` and (for editor surfaces) optional editor chrome
 * hints (selected, editable, onDelete).
 */

import {
  Download,
  Link as LinkIcon,
  Maximize2,
  Trash2,
} from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { cn } from "@multica/ui/lib/utils";
import { copyText } from "@multica/ui/lib/clipboard";
import { useQuery } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useConfigStore } from "@multica/core/config";
import type { Attachment as AttachmentRecord } from "@multica/core/types";
import { attachmentIdFromDownloadURL } from "@multica/core/types/attachment-url";
import { useT } from "../i18n";
import { useAttachmentDownloadResolver } from "./attachment-download-context";
import { useAttachmentPreview } from "./attachment-preview-modal";
import { useDownloadAttachment } from "./use-download-attachment";
import { AttachmentCard } from "./attachment-card";
import { HtmlAttachmentPreview } from "./html-attachment-preview";
import { getPreviewKind, type PreviewKind } from "./utils/preview";
import "./styles/attachment.css";

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

export type AttachmentInput =
  // Server response in hand — full record. Used by AttachmentList and any
  // caller iterating a server-returned attachments[] array.
  | { kind: "record"; attachment: AttachmentRecord }
  // Markdown / Tiptap inline: only a URL + filename. Resolves to a full
  // record via the surrounding AttachmentDownloadProvider when available;
  // otherwise renders in URL-only mode (media types still preview from URL,
  // text types fall back to a download CTA).
  | {
      kind: "url";
      url: string;
      filename: string;
      contentType?: string;
      /** Editor in-flight state. Renders a loader placeholder. */
      uploading?: boolean;
      /**
       * Intrinsic pixel dimensions. Rendered as `<img width height>` so the
       * browser reserves the box before the image decodes — prevents the
       * layout shift that would otherwise push the caret out of view on paste.
       */
      width?: number;
      height?: number;
      /**
       * Structural hint from the call site: "this slot is definitionally an
       * image / file / ...". Bypasses `getPreviewKind` autodetect, which
       * needs a filename or content-type and falls back to the file-card
       * chrome when neither is available. Required for callers that KNOW
       * the kind from context (markdown `![]()` is always an image; Tiptap
       * image NodeView is always an image) but receive only a URL with an
       * empty `alt`/`filename`.
       */
      forceKind?: PreviewKind;
    };

export interface AttachmentProps {
  attachment: AttachmentInput;
  /** Editor hint — when true, the image toolbar exposes Trash. */
  editable?: boolean;
  /** Editor hint — applies the "selected" visual to the image figure. */
  selected?: boolean;
  /** Editor hint — wired to Tiptap deleteNode(). */
  onDelete?: () => void;
  className?: string;
}

interface Normalized {
  filename: string;
  contentType: string;
  url: string;
  attachmentId?: string;
  record?: AttachmentRecord;
  uploading: boolean;
  width?: number;
  height?: number;
}

function normalize(
  input: AttachmentInput,
  resolve: (url: string) => AttachmentRecord | undefined,
  cdnDomain: string,
  cdnSigned: boolean,
): Normalized {
  if (input.kind === "record") {
    return {
      filename: input.attachment.filename,
      contentType: input.attachment.content_type,
      url: absolutizeMediaURL(
        pickInlineMediaURL(input.attachment, input.attachment.url, cdnDomain, cdnSigned),
      ),
      attachmentId: input.attachment.id,
      record: input.attachment,
      uploading: false,
    };
  }
  const record = input.url ? resolve(input.url) : undefined;
  return {
    filename: input.filename || record?.filename || "",
    contentType: input.contentType || record?.content_type || "",
    // When the markdown URL resolved to an attachment record, swap to
    // the record's freshly-loadable URL. The persisted markdown URL
    // (`/api/attachments/<id>/download` for new content; raw stored URL
    // for legacy) is correct as a stable reference but doesn't
    // necessarily load as a native <img>/<video> resource for every
    // client — token-mode clients can't attach an Authorization header
    // to bare /api/* fetches, and a CloudFront-signed `download_url`
    // is the only working media src in that mode. `pickInlineMediaURL`
    // picks the URL with embedded credentials when one exists and
    // falls back to the input URL otherwise so legacy / unresolved
    // markdown stays on its existing path. See MUL-3130 review.
    //
    // After picking the credential-bearing URL we run the absolutize
    // pass so a site-relative `/api/attachments/...` or `/uploads/...`
    // path becomes a proper origin-bearing URL when the renderer's
    // document origin doesn't proxy /api or /uploads to the API host
    // (Electron desktop, mobile webview). Web with a same-origin
    // proxy keeps `apiBaseUrl=""` and the helper is a no-op there.
    // See MUL-3192 — quick-create modal regressed because the freshly-
    // uploaded image URL stayed site-relative and Electron's renderer
    // origin (file://) couldn't load it.
    url: absolutizeMediaURL(
      record ? pickInlineMediaURL(record, input.url, cdnDomain, cdnSigned) : input.url,
    ),
    attachmentId: record?.id,
    record,
    uploading: !!input.uploading,
    width: input.width,
    height: input.height,
  };
}

// absolutizeMediaURL is the legacy-compat fallback for old markdown bodies
// that persisted a site-relative `/api/attachments/<id>/download` or
// `/uploads/<key>` URL.
//
// The current (post-MUL-3192) write path persists an absolute URL chosen
// server-side by `buildMarkdownURL` (see server/internal/handler/file.go),
// so new content already loads natively on every client. This helper only
// matters for content written BEFORE MUL-3192 — those bodies still carry
// the old relative shape, and rendering them on a surface whose document
// origin is NOT the API host (Electron desktop, mobile webview) needs the
// API base URL pinned in at render time.
//
// On web, `api.getBaseUrl()` is empty (the Next.js rewrite proxies /api/*
// to the API host server-side), so this is a no-op there.
//
// http(s)://, blob:, and data: URLs are passed through unchanged — they
// already carry their own origin.
function absolutizeMediaURL(rawUrl: string): string {
  if (!rawUrl) return rawUrl;
  if (/^https?:\/\//i.test(rawUrl)) return rawUrl;
  if (/^blob:/i.test(rawUrl) || /^data:/i.test(rawUrl)) return rawUrl;
  if (!rawUrl.startsWith("/")) return rawUrl;
  // The api singleton is a Proxy that returns `undefined` for any property
  // access before `setApiInstance()` runs (boot ordering, SSR). Optional
  // chaining lets us cope with that without throwing — pre-init renders
  // simply keep the site-relative path.
  const baseUrl = (api.getBaseUrl?.() ?? "").replace(/\/+$/, "");
  if (!baseUrl) return rawUrl;
  return `${baseUrl}${rawUrl}`;
}

// pickInlineMediaURL returns the URL most likely to load successfully
// inside a native <img>/<video>/<iframe> resource fetch — i.e. without
// the calling client attaching an Authorization header.
//
// The metadata response carries four URL fields per attachment row,
// each with a different lifetime / accessibility:
//
//   - `record.download_url` — this-response click-time URL. In
//                             CloudFront-signed mode this is the
//                             signed redirect (works as a native img
//                             src for the duration of the TTL); in
//                             other modes it's the bare API endpoint
//                             (`/api/attachments/<id>/download`) which
//                             requires per-request auth and does NOT
//                             load as a native img on a non-same-site
//                             origin like Desktop's file://.
//   - `record.content_url`  — stable same-origin inline proxy endpoint for
//                             private-bucket native previews. It requires the
//                             user's browser session, so it is useful for
//                             rendering but should not be preferred as the
//                             durable markdown URL.
//   - `record.markdown_url` — the durable URL the server picked for
//                             persistence (MUL-3192 / `buildMarkdownURL`):
//                             public CDN passthrough when the storage is
//                             public-readable, or `MULTICA_PUBLIC_URL +
//                             /api/attachments/<id>/download` for
//                             private-bucket modes. Aligned with the
//                             server-side policy by construction, so it
//                             beats `record.url` whenever both exist.
//   - `record.url`          — raw storage URL. May be private (S3 /
//                             CloudFront-signed, R2, MinIO) and unable
//                             to load directly. Last-resort fallback
//                             for legacy responses that omit
//                             `markdown_url`.
//
// Order:
//
//  1. Signed `download_url` — when CloudFront has minted a signed
//     redirect for THIS response, use it; the TTL means the signed URL
//     beats `markdown_url` on first paint (no extra hop through the
//     API endpoint), and the renderer doesn't persist it so the TTL is
//     not a problem.
//  2. Confirmed-public `record.url` — when `/api/config` exposes the same
//     CDN host AND the server-chosen durable `markdown_url` is the raw storage
//     URL too, the browser can load the object directly. `cdnDomain` alone is
//     not enough: proxy/private-bucket deployments can expose a bucket host
//     for legacy file-card detection even though direct object loads 403.
//     Skipped when the server reports `cdn_signed` — in CloudFront signed-URL
//     mode the same domain serves PRIVATE content and a raw (unsigned) storage
//     URL is a guaranteed 403 (MUL-3254).
//  3. LocalStorage signed `record.url` — when it carries `?exp&sig`, that
//     query is the auth and can load natively.
//  4. `record.content_url` — private-bucket inline proxy used by the default
//     proxy/presign mode.
//  5. `record.markdown_url` — the durable, server-policy-aligned URL.
//     Beats raw `record.url` because it never points at a private
//     bucket (must-fix 2 from MUL-3192 review).
//  6. `record.url` — legacy fallback for responses that omit
//     `markdown_url` (a backend old enough to predate MUL-3192).
//  7. The input URL — when there's no record at all.
function pickInlineMediaURL(
  record: AttachmentRecord,
  fallback: string,
  cdnDomain: string,
  cdnSigned: boolean,
): string {
  const dl = record.download_url ?? "";
  if (
    /^https?:\/\//i.test(dl) &&
    /[?&](Signature|X-Amz-Signature|Key-Pair-Id|Expires|X-Amz-Expires)=/i.test(dl)
  ) {
    return dl;
  }
  if (record.url && /[?&](exp|sig)=/i.test(record.url)) return record.url;
  if (
    !cdnSigned &&
    storageURLMatchesCdnDomain(record.url, cdnDomain) &&
    markdownURLConfirmsRawStorageURL(record.markdown_url, record.url)
  ) {
    return record.url;
  }
  if (record.content_url) return record.content_url;
  if (record.markdown_url) return record.markdown_url;
  if (record.url) return record.url;
  return fallback;
}

function storageURLMatchesCdnDomain(rawURL: string, cdnDomain: string): boolean {
  const expected = normalizeHost(cdnDomain);
  if (!rawURL || !expected) return false;
  try {
    const u = new URL(rawURL);
    if (u.protocol !== "http:" && u.protocol !== "https:") return false;
    if (normalizeHost(u.hostname) !== expected) return false;
    return !hasExpiringSignatureQuery(u.searchParams);
  } catch {
    return false;
  }
}

function markdownURLConfirmsRawStorageURL(
  markdownURL: string | null | undefined,
  rawStorageURL: string,
): boolean {
  if (!rawStorageURL) return false;
  // Legacy servers predate `markdown_url`; preserve their previous direct-CDN
  // behavior when the operator exposed a cdnDomain.
  if (!markdownURL) return true;
  return markdownURL === rawStorageURL;
}

function normalizeHost(host: string): string {
  return host.trim().toLowerCase().replace(/\.$/, "");
}

function hasExpiringSignatureQuery(q: URLSearchParams): boolean {
  for (const key of [
    "Signature",
    "X-Amz-Signature",
    "Key-Pair-Id",
    "Expires",
    "X-Amz-Expires",
  ]) {
    if (q.has(key)) return true;
  }
  return false;
}

// ---------------------------------------------------------------------------
// Inline media re-sign (MUL-3254)
// ---------------------------------------------------------------------------

// Keep refetches well inside the server's signed-URL TTL (30 min default,
// server/internal/handler/file.go) so a re-render never serves an expired
// signature from the query cache.
const RESIGN_STALE_MS = 20 * 60 * 1000;

// useResignedInlineMediaURL upgrades an auth-gated media URL to a freshly
// signed one for clients that cannot load `/api/attachments/<id>/download`
// natively.
//
// The picked inline URL can end up being the stable per-attachment API
// endpoint (e.g. a reopened issue draft, whose persisted record deliberately
// strips the short-lived signed `download_url`). That endpoint needs
// credentials: web loads it because the session cookie rides on the <img>
// request (same-site), but Desktop's file:// renderer and the mobile webview
// are cross-site — no cookie is attached and the Bearer token cannot be put
// on a native resource fetch, so the image 401s. Those clients are exactly
// the ones with a non-empty `api.getBaseUrl()` (no same-origin /api proxy),
// which is the existing platform signal `absolutizeMediaURL` keys off.
//
// For them, fetch fresh attachment metadata through the authenticated API —
// the same re-sign the click-time download path already does — and swap in
// the response's signed `download_url`. When the server has no signed URL to
// offer (non-CloudFront deployments return the API path again), keep the
// original URL rather than looping.
function useResignedInlineMediaURL(
  attachmentId: string | undefined,
  pickedUrl: string,
): { url: string; allowBlobFallback: boolean } {
  const idFromPickedUrl = attachmentIdFromDownloadURL(pickedUrl);
  const resignAttachmentId = attachmentId ?? idFromPickedUrl;
  const needsResign =
    !!resignAttachmentId &&
    !!pickedUrl &&
    idFromPickedUrl !== undefined &&
    (api.getBaseUrl?.() ?? "") !== "";

  const { data: fresh, isError, isSuccess } = useQuery({
    queryKey: ["attachment-inline-resign", resignAttachmentId],
    queryFn: () => api.getAttachment(resignAttachmentId as string),
    enabled: needsResign,
    staleTime: RESIGN_STALE_MS,
    gcTime: RESIGN_STALE_MS,
  });

  if (!needsResign) return { url: pickedUrl, allowBlobFallback: true };
  const dl = fresh?.download_url ?? "";
  // Accept the fresh URL only when it is an actual upgrade: an absolute signed
  // storage URL the renderer can load natively. Otherwise leave the API-shaped
  // URL in place so token-mode clients can blob-load it below.
  if (/^https?:\/\//i.test(dl) && attachmentIdFromDownloadURL(dl) === undefined) {
    return { url: dl, allowBlobFallback: false };
  }
  return { url: pickedUrl, allowBlobFallback: isSuccess || isError };
}

// Feishu Project plugin embeds Multica with `#shipToken=...` and authenticates
// API calls via Authorization: Bearer. Native <img> loads cannot attach that
// header, so API-shaped attachment URLs need a client-side authenticated fetch
// and a temporary blob: URL.
//
// Do not use this as a generic Desktop/web fallback. Those clients have working
// native resource auth paths: web has same-site cookies, and desktop installs
// an auth cookie into Electron's session. Pulling them through blob fetches
// bypasses those paths and has regressed normal image rendering before.
const EMBEDDED_SESSION_TOKEN_KEY = "shipToken";

function hasEmbeddedSessionToken(): boolean {
  if (typeof window === "undefined") return false;
  try {
    if (window.sessionStorage.getItem(EMBEDDED_SESSION_TOKEN_KEY)) return true;
  } catch {
    // Ignore unavailable sessionStorage.
  }
  const hash = window.location.hash.startsWith("#")
    ? window.location.hash.slice(1)
    : "";
  return hash ? new URLSearchParams(hash).has(EMBEDDED_SESSION_TOKEN_KEY) : false;
}

const ATTACHMENT_API_RE =
  /^\/api\/attachments\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\/(content|download)$/i;

function attachmentIdFromAPIURL(rawURL: string): string | undefined {
  if (!rawURL) return undefined;
  if (attachmentIdFromDownloadURL(rawURL)) {
    return attachmentIdFromDownloadURL(rawURL);
  }
  let path = rawURL;
  const qi = path.indexOf("?");
  if (qi >= 0) path = path.slice(0, qi);
  const hi = path.indexOf("#");
  if (hi >= 0) path = path.slice(0, hi);
  if (/^https?:\/\//i.test(path)) {
    try {
      path = new URL(path).pathname;
    } catch {
      return undefined;
    }
  }
  return path.match(ATTACHMENT_API_RE)?.[1];
}

function isAttachmentAPIURL(rawURL: string): boolean {
  return !!attachmentIdFromAPIURL(rawURL);
}

function useAuthenticatedBlobMediaURL(
  attachmentId: string | undefined,
  pickedUrl: string,
  enabled: boolean,
): string {
  const [blobUrl, setBlobUrl] = useState<string | null>(null);
  const id = attachmentId ?? attachmentIdFromAPIURL(pickedUrl);
  const needsBlob =
    enabled &&
    !!id &&
    isAttachmentAPIURL(pickedUrl) &&
    hasEmbeddedSessionToken() &&
    typeof URL !== "undefined" &&
    typeof URL.createObjectURL === "function";

  useEffect(() => {
    let cancelled = false;
    let objectUrl = "";
    if (!needsBlob || !id) {
      setBlobUrl(null);
      return undefined;
    }

    api.getAttachmentBlobContent(id, pickedUrl)
      .then((blob) => {
        if (cancelled) return;
        objectUrl = URL.createObjectURL(blob);
        setBlobUrl(objectUrl);
      })
      .catch(() => {
        if (!cancelled) setBlobUrl(null);
      });

    return () => {
      cancelled = true;
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [id, needsBlob]);

  return blobUrl ?? pickedUrl;
}

// ---------------------------------------------------------------------------
// Dispatcher
// ---------------------------------------------------------------------------

export function Attachment({
  attachment,
  editable,
  selected,
  onDelete,
  className,
}: AttachmentProps) {
  const { resolveAttachment, openByUrl } = useAttachmentDownloadResolver();
  const cdnDomain = useConfigStore((s) => s.cdnDomain);
  const cdnSigned = useConfigStore((s) => s.cdnSigned);
  const download = useDownloadAttachment();
  const preview = useAttachmentPreview();

  const state = normalize(attachment, resolveAttachment, cdnDomain, cdnSigned);
  // The picked URL may still be the auth-gated API endpoint (reopened drafts
  // whose persisted record has no signed download_url). Upgrade it to a
  // freshly signed URL on clients that can't load the endpoint natively.
  const media = useResignedInlineMediaURL(state.attachmentId, state.url);
  const mediaUrl = media.url;
  const displayMediaUrl = useAuthenticatedBlobMediaURL(
    state.attachmentId,
    mediaUrl,
    media.allowBlobFallback,
  );
  const forceKind =
    attachment.kind === "url" ? attachment.forceKind : undefined;
  const kind =
    forceKind ??
    (state.filename || state.contentType
      ? getPreviewKind(state.contentType, state.filename)
      : null);

  const openPreview = () => {
    if (state.record) {
      preview.tryOpen({
        kind: "full",
        attachment: {
          ...state.record,
          download_url: displayMediaUrl || state.record.download_url,
        },
      });
      return;
    }
    if (displayMediaUrl) {
      preview.tryOpen({
        kind: "url",
        url: displayMediaUrl,
        filename: state.filename,
      });
    }
  };

  const handleDownload = () => {
    if (state.attachmentId) {
      download(state.attachmentId);
      return;
    }
    if (mediaUrl) openByUrl(mediaUrl);
  };

  if (kind === "image") {
    return (
      <>
        <ImageAttachmentView
          src={displayMediaUrl}
          copySrc={mediaUrl}
          alt={state.filename}
          uploading={state.uploading}
          width={state.width}
          height={state.height}
          editable={editable}
          selected={selected}
          onView={openPreview}
          onDownload={handleDownload}
          onDelete={onDelete}
          className={className}
        />
        {preview.modal}
      </>
    );
  }

  if (kind === "html" && state.attachmentId && !state.uploading) {
    return (
      <>
        <HtmlAttachmentPreview
          attachmentId={state.attachmentId}
          filename={state.filename}
          onPreview={openPreview}
          onDownload={handleDownload}
          onDelete={editable ? onDelete : undefined}
        />
        {preview.modal}
      </>
    );
  }

  return (
    <>
      <AttachmentCard
        filename={state.filename}
        contentType={state.contentType}
        attachmentId={state.attachmentId}
        href={mediaUrl || undefined}
        uploading={state.uploading}
        onPreview={openPreview}
        onDownload={handleDownload}
        onDelete={editable ? onDelete : undefined}
      />
      {preview.modal}
    </>
  );
}

// ---------------------------------------------------------------------------
// ImageAttachmentView — inline image with hover toolbar
// ---------------------------------------------------------------------------
//
// DOM and styling are intentionally a direct port of the original
// extensions/image-view.tsx <figure> structure. Shared visual styles live in
// styles/attachment.css under `.image-figure / .image-content / .image-toolbar`
// so standalone surfaces (chat messages, AttachmentList) get identical visuals
// without depending on the editor stylesheet being imported elsewhere.

interface ImageAttachmentViewProps {
  src: string;
  copySrc?: string;
  alt: string;
  uploading: boolean;
  width?: number;
  height?: number;
  editable?: boolean;
  selected?: boolean;
  onView: () => void;
  onDownload: () => void;
  onDelete?: () => void;
  className?: string;
}

function ImageAttachmentView({
  src,
  copySrc,
  alt,
  uploading,
  width,
  height,
  editable,
  selected,
  onView,
  onDownload,
  onDelete,
  className,
}: ImageAttachmentViewProps) {
  const { t } = useT("editor");

  const handleCopyLink = async () => {
    if (await copyText(copySrc || src)) {
      toast.success(t(($) => $.image.link_copied));
    } else {
      toast.error(t(($) => $.image.copy_link_failed));
    }
  };

  // Click on figure opens the preview only in non-editor / non-uploading
  // surfaces — inside the editor we let ProseMirror own the click for
  // selection / cursor placement and route preview through the explicit
  // Maximize button. The CSS rule `.image-figure[data-clickable="true"] {
  // cursor: zoom-in }` keys off this same flag for the cursor affordance.
  const clickable = !editable && !uploading;

  // DOM mirrors the original ReadonlyImage (span-only chain so it stays
  // valid HTML5 when rendered inside a markdown <p>). In editor surfaces
  // the NodeViewWrapper still emits its own outer .image-node div around
  // this — the duplicate `image-node` class is harmless.
  return (
    <span className="image-node">
      <span
        className={cn(
          "image-figure",
          selected && editable && "image-selected",
          className,
        )}
        data-clickable={clickable || undefined}
        contentEditable={false}
        onClick={clickable ? onView : undefined}
      >
        <img
          src={src || undefined}
          alt={alt}
          width={width}
          height={height}
          className={cn("image-content", uploading && "image-uploading")}
          draggable={false}
        />
        {!uploading && src && (
          <span
            className="image-toolbar"
            onMouseDown={(e) => e.stopPropagation()}
            onClick={(e) => e.stopPropagation()}
          >
            <button type="button" onClick={onView} title={t(($) => $.image.view)}>
              <Maximize2 className="size-3.5" />
            </button>
            <button type="button" onClick={onDownload} title={t(($) => $.image.download)}>
              <Download className="size-3.5" />
            </button>
            <button type="button" onClick={handleCopyLink} title={t(($) => $.image.copy_link)}>
              <LinkIcon className="size-3.5" />
            </button>
            {editable && onDelete && (
              <button type="button" onClick={onDelete} title={t(($) => $.image.delete)}>
                <Trash2 className="size-3.5" />
              </button>
            )}
          </span>
        )}
      </span>
    </span>
  );
}
