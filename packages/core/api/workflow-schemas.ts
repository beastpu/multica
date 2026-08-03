import { z } from "zod";
import { AgentTaskSchema, IssueSchema } from "./schemas";
import type {
  ListWorkflowInstancesResponse,
  ListWorkflowsResponse,
  WorkflowAcceptance,
  WorkflowDefinition,
  WorkflowInstance,
  WorkflowInstanceDetail,
  WorkflowNodeDetail,
  WorkflowSubmission,
  WorkflowAcceptancesResponse,
  WorkflowDetail,
} from "../workflows/types";

const arrayOrEmpty = <T extends z.ZodType>(schema: T) =>
  z.preprocess((value) => (Array.isArray(value) ? value : []), z.array(schema));

const objectOrEmpty = z.preprocess(
  (value) => (typeof value === "object" && value !== null && !Array.isArray(value) ? value : {}),
  z.record(z.string(), z.unknown()),
);

const nullableString = z.string().nullable().optional().default(null);

const WorkflowRecentRunSchema = z.object({
  id: z.string(),
  title: z.string().optional().default("Untitled run"),
  status: z.string().optional().default("unknown"),
  started_at: z.string().optional().default(""),
  completed_at: nullableString,
}).loose();

const WorkflowRoleDefinitionSchema = z.object({
  key: z.string(),
  name: z.string(),
  required: z.boolean().optional().default(false),
  allowed_actor_types: arrayOrEmpty(z.string()),
}).loose();

const WorkflowIssueTemplateSchema = z.object({
  key: z.string(),
  title: z.string(),
  description: z.string().optional(),
  required: z.boolean().optional().default(false),
  initial_status: z.string().optional(),
  priority: z.string().optional(),
}).loose();

const WorkflowSubmissionFieldSchema = z.object({
  key: z.string(),
  name: z.string(),
  type: z.string(),
  required: z.boolean().optional().default(false),
}).loose();

const WorkflowCompletionDefinitionSchema = z.object({
  mode: z.enum(["automatic", "manual"]).optional().catch(undefined),
  required_issue_outcome: z.enum(["done", "terminal", "none"])
    .optional().catch(undefined),
  submission_required: z.boolean().optional().catch(undefined),
  handoff_required: z.boolean().optional().catch(undefined),
  authorized_roles: arrayOrEmpty(z.string()).optional(),
}).loose();

// One level of fallback, matching the definition: a chain longer than
// "who, and who instead" is not expressible and never was used.
//
// kind is optional here even though the definition requires it: a node that
// names nobody is picked up manually, and an executor arriving without one
// has to degrade to that rather than fail the whole response.
const WorkflowExecutorEntrySchema = z.object({
  kind: z.string().optional(),
  role: z.string().optional(),
  actor_type: z.enum(["member", "agent", "squad"]).optional(),
  actor_id: z.string().optional(),
  capability: z.string().optional(),
}).loose();

const WorkflowExecutorDefinitionSchema = WorkflowExecutorEntrySchema.extend({
  fallback: WorkflowExecutorEntrySchema.optional(),
}).loose();

const WorkflowReviewerDefinitionSchema = z.object({
  kind: z.string().optional(),
  role: z.string().optional(),
  actor_type: z.enum(["member", "agent", "squad"]).optional(),
  actor_id: z.string().optional(),
  api_url: z.string().optional(),
  condition: z.unknown().optional(),
  required: z.boolean().optional().default(false),
}).loose();

export const WorkflowNodeDefinitionSchema = z.object({
  key: z.string(),
  kind: z.string(),
  join_mode: z.string().optional(),
  name: z.string().optional().default(""),
  description: z.string().optional(),
  color: z.string().optional(),
  timeout_minutes: z.number().optional(),
  owner_role: z.string().optional(),
  issue_policy: z.string().optional(),
  issue_templates: arrayOrEmpty(WorkflowIssueTemplateSchema).optional().default([]),
  submission_schema: z.object({
    policy: z.string().optional(),
    fields: arrayOrEmpty(WorkflowSubmissionFieldSchema),
  }).loose().optional(),
  completion: WorkflowCompletionDefinitionSchema.optional().default({}),
  executor: WorkflowExecutorDefinitionSchema.optional(),
  reviewer: WorkflowReviewerDefinitionSchema.optional(),
}).loose();

