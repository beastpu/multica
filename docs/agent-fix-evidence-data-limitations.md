---
type: design
status: draft
module: operations
created: 2026-06-28
---

# AI 修单 P4/Swarm Evidence 数据与限制

> 主方案：`docs/agent-fix-prefill-flow.md`
> 实施计划：`docs/agent-fix-p4-assessment-plan.md`
> 外部依赖：`docs/agent-fix-p4-external-dependencies.md`

## 背景

第一版 AI 修单看板只处理 P4/Swarm，不接 GitHub PR。AI assessment 使用本页列出的数据做前置判断，但这些数据只是 evidence，不是最终业务事实。

最终事实仍然是人工验收 `agent_fix_review.outcome/reasons/note`。飞书/Meego 外部工单状态映射为 `done` 是唯一统计分母。

## 数据分层

| 层级 | 用途 | 是否最终事实 |
|---|---|---|
| 飞书/Meego 状态 | 判断是否进入统计分母 | 是，仅限“是否 done” |
| Multica issue/task/comment | 找 AI 修单过程、agent 产物、失败原因 | 否 |
| P4 CL | 查 shelved/submitted CL、提交人、提交时间、文件路径 | 否 |
| Swarm review | 查 review state、changes、commits、review 链路 | 否 |
| AI assessment | 机器前置判断、原因、置信度 | 否 |
| 人工 review | 通过/需修改/拒绝/不适用等最终验收 | 是 |

## 需要的数据

### 飞书/Meego 工单

| 字段 | 目的 | 当前来源 | 可靠性 | 第一版策略 |
|---|---|---|---|---|
| `work_item_id` / `external_identifier` | 和 Multica issue 绑定 | `feishu_project_issue_binding`、issue description | 高 | 必需 |
| `external_status_label` | 判断外部状态 | `feishu_project_issue_binding` | 高 | 配合 status mapping 派生是否 done |
| status mapping | 判断统计分母 | `feishu_project_integration.work_item_types[].status_mapping` | 高 | 只有映射到 `done` 才进入统计 |
| `external_url` | 跳转外部工单 | `feishu_project_issue_binding`、issue description | 高 | 展示/导出 |
| `提交记录` | 候选 final CL 来源 | Feishu Project field `field_6e908d` | 中 | 优先作为 `external_committed_cls` 来源，需验证真实 done 样本 |
| `开发分支` | workstream/分支辅助 | Feishu Project field `field_d7788a` | 中 | 作为 evidence，不单独决定 workstream |
| 飞书评论/动态 | 候选 final CL 来源 | 当前 Multica 未接入 | 未知 | 第一版不强依赖；未验证前只作为后续增强 |

### Multica 内部数据

| 字段 | 目的 | 当前来源 | 可靠性 | 第一版策略 |
|---|---|---|---|---|
| `issue.id` / `issue.identifier` | 主体关联 | `issue` | 高 | 必需 |
| `issue.status` | 内部工作流展示 | `issue` | 中 | 不作为统计完成事实 |
| `issue.description` | 抽取 External-Id、External-Url、历史线索 | `issue` | 中 | 可作为 fallback |
| `issue.metadata.flow_cl` | AI 产物 CL 线索 | `.multica/bugfix/post_fix.py` 写入 | 中 | 优先用于候选 CL，老任务可能缺失 |
| `issue.metadata.flow_review` | Swarm review 线索 | `.multica/bugfix/post_fix.py` 写入 | 中 | 优先用于候选 review，老任务可能缺失 |
| `agent_task_queue.status` | agent 运行状态 | `agent_task_queue` | 高 | 仅代表任务是否跑完 |
| task result/error | 抽取 CL、失败原因 | `agent_task_queue` | 中 | regex/fallback evidence |
| agent comment | 抽取 `Shelved CL`、`Swarm Review` | `comment` | 中 | 重要 fallback |

### P4 / Swarm 数据

