# Agent Fix P4 Assessment Current Workflow

> Status: current implementation notes
> Last updated: 2026-07-02
> Module: Operations / Feishu Project sync / P4 assessment

This document is the maintenance-oriented source for the current P4 assessment
workflow. Older files under `docs/agent-fix-p4-*` record design history and
should be read as background, not as the current implementation contract.

## Purpose

The workflow answers one product question for Feishu/Meego-synced bug fixes:

Did the AI/Swarm fix method match the final delivered CL, and what evidence
supports that judgement?

The result is surfaced in the Operations page and persisted in Multica tables.
Assessment must not mutate Feishu/Meego, P4/Swarm, issue status, or issue
comments.

## Main Data Sources

| Source | Where It Enters Multica | Current Use |
|---|---|---|
| Feishu Project work item status | `FeishuProjectSyncService.syncWorkItem` -> `feishu_project_issue_binding.external_status_label` | Mapped through integration status mapping. `mapped_status=done` is required before automatic P4 assessment trigger. |
| Feishu Project work item fields | `feishuProjectExternalFields` -> `feishu_project_issue_binding.external_fields` | Stores `提交记录`, `提交分支`, `开发分支`, and derived `final_cl`. |
| Feishu Project comments | `ListWorkItemComments` during sync/evidence build | Used to find final submitted CL. Current issue comments and `_field_linked_story` comments are read. |
| Linked story/requirement | `ListRelatedWorkItems` detects `_field_linked_story` | Hotfix CL comments often live on the linked story rather than the bug. |
| Multica issue description | Feishu sync writes issue body | Fallback for `serverStreamName`; not the preferred workstream source. |
| Issue metadata | Multica issue metadata | Compatibility source for `flow_cl` / `flow_review`. |
| Multica comments | issue comments | Compatibility source for CL/review candidates. |
| P4/Swarm webhook rows | `perforce_review` / `issue_perforce_review` | Existing stored Swarm evidence: review id, state, `changes[]`, `commits[]`, branch, event, sent time. |
| Runtime P4 commands | assessment agent, not server | Agent may run read-only `p4 describe` in the inner-network workspace. |

## Periodic Sync

Feishu Project sync is the primary trigger path. There is no live Operations-page
webhook lookup.

1. The background Feishu Project sync worker runs on its configured cadence
   (currently the production expectation is every 5 minutes).
2. It queries Feishu Project work items through the configured plugin
   credentials.
3. For each matched item, `syncWorkItem` creates or updates:
   - Multica issue fields.
   - `feishu_project_issue_binding`.
   - external status and external fields.
   - selected attachments and labels.
4. The binding is the durable bridge from a Feishu work item to a Multica issue.

The Operations page reads Multica DB through the Multica API. It does not call
Feishu directly.

## External Status And Assessment Trigger

External status labels are not interpreted by name. They are mapped through the
Feishu integration status mapping:

```text
external_status_label + work_item_type + integration mapping -> mapped_status
```

Only `mapped_status = done` is eligible for P4 assessment.

Trigger paths:

- Sync-time trigger: after `syncWorkItem` upserts a binding, it best-effort
  triggers assessment for mapped done bindings.
- Historical scanner: backfills missing assessments for mapped done bindings.
- Operations button: manually triggers or reruns assessment for a binding.

The trigger creates an `agent_task_queue` row with:

```json
{
  "type": "agent_fix_p4_assessment",
  "workspace_id": "...",
  "issue_id": "...",
  "feishu_binding_id": "...",
  "mode": "assess_only",
  "prompt_version": "p4-assessment-v1"
}
```

The task category is `analysis`, so it is intentionally separated from normal
fix tasks.

## Task Isolation

P4 assessment tasks must not pollute normal fix-task behavior.

Important invariants:

