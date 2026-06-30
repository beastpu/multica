# AI 修单 P4/Swarm Assessment 当前状态与 Demo 指南

> Status: In progress
> Last updated: 2026-06-29
> Static demo reference: `/home/wangtengfei/multica/agent-fix-board-demo.html`
> Related plan: `docs/agent-fix-p4-assessment-plan.md`
> API/workflow: `docs/agent-fix-p4-assessment-api-workflow.md`

## 目标

把静态 demo HTML 中的 P4/Swarm Assessment 信息架构产品化接入真实系统，而不是保留 standalone HTML。

整体运行流程、接口字段、页面串联和 server/client state 边界见 `docs/agent-fix-p4-assessment-api-workflow.md`。外部系统依赖、已经收窄的边界和仍待验证事项见 `docs/agent-fix-p4-external-dependencies.md`。

第一版重点是让 `/operations` 从旧的最小 agent fix 表格，升级成“AI 修单评估视图”：

- 能看到外部/Meego 工单状态。
- 能看到 workstream、Swarm review、shelved CL、final CL 等 P4 evidence。
- 能看到 AI delivery attribution prediction。
- 能看到 AI quality prediction 和 confidence。
- 能看到人工验收 outcome、reasons、note。
- 能看到 AI 判断和人工验收之间的 match/mismatch 派生结果。
- 能在真实页面里编辑人工验收，不再只停留在 summary。

## 已接入内容

### 后端与数据链路

已新增/扩展：

- `agent_fix_p4_assessment` 表：保存 AI assessment 结果和 P4 evidence。
- `agent_fix_review` 表：保存人工验收结果。
- `GET /api/operations/agent-fixes`
  - 返回旧表格字段。
  - 增加 `external`，包括 `external.binding_id`。
  - 增加 `p4_assessment`。
  - 增加 `human_review`。
  - 增加 `display_result_status` / `ai_judgement_eval`。
- `POST /api/operations/agent-fixes/p4-assessments`
  - 支持按 `binding_id` 单条触发或重跑 AI assessment。
  - `force=false` 用于首次/幂等触发。
  - completed 行的手动重跑使用 `force=true`。
- `GET /api/operations/agent-fixes/{binding_id}/p4-evidence`
  - 已有只读 evidence API。
  - 返回 binding、issue、external fields、相关 agent task 摘要、相关 comment 线索、已入库的 `perforce_review` / `issue_perforce_review` Swarm evidence。
  - task evidence 只返回受控摘要：task id、agent id、status、是否 P4 assessment、failure reason、error、时间戳；不暴露 `result`、`context`、`session_id`、`work_dir` 等运行内部信息。
  - comment evidence 只返回 agent comment 或命中 CL/Swarm 关键词的普通 comment，最多 20 条。
  - Perforce evidence 复用已入库 Swarm review 状态、review id、URL、author、shelved CL、committed CL、`changes[]`、`commits[]`、Swarm branch、event type、sent_at、受限 raw payload 和 review 时间。
- Built-in P4/Swarm assessment skill：
  - 新增 `server/internal/service/builtin_skills/multica-agent-fix-p4-assessment/SKILL.md`。
  - 新增 `references/p4-assessment-source-map.md`。
  - skill 明确要求先读取 `multica api get /api/operations/agent-fixes/<binding_id>/p4-evidence`。
  - skill 明确只允许内网只读查询 Swarm/P4，禁止写 issue/comment/status、Feishu/Meego、P4/Swarm 和 `agent_fix_review`。
  - skill 内沉淀 AI shelve CL、Swarm companion CL、human continuation CL、final submitted CL、unrelated CL 的区分规则，以及 evidence 不足时输出 `unknown` + `warnings` 的策略。
- Assessment daemon prompt：
  - `server/internal/daemon/prompt.go` 的 assessment prompt 已保持短提示，明确要求使用内置 `multica-agent-fix-p4-assessment` skill。
  - prompt 只保留 task id、binding id、evidence API、只读边界和最终 JSON 输出约束；详细判断流程放在 skill 内。
