# AI 修单 P4/Swarm Assessment 实施计划

> Status: Draft
> Owner: TBD
> Last updated: 2026-06-28
> Design: `docs/agent-fix-prefill-flow.md`
> Data limits: `docs/agent-fix-evidence-data-limitations.md`
> External dependencies: `docs/agent-fix-p4-external-dependencies.md`

## TL;DR

- 第一版只做 P4/Swarm，不接 GitHub PR。
- 飞书/Meego 外部工单映射为 `done` 是唯一统计分母。
- AI assessment 要输出自己的前置判断：归因预测、质量预测、原因、置信度、P4 evidence。
- 人工验收是最终事实：`outcome + reasons + note`。
- 判断偏差由 AI 前置判断和人工验收动态派生，不人工编辑。
- `/operations` 继续承载明细、人工验收和分析报告；图表放在现有“分析报告”页签，不新增 tab。
- `/issues` 只做轻量融合展示，不新增 AI 修单 tab。
- 真实主流程从外部 done binding 出发；Operations 单条按钮只是手动补跑入口，不是事实来源。
- `metadata.p4_assessment` 只作为 demo/兼容兜底；`agent_fix_p4_assessment` 记录才是 assessment 存在性的事实来源。
- 第一版先打通最小闭环：binding done candidate -> trigger assessment task -> evidence -> parser 写 assessment -> UI 显示 Human review。

## 已确认产品决策

1. **P4/Swarm-only**
   - 处理飞书工单、Multica issue/task/comment、P4 Swarm review、shelved CL、committed CL。
   - 不读取、不展示、不推导 GitHub PR。

2. **AI assessment 不是单纯预填**
   - AI 要给出前置判断。
   - AI 判断要落库，用于后续分析“AI 判断是否准确”。
   - AI 判断不是最终事实。

3. **人工验收是最终事实**
   - `/operations` 和 `/issues` 写同一份 review。
   - 不拆成两套语义。

4. **判断偏差动态派生**
   - 例如 `likely_correct + accepted = 判断准确`。
   - `likely_correct + needs_changes/rejected = AI 高估`。
   - `likely_wrong + accepted = AI 低估`。
   - `unknown/not_applicable` 不计入准确率分母。

5. **P4 evidence 需要包含 workstream**
   - 展示：workstream / Swarm review / AI shelve / final CL。
   - 导出也要包含 workstream。

6. **分析图表放在“分析报告”页签**
   - 不新增“图表”页签。
   - 分析报告同时展示 KPI、图表、排行和项目表。

7. **真实主轴是外部 done binding**
   - `feishu_project_issue_binding` 的状态经 integration status mapping 映射为 `done` 后，才进入 P4 assessment 候选池。
   - `agent_fix_p4_assessment` / `agent_fix_review` 都以 `workspace_id + feishu_binding_id` 为业务唯一键。
   - `issue_id` 是展示和兼容入口，不是 assessment/review 的主键语义。

8. **assessment task 与普通 issue task 隔离**
   - P4 assessment task 复用 `agent_task_queue`，但必须带 `context.type = "agent_fix_p4_assessment"`。
   - 普通 issue run 的 latest feed、session resume、completion comment fallback、rerun 语义不得把 assessment task 当作普通修单 task。
   - Assessment agent 不修改 issue、Feishu/Meego、P4，也不写人工 review。

## Phase 0：真实样本确认

目标：用 warpath3 的真实 issue/work item 验证字段形态。

已确认：

- `WAR-7392 / BUG-7008945446`
  - Multica issue description 中有 `External-Id` / `External-Url`。
  - agent run completed，产出 pending P4 CL `280825`。
  - 没有 Swarm review，也没有 final CL。
  - 这是第一版必须支持的真实形态：有 P4 CL evidence，但 P4/Swarm 交付链路未闭合。
- `WAR-7512 / BUG-7010257927`
  - agent 评论记录 `Shelved CL: 267639`、Swarm review `267641`。
  - Swarm review API 返回 `state=needsReview`、`changes=[267639,267642]`、`commits=[]`。
  - `267642` 是 Swarm companion pending CL，owner/user 为 `swarm`，不是 AI 原始 shelve。
- 生产 workspace Feishu Project integration：
  - `project_key = 6718cd2a1d3fdf1b50810683`，work item type `issue`，标识前缀 `BUG`。
  - 字段元数据中存在 `field_6e908d` / `提交记录` / `text`。
  - 字段元数据中存在 `field_d7788a` / `开发分支（QA不用手动改，这个字段QA不用维护）` / `multi_select`。
  - 当前 Multica API 能读取字段元数据和按飞书工单反查 issue，但没有暴露 raw binding/external fields，也没有 Feishu Project 评论/动态代理接口。

