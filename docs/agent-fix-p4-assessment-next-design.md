---
type: design
status: draft
module: operations
created: 2026-06-29
---

# AI 修单 P4/Swarm Assessment 后续设计

> Baseline commit: `af75ed02 docs(operations): clarify P4 external dependency boundaries`
> Related:
> - `docs/agent-fix-p4-assessment-plan.md`
> - `docs/agent-fix-prefill-flow.md`
> - `docs/agent-fix-evidence-data-limitations.md`
> - `docs/agent-fix-p4-external-dependencies.md`
> - `docs/agent-fix-p4-assessment-current-status-and-demo.md`
> - `docs/agent-fix-p4-assessment-api-workflow.md`

## Summary

P4/Swarm assessment 的后续主线应从“Multica server 尝试拼出全部真相”调整为：

1. Multica server 只提供已入库、只读、脱敏的 evidence。
2. Assessment agent 在内网运行，只读访问 Swarm/P4，补齐 server 无法访问的判断。
3. Agent 最终只输出 assessment JSON。
4. Parser 只从 `agent_task_queue.result.output` 读取严格 JSON，并写 `agent_fix_p4_assessment`。
5. 人工验收仍然只由人工 review API 写 `agent_fix_review`。

本设计不引入 GitHub PR support，不主动拉 Feishu/Meego 新信息，不让外网 Multica server 主动访问内网 Swarm/P4。

## Updated Decisions

- 后续不再主动拉 Feishu/Meego 额外信息。
- `external_fields`、`提交记录`、旧 metadata/title fallback 只作为已入库兼容 evidence，不作为主流程依赖。
- P4/Swarm commits、changes、owner、client、companion CL 等判断由 agent 在内网只读查询完成。
- Multica server 部署在外网，不假设能访问内网 Swarm/P4。
- AI assessment 不写 `agent_fix_review`，不改 issue status，不改 Feishu/Meego，不改 P4/Swarm。
- Evidence API 只读，不暴露 secret。
- Agent auth 只能读取 task context 中同一个 binding 的 evidence。
- Assessment task 必须与普通 issue task 隔离。
- Parser 只接受纯 JSON object 或唯一 fenced `json` block。
- React Query 管 server state，不把 API 数据放 Zustand。
- 新 API response 必须用 zod `parseWithFallback`。
- 修改 Go DB query 后必须运行 `make sqlc`。

## Existing Webhook Capability

### P4/Swarm Webhook

Route:

```text
POST /api/webhooks/p4-swarm
```

Entry and handler:

- `server/cmd/server/router.go`
- `server/internal/handler/perforce_webhook.go`
- Shared review processing: `server/internal/handler/perforce.go`

Auth:

- Static bearer token: `Authorization: Bearer <MULTICA_P4_SWARM_WEBHOOK_TOKEN>`.
- Empty token disables endpoint with 503.
- Router comment says public ingress is additionally IP allowlisted, but application handler itself does not implement a P4-specific allowlist or rate limiter.

Payload shape:

```json
{
  "event_type": "review.updated",
  "sent_at": "2026-06-15T12:00:00Z",
  "swarm": {
    "url": "http://w3-swarm.lilithgame.com",
    "branch": "main"
  },
  "review": {
    "id": 267641,
    "state": "needsReview",
    "title": "...",
    "description": "...",
    "author": "svr_ci",
    "changes": [267639, 267642],
    "commits": [],
    "created": 1700000000,
    "updated": 1700000500
  }
}
```

Current writes:

- `perforce_review`
- `issue_perforce_review`

Current behavior:

- `swarm.url` selects candidate `perforce_connection` rows.
- Issue identifiers in review description disambiguate workspace and issue.
- Unknown Swarm state is acknowledged and ignored.
- The handler does not call Swarm/P4.
- It stores only one `shelved_cl` and one `committed_cl`, using the max value from `changes[]` / `commits[]`.
- It uses `review.updated` as an ordering watermark.

Important side effect:

- `processPerforceReview` calls `advanceIssueToDone(..., "perforce_review_committed")` when all linked reviews are settled and at least one committed review has close intent.
- Therefore P4 webhook is not assessment-neutral. It can be evidence, but it must not be reused as the assessment state machine or assessment trigger write path.

