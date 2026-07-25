"use client";

import {
  AlertCircle,
  Check,
  ChevronRight,
  CircleDot,
  FileCheck2,
  GitBranch,
  History,
  ListChecks,
  Pause,
  Play,
  Plus,
  RefreshCw,
  RotateCcw,
  Send,
  ShieldCheck,
  Stethoscope,
  SkipForward,
  Undo2,
  UserRoundCheck,
  Users,
  Wrench,
  X,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { issueDetailOptions } from "@multica/core/issues/queries";
import { useWorkspacePaths } from "@multica/core/paths";
import {
  useCreateWorkflowSubmission,
  useConfirmWorkflowSubmissionTasks,
  useCreateWorkflowNodeIssue,
  useCreateWorkflowVerdict,
  useChangeWorkflowNodeTask,
  useCancelWorkflowInstance,
  useConfirmWorkflowNode,
  useDecideWorkflowAcceptance,
  usePauseWorkflowInstance,
  useReconcileWorkflowInstance,
  useResolveWorkflowNodeExecutor,
  useResumeWorkflowInstance,
  useTransitionWorkflowNode,
  useUpdateWorkflowInstanceRoles,
  workflowAcceptancesOptions,
  workflowDiagnosticsOptions,
  workflowEventsOptions,
  workflowInstanceIssuesOptions,
  workflowInstanceOptions,
  workflowNodeOptions,
  workflowTemplateOptions,
  type WorkflowNodeInstance,
  type WorkflowNodeTask,
  type WorkflowDiagnostics,
  type WorkflowExecutorResolution,
  type WorkflowEvent,
  type WorkflowSubmission,
  type WorkflowVerdict,
  type WorkflowSubmissionField,
  type WorkflowIssueTemplate,
  type WorkflowRoleAssignment,
  type WorkflowRoleDefinition,
} from "@multica/core/workflows";
import {
  agentListOptions,
  memberListOptions,
  squadListOptions,
} from "@multica/core/workspace/queries";
import { Alert, AlertDescription, AlertTitle } from "@multica/ui/components/ui/alert";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@multica/ui/components/ui/card";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@multica/ui/components/ui/tabs";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { cn } from "@multica/ui/lib/utils";
import { AppLink } from "../navigation";
import { CollectionPageHeader, CollectionPageState } from "../layout/collection-page";
import { useT } from "../i18n";
import { IssueDetail } from "../issues/components";
import { StatusIcon } from "../issues/components/status-icon";
import { PriorityIcon } from "../issues/components/priority-icon";
import { ActorAvatar } from "../common/actor-avatar";
import { WorkflowCanvas } from "./workflow-canvas";
import { WorkflowStatusBadge } from "./workflow-status";
import {
  latestWorkflowAttemptNodes,
  workflowIssuesForScope,
  type WorkflowIssueScope,
} from "./workflow-workbench-state";

function SubmissionFieldInput({
  field,
  value,
  actorOptions,
  onChange,
}: {
  field: WorkflowSubmissionField;
  value: unknown;
  actorOptions: WorkflowActorOption[];
  onChange: (value: unknown) => void;
}) {
  const { t } = useT("workflows");
  if (field.type === "boolean") {
    return (
      <div className="space-y-1.5">
        <Label htmlFor={`submission-${field.key}`}>
          {field.name}
          {field.required && <span className="ml-0.5 text-destructive">*</span>}
        </Label>
        <select
          id={`submission-${field.key}`}
          value={typeof value === "boolean" ? String(value) : ""}
          onChange={(event) => onChange(
            event.target.value === "" ? undefined : event.target.value === "true",
          )}
          className="min-h-11 w-full rounded-lg border border-input bg-background px-3 text-sm"
        >
          <option value="">—</option>
          <option value="true">{t(($) => $.workbench.boolean_true)}</option>
          <option value="false">{t(($) => $.workbench.boolean_false)}</option>
        </select>
      </div>
    );
  }
  if (
    field.type === "member" ||
    field.type === "agent" ||
    field.type === "squad"
  ) {
    return (
      <div className="space-y-1.5">
        <Label htmlFor={`submission-${field.key}`}>
          {field.name}
          {field.required && <span className="ml-0.5 text-destructive">*</span>}
        </Label>
        <select
          id={`submission-${field.key}`}
          value={typeof value === "string" ? value : ""}
          onChange={(event) => onChange(event.target.value || undefined)}
          className="min-h-11 w-full rounded-lg border border-input bg-background px-3 text-sm"
        >
          <option value="">—</option>
          {actorOptions
            .filter((actor) => actor.type === field.type)
            .map((actor) => (
              <option key={actor.id} value={actor.id}>{actor.name}</option>
            ))}
        </select>
      </div>
    );
  }
  return (
    <div className="space-y-1.5">
      <Label htmlFor={`submission-${field.key}`}>
        {field.name}
        {field.required && <span className="ml-0.5 text-destructive">*</span>}
      </Label>
      <Input
        id={`submission-${field.key}`}
        type={field.type === "number" ? "number" : field.type === "date" ? "date" : "text"}
        value={typeof value === "string" || typeof value === "number" ? value : ""}
        className="min-h-11 sm:min-h-8"
        onChange={(event) => {
          if (field.type === "number") {
            onChange(event.target.value === "" ? undefined : Number(event.target.value));
          } else {
            onChange(event.target.value);
          }
        }}
      />
    </div>
  );
}

function submissionFieldValueIsValid(
  field: WorkflowSubmissionField,
  value: unknown,
) {
  if (value === undefined || value === null) return !field.required;
  switch (field.type) {
    case "number":
      return typeof value === "number" && Number.isFinite(value);
    case "boolean":
      return typeof value === "boolean";
    case "text":
    case "date":
    case "member":
    case "agent":
    case "squad":
      return typeof value === "string" && value.trim() !== "";
    default:
      return false;
  }
}

export function SubmissionPanel({
  instanceId,
  node,
  submissions,
  tasks,
  actorOptions,
  canManage,
}: {
  instanceId: string;
  node: WorkflowNodeInstance;
  submissions: WorkflowSubmission[];
  tasks: WorkflowNodeTask[];
  actorOptions: WorkflowActorOption[];
  canManage: boolean;
}) {
  const { t } = useT("workflows");
  const [values, setValues] = useState<Record<string, unknown>>({});
  const [summary, setSummary] = useState("");
  const [sourceIssueId, setSourceIssueId] = useState("");
  const [proposedTitle, setProposedTitle] = useState("");
  const [proposedRequired, setProposedRequired] = useState(false);
  const [proposedTasks, setProposedTasks] = useState<WorkflowIssueTemplate[]>([]);
  const submit = useCreateWorkflowSubmission(instanceId, node.id);
  const confirmTasks = useConfirmWorkflowSubmissionTasks(instanceId, node.id);
  const fields = node.definition.submission_schema?.fields ?? [];
  const submissionPolicy = node.definition.submission_schema?.policy ?? "single";
  const taskScoped = submissionPolicy === "per_required_task" ||
    submissionPolicy === "fan_in";
  const sourceTasks = tasks.filter((task) => task.issue_id);
  const allowsFanOut = node.definition.issue_policy === "dynamic" ||
    node.definition.issue_policy === "fixed_and_dynamic";
  const hasInvalidFields = fields.some((field) =>
    !submissionFieldValueIsValid(field, values[field.key])
  );

  useEffect(() => {
    setValues({});
    setSummary("");
    setSourceIssueId("");
    setProposedTitle("");
    setProposedRequired(false);
    setProposedTasks([]);
  }, [node.id]);

  return (
    <div className="space-y-4">
      {canManage && node.definition.submission_schema &&
        (node.status === "active" || node.status === "waiting" ||
          node.status === "blocked") && (
        <div className="space-y-3 rounded-xl border bg-muted/20 p-4">
          <div className="grid gap-3 sm:grid-cols-2">
            {fields.map((field) => (
              <SubmissionFieldInput
                key={field.key}
                field={field}
                value={values[field.key]}
                actorOptions={actorOptions}
                onChange={(value) =>
                  setValues((current) => ({ ...current, [field.key]: value }))}
              />
            ))}
          </div>
          {taskScoped && (
            <div className="space-y-1.5">
              <Label htmlFor="workflow-submission-source">
                {t(($) => $.workbench.submission_source_task)}
              </Label>
              <select
                id="workflow-submission-source"
                value={sourceIssueId}
                onChange={(event) => setSourceIssueId(event.target.value)}
                className="min-h-11 w-full rounded-lg border border-input bg-background px-3 text-sm"
              >
                <option value="">
                  {t(($) => $.workbench.choose_submission_source)}
                </option>
                {sourceTasks.map((task) => (
                  <option key={task.id} value={task.issue_id!}>
                    {task.definition.title}
                  </option>
                ))}
              </select>
            </div>
          )}
          <div className="space-y-1.5">
            <Label htmlFor="workflow-submission-summary">
              {t(($) => $.workbench.summary)}
            </Label>
            <Textarea
              id="workflow-submission-summary"
              value={summary}
              onChange={(event) => setSummary(event.target.value)}
              rows={3}
            />
          </div>
          {allowsFanOut && (
            <div className="space-y-3 rounded-lg border border-dashed p-3">
              <div>
                <p className="text-sm font-medium">
                  {t(($) => $.workbench.proposed_tasks)}
                </p>
                <p className="mt-1 text-xs text-muted-foreground">
                  {t(($) => $.workbench.proposed_tasks_help)}
                </p>
              </div>
              {proposedTasks.length > 0 && (
                <ul className="space-y-2">
                  {proposedTasks.map((task) => (
                    <li
                      key={task.key}
                      className="flex min-h-11 items-center justify-between gap-2 rounded-md border px-3 text-sm"
                    >
                      <span className="min-w-0 truncate">
                        {task.title}
                        {task.required && (
                          <span className="ml-2 text-xs text-muted-foreground">
                            {t(($) => $.workbench.required)}
                          </span>
                        )}
                      </span>
                      <Button
                        type="button"
                        size="icon-sm"
                        variant="ghost"
                        aria-label={t(($) => $.actions.remove)}
                        onClick={() => setProposedTasks((current) =>
                          current.filter((item) => item.key !== task.key)
                        )}
                      >
                        <X />
                      </Button>
                    </li>
                  ))}
                </ul>
              )}
              <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto_auto] sm:items-center">
                <Input
                  aria-label={t(($) => $.workbench.proposed_task_title)}
                  placeholder={t(($) => $.workbench.proposed_task_title)}
                  value={proposedTitle}
                  className="min-h-11"
                  onChange={(event) => setProposedTitle(event.target.value)}
                />
                <label className="flex min-h-11 items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={proposedRequired}
                    onChange={(event) => setProposedRequired(event.target.checked)}
                  />
                  {t(($) => $.workbench.required)}
                </label>
                <Button
                  type="button"
                  variant="outline"
                  className="min-h-11"
                  disabled={!proposedTitle.trim()}
                  onClick={() => {
                    setProposedTasks((current) => [
                      ...current,
                      {
                        key: `proposed_${crypto.randomUUID().replaceAll("-", "_")}`,
                        title: proposedTitle.trim(),
                        required: proposedRequired,
                        initial_status: "todo",
                        priority: "none",
                      },
                    ]);
                    setProposedTitle("");
                    setProposedRequired(false);
                  }}
                >
                  <Plus />
                  {t(($) => $.actions.add_task)}
                </Button>
              </div>
            </div>
          )}
          <Button
            size="sm"
            className="min-h-11"
            onClick={() => submit.mutate({
              payload: values,
              summary,
              source_issue_id: sourceIssueId || undefined,
              proposed_tasks: proposedTasks,
            }, {
              onSuccess: () => {
                setProposedTasks([]);
                setProposedTitle("");
                setProposedRequired(false);
              },
            })}
            disabled={
              submit.isPending ||
              hasInvalidFields ||
              (taskScoped && !sourceIssueId)
            }
          >
            <Send />
            {t(($) => $.actions.submit)}
          </Button>
          {hasInvalidFields && (
            <p className="text-xs text-muted-foreground">
              {t(($) => $.workbench.submission_required_fields)}
            </p>
          )}
          {submit.isError && (
            <p role="alert" className="text-xs text-destructive">
              {t(($) => $.errors.action_failed)}
            </p>
          )}
        </div>
      )}
      {submissions.length === 0 ? (
        <p className="py-6 text-center text-sm text-muted-foreground">
          {t(($) => $.workbench.no_submission)}
        </p>
      ) : (
        <div className="space-y-3">
          {submissions.map((submission) => (
            <Card key={submission.id} size="sm">
              <CardHeader>
                <CardTitle className="flex items-center justify-between gap-3 text-sm">
                  <span>#{submission.revision}</span>
                  <WorkflowStatusBadge status={submission.status} />
                </CardTitle>
              </CardHeader>
              <CardContent className="space-y-2">
                {submission.summary && (
                  <p className="text-sm">{submission.summary}</p>
                )}
                {submission.source_issue_id && (
                  <p className="text-xs text-muted-foreground">
                    {t(($) => $.workbench.submission_source_task)}:{" "}
                    {tasks.find((task) =>
                      task.issue_id === submission.source_issue_id
                    )?.definition.title ?? submission.source_issue_id}
                  </p>
                )}
                {submission.proposed_tasks.length > 0 && (
                  <div className="space-y-2 rounded-lg border border-dashed p-3">
                    <p className="text-xs font-medium">
                      {t(($) => $.workbench.proposed_tasks)}
                    </p>
                    <ul className="space-y-1 text-xs text-muted-foreground">
                      {submission.proposed_tasks.map((task) => (
                        <li key={task.key}>
                          {task.title}
                          {task.required
                            ? ` · ${t(($) => $.workbench.required)}`
                            : ""}
                        </li>
                      ))}
                    </ul>
                    {canManage && submission.status === "valid" &&
                      !submission.proposed_tasks.every((proposed) =>
                        tasks.some((task) => task.task_key === proposed.key)
                      ) && (
                        <Button
                          type="button"
                          size="sm"
                          className="min-h-11"
                          disabled={confirmTasks.isPending}
                          onClick={() => confirmTasks.mutate(submission.id)}
                        >
                          <Check />
                          {t(($) => $.workbench.confirm_proposed_tasks)}
                        </Button>
                      )}
                  </div>
                )}
                <pre className="max-h-48 overflow-auto rounded-lg bg-muted p-3 text-xs">
                  {JSON.stringify(submission.payload, null, 2)}
                </pre>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}

export function VerdictPanel({
  instanceId,
  node,
  verdicts,
  canRecord,
}: {
  instanceId: string;
  node: WorkflowNodeInstance;
  verdicts: WorkflowVerdict[];
  canRecord: boolean;
}) {
  const { t } = useT("workflows");
  const [result, setResult] = useState<"pass" | "fail" | "blocked">("pass");
  const [reason, setReason] = useState("");
  const [confidence, setConfidence] = useState("");
  const record = useCreateWorkflowVerdict(instanceId, node.id);
  const isOpen = node.status === "active" || node.status === "waiting" ||
    node.status === "blocked";
  const reasonRequired = result === "fail" || result === "blocked";

  useEffect(() => {
    setResult("pass");
    setReason("");
    setConfidence("");
  }, [node.id]);

  return (
    <div className="space-y-4">
      {canRecord && isOpen && node.definition.verdict?.evaluator === "member" && (
        <div className="space-y-3 rounded-xl border bg-muted/20 p-4">
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="workflow-verdict-result">
                {t(($) => $.workbench.verdict_result)}
              </Label>
              <select
                id="workflow-verdict-result"
                value={result}
                onChange={(event) =>
                  setResult(event.target.value as "pass" | "fail" | "blocked")}
                className="min-h-11 w-full rounded-lg border border-input bg-background px-3 text-sm"
              >
                <option value="pass">{t(($) => $.status.pass)}</option>
                <option value="fail">{t(($) => $.status.fail)}</option>
                <option value="blocked">{t(($) => $.status.blocked)}</option>
              </select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="workflow-verdict-confidence">
                {t(($) => $.workbench.verdict_confidence)}
              </Label>
              <Input
                id="workflow-verdict-confidence"
                type="number"
                min={0}
                max={1}
                step={0.05}
                value={confidence}
                className="min-h-11"
                onChange={(event) => setConfidence(event.target.value)}
              />
            </div>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="workflow-verdict-reason">
              {t(($) => $.workbench.verdict_reason)}
              {reasonRequired && <span className="ml-0.5 text-destructive">*</span>}
            </Label>
            <Textarea
              id="workflow-verdict-reason"
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              rows={3}
            />
          </div>
          <Button
            className="min-h-11"
            disabled={record.isPending || (reasonRequired && !reason.trim())}
            onClick={() => record.mutate({
              result,
              reason: reason.trim() || undefined,
              confidence: confidence === "" ? undefined : Number(confidence),
            }, {
              onSuccess: () => {
                setReason("");
                setConfidence("");
              },
            })}
          >
            <FileCheck2 />
            {t(($) => $.actions.record_verdict)}
          </Button>
          {record.isError && (
            <p role="alert" className="text-xs text-destructive">
              {t(($) => $.errors.action_failed)}
            </p>
          )}
        </div>
      )}

      {verdicts.length === 0 ? (
        <p className="py-6 text-center text-sm text-muted-foreground">
          {t(($) => $.workbench.no_verdict)}
        </p>
      ) : (
        <div className="space-y-3">
          {verdicts.map((verdict) => (
            <Card key={verdict.id} size="sm">
              <CardHeader>
                <CardTitle className="flex items-center justify-between gap-3 text-sm">
                  <span>
                    #{verdict.revision}
                    <span className="ml-2 font-normal text-muted-foreground">
                      {verdict.evaluator_type === "agent"
                        ? t(($) => $.workbench.agent_suggestion)
                        : t(($) => $.workbench.official_verdict)}
                    </span>
                  </span>
                  <WorkflowStatusBadge status={verdict.result} />
                </CardTitle>
              </CardHeader>
              <CardContent>
                {verdict.reason && <p>{verdict.reason}</p>}
                {verdict.confidence !== null && (
                  <p className="mt-2 text-xs text-muted-foreground">
                    {t(($) => $.workbench.verdict_confidence)}:{" "}
                    {Math.round(verdict.confidence * 100)}%
                  </p>
                )}
                {verdict.evidence.length > 0 && (
                  <pre className="mt-3 overflow-auto rounded-lg bg-muted p-3 text-xs">
                    {JSON.stringify(verdict.evidence, null, 2)}
                  </pre>
                )}
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}

interface WorkflowActorOption {
  type: "member" | "agent" | "squad";
  id: string;
  name: string;
}

export function RoleSetupPanel({
  instanceId,
  roles,
  currentAssignments,
  actorOptions,
  canConfigure,
}: {
  instanceId: string;
  roles: WorkflowRoleDefinition[];
  currentAssignments: WorkflowRoleAssignment[];
  actorOptions: WorkflowActorOption[];
  canConfigure: boolean;
}) {
  const { t } = useT("workflows");
  const [assignments, setAssignments] = useState<Record<string, string>>({});
  const updateRoles = useUpdateWorkflowInstanceRoles(instanceId);

  useEffect(() => {
    setAssignments(Object.fromEntries(currentAssignments.map((assignment) => [
      assignment.role_key,
      `${assignment.actor_type}:${assignment.actor_id}`,
    ])));
  }, [currentAssignments]);

  const selectedAssignments = roles.flatMap((role) => {
    const value = assignments[role.key] ?? "";
    const separator = value.indexOf(":");
    if (separator < 1 || separator === value.length - 1) return [];
    const actorType = value.slice(0, separator);
    if (
      actorType !== "member" &&
      actorType !== "agent" &&
      actorType !== "squad"
    ) {
      return [];
    }
    return [{
      role_key: role.key,
      actor_type: actorType,
      actor_id: value.slice(separator + 1),
      source: "user_selected",
    }];
  });
  const missingRequired = roles.some((role) =>
    role.required &&
    !selectedAssignments.some((assignment) => assignment.role_key === role.key)
  );

  return (
    <section
      aria-labelledby="workflow-role-setup-title"
      className="space-y-4 rounded-xl border border-amber-500/30 bg-amber-500/5 p-4"
    >
      <div>
        <h2
          id="workflow-role-setup-title"
          className="flex items-center gap-2 text-sm font-medium"
        >
          <Users className="size-4 text-amber-600" />
          {t(($) => $.workbench.role_setup)}
        </h2>
        <p className="mt-1 text-xs text-muted-foreground">
          {canConfigure
            ? t(($) => $.workbench.role_setup_help)
            : t(($) => $.workbench.role_setup_waiting)}
        </p>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        {roles.map((role) => {
          const options = actorOptions.filter((actor) =>
            role.allowed_actor_types.includes(actor.type)
          );
          return (
            <div key={role.key} className="space-y-1.5">
              <Label htmlFor={`workflow-setup-role-${role.key}`}>
                {role.name}
                {role.required && (
                  <span className="ml-1 text-destructive" aria-hidden>*</span>
                )}
              </Label>
              <select
                id={`workflow-setup-role-${role.key}`}
                value={assignments[role.key] ?? ""}
                disabled={!canConfigure || updateRoles.isPending}
                required={role.required}
                aria-required={role.required}
                onChange={(event) => setAssignments((current) => ({
                  ...current,
                  [role.key]: event.target.value,
                }))}
                className="min-h-11 w-full rounded-lg border border-input bg-background px-3 text-sm disabled:opacity-60"
              >
                <option value="">
                  {role.required
                    ? t(($) => $.start.choose_actor)
                    : t(($) => $.start.unassigned)}
                </option>
                {options.map((actor) => (
                  <option
                    key={`${actor.type}:${actor.id}`}
                    value={`${actor.type}:${actor.id}`}
                  >
                    {actor.name} · {t(($) => $.start.actor_type[actor.type])}
                  </option>
                ))}
              </select>
              {role.required && options.length === 0 && (
                <p className="text-xs text-destructive">
                  {t(($) => $.start.no_eligible_actor)}
                </p>
              )}
            </div>
          );
        })}
      </div>
      {canConfigure && (
        <Button
          className="min-h-11"
          disabled={
            missingRequired ||
            selectedAssignments.length === 0 ||
            updateRoles.isPending
          }
          onClick={() => updateRoles.mutate(selectedAssignments)}
        >
          <Users />
          {t(($) => $.actions.save_roles)}
        </Button>
      )}
      {updateRoles.isError && (
        <p role="alert" className="text-sm text-destructive">
          {t(($) => $.errors.action_failed)}
        </p>
      )}
    </section>
  );
}

export function WorkflowTaskCard({
  instanceId,
  nodeId,
  task,
  resolution,
  actorOptions,
  canManage,
  canAdmin,
}: {
  instanceId: string;
  nodeId: string;
  task: WorkflowNodeTask;
  resolution?: WorkflowExecutorResolution;
  actorOptions: WorkflowActorOption[];
  canManage: boolean;
  canAdmin: boolean;
}) {
  const { t } = useT("workflows");
  const [actorValue, setActorValue] = useState("");
  const [reason, setReason] = useState("");
  const resolveExecutor = useResolveWorkflowNodeExecutor(instanceId, nodeId);
  const changeTask = useChangeWorkflowNodeTask(instanceId, nodeId);
  const selectedActor = actorOptions.find(
    (actor) => `${actor.type}:${actor.id}` === actorValue,
  );
  const assignedActor = resolution?.status === "resolved" &&
    resolution.actor_type && resolution.actor_id
    ? actorOptions.find(
      (actor) =>
        actor.type === resolution.actor_type && actor.id === resolution.actor_id,
    )
    : undefined;
  const needsExecutor = !task.executor_resolution_id;
  const isRecoverable = task.materialization_status === "failed" ||
    task.materialization_status === "materializing";

  return (
    <article className="space-y-3 rounded-xl border bg-surface px-4 py-3">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 className="truncate text-sm font-medium">
            {task.definition.title}
          </h3>
          <p className="mt-0.5 text-xs text-muted-foreground">
            {task.required
              ? t(($) => $.workbench.required)
              : t(($) => $.workbench.optional)}
            <span aria-hidden="true"> · </span>
            {task.source === "dynamic"
              ? t(($) => $.workbench.dynamic_task)
              : t(($) => $.workbench.template_task)}
          </p>
        </div>
        <WorkflowStatusBadge status={task.materialization_status} />
      </div>

      {assignedActor && (
        <p className="flex items-center gap-2 text-xs text-muted-foreground">
          <UserRoundCheck className="size-3.5" />
          {t(($) => $.workbench.executor)}: {assignedActor.name}
        </p>
      )}
      {resolution && (
        <p className="text-xs text-muted-foreground">
          {t(($) => $.workbench.executor_resolution)}: {resolution.strategy}
          {resolution.reason ? ` · ${resolution.reason}` : ""}
        </p>
      )}

      {needsExecutor && (
        <div className="space-y-2 rounded-lg border border-amber-500/30 bg-amber-500/5 p-3">
          <div>
            <p className="text-xs font-medium text-amber-800 dark:text-amber-200">
              {t(($) => $.workbench.executor_required)}
            </p>
          </div>
          {canManage && (
            <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto]">
              <select
                aria-label={t(($) => $.workbench.choose_executor)}
                value={actorValue}
                onChange={(event) => setActorValue(event.target.value)}
                className="min-h-11 w-full rounded-lg border border-input bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                <option value="">{t(($) => $.workbench.choose_executor)}</option>
                {actorOptions.map((actor) => (
                  <option
                    key={`${actor.type}:${actor.id}`}
                    value={`${actor.type}:${actor.id}`}
                  >
                    {actor.name} · {t(($) => $.start.actor_type[actor.type])}
                  </option>
                ))}
              </select>
              <Button
                className="min-h-11"
                disabled={!selectedActor || resolveExecutor.isPending}
                onClick={() => {
                  if (!selectedActor) return;
                  resolveExecutor.mutate({
                    task_id: task.id,
                    actor_type: selectedActor.type,
                    actor_id: selectedActor.id,
                    reason: t(($) => $.workbench.manual_executor_reason),
                  });
                }}
              >
                <UserRoundCheck />
                {t(($) => $.actions.assign)}
              </Button>
            </div>
          )}
        </div>
      )}

      {task.last_error && (
        <Alert variant="destructive">
          <AlertCircle />
          <AlertTitle>{t(($) => $.workbench.materialization_failed)}</AlertTitle>
          <AlertDescription>{task.last_error}</AlertDescription>
        </Alert>
      )}

      {canAdmin && (isRecoverable || task.issue_id) && (
        <details className="group">
          <summary className="cursor-pointer text-xs text-muted-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring">
            {t(($) => $.workbench.recovery_actions)}
          </summary>
          <div className="mt-3 grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto_auto]">
            <Input
              aria-label={t(($) => $.workbench.action_reason)}
              placeholder={t(($) => $.workbench.action_reason)}
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              className="min-h-11"
            />
            {isRecoverable && (
              <Button
                variant="outline"
                className="min-h-11"
                disabled={!reason.trim() || changeTask.isPending}
                onClick={() => changeTask.mutate({
                  taskId: task.id,
                  action: "retry",
                  reason: reason.trim(),
                })}
              >
                <RefreshCw />
                {t(($) => $.actions.retry)}
              </Button>
            )}
            {task.issue_id && (
              <Button
                variant="outline"
                className="min-h-11"
                disabled={!reason.trim() || changeTask.isPending}
                onClick={() => changeTask.mutate({
                  taskId: task.id,
                  action: "detach",
                  reason: reason.trim(),
                })}
              >
                <X />
                {t(($) => $.actions.detach)}
              </Button>
            )}
          </div>
        </details>
      )}

      {(resolveExecutor.isError || changeTask.isError) && (
        <p role="alert" className="text-xs text-destructive">
          {t(($) => $.errors.action_failed)}
        </p>
      )}
    </article>
  );
}

function DynamicIssuePanel({
  instanceId,
  node,
  actorOptions,
  canManage,
}: {
  instanceId: string;
  node: WorkflowNodeInstance;
  actorOptions: WorkflowActorOption[];
  canManage: boolean;
}) {
  const { t } = useT("workflows");
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [actorValue, setActorValue] = useState("");
  const [required, setRequired] = useState(false);
  const createIssue = useCreateWorkflowNodeIssue(instanceId, node.id);
  const policy = node.definition.issue_policy ?? "none";
  const isOpen = node.status === "active" || node.status === "waiting" ||
    node.status === "blocked";
  if (!canManage || !isOpen ||
    (policy !== "dynamic" && policy !== "fixed_and_dynamic")) {
    return null;
  }
  const selectedActor = actorOptions.find(
    (actor) => `${actor.type}:${actor.id}` === actorValue,
  );

  return (
    <div className="space-y-3 rounded-xl border border-dashed bg-muted/15 p-4">
      <div>
        <h3 className="flex items-center gap-2 text-sm font-medium">
          <Plus className="size-4" />
          {t(($) => $.workbench.add_dynamic_issue)}
        </h3>
        <p className="mt-1 text-xs text-muted-foreground">
          {t(($) => $.workbench.add_dynamic_issue_help)}
        </p>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1.5">
          <Label htmlFor="workflow-dynamic-issue-title">
            {t(($) => $.workbench.issue_title)}
          </Label>
          <Input
            id="workflow-dynamic-issue-title"
            className="min-h-11"
            value={title}
            onChange={(event) => setTitle(event.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="workflow-dynamic-issue-executor">
            {t(($) => $.workbench.executor)}
          </Label>
          <select
            id="workflow-dynamic-issue-executor"
            value={actorValue}
            onChange={(event) => setActorValue(event.target.value)}
            className="min-h-11 w-full rounded-lg border border-input bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <option value="">{t(($) => $.workbench.auto_resolve)}</option>
            {actorOptions.map((actor) => (
              <option
                key={`${actor.type}:${actor.id}`}
                value={`${actor.type}:${actor.id}`}
              >
                {actor.name} · {actor.type}
              </option>
            ))}
          </select>
        </div>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="workflow-dynamic-issue-description">
          {t(($) => $.workbench.issue_description)}
        </Label>
        <Textarea
          id="workflow-dynamic-issue-description"
          value={description}
          onChange={(event) => setDescription(event.target.value)}
          rows={2}
        />
      </div>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <label className="flex min-h-11 items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={required}
            onChange={(event) => setRequired(event.target.checked)}
          />
          {t(($) => $.workbench.required)}
        </label>
        <Button
          className="min-h-11"
          disabled={!title.trim() || createIssue.isPending}
          onClick={() => createIssue.mutate({
            title: title.trim(),
            description: description.trim() || undefined,
            assignee_type: selectedActor?.type,
            assignee_id: selectedActor?.id,
            required,
          }, {
            onSuccess: () => {
              setTitle("");
              setDescription("");
              setActorValue("");
              setRequired(false);
            },
          })}
        >
          <Plus />
          {t(($) => $.actions.create_issue)}
        </Button>
      </div>
      {createIssue.isError && (
        <p role="alert" className="text-xs text-destructive">
          {t(($) => $.errors.action_failed)}
        </p>
      )}
    </div>
  );
}

function ConfirmationPanel({
  instanceId,
  node,
  confirmations,
  actorName,
  canConfirm,
}: {
  instanceId: string;
  node: WorkflowNodeInstance;
  confirmations: Array<{
    id: string;
    member_id: string;
    decision: string;
    comment: string;
  }>;
  actorName: (type: "member", id: string) => string;
  canConfirm: boolean;
}) {
  const { t } = useT("workflows");
  const [comment, setComment] = useState("");
  const confirm = useConfirmWorkflowNode(instanceId, node.id);
  const confirmation = node.definition.completion?.confirmation;
  const policy = typeof confirmation === "string" ? confirmation : "none";
  const isOpen = node.status === "active" || node.status === "waiting" ||
    node.status === "blocked";
  if (policy === "none") return null;

  return (
    <div className="space-y-3 rounded-xl border bg-muted/15 p-4">
      <div>
        <h3 className="flex items-center gap-2 text-sm font-medium">
          <ShieldCheck className="size-4" />
          {t(($) => $.workbench.confirmation)}
        </h3>
        <p className="mt-1 text-xs text-muted-foreground">
          {t(($) => $.workbench.confirmation_policy)}: {policy}
        </p>
      </div>
      {confirmations.length > 0 && (
        <ul className="space-y-2">
          {confirmations.map((item) => (
            <li key={item.id} className="flex items-start justify-between gap-3 text-xs">
              <span>
                {actorName("member", item.member_id)}
                {item.comment && (
                  <span className="ml-2 text-muted-foreground">{item.comment}</span>
                )}
              </span>
              <WorkflowStatusBadge status={item.decision} />
            </li>
          ))}
        </ul>
      )}
      {isOpen && canConfirm && (
        <>
          <Textarea
            aria-label={t(($) => $.workbench.confirmation_comment)}
            placeholder={t(($) => $.workbench.confirmation_comment)}
            value={comment}
            onChange={(event) => setComment(event.target.value)}
            rows={2}
          />
          <div className="flex flex-wrap gap-2">
            <Button
              className="min-h-11"
              disabled={confirm.isPending}
              onClick={() => confirm.mutate({
                decision: "approved",
                comment: comment.trim(),
              })}
            >
              <Check />
              {t(($) => $.actions.confirm)}
            </Button>
            <Button
              variant="outline"
              className="min-h-11"
              disabled={confirm.isPending}
              onClick={() => confirm.mutate({
                decision: "rejected",
                comment: comment.trim(),
              })}
            >
              <X />
              {t(($) => $.actions.reject)}
            </Button>
          </div>
        </>
      )}
      {confirm.isError && (
        <p role="alert" className="text-xs text-destructive">
          {t(($) => $.errors.action_failed)}
        </p>
      )}
    </div>
  );
}

function NodeTransitionPanel({
  instanceId,
  node,
  canManage,
  canAdmin,
  instanceRunning,
}: {
  instanceId: string;
  node: WorkflowNodeInstance;
  canManage: boolean;
  canAdmin: boolean;
  instanceRunning: boolean;
}) {
  const { t } = useT("workflows");
  const [reason, setReason] = useState("");
  const transition = useTransitionWorkflowNode(instanceId, node.id);
  const open = node.status === "active" || node.status === "waiting" ||
    node.status === "blocked";
  const completion = node.definition.completion ?? {};
  const manualCompletion = node.definition.kind === "activity" &&
    node.definition.activity_mode !== "acceptance" &&
    !node.definition.submission_schema &&
    !node.definition.verdict &&
    completion.submission_required !== true &&
    (!completion.verdict_required || completion.verdict_required === "none") &&
    (!completion.confirmation || completion.confirmation === "none") &&
    !(node.definition.issue_templates ?? []).some((task) => task.required);
  const canComplete = open && (
    (manualCompletion && canManage) || canAdmin
  );
  const canRollback = canManage && instanceRunning &&
    (node.status === "completed" || node.status === "skipped");
  if (!canComplete && (!canAdmin || !open) && !canRollback) return null;

  return (
    <div className="space-y-3 rounded-xl border bg-muted/15 p-4">
      <div>
        <h3 className="flex items-center gap-2 text-sm font-medium">
          <Wrench className="size-4" />
          {manualCompletion
            ? t(($) => $.workbench.complete_activity)
            : t(($) => $.workbench.admin_actions)}
        </h3>
        <p className="mt-1 text-xs text-muted-foreground">
          {manualCompletion
            ? t(($) => $.workbench.complete_activity_help)
            : t(($) => $.workbench.admin_actions_help)}
        </p>
      </div>
      <Textarea
        aria-label={t(($) => $.workbench.action_reason)}
        placeholder={t(($) => $.workbench.action_reason)}
        value={reason}
        onChange={(event) => setReason(event.target.value)}
        rows={2}
      />
      <div className="flex flex-wrap gap-2">
        {canComplete && (
          <>
            <Button
              className="min-h-11"
              disabled={!reason.trim() || transition.isPending}
              onClick={() => transition.mutate({
                action: "complete",
                reason: reason.trim(),
              })}
            >
              <Check />
              {manualCompletion
                ? t(($) => $.actions.complete)
                : t(($) => $.actions.force_complete)}
            </Button>
          </>
        )}
        {canAdmin && open && (
          <>
            <Button
              variant="outline"
              className="min-h-11"
              disabled={!reason.trim() || transition.isPending}
              onClick={() => transition.mutate({
                action: "skip",
                reason: reason.trim(),
              })}
            >
              <SkipForward />
              {t(($) => $.actions.skip)}
            </Button>
          </>
        )}
        {canRollback && (
          <Button
            variant="outline"
            className="min-h-11"
            disabled={!reason.trim() || transition.isPending}
            onClick={() => transition.mutate({
              action: "rollback",
              reason: reason.trim(),
            })}
          >
            <Undo2 />
            {t(($) => $.actions.rollback)}
          </Button>
        )}
      </div>
      {transition.isError && (
        <p role="alert" className="text-xs text-destructive">
          {t(($) => $.errors.action_failed)}
        </p>
      )}
    </div>
  );
}

function WorkflowHistoryPanel({
  events,
  loading,
}: {
  events: WorkflowEvent[];
  loading: boolean;
}) {
  const { t } = useT("workflows");
  if (loading) {
    return (
      <div className="space-y-2" aria-busy="true">
        <Skeleton className="h-16 rounded-xl" />
        <Skeleton className="h-16 rounded-xl" />
      </div>
    );
  }
  if (events.length === 0) {
    return (
      <p className="py-6 text-center text-sm text-muted-foreground">
        {t(($) => $.workbench.no_events)}
      </p>
    );
  }
  return (
    <ol className="space-y-2">
      {events.map((event) => (
        <li key={event.id} className="rounded-xl border bg-surface px-4 py-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <p className="font-mono text-xs font-medium">{event.event_type}</p>
            <time
              dateTime={event.created_at}
              className="text-xs text-muted-foreground"
            >
              {event.created_at
                ? new Date(event.created_at).toLocaleString()
                : t(($) => $.workbench.unknown_time)}
            </time>
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            {event.actor_type}
            {event.actor_id ? ` · ${event.actor_id}` : ""}
          </p>
          {Object.keys(event.payload).length > 0 && (
            <details className="mt-2">
              <summary className="cursor-pointer text-xs text-muted-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring">
                {t(($) => $.workbench.event_payload)}
              </summary>
              <pre className="mt-2 max-h-48 overflow-auto rounded-lg bg-muted p-3 text-xs">
                {JSON.stringify(event.payload, null, 2)}
              </pre>
            </details>
          )}
        </li>
      ))}
    </ol>
  );
}

function WorkflowDiagnosticsPanel({
  diagnostics,
}: {
  diagnostics?: WorkflowDiagnostics;
}) {
  const { t } = useT("workflows");
  if (!diagnostics) {
    return (
      <div className="space-y-2" aria-busy="true">
        <Skeleton className="h-16 rounded-xl" />
        <Skeleton className="h-28 rounded-xl" />
      </div>
    );
  }
  const failedTasks = diagnostics.nodes.flatMap((node) =>
    node.tasks
      .filter((task) => task.last_error)
      .map((task) => ({
        node: node.node.name,
        task: task.definition.title,
        error: task.last_error,
      }))
  );
  const unresolved = diagnostics.nodes.flatMap((node) =>
    node.executor_resolutions
      .filter((resolution) => resolution.status !== "resolved")
      .map((resolution) => ({
        node: node.node.name,
        reason: resolution.reason,
      }))
  );

  return (
    <div className="space-y-4">
      <dl className="grid gap-3 sm:grid-cols-3">
        <div className="rounded-xl border bg-surface p-3">
          <dt className="text-xs text-muted-foreground">
            {t(($) => $.workbench.revision)}
          </dt>
          <dd className="mt-1 text-lg font-medium">{diagnostics.instance_revision}</dd>
        </div>
        <div className="rounded-xl border bg-surface p-3">
          <dt className="text-xs text-muted-foreground">
            {t(($) => $.workbench.failed_tasks)}
          </dt>
          <dd className="mt-1 text-lg font-medium">{failedTasks.length}</dd>
        </div>
        <div className="rounded-xl border bg-surface p-3">
          <dt className="text-xs text-muted-foreground">
            {t(($) => $.workbench.sweeper_anomalies)}
          </dt>
          <dd className="mt-1 text-lg font-medium">
            {diagnostics.recent_sweeper_events.length}
          </dd>
        </div>
      </dl>
      {(failedTasks.length > 0 || unresolved.length > 0) && (
        <div className="space-y-2">
          {failedTasks.map((item, index) => (
            <Alert variant="destructive" key={`${item.node}-${item.task}-${index}`}>
              <AlertCircle />
              <AlertTitle>{item.node} · {item.task}</AlertTitle>
              <AlertDescription>{item.error}</AlertDescription>
            </Alert>
          ))}
          {unresolved.map((item, index) => (
            <Alert key={`${item.node}-${index}`}>
              <UserRoundCheck />
              <AlertTitle>{item.node}</AlertTitle>
              <AlertDescription>
                {item.reason || t(($) => $.workbench.executor_required)}
              </AlertDescription>
            </Alert>
          ))}
        </div>
      )}
      {failedTasks.length === 0 && unresolved.length === 0 &&
        diagnostics.recent_sweeper_events.length === 0 && (
          <div className="rounded-xl border border-dashed p-6 text-center text-sm text-muted-foreground">
            {t(($) => $.workbench.no_diagnostic_problems)}
          </div>
        )}
    </div>
  );
}

function WorkflowCancelPanel({
  instanceId,
  status,
}: {
  instanceId: string;
  status: string;
}) {
  const { t } = useT("workflows");
  const [reason, setReason] = useState("");
  const cancel = useCancelWorkflowInstance(instanceId);
  if (status === "completed" || status === "cancelled") return null;
  return (
    <details className="rounded-xl border border-destructive/25 bg-destructive/5 p-4">
      <summary className="cursor-pointer text-sm font-medium text-destructive outline-none focus-visible:ring-2 focus-visible:ring-ring">
        {t(($) => $.workbench.cancel_workflow)}
      </summary>
      <div className="mt-3 space-y-3">
        <p className="text-xs text-muted-foreground">
          {t(($) => $.workbench.cancel_workflow_help)}
        </p>
        <Textarea
          aria-label={t(($) => $.workbench.action_reason)}
          placeholder={t(($) => $.workbench.action_reason)}
          value={reason}
          onChange={(event) => setReason(event.target.value)}
          rows={2}
        />
        <Button
          variant="destructive"
          className="min-h-11"
          disabled={!reason.trim() || cancel.isPending}
          onClick={() => cancel.mutate(reason.trim())}
        >
          <X />
          {t(($) => $.actions.cancel_workflow)}
        </Button>
        {cancel.isError && (
          <p role="alert" className="text-xs text-destructive">
            {t(($) => $.errors.action_failed)}
          </p>
        )}
      </div>
    </details>
  );
}

export function AcceptancePanel({
  instanceId,
  node,
  targets,
  pending,
  canDecide,
}: {
  instanceId: string;
  node: WorkflowNodeInstance;
  targets: Array<{ value: string; label: string }>;
  pending: boolean;
  canDecide: boolean;
}) {
  const { t } = useT("workflows");
  const [reason, setReason] = useState("");
  const [target, setTarget] = useState(targets[0]?.value ?? "");
  const decide = useDecideWorkflowAcceptance(instanceId);

  useEffect(() => {
    setTarget(targets[0]?.value ?? "");
    setReason("");
  }, [node.id, targets]);

  if (!pending) {
    return (
      <p className="py-6 text-center text-sm text-muted-foreground">
        {t(($) => $.workbench.waiting)}
      </p>
    );
  }
  if (!canDecide) {
    return (
      <p className="py-6 text-center text-sm text-muted-foreground">
        {t(($) => $.workbench.awaiting_approver)}
      </p>
    );
  }

  return (
    <div className="space-y-4 rounded-xl border bg-muted/20 p-4">
      <div className="flex flex-wrap gap-2">
        <Button
          className="min-h-11"
          onClick={() => decide.mutate({ status: "approved" })}
          disabled={decide.isPending}
        >
          <Check />
          {t(($) => $.actions.approve)}
        </Button>
      </div>
      <div className="grid gap-3 border-t pt-4 sm:grid-cols-[1fr_220px_auto] sm:items-end">
        <div className="space-y-1.5">
          <Label htmlFor="acceptance-reason">
            {t(($) => $.workbench.reason)}
          </Label>
          <Input
            id="acceptance-reason"
            value={reason}
            className="min-h-11 sm:min-h-8"
            onChange={(event) => setReason(event.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="acceptance-target">
            {t(($) => $.workbench.rework_target)}
          </Label>
          <select
            id="acceptance-target"
            value={target}
            onChange={(event) => setTarget(event.target.value)}
            className="min-h-11 w-full rounded-lg border border-input bg-background px-3 text-sm"
          >
            {targets.map((item) => (
              <option key={item.value} value={item.value}>{item.label}</option>
            ))}
          </select>
        </div>
        <Button
          variant="outline"
          className="min-h-11"
          onClick={() => {
            if (!reason.trim() || !target) return;
            decide.mutate({
              status: "changes_requested",
              reason: reason.trim(),
              rework_target_node_key: target,
            });
          }}
          disabled={decide.isPending || !reason.trim() || !target}
        >
          <RotateCcw />
          {t(($) => $.actions.request_changes)}
        </Button>
      </div>
    </div>
  );
}

export function WorkflowWorkbench({ instanceId }: { instanceId: string }) {
  const { t } = useT("workflows");
  const wsId = useWorkspaceId();
  const p = useWorkspacePaths();
  const userId = useAuthStore((state) => state.user?.id);
  const [selectedNodeId, setSelectedNodeId] = useState("");
  const [issueScope, setIssueScope] = useState<WorkflowIssueScope>("current");
  const [hostDetailsOpen, setHostDetailsOpen] = useState(false);

  const detailQuery = useQuery(workflowInstanceOptions(wsId, instanceId));
  const instance = detailQuery.data?.instance;
  const nodes = useMemo(
    () => latestWorkflowAttemptNodes(detailQuery.data?.nodes ?? []),
    [detailQuery.data?.nodes],
  );
  useEffect(() => {
    if (nodes.length === 0) return;
    if (nodes.some((node) => node.id === selectedNodeId)) return;
    const current = nodes.find((node) =>
      node.status === "active" || node.status === "waiting" ||
      node.status === "blocked"
    );
    setSelectedNodeId((current ?? nodes[0])!.id);
  }, [nodes, selectedNodeId]);

  const selectedNode = nodes.find((node) => node.id === selectedNodeId);
  const nodeQuery = useQuery(workflowNodeOptions(wsId, selectedNodeId));
  const issuesQuery = useQuery(workflowInstanceIssuesOptions(wsId, instanceId));
  const acceptancesQuery = useQuery(workflowAcceptancesOptions(wsId, instanceId));
  const templateQuery = useQuery({
    ...workflowTemplateOptions(wsId, instance?.template_id ?? ""),
    enabled: Boolean(instance?.template_id),
  });
  const hostIssueQuery = useQuery({
    ...issueDetailOptions(wsId, instance?.host_issue_id ?? ""),
    enabled: Boolean(instance?.host_issue_id),
  });
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: squads = [] } = useQuery(squadListOptions(wsId));
  const currentMember = members.find((member) => member.user_id === userId);
  const canAdmin = currentMember?.role === "owner" ||
    currentMember?.role === "admin";
  const actorOptions = useMemo<WorkflowActorOption[]>(() => [
    ...members.map((member) => ({
      type: "member" as const,
      id: member.user_id,
      name: member.name,
    })),
    ...agents
      .filter((agent) => !agent.archived_at)
      .map((agent) => ({
        type: "agent" as const,
        id: agent.id,
        name: agent.name,
      })),
    ...squads
      .filter((squad) => !squad.archived_at)
      .map((squad) => ({
        type: "squad" as const,
        id: squad.id,
        name: squad.name,
      })),
  ], [agents, members, squads]);
  const actorName = (
    type: "member" | "agent" | "squad",
    id: string,
  ) => actorOptions.find((actor) => actor.type === type && actor.id === id)?.name ??
    id;
  const canManageSelectedNode = canAdmin ||
    Boolean(
      userId && nodeQuery.data?.participants.some(
        (participant) =>
          participant.role === "owner" &&
          participant.actor_type === "member" &&
          participant.actor_id === userId,
      ),
    );
  const eventsQuery = useQuery(workflowEventsOptions(wsId, instanceId));
  const diagnosticsQuery = useQuery(
    workflowDiagnosticsOptions(wsId, instanceId, canAdmin),
  );

  const reconcile = useReconcileWorkflowInstance(instanceId);
  const pause = usePauseWorkflowInstance(instanceId);
  const resume = useResumeWorkflowInstance(instanceId);
  const visibleIssues = workflowIssuesForScope(
    issuesQuery.data?.issues ?? [],
    detailQuery.data?.tasks ?? [],
    selectedNodeId,
    issueScope,
  );
  const templateVersion = templateQuery.data?.versions.find(
    (version) => version.id === instance?.template_version_id,
  );
  const acceptanceApproverRole =
    templateVersion?.definition.acceptance.approver_role;
  const canDecideAcceptance = Boolean(
    canAdmin ||
      (userId && acceptanceApproverRole &&
        detailQuery.data?.role_assignments.some((assignment) =>
          assignment.role_key === acceptanceApproverRole &&
          assignment.actor_type === "member" &&
          assignment.actor_id === userId
        )),
  );
  const canConfigureRoles = Boolean(
    canAdmin ||
      (userId && instance?.started_by_type === "member" &&
        instance.started_by_id === userId),
  );
  const confirmationPolicy =
    selectedNode?.definition.completion?.confirmation ?? "none";
  const isSelectedNodeMember = Boolean(
    userId && nodeQuery.data?.participants.some((participant) =>
      participant.actor_type === "member" &&
      participant.actor_id === userId
    ),
  );
  const isSelectedNodeOwner = Boolean(
    userId && nodeQuery.data?.participants.some((participant) =>
      participant.role === "owner" &&
      participant.actor_type === "member" &&
      participant.actor_id === userId
    ),
  );
  const canConfirmSelectedNode = Boolean(currentMember) && (
    confirmationPolicy === "member_any" ||
    (confirmationPolicy === "member_all" && isSelectedNodeMember) ||
    ((confirmationPolicy === "owner_any" ||
      confirmationPolicy === "owner_all") && isSelectedNodeOwner) ||
    (confirmationPolicy === "admin_only" && Boolean(canAdmin))
  );
  const reworkTargetKeys = templateVersion?.definition.acceptance.rework_targets ?? [];
  const targetItems = reworkTargetKeys.map((key) => ({
    value: key,
    label: nodes.find((node) => node.node_key === key)?.name ?? key,
  }));
  const latestAcceptance = acceptancesQuery.data?.acceptances[0];
  const hasSubmissionPanel = Boolean(
    selectedNode?.definition.submission_schema &&
    selectedNode.definition.submission_schema.policy !== "none",
  );
  const hasVerdictPanel = Boolean(selectedNode?.definition.verdict?.evaluator);

  if (detailQuery.isLoading) {
    return (
      <div className="space-y-4 p-5">
        <Skeleton className="h-12 rounded-xl" />
        <Skeleton className="h-36 rounded-xl" />
        <Skeleton className="h-96 rounded-xl" />
      </div>
    );
  }
  if (detailQuery.isError || !instance) {
    return (
      <CollectionPageState
        icon={AlertCircle}
        title={t(($) => $.errors.not_found)}
        tone="destructive"
        role="alert"
      />
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <CollectionPageHeader
        icon={GitBranch}
        title={hostIssueQuery.data?.title ?? t(($) => $.workbench.title)}
        description={(
          <span className="flex flex-wrap items-center gap-2">
            <span>{hostIssueQuery.data?.identifier}</span>
            {templateVersion && (
              <AppLink
                href={p.workflowTemplate(instance.template_id)}
                className="rounded-sm underline-offset-4 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                {t(($) => $.workbench.template_version, {
                  name: templateQuery.data?.template.name ?? instance.template_name,
                  version: templateVersion.version,
                })}
              </AppLink>
            )}
          </span>
        )}
        actions={(
          <>
            {hostIssueQuery.data && (
              <span
                className="inline-flex items-center gap-1 text-xs text-muted-foreground"
                aria-label={t(($) => $.workbench.host_priority, {
                  priority: hostIssueQuery.data.priority,
                })}
              >
                <PriorityIcon priority={hostIssueQuery.data.priority} />
                <span className="hidden md:inline">
                  {hostIssueQuery.data.priority}
                </span>
              </span>
            )}
            <WorkflowStatusBadge status={instance.status} />
            {canAdmin && (
              <Button
                size="sm"
                variant="outline"
                className="min-h-11 sm:min-h-8"
                onClick={() => reconcile.mutate()}
                disabled={reconcile.isPending}
                aria-label={t(($) => $.actions.reconcile)}
              >
                <RefreshCw className={cn(
                  reconcile.isPending && "animate-spin motion-reduce:animate-none",
                )} />
                <span className="hidden md:inline">{t(($) => $.actions.reconcile)}</span>
              </Button>
            )}
            {canAdmin && instance.status === "running" && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => pause.mutate()}
                disabled={pause.isPending}
              >
                <Pause />
                <span className="hidden md:inline">{t(($) => $.actions.pause)}</span>
              </Button>
            )}
            {canAdmin && instance.status === "paused" && (
              <Button
                size="sm"
                onClick={() => resume.mutate()}
                disabled={resume.isPending}
              >
                <Play />
                <span className="hidden md:inline">{t(($) => $.actions.resume)}</span>
              </Button>
            )}
          </>
        )}
      />
      <main
        data-tab-scroll-root="workflow-workbench"
        className="min-h-0 flex-1 overflow-y-auto"
      >
        <section
          data-testid="workflow-activity-map"
          className="border-b bg-muted/15 px-5 py-4"
        >
          <div className="mx-auto max-w-[90rem]">
            <h2 className="mb-2 flex items-center gap-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
              <CircleDot className="size-3.5" />
              {t(($) => $.workbench.activity_map)}
            </h2>
            <WorkflowCanvas
              definition={templateVersion?.definition}
              nodes={nodes}
              selectedId={selectedNodeId}
              onSelect={setSelectedNodeId}
            />
          </div>
        </section>

        {instance.status === "needs_setup" && templateVersion && (
          <section className="mx-auto max-w-7xl px-5 pt-5">
            <RoleSetupPanel
              instanceId={instanceId}
              roles={templateVersion.definition.roles}
              currentAssignments={detailQuery.data?.role_assignments ?? []}
              actorOptions={actorOptions}
              canConfigure={canConfigureRoles}
            />
          </section>
        )}

        <div className="mx-auto flex max-w-[90rem] flex-col gap-5 px-5 py-5">
          <section className="order-2 min-w-0 space-y-4">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <h2 className="text-base font-medium">
                  {selectedNode?.name ?? t(($) => $.workbench.node_details)}
                </h2>
                {selectedNode && (
                <div className="mt-1 flex items-center gap-2">
                    <WorkflowStatusBadge status={selectedNode.status} />
                    <span className="text-xs text-muted-foreground">
                      {t(($) => $.workbench.attempt, {
                        attempt: selectedNode.attempt,
                      })}
                    </span>
                  </div>
                )}
              </div>
            </div>

            {selectedNode && nodeQuery.data && (
              <div className="grid gap-3 rounded-xl border bg-muted/20 p-4 sm:grid-cols-2">
                {selectedNode.definition.description && (
                  <p className="text-sm text-muted-foreground sm:col-span-2">
                    {selectedNode.definition.description}
                  </p>
                )}
                <div>
                  <p className="text-xs font-medium text-muted-foreground">
                    {t(($) => $.workbench.owners)}
                  </p>
                  <div className="mt-1.5 flex flex-wrap gap-2">
                    {nodeQuery.data.participants
                      .filter((participant) => participant.role === "owner")
                      .map((participant) => (
                        <span
                          key={participant.id}
                          className="inline-flex items-center gap-1.5 text-sm"
                        >
                          <ActorAvatar
                            actorType={participant.actor_type as "member" | "agent" | "squad"}
                            actorId={participant.actor_id}
                            size="sm"
                          />
                          {actorName(
                            participant.actor_type as "member" | "agent" | "squad",
                            participant.actor_id,
                          )}
                        </span>
                      ))}
                    {!nodeQuery.data.participants.some(
                      (participant) => participant.role === "owner",
                    ) && (
                      <span className="text-sm text-muted-foreground">
                        {t(($) => $.runs.none)}
                      </span>
                    )}
                  </div>
                </div>
                <div>
                  <p className="text-xs font-medium text-muted-foreground">
                    {t(($) => $.workbench.participants)}
                  </p>
                  <div className="mt-1.5 flex flex-wrap gap-2 text-sm">
                    {nodeQuery.data.participants
                      .filter((participant) => participant.role !== "owner")
                      .map((participant) => (
                        <span key={participant.id}>
                          {actorName(
                            participant.actor_type as "member" | "agent" | "squad",
                            participant.actor_id,
                          )}
                        </span>
                      ))}
                    {!nodeQuery.data.participants.some(
                      (participant) => participant.role !== "owner",
                    ) && (
                      <span className="text-muted-foreground">
                        {t(($) => $.runs.none)}
                      </span>
                    )}
                  </div>
                </div>
                {selectedNode.definition.timeout_minutes && (
                  <p className="text-xs text-muted-foreground sm:col-span-2">
                    {t(($) => $.workbench.timeout, {
                      minutes: selectedNode.definition.timeout_minutes,
                    })}
                  </p>
                )}
              </div>
            )}

            {selectedNode?.waiting_reasons.length ? (
              <Alert>
                <AlertCircle />
                <AlertTitle>{t(($) => $.workbench.waiting)}</AlertTitle>
                <AlertDescription>
                  <ul className="list-disc space-y-1 pl-4">
                    {selectedNode.waiting_reasons.map((reason, index) => (
                      <li key={`${reason.code}-${index}`}>
                        {reason.message || reason.code}
                      </li>
                    ))}
                  </ul>
                </AlertDescription>
              </Alert>
            ) : null}

            {selectedNode && nodeQuery.data ? (
              <Tabs defaultValue="tasks">
                <TabsList variant="line" className="w-full justify-start border-b">
                  <TabsTrigger value="tasks">
                    <ListChecks />
                    {t(($) => $.workbench.tasks)}
                  </TabsTrigger>
                  {hasSubmissionPanel && (
                    <TabsTrigger value="submission">
                      <Send />
                      {t(($) => $.workbench.submission)}
                    </TabsTrigger>
                  )}
                  {hasVerdictPanel && (
                    <TabsTrigger value="verdict">
                      <FileCheck2 />
                      {t(($) => $.workbench.verdict)}
                    </TabsTrigger>
                  )}
                  {selectedNode.definition.activity_mode === "acceptance" && (
                    <TabsTrigger value="acceptance">
                      <Check />
                      {t(($) => $.workbench.acceptance)}
                    </TabsTrigger>
                  )}
                  <TabsTrigger value="history">
                    <History />
                    {t(($) => $.workbench.history)}
                  </TabsTrigger>
                  {canAdmin && (
                    <TabsTrigger value="diagnostics">
                      <Stethoscope />
                      {t(($) => $.workbench.diagnostics)}
                    </TabsTrigger>
                  )}
                </TabsList>
                <TabsContent value="tasks" className="pt-4">
                  <div className="space-y-2">
                    {nodeQuery.data.tasks.map((task) => {
                      const resolution = nodeQuery.data.executor_resolutions.find(
                        (item) =>
                          item.id === task.executor_resolution_id ||
                          (!task.executor_resolution_id &&
                            item.workflow_node_task_id === task.id),
                      );
                      return (
                        <WorkflowTaskCard
                          key={task.id}
                          instanceId={instanceId}
                          nodeId={selectedNode.id}
                          task={task}
                          resolution={resolution}
                          actorOptions={actorOptions}
                          canManage={canManageSelectedNode}
                          canAdmin={canAdmin}
                        />
                      );
                    })}
                    {nodeQuery.data.tasks.length === 0 && (
                      <p className="py-6 text-center text-sm text-muted-foreground">
                        {t(($) => $.workbench.no_tasks)}
                      </p>
                    )}
                    <DynamicIssuePanel
                      instanceId={instanceId}
                      node={selectedNode}
                      actorOptions={actorOptions}
                      canManage={canManageSelectedNode}
                    />
                  </div>
                </TabsContent>
                {hasSubmissionPanel && (
                  <TabsContent value="submission" className="pt-4">
                    <SubmissionPanel
                      instanceId={instanceId}
                      node={selectedNode}
                      submissions={nodeQuery.data.submissions}
                      tasks={nodeQuery.data.tasks}
                      actorOptions={actorOptions}
                      canManage={canManageSelectedNode}
                    />
                  </TabsContent>
                )}
                {hasVerdictPanel && (
                  <TabsContent value="verdict" className="pt-4">
                    <VerdictPanel
                      instanceId={instanceId}
                      node={selectedNode}
                      verdicts={nodeQuery.data.verdicts}
                      canRecord={canManageSelectedNode}
                    />
                  </TabsContent>
                )}
                <TabsContent value="acceptance" className="pt-4">
                  <AcceptancePanel
                    instanceId={instanceId}
                    node={selectedNode}
                    targets={targetItems}
                    pending={latestAcceptance?.status === "pending"}
                    canDecide={canDecideAcceptance}
                  />
                </TabsContent>
                <TabsContent value="history" className="pt-4">
                  <WorkflowHistoryPanel
                    events={eventsQuery.data?.events ?? []}
                    loading={eventsQuery.isLoading}
                  />
                </TabsContent>
                {canAdmin && (
                  <TabsContent value="diagnostics" className="pt-4">
                    <WorkflowDiagnosticsPanel diagnostics={diagnosticsQuery.data} />
                  </TabsContent>
                )}
              </Tabs>
            ) : (
              <p className="py-10 text-center text-sm text-muted-foreground">
                {t(($) => $.workbench.select_node)}
              </p>
            )}

            {selectedNode && nodeQuery.data && (
              <>
                <ConfirmationPanel
                  instanceId={instanceId}
                  node={selectedNode}
                  confirmations={nodeQuery.data.confirmations}
                  actorName={actorName}
                  canConfirm={canConfirmSelectedNode}
                />
                <NodeTransitionPanel
                  instanceId={instanceId}
                  node={selectedNode}
                  canManage={canManageSelectedNode}
                  canAdmin={canAdmin}
                  instanceRunning={instance.status === "running"}
                />
                {canAdmin && (
                  <WorkflowCancelPanel
                    instanceId={instanceId}
                    status={instance.status}
                  />
                )}
              </>
            )}
          </section>

          <section
            data-testid="workflow-issue-workspace"
            className="order-1 min-w-0"
          >
            <div className="overflow-hidden rounded-xl border bg-surface">
              <div className="flex items-center justify-between gap-3 border-b px-4 py-3">
                <div>
                  <h2 className="text-sm font-medium">
                    {t(($) => $.workbench.tasks)}
                  </h2>
                  <p className="mt-0.5 text-xs text-muted-foreground">
                    {t(($) => $.workbench.issue_progress, {
                      done: visibleIssues.filter((issue) => issue.status === "done").length,
                      total: visibleIssues.length,
                    })}
                  </p>
                </div>
                <div className="flex rounded-lg bg-muted p-0.5">
                  <button
                    type="button"
                    onClick={() => setIssueScope("current")}
                    className={cn(
                      "min-h-11 rounded-md px-2 py-1 text-xs text-muted-foreground sm:min-h-8",
                      issueScope === "current" && "bg-background text-foreground shadow-xs",
                    )}
                  >
                    {t(($) => $.workbench.current_issues)}
                  </button>
                  <button
                    type="button"
                    onClick={() => setIssueScope("all")}
                    className={cn(
                      "min-h-11 rounded-md px-2 py-1 text-xs text-muted-foreground sm:min-h-8",
                      issueScope === "all" && "bg-background text-foreground shadow-xs",
                    )}
                  >
                    {t(($) => $.workbench.all_issues)}
                  </button>
                </div>
              </div>
              <div className="grid gap-px bg-border md:grid-cols-2 xl:grid-cols-3">
                {visibleIssues.length === 0 ? (
                  <p className="bg-surface px-4 py-10 text-center text-sm text-muted-foreground md:col-span-2 xl:col-span-3">
                    {t(($) => $.workbench.no_issues)}
                  </p>
                ) : visibleIssues.map((issue) => (
                  <AppLink
                    key={issue.id}
                    href={p.issueDetail(issue.id)}
                    className="group flex items-start gap-3 bg-surface px-4 py-3 transition-colors hover:bg-muted/60"
                  >
                    <StatusIcon status={issue.status} className="mt-0.5 size-4 shrink-0" />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-xs text-muted-foreground">
                        {issue.identifier}
                      </span>
                      <span className="mt-0.5 block text-sm">{issue.title}</span>
                      {issue.workflow_context && (
                        <span className="mt-1 flex flex-wrap items-center gap-1.5">
                          <Badge variant="outline" className="h-5 max-w-40 truncate px-1.5 text-[10px]">
                            {issue.workflow_context.activity_name ||
                              issue.workflow_context.activity_key}
                          </Badge>
                          <span className="text-[10px] text-muted-foreground">
                            {issue.workflow_context.required
                              ? t(($) => $.workbench.required)
                              : t(($) => $.workbench.optional)}
                          </span>
                        </span>
                      )}
                    </span>
                    {issue.assignee_type && issue.assignee_id && (
                      <ActorAvatar
                        actorType={issue.assignee_type}
                        actorId={issue.assignee_id}
                        size="sm"
                      />
                    )}
                    <ChevronRight className="mt-2 size-3.5 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100" />
                  </AppLink>
                ))}
              </div>
            </div>
          </section>
        </div>
        <section className="mx-auto max-w-[90rem] px-5 pb-5">
          <div className="overflow-hidden rounded-xl border bg-surface">
            <button
              type="button"
              aria-expanded={hostDetailsOpen}
              aria-controls="workflow-host-issue-details"
              onClick={() => setHostDetailsOpen((open) => !open)}
              className="flex min-h-11 w-full items-center gap-3 px-4 py-3 text-left outline-none transition-colors hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring motion-reduce:transition-none"
            >
              <ChevronRight
                className={cn(
                  "size-4 shrink-0 text-muted-foreground transition-transform motion-reduce:transition-none",
                  hostDetailsOpen && "rotate-90",
                )}
              />
              <span>
                <span className="block text-sm font-medium">
                  {t(($) => $.workbench.host_details)}
                </span>
                <span className="mt-0.5 block text-xs text-muted-foreground">
                  {t(($) => $.workbench.host_details_description)}
                </span>
              </span>
            </button>
            {hostDetailsOpen && (
              <div
                id="workflow-host-issue-details"
                className="h-[min(72vh,52rem)] min-h-[32rem] border-t"
              >
                <IssueDetail
                  issueId={instance.host_issue_id}
                  defaultSidebarOpen={false}
                  layoutId={`workflow_host_issue_${instance.id}`}
                />
              </div>
            )}
          </div>
        </section>
      </main>
    </div>
  );
}