已确认的数据结构决策：

- Swarm review state 保存原始 Swarm state，不使用 `committed` 作为伪 state。
- 提交状态由 `commits` / committed CL 派生。
- `ai_shelved_cls` 与 `swarm_reviews[].changes` 是不同概念；后者可能包含 Swarm companion CL。
- `workstream` 必须做结构化列。
- CL 列表用 `INTEGER[]` 存储，raw evidence 继续放 JSONB。

仍待确认：

- 确认 `field_6e908d / 提交记录` 在真实 done 工单里的内容格式，以及是否稳定承载最终 CL。
- 确认是否需要扩展 `feishu_project_issue_binding.external_fields` 提取逻辑，把 `提交记录` 和 raw work item fields 暴露给 evidence API。
- 确认 Feishu Project 官方评论/动态接口是否可用；当前 Multica API 未接入该能力。
- 确认“Multica 有 AI shelve，但飞书最终 CL 是别的 CL”的真实数据形态。
- 确认哪些样本没有 Swarm review、只有 final CL。

产出：

- 外部字段 key 映射表。
- P4 evidence sample JSON。
- 第一版 reason/prediction 枚举是否需要补充。

## Phase 1：后端存储

新增 migration：

- `agent_fix_p4_assessment`
- `agent_fix_review`

关键字段：

- `agent_fix_p4_assessment`
  - `workspace_id`
  - `issue_id`
  - `feishu_binding_id`
  - `assessment_task_id`
  - `assessment_status`
  - `delivery_attribution_prediction`
  - `quality_prediction`
  - `prediction_reasons`
  - `confidence`
  - `workstream`
  - `swarm_reviews`
  - `ai_shelved_cls`
  - `swarm_change_cls`
  - `swarm_committed_cls`
  - `external_committed_cls`
  - `evidence`
  - `summary`
  - `warnings`
  - `model`
  - `prompt_version`
  - `assessed_at`

- `agent_fix_review`
  - `workspace_id`
  - `issue_id`
  - `feishu_binding_id`
  - `p4_assessment_id`
  - `outcome`
  - `reasons`
  - `note`
  - `reviewer_id`
  - `reviewed_at`

注意：

- review 主键语义按 `workspace_id + feishu_binding_id`，不是单纯 `task_id`。
- assessment 可引用 `assessment_task_id`，但统计主轴仍是外部完成 binding。
- 人工 review 第一版只保留最新值，不做 history/audit 表；后续有审计需求再新增历史表。
- `agent_fix_p4_assessment.workstream` 是结构化列。
- `ai_shelved_cls`、`swarm_change_cls`、`swarm_committed_cls`、`external_committed_cls` 用 `INTEGER[]`，不要只塞 JSONB。

验证：

- migration up/down。
- sqlc 生成。
- Go 单测覆盖 upsert、唯一约束、workspace 隔离。

## Phase 1.5：最小闭环后端服务边界

目标：先打通真实后端流程，但不把 Operations 页面按钮设计成主流程。

现有 Feishu sync 已同步的信息：

- issue：标题、描述、内部状态、优先级、assignee、project。
- binding：外部工单 ID、外部 URL、外部原始状态、外部更新时间、`external_fields`。
- 附件：下载/上传并把 markdown 合入 issue description。
- labels：按 label sync rules 创建、绑定和解绑。
- subscriber：确保外部 assignee 订阅 issue。
- 普通 agent task 联动：外部状态映射到 `done/cancelled` 后会取消该 issue 的 active task；非终态且有 agent assignee 时会按需创建普通修单 task。

当前缺口：

- `external_fields` 解析已从 Feishu OpenAPI `fields/multi_texts` 收集 field key 和中文 display name，但落库白名单目前只保留 `提交分支` / `开发分支`，还没有保留 `提交记录`。
- P4 assessment 自动触发还没有接入 Feishu sync worker。
- `CancelAgentTasksByIssue` 当前会取消 issue 下所有 active task；实现 assessment task 后必须排除 `context.type = "agent_fix_p4_assessment"`，否则 done sync 会误取消 assessment task。

新增服务边界：

- `AssessmentCandidateService`
  - 输入：`workspace_id + feishu_binding_id`。
  - 校验 binding 属于当前 workspace，issue 存在，external status 经 integration status mapping 映射为 `done`。
  - 输出：eligible / not eligible + reason。
