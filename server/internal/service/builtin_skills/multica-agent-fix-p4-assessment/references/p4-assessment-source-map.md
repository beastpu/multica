# P4/Swarm assessment source map

Evidence layer for `multica-agent-fix-p4-assessment/SKILL.md`. Re-derive line
numbers before depending on an exact location; this file records source anchors
for the behavior contracts the skill teaches.

## Assessment task and evidence

- `server/internal/service/agent_fix_assessment.go` defines
  `P4AssessmentTaskType = "agent_fix_p4_assessment"`,
  `P4AssessmentPromptVersion = "p4-assessment-v1"`, and
  `CapabilityP4Assessment = "p4_assessment"` (the workspace capability key).
- `P4AssessmentService.Trigger` (plan C-1 native task flow) is fail-closed on
  the workspace's `workspace_agent_capability` row (migration 135; configured
  via `GET/PUT/DELETE /api/workspaces/{id}/capabilities/p4_assessment`). For
  each accepted run it: validates the binding, upserts
  `agent_fix_p4_assessment` to pending, creates the run's ASSESSMENT ISSUE
  (derived agent_work issue) assigned to the capability agent, creates the
  native `agent_task_queue` row hanging on that assessment issue
  (`CreateP4AssessmentTask`: fresh session, stale-daemon `handoff_note`),
  and points `assessment_task_id` at it
  (`SetP4AssessmentTask`) — all in one transaction. Task context carries
  `type`, `workspace_id`, `issue_id` (the REAL defect issue),
  `feishu_binding_id`, `mode: assess_only`, and `prompt_version`.
  Assignment is dispatch: the daemon claims the queued task through the
  ordinary prepare-lease channel; there is no dispatcher loop. The native
  task flow is the ONLY execution channel — the batch pull/lease/submit-by-ref
  contract and the `P4_ASSESSMENT_WORKSPACE_ALLOWLIST` env gate were removed
  in C-2.
- `P4AssessmentService.Evidence` backs
  `GET /api/operations/agent-fixes/{binding_id}/p4-evidence`. It returns
  binding, issue, task summaries, selected Multica comments, optional Feishu
  Project comments fetched through the configured integration plugin, and linked
  Perforce review summaries.
- The same evidence builder includes `issue.metadata` and derives
  `cl_candidates[]` / `review_candidates[]` from structured issue metadata
  such as `flow_cl` / `flow_review`, external fields, selected Multica
  comments, Feishu Project comments when available, and linked Perforce review
  rows. Candidate entries carry `source` and `field` so the agent can prioritize
  lookup without treating hints as proof.
- Evidence `binding.external_status_label`, `binding.mapped_status`,
  `binding.external_fields`, and `issue.description` all come from Multica DB
  rows produced by Feishu Project sync. `external_comments[]` is fetched at
  assessment evidence time through `FeishuProjectClient.ListWorkItemComments`.
  The evidence builder also uses `FeishuProjectClient.ListRelatedWorkItems` to
  follow `_field_linked_story` to a story/requirement work item and include its
  comments for hotfix flows. Failures are returned as
  `external_evidence_errors[]` instead of failing the whole evidence response.
- `p4EvidenceReviewMaps` projects linked Swarm evidence, including `changes[]`,
  `commits[]`, `swarm_branch`, `event_type`, `sent_at`, and restricted
  `raw_payload`. `changes[]` are review-attached changes, while `commits[]` /
  committed CL evidence are the submitted-change facts.
- `p4EvidenceTaskMaps` returns only controlled task summary fields and omits raw
  task `result`, `context`, `session_id`, `work_dir`, runtime internals, and
  secrets.

## HTTP routes and auth boundary

- `server/cmd/server/router.go` registers operations routes (assessment
  trigger, evidence read, result submit, human review) and the workspace
  capability config routes
  (`/api/workspaces/{id}/capabilities/{capability}`,
  `server/internal/handler/workspace_capability.go` — member-level; PUT
  validates the agent belongs to the workspace, is not archived, and runs on a
  daemon-served `local` runtime).
- `server/internal/handler/agent.go` handles Operations agent-fix APIs and
  evidence access. Agent access is task-scoped: an agent token can read
  only evidence for the same workspace and Feishu binding recorded in the
  assessment task context (`requestTaskCanReadP4Evidence`).
  `SubmitAgentFixP4Assessment` gates the result submit endpoint with the same
  scope, so only the binding's own assessment task may write its result.

## Result submit and output contract