- Assessment parser：
  - 只从 `agent_task_queue.result.output` 读取。
  - 只接受纯 JSON object 或整个输出为唯一 fenced `json` block。
  - 拒绝 prose wrapper、多个 fenced block、JSON object 后的尾随文本、未知字段、非法 attribution/quality enum、非整数 CL、越界 confidence，以及 `swarm_reviews` / `evidence` / `warnings` shape 错误。
- P4/Swarm webhook evidence persistence：
  - migration `129_perforce_review_complete_evidence` 扩展 `perforce_review`：`changes[]`、`commits[]`、`swarm_branch`、`event_type`、`sent_at`、`raw_payload`。
  - webhook handler 从 inbound payload 入库这些完整 evidence；不主动回查 Swarm/P4。
  - 旧事件仍按 `review.updated` 水位提前返回，不覆盖已保存的新 evidence。
- `PATCH /api/operations/agent-fixes/{binding_id}/review`
  - 已作为人工 review 主写入 API。
  - 以 `workspace_id + feishu_binding_id` 为写入主键语义。
  - handler 验证当前用户是 workspace member，SQL 从 `feishu_project_issue_binding` 按 workspace + binding 校验归属并取得 issue。
  - request/response 沿用现有人审 `outcome/reasons/note` 语义。
  - 不依赖 `metadata.p4_assessment`、demo 或 title 作为写入事实。
  - 不修改 assessment、不修改 issue status、不修改 Feishu/Meego、不修改 P4/Swarm。
- `PUT /api/operations/agent-fixes/{issueId}/review`
  - 继续保留兼容旧前端/旧入口。
  - 新 Core / Operations / Issues 调用会优先使用 binding id；缺少 binding id 时才 fallback 到旧 issue id 路径。

当前第一版仍以 operations feed 中的 issue/agent fix 行为主轴，已经能展示 demo P4 数据。
Operations 主轴还没有完全切到“外部 done binding 统计分母”，当前仍兼容旧的 agent fix feed 主轴；普通 latest run 查询继续排除 `context.type = agent_fix_p4_assessment`。
当前后端 feed 已开始切换主轴：`ListWorkspaceAgentFixes` 会把最新普通 issue task 与近期 Feishu/Meego binding 行合并；无普通 task 的 binding-only 行只有在 status mapping 计算为 local `done` 时才进入 Operations response。旧的普通 task 行继续作为兼容 display/evidence 来源。

### Core

已扩展：

- `AgentFixRecord` 类型。
- P4 assessment / external / human review 类型。
- `AgentFixRecordListSchema` zod schema。
- `TriggerAgentFixP4AssessmentResponseSchema` zod schema。
- `updateAgentFixReview` API client。
- `useUpdateAgentFixReview` React Query mutation。
- `updateAgentFixReview` 已优先走 `PATCH /api/operations/agent-fixes/{binding_id}/review`，并对 response 使用 `AgentFixHumanReviewSchema` + `parseWithFallback`；缺 binding id 时 fallback 旧 `PUT /api/operations/agent-fixes/{issueId}/review`。
- `triggerAgentFixP4Assessment` API client。
- `useTriggerAgentFixP4Assessment` React Query mutation。
- 人工 review UI 共享边界已收敛到 `packages/views/dashboard/components/agent-fix-review.tsx`：
  - outcome/reasons 枚举。
  - review/eval/quality/attribution label 和 tone helper。
  - `AgentFixReviewDialog`。
  - `ToneBadge`。
  - Operations 和 Issues 共用同一套人工 review 编辑器，避免可选原因、文案和保存行为漂移。

重要修复：

- `p4_assessment.swarm_reviews[].id` 真实数据可能是数字，例如 `123`。
- schema 已改为接受 `string | number`，避免 `parseWithFallback` 把整组 operations 数据 fallback 成空数组。
- `external.binding_id` 已从后端 feed 补出，前端触发 assessment 不再依赖 issue id 或 metadata 兼容信号。