Missing capability:

- No delivery table.
- No replay UI/API.
- No provider delivery dedupe key.
- No raw payload retention.
- No complete `changes[]` / `commits[]` persistence.
- No rate limit in the handler.

Existing consumers:

- `GET /api/issues/{id}/reviews`
- Settings Perforce tab
- Issue detail Perforce reviews
- P4 assessment evidence currently reads linked `perforce_review` rows.

### Perforce Connection / Review API

Routes:

```text
GET /api/workspaces/{id}/perforce/connection
PUT /api/workspaces/{id}/perforce/connection
GET /api/issues/{id}/reviews
```

Behavior:

- Connection stores only `swarm_url`.
- No Swarm user, ticket, or secret is stored after migration 121.
- Server does not actively query Swarm/P4.
- Review API is read-only and workspace/member scoped through issue access.

### Autopilot Webhook

Route:

```text
POST /api/webhooks/autopilots/{token}
```

Handler:

- `server/internal/handler/autopilot_webhook.go`
- Delivery API: `server/internal/handler/webhook_delivery.go`

Auth:

- URL token is the public credential.
- Optional provider-specific HMAC signature.
- Token is redacted in request logs.

Payload shape:

- Arbitrary JSON object or array.
- Normalized into:

```json
{
  "event": "github.workflow_run.completed",
  "eventPayload": {},
  "request": {
    "receivedAt": "...",
    "contentType": "application/json"
  }
}
```

Writes:

- `webhook_delivery`
- `autopilot_run`
- downstream task/issue rows depending on autopilot dispatch

Delivery / dedupe / replay / rate limit:

- Per-IP rate limit before DB access.
- Per-token rate limit.
- `webhook_delivery` stores raw body, selected headers, signature status, response status/body, attempt count.
- Dedupe by provider key:
  - GitHub provider: `X-GitHub-Delivery`
  - Generic provider: `Idempotency-Key`
- Replay API creates a new delivery row with `replayed_from_delivery_id`.
- Frontend has deliveries UI and replay action.

Assessment relevance:

- This is the most complete ingress pattern, but it should not be copied wholesale into P4 assessment unless P4 webhook replay/audit becomes a separate product need.
- Assessment first needs better P4 evidence persistence, not a generic webhook delivery system.

### GitHub Webhook

Route:

```text
POST /api/webhooks/github
```

Handler:

- `server/internal/handler/github.go`

Auth:

- HMAC-SHA256 via `X-Hub-Signature-256`.
- Empty GitHub webhook secret returns 503.

Payloads:

- `ping`
- `installation`
- `pull_request`
- `check_suite`

Writes:

- `github_installation`
- `github_pending_installation`
- `github_pull_request`
- `issue_pull_request`
- `github_pull_request_check_suite`
- `github_pending_check_suite`

Side effects:

- Merged PR with closing intent can call `advanceIssueToDone(..., "github_pr_merged")`.

Delivery / replay / rate limit:

- No generic delivery table.
- Idempotency mostly comes from upsert keys and timestamp guards.
- Check-suite out-of-order delivery uses pending stash and drain.

Assessment relevance:

- Do not introduce GitHub PR support into P4/Swarm assessment.
- Do not generalize P4 CL and GitHub PR into one delivery model for this phase.

### Stripe Webhook

Route:

```text
POST /api/webhooks/stripe
```

Handler:

- `server/internal/handler/cloud_billing.go`

Boundary:

- Local server checks Cloud Runtime availability.
- Per-IP rate limit before body read.
- Requires `Stripe-Signature` header.
- Reads bounded body and forwards exact bytes plus headers to Cloud Runtime `/api/v1/webhooks/stripe`.
- Local server does not verify Stripe signature, does not write billing DB state, and does not provide replay.

Assessment relevance:

- None, beyond confirming public webhook boundaries are intentionally specialized.

## Current Assessment Capability

Already present:

- `agent_fix_p4_assessment`
- `agent_fix_review`
- `GET /api/operations/agent-fixes`
- `POST /api/operations/agent-fixes/p4-assessments`
- `GET /api/operations/agent-fixes/{binding_id}/p4-evidence`
- `PATCH /api/operations/agent-fixes/{binding_id}/review`
- Legacy `PUT /api/operations/agent-fixes/{issue_id}/review`
- Assessment task context:

