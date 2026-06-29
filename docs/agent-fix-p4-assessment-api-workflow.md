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

## 整体运行流程

第一版完整链路按“外部完成态 binding → 只读 evidence → AI 评估 → 人工验收 → Operations/Issues 展示”运行。

### 1. Feishu/Meego 同步写入 binding

Feishu Project sync 创建或更新 Multica issue，同时 upsert `feishu_project_issue_binding`。这一步保存外部工单事实：

- 外部工单 ID、URL、项目、work item type。
- 外部状态 label。
- external fields，例如 `提交记录`、`提交分支`、`开发分支`。
- issue 关联关系。

同步后的外部状态不会直接按 label 判断完成，而是通过 integration status mapping 映射到 Multica local status。只有映射结果为 `done` 的 binding 才是 P4 assessment 候选。

### 2. 自动或手动触发 assessment task

触发入口统一走 `POST /api/operations/agent-fixes/p4-assessments`：

- Feishu sync 在 binding 达到 mapped done 时 best-effort 自动触发，使用 `force=false`。
- 历史 done binding scanner 补没有 assessment 的 mapped done binding，使用 `force=false`。
- Operations 单行按钮可手动触发或重跑：首次/缺失 assessment 用 `force=false`，completed 行重跑用 `force=true`。

触发时后端只接受 `binding_id`，再从 DB 校验 workspace 归属、issue 关联和 mapped done eligibility。assessment task 写入 `agent_task_queue`，并在 `context` 中标记：

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

这个 task 与普通 issue task 隔离。普通 latest task feed、统计、取消、resume、completion comment fallback 都必须排除 `context.type = "agent_fix_p4_assessment"`。

### 3. Agent 读取只读 evidence

daemon prompt 要求 assessment agent 使用内置 `multica-agent-fix-p4-assessment` skill。skill 的第一步是读取：

```text
GET /api/operations/agent-fixes/{binding_id}/p4-evidence
```

Evidence API 聚合 Multica DB 内已入库信息：

- binding 和 issue 基本信息。
- external fields 原文。
- 相关 agent task 摘要。
- 相关 comment 线索。
- 已入库的 `perforce_review` / `issue_perforce_review` Swarm evidence。

Evidence API 是只读安全边界。它不暴露 Feishu plugin secret、P4 ticket、Swarm token、task `result/context/session_id/work_dir` 等内部或密钥字段。agent auth 只能读取 task context 中同一个 workspace + binding 的 evidence。

### 4. Agent 在内网补齐 P4/Swarm 判断并输出 JSON

Multica server 不主动访问内网 P4/Swarm。需要实时判断时，由 assessment agent 在内网只读查询 Swarm/P4，并把判断写进最终 JSON：

- AI shelve CL。
- Swarm review 和 `changes[]`。
- Swarm `commits[]` 或 final CL。
- AI delivered / human delivered / conflict / unknown 等 attribution。
- likely correct / likely needs changes / likely wrong / unknown 等 quality。
- warnings 和 summary。

Agent 禁止写 issue/comment/status、Feishu/Meego、P4/Swarm，也禁止写 `agent_fix_review`。

### 5. Parser 写入 `agent_fix_p4_assessment`

后端 parser 只从 `agent_task_queue.result.output` 读取严格 JSON：

- 接受纯 JSON object。
- 或唯一 fenced `json` block，且 fenced block 必须占完整输出。
- 拒绝 prose wrapper、多个 fenced block、JSON 后尾随文本、未知字段、非法 enum、越界 confidence、错误数组/object shape。

解析成功后写入 `agent_fix_p4_assessment`。这张表是 AI assessment 存在性和结果的事实来源。

### 6. 人工验收写入 `agent_fix_review`

Operations 和 Issues 共用人工 review UI。保存时优先走：

```text
PATCH /api/operations/agent-fixes/{binding_id}/review
```

只有缺少 binding id 的 legacy/demo row 才 fallback 到：

```text
PUT /api/operations/agent-fixes/{issueId}/review
```

人工验收只 upsert `agent_fix_review`，不修改 assessment、不改 issue status、不写 Feishu/Meego、不写 P4/Swarm。

### 7. Operations 和 Issues 展示