export const WorkflowDefinitionSchema = z.object({
  schema_version: z.number().optional().default(1),
  name: z.string().optional().default(""),
  roles: arrayOrEmpty(WorkflowRoleDefinitionSchema),
  nodes: arrayOrEmpty(WorkflowNodeDefinitionSchema),
  edges: arrayOrEmpty(z.object({
    from: z.string(),
    to: z.string(),
    condition: z.unknown().optional(),
    default: z.boolean().optional(),
  }).loose()),
  acceptance: z.object({
    policy: z.string().optional(),
    approver_role: z.string().optional(),
  }).loose().optional().default({}),
  layout: z.unknown().optional(),
}).loose();

export const WorkflowSchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  name: z.string().optional().default("Untitled workflow"),
  description: z.string().optional().default(""),
  status: z.string().optional().default("published"),
  latest_published_version_id: nullableString,
  created_by: z.string().optional().default(""),
  archived_at: nullableString,
  created_at: z.string().optional().default(""),
  updated_at: z.string().optional().default(""),
  latest_published_version: z.number().optional().default(0),
  activity_count: z.number().optional().default(0),
  run_count: z.number().optional().default(0),
  recent_runs: arrayOrEmpty(WorkflowRecentRunSchema),
  last_published_by: nullableString,
  last_published_at: nullableString,
  latest_change_summary: z.string().optional().default(""),
}).loose();

export const WorkflowVersionSchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  workflow_id: z.string(),
  version: z.number().optional().default(0),
  revision: z.number().optional().default(1),
  definition: WorkflowDefinitionSchema,
  definition_checksum: z.string().optional().default(""),
  change_summary: z.string().optional().default(""),
  created_by: z.string().optional().default(""),
  published_by: nullableString,
  published_at: nullableString,
  created_at: z.string().optional().default(""),
  updated_at: z.string().optional().default(""),
}).loose();

const WorkflowCurrentActivitySchema = z.object({
  id: z.string(),
  node_key: z.string(),
  name: z.string().optional().default(""),
  status: z.string().optional().default("unknown"),
  attempt: z.number().optional().default(1),
}).loose();

const WorkflowActorReferenceSchema = z.object({
  actor_type: z.string(),
  actor_id: z.string(),
}).loose();

export const WorkflowInstanceSchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  workflow_id: z.string(),
  workflow_version_id: z.string(),
  host_issue_id: z.string(),
  title: z.string().optional().default(""),
  status: z.string().optional().default("unknown"),
  host_status_mode: z.string().optional().default("independent"),
  input: objectOrEmpty,
  result: objectOrEmpty,
  revision: z.number().optional().default(0),
  started_by_type: z.string().optional().default("system"),
  started_by_id: nullableString,
  started_at: z.string().optional().default(""),
  paused_at: nullableString,
  completed_at: nullableString,
  cancelled_at: nullableString,
  last_reconciled_at: nullableString,
  created_at: z.string().optional().default(""),
  updated_at: z.string().optional().default(""),
  next_action: z.string().optional().default("none"),
  intervention_reason: z.string().optional().default(""),
  host_issue_title: z.string().optional().default(""),
  host_issue_identifier: z.string().optional().default(""),
  host_issue_priority: z.string().optional().default("none"),
  project_id: nullableString,
  workflow_name: z.string().optional().default(""),
  workflow_version: z.number().optional().default(0),
  current_activities: arrayOrEmpty(WorkflowCurrentActivitySchema),
  activity_completed: z.number().optional().default(0),
  activity_total: z.number().optional().default(0),
  current_owners: arrayOrEmpty(WorkflowActorReferenceSchema),
}).loose();

export const WorkflowRoleAssignmentSchema = z.object({
  id: z.string(),
  role_key: z.string(),
  actor_type: z.string(),
  actor_id: z.string(),
  source: z.string().optional().default("user_selected"),
}).loose();

const WaitingReasonSchema = z.object({
  code: z.string(),
  field: z.string().optional(),
  message: z.string().optional().default(""),
}).loose();

export const WorkflowNodeInstanceSchema = z.object({
  id: z.string(),
  workflow_instance_id: z.string(),
  node_key: z.string(),
  node_kind: z.string(),
  attempt: z.number().optional().default(1),
  name: z.string().optional().default(""),
  display_order: z.number().optional().default(0),
  definition: WorkflowNodeDefinitionSchema,
  status: z.string().optional().default("unknown"),
  waiting_reasons: arrayOrEmpty(WaitingReasonSchema),
  latest_submission_id: nullableString,
  latest_verdict_id: nullableString,
  activated_at: nullableString,
  completed_at: nullableString,
}).loose();