### Operations 页面

已在 `packages/views/dashboard/components/operations-page.tsx` 接入：

- 顶部 P4 assessment summary。
- `P4 details` / `Analysis report` tab。
- 表格列：
  - issue
  - external status
  - agent
  - P4 evidence
  - AI attribution
  - AI quality
  - human review
  - eval
  - time
- P4 evidence 标签：
  - workstream
  - Swarm review
  - shelved CL
  - final CL
  - warnings
- 人工 review 弹窗：
  - 展示 P4 evidence、AI attribution、AI quality。
  - 可编辑 outcome。
  - 可选择 reasons。
  - 可填写 note。
  - 保存后走真实 API mutation；有 `external.binding_id` 时使用 binding-id 主路径，缺 binding 时 fallback 旧 issueId 兼容路径。
  - outcome/reasons/note 编辑器复用共享 `AgentFixReviewDialog`；Operations 只负责注入 P4/AI evidence slot。
- 分析报告页签：
  - attribution 分布。
  - human review 分布。
  - eval 分布。
  - workstream outcome 分组。
  - 人工 review reasons 排行。
  - AI prediction reasons 排行。
- 顶部轻量筛选：
  - workstream。
  - AI attribution。
  - AI quality。
  - mismatch-only。
- CSV 导出：
  - 基于当前筛选后的 rows 导出，不新增后端接口。
  - 文件名形如 `multica-p4-assessment-YYYY-MM-DD.csv`。
  - CSV 前置 UTF-8 BOM，方便 Excel 直接打开。
  - 数组字段用 `; ` 连接，空字段输出为空字符串。
  - 字段包含 issue、外部工单、Project、Version、Workstream、Agent、AI assessment status、AI attribution、AI quality、confidence、Swarm review、Swarm changes、Swarm commits、Swarm branch、Swarm event type、Swarm sent_at、AI shelve CL、Swarm change CL、final CL、human outcome、reasons、note、judgement eval、summary、warnings。
  - `changes[]` / `commits[]` 数组使用 `; ` 稳定拼接；缺失或空数组导出为空字段。
- P4 evidence 展示：
  - Operations P4 evidence cell 已展示 assessment 中的 `changes[]`、`commits[]`、`swarm_branch`、`event_type`、`sent_at`。
  - Review 弹窗的 P4 evidence slot 同步展示这些字段，方便人工验收时查看细节。
  - raw payload 不进入主表和 CSV；如后续需要，只应放在 debug/详情路径。
- 手动 assessment 触发：
  - 只对 `external.mapped_status === "done"` 且存在 `external.binding_id` 的行展示。
  - 未完成或无 binding id 的行不展示触发入口。
  - 无 completed assessment 的 done 行展示 `Run assessment`，请求体为 `{ binding_id, force: false }`。
  - completed 行展示 `Rerun assessment`，请求体为 `{ binding_id, force: true }`。
  - pending/running 行展示轻量进行中状态并禁用按钮。
- 历史 done binding scanner：
  - 已接入 Feishu Project sync worker，在每个 integration 的 sync/orphan reconcile 后、同一 advisory lock 内扫描一批历史 binding。
  - 候选从 `feishu_project_issue_binding` 出发，使用 integration `status_mapping/work_item_types` 判断 mapped done。
  - 只补没有 `agent_fix_p4_assessment` 记录的 binding，并调用 `Trigger(..., force=false)`。
  - 不使用 `metadata.p4_assessment`、demo 或 title 关键词作为触发事实。
  - 不自动重跑 failed/stale/completed，避免后台周期性重复烧 agent。

当前筛选都在 `packages/views/dashboard/components/operations-page.tsx` 内基于 React Query 返回的 rows 前端派生，不新增 API 参数，不把 server 数据复制进 Zustand。筛选选项从当前返回数据中提取，unknown enum 仍按原始字符串降级显示。
手动触发入口只按 external done binding 展示，不读取 `metadata.p4_assessment`、`metadata.demo` 或 title 关键词作为真实触发条件。

