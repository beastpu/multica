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
  assessment trigger, evidence read, and human review routes.
- `server/internal/handler/agent.go` handles Operations agent-fix APIs and
  evidence access. Agent access is task-scoped: an agent token can read
  only evidence for the same workspace and Feishu binding recorded in the
  assessment task context.

## Parser and output contract

- `server/internal/service/agent_fix_assessment.go` implements
  `parseP4AssessmentTaskOutput`. It reads only `agent_task_queue.result.output`,
  accepts a raw JSON object or exactly one fenced `json` block, rejects wrapper
  prose/multiple blocks and unknown fields, validates confidence range, and
  defaults missing prediction fields to `unknown`.
- `P4AssessmentService.CompleteTask` writes parsed assessment output only to
  `agent_fix_p4_assessment`. Parser failure marks the assessment failed with a
  parser warning.
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
