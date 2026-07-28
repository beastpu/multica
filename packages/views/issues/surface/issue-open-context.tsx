"use client";

import { createContext, useContext, useMemo, type ReactNode } from "react";

// IssueOpenContext lets a surface intercept "open this issue" without changing
// what the card is: still a real link, so middle-click, ⌘-click and "copy link"
// keep working, and a surface that does not provide a handler navigates
// exactly as before.
//
// It exists for surfaces where navigating away destroys the context the user
// is working in — the workflow workbench being the case that prompted it,
// where reading one issue's comments used to mean leaving the run and then
// finding your place in it again.
const IssueOpenContext = createContext<((issueId: string) => void) | null>(
  null,
);

export function IssueOpenProvider({
  onOpenIssue,
  children,
}: {
  onOpenIssue: (issueId: string) => void;
  children: ReactNode;
}) {
  // Consumers use this as an effect dependency, so an inline arrow from the
  // caller would re-run them on every parent render.
  const value = useMemo(() => onOpenIssue, [onOpenIssue]);
  return (
    <IssueOpenContext.Provider value={value}>
      {children}
    </IssueOpenContext.Provider>
  );
}

/**
 * Returns the surface's in-place open handler, or null when the surface has
 * none and the card should navigate normally.
 */
export function useIssueOpen() {
  return useContext(IssueOpenContext);
}

/**
 * Builds the click handler for an issue card. Returns undefined when there is
 * no in-place handler, leaving the link untouched.
 *
 * Modified clicks (⌘/ctrl/shift/middle) fall through to the browser so
 * "open in a new tab" keeps working even on an intercepting surface.
 */
export function useIssueOpenClick(issueId: string) {
  const openIssue = useIssueOpen();
  return useMemo(() => {
    if (!openIssue) return undefined;
    return (event: React.MouseEvent) => {
      if (
        event.defaultPrevented || event.metaKey || event.ctrlKey ||
        event.shiftKey || event.altKey || event.button !== 0
      ) {
        return;
      }
      event.preventDefault();
      openIssue(issueId);
    };
  }, [openIssue, issueId]);
}
