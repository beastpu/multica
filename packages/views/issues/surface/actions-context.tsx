"use client";

import {
  createContext,
  useContext,
  type ComponentType,
  type ReactNode,
} from "react";
import type { Issue, UpdateIssueRequest } from "@multica/core/types";
import type { IssueCreateDefaults } from "./types";

export interface IssueSurfaceMenuAction {
  id: string;
  label: string;
  icon?: ComponentType<{ className?: string }>;
  variant?: "default" | "destructive";
  isVisible?: (issue: Issue) => boolean;
  onSelect: (issue: Issue) => void;
}

export type IssueSurfaceMutationOptions = {
  errorMessage?: string;
  onSuccess?: () => void;
  onError?: (err: unknown) => void;
  onSettled?: () => void;
};

export interface IssueSurfaceActions {
  isPending: boolean;
  menuActions?: IssueSurfaceMenuAction[];
  createIssue: (defaults?: IssueCreateDefaults) => void;
  updateIssue: (
    issueId: string,
    updates: Partial<UpdateIssueRequest>,
    options?: IssueSurfaceMutationOptions,
  ) => void;
  moveIssue: (
    issueId: string,
    updates: Partial<UpdateIssueRequest>,
    options?: IssueSurfaceMutationOptions,
  ) => void;
  batchUpdate: (
    issueIds: string[],
    updates: Partial<UpdateIssueRequest>,
  ) => Promise<void>;
  batchDelete: (issueIds: string[]) => Promise<void>;
}

const IssueSurfaceActionsContext = createContext<IssueSurfaceActions | null>(
  null,
);

export function IssueSurfaceActionsProvider({
  actions,
  children,
}: {
  actions: IssueSurfaceActions;
  children: ReactNode;
}) {
  return (
    <IssueSurfaceActionsContext.Provider value={actions}>
      {children}
    </IssueSurfaceActionsContext.Provider>
  );
}

export function useIssueSurfaceActionsOptional() {
  return useContext(IssueSurfaceActionsContext);
}