- `AssessmentService.Trigger(binding_id, force)`
  - 幂等创建或刷新 `agent_fix_p4_assessment`。
  - 幂等创建 assessment task。
  - 可被手动 API 调用；后续也可被 Feishu sync worker 或定时 scanner 调用。
- `EvidenceService.Get(binding_id)`
  - 聚合只读证据，不暴露 Feishu/P4/Swarm 密钥。
- `AssessmentResultParser`
  - 只处理 `context.type = "agent_fix_p4_assessment"` 的 completed task。
  - 校验 agent 输出 JSON 后写入 `agent_fix_p4_assessment`。

第一版触发策略：

- 手动 trigger API 和 Feishu sync 自动触发都调用同一个 `AssessmentService.Trigger`。
- Feishu sync 自动触发接在 binding upsert 成功之后，且必须是 best-effort：trigger 失败只记录 warning，不影响 Feishu sync 的 created/updated/skipped/error 结果。
- 自动触发只负责首次创建 assessment task；`failed/stale/completed` 的重跑第一版走手动 `force=true`，避免 worker 每 5 分钟重复烧 agent。
- 历史 done binding 的补跑不塞进 `syncWorkItem`，后续由 scanner 或手动 trigger 覆盖。
- 外部状态从 done 回退时，不删除 assessment/review，Operations 显示 `not_in_stats/out_of_scope`。

executor agent 选择：

- 第一版默认使用 issue 当前 assignee agent。
- 若 issue 无 assignee、assignee 不是 agent、agent archived、agent 无 runtime，则不创建 task，返回不可触发原因，并可将 assessment 标记为 `failed` 或保留为 pending candidate。
- 不使用“最近修单 agent”作为默认 executor，避免权限和成本归属不清。

## Phase 2：后端 Query / API

新增或扩展：

- `GET /api/operations/agent-fixes`
  - 逐步从“最新 agent task 主轴”扩展到“外部完成 binding 主轴”。
  - 主查询应从 `feishu_project_issue_binding` 的 mapped done candidate 起步，latest task 只是 evidence/display 字段。
  - 第一版可以保留旧 task feed 兼容，但必须显式排除 `context.type = "agent_fix_p4_assessment"` 的 assessment task，避免污染普通 run。
  - 第一版可兼容原字段，同时增加 `external`、`latest_task`、`p4_assessment`、`human_review`、`display_result_status`、`ai_judgement_eval`。

- Feishu sync worker integration
  - `FeishuProjectSyncService` 持有可选 `AssessmentService`。
  - `syncWorkItem` 每次成功 upsert binding 后，若 mapped local status 为 `done`，best-effort 调用 `AssessmentService.Trigger(binding_id, force=false, source="feishu_sync")`。
  - 调用顺序必须避开普通 task cancel：先完成 issue/binding upsert 和普通 task reconcile，再触发 assessment；同时 `CancelAgentTasksByIssue` 必须排除 assessment task，防止下一轮 done sync 误杀正在跑的 assessment。
  - no-op watermark short-circuit 不触发 assessment；历史 done 由 scanner/手动补跑。

- `POST /api/operations/agent-fixes/p4-assessments`
  - 单条触发或重跑 AI assessment。
  - 请求主字段为 `binding_id`；`issue_id` 只能作为兼容别名，且必须能唯一 resolve 到当前 workspace 下的 binding，否则返回 409。
  - `force=false` 时，已有 `pending/running/completed` 不重复创建 task；`failed/stale` 可重跑。
  - `force=true` 时，允许对 completed/stale/failed 重跑。

- `GET /api/operations/agent-fixes/{binding_id}/p4-evidence`
  - 给 agent 和 UI 只读 evidence。
  - 返回 binding、integration status mapping、raw external fields、issue、task、comment、P4 change、Swarm review。
  - 不暴露飞书/Swarm/P4 密钥。
  - 用户访问走 workspace member 权限；agent 访问走 task-scoped token，只能读取 task context 中同一个 `workspace_id + feishu_binding_id` 的 evidence。

- `PATCH /api/operations/agent-fixes/{binding_id}/review`
  - 写人工 outcome/reasons/note。
  - 现有 `PUT /api/operations/agent-fixes/{issueId}/review` 可暂留兼容，但真实主接口应迁到 binding_id。

动态派生：

- `external_mapped_status`
- `is_statistical_sample`
- `display_result_status`
- `ai_judgement_eval`

验证：

