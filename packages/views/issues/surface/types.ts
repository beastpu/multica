import type { IssueScope } from "@multica/core/issues/surface/scope";
import type { CreateIssueRequest } from "@multica/core/types";
import type { ViewMode } from "@multica/core/issues/stores/view-store";
import type { IssueSurfaceMenuAction } from "./actions-context";

export type IssueCreateDefaults = Partial<
  Omit<
    CreateIssueRequest,
    "assignee_type" | "assignee_id" | "parent_issue_id" | "project_id"
  >
> & {
  assignee_type?: CreateIssueRequest["assignee_type"] | null;
  assignee_id?: string | null;
  parent_issue_id?: string | null;
  /** Display-only context for the create dialog while the parent query loads. */
  parent_issue_identifier?: string;
  project_id?: string | null;
};

export type IssueSurfaceMode = Extract<
  ViewMode,
  "board" | "list" | "table" | "swimlane" | "gantt"
>;

export interface IssueSurfaceProps {
  scope: IssueScope;
  modes: IssueSurfaceMode[];
  surfaceKey?: string;
  createDefaults?: IssueCreateDefaults;
  allowCreate?: boolean;
  onCreateIssue?: (defaults?: IssueCreateDefaults) => void;
  menuActions?: IssueSurfaceMenuAction[];
  /** Server-owned membership search shared by non-Table issue surfaces. */
  search?: string;
}
