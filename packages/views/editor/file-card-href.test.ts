import { describe, expect, it } from "vitest";

import {
  FILE_CARD_URL_PATTERN,
  isAllowedFileCardHref,
  preprocessFileCards,
} from "@multica/ui/markdown";

const ATTACHMENT_ID = "11111111-2222-3333-4444-555555555555";
const ATTACHMENT_DOWNLOAD = `/api/attachments/${ATTACHMENT_ID}/download`;
const ATTACHMENT_CONTENT = `/api/attachments/${ATTACHMENT_ID}/content?workspace_id=ws-1`;

const allowedClickHrefs = [
  "/uploads/ok",
  "/uploads/workspaces/abc/file.png",
  "https://cdn.example.com/x",
  "http://localhost:8080/uploads/x.png",
  "HTTPS://CDN.EXAMPLE.COM/x",
  ATTACHMENT_DOWNLOAD,
  ATTACHMENT_CONTENT,
];

const parsedAllowedFileCardHrefs = [
  "/uploads/x.md",
  "/uploads/workspaces/abc/019e.md",
  "https://cdn.example.com/x.md",
  "http://localhost:8080/uploads/x.md",
  ATTACHMENT_DOWNLOAD,
  ATTACHMENT_CONTENT,
];

const rejectedFileCardHrefs = [
  "javascript:alert(1)",
  "JavaScript:alert(1)",
  "data:text/html,xss",
  "//evil.com/x",
  "/../api/x",
  "/api/x",
  "/api/internal/x",
  `/api/attachments/${ATTACHMENT_ID}`,
  "/api/attachments/not-a-uuid/download",
  "/api/attachments//download",
  `/api/attachments/${ATTACHMENT_ID}/download/../../x`,
  `/api/attachments/${ATTACHMENT_ID}/download?x=1`,
  `/api/attachments/${ATTACHMENT_ID}/download#fragment`,
  "",
  "ftp://example.com/x",
  "uploads/x",
];

describe("isAllowedFileCardHref", () => {
  it.each(allowedClickHrefs)("accepts %s", (href) => {
    expect(isAllowedFileCardHref(href)).toBe(true);
  });

  it.each(rejectedFileCardHrefs)("rejects %s", (href) => {
    expect(isAllowedFileCardHref(href)).toBe(false);
  });
});

describe("FILE_CARD_URL_PATTERN", () => {
  // Mirror the parser usage: a fresh anchored regex composed from the pattern.
  const parser = new RegExp(
    `^!file\\[([^\\]]*)\\]\\((${FILE_CARD_URL_PATTERN.source})\\)$`,
  );

  it.each(parsedAllowedFileCardHrefs)("parses %s", (href) => {
    expect(parser.test(`!file[doc.md](${href})`)).toBe(true);
  });

  it.each(rejectedFileCardHrefs)("does not parse %s", (href) => {
    expect(parser.test(`!file[doc.md](${href})`)).toBe(false);
  });

  it.each([
    ...parsedAllowedFileCardHrefs.map((href) => [href, true] as const),
    ...rejectedFileCardHrefs.map((href) => [href, false] as const),
  ])("matches the click gate for %s", (href, expected) => {
    expect(parser.test(`!file[doc.md](${href})`)).toBe(expected);
    expect(isAllowedFileCardHref(href)).toBe(expected);
  });
});

describe("preprocessFileCards (integration)", () => {
  const cdn = "cdn.example.com";

  it("converts !file[…](/uploads/…) into a file-card div", () => {
    const out = preprocessFileCards("!file[doc.md](/uploads/x.md)", cdn);
    expect(out).toContain('data-type="fileCard"');
    expect(out).toContain('data-href="/uploads/x.md"');
    expect(out).toContain('data-filename="doc.md"');
  });

  it("converts !file[…](attachment download URL) into a file-card div", () => {
    const out = preprocessFileCards(
      `!file[doc.md](${ATTACHMENT_DOWNLOAD})`,
      cdn,
    );
    expect(out).toContain('data-type="fileCard"');
    expect(out).toContain(`data-href="${ATTACHMENT_DOWNLOAD}"`);
    expect(out).toContain('data-filename="doc.md"');
  });

  it("leaves a protocol-relative href untouched (not parsed as file-card)", () => {
    const out = preprocessFileCards("!file[evil.txt](//evil.com/x)", cdn);
    expect(out).not.toContain('data-type="fileCard"');
    expect(out).toBe("!file[evil.txt](//evil.com/x)");
  });

  it("leaves javascript: untouched (not parsed as file-card)", () => {
    const out = preprocessFileCards(
      "!file[evil.txt](javascript:alert(1))",
      cdn,
    );
    expect(out).not.toContain('data-type="fileCard"');
  });

  it("leaves a non-/uploads relative path untouched", () => {
    const out = preprocessFileCards("!file[name](/api/internal/x)", cdn);
    expect(out).not.toContain('data-type="fileCard"');
  });

  // A file-card emits an HTML <div> block. Without a trailing blank line the
  // markdown HTML-block rule swallows the next line, so a synced description
  // like "!file[log]\n![shot.png]" loses the image. The div must be blank-line
  // separated from adjacent content.
  it("blank-line separates the file-card div from an adjacent image", () => {
    const md =
      `!file[log.log](${ATTACHMENT_CONTENT})\n` +
      `![shot.png](/api/attachments/22222222-3333-4444-5555-666666666666/content?workspace_id=ws-1)`;
    const out = preprocessFileCards(md, cdn);
    expect(out).toContain("![shot.png]"); // image line preserved untouched
    expect(out).toContain("</div>\n\n![shot.png]"); // blank line before the image
  });

  it("blank-line separates two consecutive file-cards", () => {
    const md =
      "!file[a.log](/uploads/a.log)\n!file[b.log](/uploads/b.log)";
    const out = preprocessFileCards(md, cdn);
    expect(out).toMatch(/<\/div>\n{2,}<div/); // at least one blank line between cards
  });
});