- Normal Operations latest-run feed reads `agent_task_queue.task_category='fix'`.
- Queued TTL expiration for normal fix tasks must not expire analysis tasks.
- Resume/cancel/latest task flows for normal issue tasks must not treat P4
  assessment tasks as user-facing fix attempts.
- `CompleteTask` and `FailTask` for assessment update
  `agent_fix_p4_assessment`, not issue comments or issue status.

## Evidence API

Assessment agents start by calling:

```text
GET /api/operations/agent-fixes/{binding_id}/p4-evidence
```

The endpoint is task-scoped and read-only. It returns already-ingested or
server-fetched evidence:

- binding facts and mapped status.
- issue title, description, status, metadata.
- `external_fields`.
- selected Multica comments.
- selected Feishu Project comments.
- linked story comments when `_field_linked_story` exists.
- Perforce/Swarm review rows.
- structured `cl_candidates[]` and `review_candidates[]`.
- optional `external_evidence_errors[]`.

It deliberately does not return secrets, raw daemon runtime internals, task
session/workdir, or task result/context.

## Final CL Source

The Operations `Final CL` field should not be guessed from arbitrary agent
comments.

Current source order:

1. Feishu Project comments are fetched during sync.
2. Comments from the bound work item and linked story are considered together.
3. The latest comment containing submitted/final CL semantics wins.
4. The extracted CL is written to `external_fields.final_cl`.
5. `GET /api/operations/agent-fixes` exposes it as `external.final_cl`.
6. The UI shows `external.final_cl` before assessment-derived committed CLs.

Accepted comment patterns include submitted/final/committed/ChangeList forms,
for example:

```text
ChangeList: 284805 --Submitted
submitted CL 284805
CL 284805 已提交
```

Shelved comments are intentionally not final CL evidence:

```text
CL 287451 已 shelve
```

If a Feishu comment lookup partially fails during sync, existing
`external_fields.final_cl` is preserved rather than being cleared.

## Workstream Source

Workstream should come from Feishu-synced branch fields, not from a normalized
`serverStreamName` whenever branch fields are present.

Current source order exposed by `external.workstream`:

1. `external_fields["提交分支"]`
2. `external_fields["开发分支"]`
3. `external_fields["workstream"]`
4. `external_fields["serverStreamName"]`
5. issue description fallback: `serverStreamName: <value>`

No build/CL suffix normalization is applied. If Feishu says the submitted branch
is `rel_1.1.0`, the UI shows `rel_1.1.0`. If the only fallback is
`serverStreamName: rel_1.1.0_server_287406`, the raw value is returned.

The fallback parser only reads the value on the same line. This avoids the
known bad case where an empty field such as:

```text
serverStreamName:
UserOpenID: ...
```

would previously be parsed as `UserOpenID:`.

The Operations page derives the display/filter workstream as:

```text
p4_assessment.workstream || external.workstream || swarm_branch
```

## Assessment Agent Contract

The daemon must claim the `agent_fix_p4_assessment` analysis task and install
the built-in skill:

```text
server/internal/service/builtin_skills/multica-agent-fix-p4-assessment
```

The skill requires:

- first read `/p4-evidence`.
- use read-only P4/Swarm commands only.
- do not write Multica issue comments.
- do not change issue status.
- do not mutate Feishu/Meego.
- do not mutate P4 or Swarm.
- return only strict JSON.

The intended implementation comparison is binary:

```json
{
  "evidence": {
    "implementation_comparison": {
      "method_equivalence": "equivalent"
    }
  }
}
```

Allowed values:

- `equivalent`
- `not_equivalent`

There is no middle state for method equivalence. If evidence cannot prove
equivalence, the judgement should be `not_equivalent` with an evidence-backed
reason.

Top-level quality mapping:

- equivalent -> usually `quality_prediction=likely_correct`
- not equivalent -> usually `quality_prediction=likely_wrong`

Delivery attribution remains separate:

- `ai_delivered`
- `ai_assisted`
- `human_delivered`
- `conflict`
- `unattributed`
- `unknown`

