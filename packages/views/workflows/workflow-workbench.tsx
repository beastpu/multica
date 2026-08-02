"use client";

import {
  AlertCircle,
  ArrowUpRight,
  Check,
  CircleDot,
  FileCheck2,
  GitBranch,
  History,
  ListChecks,
  MoreHorizontal,
  PanelRight,
  Pause,
  Play,
  Plus,
  RefreshCw,
  RotateCcw,
  Send,
  SkipForward,
  Unlink,
  Undo2,
  UserRoundCheck,
  Users,
  X,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useDefaultLayout, usePanelRef } from "react-resizable-panels";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { issueDetailOptions } from "@multica/core/issues/queries";
import { useWorkspacePaths } from "@multica/core/paths";
import type { Issue } from "@multica/core/types";
import {
  useCreateWorkflowSubmission,
  useCreateWorkflowNodeIssue,
  useCreateWorkflowVerdict,
  useChangeWorkflowNodeTask,
  useCancelWorkflowInstance,
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
  workflowNodeArtifactsOptions,
  useReviewWorkflowArtifact,
  workflowOptions,
  workflowCompletionMode,
  type WorkflowNodeInstance,
  type WorkflowNodeTask,
  type WorkflowDiagnostics,
  type WorkflowExecutorResolution,
  type WorkflowEvent,
  type WorkflowSubmission,
  type WorkflowVerdict,
  type WorkflowRoleAssignment,
  type WorkflowNodeDefinition,
  type WorkflowRoleDefinition,
} from "@multica/core/workflows";
import {
  agentListOptions,
  memberListOptions,
  squadListOptions,
} from "@multica/core/workspace/queries";
import { Alert, AlertDescription, AlertTitle } from "@multica/ui/components/ui/alert";
import { Button } from "@multica/ui/components/ui/button";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "@multica/ui/components/ui/resizable";
import { Sheet, SheetContent } from "@multica/ui/components/ui/sheet";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { cn } from "@multica/ui/lib/utils";
import { AppLink } from "../navigation";
import { IssueOpenProvider } from "../issues/surface/issue-open-context";
import { CollectionPageHeader, CollectionPageState } from "../layout/collection-page";
import {
  AnimatedRightSidebar,
  getAnimatedRightSidebarInitialOpen,
  rightSidebarPanelMotionProps,
  useAnimatedRightSidebarState,
} from "../layout/animated-right-sidebar";
import { useT } from "../i18n";
import { IssueDisplayControls } from "../issues/components/issues-header";
import { IssueSurface } from "../issues/surface/issue-surface";
import { PriorityIcon } from "../issues/components/priority-icon";
import { WorkflowIssuePanel } from "./workflow-issue-panel";
import { WorkflowNodeIssues } from "./workflow-node-issues";
import { ActorAvatar } from "../common/actor-avatar";
import { WorkflowCanvas } from "./workflow-canvas";
import { Badge } from "@multica/ui/components/ui/badge";
import {
  WORKFLOW_SECTION_HEADING,
  WorkflowStatusBadge,
  isWorkflowNodeOpen,
  workflowNodeDisplayStatus,
} from "./workflow-status";
import { ReworkReasonFields } from "./rework-reason-fields";
import { branchChoiceDuty } from "./branch-choice";
import {
  composeReworkReason,
  emptyReworkReasonDraft,
  isReworkReasonComplete,
} from "./rework-reason";
import {
  latestWorkflowAttemptNodes,
  type WorkflowIssueScope,
} from "./workflow-workbench-state";

// handoffSummaryLimit mirrors the server cap. Showing it as a countdown lets an
// author trim before submitting instead of being rejected after writing.
const handoffSummaryLimit = 500;