### Issues 轻量融合

已在 issues 列表/卡片接入轻量 P4 assessment 入口：

- 优先从 Operations assessment feed 匹配 `agent_fix_p4_assessment` / `agent_fix_review` record。
- 若没有 record，仅将 `metadata.demo`、`metadata.p4_assessment`、`metadata.swarm_review`、`metadata.p4_status` 或 title 中的 `p4 assessment/swarm` 作为 legacy/demo fallback 显示信号。
- 展示 AI/P4/human review 相关标签。
- human review 标签可点击，直接打开与 Operations 共用的人工 review 弹窗。
- 对 test1/test2 这类初始未验收状态，若已有 P4/demo 信号或能从 Operations assessment feed 匹配到 issue 记录，也会显示 `Human review` 入口，不需要先在 Operations 页写入 outcome。
- Issues 列表行、看板卡片、Issue 详情页标题下方都会挂载同一套入口；组件内部自行判断是否显示，避免 record-only 的 assessment 被外层条件挡住。
- 提供跳转 `/operations` 的 assessment 入口。
- legacy/demo fallback 只影响轻量入口显示，不触发 assessment、不参与统计、不作为 review 写入事实；review 保存仍优先使用 record 的 `external.binding_id`，缺 binding 时才走旧 issueId 兼容路径。

## 尚未处理或未完整处理

以下是静态 demo 或完整方案里有，但当前第一版还没有完整落地的内容：

- 没有实现复杂图表；当前只做了不引入新图表库的轻量分析卡片、workstream 分组和 reasons 排行。
- 没有新增后端聚合/筛选 API；当前 workstream / attribution / quality / mismatch-only 都是当前结果集的前端轻量筛选。
- 没有批量操作。
- 历史 done binding scanner 已实现保守版；当前只补缺失 assessment 的 mapped done binding。
- failed/stale/completed 的产品化批量重跑还没做；当前只有单行手动入口，completed 行可 rerun。
- P4/Swarm 服务端外部查询还没做；Evidence API 当前只聚合 Multica DB 内已有证据，没有实时查询真实 P4/Swarm。
- Evidence 已聚合相关 agent task/comment/perforce_review/issue_perforce_review 的受控只读摘要；当前已补入 webhook 已入库的 `changes[]`、`commits[]`、branch/event/sent_at，后续仍可补 P4 change describe 等 agent 内网只读查询得到的外部深度字段。
- 真实 final CL 校验还没完成；`提交记录` 已同步，但还没用真实 done 样本验证字段格式和 CL 提取稳定性。
- Operations 主轴还没有完全切到“外部 done binding 统计分母”，当前仍兼容旧的 agent fix feed 主轴。
- 没有接 GitHub PR。
- 没有自动修改飞书/Meego 状态。
- 没有自动提交或修改 P4。
- 没有 review history/audit，仅保存最新人工 review。

## 本阶段验证

本阶段未启动新的 dev server，也没有改变部署/启动方式。

已通过：