| 字段 | 目的 | 当前来源 | 可靠性 | 第一版策略 |
|---|---|---|---|---|
| AI shelved CL | 判断 AI 是否产出代码 | Multica metadata/comment/task result + `p4 describe -S` | 中 | 多来源合并，保留 evidence |
| CL owner/user/client | 区分 AI CL、Swarm companion CL、人工 CL | `p4 change -o` / `p4 describe` | 高 | 结构化保存 |
| CL status | pending/submitted 判断 | `p4 change -o` / `p4 describe` | 高 | 结构化保存 |
| CL description | 关联 WAR/BUG、提取说明 | P4 CLI | 中 | 辅助关联，不单独强判 |
| CL files/depot path | 推导 workstream | P4 CLI | 中 | workstream 结构化列，保留原始 paths |
| Swarm review id | 关联 review | Multica comment/metadata、Swarm API | 中 | 多来源合并 |
| Swarm state | review 当前状态 | Swarm API / webhook | 高 | 原样保存，如 `needsReview` |
| Swarm changes | review 下所有 changes | Swarm API | 高 | 数组保存，注意含 companion CL |
| Swarm commits | review 最终提交 CL | Swarm API / webhook | 高 | 优先保存完整数组 |
| `perforce_review.committed_cl` | webhook 给出的 committed CL | Multica DB | 中 | 可作为 submitted evidence，但当前只存单个 CL |

## 已确认样本

### WAR-7392 / BUG-7008945446

- Multica issue description 有 `External-Id: BUG-7008945446` 和外部 URL。
- agent run completed，产出 pending P4 CL `280825`。
- 没有 Swarm review。
- 没有 final CL。
- 这是第一版必须支持的形态：有 AI P4 evidence，但 P4/Swarm 交付链路未闭合。

### WAR-7512 / BUG-7010257927

- agent comment 有 `Shelved CL: 267639` 和 Swarm review `267641`。
- Swarm review `267641` 返回：
  - `state = needsReview`
  - `changes = [267639, 267642]`
  - `commits = []`
  - `author = svr_ci`
- `267642` 是 Swarm companion pending CL，user 为 `swarm`，不能当作 AI 原始 shelve。

### 生产 Feishu Project 字段

- `project_key = 6718cd2a1d3fdf1b50810683`
- work item type `issue`，identifier prefix `BUG`
- 关键字段：
  - `field_6e908d`：`提交记录`，`text`
  - `field_d7788a`：`开发分支（QA不用手动改，这个字段QA不用维护）`，`multi_select`
- 当前 Multica API 能查字段元数据和按飞书工单反查 issue。
- 当前 Multica API 没有暴露 raw binding/external fields，也没有 Feishu Project 评论/动态代理接口。

## 关联规则

### 输入主轴

第一版以 `(workspace_id, feishu_binding_id)` 为主轴：

- `agent_fix_p4_assessment` 唯一键：`workspace_id + feishu_binding_id`
- `agent_fix_review` 唯一键：`workspace_id + feishu_binding_id`
- `task_id` 只是 evidence 之一，不能作为 review 主键。
- `agent_task_queue.context.type = "agent_fix_p4_assessment"` 的 task 只是 assessment 执行载体，不能继承普通 issue 修单 task 的 session resume、latest run、completion comment fallback、manual rerun 等语义。
- Issues/Operations 判断是否存在真实 P4 assessment 时，以 `agent_fix_p4_assessment` / `agent_fix_review` 记录为主；`issue.metadata.p4_assessment`、`metadata.demo`、title 关键词只允许作为 demo/兼容兜底。

### 候选 CL 提取优先级

1. `issue.metadata.flow_cl`
2. agent comment 中的 `Shelved CL`
3. task result/error 中的 CL 文本
4. P4/Swarm webhook 中绑定到 issue 的 review
5. CL description 中命中的 `WAR-*` / `BUG-*`
6. 时间窗口、提交人/client 等弱关联

### final CL 提取优先级

1. 飞书 `提交记录` 字段解析出的 CL。
2. Swarm `commits[]` 或 webhook `committed_cl`。
3. P4 submitted CL description 中明确命中同一 `WAR-*` / `BUG-*`。
4. 飞书评论/动态中的 CL，前提是官方接口验证可用。
5. 人工 review 手动确认。

如果没有可靠 final CL，`external_committed_cls=[]`，标记 `missing_external_cl`，AI attribution 不能强判 `ai_delivered`。

## 限制

### 飞书/Meego

- 当前同步没有把 raw work item fields 完整暴露给 agent/API。
- 当前只确认字段元数据存在 `提交记录`，还没确认真实 done 工单里该字段的内容格式和稳定性。
- 当前 `external_fields` 落库白名单只保留 `提交分支` / `开发分支`；虽然 OpenAPI parser 已收集 field key 和 display name，仍需扩展字段白名单，把 `提交记录` / `field_6e908d` 落库。
- 当前 Multica 没有接入 Feishu Project 评论/动态接口。
- 如果 final CL 只存在飞书评论里，第一版不能稳定自动拿到。
- 飞书状态回退时，已有 assessment/review 保留，但当前统计应显示为 `not_in_stats` 或 stale。

### Multica

