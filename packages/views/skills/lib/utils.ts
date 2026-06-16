export function isNameConflictError(msg: string): boolean {
  return /\b(409|conflict|already exists|unique constraint)\b/i.test(msg);
}

// Pulls the first http(s) URL out of arbitrary pasted text. Atlas hands users an
// "install prompt" — a block of prose with the skill URL embedded mid-sentence
// (e.g. `阅读 Atlas 官方安装说明 https://…/install-prompt?t=…，先总结…`). Extracting
// the URL lets users paste that block verbatim instead of hand-trimming it,
// keeping the existing Atlas copy-paste flow friction-free.
//
// Returns the trimmed input unchanged when no http(s) URL is present, so bare
// hosts/slugs (which the backend still accepts and prefixes with https://) keep
// working.
export function extractSkillUrl(text: string): string {
  const trimmed = text.trim();
  // RFC 3986 URL characters only. The class deliberately excludes whitespace and
  // CJK punctuation, so the match stops at the full-width comma "，" that follows
  // the Atlas URL — the surrounding Chinese prose is never swallowed into it.
  const match = trimmed.match(
    /https?:\/\/[A-Za-z0-9\-._~:/?#[\]@!$&'()*+,;=%]+/i,
  );
  if (!match) return trimmed;
  // Trailing sentence punctuation (".,;:!?") is valid in a URL but almost never
  // ends a real skill URL, so drop it when prose left it attached.
  return match[0].replace(/[.,;:!?]+$/, "");
}