## Parser And Writeback

`CompleteTask` parses `agent_task_queue.result.output` for assessment tasks.

Accepted output:

- raw JSON object.
- or exactly one fenced `json` block that is the whole output.

Rejected output:

- prose before/after JSON.
- multiple fenced blocks.
- unknown top-level fields.
- invalid enum values.
- invalid confidence range.
- wrong array/object shape.

On success, only `agent_fix_p4_assessment` is updated.

On parser failure, the assessment is marked failed with warning such as:

```text
parser_error: expected a JSON object or one fenced json block
```

Historical parser failures are not auto-fixed. Rerun assessment to replace the
failed row.

## Field Reference

### `external.*`

`external` is built from `feishu_project_issue_binding` plus integration status
mapping in `GET /api/operations/agent-fixes`.

| Field | Source | Writer | Meaning |
|---|---|---|---|
| `external.binding_id` | `feishu_project_issue_binding.id` | Feishu sync | Stable binding key for trigger, evidence, and human review APIs. |
| `external.work_item_id` | `feishu_project_issue_binding.work_item_id` | Feishu sync | Feishu Project work item id. |
| `external.url` | `feishu_project_issue_binding.external_url` | Feishu sync | Feishu Project detail URL. |
| `external.status` | `external_status_label` | Feishu sync | Raw Feishu status key/label. Do not compare directly to `done`. |
| `external.mapped_status` | integration status mapping | Operations handler | Multica local status derived from raw external status. |
| `external.done` | `mapped_status == "done"` | Operations handler | Whether the external item is currently done for Operations/P4 trigger purposes. |
| `external.project` | `project_key` | Feishu sync | Feishu Project key/id as stored in the binding. |
| `external.final_cl` | `external_fields.final_cl` | Feishu sync comment extractor | Final submitted CL extracted from Feishu comments. |
| `external.workstream` | `external_fields["提交分支"]` first | Operations handler | Branch/workstream for filtering and grouping. See source order below. |

`external.workstream` source order:

1. `external_fields["提交分支"]`
2. `external_fields["开发分支"]`
3. `external_fields["workstream"]`
4. `external_fields["serverStreamName"]`
5. issue description `serverStreamName: <value>` fallback

The first two are Feishu-synced branch fields and are the preferred business
source. The description fallback only fixes missing branch fields; it must not
overwrite Feishu branch fields.

### `p4_assessment.*`

`p4_assessment` is persisted in `agent_fix_p4_assessment` by assessment
`CompleteTask`. The AI agent writes the JSON; the server only validates shape,
enum values, and confidence range before persistence.

| Field | Source | Meaning |
|---|---|---|
| `assessment_status` | server state | `pending`, `running`, `completed`, `failed`, or stale/missing UI states derived by the feed. |
| `delivery_attribution_prediction` | AI output | Who delivered or materially contributed to the final fix. |
| `quality_prediction` | AI output | AI's quality judgement for the delivered fix. |
| `prediction_reasons[]` | AI output | Machine-readable reason codes or short reason strings. |
| `confidence` | AI output | Number from 0 to 1, or `null`. |
| `workstream` | AI output | AI-inferred workstream from evidence. UI falls back to `external.workstream` when empty. |
| `swarm_reviews[]` | AI output / evidence | Review ids and compact facts used for judgement. |
| `ai_shelved_cls[]` | AI output | CLs believed to be AI-created shelved work. |
| `swarm_change_cls[]` | AI output | CLs attached to Swarm review changes; may include companion CLs. |
| `swarm_committed_cls[]` | AI output | Submitted CLs from Swarm commits or verified submitted Swarm evidence. |
| `external_committed_cls[]` | AI output | Submitted CLs from Feishu/external evidence. |
| `evidence` | AI output | Free-form JSON object for supporting details. |
| `summary` | AI output | Human-readable assessment summary. |
| `warnings[]` | AI output or parser | Missing evidence, parser failures, unavailable lookup, etc. |
| `model` | AI output | Model/runtime identity when supplied. |