- `POST /api/operations/agent-fixes/{binding_id}/p4-assessment/result` is the
  authoritative ingestion path: the agent POSTs the bare result JSON (via
  `curl --data-binary @result.json` with the task-env headers — the skill is
  CLI-version independent by design), the server validates it, and a 400
  returns the exact validation problem so the agent can self-correct and
  resubmit. This avoids parsing a free-text agent message.
- `server/internal/service/agent_fix_assessment.go` shares one validator,
  `validateP4AssessmentPayload`: it accepts one JSON object, rejects unknown
  fields, validates the prediction enums and confidence range, requires
  `swarm_reviews`/`warnings` to be arrays and `evidence` an object, and defaults
  missing predictions to `unknown`. The endpoint (`SubmitResult`) and the
  task-output fallback (`parseP4AssessmentTaskOutput` → `CompleteTask`) both run
  it, so the contract is identical either way.
- `P4AssessmentService.SubmitResult`/`writeCompletedAssessment` write the
  validated result to `agent_fix_p4_assessment`, keyed on the task the Trigger
  stamped (`assessment_task_id`). Once a row is `completed`, the guarded
  `FailP4AssessmentFromTask` (`WHERE assessment_status <> 'completed'`) will
  not knock a submitted result back to `failed` when the task later ends and
  the output-parse fallback finds nothing to parse.
- Implementation comparison is stored inside the parsed `evidence` JSON object
  rather than as dedicated columns. The shipped skill constrains
  `evidence.implementation_comparison.method_equivalence` to the binary values
  `equivalent` or `not_equivalent`; top-level `quality_prediction` and
  `delivery_attribution_prediction` remain the persisted enum fields used by
  Operations display and filtering.

## Human review boundary

- `agent_fix_review` is human review state. `PATCH
  /api/operations/agent-fixes/{binding_id}/review` writes it through the human
  review handler path.
- Assessment completion code does not write `agent_fix_review`, does not update
  issue status, and does not call Feishu/Meego, P4, or Swarm write APIs.

## P4/Swarm evidence and side effects

- `server/internal/handler/perforce_webhook.go` exposes
  `POST /api/webhooks/p4-swarm`.
- `server/internal/handler/perforce.go` processes Swarm webhook payloads into
  `perforce_review` / `issue_perforce_review`, including stored `changes[]`,
  `commits[]`, `swarm_branch`, `event_type`, `sent_at`, and raw webhook payload
  evidence. That path can also advance issue status when a committed review
  with close intent is settled, so assessment must treat it as evidence only
  and must not reuse it as its state machine.
- Runtime-side live P4/Swarm checks are intentionally agent-owned. The Multica
  server has no Swarm/P4 credential for assessment; the skill therefore teaches
  read-only commands such as `p4 describe -S` and treats Swarm API
  `Unauthorized` as `swarm_lookup_unavailable`, not as a reason to mutate state.
- `server/pkg/db/queries/agent.sql` and generated sqlc code contain the
  assessment table queries. Workflow isolation is structural: assessment
  tasks hang on their own derived agent_work projection issue, so per-issue
  queries (latest-run, session resume, cancellation, dedup) never meet them
  through a real issue, and the operations feed spine excludes issues
  carrying the reserved `metadata.agent_work` marker.
- Read-only enforcement is server-side, not advisory. `CreateP4AssessmentTask`
  in `agent.sql` stamps `handoff_note` with the read-only assessment
  instructions (built by `p4AssessmentHandoffNote` in
  `agent_fix_assessment.go`); a stale daemon that predates the dedicated
  assessment prompt still renders `handoff_note` on the normal assignment path,
  which is how the server steers it into JSON output without a client update.
  Independently, `Handler.analysisTaskForRequest` /
  `Handler.rejectAnalysisTaskWrite` (`internal/handler/issue.go`) reject every
  issue mutation from a derived-work task in `UpdateIssue`,
  `BatchUpdateIssues`, and comment creation (`internal/handler/comment.go`).
  The actor anchor is the task's own issue carrying the server-reserved
  `metadata.agent_work` marker — the single derived-work marker in the
  system. One precise carve-out in `CreateComment`: comment creation is
  allowed only when the
  target issue carries `metadata.agent_work` AND is the task's OWN issue
  (`task.issue_id == issue.id`) — the run's assessment issue, which exists to
  hold the worker's narration. Commenting on any other issue — real or another
  run's assessment issue — is rejected. Status/field/assignee writes stay
  rejected even there, and the reserved metadata key itself is rejected by the
  user metadata API (`internal/handler/issue_metadata.go`), so a real issue
  can never be spoofed into the carve-out.