Operations 使用 React Query 读取 `GET /api/operations/agent-fixes`，展示完整 assessment 视图、筛选、分析和 CSV export。Issues 只做轻量融合入口：

- 优先用 Operations feed 中真实 `agent_fix_p4_assessment` / `agent_fix_review` row。
- 没有真实 record 时，仅把 metadata/title fallback 当作 demo/legacy 显示信号。
- legacy/demo fallback 不触发 assessment、不参与统计、不作为 review 写入事实。

## 功能设计与字段串联

### 后端事实表

| 表 / 来源 | 作用 | 关键字段 |
|---|---|---|
| `feishu_project_issue_binding` | 第一版统计分母和触发主轴 | `id`、`workspace_id`、`issue_id`、`work_item_id`、`external_url`、`external_status_label`、`external_fields` |
| integration config | 外部状态到 Multica status 的映射 | `status_mapping`、`work_item_types[].status_mapping` |
| `agent_task_queue` | assessment task 调度和 agent 输出来源 | `context.type`、`context.feishu_binding_id`、`status`、`result.output` |
| `agent_fix_p4_assessment` | AI assessment 事实来源 | attribution、quality、confidence、CL arrays、summary、warnings |
| `agent_fix_review` | 人工验收事实来源 | `workspace_id`、`feishu_binding_id`、`outcome`、`reasons`、`note`、`reviewed_at` |
| `perforce_review` / `issue_perforce_review` | 已入库 Swarm webhook evidence | review id/state/url、shelved/committed CL、`changes[]`、`commits[]`、branch/event/sent_at、restricted raw payload |

### Operations feed response

`GET /api/operations/agent-fixes` 是前端主读接口。response row 由 Core schema `AgentFixRecordListSchema` 解析，UI 不直接信任裸 JSON。

| 字段 | 来源 | 用途 |
|---|---|---|
| `task_id`、`agent_id`、`agent_name` | 最新普通 issue task | 兼容旧 Operations feed、展示 agent |
| `issue_id`、`issue_identifier`、`issue_title`、`issue_status` | issue | issue 链接、状态列、Issues 轻量入口匹配 |
| `last_comment`、`last_comment_author_type` | issue comment 摘要 | 兼容旧表格描述列；也作为 legacy P4 token fallback |
| `external.binding_id` | `feishu_project_issue_binding.id` | trigger/review/evidence 主键 |
| `external.work_item_id`、`external.url` | binding | 外部工单展示和 CSV |
| `external.status` | binding external status label | 外部状态展示 |
| `external.mapped_status` | status mapping 计算结果 | 是否展示 trigger；必须是 `done` 才能触发 |
| `external.done` | `mapped_status === "done"` | summary 和外部状态 tone |
| `external.project`、`external.version` | binding / external fields | 筛选、分组、CSV |
| `p4_assessment.assessment_status` | `agent_fix_p4_assessment` | AI assessed / pending / running 状态 |
| `p4_assessment.delivery_attribution_prediction` | assessment JSON | AI attribution 列、分析分布 |
| `p4_assessment.quality_prediction` | assessment JSON | AI quality 列、eval 派生 |
| `p4_assessment.confidence` | assessment JSON | confidence 展示 |
| `p4_assessment.workstream` | assessment JSON / external fields | P4 evidence、workstream 筛选和分组 |
| `p4_assessment.swarm_reviews[]` | assessment JSON | Swarm review 摘要、changes/commits/branch/event/sent_at 展示和 CSV |
| `p4_assessment.ai_shelved_cls[]` | assessment JSON | AI shelve CL 展示和 CSV |
| `p4_assessment.swarm_change_cls[]` | assessment JSON | Swarm change CL 展示和 CSV；可包含 companion CL |
| `p4_assessment.swarm_committed_cls[]` | assessment JSON | Swarm final/submitted CL 候选 |
| `p4_assessment.external_committed_cls[]` | assessment JSON | Feishu/Meego `提交记录` 中提取出的 final CL 候选 |
| `p4_assessment.summary`、`warnings[]` | assessment JSON | review dialog、CSV、warning badge |
| `human_review.outcome`、`reasons[]`、`note` | `agent_fix_review` | 人工验收列、弹窗初始值、分析排行、CSV |
| `display_result_status` | 后端派生 | 兼容 display fallback |
| `ai_judgement_eval` | 后端派生 | match/mismatch、分析分布、mismatch-only 筛选 |

