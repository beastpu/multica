import type { Issue } from "../types";

export interface WorkflowRoleDefinition {
  key: string;
  name: string;
  required: boolean;
  allowed_actor_types: string[];
  /** Optional template-level default assignee, pre-filled when starting. */
  default_actor_type?: "member" | "agent" | "squad";
  default_actor_id?: string;
}

export interface WorkflowIssueTemplate {
  key: string;
  title: string;
  description?: string;
  assignee_role?: string;
  assignee_type?: "member" | "agent" | "squad";
  assignee_id?: string;
  required: boolean;
  initial_status?: string;
  priority?: string;
}

export interface WorkflowExecutorStrategy {
  kind: string;
  role?: string;
  actor_type?: "member" | "agent" | "squad";
  actor_id?: string;
  capability?: string;
  node?: string;
  field?: string;
  /** Structured condition gating this strategy; same DSL as gateway edges. */
  condition?: unknown;
}

export interface WorkflowNodeAction {
  kind: string;
  status?: string;
}

export interface WorkflowCompletionDefinition {
  mode?: "automatic" | "manual";
  required_issue_outcome?: "done" | "terminal" | "none";
  submission_required?: boolean;
  verdict_required?: "none" | "pass" | "not_blocked";
  confirmation?:
    | "none"
    | "owner_any"
    | "owner_all"
    | "member_any"
    | "member_all"
    | "admin_only";
  /** Workflow roles additionally allowed to complete/skip/rollback. */
  authorized_roles?: string[];
}

/** One formal output a node is expected to deliver. */
export interface WorkflowArtifactRequirement {
  key: string;
  name: string;
  description?: string;
  kind?: "document" | "attachment" | "link";
  required?: boolean;
}

export interface WorkflowNodeDefinition {
  key: string;
  kind: string;
  join_mode?: string;
  activity_mode?: string;
  name: string;
  description?: string;
  color?: string;
  timeout_minutes?: number;
  owner_role?: string;
  participant_roles?: string[];
  issue_policy?: string;
  issue_templates?: WorkflowIssueTemplate[];
  artifacts?: WorkflowArtifactRequirement[];
  submission_schema?: {
    policy?: "none" | "single" | "per_required_task" | "fan_in";
  };
  verdict?: {
    evaluator: string;
    required_result?: string;
    condition?: unknown;
  };
  completion?: WorkflowCompletionDefinition;
  executor?: {
    strategies: WorkflowExecutorStrategy[];
  };
  /** Controlled side effects when the activity activates / completes. */
  on_enter?: WorkflowNodeAction[];
  on_complete?: WorkflowNodeAction[];
}

export interface WorkflowDefinition {
  schema_version: number;
  name: string;
  applies_to: { kind: string; type_key?: string };
  roles: WorkflowRoleDefinition[];
  nodes: WorkflowNodeDefinition[];
  edges: Array<{ from: string; to: string; condition?: unknown; default?: boolean }>;
  acceptance: {
    policy?: string;
    approver_role?: string;
    node_key?: string;
    rework_targets?: string[];
  };
  layout?: unknown;
}

export interface BuiltinWorkflowTemplate {
  key: string;
  name: string;
  description: string;
}

export interface WorkflowTemplate {
  id: string;
  workspace_id: string;
  name: string;
  description: string;
  applies_to_kind: string;
  applies_to_type_key: string;
  status: string;
  latest_published_version_id: string | null;
  created_by: string;
  archived_at: string | null;
  created_at: string;
  updated_at: string;
  latest_published_version: number;
  draft_version: number;
  has_draft: boolean;
  activity_count: number;
  run_count: number;
  last_published_by: string | null;
  last_published_at: string | null;
  latest_change_summary: string;
}