```json
{
  "type": "agent_fix_p4_assessment",
  "workspace_id": "uuid",
  "issue_id": "uuid",
  "feishu_binding_id": "uuid",
  "mode": "assess_only",
  "prompt_version": "p4-assessment-v1"
}
```

Current evidence API returns:

- Binding summary and mapped status.
- Issue title/status/description.
- Agent task summaries.
- Agent or CL/Swarm-related comments.
- Linked `perforce_review` summaries.

Current evidence API deliberately does not expose:

- Task `result`.
- Task raw `context`.
- `session_id`.
- `work_dir`.
- Runtime internals.
- Secrets.

Agent auth boundary:

- `mat_` task token resolves actor as agent.
- `X-Task-ID` is server-set by auth middleware.
- Evidence access for agent requires:
  - same agent as task,
  - task context type `agent_fix_p4_assessment`,
  - same `workspace_id`,
  - same `feishu_binding_id`.

Task isolation already exists in key paths:

- Operations latest run query excludes assessment task.
- Session/resume/dedup/cancel SQL has assessment task exclusions.
- Completion fallback comment skips assessment task.
- `issue_executed` analytics skips assessment task.
- Failed assessment task marks assessment failed instead of following ordinary issue task semantics.

Current gaps:

- Operations feed still primarily starts from latest normal agent task, not external done binding.
- P4 evidence from DB loses full Swarm `changes[]` / `commits[]`.
- Prompt is too thin for reliable inner-network P4/Swarm reasoning.
- There is no dedicated agent skill for assessment.
- Existing docs still overemphasize Feishu `提交记录` as a final CL source; this must be downgraded to compatibility evidence.

## Real Requirements

### Agent Must Judge in Inner Network

The following should be judged by the assessment agent, not Multica server:

- Swarm review full `changes[]`.
- Swarm review full `commits[]`.
- Swarm review state and timeline when DB evidence is incomplete.
- P4 CL owner/user/client.
- Whether a CL is AI-generated, Swarm companion, or human continuation.
- Whether a submitted CL is the final delivery.
- Whether a submitted CL corresponds to the same issue/binding.
- Whether multiple CLs/reviews represent the same workstream or conflicting work.
- Workstream from depot paths and branch/review metadata.

Agent may use read-only commands/API from the inner network, for example:

- Swarm review read APIs.
- P4 describe/change/filelog style read commands.
- Local repo inspection if necessary for context.

Agent must not use any write command or state-changing API.

### Multica Server Can Provide DB Evidence

Server evidence can include:

- `feishu_project_issue_binding` fields already in DB.
- Integration status mapping already in DB.
- Issue title/status/description/metadata.
- Agent task summary and status.
- Comments with agent output or P4/Swarm clues.
- `perforce_review` / `issue_perforce_review` rows.
- Operations feed assessment/review state.
- Swarm URL, review id, linked URL, single `shelved_cl`, single `committed_cl`.
- Future persisted webhook snapshot fields.

Server evidence must not include:

- Feishu plugin secret.
- Swarm/P4 token.
- Raw task result/context/session/workdir.
- Any credential-like header or env.

### Existing P4 Webhook as Evidence

Existing P4 webhook can be assessment evidence if it wrote a matching review row.

It cannot be the assessment workflow spine because:

- It can automatically change issue status.
- It stores incomplete Swarm arrays.
- It only sees events that were pushed to Multica.
- It is not task-scoped to an assessment binding.

### Feishu/Meego Data Role

Feishu/Meego status mapping remains important only for determining statistical sample eligibility:

- A binding enters assessment candidate pool only when external status maps to local `done`.

Other Feishu/Meego fields are compatibility evidence only:

- `external_fields`
- `提交记录`
- `开发分支`
- external URL
- legacy issue description External-Id / External-Url

Do not add new Feishu/Meego pull paths for assessment in this phase.

## Recommended Architecture

### Data Flow