```bash
corepack pnpm --filter @multica/views exec vitest run dashboard/components/operations-page.test.tsx
corepack pnpm --filter @multica/core exec vitest run api/schemas.test.ts
corepack pnpm --filter @multica/views exec vitest run locales/parity.test.ts
corepack pnpm --filter @multica/views typecheck
corepack pnpm --filter @multica/core typecheck
corepack pnpm --filter @multica/views exec vitest run issues/components/p4-assessment-entry.test.tsx
corepack pnpm --filter @multica/views exec vitest run issues/components/pickers/label-picker.test.tsx
corepack pnpm --filter @multica/views exec vitest run issues/components/pickers/stage-picker.test.tsx
corepack pnpm --filter @multica/views typecheck
corepack pnpm --filter @multica/core exec vitest run api/schemas.test.ts api/client.test.ts
corepack pnpm --filter @multica/views exec vitest run dashboard/components/operations-page.test.tsx locales/parity.test.ts
make sqlc
cd server && go test ./internal/service -run 'TestP4AssessmentBackfillScansBindingsWithStatusMapping|TestP4AssessmentTaskIsolationSQLInvariants|TestP4AssessmentTriggerUsesBindingRowLock|TestAgentFixExternalDoneUsesStatusMappingInputs|TestTriggerP4AssessmentForDoneBinding'
cd server && go test ./internal/service -run 'TestP4Evidence|TestP4Assessment|TestParseP4Assessment|TestFeishuProjectExternalFieldsKeepsSubmitRecord'
cd server && go test -c ./cmd/server -o /tmp/multica-cmd-server.test
git diff --check
```

本次 binding-id review API 阶段新增通过：

```bash
make sqlc
corepack pnpm --filter @multica/core exec vitest run api/client.test.ts api/schemas.test.ts
corepack pnpm --filter @multica/views exec vitest run dashboard/components/operations-page.test.tsx issues/components/p4-assessment-entry.test.tsx
cd server && go test ./internal/service -run 'TestAgentFixReviewByBindingUsesBindingAsWriteSpine|TestP4AssessmentTaskIsolationSQLInvariants|TestP4AssessmentBackfillScansBindingsWithStatusMapping|TestP4Evidence'
corepack pnpm --filter @multica/core typecheck
corepack pnpm --filter @multica/views typecheck
```

本次清理阶段新增通过：

```bash
corepack pnpm --filter @multica/views exec vitest run dashboard/components/operations-page.test.tsx issues/components/p4-assessment-entry.test.tsx
corepack pnpm --filter @multica/views typecheck
```

本次 UI / Export follow-up 阶段新增通过：

```bash
corepack pnpm --filter @multica/core exec vitest run api/schemas.test.ts
corepack pnpm --filter @multica/views exec vitest run dashboard/components/operations-page.test.tsx
corepack pnpm --filter @multica/views exec vitest run issues/components/p4-assessment-entry.test.tsx
corepack pnpm --filter @multica/core exec vitest run api/client.test.ts api/schemas.test.ts
corepack pnpm --filter @multica/views exec vitest run locales/parity.test.ts
corepack pnpm --filter @multica/core typecheck
corepack pnpm --filter @multica/views typecheck
git diff --check
```

本次 Agent skill / P4 evidence persistence 阶段新增通过：

```bash
cd server && go test ./internal/service -run TestAgentFixP4AssessmentSkillCoversReadOnlyAssessmentContract
cd server && go test ./internal/daemon -run TestBuildP4AssessmentPrompt
cd server && go test ./internal/service -run 'Test.*Skill'
cd server && go test ./internal/daemon
make sqlc
cd server && go test ./internal/service -run TestP4EvidenceReviewProjectionKeepsSwarmAndCLFields
cd server && go test ./internal/service -run 'TestParseP4Assessment|TestP4EvidenceReviewProjectionKeepsSwarmAndCLFields|TestAgentFixP4AssessmentSkillCoversReadOnlyAssessmentContract'
cd server && go test ./internal/service -run 'TestP4Evidence|TestParseP4Assessment|TestAgentFixP4AssessmentSkillCoversReadOnlyAssessmentContract'
cd server && go test ./internal/service -run 'TestOperationsFeedUsesBindingSpineWithoutAssessmentTaskPollution|TestAgentFixExternalDoneUsesStatusMappingInputs|TestP4AssessmentTaskIsolationSQLInvariants|TestP4Evidence|TestParseP4Assessment'
cd server && go test -c ./internal/handler -o /tmp/multica-handler.test
corepack pnpm --filter @multica/core exec vitest run api/schemas.test.ts api/client.test.ts
git diff --check
```

新增/补充覆盖：

