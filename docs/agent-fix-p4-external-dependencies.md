# AI Fix P4/Swarm External Dependencies

> Status: Draft
> Last updated: 2026-06-29
> Related design: `docs/agent-fix-prefill-flow.md`
> API/workflow: `docs/agent-fix-p4-assessment-api-workflow.md`

This document tracks external-system facts and open questions for the AI fix
P4/Swarm assessment flow. The main backend flow should not depend on unverified
external assumptions without recording them here.

## Feishu / Meego

### Status Mapping

- The statistical denominator is a Feishu/Meego binding whose external status
  maps to Multica local status `done`.
- Candidate selection must use integration status mapping, not external status
  label heuristics such as `done`, `closed`, or `完成`.
- Current code has the newer `work_item_types[].status_mapping` model. Legacy
  top-level `status_mapping` may still exist in older data and must be handled
  deliberately when implementing candidate queries.
- Scheduled sync only pulls work items whose external status is in the configured
  mapped status set. Targeted sync by work item ID bypasses status and updated_at
  filters.

### Synced Binding Data

Current Feishu sync stores:

- `workspace_id`
- `integration_id`
- `issue_id`
- `project_key`
- `work_item_type`
- `work_item_id`
- `external_identifier`
- `external_url`
- `external_status_label`
- `last_external_updated_at`
- `last_synced_at`
- `external_fields`

It also updates the Multica issue title, description, status, priority,
assignee, project, attachments, labels, and subscribers.

### External Fields

Known fields from the warpath3 production workspace:

| Field key | Display name | Type | Use |
|---|---|---|---|
| `field_6e908d` | `提交记录` | `text` | Candidate source for final CL / `external_committed_cls` |
| `field_d7788a` | `开发分支（QA不用手动改，这个字段QA不用维护）` | `multi_select` | Workstream / branch evidence |

Current parser behavior:

- OpenAPI parsing already indexes field values by both field key and display
  name.
- `external_fields` currently only keeps `提交分支` and `开发分支`.
- Implementation must extend the external field whitelist so `提交记录` and/or
  `field_6e908d` is persisted.

Open questions:

- Confirm real done work item samples contain final CLs in `提交记录`.
- Confirm `field_6e908d` is stable across relevant projects, or document a
  per-project field mapping strategy.
- Confirm the exact content format of `提交记录` and the CL extraction regex.

### Comments / Activity

- Multica currently does not expose a Feishu Project comments/activity proxy.
- First version must not require Feishu comments/activity to infer final CL.
- If final CL only exists in Feishu comments/activity, assessment should output
  `external_committed_cls=[]` with a warning such as `missing_external_cl`.

## Feishu Sync Effects

- Feishu sync runs every 5 minutes for enabled integrations.
- `syncWorkItem` can create or update Multica issues and upsert bindings.
- When a synced issue reaches local `done` or `cancelled`, current sync logic
  cancels active tasks for that issue.
- P4 assessment implementation must exclude `context.type =
  "agent_fix_p4_assessment"` tasks from terminal-status cancellation.
- Watermark short-circuit means unchanged items can return `skipped` without
  rewriting the binding. Historical done binding backfill should use manual
  trigger or a scanner, not rely on item changes.

## P4 / Swarm

Known facts:

- Swarm review state should be stored as the raw Swarm state, such as
  `needsReview`.
- Do not invent a pseudo `committed` Swarm state. Submission status must come
  from `commits[]`, `committed_cl`, or other submitted-CL evidence.
- `swarm_reviews[].changes` may include Swarm companion CLs. Do not treat every
  change as an AI shelved CL.
- Existing Multica `perforce_review` stores a single `shelved_cl` and
  `committed_cl`, which may lose multi-CL details.

Confirmed samples:

- `WAR-7392 / BUG-7008945446`: completed agent run with pending P4 CL `280825`,
  no Swarm review, no final CL.
- `WAR-7512 / BUG-7010257927`: agent comment has shelved CL `267639` and Swarm
  review `267641`; Swarm review state is `needsReview`, changes include
  `[267639,267642]`, commits are empty. CL `267642` is a Swarm companion CL.

Open questions:

- Verify whether Swarm webhook payloads can provide full `review.commits[]`.
- Find samples with only final CL and no Swarm review.
- Find samples where AI shelve was continued or submitted by a human.

## Runtime / P4 Client Limits

- Some runtimes may produce only pending/shelved CL evidence because the P4
  client cannot submit or cannot create a proper Swarm flow.
- Assessment must treat pending CL without Swarm/final CL as incomplete evidence,
  not as successful AI delivery.
- Missing P4/Swarm evidence should produce `unknown` predictions and warnings,
  not guessed attribution.

## Security Boundary

- Evidence API must not expose Feishu plugin secret, P4 ticket, Swarm token, or
  any other credential.
- User access requires workspace membership and binding workspace scope.
- Agent access requires a task-scoped token and must be limited to the binding
  recorded in the task context.
- Cross-workspace or cross-binding access must fail closed without leaking target
  existence.

## Internal-only API/UI Work

The binding-id human review API and review UI consolidation do not add external
Feishu, Meego, P4, or Swarm dependencies.

- `PATCH /api/operations/agent-fixes/{binding_id}/review` writes only Multica
  `agent_fix_review`.
- The handler validates workspace membership and binding workspace scope through
  Multica DB rows.
- The API does not call Feishu/Meego, P4, or Swarm and must not mutate those
  systems.
- Operations and Issues share the same human review editor. This is a frontend
  DRY cleanup over existing Multica API responses, not a new external data
  source.
- Issue metadata/title fallback is only a legacy/demo display signal. It must
  not be used to trigger assessment, write review facts, or decide the
  statistical denominator.

## Verification Checklist

- Validate `提交记录` field content on real done work items.
- Validate CL extraction from `提交记录`.
- Validate `field_6e908d` stability or define per-project mapping.
- Validate one sample for pending CL without Swarm.
- Validate one sample for Swarm companion CL.
- Validate one sample for final CL from Swarm commits or webhook.
- Validate evidence API response does not include secrets.