```text
Feishu/Meego sync
  -> feishu_project_issue_binding
  -> mapped done candidate
  -> agent_fix_p4_assessment pending
  -> assessment agent task
  -> Evidence API reads Multica DB evidence
  -> agent optionally reads Swarm/P4 directly in inner network
  -> agent outputs strict JSON
  -> parser writes agent_fix_p4_assessment
  -> human writes agent_fix_review
  -> Operations / Issues render via React Query
```

### Server Responsibilities

- Candidate eligibility from binding + status mapping.
- Idempotent assessment trigger.
- Assessment task enqueue with isolated context.
- Evidence API with workspace/task scoped auth.
- Parser and DB writes for assessment output.
- Human review API.
- Operations feed and UI data.
- No active Swarm/P4/Feishu/Meego external reads for assessment.

### Agent Responsibilities

- Read Multica evidence.
- Perform inner-network read-only P4/Swarm inspection when needed.
- Distinguish:
  - AI shelved CL,
  - Swarm companion CL,
  - final submitted CL,
  - human continuation,
  - unrelated CL/review.
- Produce a strict JSON assessment.
- Include warnings instead of guessing when evidence is incomplete.

### Human Responsibilities

- Edit final `agent_fix_review`.
- Confirm acceptance/rework/rejection/not-applicable.
- Resolve ambiguous cases that AI marks unknown/conflict.

## Data Model Changes

### Extend P4 Review Evidence

Current `perforce_review` loses important arrays. Add persisted evidence fields:

```sql
ALTER TABLE perforce_review
  ADD COLUMN swarm_branch TEXT NOT NULL DEFAULT '',
  ADD COLUMN changes INTEGER[] NOT NULL DEFAULT '{}',
  ADD COLUMN commits INTEGER[] NOT NULL DEFAULT '{}',
  ADD COLUMN raw_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN event_type TEXT NOT NULL DEFAULT '',
  ADD COLUMN sent_at TIMESTAMPTZ;
```

Notes:

- `shelved_cl` and `committed_cl` may remain for existing UI compatibility.
- `changes[]` and `commits[]` preserve full Swarm evidence.
- `raw_payload` is useful because server cannot re-fetch Swarm later.
- Do not store secrets; webhook payload should be a review snapshot only.
- If raw payload size or retention becomes a concern, add retention later. Do not block the evidence schema on a generic delivery system.

Alternative:

- New `perforce_review_snapshot` table keyed by `perforce_review_id`.
- This is cleaner for audit/history but more work. For the next phase, direct columns are enough unless replay/history becomes a product requirement.

After query changes:

```bash
make sqlc
```

### Assessment Tables

Keep current `agent_fix_p4_assessment` shape:

- `workspace_id`
- `issue_id`
- `feishu_binding_id`
- `assessment_task_id`
- prediction fields
- P4 evidence arrays
- `evidence` JSONB
- `summary`
- `warnings`
- `model`
- `prompt_version`

Keep current `agent_fix_review` shape:

- Unique business key remains `workspace_id + feishu_binding_id`.
- Only human review API writes this table.
- No assessment parser write.

## API Changes

### Evidence API

Existing route remains:

```text
GET /api/operations/agent-fixes/{binding_id}/p4-evidence
```

Enhance response with explicit P4/Swarm sections:

```json
{
  "binding": {},
  "issue": {},
  "tasks": [],
  "comments": [],
  "perforce_reviews": [
    {
      "id": "uuid",
      "review_id": 267641,
      "state": "needsReview",
      "html_url": "http://w3-swarm.lilithgame.com/reviews/267641",
      "swarm_url": "http://w3-swarm.lilithgame.com",
      "swarm_branch": "main",
      "author": "svr_ci",
      "shelved_cl": 267642,
      "committed_cl": null,
      "changes": [267639, 267642],
      "commits": [],
      "close_intent": true,
      "review_created_at": "...",
      "review_updated_at": "..."
    }
  ]
}
```

Keep raw payload either omitted or explicitly placed under a bounded/debug section:

```json
{
  "raw_payload": {
    "event_type": "review.updated",
    "review": {
      "id": 267641,
      "changes": [267639, 267642],
      "commits": []
    }
  }
}
```

Do not expose:

- credentials,
- task result/context,
- full unrelated comments,
- workspace secrets.

### Operations Feed

Current:

```text
GET /api/operations/agent-fixes
```

Target:

