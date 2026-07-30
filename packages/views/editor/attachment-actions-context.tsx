"use client";

import { createContext, useContext, type ReactNode } from "react";

/**
 * An extra action a surface wants on every attachment card.
 *
 * The card is a generic editor component and must not learn what a workflow
 * artifact is, so surfaces contribute actions instead of the card importing
 * them. A surface that contributes nothing leaves the card exactly as it was.
 */
export interface AttachmentAction {
  /** Stable across renders — used as the React key. */
  id: string;
  label: string;
  icon?: ReactNode;
  /** Called with the attachment id the card was rendered for. */
  onSelect: (attachmentId: string) => void;
  disabled?: boolean;
}

const AttachmentActionsContext = createContext<
  ((attachmentId: string) => AttachmentAction[]) | null
>(null);

export function AttachmentActionsProvider({
  actionsFor,
  children,
}: {
  /**
   * Returns the actions for one attachment. Return an empty array to leave a
   * particular attachment alone — the workflow surface uses this to offer
   * "adopt as artifact" only where an artifact slot can actually take it.
   */
  actionsFor: (attachmentId: string) => AttachmentAction[];
  children: ReactNode;
}) {
  return (
    <AttachmentActionsContext.Provider value={actionsFor}>
      {children}
    </AttachmentActionsContext.Provider>
  );
}

/** Actions contributed by the surrounding surface, empty when there is none. */
export function useAttachmentActions(attachmentId?: string) {
  const actionsFor = useContext(AttachmentActionsContext);
  if (!actionsFor || !attachmentId) return [];
  return actionsFor(attachmentId);
}
