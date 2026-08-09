import type { Issue } from "../types";
import type { AgentTask } from "../types/agent";

export interface WorkflowRoleDefinition {
  key: string;
  name: string;
  required: boolean;
  allowed_actor_types: string[];
}

export interface WorkflowIssueTemplate {
  key: string;
  title: string;
  description?: string;
  required: boolean;
  initial_status?: string;
  priority?: string;
}

/**
 * Who is expected to produce the node's output. The single entry point: an
 * issue template carries no assignee, so there is one place to read and one
 * answer to give. `fallback` is one layer deep and cannot nest.
 */
export interface WorkflowExecutorDefinition {
  /** Absent means manual pickup — the same thing an unresolvable one degrades to. */
  kind?: "role" | "actor" | "capability" | "manual";
  role?: string;
  actor_type?: "member" | "agent" | "squad";
  actor_id?: string;
  capability?: string;
  fallback?: WorkflowExecutorDefinition;
}

/**
 * Who judges the node's output. Absent means the node completes on delivery;
 * present and required means delivery moves the node to in_review.
 */
export interface WorkflowReviewerDefinition {
  kind?: "role" | "actor" | "api" | "owner" | "auto";
  role?: string;
  actor_type?: "member" | "agent" | "squad";
  actor_id?: string;
  api_url?: string;
  condition?: unknown;
  required?: boolean;
}

export interface WorkflowCompletionDefinition {
  mode?: "automatic" | "manual";
  required_issue_outcome?: "done" | "terminal" | "none";
  submission_required?: boolean;
  /** Cap on rework rounds; 0 or absent means uncapped. */
  max_attempts?: number;
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

export type WorkflowOutputFieldType =
  | "bool"
  | "enum"
  | "number"
  | "string"
  | "string[]";

/** One structured field an activity owes on delivery. */
export interface WorkflowOutputField {
  key: string;
  type: WorkflowOutputFieldType;
  values?: string[];
  required?: boolean;
  desc?: string;
  max_len?: number;
}

/**
 * One row of a gateway's routing table. Evaluated in declared order, first
 * match wins; the mandatory trailing else case has id "else" and no `when`.
 */
export interface WorkflowGatewayCase {
  id: string;
  label?: string;
  when?: string;
}

export interface WorkflowNodeDefinition {
  key: string;
  kind: string;
  join_mode?: string;
  name: string;
  description?: string;
  color?: string;
  timeout_minutes?: number;
  owner_role?: string;
  issue_policy?: string;
  issue_templates?: WorkflowIssueTemplate[];
  artifacts?: WorkflowArtifactRequirement[];
  submission_schema?: {
    policy?: "none" | "single" | "per_required_task" | "fan_in";
  };
  outputs?: WorkflowOutputField[];
  cases?: WorkflowGatewayCase[];
  /** Gateway routing: "switch" takes the first match, "filter" takes all. */
  mode?: "switch" | "filter";
  completion?: WorkflowCompletionDefinition;
  executor?: WorkflowExecutorDefinition;
  reviewer?: WorkflowReviewerDefinition;
}

/** One artifact the node owes, paired with whether it has been delivered. */
export interface WorkflowNodeContextDuty {
  id: string;
  key: string;
  name: string;
  description: string;
  kind: string;
  required: boolean;
  delivered: boolean;
  review_status: string;
}

export interface WorkflowNodeContextUpstreamArtifact {
  id: string;
  artifact_key: string;
  kind: string;
  name: string;
}

/**
 * One direct predecessor's handover. `summary` is the conclusion its author
 * wrote; `worker_output` is raw execution output the platform fell back to when
 * nobody wrote one. They stay apart so an extract is never shown as a
 * conclusion.
 */
export interface WorkflowNodeContextUpstream {
  node_key: string;
  name: string;
  status: string;
  summary: string;
  worker_output: string;
  issues: string[];
  artifacts: WorkflowNodeContextUpstreamArtifact[];
}

/**
 * What a node child issue is a node of, read live. None of it is stored on the
 * issue: a rework changes the upstream conclusion, the delivery state and the
 * run's position together, so a copy written into the description would be
 * wrong exactly when it mattered.
 */
export interface WorkflowNodeContext {
  instance_id: string;
  node_instance_id: string;
  node_key: string;
  node_name: string;
  run_title: string;
  instructions: string;
  host_issue: string;
  node_issues: string[];
  artifacts: WorkflowNodeContextDuty[];
  outputs: WorkflowOutputField[];
  upstream: WorkflowNodeContextUpstream[];
}

export interface WorkflowDefinition {
  schema_version: number;
  name: string;
  roles: WorkflowRoleDefinition[];
  nodes: WorkflowNodeDefinition[];
  edges: Array<{ from: string; to: string; from_case?: string }>;
  acceptance: {
    policy?: string;
    approver_role?: string;
  };
  layout?: unknown;
}

export interface BuiltinWorkflowTemplate {
  key: string;
  name: string;
  description: string;
}

export interface Workflow {
  id: string;
  workspace_id: string;
  name: string;
  description: string;
  status: string;
  latest_published_version_id: string | null;
  created_by: string;
  archived_at: string | null;
  created_at: string;
  updated_at: string;
  latest_published_version: number;
  activity_count: number;
  run_count: number;
  recent_runs: WorkflowRecentRun[];
  last_published_by: string | null;
  last_published_at: string | null;
  latest_change_summary: string;
}

export interface WorkflowRecentRun {
  id: string;
  title: string;
  status: string;
  started_at: string;
  completed_at: string | null;
}

export interface WorkflowVersion {
  id: string;
  workspace_id: string;
  workflow_id: string;
  version: number;
  revision: number;
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
  workflow_id: string;
  workflow_version_id: string;
  host_issue_id: string;
  title: string;
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
  workflow_name: string;
  workflow_version: number;
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
  definition: WorkflowIssueTemplate | WorkflowNodeDefinition;
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
  executions: AgentTask[];
  submissions: WorkflowSubmission[];
  verdicts: WorkflowVerdict[];
  participants: WorkflowNodeParticipant[];
  executor_resolutions: WorkflowExecutorResolution[];
}

export interface ListWorkflowInstancesResponse {
  instances: WorkflowInstance[];
  total: number;
  next_cursor?: string | null;
}

export interface ListWorkflowsResponse {
  workflows: Workflow[];
  total: number;
}

export interface WorkflowDetail {
  workflow: Workflow;
  versions: WorkflowVersion[];
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
  workflow_id: string;
  workflow_version_id?: string;
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

export interface CreateWorkflowInput extends Omit<StartWorkflowInput, "workflow_id"> {
  title: string;
  description?: string;
  priority?: string;
  project_id?: string;
  workflow_id: string;
}

export interface RunWorkflowInput {
  title?: string;
  workflow_version_id?: string;
  input?: Record<string, unknown>;
  role_assignments: StartWorkflowInput["role_assignments"];
  idempotency_key: string;
}
