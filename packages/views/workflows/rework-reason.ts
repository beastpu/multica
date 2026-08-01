/**
 * A rework reason is read by whoever redoes the work — often an agent, which
 * gets it verbatim in its task brief. Free text tends to arrive as a sentence
 * like "some points were missed", which reaches the executor intact and still
 * tells it nothing, so the form asks three questions instead of one.
 *
 * The composed labels stay English on purpose. The form around them is
 * localised, but the string itself is data travelling to an executor that may
 * not share the operator's UI language.
 */
export type ReworkReasonDraft = {
  /** Which acceptance criterion, test, or case failed. */
  criterion: string;
  /** Expected behaviour versus what actually happened. */
  gap: string;
  /** Where the author suggests changing things. Optional. */
  hint: string;
};

export const emptyReworkReasonDraft: ReworkReasonDraft = {
  criterion: "",
  gap: "",
  hint: "",
};

/**
 * The gap is the one field that cannot be inferred from anywhere else: without
 * it there is no statement of what is wrong, and the executor is back to
 * guessing. The other two are helpful but recoverable from the issue.
 */
export function isReworkReasonComplete(draft: ReworkReasonDraft): boolean {
  return draft.gap.trim().length > 0;
}

export function composeReworkReason(draft: ReworkReasonDraft): string {
  const lines: string[] = [];
  const criterion = draft.criterion.trim();
  const gap = draft.gap.trim();
  const hint = draft.hint.trim();
  if (criterion) lines.push(`Failed: ${criterion}`);
  if (gap) lines.push(`Expected vs actual: ${gap}`);
  if (hint) lines.push(`Suggested fix: ${hint}`);
  return lines.join("\n");
}