export const WorkflowNodeTaskSchema = z.object({
  id: z.string(),
  workflow_node_instance_id: z.string(),
  task_key: z.string(),
  source: z.string().optional().default("template"),
  required: z.boolean().optional().default(false),
  definition: z.union([WorkflowIssueTemplateSchema, WorkflowNodeDefinitionSchema]),
  materialization_status: z.string().optional().default("unknown"),
  issue_id: nullableString,
  executor_resolution_id: nullableString,
  attempt_count: z.number().optional().default(0),
  last_error: z.string().optional().default(""),
}).loose();

export const WorkflowSubmissionSchema = z.object({
  id: z.string(),
  workflow_node_instance_id: z.string(),
  revision: z.number().optional().default(1),
  status: z.string().optional().default("unknown"),
  payload: objectOrEmpty,
  summary: z.string().optional().default(""),
  evidence: arrayOrEmpty(z.unknown()),
  submitted_by_type: z.string().optional().default("system"),
  submitted_by_id: nullableString,
  source_issue_id: nullableString,
  source_agent_run_id: nullableString,
  created_at: z.string().optional().default(""),
}).loose();

export const WorkflowVerdictSchema = z.object({
  id: z.string(),
  workflow_node_instance_id: z.string(),
  revision: z.number().optional().default(1),
  result: z.string().optional().default("unknown"),
  reason: z.string().optional().default(""),
  confidence: z.number().nullable().optional().default(null),
  evidence: arrayOrEmpty(z.unknown()),
  basis: objectOrEmpty,
  evaluator_type: z.string().optional().default("unknown"),
  evaluator_id: nullableString,
  created_at: z.string().optional().default(""),
}).loose();

export const WorkflowAcceptanceSchema = z.object({
  id: z.string(),
  workflow_node_instance_id: z.string(),
  revision: z.number().optional().default(1),
  status: z.string().optional().default("unknown"),
  decided_by_type: nullableString,
  decided_by_id: nullableString,
  reason: z.string().optional().default(""),
  rework_target_node_key: nullableString,
  evidence: arrayOrEmpty(z.unknown()),
  decided_at: nullableString,
  created_at: z.string().optional().default(""),
}).loose();

export const WorkflowNodeParticipantSchema = z.object({
  id: z.string(),
  role: z.string().optional().default("participant"),
  actor_type: z.string().optional().default("unknown"),
  actor_id: z.string().optional().default(""),
  created_at: z.string().optional().default(""),
}).loose();

export const WorkflowExecutorResolutionSchema = z.object({
  id: z.string(),
  workflow_node_instance_id: z.string(),
  workflow_node_task_id: nullableString,
  strategy: z.string().optional().default("manual"),
  status: z.string().optional().default("unknown"),
  actor_type: nullableString,
  actor_id: nullableString,
  candidates: arrayOrEmpty(z.unknown()),
  reason: z.string().optional().default(""),
  resolved_at: nullableString,
  created_at: z.string().optional().default(""),
}).loose();

export const WorkflowEventSchema = z.object({
  id: z.string(),
  workflow_node_instance_id: nullableString,
  event_type: z.string().optional().default("unknown"),
  actor_type: z.string().optional().default("system"),
  actor_id: nullableString,
  idempotency_key: z.string().optional().default(""),
  payload: objectOrEmpty,
  created_at: z.string().optional().default(""),
}).loose();

export const ListWorkflowInstancesResponseSchema = z.object({
  instances: arrayOrEmpty(WorkflowInstanceSchema),
  total: z.number().optional().default(0),
  next_cursor: nullableString,
}).loose();

export const WorkflowInstanceDetailSchema = z.object({
  instance: WorkflowInstanceSchema,
  role_assignments: arrayOrEmpty(WorkflowRoleAssignmentSchema),
  nodes: arrayOrEmpty(WorkflowNodeInstanceSchema),
  tasks: arrayOrEmpty(WorkflowNodeTaskSchema),
}).loose();