- handler 单测覆盖权限、workspace 隔离、字段缺失、无 assessment、无 review。
- trigger 单测覆盖 not-done、cross-workspace、pending/running/completed 幂等、failed/stale 重跑、force 重跑、无 executor agent。
- evidence 单测覆盖 user auth 和 task-scoped auth，确认跨 workspace / 跨 binding 读不到数据。
- schema drift 测试：前端 zod schema 对缺字段/新 enum 不崩。

## Phase 3：Agent Task / Evidence / Result Parser

任务：

- 支持 `agent_task_queue.context.type = "agent_fix_p4_assessment"`。
- 新增 assessment 专用 enqueue 路径：创建 issue-bound task 但写入专用 context，且不参与普通 issue task 语义。
- claim/prompt 分支注入 P4 assessment 指令。
- 提供只读 evidence API/CLI。
- 解析 agent 输出 JSON，校验后写入 `agent_fix_p4_assessment`。

普通 task 隔离：

- session resume 查询必须排除 `context.type = "agent_fix_p4_assessment"`。
- Operations latest agent run 必须排除 assessment task。
- task completion 的“没有 agent comment 则补评论”逻辑必须跳过 assessment task。
- manual rerun / issue activity 统计不得把 assessment task 当作普通修单执行结果。
- `CancelAgentTasksByIssue` 和 Feishu sync 的终态取消逻辑必须排除 assessment task；显式取消某个 assessment task 或 agent-level cancel-all 可以仍按现有取消语义执行。
- `HasTaskForIssueAndAgent` 这类普通修单 dedup 查询必须排除 assessment task，避免 assessment task 阻止真正的修单 task 创建。

result envelope：

- 第一版不改 daemon complete 协议；server 已把 complete request 的 `output` 存进 `agent_task_queue.result`。
- assessment prompt 要求 agent 最终输出单个 JSON object；parser 从 `result.output` 读取。
- parser 接受整个 `output` 为 JSON object，或唯一明确的 fenced `json` block。
- parser 不从任意自然语言全文中猜测字段；找不到 JSON object 时写 `assessment_status=failed` 和 parser warning。

AI 输出 schema：

- `delivery_attribution_prediction`
- `quality_prediction`
- `prediction_reasons`
- `confidence`
- `workstream`
- `swarm_reviews`
- `ai_shelved_cls`
- `swarm_change_cls`
- `swarm_committed_cls`
- `external_committed_cls`
- `summary`
- `warnings`

约束：

- agent 不改 issue status。
- agent 不改飞书状态。
- agent 不写人工 review。
- agent 不提交、不修改 P4/Swarm。
- 缺证据时输出 `unknown` 或 warning，不猜。

状态机：

- none + eligible -> create assessment `pending` + create task。
- pending/running -> trigger 返回已有 task，不重复创建。
- completed + `force=false` -> 返回已有 assessment。
- completed + `force=true` -> 新建 task，assessment 回到 `pending`，保留旧 result 在 evidence/raw history 中或由 updated_at 覆盖。
- failed/stale -> 允许新建 task 并回到 `pending`。
- task completed + parser ok -> `completed` + `assessed_at`。
- task completed + parser fail -> `failed` + warning。
- task failed/cancelled -> `failed`，不写人工 review。

验证：

- result parser 单测。
- 失败/取消/重跑 task 状态覆盖。
- 普通 task 隔离单测：assessment task 不影响 latest run、不参与 resume、不触发 completion comment fallback。
- Feishu sync 自动触发单测：done binding upsert 后触发 assessment；trigger 失败不影响 sync result；终态 task cancel 不取消 assessment task；普通 task dedup 不被 assessment task 阻塞。
- external fields 单测：`提交记录` / `field_6e908d` 被落入 `external_fields`，并可被 evidence parser 派生 `external_committed_cls`。
- 缺 Swarm、缺 final CL、多 CL、多 review 样本覆盖。
- `changes[]` 中包含 Swarm companion CL 时，不把 companion CL 误判为 AI shelve。
- 有 pending/shelved CL 但无 Swarm review 时，输出 warning，不判断最终提交成功。
- PICT 决策表覆盖 binding done/not done、assessment 状态、force、task result valid/invalid/failed、cross workspace、missing runtime、missing evidence。

## Phase 4：Core Schema / Query / Mutation

位置：

- `packages/core/api/schemas.ts`
- `packages/core/api/client.ts`
- `packages/core/dashboard/queries.ts`
- `packages/core/dashboard/operations-view-store.ts`

要求：

- React Query 管 server state。
- Zustand 只放筛选、tab、列显隐等 UI state。
- 新 API response 用 zod parseWithFallback，不裸 cast。
- enum drift 必须 fallback，不白屏。