- Move main query from latest normal agent task to external binding candidates.
- Latest normal task becomes display/evidence field.
- Assessment task remains excluded from ordinary latest run semantics.
- Keep compatibility fields until UI migration is complete.

The feed should eventually include rows with:

- external done binding but no latest task,
- external done binding but no assessment,
- assessment failed/pending/running/completed,
- human review state.

### Trigger API

Current:

```text
POST /api/operations/agent-fixes/p4-assessments
```

Keep:

- Request main field is `binding_id`.
- `force=false` idempotent first trigger.
- `force=true` manual rerun for completed/failed/stale.
- No issue-id trigger path unless retained as legacy compatibility.

### Human Review API

Current main path:

```text
PATCH /api/operations/agent-fixes/{binding_id}/review
```

Keep:

- Only writes `agent_fix_review`.
- Does not update assessment.
- Does not update issue status.
- Does not update Feishu/Meego.
- Does not update P4/Swarm.

Legacy path:

```text
PUT /api/operations/agent-fixes/{issue_id}/review
```

Keep temporarily for demo/legacy rows, but all new code should prefer binding id.

## Agent Skill Design

### Why a Skill Is Required

The daemon prompt is intentionally short. It can state the task and constraints, but it is not enough to reliably teach agents how to inspect Swarm/P4 evidence.

Assessment correctness depends on detailed operational rules:

- how to read Multica evidence,
- how to query Swarm/P4 read-only,
- how to distinguish AI shelve from Swarm companion CL,
- how to identify final submitted CL,
- how to handle missing evidence,
- how to produce strict JSON.

Therefore the next phase should add a built-in assessment skill and make the assessment prompt reference it.

### Proposed Skill Location

```text
server/internal/service/builtin_skills/agent-fix-p4-assessment/SKILL.md
server/internal/service/builtin_skills/agent-fix-p4-assessment/references/p4-assessment-source-map.md
```

If the builtin skill registry has naming conventions, follow the closest existing built-in skill style and update any generated source-map/check tests.

### Skill Contract

The skill must instruct the agent to:

1. Read the Multica evidence:

```bash
multica api get /api/operations/agent-fixes/<binding_id>/p4-evidence
```

2. Inspect only read-only P4/Swarm data when Multica evidence is incomplete.

3. Never run write commands:

```text
p4 submit
p4 shelve
p4 reopen
p4 revert
p4 edit
p4 sync
Swarm review state mutations
Feishu/Meego mutations
Multica issue/comment/status mutations
```

4. Identify evidence categories:

- `ai_shelved_cls`
- `swarm_change_cls`
- `swarm_committed_cls`
- `external_committed_cls`
- `swarm_reviews`
- `workstream`

5. Distinguish CL roles:

- AI original shelve.
- Swarm companion CL, often owner/user `swarm`.
- Human continuation CL.
- Final submitted CL.
- Unrelated CL.

6. Apply conservative predictions:

- Use `unknown` when evidence is incomplete.
- Use warnings such as:
  - `missing_external_cl`
  - `swarm_review_not_committed`
  - `multiple_candidate_cls`
  - `companion_cl_detected`
  - `p4_lookup_unavailable`
  - `swarm_lookup_unavailable`

7. Output only strict JSON:

```json
{
  "delivery_attribution_prediction": "unknown",
  "quality_prediction": "unknown",
  "prediction_reasons": [],
  "confidence": null,
  "workstream": "",
  "swarm_reviews": [],
  "ai_shelved_cls": [],
  "swarm_change_cls": [],
  "swarm_committed_cls": [],
  "external_committed_cls": [],
  "evidence": {},
  "summary": "",
  "warnings": [],
  "model": ""
}
```

No prose outside JSON.

### Prompt Integration

Keep `server/internal/daemon/prompt.go` short, but update assessment prompt to explicitly say:

- use the built-in P4 assessment skill,
- fetch evidence with the binding id,
- inner-network Swarm/P4 lookup must be read-only,
- final output must satisfy parser schema.

The detailed procedure should live in the skill, not in the daemon prompt.

### Skill Source Map

The source map should point to:

- Evidence API handler/service.
- Assessment task context creation.
- Parser implementation.
- P4 webhook handler and perforce review schema.
- Review write handler showing AI must not write human review.
- Task isolation SQL/logic.