export const WorkflowArtifactSchema = z.object({
  id: z.string(),
  workflow_instance_id: z.string().optional().default(""),
  workflow_node_instance_id: z.string().optional().default(""),
  artifact_key: z.string().optional().default(""),
  attempt: z.number().optional().default(1),
  kind: z.string().optional().default("document"),
  name: z.string().optional().default(""),
  description: z.string().optional().default(""),
  content: z.string().optional().default(""),
  attachment_id: nullableString,
  url: z.string().optional().default(""),
  review_status: z.string().optional().default("submitted"),
  review_comment: z.string().optional().default(""),
  reviewed_by: nullableString,
  reviewed_at: nullableString,
  submitted_by_type: z.string().optional().default("system"),
  submitted_by_id: nullableString,
  created_at: z.string().optional().default(""),
  updated_at: z.string().optional().default(""),
}).loose();

export type WorkflowArtifact = z.infer<typeof WorkflowArtifactSchema>;

export const WorkflowArtifactListSchema = z.object({
  artifacts: arrayOrEmpty(WorkflowArtifactSchema),
}).loose();

export type WorkflowArtifactList = z.infer<typeof WorkflowArtifactListSchema>;

export const EMPTY_WORKFLOW_ARTIFACT_LIST: WorkflowArtifactList = { artifacts: [] };

export const WorkflowNodeDetailSchema = z.object({
  instance: WorkflowInstanceSchema,
  node: WorkflowNodeInstanceSchema,
  tasks: arrayOrEmpty(WorkflowNodeTaskSchema),
  executions: arrayOrEmpty(AgentTaskSchema),
  submissions: arrayOrEmpty(WorkflowSubmissionSchema),
  verdicts: arrayOrEmpty(WorkflowVerdictSchema),
  participants: arrayOrEmpty(WorkflowNodeParticipantSchema),
  executor_resolutions: arrayOrEmpty(WorkflowExecutorResolutionSchema),
}).loose();

export const ListWorkflowsResponseSchema = z.object({
  workflows: arrayOrEmpty(WorkflowSchema),
  total: z.number().optional().default(0),
}).loose();

export const BuiltinWorkflowTemplateSchema = z.object({
  key: z.string(),
  name: z.string().optional().default(""),
  description: z.string().optional().default(""),
}).loose();

export const ListBuiltinWorkflowTemplatesResponseSchema = z.object({
  templates: arrayOrEmpty(BuiltinWorkflowTemplateSchema),
}).loose();

export const WorkflowDetailSchema = z.object({
  workflow: WorkflowSchema,
  versions: arrayOrEmpty(WorkflowVersionSchema),
}).loose();

export const WorkflowCreateResponseSchema = z.object({
  workflow: WorkflowSchema,
  version: WorkflowVersionSchema,
}).loose();

export const WorkflowSaveResponseSchema = z.object({
  version: WorkflowVersionSchema,
}).loose();

export const WorkflowPublishResponseSchema = z.object({
  workflow: WorkflowSchema,
  version: WorkflowVersionSchema,
}).loose();

export const WorkflowDefinitionValidationResponseSchema = z.object({
  valid: z.boolean().optional().default(false),
  errors: arrayOrEmpty(z.string()),
}).loose();

export const WorkflowSubmissionMutationResponseSchema = z.object({
  submission: WorkflowSubmissionSchema,
  validation_errors: arrayOrEmpty(WaitingReasonSchema),
}).loose();

export const WorkflowVerdictMutationResponseSchema = z.object({
  verdict: WorkflowVerdictSchema,
}).loose();

export const WorkflowTaskMutationResponseSchema = z.object({
  task: WorkflowNodeTaskSchema,
}).loose();

export const WorkflowExecutorResolutionMutationResponseSchema = z.object({
  resolution: WorkflowExecutorResolutionSchema,
}).loose();

export const WorkflowAcceptanceMutationResponseSchema = z.object({
  acceptance: WorkflowAcceptanceSchema,
  instance: WorkflowInstanceSchema.optional(),
}).loose();

export const WorkflowIssuesResponseSchema = z.object({
  issues: arrayOrEmpty(IssueSchema),
  total: z.number().optional().default(0),
}).loose();

export const WorkflowAcceptancesResponseSchema = z.object({
  acceptances: arrayOrEmpty(WorkflowAcceptanceSchema),
}).loose();

export const WorkflowEventsResponseSchema = z.object({
  events: arrayOrEmpty(WorkflowEventSchema),
}).loose();