- CSV 纯函数测试：BOM、稳定表头、CSV 转义、数组字段、稀疏旧 rows。
- Operations DOM 测试：先按 workstream 筛选，再点击 `Export CSV`，确认导出内容只包含当前筛选 rows。
- Operations DOM 测试：done binding 行展示 `Run assessment`，非 done 行不展示；点击 `Run assessment` 调用 `{ binding_id, force: false }`；completed 行 `Rerun assessment` 调用 `{ binding_id, force: true }`。
- Core schema/client 测试：`external.binding_id` 保留；`POST /api/operations/agent-fixes/p4-assessments` response 走 zod parseWithFallback；client 使用 `binding_id` 和 `force` 请求体。
- Core schema/client 测试：human review response 走 zod `parseWithFallback` 并覆盖 schema drift；client 优先调用 binding-id `PATCH`，缺 binding id 时 fallback 旧 issue-id `PUT`。
- P4 assessment scanner 结构测试：历史补跑 SQL 从 binding 出发，使用 status mapping 输入，不读取 metadata/demo/title 作为触发信号。
- Evidence API 深度 DB 证据测试：task/comment 查询必须按 workspace + issue 限定并限制 20 条；task 查询只返回受控摘要，不选择 `result/context/session_id/work_dir`；service projection 不泄露 raw task internals；Swarm review projection 保留 review id/state/shelved CL/committed CL。
- Built-in skill 结构测试：P4 assessment skill 必须不可用户直接调用，必须包含 evidence API、只读边界、禁止写入边界、CL 角色区分、unknown/warnings 策略和 source-map reference。
- Daemon prompt 测试：assessment prompt 必须明确引用内置 P4 assessment skill，同时不能把 CL 角色判断和完整 schema 细节塞回 prompt。
- P4 evidence persistence / Evidence API 测试：service projection 保留 webhook 已入库的 `changes[]`、`commits[]`、Swarm branch、event type、sent_at 和受限 raw payload；handler 编译测试覆盖新增 sqlc/generated 类型；结构测试固定 agent evidence auth 必须校验 task id、task agent、assessment context type、workspace 和 binding。
- Parser hardening 测试：覆盖纯 JSON、唯一 fenced JSON、prose wrapper、多 fenced block、尾随文本、非法 enum、错误 JSON shape、非整数 CL、越界 confidence。
- Operations feed 主轴结构测试：SQL 必须有 binding 主轴、继续排除 assessment task；handler 必须过滤无普通 task 且 mapped status 非 done 的 binding-only 行；Core schema/client 测试确认 API response 兼容。
- Issues DOM 测试：展示 Operations 共享人工 review outcome；从 Issues 直接打开人工 review 弹窗并通过 Operations mutation 保存；未标注初始状态显示 `Human review` 入口；Issue 缺 P4 metadata 但 assessment feed 能匹配时仍显示入口。
- Operations / Issues DOM 测试：人工 review 保存会把 `external.binding_id` 传入 mutation，优先使用 binding-id 主路径。
- Operations / Issues DOM 测试：共用 `AgentFixReviewDialog` 后，人审保存和 legacy/demo fallback 入口行为保持不变。
- Label picker DOM 测试：覆盖已有 label chip 触发器，避免 Base UI `nativeButton` warning。
- 继续保留 unknown enum 降级展示、analysis report、筛选、review 弹窗相关测试。

验证限制：

- Go handler / cmd server 测试在本地 fixture 初始化阶段失败：测试数据库缺少 `workspace` 表（未迁移/未初始化），不是本次 UI 改动的断言失败。
- P4 webhook / Evidence auth handler 运行态测试同样受本地 handler fixture 缺 `workspace` 表影响；本阶段已用 `go test -c ./internal/handler` 覆盖编译，并保留运行态测试用例/结构测试待可用测试 DB 执行更完整验证。
- `cd server && go test ./internal/service ./cmd/server` 还暴露一个既有失败：`TestResolveAttachmentContentType/log_file_stays_text/plain` 期望 `text/plain`，实际为 `text/x-log; charset=utf-8`。