This keeps shipped agent instructions traceable to code behavior.

## Parser Design

Keep current boundary:

- Parser reads only `agent_task_queue.result.output`.
- Parser accepts:
  - whole output is a JSON object, or
  - exactly one fenced `json` block containing one JSON object.
- Parser rejects natural-language wrappers.

Tighten validation:

- `delivery_attribution_prediction` must be one of:
  - `ai_delivered`
  - `ai_assisted`
  - `human_delivered`
  - `conflict`
  - `unattributed`
  - `unknown`
- `quality_prediction` must be one of:
  - `likely_correct`
  - `likely_needs_changes`
  - `likely_wrong`
  - `unknown`
- CL arrays must contain integers only.
- `confidence` must be null or within `[0, 1]`.
- `swarm_reviews` must be array.
- `evidence` must be object.
- `warnings` must be array.

Parser failure:

- Set assessment status to `failed`.
- Write parser warning.
- Do not write human review.
- Do not mutate issue.

Missing evidence is not parser failure. Agent should output `unknown` and warnings.

## Frontend Design

State boundary:

- Operations rows, P4 assessment, human review, evidence, and triggers are server state.
- React Query owns them.
- Zustand can hold only UI preferences such as filters, column visibility, and tab state.

API response boundary:

- Every new/changed endpoint consumed by UI must use zod `parseWithFallback`.
- Enum drift should render fallback labels, not crash.
- Unknown server fields should be allowed with `.loose()` where appropriate.

Operations:

- Full assessment workspace:
  - external mapped status,
  - P4 evidence,
  - AI attribution,
  - AI quality,
  - confidence,
  - warnings,
  - human review,
  - derived judgement eval,
  - CSV export.
- Trigger button visible only for external mapped `done` rows with `binding_id`.
- Metadata/title fallback must never trigger real assessment.

Issues:

- Lightweight P4/human review entry only.
- Prefer real operations/assessment feed records.
- Demo/legacy fallback only controls display, not writes/statistics/triggers.

## Risks and Mitigations

### P4 Webhook Auto-Closes Issues

Risk:

- Assessment code might accidentally rely on or invoke P4 webhook logic and mutate issue status.

Mitigation:

- Treat P4 webhook as evidence ingestion only.
- Do not route assessment trigger through `processPerforceReview`.
- Keep explicit no-go paths in tests and docs.

### Incomplete DB Evidence

Risk:

- Existing `perforce_review` stores only max CL, losing full arrays.

Mitigation:

- 持久化 `changes[]`、`commits[]` 和受限 raw payload。
- Agent 仍可在内网只读查询 Swarm/P4。

### Server Cannot Reach Swarm/P4

Risk:

- Any server-side live lookup will fail in production topology.

Mitigation:

- Prohibit server live Swarm/P4 access for assessment.
- Put read-only live lookup in agent skill.

### Feishu Field Drift

Risk:

- `提交记录` may be empty, inconsistent, or project-specific.

Mitigation:

- Use only already-ingested fields as compatibility evidence.
- Do not pull new Feishu/Meego data.
- Do not make final CL attribution depend on this field.

### Agent Output Drift

Risk:

- Agent returns prose, wrong enum, or malformed JSON.

Mitigation:

- Dedicated skill.
- Strict prompt.
- Strict parser.
- Failed assessment status with warnings.

### Security Leakage

Risk:

- Evidence API leaks task internals or credentials.

Mitigation:

- Keep current task projection.
- Add tests for forbidden fields.
- Agent auth must remain task-context scoped to same binding.

## Phased Plan

### 阶段 1：文档基线

- Add this design as the new baseline.
- Update older docs to point here for post-`af75ed02` decisions.
- Downgrade Feishu `提交记录` language to compatibility evidence.
- Explicitly document that P4 webhook can auto-close issue and must be isolated.

### 阶段 2：Agent Skill

- Add built-in P4/Swarm assessment skill.
- Add source map references.
- Update assessment prompt to invoke/use the skill.
- Add tests or source-map checks consistent with existing built-in skill conventions.

状态：已实现。内置 skill 位于 `server/internal/service/builtin_skills/multica-agent-fix-p4-assessment/`。

