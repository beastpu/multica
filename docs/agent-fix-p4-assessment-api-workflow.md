# AI 修单 P4/Swarm Assessment API 与工作流

> Status: In progress
> Last updated: 2026-06-29
> Related design: `docs/agent-fix-prefill-flow.md`
> Plan: `docs/agent-fix-p4-assessment-plan.md`

## 设计结论

第一版 P4/Swarm assessment 的真实主轴是 Feishu/Meego external done binding：

1. Feishu Project sync 写入 `feishu_project_issue_binding`。
2. 后端用 integration status mapping 将外部状态映射到 Multica local status。
3. 只有 mapped status 为 `done` 的 binding 才进入 assessment 候选池。
4. AI assessment 写 `agent_fix_p4_assessment`。
5. 人工验收写 `agent_fix_review`。

`agent_fix_p4_assessment` 是 assessment 存在性的事实来源。`agent_fix_review` 的业务唯一键语义是 `workspace_id + feishu_binding_id`。`issue_id` 只是展示、旧入口兼容和关联查询字段，不是人工 review 的主键语义。

`issue.metadata.p4_assessment`、`metadata.demo`、title 中的 `p4 assessment/swarm` 只能作为 demo/legacy UI fallback，不能作为触发、统计、review 写入或 assessment 存在性事实。

## 后端 API

### List operations rows

```text
GET /api/operations/agent-fixes
```

当前第一版仍兼容旧的 latest agent run feed，但 response 已包含 P4 assessment 需要的 binding 事实：

- `external.binding_id`
- `external.mapped_status`
- `p4_assessment`
- `human_review`
- `display_result_status`
- `ai_judgement_eval`

普通 latest run 查询必须排除 `agent_task_queue.context.type = "agent_fix_p4_assessment"`，避免 assessment task 污染普通 issue task 视图。

### Trigger AI assessment

```text
POST /api/operations/agent-fixes/p4-assessments
```

Request:

```json
{
  "binding_id": "uuid",
  "force": false
}
```

规则：

- 主输入是 `binding_id`。
- 后端从 binding 校验 workspace 归属、issue 关联和 mapped done eligibility。
- `force=false` 用于首次/幂等触发。
- `force=true` 用于手动重跑 completed/stale/failed assessment。
- 自动触发和手动触发调用同一个 service。

### Read evidence

```text
GET /api/operations/agent-fixes/{binding_id}/p4-evidence
```

Evidence API 是只读边界：

- 用户访问必须是 workspace member，且 binding 属于当前 workspace。
- agent auth 只能读取 task context 中同一个 `workspace_id + feishu_binding_id` 的 evidence。
- 不暴露 Feishu plugin secret、P4 ticket、Swarm token、task `result/context/session_id/work_dir` 等内部或密钥字段。
- 当前 evidence 聚合 DB 内已有 binding、issue、external fields、agent task 摘要、comment 线索、`perforce_review` / `issue_perforce_review`。

### Human review

主路径：

```text
PATCH /api/operations/agent-fixes/{binding_id}/review
```

兼容路径：

```text
PUT /api/operations/agent-fixes/{issueId}/review
```

Request:

```json
{
  "outcome": "accepted",
  "reasons": ["complete_usable"],
  "note": "AI shelve 最终被提交，验证通过。"
}
```

规则：

- 新 Core / Operations / Issues 调用必须优先使用 `binding_id`。
- 只有缺少 `external.binding_id` 的 legacy/demo row 才 fallback 到旧 `issueId` 路径。
- handler 必须验证 workspace member。
- binding-id SQL 从 `feishu_project_issue_binding` 按 `workspace_id + binding_id` 校验归属并取得 issue。
- 写入只 upsert `agent_fix_review`，不修改 assessment、issue status、Feishu/Meego、P4 或 Swarm。

## 前端工作流

### Server state

Operations feed、P4 assessment、human review 都是 server state，由 TanStack Query 持有。Views 只从 `operationsFixesOptions` 读取 rows，不把 API rows 复制进 Zustand。

Zustand 只保存客户端 UI 偏好，例如 Operations column widths。

### Core API client

新增/变更 response 必须通过 zod `parseWithFallback`：

- `AgentFixRecordListSchema`
- `TriggerAgentFixP4AssessmentResponseSchema`
- `AgentFixHumanReviewSchema`

`updateAgentFixReview(issueId, data, bindingId?)` 的路由选择：

- `bindingId` 存在：`PATCH /api/operations/agent-fixes/{binding_id}/review`
- `bindingId` 缺失：`PUT /api/operations/agent-fixes/{issueId}/review`

### Shared review UI

`packages/views/dashboard/components/agent-fix-review.tsx` 是人工 review UI 的共享边界：

- outcome/reasons 枚举。
- review/eval/quality/attribution label 和 tone helper。
- `AgentFixReviewDialog`。
- `ToneBadge`。

Operations 和 Issues 使用同一份人工 review editor，避免 outcome/reasons 文案、可选原因和保存行为漂移。

### Operations

Operations 负责完整 P4 assessment 视图：

- P4 evidence。
- AI attribution / quality prediction。
- human review。
- judgement eval。
- 手动 trigger / rerun assessment。
- 轻量分析和 CSV export。

手动 trigger 只在 `external.mapped_status === "done"` 且存在 `external.binding_id` 时展示。不读取 metadata/demo/title 作为触发事实。

### Issues

Issues 只做轻量融合入口：

- 如果能从 Operations feed 匹配到 `agent_fix_p4_assessment` / `agent_fix_review` row，则显示真实 P4/human review 入口。
- 若没有 record，但命中 `metadata.demo`、`metadata.p4_assessment`、`metadata.swarm_review`、`metadata.p4_status` 或 title fallback，仅作为 legacy/demo 显示入口。
- legacy/demo fallback 不能触发 assessment，也不能被当作统计或写入事实。

## Assessment task 隔离

P4 assessment task 必须带：

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

普通 issue task 查询、统计、取消、session resume、completion comment fallback、dedup 都必须排除 assessment task。

Assessment agent 不写 human review、不改 issue status、不改 Feishu/Meego、不改 P4/Swarm。

## 当前未完成

- Operations feed 主轴还未完全切到 external done binding 统计分母，当前仍兼容 latest agent run feed。
- 没有后端聚合筛选 API。
- 没有批量 review / 批量 rerun。
- P4/Swarm 实时外部查询还未接入，Evidence API 当前只聚合 DB 内已有证据。
- `提交记录` 真实 done 样本格式和 CL 提取稳定性仍待验证。
- 没有 review history/audit，当前只保留最新人工 review。
