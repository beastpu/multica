# P4/Swarm assessment source map

Evidence layer for `multica-agent-fix-p4-assessment/SKILL.md`. Re-derive line
numbers before depending on an exact location; this file records source anchors
for the behavior contracts the skill teaches.

## Assessment task and evidence

- `server/internal/service/agent_fix_assessment.go` defines
  `P4AssessmentTaskType = "agent_fix_p4_assessment"` and
  `P4AssessmentPromptVersion = "p4-assessment-v1"`.
- `P4AssessmentService.Trigger` validates the binding, writes/refreshes
  `agent_fix_p4_assessment` as pending, creates an isolated
  `agent_task_queue` row, and stores task context with `type`, `workspace_id`,
  `issue_id`, `feishu_binding_id`, `mode: assess_only`, and `prompt_version`.
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

- `server/cmd/server/router.go` registers operations routes, including
  assessment trigger, evidence read, the result submit route, and human review
  routes.
- `server/internal/handler/agent.go` handles Operations agent-fix APIs and
  evidence access. Agent access is task-scoped: an agent token can read
  only evidence for the same workspace and Feishu binding recorded in the
  assessment task context. `SubmitAgentFixP4Assessment` gates the result
  submit endpoint with the same `requestTaskCanReadP4Evidence` scope, so only
  the binding's own assessment task may write its result.

## Batch worker contract (pull / submit-by-ref)

- `GET /api/operations/assessments/pending?limit=N`
  (`Handler.ListPendingP4Assessments` → `P4AssessmentService.LeasePending`)
  atomically leases up to N claimable rows (`pending`/`failed`/`stale`, or
  `running` with an expired lease) to the caller's task via
  `LeaseP4AssessmentsPending` (SKIP LOCKED), and returns each with an opaque
  `ref` (internally the binding UUID), the issue summary, `lease_expires_at`
  (`P4AssessmentLeaseDuration`, 30 minutes), and inlined evidence from
  `P4AssessmentService.Evidence`. Rows whose evidence fails to build are
  released back to the pool (`ReleaseP4AssessmentLease`).
- `POST /api/operations/assessments/result`
  (`Handler.SubmitP4AssessmentResultByRef` →
  `P4AssessmentService.SubmitBatchResult`) strips `ref`, runs the same
  `validateP4AssessmentPayload`, and completes the row keyed on
  (workspace, binding, leasing task) via `CompleteP4AssessmentFromBinding`.
  A reclaimed/unknown ref returns `ErrP4AssessmentRefNotLeased` → HTTP 409.
- Both endpoints require an agent actor whose `X-Task-ID` task belongs to it
  and to the workspace (`requestBatchAssessmentTask`), and are fail-closed on
  the same `P4AssessmentAllowlist` (`P4_ASSESSMENT_WORKSPACE_ALLOWLIST`) that
  gates auto-trigger.
- `P4AssessmentService.Trigger` no longer spawns per-binding agent tasks: it
  upserts the row to `pending` and the batch worker consumes the pool, so
  assessment work does not fan out into `agent_task_queue`.

## Result submit and output contract

- The batch path above is the primary ingestion route. The legacy per-binding
  endpoint `POST /api/operations/agent-fixes/{binding_id}/p4-assessment/result`
  remains for in-flight per-binding tasks: the agent POSTs the bare result
  JSON (via `curl --data-binary @result.json` with the task-env headers — the
  skill is CLI-version independent by design), the server validates it, and a
  400 returns the exact validation problem so the agent can self-correct and
  resubmit. This avoids parsing a free-text agent message.
- `server/internal/service/agent_fix_assessment.go` shares one validator,
  `validateP4AssessmentPayload`: it accepts one JSON object, rejects unknown
  fields, validates the prediction enums and confidence range, requires
  `swarm_reviews`/`warnings` to be arrays and `evidence` an object, and defaults
  missing predictions to `unknown`. The endpoint (`SubmitResult`) and the
  task-output fallback (`parseP4AssessmentTaskOutput` → `CompleteTask`) both run
  it, so the contract is identical either way.
- `P4AssessmentService.SubmitResult`/`writeCompletedAssessment` write the
  validated result to `agent_fix_p4_assessment`. Once a row is `completed`, the
  guarded `FailP4AssessmentFromTask` (`WHERE assessment_status <> 'completed'`)
  will not knock a submitted result back to `failed` when the task later ends
  and the output-parse fallback finds nothing to parse.
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
  assessment table queries and task-isolation filters excluding
  `agent_fix_p4_assessment` from ordinary issue task queries, latest-run views,
  session resume, cancellation, and dedup paths.
- Read-only enforcement is server-side, not advisory. `CreateP4AssessmentTask`
  in `agent.sql` stamps `handoff_note` with the read-only assessment
  instructions (built by `p4AssessmentHandoffNote` in
  `agent_fix_assessment.go`); a stale daemon that predates the dedicated
  assessment prompt still renders `handoff_note` on the normal assignment path,
  which is how the server steers it into JSON output without a client update.
  Independently, `Handler.isAnalysisTaskActor` /
  `Handler.rejectAnalysisTaskWrite` (`internal/handler/issue.go`) reject every
  issue mutation from an analysis-category task in `UpdateIssue`,
  `BatchUpdateIssues`, and comment creation (`internal/handler/comment.go`), so
  even a stale daemon running the task as a normal fix cannot change status,
  edit fields, or post comments.

## Product design baseline

- `docs/agent-fix-p4-assessment-next-design.md` is the current baseline: Multica
  server does not actively query inner-network Swarm/P4 for assessment; the
  agent may perform read-only inner-network P4/Swarm inspection; assessment
  writes only `agent_fix_p4_assessment`.
- `docs/agent-fix-p4-assessment-api-workflow.md` defines the evidence endpoint,
  task context, server-state boundary, parser boundary, and human review API
  ownership.
- `docs/agent-fix-p4-external-dependencies.md` records external-data limits:
  Feishu/Meego fields are already-ingested compatibility evidence, and missing
  P4/Swarm evidence should produce `unknown` plus warnings rather than guesses.

## Dashboard metric contract

- `packages/views/dashboard/operations-metrics.ts` computes the operations
  dashboard KPIs from assessment rows. `isVerifiableOutput` (the quality-pipeline
  gate: judged / pass rate / coverage) requires a completed assessment and AI
  output evidence (`ai_shelved_cls` or `swarm_reviews`) — a committed CL is NOT
  required, because quality judges the plan's code, not whether it shipped. The
  pass rate is a single plan-quality rate (`likely_correct` / judged). Delivery
  is a separate axis: `contributionRate` and the delivery-composition bar are
  driven by `delivery_attribution_prediction` (`ai_delivered` / `ai_assisted`),
  which is why attribution must not be guessed.
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