export interface WorkflowTemplateVersion {
  id: string;
  workspace_id: string;
  template_id: string;
  version: number;
  revision: number;
  status: string;
  definition: WorkflowDefinition;
  definition_checksum: string;
  change_summary: string;
  created_by: string;
  published_by: string | null;
  published_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface WorkflowInstance {
  id: string;
  workspace_id: string;
  template_id: string;
  template_version_id: string;
  host_issue_id: string;
  status: string;
  host_status_mode: string;
  input: Record<string, unknown>;
  result: Record<string, unknown>;
  revision: number;
  started_by_type: string;
  started_by_id: string | null;
  started_at: string;
  paused_at: string | null;
  completed_at: string | null;
  cancelled_at: string | null;
  last_reconciled_at: string | null;
  created_at: string;
  updated_at: string;
  next_action: string;
  intervention_reason: string;
  host_issue_title: string;
  host_issue_identifier: string;
  host_issue_priority: string;
  project_id: string | null;
  template_name: string;
  template_version: number;
  current_activities: WorkflowCurrentActivity[];
  activity_completed: number;
  activity_total: number;
  current_owners: WorkflowActorReference[];
}

export interface WorkflowCurrentActivity {
  id: string;
  node_key: string;
  name: string;
  status: string;
  attempt: number;
}

export interface WorkflowActorReference {
  actor_type: string;
  actor_id: string;
}

export interface WorkflowRoleAssignment {
  id: string;
  role_key: string;
  actor_type: string;
  actor_id: string;
  source: string;
}

export interface WorkflowNodeInstance {
  id: string;
  workflow_instance_id: string;
  node_key: string;
  node_kind: string;
  attempt: number;
  name: string;
  display_order: number;
  definition: WorkflowNodeDefinition;
  status: string;
  waiting_reasons: Array<{ code: string; field?: string; message: string }>;
  latest_submission_id: string | null;
  latest_verdict_id: string | null;
  activated_at: string | null;
  completed_at: string | null;
}

export interface WorkflowNodeTask {
  id: string;
  workflow_node_instance_id: string;
  task_key: string;
  source: string;
  required: boolean;
  definition: WorkflowIssueTemplate;
  materialization_status: string;
  issue_id: string | null;
  executor_resolution_id: string | null;
  attempt_count: number;
  last_error: string;
}

export interface WorkflowNodeParticipant {
  id: string;
  role: string;
  actor_type: string;
  actor_id: string;
  created_at: string;
}

export interface WorkflowExecutorResolution {
  id: string;
  workflow_node_instance_id: string;
  workflow_node_task_id: string | null;
  strategy: string;
  status: string;
  actor_type: string | null;
  actor_id: string | null;
  candidates: unknown[];
  reason: string;
  resolved_at: string | null;
  created_at: string;
}

export interface WorkflowConfirmation {
  id: string;
  workflow_node_instance_id: string;
  member_id: string;
  decision: string;
  comment: string;
  decided_at: string;
  updated_at: string;
}

export interface WorkflowSubmission {
  id: string;
  workflow_node_instance_id: string;
  revision: number;
  status: string;
  payload: Record<string, unknown>;
  summary: string;
  evidence: unknown[];
  submitted_by_type: string;
  submitted_by_id: string | null;
  source_issue_id: string | null;
  source_agent_run_id: string | null;
  created_at: string;
}

export interface WorkflowVerdict {
  id: string;
  workflow_node_instance_id: string;
  revision: number;
  result: string;
  reason: string;
  confidence: number | null;
  evidence: unknown[];
  basis: Record<string, unknown>;
  evaluator_type: string;
  evaluator_id: string | null;
  created_at: string;
}

export interface WorkflowAcceptance {
  id: string;
  workflow_node_instance_id: string;
  revision: number;
  status: string;
  decided_by_type: string | null;
  decided_by_id: string | null;
  reason: string;
  rework_target_node_key: string | null;
  evidence: unknown[];
  decided_at: string | null;
  created_at: string;
}

export interface WorkflowInstanceDetail {
  instance: WorkflowInstance;
  role_assignments: WorkflowRoleAssignment[];
  nodes: WorkflowNodeInstance[];
  tasks: WorkflowNodeTask[];
}

export interface WorkflowNodeDetail {
  instance: WorkflowInstance;
  node: WorkflowNodeInstance;
  tasks: WorkflowNodeTask[];
  submissions: WorkflowSubmission[];
  verdicts: WorkflowVerdict[];
  participants: WorkflowNodeParticipant[];
  executor_resolutions: WorkflowExecutorResolution[];
  confirmations: WorkflowConfirmation[];
}

export interface ListWorkflowInstancesResponse {
  instances: WorkflowInstance[];
  total: number;
  next_cursor?: string | null;
}

export interface ListWorkflowTemplatesResponse {
  templates: WorkflowTemplate[];
  total: number;
}

export interface WorkflowTemplateDetail {
  template: WorkflowTemplate;
  versions: WorkflowTemplateVersion[];
}

export interface WorkflowIssuesResponse {
  issues: Issue[];
  total: number;
}

export interface WorkflowAcceptancesResponse {
  acceptances: WorkflowAcceptance[];
}

export interface WorkflowEvent {
  id: string;
  workflow_node_instance_id: string | null;
  event_type: string;
  actor_type: string;
  actor_id: string | null;
  idempotency_key: string;
  payload: Record<string, unknown>;
  created_at: string;
}

export interface WorkflowEventsResponse {
  events: WorkflowEvent[];
}

export interface WorkflowDiagnosticNode {
  node: WorkflowNodeInstance;
  tasks: WorkflowNodeTask[];
  executor_resolutions: WorkflowExecutorResolution[];
  submissions: WorkflowSubmission[];
  verdicts: WorkflowVerdict[];
  confirmations: WorkflowConfirmation[];
}

export interface WorkflowDiagnostics {
  instance: WorkflowInstance;
  instance_revision: number;
  last_reconciled_at: string | null;
  nodes: WorkflowDiagnosticNode[];
  acceptances: WorkflowAcceptance[];
  allowed_rework_targets: string[];
  recent_sweeper_events: WorkflowEvent[];
  events: WorkflowEvent[];
}

export interface StartWorkflowInput {
  template_id: string;
  template_version_id?: string;
  host_status_mode?: string;
  input?: Record<string, unknown>;
  role_assignments: Array<{
    role_key: string;
    actor_type: string;
    actor_id: string;
    source?: string;
  }>;
  idempotency_key: string;
}

export interface CreateWorkflowInput extends Omit<StartWorkflowInput, "template_id"> {
  title: string;
  description?: string;
  priority?: string;
  project_id?: string;
  template_id: string;
}