`delivery_attribution_prediction` values:

| Value | Meaning |
|---|---|
| `ai_delivered` | Automation/AI delivered the final fix directly. |
| `ai_assisted` | Human final submission uses or materially follows a verified, method-equivalent AI/Swarm implementation. Chronology does not exclude the match. |
| `human_delivered` | Human delivered a materially different implementation and human ownership is proven. |
| `conflict` | Evidence conflicts and attribution cannot be cleanly resolved. |
| `unattributed` | Final delivery exists but owner/source is not attributable. |
| `unknown` | Insufficient evidence. |

`quality_prediction` values:

| Value | Meaning |
|---|---|
| `likely_correct` | AI believes the final implementation is correct/equivalent. |
| `likely_needs_changes` | AI believes the fix is partial or may need follow-up. |
| `likely_wrong` | AI believes the fix is wrong or not method-equivalent. |
| `unknown` | AI cannot judge from available evidence. |

`evidence.implementation_comparison.method_equivalence` values:

| Value | Meaning |
|---|---|
| `equivalent` | AI/Swarm implementation and final submitted CL use the same or equivalent fix method. Names and merge mechanics may differ. |
| `not_equivalent` | Final submitted CL is materially different, or available evidence cannot prove equivalence. |

`method_equivalence` is intentionally binary. It is evidence detail stored inside
`evidence`, not a dedicated DB column. UI summary still primarily uses
`quality_prediction` and `delivery_attribution_prediction`.

### `human_review.*`

`human_review` is persisted in `agent_fix_review` by the human review APIs.
Assessment agents never write it.

| Field | Source | Meaning |
|---|---|---|
| `outcome` | human reviewer | Final human judgement for the row. |
| `reasons[]` | human reviewer | Structured reason codes used for analysis. |
| `note` | human reviewer | Free-form note. |
| `reviewer_id` | handler | Reviewer member id. |
| `reviewed_at` | handler | Set when outcome is not `unreviewed`. |

`outcome` values:

| Value | Meaning |
|---|---|
| `unreviewed` | No human judgement yet. |
| `accepted` | Human accepted the fix. |
| `needs_changes` | Human says the fix needs follow-up. |
| `rejected` | Human rejected the fix. |
| `not_applicable` | Row should not be scored. |

### Derived Operations Fields

`display_result_status` and `ai_judgement_eval` are derived by the Operations
handler. They are not persisted facts.

`ai_judgement_eval` compares `p4_assessment.quality_prediction` with
`human_review.outcome` on a severity axis:

```text
likely_correct / accepted       -> 0
likely_needs_changes / needs_changes -> 1
likely_wrong / rejected         -> 2
```

| Value | Meaning |
|---|---|
| `pending` | No comparable human review yet. |
| `not_comparable` | Review is `not_applicable`, assessment is missing, or values are unknown/unrecognized. |
| `match` | AI quality severity matches human review severity. |
| `overestimated` | AI was too optimistic; human review is worse than AI predicted. |
| `underestimated` | AI was too pessimistic; human review is better than AI predicted. |

The UI also knows legacy labels such as `wrong_attribution`, `mismatch`,
`accurate`, `out_of_scope`, `needs_ai_assessment`, `ai_assessing`,
`ai_assessment_failed`, `needs_review_conflict`, and `needs_human_review`.
Those are display-compatible values, but the current backend quality-vs-review
derivation returns only `pending`, `not_comparable`, `match`, `overestimated`,
or `underestimated`.

`display_result_status` currently mirrors `ai_judgement_eval` for the Operations
feed.

### Operations Summary Fields

The summary cards are front-end derived from the currently filtered rows:

| Label | Source | Meaning |
|---|---|---|
| External done | rows where `external.done === true` | Count of externally done rows in current filter. |
| P4 coverage | `hasP4Signal(row)` | Rows with P4 assessment or derived P4 evidence such as swarm/shelve/final CL. |
| AI 交付 | `ai_delivered` + `ai_assisted` | AI participated in delivery. This is broader than automation submitted the final CL. |
| Human reviewed | human outcome other than empty/`unreviewed` | Rows with human review decision. |
| Accepted | `human_review.outcome === "accepted"` | Human accepted rows. |
| Judgement drift | `isMismatchEval(row)` | Rows whose eval is counted as drift. |

`isMismatchEval(row)` currently treats these values as drift:

- `overestimated`
- `underestimated`
- `wrong_attribution`
- `mismatch`

## Operations Page

Main API:

```text
GET /api/operations/agent-fixes?days=<1|7|30|90>&search=<term>
```

Current date-window semantics:

- Feishu-bound rows prefer `last_external_updated_at`.
- If missing, they fallback to `last_synced_at`.
- Non-external normal fix rows use task activity time.

This is intentionally closer to business update time than sync time, while
avoiding a new DB column for done time.

The page shows:

- issue and external status.
- P4 evidence.
- AI attribution.
- AI quality.
- human review.
- judgement eval.
- date.

Filters:

- agent.
- issue status.
- workstream.
- AI attribution.
- AI quality.
- mismatch only.

Filter reset buttons are visible after selecting a non-all filter value.

`AI 交付` summary currently counts both `ai_delivered` and `ai_assisted`. It
means the final implementation was delivered directly by AI or is
method-equivalent to the AI implementation, not necessarily that automation
submitted the final CL.

`仅看偏差` shows rows whose `ai_judgement_eval` is one of:

- `overestimated`
- `underestimated`
- `wrong_attribution`
- `mismatch`

## Important Files

Backend:

- `server/internal/service/feishu_project.go`
- `server/internal/service/agent_fix_assessment.go`
- `server/internal/handler/agent.go`
- `server/pkg/db/queries/agent.sql`
- `server/pkg/db/queries/feishu_project.sql`
- `server/internal/service/builtin_skills/multica-agent-fix-p4-assessment/SKILL.md`

Frontend/core:

- `packages/core/api/schemas.ts`
- `packages/core/types/agent.ts`
- `packages/core/dashboard/queries.ts`
- `packages/views/dashboard/components/operations-page.tsx`
- `packages/views/dashboard/components/operations-page.test.tsx`

Historical/background docs:

- `docs/agent-fix-p4-assessment-api-workflow.md`
- `docs/agent-fix-p4-assessment-next-design.md`
- `docs/agent-fix-p4-external-dependencies.md`

## Verification Commands

Targeted checks used for this workflow:

```bash
cd server && go test ./internal/service -run 'TestOperationsFeedUsesBindingSpineWithoutAssessmentTaskPollution|TestAgentFixExternalDoneUsesStatusMappingInputs|TestFeishuProjectLatestSubmittedCLFromComments|TestFeishuProjectListWorkItemCommentsUsesIntegrationAuthAndAPIName|TestFeishuProjectListRelatedWorkItemsFindsLinkedStory'
corepack pnpm --filter @multica/views exec vitest run dashboard/components/operations-page.test.tsx
corepack pnpm --filter @multica/core typecheck
corepack pnpm --filter @multica/views typecheck
git diff --check
```

Broader handler tests may require a local test DB with the full schema.

## Known Boundaries

- Feishu `last_external_updated_at` is used as the current Operations business
  window proxy. It is not a guaranteed done-time field.
- The page does not call Feishu live. It reads the Multica API only.
- Final CL extraction depends on comments visible through Feishu Project OpenAPI.
- If the final CL only exists in UI-only notification cards not returned by the
  API, evidence may still be missing.
- Workstream uses Feishu-synced branch fields first; `serverStreamName` is only
  a fallback.
- P4/Swarm realtime inspection is done by the assessment agent, not the server.