## Assessment issue (agent_work projection) and run lifecycle

- `docs/agent-fix-p4-assessment-issue-design.md` defines the derived-work
  primitive and the plan C native task flow.
  `server/internal/service/agent_fix_assessment_projection.go` implements the
  P4 instantiation: `P4AssessmentService.Trigger` creates one assessment issue
  per run inside its transaction (`CreateAgentWorkIssue` stamps
  `metadata.agent_work` with `kind=p4_assessment`, `source_issue_id`,
  `source_ref`, `trigger`, `extra.prompt_version`, and assigns the capability
  agent) and points `agent_fix_p4_assessment.assessment_issue_id` at it
  (`SetP4AssessmentIssue`); a force re-run creates a NEW issue (and task) and
  repoints, leaving the previous run's issue untouched.
- Run status is a SINGLE-DIRECTION projection of the task lifecycle
  (`server/internal/service/task.go` hooks →
  `P4AssessmentService.StartFromTask` / `FailFromTask` /
  `writeCompletedAssessment`): task running → row running + issue
  `in_progress` (`StartP4AssessmentFromTask`), terminal task failure → row
  failed + issue `cancelled` (issue.status has no `failed` value) + server
  failure comment, result submit → row completed + issue `done` + server
  result-summary comment (`p4AssessmentResultComment`). Server comments are
  authored as the task's agent and are best-effort (never roll back a
  transition). Rows with NULL `assessment_issue_id` skip projection. No
  reverse path exists — nothing on the issue drives the row.
- `agent_fix_p4_assessment` remains the single source of truth: operations
  KPIs read only the queue table, never the assessment issue's status.

## Product design baseline

- `docs/agent-fix-p4-assessment-next-design.md` is the current baseline: Multica
  server does not actively query inner-network Swarm/P4 for assessment; the
  agent may perform read-only inner-network P4/Swarm inspection; assessment
  writes only `agent_fix_p4_assessment`.
- `docs/agent-fix-p4-assessment-api-workflow.md` defines the evidence endpoint,
  task context, server-state boundary, parser boundary, and human review API
  ownership (its batch pull/submit chapter is retired by plan C).
- `docs/agent-fix-p4-external-dependencies.md` records external-data limits:
  Feishu/Meego fields are already-ingested compatibility evidence, and missing
  P4/Swarm evidence should produce `unknown` plus warnings rather than guesses.

## Dashboard metric contract

- `packages/views/dashboard/operations-metrics.ts` computes the operations
  dashboard KPIs from assessment rows. `isVerifiableOutput` (the quality-pipeline
  gate: judged / pass rate / coverage) requires a completed assessment and AI
  output evidence (`ai_shelved_cls` or `swarm_reviews`) — a committed CL is NOT
  required, because quality judges the plan's code, not whether it shipped. The
  headline is a strict nesting chain — contribution (AI produced a plan /
  外部完成) → coverage (judged / produced) → pass rate (`likely_correct` /
  judged). Delivery attribution (`ai_delivered` / `ai_assisted` from
  `delivery_attribution_prediction`) drives the analysis tab's attribution
  distribution and the composition partition, which is why attribution must not
  be guessed.
- Delivery-side metrics and the missing-CL process gap read the structured
  `*_committed_cls` arrays, not `summary` / `prediction_reasons`. This is why
  the SKILL requires a verified submitted CL — including one confirmed only via
  read-only P4 describe/filelog — to be written into `swarm_committed_cls` /
  `external_committed_cls`; a submitted CL narrated only in prose is invisible
  to these metrics and undercounts delivery.
- `blockedWarningFamily` classifies access-blocked warnings by matching
  `unavailable|not_found|unreachable|unauthorized` and grouping into
  auth (any `unauthorized`), identification (`not_found` + cl/shelve/branch),
  swarm, p4, and evidence_endpoint families. This is why the SKILL's
  prediction policy requires a canonical `*_unavailable` warning whenever
  evidence access is blocked, and requires `quality_prediction: "unknown"`
  when no AI output evidence was verified.
- `hasMissingExternalClWarning` counts the "missing human CL" process gap:
  completed assessments carrying `missing_external_cl` with no committed CL
  evidence. This is why the SKILL requires `missing_external_cl` whenever the
  external item is done but no submitted CL was found in any evidence source.
