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
 * Builds AppLink's `intercept` for an issue card: returns true when the
 * surface opened the issue in place, so no route is pushed.
 *
 * Returns undefined when no surface handles opening, leaving the link fully
 * default. AppLink already routes modifier-clicks to the new-tab path before
 * consulting intercept, so "open in a new tab" needs no handling here.
 */
export function useIssueOpenIntercept(issueId: string) {
  const openIssue = useIssueOpen();
  return useMemo(() => {
    if (!openIssue) return undefined;
    return () => {
      openIssue(issueId);
      return true;
    };
  }, [openIssue, issueId]);
}