新增类型：

- `P4Assessment`
- `HumanReview`
- `AgentFixP4Record`
- `DisplayResultStatus`
- `AIJudgementEval`

验证：

- schema malformed response 测试。
- query key 包含 `wsId` 和筛选参数。

## Phase 5：Operations 明细 UI

目标：不破坏现有 `/operations` 表格使用习惯，扩展 P4 评估字段。

列：

- Issue / 工单
- 外部状态
- Agent
- P4 证据：workstream、Swarm review、AI shelve、final CL
- AI 归因预判
- AI 质量预判
- 人工验收
- 判断偏差

交互：

- 点击人工验收 badge 打开编辑弹窗。
- 不单独保留“操作”列，减少表格噪音。
- 判断偏差只展示，不可编辑。

筛选：

- 外部已完成
- 待 AI 预判
- AI 预判中
- AI 预判失败
- 归因冲突
- 待人工验收
- 人工 outcome
- agent / project / workstream / 时间范围

验证：

- operations page 测试覆盖筛选、弹窗、保存、空数据、unknown enum。

## Phase 6：Operations 分析报告 UI

目标：在现有“分析报告”页签里接入图表，不新增 tab。

图表栈：

- 复用 `recharts`。
- 复用 `@multica/ui/components/ui/chart`。
- 参考 `packages/views/runtimes/components/charts/*` 和 `packages/views/dashboard/components/dashboard-page.tsx`。

第一版图表：

- 人工结果分布：饼图/环图。
- AI 归因分布：饼图/环图。
- 项目结果堆叠：堆叠柱状图。
- 完成趋势：按天/周展示外部完成、AI 已判断、人工已验收。
- Top reasons：条形排行。
- AI 判断偏差：准确/高估/低估/归因错误排行。

数据来源：

- 第一版可对当前筛选结果前端派生。
- 如果数据量大，再补后端聚合 API。

验证：

- 图表空态。
- 小屏布局。
- 筛选变化后图表同步。
- 不新增图表专用 Zustand server data。

## Phase 7：Issues 轻量融合

目标：不新增 AI 修单 tab，在原卡片/详情融合轻量字段。

展示：

- 外部工单完成态。
- AI P4 预判 badge。
- P4 evidence 摘要：workstream / Swarm / shelve / final CL。
- 人工验收 badge。
- 冲突提示。
- 跳转 `/operations` 对应记录。

约束：

- `/issues` 不承担完整运营分析。
- 不铺一排验收按钮。
- 人工验收入口可以是 badge/dropdown/modal。
- 显示 Human review 的主条件是存在 `agent_fix_p4_assessment` 或 `agent_fix_review` 记录。
- `metadata.demo` / `metadata.p4_assessment` / `metadata.swarm_review` / title 只作为 demo/兼容兜底，不能作为真实流程事实来源。

验证：

- board/list/detail 均不挤压主工作流信息。
- mobile/窄屏文本不溢出。

## Phase 8：导出与报表

导出字段：

- Issue
- 外部工单 ID / URL / 状态
- Project
- Workstream
- Agent
- AI assessment status
- AI attribution prediction
- AI quality prediction
- Confidence
- Swarm review
- AI shelve
- Final CL
- Human outcome
- Reasons
- Note
- Judgement eval

验证：

- 当前筛选结果导出。
- 中文字段 BOM。
- 空字段和数组字段格式稳定。

## Release / 验证顺序

1. 后端 migration + query + parser 单测。
2. API response schema + client tests。
3. 本地 seed/mock 数据验证 operations feed。
4. 用 warpath3 单条真实样本验证 evidence。
5. Operations 明细 UI。
6. 分析报告图表。
7. Issues 轻量融合。
8. 导出。
9. 回归现有 `/operations` 表格和 `/issues` 工作流。

## 非范围

- 第一版不接 GitHub PR。
- 第一版不自动判定人工 outcome。
- 第一版不自动改飞书状态。
- 第一版不自动提交/修改 P4。
- 第一版不新建独立 AI 修单 tab。
- 第一版不引入新的图表库。

## Demo

当前静态 demo：

```text
http://10.1.24.179:8088/agent-fix-board-demo.html
```

文件：

```text
agent-fix-board-demo.html
```

demo 已包含：

- Issues 轻量融合。
- Operations P4 明细。
- 人工验收编辑弹窗。
- 判断偏差派生展示。
- workstream P4 evidence。
- 分析报告图表原型。