## Demo 数据

当前本地 demo DB 已 seed 一条可见数据：

- Workspace slug: `test`
- Issue: `P4 assessment demo - visible summary`
- Identifier: `TES-3`
- Agent: `P4 Demo Agent`
- External work item: `P4-DEMO-1`
- Workstream: `server`
- Swarm review: `123`
- AI shelved CL: `1001`
- Swarm change CL: `1002`
- Swarm committed CL: `1003`
- External committed CL: `1004`
- AI attribution prediction: `ai_delivered`
- AI quality prediction: `likely_correct`
- Human review outcome: `needs_changes`
- Eval: `overestimated`

另有三条用于手动体验人工标注流程的未标注样例：

- `TES-4`: `P4 assessment manual review demo - unreviewed AI likely correct`
  - External work item: `P4-DEMO-UNREVIEWED-1`
  - AI attribution prediction: `ai_delivered`
  - AI quality prediction: `likely_correct`
  - Human review outcome: 空，等待用户从 Issues 或 Operations 页面手动填写。
- `TES-5`: `P4 assessment manual review demo - unreviewed needs changes`
  - External work item: `P4-DEMO-UNREVIEWED-2`
  - AI attribution prediction: `ai_assisted`
  - AI quality prediction: `likely_needs_changes`
  - Human review outcome: 空，等待用户从 Issues 或 Operations 页面手动填写。
- `TES-6`: `P4 assessment manual review demo - unreviewed uncertain attribution`
  - External work item: `P4-DEMO-UNREVIEWED-3`
  - AI attribution prediction: `conflict`
  - AI quality prediction: `unknown`
  - Human review outcome: 空，等待用户从 Issues 或 Operations 页面手动填写。

可用测试账号：

```text
email: 13@aa
verification code: 888888
workspace: test
```

## 本地启动方式

当前推荐让后端只作为 Web 的同源代理目标，外部用户只访问 Web。

### Backend

后端端口：

```text
http://localhost:18918
```

关键环境变量：

```bash
DATABASE_URL='postgres://multica:multica@localhost:5432/multica_multica_agent_fix_p4_assessment_v1_838?sslmode=disable'
PORT=18918
JWT_SECRET=change-me-in-production
MULTICA_DEV_VERIFICATION_CODE=888888
FRONTEND_ORIGIN=http://localhost:13838
MULTICA_APP_URL=http://localhost:13838
CORS_ALLOWED_ORIGINS=http://localhost:13838,http://10.1.24.179:13838
```

启动：

```bash
go run ./cmd/server
```

### Web

Web 端口：

```text
http://localhost:13838
http://10.1.24.179:13838
```

推荐启动命令：

```bash
FRONTEND_PORT=13838 \
REMOTE_API_URL=http://localhost:18918 \
NEXT_PUBLIC_API_URL= \
NEXT_PUBLIC_WS_URL= \
COREPACK_HOME=/tmp/corepack \
corepack pnpm --filter @multica/web dev
```

关键点：

- 必须设置 `REMOTE_API_URL=http://localhost:18918`。
- 不要把 `NEXT_PUBLIC_API_URL` 指向后端，否则浏览器会直接跨域访问后端。
- 让浏览器访问 Web，再由 Next rewrite 代理：
  - `/api/*` -> `http://localhost:18918/api/*`
  - `/auth/*` -> `http://localhost:18918/auth/*`
  - `/ws` -> `http://localhost:18918/ws`

## 外部访问方式

对外只给 Web 地址：

```text
http://10.1.24.179:13838/test/operations
```

登录：

```text
email: 13@aa
code: 888888
```

外部用户不需要直接访问 backend `18918`。

如果希望后端不对外暴露，应让后端只监听 `127.0.0.1:18918`，Web 继续监听 `0.0.0.0:13838`。当前验证链路是外部访问 Web，Web 同源代理到后端。