### Core schema 和 drift 策略

Core API client 对 Operations response 使用 zod + `parseWithFallback`：

- `AgentFixRecordListSchema` 解析 list response。
- enum 字段保持 string，不做前端 enum narrowing；未知值降级显示原始字符串。
- `swarm_reviews[].id/review_id` 接受 `string | number`。
- CL 数组接受 `string | number` 元素；缺失、`null` 或空数组归一为空数组，避免 CSV 和 UI 出现不稳定 shape。
- `swarm_reviews[].swarm_branch/event_type/sent_at` 缺失或 `null` 归一为空字符串。
- 非数组 response 或关键字段类型错误时返回 fallback `[]`，避免白屏。

### Operations 页面

Operations 是完整工作台：

- 顶部 summary：基于当前 React Query rows 前端派生外部 done、P4 coverage、AI accepted/needs review 等轻量指标。
- Detail table：展示 issue、external status、agent、P4 evidence、AI attribution、AI quality、human review、eval、time。
- P4 evidence cell：展示 workstream、Swarm review、`changes[]`、`commits[]`、`swarm_branch`、`event_type`、`sent_at`、AI shelve CL、final CL、warning。
- Review dialog：复用共享 `AgentFixReviewDialog`，Operations 只注入 P4/AI evidence slot。
- Analysis tab：前端派生 attribution 分布、human review 分布、eval 分布、workstream outcome、reasons 排行。
- Filters：workstream、AI attribution、AI quality、mismatch-only 都基于当前 rows 前端派生；当前没有新增后端筛选 API。
- Trigger button：只在 `external.mapped_status === "done"` 且存在 `external.binding_id` 时展示。
- CSV export：导出当前筛选后的 rows，不新增后端接口；数组字段用 `; ` 拼接，空字段输出为空字符串。

raw payload 不进入主表和 CSV。它当前只通过 Evidence API 作为受限 evidence 给 agent 使用；如果未来要给人看，应新增 debug/detail 视图并继续走脱敏边界。

### Issues 轻量入口

Issues 不是完整 assessment 工作台，只做入口融合：

- 通过 React Query 读取 Operations feed，按 `issue_id` 或 `issue_identifier` 匹配 record。
- 匹配到真实 record 时展示 P4/AI/human review tags，并允许打开共享人工 review dialog。
- 未匹配真实 record 时，只允许 metadata/title legacy/demo fallback 显示入口。
- 保存 review 时优先使用 record 的 `external.binding_id`；缺 binding 才 fallback issue id。
- 不把 Operations rows 复制进 Zustand，不做本地 server state 缓存。

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
- `perforce_review` evidence 已包含 webhook 入库的 `changes[]`、`commits[]`、Swarm branch、event type、sent_at 和受限 raw payload。

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

当前 UI / Export follow-up 已补齐 webhook evidence 字段：

- Operations P4 evidence cell 展示 `changes[]`、`commits[]`、`swarm_branch`、`event_type`、`sent_at` 的紧凑摘要。
- Review 弹窗 detail slot 同步展示这些字段，供人工验收查看。
- CSV export 增加 Swarm changes、Swarm commits、Swarm branch、Swarm event type、Swarm sent_at 列。
- 数组列使用 `; ` 稳定拼接，缺失、`null` 或空数组导出为空字段。
- raw payload 不进入主表和 CSV；后续如需要只能走 debug/详情路径。

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

- Operations feed 已开始切到 external done binding 主轴：后端会合并最新普通 task 与近期 Feishu/Meego binding-only 行；binding-only 行只有 mapped status 为 `done` 才进入 response。UI/export 已覆盖当前入库的 Swarm evidence 字段。
- 没有后端聚合筛选 API。
- 没有批量 review / 批量 rerun。
- P4/Swarm 实时外部查询仍未接入；Evidence API 当前只聚合 DB 内已有证据，外部实时判断由 assessment agent 在内网只读完成。
- `提交记录` 真实 done 样本格式和 CL 提取稳定性仍待验证。
- 没有 review history/audit，当前只保留最新人工 review。