export const WorkflowDiagnosticsSchema = z.object({
  instance: WorkflowInstanceSchema,
  instance_revision: z.number().optional().default(0),
  last_reconciled_at: nullableString,
  nodes: arrayOrEmpty(z.object({
    node: WorkflowNodeInstanceSchema,
    tasks: arrayOrEmpty(WorkflowNodeTaskSchema),
    executor_resolutions: arrayOrEmpty(WorkflowExecutorResolutionSchema),
    submissions: arrayOrEmpty(WorkflowSubmissionSchema),
    verdicts: arrayOrEmpty(WorkflowVerdictSchema),
    }).loose()),
  acceptances: arrayOrEmpty(WorkflowAcceptanceSchema),
  allowed_rework_targets: arrayOrEmpty(z.string()),
  recent_sweeper_events: arrayOrEmpty(WorkflowEventSchema),
  events: arrayOrEmpty(WorkflowEventSchema),
}).loose();

export const EMPTY_WORKFLOW_INSTANCE: WorkflowInstance = {
  id: "", workspace_id: "", workflow_id: "", workflow_version_id: "", host_issue_id: "",
  title: "",
  status: "unknown", host_status_mode: "independent", input: {}, result: {}, revision: 0,
  started_by_type: "system", started_by_id: null, started_at: "", paused_at: null,
  completed_at: null, cancelled_at: null, last_reconciled_at: null, created_at: "",
  updated_at: "", next_action: "none", intervention_reason: "",
  host_issue_title: "", host_issue_identifier: "", host_issue_priority: "none",
  project_id: null, workflow_name: "", workflow_version: 0,
  current_activities: [], activity_completed: 0, activity_total: 0,
  current_owners: [],
};

export const EMPTY_LIST_WORKFLOW_INSTANCES: ListWorkflowInstancesResponse = {
  instances: [],
  total: 0,
  next_cursor: null,
};
export const EMPTY_WORKFLOW_ACCEPTANCES: WorkflowAcceptancesResponse = {
  acceptances: [],
};
export const EMPTY_LIST_WORKFLOWS: ListWorkflowsResponse = { workflows: [], total: 0 };
export const EMPTY_WORKFLOW_INSTANCE_DETAIL: WorkflowInstanceDetail = {
  instance: EMPTY_WORKFLOW_INSTANCE, role_assignments: [], nodes: [], tasks: [],
};
export const EMPTY_WORKFLOW_NODE_DETAIL: WorkflowNodeDetail = {
  instance: EMPTY_WORKFLOW_INSTANCE,
  node: {
    id: "", workflow_instance_id: "", node_key: "", node_kind: "unknown", attempt: 1,
    name: "", display_order: 0, definition: { key: "", kind: "unknown", name: "" },
    status: "unknown", waiting_reasons: [], latest_submission_id: null,
    latest_verdict_id: null, activated_at: null, completed_at: null,
  },
  tasks: [], executions: [], submissions: [], verdicts: [], participants: [],
  executor_resolutions: [],
};
export const EMPTY_WORKFLOW_DETAIL: WorkflowDetail = {
  workflow: {
    id: "", workspace_id: "", name: "", description: "",
    status: "unknown", latest_published_version_id: null,
    created_by: "", archived_at: null, created_at: "", updated_at: "",
    latest_published_version: 0,
    activity_count: 0, run_count: 0, recent_runs: [], last_published_by: null,
    last_published_at: null, latest_change_summary: "",
  },
  versions: [],
};

export const EMPTY_WORKFLOW_TEMPLATE_VERSION = {
  id: "",
  workspace_id: "",
  workflow_id: "",
  version: 0,
  revision: 1,
  definition: {
    schema_version: 1,
    name: "",
    roles: [],
    nodes: [],
    edges: [],
    acceptance: {},
  },
  definition_checksum: "",
  change_summary: "",
  created_by: "",
  published_by: null,
  published_at: null,
  created_at: "",
  updated_at: "",
} satisfies import("../workflows/types").WorkflowVersion;

export type ParsedWorkflowDefinition = z.infer<typeof WorkflowDefinitionSchema> & WorkflowDefinition;
export type WorkflowSubmissionMutationResponse = {
  submission: WorkflowSubmission;
  validation_errors: Array<{ code: string; field?: string; message: string }>;
};
export type WorkflowAcceptanceMutationResponse = {
  acceptance: WorkflowAcceptance;
  instance?: WorkflowInstance;
};