export function SubmissionPanel({
  instanceId,
  node,
  submissions,
  tasks,
  canManage,
  branchDuty,
}: {
  instanceId: string;
  node: WorkflowNodeInstance;
  submissions: WorkflowSubmission[];
  tasks: WorkflowNodeTask[];
  actorOptions: WorkflowActorOption[];
  canManage: boolean;
  branchDuty: ReturnType<typeof branchChoiceDuty>;
}) {
  const { t } = useT("workflows");
  const [values, setValues] = useState<Record<string, unknown>>({});
  const [summary, setSummary] = useState("");
  const [choice, setChoice] = useState("");
  const [sourceIssueId, setSourceIssueId] = useState("");
  const submit = useCreateWorkflowSubmission(instanceId, node.id);
  const submissionPolicy = node.definition.submission_schema?.policy ?? "single";
  const taskScoped = submissionPolicy === "per_required_task" ||
    submissionPolicy === "fan_in";
  const sourceTasks = tasks.filter((task) => task.issue_id);
  // A node that owes a verdict but declares no schema gets a submission
  // synthesised for it, carrying a canned summary and a payload that names
  // the rule that produced it. It is engine bookkeeping, and listing it
  // beside what a person actually handed over reads as if they wrote it.
  const deliveredSubmissions = submissions.filter(
    (submission) => submission.submitted_by_type !== "system",
  );
  // Only the values a gateway condition actually matches. Offering every node
  // let an author pick one no condition reads: the server accepts it and the
  // run then takes the default edge, looking exactly like no decision at all.

  useEffect(() => {
    setValues({});
    setSummary("");
    setChoice("");
    setSourceIssueId("");
  }, [node.id]);

  return (
    <div className="space-y-4">
      {/* A node with no schema still owes the next node a conclusion, and the
          default node shape has no schema — gating this panel on one left the
          most common node with nowhere to hand anything off from. */}
      {canManage && isWorkflowNodeOpen(node.status) && (
        <div className="space-y-3 rounded-xl border bg-muted/20 p-4">
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
                    {workflowTaskTitle(task)}
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
              maxLength={handoffSummaryLimit}
            />
            <p className="text-xs text-muted-foreground">
              {t(($) => $.workbench.summary_help, {
                used: summary.length,
                limit: handoffSummaryLimit,
              })}
            </p>
          </div>
          {branchDuty && (
            <div className="space-y-1.5">
              <Label htmlFor="workflow-submission-choice">
                {t(($) => $.workbench.branch_choice)}
              </Label>
              <select
                id="workflow-submission-choice"
                value={choice}
                onChange={(event) => setChoice(event.target.value)}
                className="min-h-11 w-full rounded-lg border border-input bg-background px-3 text-sm"
              >
                <option value="">
                  {t(($) => $.workbench.branch_choice_none)}
                </option>
                {branchDuty.options.map((option) => (
                  <option key={option.key} value={option.key}>
                    {option.key} — {option.target}
                  </option>
                ))}
              </select>
              {branchDuty.defaultTarget && (
                <p className="text-xs text-muted-foreground">
                  {t(($) => $.workbench.branch_choice_help, {
                    gateway: branchDuty.gatewayName,
                    fallback: branchDuty.defaultTarget,
                  })}
                </p>
              )}
            </div>
          )}
          <Button
            size="sm"
            className="min-h-11"
            onClick={() => submit.mutate({
              payload: values,
              summary,
              choice: choice || undefined,
              source_issue_id: sourceIssueId || undefined,
            }, {
              onSuccess: () => {
                setChoice("");
              },
            })}
            disabled={
              submit.isPending ||
              (taskScoped && !sourceIssueId)
            }
          >
            <Send />
            {t(($) => $.actions.submit)}
          </Button>
          {submit.isError && (
            <p role="alert" className="text-xs text-destructive">
              {t(($) => $.errors.action_failed)}
            </p>
          )}
        </div>
      )}
      {deliveredSubmissions.length === 0 ? (
        <p className="py-6 text-center text-sm text-muted-foreground">
          {t(($) => $.workbench.no_submission)}
        </p>
      ) : (
        <div className="space-y-3">
          {deliveredSubmissions.map((submission) => (
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
                    {workflowTaskTitle(tasks.find((task) =>
                      task.issue_id === submission.source_issue_id
                    )) ?? submission.source_issue_id}
                  </p>
                )}
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}

// ArtifactPanel is the human half of artifacts. Agents submit through the CLI;
// members need to read what was produced and say whether it passes, because a
// required artifact that nobody can see is a gate nobody can clear.
export function ArtifactPanel({
  instanceId,
  node,
  canManage,
}: {
  instanceId: string;
  node: WorkflowNodeInstance;
  canManage: boolean;
}) {
  const { t } = useT("workflows");
  const wsId = useWorkspaceId();
  const [expanded, setExpanded] = useState<string>("");
  const [comment, setComment] = useState("");
  const artifactsQuery = useQuery(workflowNodeArtifactsOptions(wsId, node.id));
  const review = useReviewWorkflowArtifact(instanceId, node.id);
  const required = node.definition.artifacts ?? [];
  const artifacts = artifactsQuery.data?.artifacts ?? [];
  const delivered = new Set(artifacts.map((artifact) => artifact.artifact_key));

  if (required.length === 0 && artifacts.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        {t(($) => $.workbench.no_artifacts)}
      </p>
    );
  }

  return (
    <div className="space-y-3">
      {required
        .filter((requirement) => !delivered.has(requirement.key))
        .map((requirement) => (
          <div
            key={requirement.key}
            className="rounded-xl border border-dashed p-4"
          >
            <p className="text-sm font-medium">{requirement.name}</p>
            {requirement.description && (
              <p className="mt-1 text-xs text-muted-foreground">
                {requirement.description}
              </p>
            )}
            <p className="mt-2 text-xs text-muted-foreground">
              {requirement.required
                ? t(($) => $.workbench.artifact_missing_required)
                : t(($) => $.workbench.artifact_missing_optional)}
            </p>
          </div>
        ))}
      {artifacts.map((artifact) => (
        <div key={artifact.id} className="space-y-3 rounded-xl border p-4">
          <div className="flex flex-wrap items-start justify-between gap-2">
            <div className="min-w-0">
              <p className="truncate text-sm font-medium">{artifact.name}</p>
              {artifact.description && (
                <p className="mt-1 text-xs text-muted-foreground">
                  {artifact.description}
                </p>
              )}
            </div>
            <Badge
              variant={artifact.review_status === "rejected"
                ? "destructive"
                : "outline"}
            >
              {artifact.review_status}
            </Badge>
          </div>
          {artifact.kind === "link" && artifact.url && (
            <a
              href={artifact.url}
              target="_blank"
              rel="noreferrer noopener"
              className="block truncate text-sm underline underline-offset-2"
            >
              {artifact.url}
            </a>
          )}
          {artifact.kind === "document" && artifact.content && (
            <>
              <Button
                size="sm"
                variant="outline"
                className="min-h-11"
                onClick={() =>
                  setExpanded(expanded === artifact.id ? "" : artifact.id)}
              >
                {expanded === artifact.id
                  ? t(($) => $.workbench.artifact_hide)
                  : t(($) => $.workbench.artifact_read)}
              </Button>
              {expanded === artifact.id && (
                <pre className="max-h-96 overflow-auto whitespace-pre-wrap rounded-lg bg-muted/40 p-3 text-xs">
                  {artifact.content}
                </pre>
              )}
            </>
          )}
          {artifact.review_comment && (
            <p className="text-xs text-muted-foreground">
              {artifact.review_comment}
            </p>
          )}
          {canManage && (
            <div className="space-y-2">
              <Textarea
                value={expanded === artifact.id ? comment : ""}
                rows={2}
                placeholder={t(($) => $.workbench.artifact_review_comment)}
                onChange={(event) => {
                  setExpanded(artifact.id);
                  setComment(event.target.value);
                }}
              />
              <div className="flex flex-wrap gap-2">
                <Button
                  size="sm"
                  className="min-h-11"
                  disabled={review.isPending}
                  onClick={() => review.mutate({
                    artifactId: artifact.id,
                    status: "approved",
                    comment: comment.trim() || undefined,
                  }, { onSuccess: () => setComment("") })}
                >
                  {t(($) => $.actions.approve)}
                </Button>
                <Button
                  size="sm"
                  variant="destructive"
                  className="min-h-11"
                  disabled={review.isPending}
                  onClick={() => review.mutate({
                    artifactId: artifact.id,
                    status: "rejected",
                    comment: comment.trim() || undefined,
                  }, { onSuccess: () => setComment("") })}
                >
                  {t(($) => $.actions.reject)}
                </Button>
              </div>
            </div>
          )}
        </div>
      ))}
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
  const isOpen = isWorkflowNodeOpen(node.status);
  const reasonRequired = result === "fail" || result === "blocked";

  useEffect(() => {
    setResult("pass");
    setReason("");
    setConfidence("");
  }, [node.id]);

  return (
    <div className="space-y-4">
      {canRecord && isOpen && reviewerAcceptsMember(node.definition) && (
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
  variant = "setup",
}: {
  instanceId: string;
  roles: WorkflowRoleDefinition[];
  currentAssignments: WorkflowRoleAssignment[];
  actorOptions: WorkflowActorOption[];
  canConfigure: boolean;
  /** "setup" blocks the run and stays open; "reassign" is a routine edit. */
  variant?: "setup" | "reassign";
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

  const reassign = variant === "reassign";
  const body = (
    <>
      {!reassign && (
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
      )}
      {reassign && (
        <p className="text-xs text-muted-foreground">
          {t(($) => $.workbench.role_reassign_help)}
        </p>
      )}
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
    </>
  );

  if (reassign) {
    return (
      <details className="rounded-xl border bg-background">
        <summary className="min-h-9 cursor-pointer select-none px-3 py-2 text-sm font-medium">
          {t(($) => $.workbench.role_reassign)}
        </summary>
        <div className="space-y-4 border-t p-3">{body}</div>
      </details>
    );
  }

  return (
    <section
      aria-labelledby="workflow-role-setup-title"
      className="space-y-4 rounded-xl border border-amber-500/30 bg-amber-500/5 p-4"
    >
      {body}
    </section>
  );
}

// Three type sizes in the node panel, and no more. Headings are signposts, not
// content: they stay xs and muted so the eye lands on values instead. Values are
// sm. Only the node's own name gets base — it is the one thing the panel is
// about. Mixing sm-weight headings with xs-weight ones is what made the panel
// read as "fonts jumping around" (the same label appeared at two sizes
// depending on which block it sat in).
const SECTION_HEADING = WORKFLOW_SECTION_HEADING;

function workflowTaskTitle(task?: WorkflowNodeTask): string | undefined {
  if (!task) return undefined;
  return "title" in task.definition
    ? task.definition.title
    : task.definition.name || task.task_key;
}

export function WorkflowTaskCard({
  instanceId,
  nodeId,
  task,
  resolution,
  actorOptions,
  canManage,
  canAdmin,
  executionFailed = false,
}: {
  instanceId: string;
  nodeId: string;
  task: WorkflowNodeTask;
  resolution?: WorkflowExecutorResolution;
  actorOptions: WorkflowActorOption[];
  canManage: boolean;
  canAdmin: boolean;
  executionFailed?: boolean;
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
  const needsExecutor = resolution?.status !== "resolved";
  const isRecoverable = task.materialization_status === "failed" ||
    task.materialization_status === "materializing" || executionFailed;
  const taskTitle = workflowTaskTitle(task);

  return (
    <article className="space-y-3 rounded-xl border bg-surface px-4 py-3">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 className="truncate text-sm font-medium">
            {taskTitle}
          </h3>
          <p className="mt-0.5 text-xs text-muted-foreground">
            {task.required
              ? t(($) => $.workbench.required)
              : t(($) => $.workbench.optional)}
            <span aria-hidden="true"> · </span>
            {task.source === "execution"
              ? t(($) => $.workbench.direct_execution)
              : task.source === "dynamic"
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

      {canAdmin && isRecoverable && (
        <details className="group">
          <summary className="cursor-pointer text-xs text-muted-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring">
            {t(($) => $.workbench.recovery_actions)}
          </summary>
          <div className="mt-3 grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto]">
            <Input
              aria-label={t(($) => $.workbench.action_reason)}
              placeholder={t(($) => $.workbench.action_reason)}
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              className="min-h-11"
            />
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
  onCreated,
  showIntro = true,
}: {
  instanceId: string;
  node: WorkflowNodeInstance;
  actorOptions: WorkflowActorOption[];
  canManage: boolean;
  onCreated?: () => void;
  showIntro?: boolean;
}) {
  const { t } = useT("workflows");
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [actorValue, setActorValue] = useState("");
  const [required, setRequired] = useState(false);
  const createIssue = useCreateWorkflowNodeIssue(instanceId, node.id);
  const policy = node.definition.issue_policy ?? "none";
  const isOpen = isWorkflowNodeOpen(node.status);
  if (!canManage || !isOpen ||
    (policy !== "dynamic" && policy !== "fixed_and_dynamic")) {
    return null;
  }
  const selectedActor = actorOptions.find(
    (actor) => `${actor.type}:${actor.id}` === actorValue,
  );

  return (
    <div className="space-y-3 rounded-xl border border-dashed bg-muted/15 p-4">
      {showIntro && (
        <div>
          <h3 className="flex items-center gap-2 text-sm font-medium">
            <Plus className="size-4" />
            {t(($) => $.workbench.add_dynamic_issue)}
          </h3>
          <p className="mt-1 text-xs text-muted-foreground">
            {t(($) => $.workbench.add_dynamic_issue_help)}
          </p>
        </div>
      )}
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
              onCreated?.();
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

function NodeTransitionPanel({
  instanceId,
  node,
  submissions,
  canManage,
  canAdmin,
  instanceRunning,
}: {
  instanceId: string;
  node: WorkflowNodeInstance;
  submissions: WorkflowSubmission[];
  canManage: boolean;
  canAdmin: boolean;
  instanceRunning: boolean;
}) {
  const { t } = useT("workflows");
  const [completeOpen, setCompleteOpen] = useState(false);
  const [completionNote, setCompletionNote] = useState("");
  const [completionValues, setCompletionValues] = useState<Record<string, unknown>>({});
  const [reworkDraft, setReworkDraft] = useState(emptyReworkReasonDraft);
  const [managementAction, setManagementAction] = useState<
    "complete" | "skip" | "rollback" | null
  >(null);
  const [managementReason, setManagementReason] = useState("");
  const submit = useCreateWorkflowSubmission(instanceId, node.id);
  const transition = useTransitionWorkflowNode(instanceId, node.id);
  const open = isWorkflowNodeOpen(node.status);
  const manualCompletion = workflowCompletionMode(node.definition) === "manual";
  const canComplete = open && manualCompletion && canManage;
  const canRollback = canManage && instanceRunning &&
    (node.status === "completed" || node.status === "skipped");
  const canOpenManagement = (canAdmin && open) || canRollback;
  const schema = node.definition.submission_schema;
  const singleCompletionForm = schema?.policy === "single";
  const taskScopedCompletionForm = schema?.policy === "per_required_task" ||
    schema?.policy === "fan_in";
  const prerequisiteReasons = node.waiting_reasons.filter((reason) =>
    reason.code !== "manual_completion_required" &&
    !(singleCompletionForm && reason.code === "valid_submission_required")
  );
  const latestSubmission = submissions.find((item) => item.status === "valid");

  useEffect(() => {
    setCompleteOpen(false);
    setCompletionNote("");
    setCompletionValues({});
    setManagementAction(null);
    setManagementReason("");
  }, [node.id]);

  const openCompletionDialog = () => {
    setCompletionValues(
      singleCompletionForm
        ? { ...(latestSubmission?.payload ?? {}) }
        : {},
    );
    setCompleteOpen(true);
  };

  const runCompletion = async () => {
    try {
      if (singleCompletionForm) {
        await submit.mutateAsync({
          payload: completionValues,
          summary: completionNote.trim(),
        });
      }
      await transition.mutateAsync({
        action: "complete",
        reason: completionNote.trim(),
      });
      setCompleteOpen(false);
    } catch {
      return;
    }
  };

  // A rollback sends work back to an executor; force-complete and skip end it.
  // Only the first has someone downstream who needs to know what to change.
  const isReworkAction = managementAction === "rollback";
  const managementReasonValue = isReworkAction
    ? composeReworkReason(reworkDraft)
    : managementReason.trim();
  const managementReasonReady = isReworkAction
    ? isReworkReasonComplete(reworkDraft)
    : managementReason.trim().length > 0;

  const runManagementAction = async () => {
    if (!managementAction || !managementReasonReady) return;
    try {
      await transition.mutateAsync({
        action: managementAction,
        reason: managementReasonValue,
      });
      setManagementAction(null);
      setManagementReason("");
      setReworkDraft(emptyReworkReasonDraft);
    } catch {
      return;
    }
  };

  if (!canComplete && !canOpenManagement) return null;

  return (
    // No card, no "node operations" heading: this is the action the panel
    // builds up to, and wrapping it in chrome made it read as one more
    // reference block. The primary button carries the meaning; the rest of the
    // node's transitions stay behind the overflow menu.
    <div className="flex items-center gap-1.5">
      <div className="flex flex-1 items-center gap-1.5">
        {canComplete && (
          <Button
            size="sm"
            className="min-h-11 sm:min-h-8"
            disabled={transition.isPending || submit.isPending}
            onClick={openCompletionDialog}
          >
            <Check aria-hidden="true" />
            {t(($) => $.actions.complete)}
          </Button>
        )}
        {canOpenManagement && (
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <Button
                  type="button"
                  variant="ghost"
                  size={canComplete ? "icon-sm" : "sm"}
                  className="min-h-11 sm:min-h-8"
                  aria-label={t(($) => $.workbench.manage_node)}
                >
                  <MoreHorizontal aria-hidden="true" />
                  {!canComplete && t(($) => $.workbench.manage_node)}
                </Button>
              }
            />
            <DropdownMenuContent align="end">
              {canAdmin && open && (
                <DropdownMenuItem onClick={() => setManagementAction("complete")}>
                  <Check aria-hidden="true" />
                  {t(($) => $.actions.force_complete)}
                </DropdownMenuItem>
              )}
              {canAdmin && open && (
                <DropdownMenuItem onClick={() => setManagementAction("skip")}>
                  <SkipForward aria-hidden="true" />
                  {t(($) => $.actions.skip)}
                </DropdownMenuItem>
              )}
              {canRollback && (
                <DropdownMenuItem onClick={() => setManagementAction("rollback")}>
                  <Undo2 aria-hidden="true" />
                  {t(($) => $.actions.rollback)}
                </DropdownMenuItem>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </div>

      <Dialog open={completeOpen} onOpenChange={setCompleteOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t(($) => $.workbench.complete_activity)}</DialogTitle>
            <DialogDescription>
              {t(($) => $.workbench.complete_dialog_help)}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            {taskScopedCompletionForm && (
              <Alert>
                <AlertCircle aria-hidden="true" />
                <AlertDescription>
                  {t(($) => $.workbench.task_scoped_completion_form_help)}
                </AlertDescription>
              </Alert>
            )}
            {prerequisiteReasons.length > 0 && (
              <Alert>
                <AlertCircle aria-hidden="true" />
                <AlertDescription>
                  <ul className="list-disc space-y-1 pl-4">
                    {prerequisiteReasons.map((reason, index) => (
                      <li key={`${reason.code}-${index}`}>
                        {reason.message || reason.code}
                      </li>
                    ))}
                  </ul>
                </AlertDescription>
              </Alert>
            )}
            <div className="space-y-1.5">
              <Label htmlFor={`workflow-completion-note-${node.id}`}>
                {t(($) => $.workbench.completion_note)}
              </Label>
              <Textarea
                id={`workflow-completion-note-${node.id}`}
                value={completionNote}
                onChange={(event) => setCompletionNote(event.target.value)}
                placeholder={t(($) => $.workbench.completion_note_placeholder)}
                rows={3}
              />
            </div>
            {(transition.isError || submit.isError) && (
              <p role="alert" className="text-xs text-destructive">
                {t(($) => $.errors.action_failed)}
              </p>
            )}
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => setCompleteOpen(false)}
            >
              {t(($) => $.start.cancel)}
            </Button>
            <Button
              type="button"
              disabled={
                prerequisiteReasons.length > 0 ||
                transition.isPending ||
                submit.isPending
              }
              onClick={() => void runCompletion()}
            >
              <Check aria-hidden="true" />
              {t(($) => $.actions.complete)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={managementAction !== null}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) {
            setManagementAction(null);
            setManagementReason("");
          }
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t(($) => $.workbench.admin_actions)}</DialogTitle>
            <DialogDescription>
              {t(($) => $.workbench.admin_actions_help)}
            </DialogDescription>
          </DialogHeader>
          {isReworkAction
            ? (
              <ReworkReasonFields
                idPrefix={`workflow-rollback-${node.id}`}
                draft={reworkDraft}
                onChange={setReworkDraft}
              />
            )
            : (
              <div className="space-y-1.5">
                <Label htmlFor={`workflow-management-reason-${node.id}`}>
                  {t(($) => $.workbench.action_reason)}
                </Label>
                <Textarea
                  id={`workflow-management-reason-${node.id}`}
                  value={managementReason}
                  onChange={(event) => setManagementReason(event.target.value)}
                  rows={3}
                />
              </div>
            )}
          {transition.isError && (
            <p role="alert" className="text-xs text-destructive">
              {t(($) => $.errors.action_failed)}
            </p>
          )}
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => setManagementAction(null)}
            >
              {t(($) => $.start.cancel)}
            </Button>
            <Button
              type="button"
              variant={managementAction === "rollback" ? "outline" : "default"}
              disabled={!managementReasonReady || transition.isPending}
              onClick={() => void runManagementAction()}
            >
              {managementAction === "rollback" && <Undo2 aria-hidden="true" />}
              {managementAction === "skip" && <SkipForward aria-hidden="true" />}
              {managementAction === "complete" && <Check aria-hidden="true" />}
              {managementAction === "rollback"
                ? t(($) => $.actions.rollback)
                : managementAction === "skip"
                  ? t(($) => $.actions.skip)
                  : t(($) => $.actions.force_complete)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
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
        task: workflowTaskTitle(task) ?? task.task_key,
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
// Only a person can be asked to post a verdict: an api or auto reviewer rules
// on its own, and offering the form there would let a member overrule the
// judge the template picked.
function reviewerAcceptsMember(definition: WorkflowNodeDefinition): boolean {
  const kind = definition.reviewer?.kind;
  return kind === "role" || kind === "actor" || kind === "owner";
}


export function AcceptancePanel({
  instanceId,
  targets,
  pending,
  canDecide,
}: {
  instanceId: string;
  targets: Array<{ value: string; label: string }>;
  pending: boolean;
  canDecide: boolean;
}) {
  const { t } = useT("workflows");
  const [target, setTarget] = useState(targets[0]?.value ?? "");
  const [reworkDraft, setReworkDraft] = useState(emptyReworkReasonDraft);
  const decide = useDecideWorkflowAcceptance(instanceId);

  useEffect(() => {
    setTarget(targets[0]?.value ?? "");
    setReworkDraft(emptyReworkReasonDraft);
  }, [targets]);

  // Nothing to show until the run reaches its end gate: acceptance is the
  // last thing that happens, not a standing section.
  if (!pending) return null;
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
      <div className="space-y-3 border-t pt-4">
        <ReworkReasonFields
          idPrefix="acceptance-rework"
          draft={reworkDraft}
          onChange={setReworkDraft}
        />
      </div>
      <div className="grid gap-3 sm:grid-cols-[220px_auto] sm:items-end">
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
            if (!isReworkReasonComplete(reworkDraft) || !target) return;
            decide.mutate({
              status: "changes_requested",
              reason: composeReworkReason(reworkDraft),
              rework_target_node_key: target,
            });
          }}
          disabled={decide.isPending ||
            !isReworkReasonComplete(reworkDraft) || !target}
        >
          <RotateCcw />
          {t(($) => $.actions.request_changes)}
        </Button>
      </div>
    </div>
  );
}

function WorkflowIssueDetachDialog({
  instanceId,
  issue,
  onClose,
}: {
  instanceId: string;
  issue: Issue;
  onClose: () => void;
}) {
  const { t } = useT("workflows");
  const wsId = useWorkspaceId();
  const nodeId = issue.workflow_context?.workflow_node_instance_id ?? "";
  const nodeQuery = useQuery(workflowNodeOptions(wsId, nodeId));
  const changeTask = useChangeWorkflowNodeTask(instanceId, nodeId);
  const task = nodeQuery.data?.tasks.find((item) => item.issue_id === issue.id);
  const activityName = issue.workflow_context?.activity_name ??
    nodeQuery.data?.node.name ??
    t(($) => $.workbench.current_issues);

  return (
    <AlertDialog
      open
      onOpenChange={(open) => {
        if (!open && !changeTask.isPending) onClose();
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {t(($) => $.workbench.detach_issue_title)}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {t(($) => $.workbench.detach_issue_description, {
              identifier: issue.identifier,
              activity: activityName,
            })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        {nodeQuery.isError || (!nodeQuery.isLoading && !task) ? (
          <p role="alert" className="text-sm text-destructive">
            {t(($) => $.workbench.detach_issue_unavailable)}
          </p>
        ) : null}
        {changeTask.isError && (
          <p role="alert" className="text-sm text-destructive">
            {t(($) => $.errors.action_failed)}
          </p>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={changeTask.isPending}>
            {t(($) => $.start.cancel)}
          </AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={!task || nodeQuery.isLoading || changeTask.isPending}
            onClick={(event) => {
              event.preventDefault();
              if (!task) return;
              changeTask.mutate(
                {
                  taskId: task.id,
                  action: "detach",
                  reason: t(($) => $.workbench.detach_issue_audit_reason),
                },
                {
                  onSuccess: () => {
                    toast.success(t(($) => $.workbench.detach_issue_success));
                    onClose();
                  },
                },
              );
            }}
          >
            {t(($) => $.workbench.detach_issue_confirm)}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

export function WorkflowWorkbench({ instanceId }: { instanceId: string }) {
  const { t } = useT("workflows");
  const wsId = useWorkspaceId();
  const p = useWorkspacePaths();
  const userId = useAuthStore((state) => state.user?.id);
  const [selectedNodeId, setSelectedNodeId] = useState("");
  const [issueScope, setIssueScope] = useState<WorkflowIssueScope>("current");
  const [createIssueOpen, setCreateIssueOpen] = useState(false);
  // The issue being read inside the run. Null means the sidebar shows the
  // node, which is the resting state.
  const [openIssueId, setOpenIssueId] = useState<string | null>(null);
  const [detachIssue, setDetachIssue] = useState<Issue | null>(null);
  const isMobile = useIsMobile();
  const { defaultLayout, onLayoutChanged } = useDefaultLayout({
    id: "multica_workflow_workbench_layout",
  });
  const sidebarRef = usePanelRef();
  const desktopSidebarInitialOpen = getAnimatedRightSidebarInitialOpen(
    true,
    defaultLayout,
  );
  const {
    open: desktopSidebarOpen,
    visualOpen: desktopSidebarVisualOpen,
    motionEnabled: desktopSidebarMotionEnabled,
    beginToggle: beginDesktopSidebarToggle,
    handleResize: handleDesktopSidebarResize,
  } = useAnimatedRightSidebarState(desktopSidebarInitialOpen);
  const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false);
  const sidebarOpen = isMobile ? mobileSidebarOpen : desktopSidebarOpen;

  useEffect(() => {
    if (isMobile) setMobileSidebarOpen(false);
  }, [isMobile]);

  const handleToggleSidebar = useCallback(() => {
    if (isMobile) {
      setMobileSidebarOpen((open) => !open);
      return;
    }
    const panel = sidebarRef.current;
    if (!panel) return;
    const nextOpen = panel.isCollapsed();
    beginDesktopSidebarToggle(nextOpen);
    window.requestAnimationFrame(() => {
      if (nextOpen) panel.expand();
      else panel.collapse();
    });
  }, [beginDesktopSidebarToggle, isMobile, sidebarRef]);

  const detailQuery = useQuery(workflowInstanceOptions(wsId, instanceId));
  const instance = detailQuery.data?.instance;
  const nodes = useMemo(
    () => latestWorkflowAttemptNodes(detailQuery.data?.nodes ?? []),
    [detailQuery.data?.nodes],
  );
  useEffect(() => {
    if (nodes.length === 0) return;
    if (nodes.some((node) => node.id === selectedNodeId)) return;
    const current = nodes.find((node) => isWorkflowNodeOpen(node.status));
    setSelectedNodeId((current ?? nodes[0])!.id);
  }, [nodes, selectedNodeId]);

  // Rework is invisible once it is over: the retried attempt supersedes the
  // one before it, and the issue it reuses looks like any other. Counting the
  // activities that took more than one attempt is what makes a run that
  // bounced twice distinguishable from one that went straight through.
  const reworkedActivityCount = useMemo(
    () =>
      nodes.filter((node) =>
        node.node_kind === "activity" && node.attempt > 1
      ).length,
    [nodes],
  );

  const selectedNode = nodes.find((node) => node.id === selectedNodeId);
  useEffect(() => {
    setCreateIssueOpen(false);
  }, [selectedNodeId]);
  const nodeQuery = useQuery(workflowNodeOptions(wsId, selectedNodeId));
  const workflowIssuesQuery = useQuery(
    workflowInstanceIssuesOptions(wsId, instanceId),
  );
  const acceptancesQuery = useQuery(workflowAcceptancesOptions(wsId, instanceId));
  const templateQuery = useQuery({
    ...workflowOptions(wsId, instance?.workflow_id ?? ""),
    enabled: Boolean(instance?.workflow_id),
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
  const issueSurfaceScope = useMemo(
    () => ({
      type: "workflow" as const,
      instanceId,
      activityKey:
        issueScope === "current" ? selectedNode?.node_key : undefined,
    }),
    [instanceId, issueScope, selectedNode?.node_key],
  );
  const templateVersion = templateQuery.data?.versions.find(
    (version) => version.id === instance?.workflow_version_id,
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
  // Every activity in the template is a legitimate destination; the approver
  // picks one at rejection time and everything after it rolls back with it.
  const targetItems = (templateVersion?.definition.nodes ?? [])
    .filter((node) => node.kind === "activity")
    .map((node) => ({
      value: node.key,
      label: nodes.find((instance) => instance.node_key === node.key)?.name
        ?? node.name,
    }));
  const latestAcceptance = acceptancesQuery.data?.acceptances[0];
  // Every activity can hand off, so every activity gets the tab. Gating it on a
  // schema hid it from the default node shape — which declares none — leaving
  // the panel inside reachable only by URL.
  const hasSubmissionPanel = selectedNode?.node_kind === "activity";
  const hasVerdictPanel = Boolean(
    selectedNode && reviewerAcceptsMember(selectedNode.definition),
  );
  // Acceptance is a property of the run, so the tab follows the template's
  // acceptance policy rather than any node's kind.
  const hasAcceptancePanel =
    templateVersion?.definition.acceptance.policy === "member";
  const selectedIssuePolicy = selectedNode?.definition.issue_policy ?? "none";
  const selectedNodeAcceptsIssues = Boolean(
    selectedNode &&
      canManageSelectedNode &&
      isWorkflowNodeOpen(selectedNode.status) &&
      (
        selectedIssuePolicy === "dynamic" ||
        selectedIssuePolicy === "fixed_and_dynamic"
      ),
  );
  const selectedNodeIssues = (workflowIssuesQuery.data?.issues ?? []).filter(
    (issue) =>
      issue.workflow_context?.workflow_node_instance_id === selectedNode?.id,
  );
  const workflowIssueCount = workflowIssuesQuery.data?.issues.length ?? 0;
  const selectedNodeUsesIssues = selectedIssuePolicy !== "none";
  const showIssueSurface = issueScope === "all"
    ? workflowIssueCount > 0
    : selectedNodeUsesIssues || selectedNodeIssues.length > 0;
  const selectedRequiredTasks = (nodeQuery.data?.tasks ?? []).filter(
    (task) =>
      task.required && task.materialization_status !== "cancelled",
  );
  const selectedIssueById = new Map(
    selectedNodeIssues.map((issue) => [issue.id, issue]),
  );
  const requiredIssueOutcome =
    selectedNode?.definition.completion?.required_issue_outcome ?? "done";
  const issueSatisfiesNode = (issue: Issue) => {
    if (requiredIssueOutcome === "terminal") {
      return issue.status === "done" || issue.status === "cancelled";
    }
    return requiredIssueOutcome === "none" || issue.status === "done";
  };
  const completedRequiredIssues = selectedRequiredTasks.filter((task) => {
    if (!task.issue_id) return false;
    const issue = selectedIssueById.get(task.issue_id);
    if (!issue) return false;
    return issueSatisfiesNode(issue);
  }).length;
  // The panel lists what is still holding the node, not everything the node
  // produced — the board beside it already shows all of them, by status. A
  // second full list would be the same information asking to be read twice;
  // "what's left" is the thing the board cannot say.
  const blockingNodeIssues = selectedRequiredTasks
    .map((task) => (task.issue_id ? selectedIssueById.get(task.issue_id) : undefined))
    .filter((issue): issue is Issue => Boolean(issue) && !issueSatisfiesNode(issue!));
  const nodeOwners = (nodeQuery.data?.participants ?? []).filter(
    (participant) => participant.role === "owner",
  );
  const visibleWaitingReasons = selectedNode?.waiting_reasons.filter(
    (reason) =>
      reason.code !== "required_issue_not_done" &&
      reason.code !== "required_issue_cancelled",
  ) ?? [];
  const taskInterventions = (nodeQuery.data?.tasks ?? []).filter((task) => {
    if (task.materialization_status === "cancelled") return false;
    const resolution = nodeQuery.data?.executor_resolutions.find(
      (item) =>
        item.id === task.executor_resolution_id ||
        item.workflow_node_task_id === task.id,
    );
    const executionFailed = selectedNode?.waiting_reasons.some(
      (reason) =>
        reason.code === "direct_execution_failed" &&
        reason.field === task.task_key,
    ) ?? false;
    return task.materialization_status === "failed" ||
      (task.materialization_status === "materializing" &&
        selectedNode?.status === "blocked") ||
      executionFailed ||
      resolution?.status !== "resolved";
  });
  // A node in review is waiting on exactly one action, so open on it. Every
  // activity has a submission panel, so without this the reviewer always
  // landed on the handoff form and had to go looking for the verdict.
  // A node in review is waiting on exactly one action, so open on it.
  const sidebarDefaultTab =
    hasVerdictPanel && selectedNode?.status === "in_review"
      ? "verdict"
      : "delivery";
  const workflowIssueMenuActions = useMemo(() => [{
    id: "remove-from-workflow-node",
    label: t(($) => $.workbench.detach_issue_title),
    icon: Unlink,
    variant: "destructive" as const,
    isVisible: (issue: Issue) =>
      issue.workflow_context?.workflow_instance_id === instanceId,
    onSelect: (issue: Issue) => setDetachIssue(issue),
  }], [instanceId, t]);
  const completedWorkflowNodes = nodes.filter(
    (node) => node.status === "completed" || node.status === "skipped",
  ).length;
  const workflowProgressPercent = nodes.length > 0
    ? (completedWorkflowNodes / nodes.length) * 100
    : 0;
  const hostIssue = hostIssueQuery.data;

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

  const nodeSidebarContent = selectedNode && nodeQuery.data ? (
    <div className="-m-4 min-h-full">
      {/*
        The host issue is a reference here, not a subject. Its status, priority
        and assignee belong to it, not to the job this panel exists for —
        pushing the current node forward — and they used to take the top third
        of the panel before the node was even named. What stays is the pointer
        (so you always know which requirement you are inside) and the run's
        progress, which is the one host-level fact that frames the node.
      */}
      <section
        aria-labelledby="workflow-host-issue-heading"
        className="space-y-2 border-b bg-muted/15 px-4 py-3"
      >
        <div className="flex items-center justify-between gap-2">
          <div className="flex min-w-0 items-baseline gap-2">
            <span className={SECTION_HEADING}>
              {hostIssue
                ? t(($) => $.workbench.host_issue)
                : t(($) => $.workbench.run_context)}
            </span>
            <span
              id="workflow-host-issue-heading"
              className="truncate text-sm"
            >
              {hostIssue?.identifier ?? instance.title}
            </span>
            {reworkedActivityCount > 0 && (
              <span className="shrink-0 rounded-full bg-amber-500/15 px-1.5 py-0.5 text-[10px] font-medium text-amber-700 tabular-nums dark:text-amber-400">
                {t(($) => $.workbench.reworked_activities)}
                {" "}
                {reworkedActivityCount}
              </span>
            )}
          </div>
          {instance.host_issue_id && (
            <AppLink
              href={p.issueDetail(instance.host_issue_id)}
              className="inline-flex min-h-11 shrink-0 items-center gap-1 rounded-md px-1.5 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring sm:min-h-7"
              aria-label={t(($) => $.workbench.open_parent_issue)}
            >
              {t(($) => $.workbench.open_parent_issue)}
              <ArrowUpRight className="size-3.5" />
            </AppLink>
          )}
        </div>
        {nodes.length > 0 && (
          <div className="flex items-center gap-2">
            <div
              className="h-1 flex-1 overflow-hidden rounded-full bg-muted"
              role="progressbar"
              aria-valuemin={0}
              aria-valuemax={nodes.length}
              aria-valuenow={completedWorkflowNodes}
              aria-label={t(($) => $.workbench.workflow_progress)}
            >
              <div
                className="h-full rounded-full bg-primary transition-[width] motion-reduce:transition-none"
                style={{ width: `${workflowProgressPercent}%` }}
              />
            </div>
            <span className="shrink-0 text-xs tabular-nums text-muted-foreground">
              {completedWorkflowNodes}/{nodes.length}
            </span>
          </div>
        )}
        {/*
          Acceptance judges the run, not a node — which is why it stopped being
          an activity on the canvas. It was still a tab on every node, so the
          old model was the last thing left saying otherwise.
        */}
        {hasAcceptancePanel && (
          <AcceptancePanel
            instanceId={instanceId}
            targets={targetItems}
            pending={latestAcceptance?.status === "pending"}
            canDecide={canDecideAcceptance}
          />
        )}
        {/*
          Cancelling ends the run and diagnostics describe the run, so neither
          belongs under whichever node happens to be selected. Collapsed:
          troubleshooting is not a step in anyone's work.
        */}
        {canAdmin && (
          <details>
            <summary className={cn(SECTION_HEADING, "cursor-pointer outline-none focus-visible:ring-2 focus-visible:ring-ring")}>
              {t(($) => $.workbench.run_administration)}
            </summary>
            <div className="space-y-4 pt-3">
              <WorkflowDiagnosticsPanel diagnostics={diagnosticsQuery.data} />
              <WorkflowCancelPanel
                instanceId={instanceId}
                status={instance.status}
              />
            </div>
          </details>
        )}
      </section>

      <section
        aria-labelledby="workflow-current-node-heading"
        className="space-y-5 p-4"
      >
        <div>
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <p className={SECTION_HEADING}>
              {t(($) => $.workbench.current_node)}
            </p>
            <h2
              id="workflow-current-node-heading"
              className="mt-1 truncate text-base font-semibold"
            >
              {selectedNode.name}
            </h2>
          </div>
          <WorkflowStatusBadge
            status={workflowNodeDisplayStatus(selectedNode.status)}
          />
        </div>
        {selectedNode.definition.description && (
          <p className="mt-3 text-sm leading-relaxed text-muted-foreground">
            {selectedNode.definition.description}
          </p>
        )}
        {/*
          Who is on the hook stays visible; everything else about the node
          moved into the collapsed block at the bottom. "Who owns this" is the
          one fact people scan for without having gone looking for it.
        */}
        {nodeOwners.length > 0 && (
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <span className={SECTION_HEADING}>
              {t(($) => $.workbench.owners)}
            </span>
            {nodeOwners.map((participant) => (
              <span
                key={participant.id}
                className="inline-flex min-w-0 items-center gap-1.5 text-sm"
              >
                <ActorAvatar
                  actorType={participant.actor_type as "member" | "agent" | "squad"}
                  actorId={participant.actor_id}
                  size="sm"
                />
                <span className="truncate">
                  {actorName(
                    participant.actor_type as "member" | "agent" | "squad",
                    participant.actor_id,
                  )}
                </span>
              </span>
            ))}
          </div>
        )}
      </div>


      
      {taskInterventions.length > 0 && (
        <section className="space-y-2">
          <h3 className={SECTION_HEADING}>
            {t(($) => $.workbench.needs_attention)}
          </h3>
          {taskInterventions.map((task) => {
            const resolution = nodeQuery.data.executor_resolutions.find(
              (item) =>
                item.id === task.executor_resolution_id ||
                item.workflow_node_task_id === task.id,
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
                executionFailed={selectedNode.waiting_reasons.some(
                  (reason) =>
                    reason.code === "direct_execution_failed" &&
                    reason.field === task.task_key,
                )}
              />
            );
          })}
        </section>
      )}

      {/*
        What blocks the node, the rule that defines "blocked", and the action
        that clears it are one thought — so they are one block, above the tabs.
        They used to be three: a completion-rule section, an issue list, and a
        "node operations" card at the very bottom of the panel, which meant the
        primary action of the whole surface sat below four tabs of reference
        material.
      */}
      <WorkflowNodeIssues
        issues={blockingNodeIssues}
        canManage={canManageSelectedNode}
        attempt={selectedNode.attempt}
        blockers={visibleWaitingReasons.length > 0
          ? (
            <Alert>
              <AlertCircle />
              <AlertTitle>{t(($) => $.workbench.waiting)}</AlertTitle>
              <AlertDescription>
                <ul className="list-disc space-y-1 pl-4">
                  {visibleWaitingReasons.map((reason, index) => (
                    <li key={`${reason.code}-${index}`}>
                      {reason.message || reason.code}
                    </li>
                  ))}
                </ul>
              </AlertDescription>
            </Alert>
          )
          : null}
        rule={requiredIssueOutcome === "terminal"
          ? t(($) => $.workbench.completion_rule_terminal)
          : requiredIssueOutcome === "none"
            ? t(($) => $.workbench.completion_rule_none)
            : t(($) => $.workbench.completion_rule_done)}
        completed={completedRequiredIssues}
        total={requiredIssueOutcome === "none"
          ? 0
          : selectedRequiredTasks.length}
        action={
          <NodeTransitionPanel
            instanceId={instanceId}
            node={selectedNode}
            submissions={nodeQuery.data.submissions}
            canManage={canManageSelectedNode}
            canAdmin={canAdmin}
            instanceRunning={instance.status === "running"}
          />
        }
      />

      <Tabs key={selectedNode.id} defaultValue={sidebarDefaultTab}>
        <TabsList
          variant="line"
          className="w-full justify-start overflow-x-auto border-b"
        >
          <TabsTrigger value="delivery">
            <Send />
            {t(($) => $.workbench.tab_delivery)}
          </TabsTrigger>
          {hasVerdictPanel && (
            <TabsTrigger value="verdict">
              <FileCheck2 />
              {t(($) => $.workbench.verdict)}
            </TabsTrigger>
          )}
          <TabsTrigger value="history">
            <History />
            {t(($) => $.workbench.history)}
          </TabsTrigger>
        </TabsList>
        {/*
          One delivery, not two tabs. The node's business output is an
          artifact and its conclusion is the handoff summary — halves of the
          same handover, which is why they were never worth a tab each.
        */}
        <TabsContent value="delivery" className="space-y-6 pt-4">
          {hasSubmissionPanel && (
            <SubmissionPanel
              instanceId={instanceId}
              node={selectedNode}
              submissions={nodeQuery.data.submissions}
              tasks={nodeQuery.data.tasks}
              actorOptions={actorOptions}
              canManage={canManageSelectedNode}
              branchDuty={templateVersion
                ? branchChoiceDuty(
                  templateVersion.definition.nodes,
                  templateVersion.definition.edges,
                  selectedNode.node_key,
                )
                : null}
            />
          )}
          <ArtifactPanel
            instanceId={instanceId}
            node={selectedNode}
            canManage={canManageSelectedNode}
          />
        </TabsContent>
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
        <TabsContent value="history" className="pt-4">
          <WorkflowHistoryPanel
            events={eventsQuery.data?.events ?? []}
            loading={eventsQuery.isLoading}
          />
        </TabsContent>
      </Tabs>

      {/* The task rows behind this node's issues — node-scoped debugging. */}
      {canAdmin && (
        <div className="border-t pt-3">
          <div>
            <details className="rounded-xl border bg-muted/10">
              <summary className="cursor-pointer px-4 py-3 text-sm font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring">
                {t(($) => $.workbench.internal_tasks)}
              </summary>
              <div className="space-y-2 border-t p-3">
                {nodeQuery.data.tasks.map((task) => {
                  const resolution =
                    nodeQuery.data.executor_resolutions.find(
                      (item) =>
                        item.id === task.executor_resolution_id ||
                        item.workflow_node_task_id === task.id,
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
                      executionFailed={selectedNode.waiting_reasons.some(
                        (reason) =>
                          reason.code === "direct_execution_failed" &&
                          reason.field === task.task_key,
                      )}
                    />
                  );
                })}
                {nodeQuery.data.tasks.length === 0 && (
                  <p className="py-4 text-center text-sm text-muted-foreground">
                    {t(($) => $.workbench.no_tasks)}
                  </p>
                )}
              </div>
            </details>
          </div>
        </div>
      )}

      {/*
        Node configuration — who owns it, how long before it is
        flagged. Reference material: true for the whole run, needed once, and
        never the reason someone opened this panel. It sat between the node
        title and the action, so the primary control of the surface began below
        four rows of facts nobody had asked for. Collapsed, and below.
      */}
      <details className="border-t pt-3">
        <summary className={cn(SECTION_HEADING, "cursor-pointer outline-none focus-visible:ring-2 focus-visible:ring-ring")}>
          {t(($) => $.workbench.node_configuration)}
        </summary>
        <dl className="grid grid-cols-2 gap-x-4 gap-y-3 border-t py-4">
        <div className="min-w-0">
          <dt className={SECTION_HEADING}>{t(($) => $.workbench.owners)}</dt>
          <dd className="mt-1 flex flex-wrap gap-2">
            {nodeQuery.data.participants
              .filter((participant) => participant.role === "owner")
              .map((participant) => (
                <span
                  key={participant.id}
                  className="inline-flex min-w-0 items-center gap-1.5 text-sm"
                >
                  <ActorAvatar
                    actorType={participant.actor_type as "member" | "agent" | "squad"}
                    actorId={participant.actor_id}
                    size="sm"
                  />
                  <span className="truncate">
                    {actorName(
                      participant.actor_type as "member" | "agent" | "squad",
                      participant.actor_id,
                    )}
                  </span>
                </span>
              ))}
            {!nodeQuery.data.participants.some(
              (participant) => participant.role === "owner",
            ) && (
              <span className="text-sm text-muted-foreground">
                {t(($) => $.runs.none)}
              </span>
            )}
          </dd>
        </div>
        {/* Omitted entirely when empty — a heading over "none" spends a row to
            say nothing. */}
        {nodeQuery.data.participants.some(
          (participant) => participant.role !== "owner",
        ) && (
          <div className="min-w-0">
            <dt className={SECTION_HEADING}>
              {t(($) => $.workbench.participants)}
            </dt>
            <dd className="mt-1 flex flex-wrap gap-2 text-sm">
              {nodeQuery.data.participants
                .filter((participant) => participant.role !== "owner")
                .map((participant) => (
                  <span key={participant.id} className="truncate">
                    {actorName(
                      participant.actor_type as "member" | "agent" | "squad",
                      participant.actor_id,
                    )}
                  </span>
                ))}
            </dd>
          </div>
        )}
        {selectedNode.definition.timeout_minutes && (
          <div className="min-w-0">
            <dt className={SECTION_HEADING}>
              {t(($) => $.workbench.timeout_label)}
            </dt>
            <dd className="mt-1 text-sm tabular-nums">
              {t(($) => $.workbench.timeout, {
                minutes: selectedNode.definition.timeout_minutes,
              })}
            </dd>
          </div>
        )}
      </dl>
      </details>


      </section>
    </div>
  ) : (
    <p className="py-10 text-center text-sm text-muted-foreground">
      {t(($) => $.workbench.select_node)}
    </p>
  );

  // While an issue is open it takes over the sidebar rather than stacking a
  // third column: the board keeps the rest of the width, and the comment
  // thread gets a column wide enough to actually read.
  const sidebarContent = openIssueId
    ? (
      <WorkflowIssuePanel
        issueId={openIssueId}
        issues={selectedNodeIssues}
        onClose={() => setOpenIssueId(null)}
        onNavigate={setOpenIssueId}
      />
    )
    : nodeSidebarContent;

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <CollectionPageHeader
        icon={GitBranch}
        title={instance.title || hostIssueQuery.data?.title ||
          t(($) => $.workbench.title)}
        description={(
          <span className="flex flex-wrap items-center gap-2">
            <span>
              {hostIssueQuery.data?.identifier ?? t(($) => $.runs.standalone)}
            </span>
            {templateVersion && (
              <AppLink
                href={p.workflow(instance.workflow_id)}
                className="rounded-sm underline-offset-4 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                {t(($) => $.workbench.workflow_version, {
                  name: templateQuery.data?.workflow.name ?? instance.workflow_name,
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
            <Button
              size="icon-sm"
              variant={sidebarOpen ? "secondary" : "ghost"}
              onClick={handleToggleSidebar}
              aria-label={t(($) => $.workbench.node_details)}
              title={t(($) => $.workbench.node_details)}
            >
              <PanelRight />
            </Button>
          </>
        )}
      />
      <main
        data-tab-scroll-root="workflow-workbench"
        className="flex min-h-0 flex-1 flex-col overflow-hidden"
      >
        <ResizablePanelGroup
          orientation="horizontal"
          className="min-h-0 flex-1"
          defaultLayout={defaultLayout}
          onLayoutChanged={onLayoutChanged}
        >
          <ResizablePanel id="issues" minSize="50%">
            <div className="flex h-full min-w-0 flex-col">
        <section
          data-testid="workflow-activity-map"
          className="shrink-0 border-b bg-muted/15 px-4 py-3"
        >
          <div className="mx-auto max-w-[100rem]">
            <h2 className={cn(SECTION_HEADING, "mb-2 flex items-center gap-2")}>
              <CircleDot className="size-3.5" />
              {t(($) => $.workbench.activity_map)}
            </h2>
            <WorkflowCanvas
              definition={templateVersion?.definition}
              nodes={nodes}
              selectedId={selectedNodeId}
              onSelect={setSelectedNodeId}
              minHeight={200}
            />
          </div>
        </section>

        {templateVersion &&
          (instance.status === "needs_setup" ||
            instance.next_action === "configure_roles" ||
            instance.next_action === "configure_executor") && (
          <section className="max-h-64 shrink-0 overflow-y-auto border-b px-4 py-3">
            <RoleSetupPanel
              instanceId={instanceId}
              roles={templateVersion.definition.roles}
              currentAssignments={detailQuery.data?.role_assignments ?? []}
              actorOptions={actorOptions}
              canConfigure={canConfigureRoles}
              variant={
                instance.status === "needs_setup" ? "setup" : "reassign"
              }
            />
          </section>
        )}

            <section
              data-testid="workflow-issue-workspace"
              className="flex min-h-0 min-w-0 flex-1 flex-col"
            >
              {/*
                Cards stay real links — modified clicks still open a tab — but
                a plain click reads the issue in the sidebar so the board and
                the user's place in it survive.
              */}
              {showIssueSurface ? (
                <IssueOpenProvider onOpenIssue={setOpenIssueId}>
                <IssueSurface
                scope={issueSurfaceScope}
                modes={issueScope === "all"
                  ? ["list", "board", "swimlane"]
                  : ["board", "list", "swimlane"]}
                surfaceKey={`workflow:${instanceId}:${issueScope}`}
                allowCreate={selectedNodeAcceptsIssues}
                onCreateIssue={() => setCreateIssueOpen(true)}
                menuActions={workflowIssueMenuActions}
                batchToolbar="list"
                renderHeader={({ controller }) => {
                  const scopedIssues = controller.surfaceIssues;
                  const done = scopedIssues.filter(
                    (issue) => issue.status === "done",
                  ).length;
                  return (
                    <div className="flex h-14 shrink-0 items-center justify-between gap-3 overflow-x-auto border-b px-4">
                      <div className="flex shrink-0 items-center gap-3">
                        <div className="flex rounded-lg bg-muted p-0.5">
                          <button
                            type="button"
                            onClick={() => setIssueScope("current")}
                            className={cn(
                              "min-h-11 rounded-md px-3 py-1 text-xs text-muted-foreground sm:min-h-8",
                              issueScope === "current" &&
                                "bg-background text-foreground shadow-xs",
                            )}
                          >
                            {selectedNode?.name ??
                              t(($) => $.workbench.current_issues)}
                          </button>
                          <button
                            type="button"
                            onClick={() => setIssueScope("all")}
                            className={cn(
                              "min-h-11 rounded-md px-3 py-1 text-xs text-muted-foreground sm:min-h-8",
                              issueScope === "all" &&
                                "bg-background text-foreground shadow-xs",
                            )}
                          >
                            {t(($) => $.workbench.all_issues)}
                          </button>
                        </div>
                        <span className="hidden text-xs text-muted-foreground lg:inline">
                          {t(($) => $.workbench.issue_progress, {
                            done,
                            total: scopedIssues.length,
                          })}
                        </span>
                      </div>
                      <div className="flex shrink-0 items-center gap-2">
                        {selectedNodeAcceptsIssues && (
                          <Button
                            size="sm"
                            onClick={() => setCreateIssueOpen(true)}
                          >
                            <Plus />
                            {t(($) => $.actions.create_issue)}
                          </Button>
                        )}
                        <IssueDisplayControls scopedIssues={scopedIssues} />
                      </div>
                    </div>
                  );
                }}
                renderEmpty={() => (
                  <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 text-muted-foreground">
                    <ListChecks className="size-10 opacity-40" />
                    <p className="text-sm">{t(($) => $.workbench.no_issues)}</p>
                    {selectedNodeAcceptsIssues && (
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => setCreateIssueOpen(true)}
                      >
                        <Plus />
                        {t(($) => $.actions.create_issue)}
                      </Button>
                    )}
                  </div>
                )}
              />
                </IssueOpenProvider>
              ) : (
                <div className="flex min-h-0 flex-1 items-center justify-center p-6">
                  <div className="w-full max-w-xl rounded-xl border bg-muted/20 p-5">
                    <div className="flex items-start gap-3">
                      <div className="rounded-lg bg-primary/10 p-2 text-primary">
                        <Users className="size-5" />
                      </div>
                      <div className="min-w-0 flex-1">
                        <h3 className="text-sm font-medium">
                          {t(($) => $.workbench.direct_execution)}
                        </h3>
                        <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
                          {t(($) => $.workbench.direct_execution_help)}
                        </p>
                        {nodeOwners.length > 0 && (
                          <div className="mt-3 flex flex-wrap gap-2">
                            {nodeOwners.map((participant) => (
                              <span
                                key={participant.id}
                                className="inline-flex items-center gap-1.5 rounded-full border bg-background px-2.5 py-1 text-xs"
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
                          </div>
                        )}
                        {workflowIssueCount > 0 && (
                          <Button
                            className="mt-4"
                            size="sm"
                            variant="outline"
                            onClick={() => setIssueScope("all")}
                          >
                            <ListChecks />
                            {t(($) => $.workbench.view_all_issues, {
                              count: workflowIssueCount,
                            })}
                          </Button>
                        )}
                      </div>
                    </div>
                  </div>
                </div>
              )}
            </section>
            </div>
          </ResizablePanel>

          {!isMobile && <ResizableHandle />}
          {!isMobile && (
            <ResizablePanel
              id="sidebar"
              {...rightSidebarPanelMotionProps}
              data-right-sidebar-motion={
                desktopSidebarMotionEnabled ? "enabled" : undefined
              }
              defaultSize={desktopSidebarOpen ? 360 : 0}
              minSize={300}
              maxSize={480}
              collapsible
              groupResizeBehavior="preserve-pixel-size"
              panelRef={sidebarRef}
              onResize={handleDesktopSidebarResize}
            >
              <AnimatedRightSidebar
                open={desktopSidebarVisualOpen}
                motionEnabled={desktopSidebarMotionEnabled}
              >
                {sidebarContent}
              </AnimatedRightSidebar>
            </ResizablePanel>
          )}
        </ResizablePanelGroup>

        {isMobile && (
          <Sheet open={mobileSidebarOpen} onOpenChange={setMobileSidebarOpen}>
            <SheetContent
              side="right"
              showCloseButton={false}
              className="w-[min(92vw,24rem)] overflow-y-auto p-4"
            >
              {sidebarContent}
            </SheetContent>
          </Sheet>
        )}

        <Dialog open={createIssueOpen} onOpenChange={setCreateIssueOpen}>
          <DialogContent className="sm:max-w-2xl">
            <DialogHeader>
              <DialogTitle>
                {t(($) => $.workbench.add_dynamic_issue)}
              </DialogTitle>
              <DialogDescription>
                {t(($) => $.workbench.add_dynamic_issue_help)}
              </DialogDescription>
            </DialogHeader>
            {selectedNode && (
              <DynamicIssuePanel
                instanceId={instanceId}
                node={selectedNode}
                actorOptions={actorOptions}
                canManage={canManageSelectedNode}
                showIntro={false}
                onCreated={() => setCreateIssueOpen(false)}
              />
            )}
          </DialogContent>
        </Dialog>
        {detachIssue && (
          <WorkflowIssueDetachDialog
            instanceId={instanceId}
            issue={detachIssue}
            onClose={() => setDetachIssue(null)}
          />
        )}
      </main>
    </div>
  );
}