- `issue.status` 是内部状态，不等于外部完成。
- `agent_task_queue.status=completed` 只说明 agent 跑完，不说明修复正确。
- P4 assessment task 必须带 `context.type = "agent_fix_p4_assessment"`，普通 issue task 查询和统计要显式排除它。
- assessment executor 第一版默认取 issue 当前 assignee agent；无可用 agent/runtime 时保留 candidate，不静默改用其他 agent。
- Feishu sync 的 `done/cancelled` 终态取消逻辑必须排除 assessment task；当前 `CancelAgentTasksByIssue` 会取消 issue 下所有 active task，直接复用会误杀 assessment。
- 普通修单 task dedup 查询必须排除 assessment task，避免 assessment 执行载体阻塞真正的修单任务。
- 老任务可能没有 `flow_cl/flow_review` metadata。
- 当前 issue detail API 不暴露 raw binding/status mapping 给 agent。

### Evidence API / auth

- 用户访问 evidence 必须是当前 workspace member，且 binding 属于该 workspace。
- agent 访问 evidence 必须使用 task-scoped token，只能读取当前 task context 中的 `workspace_id + feishu_binding_id`。
- evidence API 不暴露 Feishu plugin secret、P4 ticket、Swarm token 等密钥。
- cross-workspace 或 cross-binding 访问必须拒绝，不能因为返回证据空对象而泄露目标存在性。

### P4 / Swarm

- Swarm state 没有 `committed` 伪状态；提交状态要看 `commits[]` 或 committed CL。
- Swarm `changes[]` 可能包含 companion CL，不能直接等于 AI CL。
- 现有 Multica webhook 入库的 `perforce_review` 只保存单个 `shelved_cl/committed_cl`，会丢多 CL 细节。
- 如果人工绕过 AI Swarm review 直接提交另一个 CL，Swarm webhook 不一定能把它和 AI evidence 串起来。
- workstream 可以从 depot path 推导，但需要保留原始 paths 作为 evidence。

### AI assessment

- AI 不写最终 review。
- AI 不改 `issue.status`。
- AI 不改飞书状态。
- AI 不改 P4/Swarm。
- parser 只处理 `context.type = "agent_fix_p4_assessment"` 的 task result。
- 第一版 parser 从现有 `agent_task_queue.result.output` 读取 agent 输出，不改 daemon complete 协议。
- agent 输出必须是单个 JSON object 或唯一明确的 fenced `json` block；找不到 JSON 或字段类型非法时，assessment 写 `failed` 和 warning，而不是猜测。
- unknown、缺字段、多 CL、多 Swarm review、失败/取消/重跑必须保留为结构化 evidence，不应静默丢弃。

## 第一版处理策略

- 统计分母只看飞书/Meego 状态映射为 `done`。
- assessment/review 主键都挂 `(workspace_id, feishu_binding_id)`。
- 人工 review 第一版只保留最新版，不保存历史。
- final CL 缺失时，不强行推 `ai_delivered`。
- `unknown` / `not_applicable` 不计入 AI 判断准确率分母。
- 第一版接入单条 trigger API 和 Feishu sync worker：binding upsert 成功且 mapped local status 为 `done` 后，best-effort 调用同一个 trigger service。
- Feishu sync 自动触发只做首次 `force=false` 创建；失败/重跑先走手动 `force=true`。
- 历史 done binding 补跑由后续 scanner 或手动 trigger 覆盖，不依赖单个 sync item 发生变化。
- trigger 使用 `binding_id` 为主输入；`issue_id` 只能作为兼容别名，且多 binding 时必须拒绝。
- `evidence` 使用 JSONB 保存原始数据；高频筛选、导出和图表字段做结构化列。
- `external_committed_cls`、`ai_shelved_cls`、`swarm_change_cls`、`swarm_committed_cls` 使用 `INTEGER[]`。
- `workstream` 做结构化列。

## 待验证清单

- 找一个真实 done 工单，验证 `提交记录` 字段内容格式。
- 确认 `field_6e908d` 是否所有相关项目稳定存在，还是只在 warpath3 当前空间存在。
- 在服务端持有 `plugin_secret` 的环境中验证 Feishu Project 评论/动态 OpenAPI。
- 验证 Swarm webhook payload 是否能提供完整 `review.commits[]`，并评估是否需要扩展 DB。
- 找一个“AI shelve 被人工接续提交”的样本，校准 `ai_assisted` / `conflict` 判断。
- 找一个“飞书 done 但没有 AI task”的样本，校准 `human_delivered` / `unattributed` 判断。