## 部署/调试中遇到的问题

### 1. Web 代理打回自己，导致页面空或 500

现象：

- `/test/operations` 页面返回 200，但登录和 API 失败。
- Next dev server 日志出现类似：

```text
Failed to proxy http://localhost:13838/api/workspaces
Failed to proxy http://localhost:13838/api/operations/agent-fixes
```

原因：

- Web 启动时没有设置 `REMOTE_API_URL=http://localhost:18918`。
- `apps/web/next.config.ts` 的 rewrite 目标回退到了错误地址，导致 `/api/*` 代理回 Web 自己。

处理：

```bash
kill -TERM <old-next-pids>

FRONTEND_PORT=13838 \
REMOTE_API_URL=http://localhost:18918 \
NEXT_PUBLIC_API_URL= \
NEXT_PUBLIC_WS_URL= \
COREPACK_HOME=/tmp/corepack \
corepack pnpm --filter @multica/web dev
```

### 2. API 有数据，但 Operations 页面显示空

现象：

- 后端 `/api/operations/agent-fixes` 返回 demo row。
- Web 页面仍显示空态。
- 浏览器/Next 日志出现：

```text
API response failed schema validation: GET /api/operations/agent-fixes
invalid_type expected string received number
```

原因：

- `p4_assessment.swarm_reviews[].id` 真实返回数字。
- 前端 schema 原先只接受 string。
- `parseWithFallback` 按 API 边界规则返回 fallback `[]`，所以 UI 没数据。

处理：

- `AgentFixSwarmReviewSchema.id` 改成 `string | number`。
- `AgentFixSwarmReview.id` 类型同步。
- 增加 schema regression test。

### 3. Playwright 无法截图

现象：

```text
browserType.launch: Executable doesn't exist
Please run: npx playwright install
```

原因：

- repo 有 Playwright 包，但本机未下载浏览器二进制。

当前处理：

- 没有下载新依赖。
- 使用 Next dev server browser logs、curl、后端日志验证。

## 快速验证命令

页面入口：

```bash
curl -sS -o /tmp/multica_ops.html -w '%{http_code}\n' \
  http://10.1.24.179:13838/test/operations
```

通过 Web 同源代理验证 operations API：

```bash
curl -sS -c /tmp/multica_demo.cookies \
  -H 'content-type: application/json' \
  -d '{"email":"13@aa"}' \
  http://10.1.24.179:13838/auth/send-code

curl -sS -b /tmp/multica_demo.cookies -c /tmp/multica_demo.cookies \
  -H 'content-type: application/json' \
  -d '{"email":"13@aa","code":"888888"}' \
  http://10.1.24.179:13838/auth/verify-code

curl -sS -b /tmp/multica_demo.cookies \
  -H 'x-workspace-slug: test' \
  'http://10.1.24.179:13838/api/operations/agent-fixes?days=30'
```

期望返回包含：

```text
P4 assessment demo - visible summary
P4-DEMO-1
ai_delivered
likely_correct
needs_changes
overestimated
```

## 已跑验证

```bash
./node_modules/.bin/vitest run packages/views/dashboard/components/operations-page.test.tsx --environment jsdom
./node_modules/.bin/vitest run packages/core/api/schemas.test.ts
./node_modules/.bin/tsc --noEmit -p packages/views/tsconfig.json
./node_modules/.bin/tsc --noEmit -p packages/core/tsconfig.json
corepack pnpm --filter @multica/core exec vitest run api/schemas.test.ts api/client.test.ts
corepack pnpm --filter @multica/views exec vitest run dashboard/components/operations-page.test.tsx locales/parity.test.ts
corepack pnpm --filter @multica/core typecheck
corepack pnpm --filter @multica/views typecheck
git diff --check
```

当前结果：

- core schema test: pass
- core API client test: pass
- core typecheck: pass
- operations page test: pass
- locale parity test: pass
- views typecheck: pass