### 阶段 3：P4 Evidence 持久化

- Extend `perforce_review` with `changes[]`, `commits[]`, `swarm_branch`, `event_type`, `sent_at`, `raw_payload`.
- Update webhook handler and SQL.
- Run `make sqlc`.
- Add handler/service tests for arrays and stale event behavior.

状态：主体已实现。migration `128_perforce_review_complete_evidence` 已新增上述字段；P4/Swarm webhook 会从入站 payload 写入完整 evidence；旧事件仍会在 upsert 前按 `review.updated` 水位提前返回，不能覆盖已保存的新 evidence。

### 阶段 4：Evidence API 增强

- Return complete DB-backed P4/Swarm evidence.
- Preserve secret/task-internal redaction.
- Add user auth and task-scoped auth tests.

状态：代码侧已完成。service evidence projection 已返回 linked `perforce_review` 中保存的 `changes`、`commits`、`swarm_branch`、`event_type`、`sent_at` 和受限 `raw_payload`。结构测试已固定 agent 访问路径必须校验 `X-Task-ID`、task agent、assessment context type、workspace 和 Feishu binding。完整 handler 运行态 auth 测试仍受本地 handler 测试 DB fixture 阻塞。

### 阶段 5：Parser 收紧

- Tighten enum and shape validation.
- Add tests for:
  - pure JSON object,
  - unique fenced JSON,
  - multiple fenced blocks rejection,
  - prose rejection,
  - bad enum,
  - non-integer CL,
  - confidence out of range.

状态：已实现并覆盖单元测试。parser 现在会拒绝 fenced JSON 前后的自然语言、JSON object 后的尾随文本、多个 fenced block、非法 delivery/quality enum、错误的 `swarm_reviews` / `evidence` / `warnings` shape、非整数 CL 和越界 confidence。

### 阶段 6：Operations Feed 主轴

- Move main query toward external done binding.
- Keep latest normal task as evidence/display.
- Keep assessment tasks excluded from ordinary issue task semantics.
- Update core zod schemas and UI.

状态：已实现最小后端主轴。`ListWorkspaceAgentFixes` 现在合并“最新普通 issue task”和“近期 Feishu/Meego binding 行”。binding-only 行使用 issue 的 agent assignee 做可见性过滤，并在 handler 中只保留 mapped `done` 的 binding-only 行。普通 latest task 行继续作为兼容 display/evidence 行；assessment task 继续从普通 task 选择中排除。API response shape 没有变化，因此现有 core zod schema 仍然有效。

### 阶段 7：UI / Export 跟进

- Ensure Operations and Issues render new evidence fields.
- Keep React Query server-state boundary.
- Keep CSV stable for arrays and empty fields.

## Keep / Downgrade / Remove

Keep:

- P4/Swarm webhook as existing evidence ingestion and issue auto-close integration.
- Perforce connection/review UI.
- Current assessment/review tables.
- Binding-id review API.
- Assessment task isolation.
- Autopilot webhook delivery system as unrelated existing functionality.

Downgrade:

- Feishu `提交记录` and `external_fields`: compatibility evidence only.
- Issue metadata/title demo fallback: display only, never trigger/statistics/write.
- Legacy issue-id review API: compatibility only.

Do not add:

- GitHub PR support for assessment.
- Server-side live Swarm/P4 lookup.
- New Feishu/Meego pull path.
- Assessment-driven issue status mutation.
- Assessment-driven human review write.

## No-Go Paths

The following paths must not be changed or reused by AI assessment:

- Feishu/Meego write paths.
- P4/Swarm write paths.
- `issue.status` write paths, including `advanceIssueToDone`.
- `agent_fix_review` write path, except explicit human review API.
- GitHub PR integration paths.

## Verification Checklist

Before implementation PRs are considered complete:

- `make sqlc` after SQL changes.
- Go tests for P4 webhook evidence persistence and stale event behavior.
- Go tests for Evidence API auth and redaction.
- Go tests for parser strictness.
- Go tests or structural checks for task isolation.
- Core schema tests for malformed API responses.
- Views tests for Operations/Issues display and review save behavior.
- No code path lets assessment write issue status, Feishu/Meego, P4/Swarm, or `agent_fix_review`.
